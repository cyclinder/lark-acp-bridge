// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// modelsCacheTTL bounds how often /model spawns a fresh `copilot --acp`
// probe. The probe takes a few seconds (MCP handshake), so repeated /model
// calls within the TTL reuse the cached list.
const modelsCacheTTL = 10 * time.Minute

// listModels probes the real, account-specific model list by starting
// `copilot --acp` and reading the models block from the session/new
// response. Copilot CLI has no non-interactive model-list command; ACP is
// the only machine-readable surface. Results are cached for modelsCacheTTL.
//
// The returned currentID is what `copilot -p` would actually use without
// --model: the persisted `model` from ~/.copilot/settings.json when
// readable, else the session/new response's currentModelId.
func (a *Adapter) ListModels(ctx context.Context) ([]agent.ModelInfo, string, error) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if time.Since(a.modelsFetchedAt) < modelsCacheTTL && len(a.modelsCache) > 0 {
		return a.modelsCache, a.currentModel(), nil
	}
	models, currentID, err := a.probeModels(ctx)
	if err != nil {
		return nil, "", err
	}
	a.modelsCache = models
	a.acpCurrentID = currentID
	a.modelsFetchedAt = time.Now()
	return a.modelsCache, a.currentModel(), nil
}

// currentModel resolves the effective default model for new -p sessions.
func (a *Adapter) currentModel() string {
	if m := readSettingsModel(); m != "" {
		return m
	}
	return a.acpCurrentID
}

// readSettingsModel reads the persisted default model from the Copilot CLI's
// own settings file. Best-effort: any error yields "".
func readSettingsModel() string {
	data, err := os.ReadFile(filepath.Join(copilotHome(), "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ""
	}
	return settings.Model
}

// probeModels performs one ACP handshake against `copilot --acp`.
func (a *Adapter) probeModels(ctx context.Context) ([]agent.ModelInfo, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.binary, "--acp", "--no-color")
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", fmt.Errorf("copilot acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", fmt.Errorf("copilot acp stdout pipe: %w", err)
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start copilot --acp: %w", err)
	}
	// The probe is short-lived; make sure the process never outlives it.
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGKILL)
			}
			<-done
		}
	}()

	responses := make(chan map[string]any, 16)
	scanErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
				responses <- msg
			}
		}
		scanErr <- scanner.Err()
	}()

	rpc := func(id int, method string, params any) error {
		line, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		})
		if err != nil {
			return err
		}
		_, err = stdin.Write(append(line, '\n'))
		return err
	}
	if err := rpc(1, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "lark-acp-bridge", "version": "0.1"},
	}); err != nil {
		return nil, "", fmt.Errorf("copilot acp initialize write: %w", err)
	}
	if err := rpc(2, "session/new", map[string]any{"cwd": os.TempDir(), "mcpServers": []any{}}); err != nil {
		return nil, "", fmt.Errorf("copilot acp session/new write: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, "", fmt.Errorf("copilot acp probe: %w", ctx.Err())
		case err := <-scanErr:
			return nil, "", fmt.Errorf("copilot acp read: %w", err)
		case msg := <-responses:
			if id, _ := msg["id"].(float64); int(id) != 2 {
				continue
			}
			if rpcErr, ok := msg["error"].(map[string]any); ok {
				return nil, "", fmt.Errorf("copilot acp session/new: %s", strVal(rpcErr["message"]))
			}
			result, ok := msg["result"].(map[string]any)
			if !ok {
				return nil, "", errors.New("copilot acp session/new: missing result")
			}
			return parseModelsBlock(result)
		}
	}
}

// parseModelsBlock extracts the model table from a session/new result.
func parseModelsBlock(result map[string]any) ([]agent.ModelInfo, string, error) {
	block, ok := result["models"].(map[string]any)
	if !ok {
		return nil, "", errors.New("copilot acp session/new: no models block")
	}
	currentID := strVal(block["currentModelId"])
	available, _ := block["availableModels"].([]any)
	models := make([]agent.ModelInfo, 0, len(available))
	for _, entry := range available {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		value := strVal(m["modelId"])
		if value == "" {
			continue
		}
		name := strVal(m["name"])
		if name == "" {
			name = value
		}
		models = append(models, agent.ModelInfo{Value: value, Name: name})
	}
	if len(models) == 0 {
		return nil, "", errors.New("copilot acp session/new: empty model list")
	}
	return models, currentID, nil
}

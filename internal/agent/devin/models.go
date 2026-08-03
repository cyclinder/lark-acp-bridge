// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package devin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/agent/devin/acp"
)

// modelsCacheTTL bounds how often /model spawns a fresh `devin acp` probe.
// The probe takes several seconds (session setup), so repeated /model calls
// within the TTL reuse the cached list.
const modelsCacheTTL = 10 * time.Minute

// ListModels implements the commands.ModelLister seam: it enumerates the
// real, account-specific model list that `devin acp` advertises in the
// session/new configOptions (the config option with id/category "model").
// Devin CLI has no non-interactive model-list command; ACP is the only
// machine-readable surface.
//
// Resolution order: TTL cache, then the config options of any live pooled
// session (free), then a short-lived probe process. The returned currentID
// is the session/new response's currentValue for the model option.
func (a *Adapter) ListModels(ctx context.Context) ([]agent.ModelInfo, string, error) {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if time.Since(a.modelsFetchedAt) < modelsCacheTTL && len(a.modelsCache) > 0 {
		return a.modelsCache, a.modelsCurrentID, nil
	}
	if models, currentID, ok := a.modelsFromPool(); ok {
		a.setModelsCache(models, currentID)
		return models, currentID, nil
	}
	models, currentID, err := a.probeModels(ctx)
	if err != nil {
		return nil, "", err
	}
	a.setModelsCache(models, currentID)
	return models, currentID, nil
}

func (a *Adapter) setModelsCache(models []agent.ModelInfo, currentID string) {
	a.modelsCache = models
	a.modelsCurrentID = currentID
	a.modelsFetchedAt = time.Now()
}

// modelsFromPool extracts the model list from any live pooled session's
// config options, avoiding a probe spawn when a session is already warm.
func (a *Adapter) modelsFromPool() ([]agent.ModelInfo, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, sc := range a.pool {
		if !sc.isAlive() {
			continue
		}
		sc.modelMu.Lock()
		opts := sc.modelOpts
		if len(opts) == 0 {
			opts = sc.configOpts
		}
		sc.modelMu.Unlock()
		if models, currentID, ok := modelFromConfigOptions(opts); ok {
			return models, currentID, true
		}
	}
	return nil, "", false
}

// probeModels performs one ACP handshake against a short-lived `devin acp`
// process and reads the model config option from the session/new response.
func (a *Adapter) probeModels(ctx context.Context) ([]agent.ModelInfo, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.binary, "acp")
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", fmt.Errorf("devin acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", fmt.Errorf("devin acp stdout pipe: %w", err)
	}
	// devin acp writes noisy tracing logs to stderr; discard them.
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start devin acp: %w", err)
	}
	// The probe is short-lived; make sure the process never outlives it.
	// devin acp does not exit on stdin EOF, so kill it explicitly.
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	client := acp.NewClient(stdin, stdout)
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(ctx, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo:      acp.ImplementationInfo{Name: "lark-acp-bridge", Version: "0.1.0"},
	}); err != nil {
		return nil, "", fmt.Errorf("devin acp initialize: %w", err)
	}
	sn, err := client.SessionNew(ctx, acp.SessionNewParams{
		Cwd:        os.TempDir(),
		McpServers: []acp.MCPServer{},
	})
	if err != nil {
		return nil, "", fmt.Errorf("devin acp session/new: %w", err)
	}
	if models, currentID, ok := modelFromConfigOptions(sn.ConfigOptions); ok {
		return models, currentID, nil
	}
	return nil, "", errors.New("devin acp session/new: no model config option")
}

// modelFromConfigOptions extracts the model table from a session/new (or
// set_config_option) config option list. The bool reports whether a model
// option with at least one value was found.
func modelFromConfigOptions(opts []acp.ConfigOption) ([]agent.ModelInfo, string, bool) {
	for _, co := range opts {
		if co.ID != "model" && co.Category != "model" {
			continue
		}
		models := make([]agent.ModelInfo, 0, len(co.Options))
		for _, o := range co.Options {
			if o.Value == "" {
				continue
			}
			name := o.Name
			if name == "" {
				name = o.Value
			}
			models = append(models, agent.ModelInfo{Value: o.Value, Name: name})
		}
		if len(models) == 0 {
			return nil, "", false
		}
		return models, co.CurrentValue, true
	}
	return nil, "", false
}

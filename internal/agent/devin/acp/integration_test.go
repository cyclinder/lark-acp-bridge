//go:build integration

// This file contains integration tests that spawn a real `devin acp`
// subprocess. They are gated behind the "integration" build tag so they
// do not run in normal `go test`. Run with:
//
//	go test -tags integration ./internal/agent/devin/acp/ -v -timeout 120s
package acp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startRealDevin spawns a real `devin acp` subprocess and returns a Client
// connected to it. The caller must defer-close both the client and kill the
// process.
func startRealDevin(t *testing.T) (*Client, *exec.Cmd) {
	t.Helper()
	// Global flags before "acp" subcommand.
	cmd := exec.Command("devin", "--permission-mode", "dangerous", "acp")
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("devin not available: %v", err)
	}
	client := NewClient(stdin, stdout)
	return client, cmd
}

func TestRealInitialize(t *testing.T) {
	client, cmd := startRealDevin(t)
	defer func() {
		_ = client.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := client.Initialize(ctx, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ImplementationInfo{Name: "lark-acp-bridge-test", Version: "0.1.0"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", res.ProtocolVersion, ProtocolVersion)
	}
	if !res.AgentCapabilities.LoadSession {
		t.Error("expected loadSession=true")
	}
	t.Logf("agentInfo: %+v", res.AgentInfo)
	t.Logf("sessionCapabilities: %+v", res.AgentCapabilities.SessionCapabilities)
	t.Logf("promptCapabilities: %+v", res.AgentCapabilities.PromptCapabilities)
}

func TestRealSessionNewAndPrompt(t *testing.T) {
	client, cmd := startRealDevin(t)
	defer func() {
		_ = client.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Initialize.
	_, err := client.Initialize(ctx, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ImplementationInfo{Name: "lark-acp-bridge-test", Version: "0.1.0"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Create a session.
	sn, err := client.SessionNew(ctx, SessionNewParams{Cwd: t.TempDir(), McpServers: []MCPServer{}})
	if err != nil {
		t.Fatalf("SessionNew: %v", err)
	}
	if sn.SessionID == "" {
		t.Fatal("empty sessionId")
	}
	t.Logf("sessionId: %s", sn.SessionID)
	t.Logf("configOptions: %d items", len(sn.ConfigOptions))
	for _, co := range sn.ConfigOptions {
		t.Logf("  config: id=%s name=%s current=%s options=%d", co.ID, co.Name, co.CurrentValue, len(co.Options))
	}

	// Send a simple prompt and collect events.
	var textChunks, toolCalls, usages int
	var stopReason string
	promptDone := make(chan error, 1)

	// Collect notifications concurrently while the prompt runs.
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		for msg := range client.Updates {
			ev, ok := ParseUpdate(msg)
			if !ok {
				continue
			}
			switch ev.Kind {
			case EventKindText:
				textChunks++
				t.Logf("text chunk: %q", ev.Text)
			case EventKindToolStart:
				toolCalls++
				t.Logf("tool start: %s", ev.ToolTitle)
			case EventKindUsage:
				usages++
				t.Logf("usage: tokens=%d cost=%.4f", ev.UsedTokens, ev.CostAmount)
			}
		}
	}()

	go func() {
		res, err := client.SessionPrompt(ctx, SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []ContentBlock{{Type: "text", Text: "Reply with exactly: hello world"}},
		})
		if err != nil {
			promptDone <- err
			return
		}
		stopReason = res.StopReason
		promptDone <- nil
	}()

	// Wait for the prompt to complete.
	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatalf("SessionPrompt: %v", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("prompt timed out after 90s")
	}

	// Kill the devin process so stdout EOFs, which stops the read loop,
	// which closes Updates and lets the collector goroutine finish.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	<-collectorDone

	t.Logf("stopReason: %s", stopReason)
	t.Logf("events: text=%d tools=%d usage=%d", textChunks, toolCalls, usages)

	if textChunks == 0 {
		t.Error("expected at least one text chunk")
	}
	if stopReason == "" {
		t.Error("expected non-empty stopReason")
	}
}

func TestRealSetConfigOption(t *testing.T) {
	client, cmd := startRealDevin(t)
	defer func() {
		_ = client.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.Initialize(ctx, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ImplementationInfo{Name: "lark-acp-bridge-test", Version: "0.1.0"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	sn, err := client.SessionNew(ctx, SessionNewParams{Cwd: t.TempDir(), McpServers: []MCPServer{}})
	if err != nil {
		t.Fatalf("SessionNew: %v", err)
	}

	// Find the model config option and try switching.
	var modelOpt *ConfigOption
	for i := range sn.ConfigOptions {
		if sn.ConfigOptions[i].ID == "model" || sn.ConfigOptions[i].Category == "model" {
			modelOpt = &sn.ConfigOptions[i]
			break
		}
	}
	if modelOpt == nil {
		t.Skip("no model config option advertised")
	}
	if len(modelOpt.Options) < 2 {
		t.Skipf("model option has only %d choices, need >=2", len(modelOpt.Options))
	}

	// Pick a different model than the current one.
	target := modelOpt.Options[0].Value
	if target == modelOpt.CurrentValue && len(modelOpt.Options) > 1 {
		target = modelOpt.Options[1].Value
	}
	t.Logf("switching model from %s to %s", modelOpt.CurrentValue, target)

	res, err := client.SetConfigOption(ctx, SetConfigOptionParams{
		SessionID: sn.SessionID,
		ConfigID:  modelOpt.ID,
		Value:     target,
	})
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	// Verify the change took effect.
	for _, co := range res.ConfigOptions {
		if co.ID == modelOpt.ID {
			if co.CurrentValue != target {
				t.Errorf("currentValue = %s, want %s", co.CurrentValue, target)
			} else {
				t.Logf("model switched to %s successfully", co.CurrentValue)
			}
		}
	}
}

func TestRealSessionCancel(t *testing.T) {
	client, cmd := startRealDevin(t)
	defer func() {
		_ = client.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := client.Initialize(ctx, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ImplementationInfo{Name: "lark-acp-bridge-test", Version: "0.1.0"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	sn, err := client.SessionNew(ctx, SessionNewParams{Cwd: t.TempDir(), McpServers: []MCPServer{}})
	if err != nil {
		t.Fatalf("SessionNew: %v", err)
	}

	// Send a prompt that will take a while, then cancel it.
	promptDone := make(chan error, 1)
	var stopReason string
	go func() {
		res, err := client.SessionPrompt(ctx, SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []ContentBlock{{Type: "text", Text: "Write a 1000 word essay about the history of computing."}},
		})
		if err != nil {
			promptDone <- err
			return
		}
		stopReason = res.StopReason
		promptDone <- nil
	}()

	// Wait a moment for the prompt to start, then cancel.
	time.Sleep(2 * time.Second)
	if err := client.SessionCancel(ctx, SessionCancelParams{SessionID: sn.SessionID}); err != nil {
		t.Fatalf("SessionCancel: %v", err)
	}

	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatalf("SessionPrompt after cancel: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("prompt did not return after cancel within 15s")
	}

	t.Logf("stopReason after cancel: %s", stopReason)
	// Some agents return "cancelled", others may return "end_turn" if they
	// finished just before the cancel took effect. Either is acceptable.
	if stopReason != "" && stopReason != StopCancelled && stopReason != StopEndTurn {
		t.Errorf("stopReason = %s, want cancelled or end_turn", stopReason)
	}
}

// TestRealProtocolVersionMismatch verifies the client handles a version
// mismatch gracefully (devin should negotiate down or reject).
func TestRealProtocolVersionMismatch(t *testing.T) {
	client, cmd := startRealDevin(t)
	defer func() {
		_ = client.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Send an absurdly high protocol version.
	res, err := client.Initialize(ctx, InitializeParams{
		ProtocolVersion: 999,
		ClientInfo:      ImplementationInfo{Name: "test", Version: "0.1"},
	})
	if err != nil {
		// Error is acceptable; the agent may reject unsupported versions.
		t.Logf("Initialize with v999 returned error (acceptable): %v", err)
		return
	}
	t.Logf("negotiated protocolVersion: %d (requested 999)", res.ProtocolVersion)
	if res.ProtocolVersion > ProtocolVersion {
		t.Errorf("negotiated version %d > our expected %d", res.ProtocolVersion, ProtocolVersion)
	}
}

func init() {
	// Ensure devin is in PATH; skip if not.
	if _, err := exec.LookPath("devin"); err != nil {
		panic("integration tests require devin in PATH: " + err.Error())
	}
	// Quick sanity: devin --version must succeed.
	out, err := exec.Command("devin", "--version").Output()
	if err != nil {
		panic("devin --version failed: " + err.Error())
	}
	if !strings.Contains(string(out), "devin") {
		panic("devin --version output unexpected: " + string(out))
	}
}

// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package devin

import (
	"context"
	"os/exec"
	"testing"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/agent/devin/acp"
)

func TestModelFromConfigOptions(t *testing.T) {
	tests := []struct {
		name        string
		opts        []acp.ConfigOption
		wantModels  []agent.ModelInfo
		wantCurrent string
		wantOK      bool
	}{
		{
			name: "matched by id",
			opts: []acp.ConfigOption{
				{ID: "mode", Category: "mode", Options: []acp.ConfigOptionVal{{Value: "bypass", Name: "Bypass"}}},
				{ID: "model", Category: "model", CurrentValue: "m2", Options: []acp.ConfigOptionVal{
					{Value: "m1", Name: "Model One"},
					{Value: "m2", Name: "Model Two"},
				}},
			},
			wantModels:  []agent.ModelInfo{{Value: "m1", Name: "Model One"}, {Value: "m2", Name: "Model Two"}},
			wantCurrent: "m2",
			wantOK:      true,
		},
		{
			name: "matched by category only",
			opts: []acp.ConfigOption{
				{ID: "other", Category: "model", CurrentValue: "x", Options: []acp.ConfigOptionVal{{Value: "x", Name: "X"}}},
			},
			wantModels:  []agent.ModelInfo{{Value: "x", Name: "X"}},
			wantCurrent: "x",
			wantOK:      true,
		},
		{
			name: "name falls back to value",
			opts: []acp.ConfigOption{
				{ID: "model", Options: []acp.ConfigOptionVal{{Value: "m1"}}},
			},
			wantModels: []agent.ModelInfo{{Value: "m1", Name: "m1"}},
			wantOK:     true,
		},
		{
			name: "empty values skipped",
			opts: []acp.ConfigOption{
				{ID: "model", Options: []acp.ConfigOptionVal{{Value: ""}, {Value: "m1", Name: "M1"}}},
			},
			wantModels: []agent.ModelInfo{{Value: "m1", Name: "M1"}},
			wantOK:     true,
		},
		{
			name:   "no model option",
			opts:   []acp.ConfigOption{{ID: "mode", Category: "mode", Options: []acp.ConfigOptionVal{{Value: "bypass"}}}},
			wantOK: false,
		},
		{
			name:   "model option with no values",
			opts:   []acp.ConfigOption{{ID: "model", Category: "model"}},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models, current, ok := modelFromConfigOptions(tt.opts)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if current != tt.wantCurrent {
				t.Errorf("current = %q, want %q", current, tt.wantCurrent)
			}
			if len(models) != len(tt.wantModels) {
				t.Fatalf("len(models) = %d, want %d", len(models), len(tt.wantModels))
			}
			for i, m := range models {
				if m != tt.wantModels[i] {
					t.Errorf("models[%d] = %+v, want %+v", i, m, tt.wantModels[i])
				}
			}
		})
	}
}

// TestListModelsServesFromCache verifies that a populated cache short-circuits
// any probing (no subprocess is spawned even though one would fail — the
// binary is pointed at a nonexistent path).
func TestListModelsServesFromCache(t *testing.T) {
	a := New(WithBinary("/nonexistent/devin"))
	a.setModelsCache([]agent.ModelInfo{{Value: "m1", Name: "M1"}}, "m1")
	models, current, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if current != "m1" || len(models) != 1 || models[0].Value != "m1" {
		t.Errorf("got (%+v, %q), want cached m1", models, current)
	}
}

// TestListModelsServesFromPool verifies that ListModels reuses the config
// options of a live pooled session instead of spawning a probe process.
func TestListModelsServesFromPool(t *testing.T) {
	a := New(WithBinary("/nonexistent/devin"))
	// A real (harmless) process so sessionClient.isAlive() returns true.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	sc := &sessionClient{
		scope: "chat_1",
		cmd:   cmd,
		alive: true,
		configOpts: []acp.ConfigOption{
			{ID: "model", Category: "model", CurrentValue: "pool-model", Options: []acp.ConfigOptionVal{
				{Value: "pool-model", Name: "Pool Model"},
			}},
		},
	}
	a.mu.Lock()
	a.pool["chat_1"] = sc
	a.mu.Unlock()

	models, current, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if current != "pool-model" || len(models) != 1 || models[0].Value != "pool-model" {
		t.Errorf("got (%+v, %q), want pool-model from pooled session", models, current)
	}
}

// TestListModelsProbeFailure verifies that a probe error (binary missing, no
// pool, empty cache) is surfaced so the caller falls back to the static table.
func TestListModelsProbeFailure(t *testing.T) {
	a := New(WithBinary("/nonexistent/devin"))
	_, _, err := a.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected probe error for missing binary")
	}
}

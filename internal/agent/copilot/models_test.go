// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package copilot

import (
	"testing"
)

func TestParseModelsBlock(t *testing.T) {
	result := map[string]any{
		"sessionId": "sess_x",
		"models": map[string]any{
			"currentModelId": "claude-sonnet-5",
			"availableModels": []any{
				map[string]any{"modelId": "auto", "name": "Auto"},
				map[string]any{"modelId": "claude-fable-5", "name": "Claude Fable 5"},
				map[string]any{"modelId": "nameless"},
				"not-a-map",
				map[string]any{"name": "no-id"},
			},
		},
	}
	models, currentID, err := parseModelsBlock(result)
	if err != nil {
		t.Fatalf("parseModelsBlock: %v", err)
	}
	if currentID != "claude-sonnet-5" {
		t.Errorf("currentID = %q, want claude-sonnet-5", currentID)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3 (skip malformed entries): %+v", len(models), models)
	}
	if models[0].Value != "auto" || models[0].Name != "Auto" {
		t.Errorf("models[0] = %+v", models[0])
	}
	if models[1].Value != "claude-fable-5" || models[1].Name != "Claude Fable 5" {
		t.Errorf("models[1] = %+v", models[1])
	}
	// Missing name falls back to the model id.
	if models[2].Value != "nameless" || models[2].Name != "nameless" {
		t.Errorf("models[2] = %+v", models[2])
	}
}

func TestParseModelsBlockMissing(t *testing.T) {
	if _, _, err := parseModelsBlock(map[string]any{"sessionId": "x"}); err == nil {
		t.Fatal("expected error for missing models block")
	}
	if _, _, err := parseModelsBlock(map[string]any{
		"models": map[string]any{"availableModels": []any{}},
	}); err == nil {
		t.Fatal("expected error for empty model list")
	}
}

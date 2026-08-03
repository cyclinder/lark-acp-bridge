// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// This file contains integration tests that spawn a real `devin acp`
// subprocess. They are gated behind the "integration" build tag so they
// do not run in normal `go test`. Run with:
//
//	go test -tags integration ./internal/agent/devin/ -v -timeout 120s -run TestSmokeListModels
package devin

import (
	"context"
	"fmt"
	"testing"
)

// TestSmokeListModels probes the real devin acp model list end to end.
func TestSmokeListModels(t *testing.T) {
	a := New()
	if err := a.Available(context.Background()); err != nil {
		t.Skipf("devin not available: %v", err)
	}
	models, currentID, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("empty model list")
	}
	fmt.Printf("current: %s (%d models)\n", currentID, len(models))
	for _, m := range models {
		fmt.Printf("    %s (%s)\n", m.Value, m.Name)
	}
	// Second call must be served from the cache.
	models2, _, err := a.ListModels(context.Background())
	if err != nil || len(models2) != len(models) {
		t.Fatalf("cached ListModels: err=%v len=%d want %d", err, len(models2), len(models))
	}
}

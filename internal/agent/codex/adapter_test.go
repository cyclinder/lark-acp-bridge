// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// fakeBinary writes an executable shell script and returns its path.
func fakeBinary(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func collect(t *testing.T, r agent.Run) []agent.Event {
	t.Helper()
	var evs []agent.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-timeout:
			t.Fatalf("timed out waiting for events; got %+v", evs)
		}
	}
}

func TestRunStaleResumeRetriesFresh(t *testing.T) {
	bin := fakeBinary(t, `case "$*" in
*resume*) cat >/dev/null; echo "Error: conversation not found" >&2; exit 1;;
esac
cat >/dev/null
echo '{"type":"thread.started","thread_id":"fresh-1"}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
`)
	a := New(WithBinary(bin))
	r, err := a.Run(context.Background(), agent.RunOptions{Prompt: "hi", Cwd: t.TempDir(), SessionID: "gone"})
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, r)
	last := evs[len(evs)-1]
	if last.Type != agent.EventDone || last.SessionID != "fresh-1" {
		t.Fatalf("want EventDone with fresh thread id, got %+v", evs)
	}
}

func TestRunFailureIncludesStderr(t *testing.T) {
	bin := fakeBinary(t, `cat >/dev/null; echo "boom: something broke" >&2; exit 1
`)
	a := New(WithBinary(bin))
	r, err := a.Run(context.Background(), agent.RunOptions{Prompt: "hi", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, r)
	last := evs[len(evs)-1]
	if last.Type != agent.EventError || !strings.Contains(last.Err.Error(), "boom: something broke") {
		t.Fatalf("want EventError containing stderr tail, got %+v", evs)
	}
}

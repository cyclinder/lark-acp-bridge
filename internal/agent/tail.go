// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"strings"
	"sync"
)

// TailBuffer is an io.Writer that retains only the last max bytes written.
// Adapters attach it to a subprocess's stderr so a crash message can be
// included in the surfaced error without unbounded memory growth.
type TailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

// NewTailBuffer creates a TailBuffer keeping at most max bytes.
func NewTailBuffer(max int) *TailBuffer { return &TailBuffer{max: max} }

// Write appends p, discarding the oldest bytes beyond the cap.
func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

// String returns the retained bytes with surrounding whitespace trimmed.
func (b *TailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

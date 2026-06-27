// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package log provides a minimal structured logger that writes JSON lines
// to the bridge's log directory. It wraps the standard library log package
// to keep dependencies at zero.
package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	out     io.Writer = os.Stderr
	enabled bool
)

// Init opens the daily log file under home/logs and enables structured
// logging. Until Init is called, logs go to stderr only.
func Init(home string) error {
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("bridge-%s.log", time.Now().Format("20060102"))
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	mu.Lock()
	out = io.MultiWriter(os.Stderr, f)
	enabled = true
	mu.Unlock()
	return nil
}

// Entry is one structured log record.
type Entry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Scope   string `json:"scope"`
	Event   string `json:"event"`
	Message string `json:"message,omitempty"`
}

func write(level, scope, event, msg string) {
	e := Entry{
		Time:  time.Now().UTC().Format(time.RFC3339Nano),
		Level: level,
		Scope: scope,
		Event: event,
	}
	if msg != "" {
		e.Message = msg
	}
	data, _ := json.Marshal(e)
	mu.Lock()
	_, _ = fmt.Fprintln(out, string(data))
	mu.Unlock()
}

// Info logs at info level.
func Info(scope, event string, msg ...string) {
	write("info", scope, event, join(msg))
}

// Warn logs at warn level.
func Warn(scope, event string, msg ...string) {
	write("warn", scope, event, join(msg))
}

// Error logs at error level.
func Error(scope, event string, msg ...string) {
	write("error", scope, event, join(msg))
}

func join(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

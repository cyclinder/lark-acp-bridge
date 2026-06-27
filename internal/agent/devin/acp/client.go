// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Client is a JSON-RPC 2.0 client over a line-delimited stdio transport.
// It owns no subprocess; the caller provides the stdin/stdout pair (typically
// from os/exec.Cmd). One Client serves one ACP connection.
//
// Methods with a return value block until the matching response arrives or
// the context is cancelled. Notifications received from the server are pushed
// to the Updates channel.
type Client struct {
	stdin   io.WriteCloser
	stdout  io.Reader
	writeMu sync.Mutex

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan *JSONRPCMessage
	closed  bool
	closeCh chan struct{}

	// updatesOut is the send side of Updates; readLoop pushes notifications
	// here. Kept unexported so external callers can only receive.
	updatesOut chan<- *JSONRPCMessage

	// Updates delivers server notifications (session/update, etc.) to the
	// caller. Closed when the read loop ends.
	Updates <-chan *JSONRPCMessage
}

// NewClient wraps an already-started ACP subprocess's stdio. The caller is
// responsible for the process lifecycle (start, kill); the Client only owns
// the JSON-RPC framing and request/response correlation.
func NewClient(stdin io.WriteCloser, stdout io.Reader) *Client {
	updates := make(chan *JSONRPCMessage, 64)
	c := &Client{
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan *JSONRPCMessage),
		closeCh: make(chan struct{}),
		Updates: updates,
	}
	// Keep a back-reference to the raw channel so the read loop can push
	// notifications without a type assertion on the read-only Updates.
	c.updatesOut = updates
	go c.readLoop()
	return c
}

// updatesOut is set by NewClient so readLoop can send on the buffered channel
// that Updates exposes read-only. Kept unexported to avoid external sends.

// Initialize performs the ACP initialize handshake.
func (c *Client) Initialize(ctx context.Context, params InitializeParams) (*InitializeResult, error) {
	var res InitializeResult
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SessionNew creates a new session and returns its id plus any config options
// the agent advertises (e.g. the model selector).
func (c *Client) SessionNew(ctx context.Context, params SessionNewParams) (*SessionNewResult, error) {
	var res SessionNewResult
	if err := c.call(ctx, "session/new", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SessionResume reconnects to an existing session without replaying history.
func (c *Client) SessionResume(ctx context.Context, params SessionResumeParams) error {
	var res struct{}
	return c.call(ctx, "session/resume", params, &res)
}

// SessionPrompt sends a prompt and blocks until the turn ends (stopReason
// returned) or the context is cancelled. Notifications emitted during the
// turn are delivered on Updates concurrently.
func (c *Client) SessionPrompt(ctx context.Context, params SessionPromptParams) (*SessionPromptResult, error) {
	var res SessionPromptResult
	if err := c.call(ctx, "session/prompt", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SessionCancel is a notification; it has no response. It aborts the current
// prompt turn. The pending session/prompt call will resolve with
// stopReason "cancelled".
func (c *Client) SessionCancel(ctx context.Context, params SessionCancelParams) error {
	return c.notify(ctx, "session/cancel", params)
}

// SetConfigOption changes a session config option (e.g. model) and returns
// the full, updated config option list.
func (c *Client) SetConfigOption(ctx context.Context, params SetConfigOptionParams) (*SetConfigOptionResult, error) {
	var res SetConfigOptionResult
	if err := c.call(ctx, "session/set_config_option", params, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SessionClose cancels ongoing work for a session and frees its resources.
func (c *Client) SessionClose(ctx context.Context, params SessionCloseParams) error {
	var res struct{}
	return c.call(ctx, "session/close", params, &res)
}

// Close tears down the client. It closes stdin (signalling EOF to the agent)
// and waits for the read loop to drain. Safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	close(c.closeCh)
	if err := c.stdin.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return nil
}

// call sends a request and waits for the matching response.
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s params: %w", method, err)
	}
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: &id, Method: method, Params: raw}
	respCh := make(chan *JSONRPCMessage, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("acp client closed")
	}
	c.pending[id] = respCh
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(msg); err != nil {
		return fmt.Errorf("send %s: %w", method, err)
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return resp.Error
		}
		if len(resp.Result) == 0 || string(resp.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("unmarshal %s result: %w", method, err)
		}
		return nil
	case <-c.closeCh:
		return errors.New("acp client closed during call")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notify sends a notification (no id, no response expected).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s params: %w", method, err)
	}
	msg := JSONRPCMessage{JSONRPC: "2.0", Method: method, Params: raw}
	if err := c.send(msg); err != nil {
		return fmt.Errorf("send %s: %w", method, err)
	}
	return nil
}

func (c *Client) send(msg JSONRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	_, err = c.stdin.Write(data)
	c.writeMu.Unlock()
	return err
}

// readLoop reads line-delimited JSON-RPC frames from stdout, dispatches
// responses to pending requesters, and pushes notifications to Updates.
func (c *Client) readLoop() {
	defer close(c.updatesOut)
	r := bufio.NewReaderSize(c.stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			// Strip trailing newline; tolerate trailing whitespace.
			for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
				line = line[:len(line)-1]
			}
			if len(line) > 0 {
				c.handleFrame(line)
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) handleFrame(data []byte) {
	var msg JSONRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		// Non-JSON lines are ignored; the agent may emit diagnostics.
		return
	}
	// Response: has id, no method.
	if msg.ID != nil && msg.Method == "" {
		c.mu.Lock()
		ch, ok := c.pending[*msg.ID]
		c.mu.Unlock()
		if !ok {
			return
		}
		select {
		case ch <- &msg:
		default:
		}
		return
	}
	// Notification: has method, no id.
	if msg.Method != "" && msg.ID == nil {
		select {
		case c.updatesOut <- &msg:
		case <-c.closeCh:
		}
		return
	}
	// Requests from server to client (e.g. session/request_permission) are
	// not handled in v1. Drop them; the agent will time out its own
	// permission prompt and proceed under the configured permission mode.
}

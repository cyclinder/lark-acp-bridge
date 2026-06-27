package acp

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

// fakeServer is a minimal in-process ACP server speaking line-delimited
// JSON-RPC over an in-memory pipe pair. It implements just enough of the
// protocol to exercise the Client: initialize, session/new, session/prompt
// (with a scripted stream of session/update notifications), session/cancel,
// session/set_config_option, and session/close.
type fakeServer struct {
	r       *io.PipeReader
	w       *io.PipeWriter
	clientW *io.PipeWriter // writes go to the client's stdin
	clientR *io.PipeReader // reads come from the client's stdout

	mu       sync.Mutex
	closed   bool
	handlers map[string]func(id *int64, params json.RawMessage)
}

func newFakeServer(t *testing.T) (*fakeServer, *Client) {
	t.Helper()
	// Client stdin: server writes -> client reads. Pipe() returns (r, w).
	cliStdinR, srvW := io.Pipe()
	// Client stdout: client writes -> server reads. Pipe() returns (r, w).
	srvR, cliStdoutW := io.Pipe()
	srv := &fakeServer{
		r:        srvR,
		w:        srvW,
		clientW:  cliStdoutW,
		clientR:  cliStdinR,
		handlers: map[string]func(*int64, json.RawMessage){},
	}
	srv.installDefaults()
	cli := NewClient(pipeWriteCloser{cliStdoutW}, pipeReadCloser{cliStdinR})
	go srv.serve()
	return srv, cli
}

// pipeWriteCloser wraps io.PipeWriter to satisfy io.WriteCloser.
type pipeWriteCloser struct{ *io.PipeWriter }

func (p pipeWriteCloser) Close() error { return p.PipeWriter.Close() }

type pipeReadCloser struct{ *io.PipeReader }

func (p pipeReadCloser) Close() error { return p.PipeReader.Close() }

func (s *fakeServer) installDefaults() {
	s.handlers["initialize"] = func(id *int64, _ json.RawMessage) {
		s.respond(id, InitializeResult{
			ProtocolVersion: ProtocolVersion,
			AgentCapabilities: AgentCapabilities{
				LoadSession:         true,
				SessionCapabilities: &SessionCapabilities{Resume: &struct{}{}, Close: &struct{}{}},
			},
			AgentInfo: ImplementationInfo{Name: "fake-devin", Version: "test"},
		})
	}
	s.handlers["session/new"] = func(id *int64, _ json.RawMessage) {
		s.respond(id, SessionNewResult{
			SessionID: "sess_test",
			ConfigOptions: []ConfigOption{
				{
					ID: "model", Name: "Model", Type: "select", Category: "model",
					CurrentValue: "opus",
					Options: []ConfigOptionVal{
						{Value: "opus", Name: "Opus"},
						{Value: "sonnet", Name: "Sonnet"},
					},
				},
			},
		})
	}
	s.handlers["session/resume"] = func(id *int64, _ json.RawMessage) {
		s.respond(id, struct{}{})
	}
	s.handlers["session/close"] = func(id *int64, _ json.RawMessage) {
		s.respond(id, struct{}{})
	}
	s.handlers["session/set_config_option"] = func(id *int64, p json.RawMessage) {
		var params SetConfigOptionParams
		_ = json.Unmarshal(p, &params)
		s.respond(id, SetConfigOptionResult{
			ConfigOptions: []ConfigOption{
				{ID: "model", Name: "Model", Type: "select", CurrentValue: params.Value,
					Options: []ConfigOptionVal{{Value: "opus", Name: "Opus"}, {Value: "sonnet", Name: "Sonnet"}}},
			},
		})
	}
	s.handlers["session/prompt"] = func(id *int64, _ json.RawMessage) {
		// Stream a text chunk, a tool call, a usage update, then end.
		s.notify("session/update", SessionUpdateParams{
			SessionID: "sess_test",
			Update: Update{
				SessionUpdate: UpdateAgentMessageChunk,
				MessageID:     "m1",
				Content:       mustJSON(Content{Type: "text", Text: "Hello "}),
			},
		})
		s.notify("session/update", SessionUpdateParams{
			SessionID: "sess_test",
			Update: Update{
				SessionUpdate: UpdateAgentMessageChunk,
				MessageID:     "m1",
				Content:       mustJSON(Content{Type: "text", Text: "world"}),
			},
		})
		s.notify("session/update", SessionUpdateParams{
			SessionID: "sess_test",
			Update: Update{
				SessionUpdate: UpdateToolCall,
				ToolCallID:    "t1", Title: "Run ls", Kind: "other", Status: "pending",
			},
		})
		s.notify("session/update", SessionUpdateParams{
			SessionID: "sess_test",
			Update: Update{
				SessionUpdate: UpdateToolCallUpdate,
				ToolCallID:    "t1", Status: "completed",
				Content:       mustJSON([]UpdateContent{{Type: "text", Text: "file.txt"}}),
			},
		})
		used := 120
		size := 200000
		s.notify("session/update", SessionUpdateParams{
			SessionID: "sess_test",
			Update: Update{
				SessionUpdate: UpdateUsage,
				Used:          &used, Size: &size,
				Cost: &Cost{Amount: 0.01, Currency: "USD"},
			},
		})
		s.respond(id, SessionPromptResult{StopReason: StopEndTurn})
	}
	s.handlers["session/cancel"] = func(id *int64, _ json.RawMessage) {
		// Notifications have nil id; nothing to respond with.
	}
}

func (s *fakeServer) serve() {
	dec := json.NewDecoder(s.r)
	for {
		var msg JSONRPCMessage
		if err := dec.Decode(&msg); err != nil {
			return
		}
		if msg.Method == "" {
			continue
		}
		s.mu.Lock()
		h, ok := s.handlers[msg.Method]
		s.mu.Unlock()
		if !ok {
			// Unknown method: respond with a method-not-found error.
			s.respond(msg.ID, nil)
			continue
		}
		h(msg.ID, msg.Params)
	}
}

func (s *fakeServer) respond(id *int64, result any) {
	if id == nil {
		return
	}
	var raw json.RawMessage
	if result != nil {
		raw, _ = json.Marshal(result)
	} else {
		raw = json.RawMessage("null")
	}
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: id, Result: raw}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	_, _ = s.w.Write(data)
}

// mustJSON marshals v to json.RawMessage, panicking on error (test-only).
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func (s *fakeServer) notify(method string, params any) {
	raw, _ := json.Marshal(params)
	msg := JSONRPCMessage{JSONRPC: "2.0", Method: method, Params: raw}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	_, _ = s.w.Write(data)
}

func (s *fakeServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.r.Close()
	_ = s.w.Close() // closing the write end EOFs the client's read loop
}

func TestInitializeAndSessionNew(t *testing.T) {
	srv, cli := newFakeServer(t)
	defer srv.close()
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := cli.Initialize(ctx, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ImplementationInfo{Name: "test", Version: "0.1"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %d, want %d", res.ProtocolVersion, ProtocolVersion)
	}
	if !res.AgentCapabilities.LoadSession {
		t.Fatal("expected loadSession capability")
	}

	sn, err := cli.SessionNew(ctx, SessionNewParams{Cwd: "/tmp", McpServers: []MCPServer{}})
	if err != nil {
		t.Fatalf("SessionNew: %v", err)
	}
	if sn.SessionID != "sess_test" {
		t.Fatalf("sessionId = %q, want sess_test", sn.SessionID)
	}
	if len(sn.ConfigOptions) == 0 || sn.ConfigOptions[0].ID != "model" {
		t.Fatalf("expected model config option, got %+v", sn.ConfigOptions)
	}
}

func TestSessionPromptStreamsEvents(t *testing.T) {
	srv, cli := newFakeServer(t)
	defer srv.close()
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := cli.Initialize(ctx, InitializeParams{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := cli.SessionNew(ctx, SessionNewParams{Cwd: "/tmp", McpServers: []MCPServer{}}); err != nil {
		t.Fatalf("SessionNew: %v", err)
	}

	// Collect notifications in the background while session/prompt blocks.
	var events []Event
	done := make(chan struct{})
	go func() {
		for msg := range cli.Updates {
			if ev, ok := ParseUpdate(msg); ok {
				events = append(events, ev)
			}
		}
		close(done)
	}()

	res, err := cli.SessionPrompt(ctx, SessionPromptParams{
		SessionID: "sess_test",
		Prompt:    []ContentBlock{{Type: "text", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("SessionPrompt: %v", err)
	}
	if res.StopReason != StopEndTurn {
		t.Fatalf("stopReason = %q, want end_turn", res.StopReason)
	}
	// Close both ends so the read loop exits and Updates closes. Order
	// matters: cli.Close() EOFs the server's read; srv.close() EOFs the
	// client's read, which lets the Updates drain goroutine finish.
	_ = cli.Close()
	srv.close()
	<-done

	// Expect: 2 text chunks, 1 tool start, 1 tool update, 1 usage.
	var texts, toolStarts, toolUpdates, usages int
	for _, ev := range events {
		switch ev.Kind {
		case EventKindText:
			texts++
		case EventKindToolStart:
			toolStarts++
		case EventKindToolUpdate:
			toolUpdates++
		case EventKindUsage:
			usages++
		}
	}
	if texts != 2 {
		t.Errorf("text events = %d, want 2", texts)
	}
	if toolStarts != 1 {
		t.Errorf("tool start events = %d, want 1", toolStarts)
	}
	if toolUpdates != 1 {
		t.Errorf("tool update events = %d, want 1", toolUpdates)
	}
	if usages != 1 {
		t.Errorf("usage events = %d, want 1", usages)
	}
	// Verify usage payload.
	for _, ev := range events {
		if ev.Kind == EventKindUsage {
			if ev.UsedTokens != 120 || ev.CostAmount != 0.01 {
				t.Errorf("usage payload = %+v", ev)
			}
		}
	}
}

func TestSetConfigOption(t *testing.T) {
	srv, cli := newFakeServer(t)
	defer srv.close()
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = cli.Initialize(ctx, InitializeParams{ProtocolVersion: ProtocolVersion})
	_, _ = cli.SessionNew(ctx, SessionNewParams{Cwd: "/tmp", McpServers: []MCPServer{}})

	res, err := cli.SetConfigOption(ctx, SetConfigOptionParams{
		SessionID: "sess_test", ConfigID: "model", Value: "sonnet",
	})
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	if len(res.ConfigOptions) == 0 || res.ConfigOptions[0].CurrentValue != "sonnet" {
		t.Fatalf("config after switch = %+v", res.ConfigOptions)
	}
}

func TestSessionCancel(t *testing.T) {
	srv, cli := newFakeServer(t)
	defer srv.close()
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = cli.Initialize(ctx, InitializeParams{ProtocolVersion: ProtocolVersion})
	_, _ = cli.SessionNew(ctx, SessionNewParams{Cwd: "/tmp", McpServers: []MCPServer{}})

	if err := cli.SessionCancel(ctx, SessionCancelParams{SessionID: "sess_test"}); err != nil {
		t.Fatalf("SessionCancel: %v", err)
	}
}

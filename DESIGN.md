# Design Document: lark-acp-bridge

Status: draft (v1 scope: Devin only)
Last updated: 2026-06-24

<!-- Verified findings from real `devin acp` (2026.8.18) integration tests.
     Run with: go test -tags integration ./internal/agent/devin/acp/ -v -->

## 1. Goals

Bridge Feishu / Lark messenger with local CLI coding agents, starting with
Devin (driven through the Agent Client Protocol). The bridge runs as a single
process: it receives Feishu messages over WebSocket, dispatches slash commands
locally, and forwards plain prompts to a local agent subprocess whose streamed
output is rendered onto one live Lark card.

### v1 scope

- One provider: **Devin**, driven via `devin acp` (ACP JSON-RPC over stdio).
- Slash commands handled locally (no token cost): `/help`, `/new`, `/reset`,
  `/cd`, `/ws`, `/status`, `/pwd`, `/stop`, `/resume`, `/model`.
- Dynamic `/help` separating bridge commands from agent-specific commands.
- `/model` (list, current marked) and `/model <N|name>` (switch) for model
  switching, sourced from ACP `configOptions` when available, with a
  built-in fallback table.
- Per-chat session continuity (`chatId` or `chatId:threadId`).
- Streaming run card: text deltas, tool calls, usage, and final status update
  on one card in real time.
- Single profile, single Feishu app.

### v1.1 scope

- **Codex** provider, driven via `codex exec --json` (NDJSON event stream
  over stdout). Each run spawns a fresh `codex exec` process; session
  continuity uses `resume <threadId>` on the next spawn.
- `/provider` switch command: list registered providers, `/provider <id>`
  to override the default per chat, `/provider default` to revert. Switching
  provider clears the current session (context is not portable across
  providers).
- A provider registry (`internal/provider`) holds all adapters and a
  persisted per-scope selection (`providers.json`). The default provider is
  `config.defaultProvider` (defaults to `devin`).

### Out of scope (v2+)

- Claude adapter.
- Access control lists (allowed users / admins / allowed chats).
- lark-cli identity policy, cloud-doc comments, multi-profile, QR app
  registration wizard.
- Image / file attachment forwarding (design leaves room; not implemented v1).

### v1.2 scope: CLI process management

- The binary is a CLI with `run`, `status`, `stop`, and `uninstall`
  subcommands (no subcommand defaults to `run` in the foreground).
- `run --detach` spawns the bridge as a background daemon (new session via
  `setsid`, PID file + daemon state under the bridge home, stdout/stderr
  redirected to `logs/daemon-stdout.log`).
- `run --mode systemd` generates a `lark-acp-bridge.service` unit and runs
  `systemctl enable --now` (system scope by default, `--user` for the user
  manager). The unit runs `run --foreground` with `Restart=on-failure`.
- `status` reports running state, launch mode (process/systemd), pid or unit,
  uptime, and config path. `stop` sends SIGTERM (process mode) or
  `systemctl stop` (systemd mode). Both auto-detect the mode from the daemon
  state file and clean up stale bookkeeping.
- `internal/daemon` owns the state file (`daemon.json`), PID file
  (`bridge.pid`), systemd unit rendering/install, and the detached spawn.

## 2. Non-goals

- Replacing the upstream TypeScript `lark-coding-agent-bridge`. This is a
  fresh Go project that borrows design ideas only.
- Sandboxing agent file access. The working directory is the agent's CWD;
  actual file access is governed by the agent's own permission mode.
- Reimplementing ACP client features the bridge does not need (terminal
  management, client filesystem methods, MCP server injection).

## 3. Background and verified facts

### Devin CLI non-interactive surface

Verified on `devin 2026.8.18` (build 16737566), confirmed via integration
tests in `internal/agent/devin/acp/integration_test.go`:

- `devin -p "<prompt>"` runs non-interactively and prints the final response
  to stdout, then exits 0. Output is buffered (not streamed), with no
  tool-call events, no token usage, and no session id on stdout. Suitable
  only as a fallback.
- `devin acp` starts an ACP server speaking JSON-RPC over stdio. Chisel
  logs go to **stderr**; JSON-RPC frames go to **stdout**, one JSON object
  per line. This is the streaming channel used by Zed / Windsurf / JetBrains.
- **Global flags must precede the `acp` subcommand**: `devin --permission-mode
  dangerous acp`, NOT `devin acp --permission-mode dangerous` (the latter
  fails with "unexpected argument").
- `--model <name>` and `--permission-mode <mode>` are global flags accepted
  before the `acp` subcommand. `DEVIN_MODEL` env also works.
- `--agent-config <file>` accepts a JSON/YAML declarative agent config
  (system instructions, tool visibility, permissions) — the Devin analogue
  of `--append-system-prompt`.
- `devin list --format json` lists recent sessions in the current directory
  with human-readable slug ids (e.g. `four-anemone`).
- `devin --export <file> -p "..."` writes an ATIF-v1.7 transcript with
  `session_id`, `agent.model_name`, `steps[]`, and `final_metrics`
  (token/cost). Post-run only; not streaming.
- Auth: `devin auth login` (browser, or `--force-manual-token-flow` for SSH)
  or `WINDSURF_API_KEY` env. One-time setup; bridge preflight only needs
  `devin --version`.

### ACP protocol surface used by v1

Reference: https://agentclientprotocol.com/protocol/v1/

Verified against real `devin acp` (2026.8.18):

- `initialize` (request): client sends `protocolVersion: 1`,
  `clientCapabilities`, `clientInfo`. Agent responds with
  `agentCapabilities` and `agentInfo`. **Real response fields verified:**
  - `agentCapabilities.loadSession: true`
  - `agentCapabilities.promptCapabilities: {image: true, audio: false,
    embeddedContext: true}`
  - `agentCapabilities.mcpCapabilities: {http: false, sse: false}`
  - `agentCapabilities.sessionCapabilities: {list: {}, additionalDirectories:
    {}}` — **does NOT include `resume` or `close`** in this version.
  - `authMethods` uses `id`/`name`/`description` fields (not `methodType`).
  - `agentInfo: {name: "affogato", title: "Affogato Agent", version:
    "0.0.0-dev"}`.
  - Vendor `_meta` fields present (e.g. `cognition.ai/multiRootWorkspace`).
- `session/new` (request): params `{ cwd, mcpServers }`. **`mcpServers` is
  required** — the real agent rejects `{"cwd": "/tmp"}` with
  `missing field 'mcpServers'`. Pass `[]` (empty array, not null) when no
  MCP servers are needed. Response `{ sessionId, configOptions? }`.
  - `configOptions` includes two options: `id: "mode"` (Session Mode:
    accept-edits/ask/plan/bypass) and `id: "model"` (Model: 92+ options
    including Claude, GPT, Gemini, Kimi, GLM, SWE variants, and
    `adaptive` as default).
  - The agent also emits a `session/update` notification with
    `sessionUpdate: "config_option_update"` immediately after session
    creation, carrying the same configOptions.
- `session/prompt` (request): params `{ sessionId, prompt: ContentBlock[] }`.
  Response `{ stopReason }` where `stopReason` is one of `end_turn`,
  `max_tokens`, `max_turn_requests`, `refusal`, `cancelled`. **Verified:
  simple prompt "Reply with exactly: hello world" returns `end_turn` with
  one text chunk "hello world" in ~7s.**
- `session/update` (notification): carries one of
  `agent_message_chunk`, `user_message_chunk`, `tool_call`,
  `tool_call_update`, `plan`, `usage_update`, `config_option_update`,
  `session_info_update`. These map directly onto the bridge `AgentEvent`
  union (see §6).
- `session/cancel` (notification): params `{ sessionId }`. Aborts the
  current prompt turn. **Verified: agent resolves the pending
  `session/prompt` with `stopReason: "cancelled"` within ~7s.**
- `session/set_config_option` (request): params
  `{ sessionId, configId, value }`. Used by `/model` to switch models
  mid-session. **Verified: switching from `adaptive` to
  `claude-opus-4-8-medium` succeeds and response confirms the new
  currentValue.**
- `session/resume` (request): params `{ sessionId, cwd, mcpServers }`.
  **NOT supported by devin 2026.8.18** — `sessionCapabilities.resume` is
  absent from the initialize response. The adapter gracefully falls back
  to `session/new` when resume is unavailable.
- `session/close` (request): **NOT supported by devin 2026.8.18** —
  `sessionCapabilities.close` is absent. The adapter does not call it.

### Feishu / Lark Go SDK

`github.com/larksuite/oapi-sdk-go/v3` (v3.9.6, MIT) provides a `channel`
module built on `ws.Client` + `lark.Client` + `event.EventDispatcher`. It
exposes `OnMessage`, `Send` (with `Markdown` / `Card` / streaming reply
support), normalized messages, media upload, and card interactions. This is
the Go analogue of `@larksuite/channel` used by the TS bridge.

## 4. Architecture overview

```
                       Feishu / Lark
                            |  (WebSocket)
                            v
                   +-------------------+
                   |   lark.Channel    |   internal/lark
                   |  (SDK channel)    |
                   +-------------------+
                            |  NormalizedMessage
                            v
                   +-------------------+
                   |  CommandDispatch  |   internal/commands
                   +-------------------+
                     /              \
          slash cmd (local)    plain prompt (forwarded)
                    |                |
                    v                v
            local handler     +-------------------+
            (no token cost)   |   RunExecutor     |   internal/run
                              +-------------------+
                                       |  AgentRunOptions
                                       v
                              +-------------------+
                              |  DevinAdapter     |   internal/agent/devin
                              |  (AgentAdapter)   |
                              +-------------------+
                                       |  JSON-RPC over stdio
                                       v
                              +-------------------+
                              |  ACPClient        |   internal/agent/devin/acp
                              |  (devin acp proc) |
                              +-------------------+
                                       |  AgentEvent stream
                                       v
                              +-------------------+
                              |  CardRenderer     |   internal/card
                              |  (streaming card) |
                              +-------------------+
                                       |
                                       v
                                  Feishu card
```

### Process model

One bridge process = one Feishu app connection + one long-lived
`devin acp` subprocess per active session (see §7 for the pooling
strategy). The bridge owns the ACP client; the ACP client owns the
`os/exec.Cmd` for `devin acp`.

### Concurrency

- Feishu messages arrive concurrently; the bridge debounces messages from
  the same scope within a short window (600ms) and batches them into one
  prompt.
- One active run per scope. Messages arriving during an active run are
  queued and fed to the next turn.
- A bounded worker pool caps concurrent runs across scopes (default 4).

## 5. Package layout

```
lark-acp-bridge/
├── go.mod
├── cmd/lark-acp-bridge/main.go        # entrypoint
├── internal/
│   ├── config/                         # profile config load/save
│   │   ├── config.go                   # Config struct, paths, defaults
│   │   └── store.go                    # JSON read/write to ~/.lark-acp-bridge/
│   ├── lark/                           # Feishu channel wrapper
│   │   ├── channel.go                  # wraps SDK channel, lifecycle
│   │   ├── message.go                  # NormalizedMessage helpers
│   │   └── card.go                     # card send / update / stream
│   ├── agent/
│   │   ├── adapter.go                  # AgentAdapter interface
│   │   ├── event.go                    # AgentEvent union + mappers
│   │   └── devin/
│   │       ├── adapter.go              # DevinAdapter: implements AgentAdapter
│   │       ├── models.go               # built-in Devin model fallback table
│   │       └── acp/
│   │           ├── client.go           # ACPClient: initialize/new/prompt/cancel
│   │           ├── protocol.go         # ACP JSON-RPC request/response/notification types
│   │           └── stream.go           # session/update -> AgentEvent translation
│   ├── session/                        # scope -> sessionId mapping
│   │   └── store.go
│   ├── workspace/                      # scope -> cwd mapping
│   │   └── store.go
│   ├── commands/                       # slash command handlers
│   │   ├── dispatch.go                 # tryHandleCommand, admin gating
│   │   ├── context.go                  # CommandContext
│   │   ├── help.go                     # dynamic /help
│   │   ├── new.go                      # /new /reset
│   │   ├── cd.go                       # /cd
│   │   ├── status.go                   # /status
│   │   ├── stop.go                     # /stop
│   │   ├── resume.go                   # /resume
│   │   └── model.go                    # /model (list + switch)
│   ├── card/                           # card rendering
│   │   ├── templates.go                # help/status/model/run card builders
│   │   └── runstate.go                 # run state machine for streaming card
│   ├── run/                            # run orchestration
│   │   └── executor.go                 # RunExecutor: adapter -> events -> card
│   ├── daemon/                         # CLI process management
│   │   ├── daemon.go                   # state + PID file + status + stop
│   │   ├── systemd.go                  # unit file render/install/uninstall
│   │   └── detach.go                   # detached background spawn
│   ├── preflight/                      # agent availability checks
│   │   └── devin.go                    # `devin --version` probe
│   └── log/                            # structured logging wrapper
│       └── log.go
└── README.md
```

## 6. Core types

### AgentAdapter (internal/agent/adapter.go)

```go
type AgentAdapter interface {
    ID() string          // "devin"
    DisplayName() string // "Devin"
    Available(ctx context.Context) error
    Run(ctx context.Context, opts RunOptions) (Run, error)
}

type RunOptions struct {
    Prompt     string
    Cwd        string
    SessionID  string // empty = new session
    Model      string // empty = agent default
    StopGrace  time.Duration
}

type Run interface {
    Events() <-chan AgentEvent
    Stop() error
    Wait() error // blocks until process exits; returns exit error
}
```

### AgentEvent (internal/agent/event.go)

```go
type AgentEvent struct {
    Type       EventType
    // text
    Delta      string
    // tool
    ToolID     string
    ToolName   string
    ToolInput  json.RawMessage
    ToolOutput string
    ToolError  bool
    // usage
    InputTokens  int
    OutputTokens int
    CostUSD      float64
    // done / error
    SessionID      string
    StopReason     string // end_turn, max_tokens, cancelled, ...
    Err            error
}

type EventType int
const (
    EventText Event = iota
    EventThinking
    EventToolUse
    EventToolResult
    EventUsage
    EventDone
    EventError
)
```

ACP `session/update` mapping:

| ACP `sessionUpdate`        | AgentEvent Type     | Fields populated                     |
|----------------------------|---------------------|--------------------------------------|
| `agent_message_chunk`      | EventText           | Delta                                |
| `tool_call`                | EventToolUse        | ToolID, ToolName, ToolInput          |
| `tool_call_update`         | EventToolResult     | ToolID, ToolOutput, ToolError        |
| `usage_update`             | EventUsage          | InputTokens, OutputTokens, CostUSD   |
| `plan`                     | EventThinking       | Delta (joined plan entries)          |
| `config_option_update`     | (internal)          | updates adapter's cached model list  |
| `session/prompt` response  | EventDone           | StopReason, SessionID                |

### CommandContext (internal/commands/context.go)

```go
type CommandContext struct {
    Channel   *lark.Channel
    Msg       *types.NormalizedMessage
    Scope     string             // chatId or chatId:threadId
    ChatMode  string             // "p2p" | "group" | "topic"
    Sessions  *session.Store
    Workspaces *workspace.Store
    Adapter   agent.AgentAdapter
    Runs      *run.ActiveRuns
    Config    *config.Config
    // for /model: cached ACP configOptions from the active session
    ModelList []ModelOption
}
```

## 7. ACP client design (internal/agent/devin)

### Lifecycle

`DevinAdapter` maintains a **pool of long-lived `devin acp` subprocesses**
keyed by scope (chatId or chatId:threadId). Consecutive messages in the same
chat reuse the same process and session, preserving context.

On `Run()`:

1. Look up the scope's `sessionClient` in the pool. If it exists and is
   alive, reuse it. If it has died (process exited), remove it and spawn a
   fresh one.
2. If no client exists, spawn `devin --permission-mode <mode> [--model <m>] acp`,
   perform `initialize`, then `session/new` with `{ cwd, mcpServers: [] }`.
   Cache the returned `sessionId` and `configOptions` in the pool entry.
3. Acquire the per-client `runMu` (serializes prompt turns).
4. Send `session/prompt` with `{ sessionId, prompt }`. Stream
   `session/update` notifications to the events channel until the prompt
   response arrives.
5. **Do NOT kill the process.** Arm an idle timer; the process stays alive
   for the next message.
6. On `Stop()`: send `session/cancel`. The pending `session/prompt`
   resolves with `stopReason: "cancelled"`.
7. On `/new` `/cd` `/reset`: `adapter.Close(scope)` kills the process and
   removes it from the pool.
8. On bridge shutdown: `adapter.CloseAll()` kills every process in the pool.

### Idle reaping

After each run ends, an idle timer is armed (default 10 minutes,
configurable via `idleTimeoutMinutes`). When it fires, the process is
killed and the pool entry is removed. A new message arriving before the
timer fires cancels it and reuses the process. Setting
`idleTimeoutMinutes: 0` disables idle reaping (processes live until
explicit `/new` `/cd` or shutdown).

### Concurrency cap

The pool size is capped at `maxConcurrentRuns` (default 4). When the cap
is reached, new scopes are rejected with an error message until an existing
scope's process is reaped by idle timeout or `/new` `/cd`. Rebuilding a
dead scope's process does not count against the cap (the dead entry is
removed before the new one is spawned).

### Why one subprocess per scope (not one global)

ACP ties a session to a single `devin acp` process. Multiple concurrent
chats need multiple subprocesses. The pool caps this naturally: idle
processes are reaped, and `/new` `/cd` explicitly close them.

### Model switching mid-session

`/model <name>` calls `adapter.SetModel(scope, model)` which issues
`session/set_config_option` with `{ sessionId, configId: "model", value }`
on the scope's active session. **Context is preserved** — the session is
not reset. If no session is active (no prior run in this scope), the choice
is stashed in the session store and applied as `--model` on the next spawn.

### Request/response correlation

The ACP client maintains a map of `id -> chan response`. Notifications
(method present, no id) are pushed to a separate channel. A background
reader goroutine splits incoming lines into requests vs notifications and
fans them out. Between runs (when no pump goroutine is reading the
notifications channel), notifications are dropped — this is acceptable
since inter-run notifications (e.g. `config_option_update`) are not
actionable.

### System prompt injection

The bridge writes a small YAML agent config to a temp file:

```yaml
systemInstructions: |
  You are bridged into Feishu/Lark as a bot. Your open_id is <botOpenId>.
  Reply in the user's language. Keep tool calls concise for card display.
```

Passed via `--agent-config <tmpfile>` on `devin acp` spawn. Removed when
the ACP client closes.

## 8. Slash command spec

All commands are handled locally by the bridge and never invoke the agent
subprocess. They cost zero tokens.

| Command                  | Effect                                                              |
|--------------------------|---------------------------------------------------------------------|
| `/help`                  | Dynamic card: bridge commands (always) + Devin commands (always in v1). |
| `/new` `/reset`          | Close active ACP session for scope, clear scope->sessionId mapping. |
| `/cd <path>`             | Set scope cwd, clear session. Validates absolute path, rejects overly broad roots. |
| `/ws`                    | Manage named workspace aliases: `/ws save <name>`, `/ws use <name>`, `/ws remove <name>`. Aliases persist in workspaces.json. |
| `/status`                | Card showing profile, cwd, session id, model, active run state.     |
| `/stop`                  | Cancel active run (`session/cancel` + SIGTERM fallback).            |
| `/resume`                | List `devin list --format json` sessions for current cwd; `/resume use <id>` resumes. |
| `/model`                 | List available models with sequence numbers (1, 2, ...), marking the current one. Source: ACP configOptions if a session is active, else built-in fallback table. |
| `/model <N\|name>`       | Switch model. If session active: `session/set_config_option`. Else stash for next spawn. Clears session (context loss accepted). |

### `/help` dynamic layout

```
Bridge commands (always available):
  /new /reset /cd <path> /ws /status /pwd /stop /resume /model /help

Devin commands (provider-specific, no token cost):
  /model   list models (current marked); /model <N|name> switches
  /resume [use <id>]        resume a Devin session

Anything else is sent to Devin as a prompt.
```

In v2, the "Devin commands" section becomes "current provider: Devin" and
swaps content based on the active provider.

## 9. Streaming run card

One card per run, updated in place via the SDK channel's streaming reply.
The card has three regions:

1. **Text region**: agent message chunks appended as markdown.
2. **Tool region**: collapsible list of tool calls with status
   (pending / in_progress / completed) and truncated output.
3. **Footer**: usage (tokens, cost) and a Stop button.

State machine (`internal/card/runstate.go`):

```
idle -> running -> (streaming updates) -> done | error | cancelled
```

Each `AgentEvent` advances the state and produces a card patch. The
`CardRenderer` coalesces rapid updates (debounce 300ms) to avoid
overwhelming the Feishu API.

## 10. Configuration

`~/.lark-acp-bridge/config.json`:

```json
{
  "app": {
    "id": "cli_xxx",
    "secret": "xxx",
    "tenant": "feishu"
  },
  "workspace": {
    "default": "/home/me/projects"
  },
  "agent": {
    "binary": "devin",
    "permissionMode": "dangerous",
    "defaultModel": ""
  },
  "maxConcurrentRuns": 4,
  "debounceMs": 600,
  "stopGraceMs": 5000
}
```

No profile multiplexing in v1. Secret is stored as a literal string in v1;
encrypted keystore deferred.

## 11. Data directories

| Path                                          | Content                                  |
|-----------------------------------------------|------------------------------------------|
| `~/.lark-acp-bridge/config.json`              | Root config                              |
| `~/.lark-acp-bridge/sessions.json`            | scope -> sessionId + cwd mapping         |
| `~/.lark-acp-bridge/logs/bridge-YYYYMMDD.log` | Structured run logs                      |

`LARK_ACP_BRIDGE_HOME` env overrides the root.

## 12. Error handling

- ACP spawn failure (devin not installed / not logged in): surface the
  preflight diagnostic to the user via a one-shot card, do not crash.
- ACP protocol error (unexpected frame, missing response): emit
  `EventError` and end the run; the card shows the error in the footer.
- Feishu send failure: log and continue; never crash the bridge on a send
  error.
- Run timeout (idle watchdog): optional, off by default in v1. If enabled,
  kill the run after N minutes of no events.

## 13. Verification plan

Per `AGENTS.md`:

```bash
go build ./...
go vet ./...
go test ./...
```

v1 test layers:

- `internal/agent/devin/acp`: unit tests with a fake ACP server (a Go
  goroutine that speaks JSON-RPC over an in-memory pipe) to verify
  initialize / session/new / prompt / cancel / set_config_option flows
  and event mapping.
- `internal/commands`: table-driven tests for each slash command against
  a mock CommandContext.
- `internal/card/runstate`: state machine transition tests.
- `internal/run`: integration test with a fake AgentAdapter that emits a
  scripted event sequence, asserting card patches.
- Manual smoke test: `go run ./cmd/lark-acp-bridge` against a real Feishu
  app and a real `devin acp`, exercising `/help`, `/model`,
  `/cd`, a plain prompt, and `/stop`.

## 14. v2 extension points

The design isolates provider-specific behavior behind `AgentAdapter` and
the `commands` package's provider-aware sections. Adding Claude and Codex:

- Implement `ClaudeAdapter` (spawn `claude -p --output-format stream-json`,
  map stream-json events to `AgentEvent`) and `CodexAdapter` (spawn
  `codex exec --json`, map jsonl events).
- Add `/provider` (list available adapters via preflight, switch the
  active adapter for the scope, clear session).
- Generalize `AgentKind` from `"devin"` to `"devin" | "claude" | "codex"`.
- `/help` agent section becomes provider-dependent.
- `/model` falls back to per-provider built-in tables when the provider
  has no dynamic model list (Claude, Codex).

No changes to `AgentAdapter` interface, `RunExecutor`, `CardRenderer`, or
the Feishu layer are required for v2.

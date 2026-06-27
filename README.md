# lark-acp-bridge

A Go bridge that connects Feishu / Lark messenger with local CLI coding
agents. v1 ships **Devin** (driven through the Agent Client Protocol); v1.1
adds **Codex** and a `/provider` switch command.

## What it does

- Forwards Feishu / Lark messages to a local agent subprocess (Devin via
  `devin acp`, or Codex via `codex exec --json`).
- Streams agent responses (text, tool calls, usage) onto one live Lark card.
- Handles slash commands locally (zero token cost): `/help`, `/new`, `/cd`,
  `/ws`, `/open`, `/status`, `/pwd`, `/stop`, `/model`, `/provider`, `/resume`.
- `/open` creates (or reuses) a Feishu group bound to a working directory,
  so a project can have its own dedicated multi-user chat.
- Per-chat session continuity.
- Per-chat provider override: each chat can switch between registered
  providers with `/provider <id>`.

## Prerequisites

- Go 1.26+
- Devin CLI installed and logged in (`devin auth login`) — required for the
  default provider.
- Codex CLI installed (`codex`) — optional; only needed if you enable the
  `codex` config block and want `/provider codex` to work.
- A Feishu / Lark app with the bot scope enabled. To use `/open`, the app
  also needs the `im:chat:create` and `im:chat:members:write` scopes so it
  can create groups and add users.

## Configuration

Create `~/.lark-acp-bridge/config.json`:

```json
{
  "app": {
    "id": "cli_xxxxxxxxxxxx",
    "secret": "your_app_secret",
    "tenant": "feishu"
  },
  "agent": {
    "binary": "devin",
    "permissionMode": "dangerous",
    "defaultModel": ""
  },
  "defaultProvider": "devin",
  "codex": {
    "binary": "codex",
    "sandbox": "danger-full-access",
    "defaultModel": ""
  },
  "workspace": {
    "default": "/home/me/projects"
  },
  "maxConcurrentRuns": 4,
  "debounceMs": 600,
  "stopGraceMs": 5000
}
```

Set `tenant` to `"lark"` for Lark (global) apps. `defaultProvider` defaults
to `devin`; set it to `codex` to make Codex the default. The `codex` block is
optional — omit it to disable Codex entirely.

## Run

```bash
go run ./cmd/lark-acp-bridge
```

DM the bot directly, or `@bot` in a group. Use `/cd <path>` to set a working
directory, then send any message to start a run with the effective provider.

## Slash commands

| Command | Effect |
|---|---|
| `/help` | Dynamic help card (bridge + agent commands) |
| `/new` `/reset` | Clear the current chat session |
| `/cd <path>` | Switch working directory (resets session) |
| `/ws` | Manage named workspace aliases (`/ws save\|use\|remove <name>`) |
| `/open [path]` | Create/reuse a group bound to a cwd (p2p only) |
| `/status` | Show current state |
| `/pwd` | Print the current working directory |
| `/stop` | Stop the active run |
| `/model` | List available models (current marked) |
| `/model <N\|name>` | Switch model (resets session) |
| `/provider` | List providers; `/provider <id>` switches, `/provider default` reverts |
| `/resume` | List and resume past sessions |

All commands are handled locally and never invoke the agent subprocess.

## Architecture

See [DESIGN.md](./DESIGN.md) for the full design document and
[AGENTS.md](./AGENTS.md) for project rules.

```
Feishu -> lark.Channel -> commands.Dispatch
  |                        |-- slash cmd -> local handler (no tokens)
  |                        |-- plain msg -> run.Executor
  |                                            |-- DevinAdapter
  |                                            |     |-- ACPClient (JSON-RPC over stdio)
  |                                            |     |-- devin acp subprocess
  |                                            |-- CardRenderer (streaming card)
  |                                            v
  |                                          Feishu card
```

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

## License

MIT

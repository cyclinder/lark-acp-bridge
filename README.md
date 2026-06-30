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

## Install

### Option A: download a prebuilt binary

Each release publishes prebuilt binaries to GitHub Releases. Download the one
matching your OS/arch from the
[releases page](https://github.com/cyclinder/lark-acp-bridge/releases), e.g.:

```bash
# Linux amd64
curl -L -o lark-acp-bridge \
  https://github.com/cyclinder/lark-acp-bridge/releases/latest/download/lark-acp-bridge-linux-amd64
chmod +x lark-acp-bridge
sudo mv lark-acp-bridge /usr/local/bin/
```

Available assets per release (named `lark-acp-bridge-<os>-<arch>`):

| Asset                          | OS      | Arch   |
|--------------------------------|---------|--------|
| `lark-acp-bridge-linux-amd64`  | Linux   | amd64  |
| `lark-acp-bridge-linux-arm64`  | Linux   | arm64  |
| `lark-acp-bridge-darwin-amd64` | macOS   | amd64  |
| `lark-acp-bridge-darwin-arm64` | macOS   | arm64  |

Verify the install:

```bash
lark-acp-bridge help
```

### Option B: build from source

```bash
git clone https://github.com/cyclinder/lark-acp-bridge.git
cd lark-acp-bridge
go build -o lark-acp-bridge ./cmd/lark-acp-bridge
```

A development build can also be run directly without producing a binary:

```bash
go run ./cmd/lark-acp-bridge run
```

### Releasing

Releases are cut by tagging a commit (e.g. `v1.2.0`) and pushing the tag.
The release workflow cross-compiles the binaries above and attaches them to
the GitHub Release so the `releases/latest/download/...` URLs stay stable.
When publishing a new release, attach the built binaries to the release
notes so users can `curl` them as shown in Option A.

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

The CLI exposes `run`, `status`, `stop`, and `uninstall` subcommands. With no
subcommand, `run` is assumed (foreground), preserving the original behavior.

### Foreground (default)

```bash
./lark-acp-bridge run
./lark-acp-bridge run -c /path/to/config.json
```

### Detached background daemon

```bash
./lark-acp-bridge run --detach
```

Spawns the bridge in a new session (survives shell exit), writes a PID file
and a daemon state file under the bridge home, and redirects stdout/stderr to
`~/.lark-acp-bridge/logs/daemon-stdout.log`.

### systemd service

```bash
# system scope (requires root; unit under /etc/systemd/system/)
sudo ./lark-acp-bridge run --mode systemd

# user scope (unit under ~/.config/systemd/user/; enable lingering so it
# survives logout: loginctl enable-linger $USER)
./lark-acp-bridge run --mode systemd --user
```

This generates a `lark-acp-bridge.service` unit, reloads the manager, and runs
`systemctl enable --now`. The bridge then runs under systemd with
`Restart=on-failure`. Override the binary path used in `ExecStart` with
`--binary /path/to/lark-acp-bridge` if needed.

### Status and stop

```bash
./lark-acp-bridge status   # shows running state, pid/unit, uptime, config
./lark-acp-bridge stop     # SIGTERM (process mode) or systemctl stop (systemd)
```

`status` and `stop` auto-detect the launch mode from the daemon state file
(`~/.lark-acp-bridge/daemon.json`). Stale state (process gone / unit inactive)
is cleaned up automatically.

### Remove the systemd unit

```bash
sudo ./lark-acp-bridge uninstall
./lark-acp-bridge uninstall --user
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

Apache License, Version 2.0.

Copyright 2026 cyclinder kuo

Licensed under the Apache License, Version 2.0 (the "License"); you may not
use this project except in compliance with the License. You may obtain a copy
of the License at:

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
License for the specific language governing permissions and limitations under
the License.

The full license text is in the [LICENSE](LICENSE) file. The vendored
dependency `github.com/larksuite/oapi-sdk-go/v3` retains its own MIT license.

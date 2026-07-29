# lark-acp-bridge

English | [简体中文](./README.zh.md)

A Go bridge that connects Feishu / Lark messenger with local CLI coding
agents. v1 ships **Devin** (driven through the Agent Client Protocol); v1.1
adds **Codex** and a `/provider` switch command; v1.3 adds **GitHub Copilot**.

## What it does

- Forwards Feishu / Lark messages to a local agent subprocess (Devin via
  `devin acp`, Codex via `codex exec --json`, or GitHub Copilot via
  `copilot -p --output-format json`).
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
- GitHub Copilot CLI installed (`copilot`) — optional; only needed if you
  enable the `copilot` config block and want `/provider copilot` to work.
- A Feishu / Lark **self-built app** with the bot capability enabled and the
  permissions / event subscription below. The bridge talks to Feishu over a
  WebSocket long connection, so no public IP, domain, or webhook URL is
  needed — the bridge dials out to Feishu.

## Feishu / Lark app setup

The bridge connects as a self-built app over the SDK long-connection
(WebSocket) channel, so you do NOT need a public endpoint or webhook. You only
need to create an app, enable the bot, grant a few permissions, and subscribe
to one event.

### 1. Create a self-built app

1. Open the [Feishu Developer Console](https://open.feishu.cn/app) (Feishu)
   or [Lark Developer Console](https://open.larksuite.com/app) (Lark global).
2. Click **Create custom app** (创建企业自建应用), fill in a name and
   description, and create the app.
3. Note the **App ID** (`cli_xxxxxxxxxxxx`) and **App Secret** on the
   **Credentials & Basic Info** (凭证与基础信息) page — these go into
   `config.json` as `app.id` and `app.secret`.

### 2. Enable the bot capability

On the app's **Capabilities** (应用能力) page, add the **Bot** (机器人)
capability. This is what lets the app receive and send IM messages. Without
it, the long connection has nothing to deliver.

### 3. Grant permissions

On **Permissions & Scopes** (权限管理) → **API Permissions**, add these
scopes. The first four are required for the core bridge; the last two are
only needed for `/open` (creating groups and adding members).

| Scope | Why |
|---|---|
| `im:message:send_as_bot` | Send messages / cards as the bot |
| `im:message.p2p_msg:readonly` | Receive DM (p2p) messages from users |
| `im:message.group_at_msg:readonly` | Receive group messages that @mention the bot |
| `im:message.reaction:write` | Add the "typing" reaction to acknowledge a user message |
| `im:chat:create` | `/open`: create a group bound to a working directory |
| `im:chat:members:write` | `/open`: add users to the created group |

After adding scopes, click **Publish version** (创建版本) and have your
tenant admin **approve** it (or, for a personal test app, approve it
yourself if you are the admin). Scopes only take effect once a version is
approved and released.

### 4. Subscribe to the receive-message event

On **Event Subscriptions** (事件与回调) → **Event Configuration**:

1. Set **Subscription mode** (订阅方式) to **Receive events via long
   connection** (使用长连接接收事件). This is the WebSocket mode the bridge
   uses; no request URL is needed.
2. Under **Added events**, add **Receive message (im.message.receive_v1)**
   (接收消息 v2.0). This is the only event the bridge needs.

> The long-connection mode is only available for self-built apps, which is
> exactly what this bridge uses. The SDK handles authentication on connect;
> events arrive as plaintext over the WebSocket.

### 5. Set the app's availability range

On **App Release** (版本发布与发布) → **Availability** (可用范围), add
yourself (or the users / departments who should be able to DM the bot). Then
**Create version** → **Submit for review** → **Approve**. Once approved, the
bot is reachable in Feishu / Lark: search for it by name to start a DM, or
add it to a group and @mention it.

### 6. Put the credentials in config

```json
{
  "app": {
    "id": "cli_xxxxxxxxxxxx",
    "secret": "your_app_secret",
    "tenant": "feishu"
  },
  ...
}
```

Set `tenant` to `"lark"` instead of `"feishu"` if your app is on the Lark
(global) tenant.

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
  "copilot": {
    "binary": "copilot",
    "permissions": "allow-all",
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
to `devin`; set it to `codex` or `copilot` to make one of them the default.
The `codex` and `copilot` blocks are optional — omit them to disable those
providers entirely.

The `copilot.permissions` field maps onto Copilot CLI approval flags:
`"allow-all"` (`--allow-all`: tools, paths, URLs — the default),
`"allow-all-tools"` (`--allow-all-tools`: tools auto-approved, out-of-workspace
paths still denied), or `"read-only"` (best-effort: `shell`/`edit`/`create`
denied — Copilot CLI has no kernel sandbox, so this relies on the model
honoring tool denials).

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

## Quick start (first run)

Once the app is approved and the bridge is running, the typical first-run
flow is:

1. In Feishu / Lark, search for the bot by name and start a DM (or add it
   to a group and @mention it).
2. Send `/cd /path/to/your/project` to bind the chat to a working
   directory. The agent runs there; session state is scoped to this chat.
3. Send `/status` to confirm the cwd, provider, and model.
4. Send any plain message — it is forwarded to the agent as a prompt. The
   reply streams onto one live card (text + tool calls + usage).
5. Use `/stop` to cancel a run mid-turn, `/new` to reset the session, and
   `/help` to see all commands.

In a group, the bot only responds when @mentioned. In a DM, every plain
message is a prompt. Slash commands are handled locally and never cost
tokens; only plain messages reach the agent.

## Slash commands

All commands are handled locally by the bridge — they never invoke the
agent subprocess and never consume tokens.

| Command | Effect |
|---|---|
| `/help` | Dynamic help card: bridge commands always, agent-specific commands only when a provider is selected |
| `/new` `/reset` | Clear the current chat session (context is dropped; the agent process is reaped) |
| `/cd <path>` | Switch working directory for this chat (resets the session) |
| `/ws` | Manage named workspace aliases: `/ws save <name>`, `/ws use <name>`, `/ws remove <name>` |
| `/open [path]` | Create (or reuse) a Feishu group bound to a cwd; p2p only |
| `/status` | Show current scope, cwd, session, provider, model, and active-run state |
| `/pwd` | Print the current working directory |
| `/stop` | Stop the active run for this chat |
| `/model` | List available models with the current one marked |
| `/model <N\|name>` | Switch model (resets the session); `<N>` is the list index from `/model` |
| `/provider` | List registered providers; `/provider <id>` overrides the default per chat, `/provider default` reverts (switching clears the session) |
| `/resume` | List past sessions and resume one with `/resume <N>` |

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

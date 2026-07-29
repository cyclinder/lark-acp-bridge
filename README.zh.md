# lark-acp-bridge

[English](./README.md) | 简体中文

一个用 Go 编写的桥接器，将飞书 / Lark 即时通讯与本地 CLI 编码代理连接起来。
v1 提供 **Devin**（通过 Agent Client Protocol 驱动）；v1.1 增加 **Codex** 和
`/provider` 切换命令；v1.3 增加 **GitHub Copilot**。

## 功能

- 将飞书 / Lark 消息转发到本地代理子进程（Devin 通过 `devin acp`，Codex 通过
  `codex exec --json`，GitHub Copilot 通过 `copilot -p --output-format json`）。
- 将代理响应（文本、工具调用、用量）流式输出到一张实时更新的飞书卡片上。
- 本地处理斜杠命令（零 token 消耗）：`/help`、`/new`、`/cd`、`/ws`、`/open`、
  `/status`、`/pwd`、`/stop`、`/model`、`/provider`、`/resume`。
- `/open` 创建（或复用）一个绑定到工作目录的飞书群，让一个项目拥有自己专属的
  多人聊天。
- 按聊天保持会话连续性。
- 按聊天覆盖 provider：每个聊天可通过 `/provider <id>` 在已注册的 provider 之间
  切换。

## 前置条件

- Go 1.26+
- 已安装并登录 Devin CLI（`devin auth login`）——默认 provider 必需。
- 已安装 Codex CLI（`codex`）——可选；仅在启用 `codex` 配置块且希望
  `/provider codex` 可用时需要。
- 已安装 GitHub Copilot CLI（`copilot`）——可选；仅在启用 `copilot` 配置块且希望
  `/provider copilot` 可用时需要。
- 一个飞书 / Lark **自建应用**，已开启机器人能力并配置下方的权限与事件订阅。
  bridge 通过 WebSocket 长连接与飞书通信，**无需公网 IP、域名或 webhook URL**——
  bridge 主动外连飞书。

## 飞书 / Lark 应用接入

bridge 以自建应用身份通过 SDK 长连接（WebSocket）通道接入，因此你**不需要**公网
端点或 webhook。只需创建应用、开启机器人、授予若干权限、订阅一个事件。

### 1. 创建自建应用

1. 打开 [飞书开发者后台](https://open.feishu.cn/app)（飞书）或
   [Lark 开发者后台](https://open.larksuite.com/app)（Lark 国际版）。
2. 点击 **创建企业自建应用**，填写名称和描述，创建应用。
3. 在 **凭证与基础信息** 页面记录 **App ID**（`cli_xxxxxxxxxxxx`）和
   **App Secret**——它们将填入 `config.json` 的 `app.id` 和 `app.secret`。

### 2. 开启机器人能力

在应用的 **应用能力** 页面，添加 **机器人** 能力。这是让应用能够收发 IM 消息的
前提。没有它，长连接没有内容可推送。

### 3. 授予权限

在 **权限管理** → **API 权限** 页面，添加以下 scope。前四个是 bridge 核心功能
必需的；最后两个仅在 `/open`（创建群并添加成员）时需要。

| Scope | 用途 |
|---|---|
| `im:message:send_as_bot` | 以机器人身份发送消息 / 卡片 |
| `im:message.p2p_msg:readonly` | 接收用户发给机器人的单聊（p2p）消息 |
| `im:message.group_at_msg:readonly` | 接收群聊中 @机器人 的消息 |
| `im:message.reaction:write` | 对用户消息添加 "typing" 表情回应以示确认 |
| `im:chat:create` | `/open`：创建绑定到工作目录的群 |
| `im:chat:members:write` | `/open`：向创建的群添加用户 |

添加 scope 后，点击 **创建版本** 并由租户管理员**审批**（如果是个人测试应用且
你自己是管理员，可自行审批）。scope 只有在版本审批通过并发布后才生效。

### 4. 订阅接收消息事件

在 **事件与回调** → **事件配置** 页面：

1. 将 **订阅方式** 设为 **使用长连接接收事件**。这是 bridge 使用的 WebSocket
   模式，无需填写请求 URL。
2. 在 **已添加事件** 区域，添加 **接收消息（im.message.receive_v1）**
   （接收消息 v2.0）。这是 bridge 唯一需要的事件。

> 长连接模式仅支持企业自建应用，正好与本 bridge 一致。SDK 在建连时处理鉴权，
> 事件以明文形式通过 WebSocket 推送。

### 5. 设置应用可用范围

在 **版本发布与发布** → **可用范围** 页面，添加你自己（或应能 DM 该机器人的
用户 / 部门）。然后 **创建版本** → **提交审核** → **审批**。审批通过后，机器人
即可在飞书 / Lark 中被访问：按名称搜索即可发起单聊，或将其加入群并 @mention。

### 6. 将凭证填入配置

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

如果你的应用在 Lark（国际版）租户上，将 `tenant` 设为 `"lark"` 而非 `"feishu"`。

## 安装

### 方式 A：下载预编译二进制

每个 release 会将预编译二进制发布到 GitHub Releases。从
[releases 页面](https://github.com/cyclinder/lark-acp-bridge/releases) 下载与你的
操作系统 / 架构匹配的版本，例如：

```bash
# Linux amd64
curl -L -o lark-acp-bridge \
  https://github.com/cyclinder/lark-acp-bridge/releases/latest/download/lark-acp-bridge-linux-amd64
chmod +x lark-acp-bridge
sudo mv lark-acp-bridge /usr/local/bin/
```

每个 release 的可用产物（命名为 `lark-acp-bridge-<os>-<arch>`）：

| 产物                          | OS      | 架构   |
|--------------------------------|---------|--------|
| `lark-acp-bridge-linux-amd64`  | Linux   | amd64  |
| `lark-acp-bridge-linux-arm64`  | Linux   | arm64  |
| `lark-acp-bridge-darwin-amd64` | macOS   | amd64  |
| `lark-acp-bridge-darwin-arm64` | macOS   | arm64  |

验证安装：

```bash
lark-acp-bridge help
```

### 方式 B：从源码构建

```bash
git clone https://github.com/cyclinder/lark-acp-bridge.git
cd lark-acp-bridge
go build -o lark-acp-bridge ./cmd/lark-acp-bridge
```

开发构建也可直接运行，无需生成二进制：

```bash
go run ./cmd/lark-acp-bridge run
```

### 发布

通过给 commit 打 tag（如 `v1.2.0`）并推送 tag 来发布。release 工作流会交叉编译
上述二进制并附加到 GitHub Release，使 `releases/latest/download/...` URL 保持
稳定。发布新 release 时，将构建好的二进制附加到 release notes，以便用户按方式 A
中的方式 `curl` 下载。

## 配置

创建 `~/.lark-acp-bridge/config.json`：

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

Lark（国际版）应用将 `tenant` 设为 `"lark"`。`defaultProvider` 默认为 `devin`；
设为 `codex` 或 `copilot` 可将其设为默认。`codex` 和 `copilot` 块是可选的——
省略即完全禁用这些 provider。

`copilot.permissions` 字段映射到 Copilot CLI 的审批标志：
`"allow-all"`（`--allow-all`：工具、路径、URL 全部放行——默认值）、
`"allow-all-tools"`（`--allow-all-tools`：工具自动审批，工作区外路径仍拒绝）、
或 `"read-only"`（尽力而为：`shell`/`edit`/`create` 拒绝——Copilot CLI 无内核
沙箱，依赖模型遵守工具拒绝）。

## 运行

CLI 提供 `run`、`status`、`stop`、`uninstall` 子命令。不带子命令时默认为 `run`
（前台运行），以保持原有行为。

### 前台运行（默认）

```bash
./lark-acp-bridge run
./lark-acp-bridge run -c /path/to/config.json
```

### 后台守护进程

```bash
./lark-acp-bridge run --detach
```

在新 session 中启动 bridge（shell 退出后仍存活），在 bridge home 下写入 PID 文件
和守护进程状态文件，并将 stdout/stderr 重定向到
`~/.lark-acp-bridge/logs/daemon-stdout.log`。

### systemd 服务

```bash
# system 范围（需要 root；unit 位于 /etc/systemd/system/）
sudo ./lark-acp-bridge run --mode systemd

# user 范围（unit 位于 ~/.config/systemd/user/；开启 lingering 以在注销后存活：
# loginctl enable-linger $USER）
./lark-acp-bridge run --mode systemd --user
```

这会生成 `lark-acp-bridge.service` unit，重载管理器，并执行
`systemctl enable --now`。bridge 随后在 systemd 下以 `Restart=on-failure` 运行。
如需覆盖 `ExecStart` 中的二进制路径，使用 `--binary /path/to/lark-acp-bridge`。

### 状态与停止

```bash
./lark-acp-bridge status   # 显示运行状态、pid/unit、运行时长、配置
./lark-acp-bridge stop     # SIGTERM（进程模式）或 systemctl stop（systemd）
```

`status` 和 `stop` 从守护进程状态文件（`~/.lark-acp-bridge/daemon.json`）自动检测
启动模式。过期状态（进程已退出 / unit 不活跃）会自动清理。

### 移除 systemd unit

```bash
sudo ./lark-acp-bridge uninstall
./lark-acp-bridge uninstall --user
```

## 快速上手（首次运行）

应用审批通过且 bridge 运行后，典型的首次使用流程为：

1. 在飞书 / Lark 中按名称搜索机器人并发起单聊（或将其加入群并 @mention）。
2. 发送 `/cd /path/to/your/project` 将该聊天绑定到工作目录。代理在此目录运行；
   会话状态以此聊天为作用域。
3. 发送 `/status` 确认 cwd、provider 和 model。
4. 发送任意普通消息——它将作为 prompt 转发给代理。回复会流式输出到一张实时
   卡片上（文本 + 工具调用 + 用量）。
5. 用 `/stop` 中途取消运行，`/new` 重置会话，`/help` 查看所有命令。

在群聊中，机器人仅在被 @mention 时响应。在单聊中，每条普通消息都是一个 prompt。
斜杠命令由本地处理，不消耗 token；只有普通消息会到达代理。

## 斜杠命令

所有命令均由 bridge 本地处理——它们不会调用代理子进程，也不消耗 token。

| 命令 | 作用 |
|---|---|
| `/help` | 动态帮助卡片：始终显示 bridge 命令，仅在选中 provider 时显示该 provider 专属命令 |
| `/new` `/reset` | 清除当前聊天会话（上下文丢弃；代理进程被回收） |
| `/cd <path>` | 切换此聊天的工作目录（重置会话） |
| `/ws` | 管理命名工作区别名：`/ws save <name>`、`/ws use <name>`、`/ws remove <name>` |
| `/open [path]` | 创建（或复用）绑定到 cwd 的飞书群；仅单聊可用 |
| `/status` | 显示当前 scope、cwd、会话、provider、model 及活跃运行状态 |
| `/pwd` | 打印当前工作目录 |
| `/stop` | 停止此聊天的活跃运行 |
| `/model` | 列出可用模型，当前模型会被标记 |
| `/model <N\|name>` | 切换模型（重置会话）；`<N>` 为 `/model` 列表的索引 |
| `/provider` | 列出已注册 provider；`/provider <id>` 按聊天覆盖默认值，`/provider default` 恢复（切换会清除会话） |
| `/resume` | 列出历史会话并用 `/resume <N>` 恢复其中一个 |

## 架构

完整设计文档见 [DESIGN.md](./DESIGN.md)，项目规则见 [AGENTS.md](./AGENTS.md)。

```
Feishu -> lark.Channel -> commands.Dispatch
  |                        |-- slash cmd -> 本地处理 (不耗 token)
  |                        |-- plain msg -> run.Executor
  |                                            |-- DevinAdapter
  |                                            |     |-- ACPClient (JSON-RPC over stdio)
  |                                            |     |-- devin acp 子进程
  |                                            |-- CardRenderer (流式卡片)
  |                                            v
  |                                          飞书卡片
```

## 开发

```bash
go build ./...
go vet ./...
go test ./...
```

## 许可证

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

完整许可证文本见 [LICENSE](LICENSE) 文件。vendored 依赖
`github.com/larksuite/oapi-sdk-go/v3` 保留其自身的 MIT 许可证。

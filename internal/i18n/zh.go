// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package i18n

// zh maps English message literals to their Chinese translations. Format
// verbs (%s, %d) must appear in the same order as in the English key.
var zh = map[string]string{
	// --- commands: session / cwd -------------------------------------------
	"Session cleared.": "会话已清除。",
	"Usage: `/cd <path>` — absolute, `~/sub`, or relative to the current cwd": "用法：`/cd <路径>` — 绝对路径、`~/子目录`，或相对当前工作目录的路径",
	"Invalid path: %s":                                  "无效路径：%s",
	"Switched cwd to `%s` (session reset).":             "工作目录已切换到 `%s`（会话已重置）。",
	"No working directory set. Use `/cd <path>` first.": "尚未设置工作目录，请先使用 `/cd <路径>`。",
	"Current directory: `%s`":                           "当前目录：`%s`",
	"No active run to stop.":                            "当前没有正在运行的任务。",
	"Stopped the active run.":                           "已停止当前运行。",

	// --- commands: help -----------------------------------------------------
	"`/model` — list available models (current marked); `/model N|name` switches": "`/model`（/模型）— 列出可用模型（标记当前）；`/model 序号|名称` 切换",
	"`/resume` — list saved sessions; `/resume N` to reconnect one":               "`/resume`（/恢复）— 列出已保存会话；`/resume 序号` 重新连接",
	"`/sessions` — list provider sessions; `/sessions N` to continue one":         "`/sessions`（/会话）— 列出提供方会话；`/sessions 序号` 继续某个会话",

	// --- commands: model ----------------------------------------------------
	"Unknown model: %s. Use `/model` to list.":                          "未知模型：%s。使用 `/model` 查看列表。",
	"Model switch failed: %s":                                           "模型切换失败：%s",
	"Switched model to `%s` (session preserved).":                       "已切换模型为 `%s`（会话保留）。",
	"Switched model to `%s` (session cleared; will apply on next run).": "已切换模型为 `%s`（会话已清除；下次运行时生效）。",

	// --- commands: resume / sessions ---------------------------------------
	"Usage: `/resume` to list, or `/resume N` to pick a session.":                      "用法：`/resume` 列出会话，`/resume 序号` 选择一个。",
	"Invalid session number %d. Use `/resume` to list (1-%d).":                         "无效的会话编号 %d。使用 `/resume` 查看列表（1-%d）。",
	"Resumed session `%s` (from scope `%s`). Next message will continue that context.": "已恢复会话 `%s`（来自作用域 `%s`）。下一条消息将继续该上下文。",
	"provider `%s` does not support listing sessions":                                  "提供方 `%s` 不支持列出会话",
	"Cannot list sessions: %s":                                                         "无法列出会话：%s",
	"Usage: `/sessions` to list, or `/sessions N` to pick a session.":                  "用法：`/sessions` 列出会话，`/sessions 序号` 选择一个。",
	"Invalid session number %d. Use `/sessions` to list (1-%d).":                       "无效的会话编号 %d。使用 `/sessions` 查看列表（1-%d）。",
	"Session `%s` is open in another client and cannot be continued here.":             "会话 `%s` 正在其他客户端中使用，无法在此继续。",
	"(untitled)": "（无标题）",
	"Switched to session `%s` (%s). Subsequent messages continue that session; cwd is now `%s`.": "已切换到会话 `%s`（%s）。后续消息将继续该会话；工作目录现为 `%s`。",

	// --- commands: provider -------------------------------------------------
	"Provider switching is not configured.":                             "未配置提供方切换功能。",
	"Unknown provider: `%s`. Use `/provider` to list.":                  "未知提供方：`%s`。使用 `/provider` 查看列表。",
	"Provider `%s` is not available (binary missing or not logged in).": "提供方 `%s` 不可用（缺少可执行文件或未登录）。",
	"Reverted to the default provider (session cleared).":               "已恢复为默认提供方（会话已清除）。",
	"Switched provider to `%s` (session cleared).":                      "已切换提供方为 `%s`（会话已清除）。",

	// --- commands: /ws ------------------------------------------------------
	"Usage: `/ws [list|save <name>|use <name>|remove <name>]`":                  "用法：`/ws [list|save <名称>|use <名称>|remove <名称>]`",
	"Usage: `/ws save <name>`":                                                  "用法：`/ws save <名称>`",
	"No working directory set. Use `/cd <path>` first, then `/ws save <name>`.": "尚未设置工作目录。请先 `/cd <路径>`，再 `/ws save <名称>`。",
	"Saved workspace alias `%s` -> `%s`.":                                       "已保存工作区别名 `%s` -> `%s`。",
	"Usage: `/ws use <name>`":                                                   "用法：`/ws use <名称>`",
	"No workspace alias named `%s`. Use `/ws` to list.":                         "没有名为 `%s` 的工作区别名。使用 `/ws` 查看列表。",
	"Alias `%s` points to `%s` which is no longer valid: %s":                    "别名 `%s` 指向的 `%s` 已失效：%s",
	"Switched to `%s` (`%s`). Session reset.":                                   "已切换到 `%s`（`%s`）。会话已重置。",
	"Usage: `/ws remove <name>`":                                                "用法：`/ws remove <名称>`",
	"No workspace alias named `%s`.":                                            "没有名为 `%s` 的工作区别名。",
	"Removed workspace alias `%s`.":                                             "已删除工作区别名 `%s`。",

	// --- commands: /open ----------------------------------------------------
	"Please use `/open` in a direct message with the bot.":                                                    "请在与机器人的私聊中使用 `/open`。",
	"Group creation is not configured on this bridge.":                                                        "此桥接未配置建群功能。",
	"Existing group **%s** is still around but I could not add you back: %s. Creating a new group instead.":   "群 **%s** 仍然存在，但无法将你重新加入：%s。将改为创建新群。",
	"Welcome back. This group is bound to `%s`.":                                                              "欢迎回来。本群已绑定到 `%s`。",
	"Reused existing group **%s** for `%s`.":                                                                  "已复用现有群 **%s**（绑定 `%s`）。",
	"Found an existing group **%s** on Feishu but could not add you: %s. Creating a new group instead.":       "在飞书上找到现有群 **%s**，但无法将你加入：%s。将改为创建新群。",
	"Reused existing group **%s** but failed to record the bind: %s":                                          "已复用现有群 **%s**，但绑定记录保存失败：%s",
	"Failed to create group: %s. Check the bot has `im:chat:create` permission.":                              "建群失败：%s。请检查机器人是否具有 `im:chat:create` 权限。",
	"Created group **%s** but failed to record the bind: %s":                                                  "已创建群 **%s**，但绑定记录保存失败：%s",
	"This group is bound to `%s`. Send any message (and `@bot` in groups) to start a run.":                    "本群已绑定到 `%s`。发送任意消息（群内需 `@机器人`）即可开始运行。",
	"Created group **%s** for `%s`, but could not add you automatically: %s. Please join the group manually.": "已创建群 **%s**（绑定 `%s`），但无法自动将你加入：%s。请手动加入该群。",
	"Created group **%s** for `%s`.":                                                                          "已创建群 **%s**（绑定 `%s`）。",
	"No working directory set. Use `/cd <path>` first, or pass a path to `/open <path>`.":                     "尚未设置工作目录。请先 `/cd <路径>`，或直接使用 `/open <路径>`。",

	// --- commands: /new-issue -----------------------------------------------
	"`/new-issue` is not available: the bridge is missing chat history access (grant `im:message.group_msg` and `im:resource`).":                                                                                                                                                                   "`/new-issue` 不可用：桥接缺少聊天历史读取权限（请授予 `im:message.group_msg` 和 `im:resource`）。",
	"Usage: `/new-issue <repo> [--last N] [--since today|24h|7d] [extra notes]`\n`<repo>` is required: `owner/repo`, a GitHub URL, or a project name (the agent locates the upstream repo and asks back when ambiguous).\nOn mobile you can skip the dashes: `last:200` and `since:24h` work too.": "用法：`/new-issue <仓库> [--last N] [--since today|24h|7d] [补充说明]`\n`<仓库>` 必填：`owner/repo`、GitHub 链接或项目名（agent 会自动定位上游仓库，歧义时会反问）。\n手机上可省略连字符：`last:200`、`since:24h` 同样有效。",
	"Failed to fetch chat history: %s":                 "获取聊天历史失败：%s",
	"Failed to create image dir: %s":                   "创建图片目录失败：%s",
	"No usable messages found in this chat's history.": "本聊天的历史中没有可用的消息。",
	"Collected %d messages":                            "已收集 %d 条消息",
	" and %d images":                                   "和 %d 张图片",
	" (fetch limit %d reached)":                        "（已达到拉取上限 %d）",
	"; generating the issue…":                          "；正在生成 issue…",

	// --- main: message pipeline ---------------------------------------------
	"Only the bridge owner can use this. Available to you: `/help`, `/status`, `/pwd`.":        "只有桥接所有者可以使用此功能。你可用的命令：`/help`、`/status`、`/pwd`。",
	"Cannot verify the bridge owner right now; only `/help`, `/status`, `/pwd` are available.": "暂时无法验证桥接所有者身份；当前仅可使用 `/help`、`/status`、`/pwd`。",
	"Command error: %s": "命令执行出错：%s",
	"No provider available for this chat. Use `/provider` to pick one.": "此聊天没有可用的提供方。使用 `/provider` 选择一个。",
	"Run failed: %s": "运行失败：%s",

	// --- card: help ---------------------------------------------------------
	"Help": "帮助",
	"_Chat in Feishu and let a local AI agent do the work for you._": "_在飞书里聊天，让本地 AI Agent 替你干活。_",
	"🔥 **Frequent**":                          "🔥 **常用**",
	"📁 **Workspace**":                         "📁 **工作区**",
	"🧠 **Session & model**":                   "🧠 **会话与模型**",
	"Anything else is sent to %s as a prompt.": "其余任何消息都会作为提示发送给 %s。",
	"`/new` `/reset` — clear the current chat session":       "`/new` `/reset`（/新建 /重置）— 清除当前聊天会话",
	"`/cd <path>` — switch working directory (resets session)": "`/cd <路径>`（/目录）— 切换工作目录（重置会话）",
	"`/ws` — manage named workspace aliases (`/ws save|use|remove <name>`)": "`/ws`（/工作区）— 管理工作区别名（`/ws save|use|remove <名称>`）",
	"`/open [path]` — create/reuse a group bound to a cwd (p2p only)":       "`/open [路径]`（/开群）— 创建/复用绑定到工作目录的群（仅私聊）",
	"`/status` — show current state":                                        "`/status`（/状态）— 查看当前状态",
	"`/pwd` — print the current working directory":                          "`/pwd`（/当前目录）— 显示当前工作目录",
	"`/stop` — stop the active run":                          "`/stop`（/停止）— 停止当前运行",
	"`/new-issue <repo> [--last N] [--since today|24h|7d]` — turn recent group messages into a GitHub issue in the given repo": "`/new-issue <仓库> [--last N] [--since today|24h|7d]`（/新建议题）— 将近期群消息整理成指定仓库的 GitHub issue",
	"↳ e.g. `/new-issue spidernet-io/spiderpool --last 100 focus on the RDMA discussion`": "↳ 例如：`/new-issue spidernet-io/spiderpool --last 100 重点看 RDMA 的讨论`",
	"↳ e.g. `/cd ~/projects/spiderpool`":                     "↳ 例如：`/cd ~/projects/spiderpool`",
	"↳ e.g. `/ws save spiderpool`, later `/ws use spiderpool`": "↳ 例如：`/ws save spiderpool`，之后 `/ws use spiderpool`",
	"`/provider` — list providers; `/provider <id>` to switch, `/provider default` to reset":                                   "`/provider`（/提供方）— 列出提供方；`/provider <id>` 切换，`/provider default` 重置",
	"`/help` — this help": "`/help`（/帮助）— 显示本帮助",

	// --- card: status -------------------------------------------------------
	"Status":             "状态",
	"(none)":             "（无）",
	"(unset)":            "（未设置）",
	"(agent default)":    "（agent 默认）",
	"**scope**: `%s`":    "**作用域**: `%s`",
	"**profile**: %s":    "**配置档**: %s",
	"**cwd**: `%s`":      "**工作目录**: `%s`",
	"**session**: `%s`":  "**会话**: `%s`",
	"**agent**: %s":      "**代理**: %s",
	"**model**: %s":      "**模型**: %s",
	"**active run**: %s": "**运行中**: %s",
	"yes":                "是",
	"no":                 "否",

	// --- card: models -------------------------------------------------------
	"Model": "模型",
	"No models available. Start a session first, or the agent did not advertise a model list.": "没有可用模型。请先开始一个会话，或该 agent 未提供模型列表。",
	"**Current model:** `%s`\n\n":                                           "**当前模型：** `%s`\n\n",
	"**Current model:** _none (provider default will apply)_\n\n":           "**当前模型：** _无（将使用提供方默认模型）_\n\n",
	"**Available models** (use `/model N` or `/model name` to switch):\n\n": "**可用模型**（使用 `/model 序号` 或 `/model 名称` 切换）：\n\n",
	"  <- current": "  <- 当前",

	// --- card: workspaces ---------------------------------------------------
	"Workspaces":                        "工作区",
	"**Current directory:** _none_\n\n": "**当前目录：** _无_\n\n",
	"**Current directory:** `%s`\n\n":   "**当前目录：** `%s`\n\n",
	"No saved workspace aliases.\n":     "暂无已保存的工作区别名。\n",
	"Use `/ws save <name>` to save the current cwd as a named alias.":                        "使用 `/ws save <名称>` 将当前目录保存为别名。",
	"**Saved aliases** (use `/ws use <name>` to switch, `/ws remove <name>` to delete):\n\n": "**已保存别名**（`/ws use <名称>` 切换，`/ws remove <名称>` 删除）：\n\n",

	// --- card: providers ----------------------------------------------------
	"Providers": "提供方",
	"**Providers** (use `/provider <id>` to switch, `/provider default` to reset):\n\n": "**提供方**（`/provider <id>` 切换，`/provider default` 重置）：\n\n",
	"  <- default":   "  <- 默认",
	" (unavailable)": "（不可用）",

	// --- card: resume -------------------------------------------------------
	"Resume": "恢复会话",
	"No saved sessions. Start a run first, then use `/resume` to reconnect.": "暂无已保存会话。先运行一次，再使用 `/resume` 重新连接。",
	"**Saved sessions** (use `/resume N` to reconnect):\n\n":                 "**已保存会话**（使用 `/resume 序号` 重新连接）：\n\n",
	"  <- current chat": "  <- 当前聊天",
	"(default)":         "（默认）",

	// --- card: provider sessions ---------------------------------------------
	"Sessions":                           "会话",
	"The provider reported no sessions.": "提供方没有任何会话。",
	"**Provider sessions** (use `/sessions N` to continue one):\n\n": "**提供方会话**（使用 `/sessions 序号` 继续）：\n\n",
	" (in use elsewhere)": "（其他客户端使用中）",
	"unknown time":        "未知时间",
	"\n_Showing the %d most recent of %d sessions._\n": "\n_仅显示最近 %d 条，共 %d 条会话。_\n",

	// --- card: streaming run --------------------------------------------------
	"_(... earlier plan omitted)_\n\n":    "_（……较早的计划已省略）_\n\n",
	"_(... earlier content omitted)_\n\n": "_（……较早内容已省略）_\n\n",
	"🧠 Thinking":                          "🧠 思考中",
	"_+ %d earlier tool calls_":           "_+ 前面还有 %d 次工具调用_",
	"tokens: %d | cost: $%.4f":            "token：%d | 费用：$%.4f",
	"running %s · last: %s (%s ago) | ":   "运行中 %s · 最近：%s（%s 前）| ",
	"thinking":                            "思考",
	"writing":                             "输出",
	"planning":                            "规划",
	"tool: ":                              "工具：",
	"tool done":                           "工具完成",
	"usage":                               "用量",
	"✅ Done":                              "✅ 完成",
	"❌ Error":                             "❌ 出错",
	"⏹️ Cancelled":                        "⏹️ 已取消",
	"✍️ Writing response...":              "✍️ 正在输出…",
	"🧠 Planning...":                       "🧠 规划中…",
	"🛠️ Running tool: ":                   "🛠️ 正在运行工具：",
	"🛠️ Running tool...":                  "🛠️ 正在运行工具…",
	"🧠 Thinking...":                       "🧠 思考中…",
	"running...":                          "运行中…",
	"✅ done":                              "✅ 完成",
	"❌ error":                             "❌ 出错",
	"⏹️ cancelled":                        "⏹️ 已取消",
	"unknown":                             "未知",
	"Output":                              "输出",
	"Error":                               "错误",
	"_running..._":                        "_运行中…_",
	"_no output_":                         "_无输出_",
	"\n\n_(body truncated, see /doctor or logs)_": "\n\n_（内容已截断，详见日志）_",
	"**Command**\n```bash\n%s\n```":               "**命令**\n```bash\n%s\n```",
	"**File** `%s`":                               "**文件** `%s`",
	"**Pattern** `%s`":                            "**模式** `%s`",
	"**Path** `%s`":                               "**路径** `%s`",
	"**URL** %s":                                  "**URL** %s",
	"**Query** `%s`":                              "**查询** `%s`",
}

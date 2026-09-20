// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// HistoryMessage is a minimal projection of one Lark chat message used to
// build the /new-issue transcript. It mirrors lark's normalized history
// items; keeping it here (like ChatInfo) avoids a commands -> lark import
// cycle.
type HistoryMessage struct {
	MessageID  string
	SenderID   string // open_id (users) or app_id (bots)
	SenderType string // "user" | "app" | "anonymous" | "unknown"
	CreateTime time.Time
	MsgType    string // "text", "post", "image", ...
	Text       string // extracted plain text ("" for pure image messages)
	ImageKeys  []string
}

// HistoryReader fetches chat history and message image resources. It is a
// seam so the commands package stays testable without a live Lark client;
// *lark.Channel implements it. Nil in deployments without the
// im:message:readonly scope.
type HistoryReader interface {
	// ListMessages returns up to limit messages from chatID in ascending
	// time order (oldest first), skipping recalled messages. A zero since
	// means no time filter.
	ListMessages(chatID string, limit int, since time.Time) ([]HistoryMessage, error)
	// DownloadImage downloads one image attached to a message into destDir
	// and returns the local file path.
	DownloadImage(messageID, imageKey, destDir string) (string, error)
}

// newIssueOpts is the parsed form of /new-issue arguments.
type newIssueOpts struct {
	Repo  string    // required: owner/repo, a git URL, or a bare project name
	Last  int       // 0 = config default
	Since time.Time // zero = no time filter
	Extra string    // free-form extra instructions for the agent
}

// parseNewIssueArgs parses `/new-issue <repo> [--last N] [--since today|Nh|Nd]
// [extra...]`. The first positional token is the repository reference; the
// rest are extra instructions. now anchors relative --since values.
func parseNewIssueArgs(args string, now time.Time) (newIssueOpts, error) {
	var o newIssueOpts
	toks := strings.Fields(args)
	var extra []string
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		switch {
		case tok == "--last" || strings.HasPrefix(tok, "--last="):
			val := strings.TrimPrefix(tok, "--last=")
			if val == "--last" || val == "" {
				if i+1 >= len(toks) {
					return o, fmt.Errorf("--last needs a number, e.g. --last 200")
				}
				i++
				val = toks[i]
			}
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("--last needs a positive number, got %q", val)
			}
			o.Last = n
		case tok == "--since" || strings.HasPrefix(tok, "--since="):
			val := strings.TrimPrefix(tok, "--since=")
			if val == "--since" || val == "" {
				if i+1 >= len(toks) {
					return o, fmt.Errorf("--since needs a value: today, <N>h, or <N>d")
				}
				i++
				val = toks[i]
			}
			t, err := parseSince(val, now)
			if err != nil {
				return o, err
			}
			o.Since = t
		case o.Repo == "":
			o.Repo = tok
		default:
			extra = append(extra, tok)
		}
	}
	o.Extra = strings.Join(extra, " ")
	return o, nil
}

// parseSince converts "today", "<N>h", or "<N>d" into an absolute time.
func parseSince(val string, now time.Time) (time.Time, error) {
	if val == "today" {
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(val, "h")); err == nil && strings.HasSuffix(val, "h") && n > 0 {
		return now.Add(-time.Duration(n) * time.Hour), nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(val, "d")); err == nil && strings.HasSuffix(val, "d") && n > 0 {
		return now.AddDate(0, 0, -n), nil
	}
	return time.Time{}, fmt.Errorf("--since accepts today, <N>h, or <N>d, got %q", val)
}

// transcriptLine is one kept message during transcript assembly.
type transcriptLine struct {
	when   time.Time
	alias  string
	text   string
	images []imageRef
}

type imageRef struct {
	messageID string
	key       string
}

// imageFetcher downloads one image and returns its local path. ok=false
// means the download failed and the transcript should note it.
type imageFetcher func(messageID, imageKey string) (path string, ok bool)

// buildTranscript renders history messages into the prompt transcript:
// bot/app messages and local slash commands are skipped, senders are
// anonymized (用户A, 用户B, ... in order of first appearance), the total text
// is trimmed to charBudget by dropping the oldest lines, and up to maxImages
// images from the kept lines (newest first) are downloaded via fetch.
// Returns the transcript, the number of messages included, and the number of
// images downloaded.
func buildTranscript(msgs []HistoryMessage, charBudget, maxImages int, fetch imageFetcher) (string, int, int) {
	aliases := map[string]string{}
	alias := func(senderID string) string {
		if a, ok := aliases[senderID]; ok {
			return a
		}
		a := "用户" + string(rune('A'+len(aliases)%26))
		if n := len(aliases) / 26; n > 0 {
			a += strconv.Itoa(n + 1)
		}
		aliases[senderID] = a
		return a
	}

	var lines []transcriptLine
	total := 0
	for _, m := range msgs {
		if m.SenderType != "user" && m.SenderType != "anonymous" {
			continue // skip the bot's own replies and other apps
		}
		text := strings.TrimSpace(m.Text)
		if strings.HasPrefix(text, "/") && len(m.ImageKeys) == 0 {
			continue // skip bridge slash commands
		}
		if text == "" && len(m.ImageKeys) == 0 {
			continue
		}
		l := transcriptLine{when: m.CreateTime, alias: alias(m.SenderID), text: text}
		for _, k := range m.ImageKeys {
			l.images = append(l.images, imageRef{messageID: m.MessageID, key: k})
		}
		lines = append(lines, l)
		total += len(text) + 32 // rough per-line overhead (timestamp, alias)
	}

	// Trim oldest lines until within budget.
	drop := 0
	for total > charBudget && drop < len(lines)-1 {
		total -= len(lines[drop].text) + 32
		drop++
	}
	lines = lines[drop:]

	// Download images from the kept lines, newest first, up to maxImages.
	imagePaths := map[imageRef]string{}
	downloaded := 0
	if fetch != nil {
		for i := len(lines) - 1; i >= 0 && downloaded < maxImages; i-- {
			for _, ref := range lines[i].images {
				if downloaded >= maxImages {
					break
				}
				if p, ok := fetch(ref.messageID, ref.key); ok {
					imagePaths[ref] = p
					downloaded++
				}
			}
		}
	}

	var sb strings.Builder
	for _, l := range lines {
		sb.WriteByte('[')
		sb.WriteString(l.when.Format("2006-01-02 15:04"))
		sb.WriteString("] ")
		sb.WriteString(l.alias)
		sb.WriteString(": ")
		sb.WriteString(l.text)
		for _, ref := range l.images {
			if p, ok := imagePaths[ref]; ok {
				sb.WriteString(fmt.Sprintf(" [图片: %s]", p))
			} else {
				sb.WriteString(" [图片(未下载)]")
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String(), len(lines), downloaded
}

// defaultNewIssuePrompt is the built-in prompt template. Placeholders:
// {{repo}}, {{count}}, {{extra}}, {{history}}.
const defaultNewIssuePrompt = `你收到了一段飞书群聊的讨论记录。请理解讨论内容，并在 GitHub 仓库创建一个 issue。

目标仓库：{{repo}}

要求：
1. 若目标仓库不是完整的 owner/repo 或 URL，先用 gh search repos 等方式定位它对应的 GitHub 官方主仓库（upstream，非 fork）。
2. 如果无法唯一确定目标仓库，绝对不要创建 issue：回复候选仓库列表（owner/repo 和一句话简介），请用户确认后重新执行。
3. 确定仓库后，先查看它的 issue 模板（.github/ISSUE_TEMPLATE/ 目录或 ISSUE_TEMPLATE.md，可用 gh api 读取）。有模板时选择最匹配的一个（bug report / feature request 等），严格按模板的结构和必填项撰写；没有模板则采用：背景、问题描述、复现步骤（如有）、期望行为。
4. 从讨论中提炼出问题或需求，撰写清晰的 issue 标题与正文。标题用英文。
5. 正文写两个版本：先完整英文版，再加一个中文版（用 "## 中文版本" 分节），两版内容一致。中文版必须保留模板原有的标题、字段名和结构（不翻译模板本身），只把填写的内容翻译成中文。
6. 讨论记录中标注了本地图片路径的，先查看图片内容再撰写。
7. 目标仓库唯一确定后无需向我确认，直接用 gh issue create 创建。
8. 创建成功后，回复 issue 链接和一句话摘要；失败时说明原因，不要重试超过一次。
{{extra}}
——— 讨论记录（共 {{count}} 条，按时间升序，发言人已匿名为 用户A/用户B…）———
{{history}}`

// renderNewIssuePrompt fills the prompt template. An empty template uses the
// built-in default. repo must be non-empty (enforced by the handler).
func renderNewIssuePrompt(template, repo, extra, history string, count int) string {
	if template == "" {
		template = defaultNewIssuePrompt
	}
	extraLine := ""
	if extra != "" {
		extraLine = "附加要求：" + extra + "\n"
	}
	r := strings.NewReplacer(
		"{{repo}}", repo,
		"{{extra}}", extraLine,
		"{{count}}", strconv.Itoa(count),
		"{{history}}", history,
	)
	return r.Replace(template)
}

// handleNewIssue implements /new-issue: it pulls the group's recent history,
// renders it into the issue prompt, and launches an agent run that creates
// the GitHub issue directly.
func handleNewIssue(args string, ctx *Context) error {
	if ctx.History == nil || ctx.StartRun == nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			"`/new-issue` is not available: the bridge is missing chat history access (grant `im:message.group_msg` and `im:resource`).",
			ctx.MessageID)
	}
	opts, err := parseNewIssueArgs(args, time.Now())
	if err != nil || opts.Repo == "" {
		usage := "Usage: `/new-issue <repo> [--last N] [--since today|24h|7d] [extra notes]`\n" +
			"`<repo>` is required: `owner/repo`, a GitHub URL, or a project name (the agent locates the upstream repo and asks back when ambiguous)."
		if err != nil {
			usage += fmt.Sprintf("\n%s", err)
		}
		return ctx.Sender.SendMarkdown(ctx.ChatID, usage, ctx.MessageID)
	}
	// No /cd is required: issue creation runs `gh` against the remote repo,
	// so any directory works. Fall back to the workspace default, then home.
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		} else {
			cwd = os.TempDir()
		}
	}
	limit := opts.Last
	if limit == 0 {
		limit = ctx.Config.NewIssueMaxMessages()
	}
	msgs, err := ctx.History.ListMessages(ctx.ChatID, limit, opts.Since)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Failed to fetch chat history: %s", err), ctx.MessageID)
	}
	imgDir, err := os.MkdirTemp("", "lark-new-issue-*")
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Failed to create image dir: %s", err), ctx.MessageID)
	}
	fetch := func(messageID, key string) (string, bool) {
		p, err := ctx.History.DownloadImage(messageID, key, imgDir)
		return p, err == nil
	}
	history, count, images := buildTranscript(msgs, ctx.Config.NewIssueCharBudget(), ctx.Config.NewIssueMaxImages(), fetch)
	if count == 0 {
		_ = os.RemoveAll(imgDir)
		return ctx.Sender.SendMarkdown(ctx.ChatID, "No usable messages found in this chat's history.", ctx.MessageID)
	}
	prompt := renderNewIssuePrompt(ctx.Config.NewIssuePromptTemplate(), opts.Repo, opts.Extra, history, count)
	note := fmt.Sprintf("Collected %d messages", count)
	if images > 0 {
		note += fmt.Sprintf(" and %d images", images)
	}
	if len(msgs) == limit {
		note += fmt.Sprintf(" (fetch limit %d reached)", limit)
	}
	note += "; generating the issue…"
	if err := ctx.Sender.SendMarkdown(ctx.ChatID, note, ctx.MessageID); err != nil {
		return err
	}
	ctx.StartRun(prompt, cwd)
	return nil
}

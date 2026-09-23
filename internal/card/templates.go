// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package card builds Feishu interactive card payloads and drives the
// streaming run-card state machine.
package card

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/i18n"
)

// Card is a minimal Feishu card payload. The lark package marshals it to the
// SDK's card type before sending.
type Card struct {
	Config   CardConfig `json:"config"`
	Header   CardHeader `json:"header"`
	Elements []Element  `json:"elements"`
}

type CardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
	UpdateMulti    bool `json:"update_multi"`
}

type CardHeader struct {
	Title CardText `json:"title"`
}

type CardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// Element is one card section. The lark package renders these into the SDK's
// element types; here we keep a union of what the bridge needs: plain divs,
// markdown divs, hr, and collapsible_panel (used for plan + tool calls so
// they are visually distinct from the agent's prose and don't rely on
// lark_md's limited blockquote/code-fence support).
type Element struct {
	Tag             string       `json:"tag"`
	Content         string       `json:"content,omitempty"`
	Text            *CardText    `json:"text,omitempty"`
	Elements        []Element    `json:"elements,omitempty"`
	Expanded        bool         `json:"expanded,omitempty"`
	Header          *PanelHeader `json:"header,omitempty"`
	Border          *PanelBorder `json:"border,omitempty"`
	VerticalSpacing string       `json:"vertical_spacing,omitempty"`
	Padding         string       `json:"padding,omitempty"`
	TextSize        string       `json:"text_size,omitempty"`
}

// PanelHeader is the header of a collapsible_panel.
type PanelHeader struct {
	Title         CardText `json:"title"`
	VerticalAlign string   `json:"vertical_align,omitempty"`
}

// PanelBorder is the border styling of a collapsible_panel.
type PanelBorder struct {
	Color        string `json:"color,omitempty"`
	CornerRadius string `json:"corner_radius,omitempty"`
}

func md(content string) Element {
	return Element{Tag: "div", Text: &CardText{Tag: "lark_md", Content: content}}
}

// markdown creates a top-level markdown element. Unlike md() (which uses
// div + lark_md and only supports bold/italic/links), the markdown element
// supports the full Feishu markdown subset: headers (#, ##), lists, tables,
// code blocks, etc. Use this for the agent's message text.
func markdown(content string) Element {
	return Element{Tag: "markdown", Content: content}
}

// markdownSmall is markdown() with notation text size, used inside
// collapsible panel bodies.
func markdownSmall(content string) Element {
	return Element{Tag: "markdown", Content: content, TextSize: "notation"}
}

func mdSmall(content string) Element {
	return Element{Tag: "div", Text: &CardText{Tag: "lark_md", Content: content}, TextSize: "notation"}
}

func plainText(content string) Element {
	return Element{Tag: "div", Text: &CardText{Tag: "plain_text", Content: content}}
}

var hr = Element{Tag: "hr"}

// collapsiblePanel builds a collapsible_panel element. When expanded is true
// the panel starts open (used for the active plan / running tool); when false
// it starts collapsed (used for completed tools so the card stays compact).
func collapsiblePanel(titleMd string, bodyMd string, expanded bool, borderColor string) Element {
	border := &PanelBorder{Color: borderColor, CornerRadius: "5px"}
	if borderColor == "" {
		border = nil
	}
	return Element{
		Tag:             "collapsible_panel",
		Expanded:        expanded,
		Header:          &PanelHeader{Title: CardText{Tag: "markdown", Content: titleMd}, VerticalAlign: "center"},
		Border:          border,
		VerticalSpacing: "8px",
		Padding:         "8px 8px 8px 8px",
		Elements:        []Element{markdownSmall(bodyMd)},
	}
}

func shell(title string, elements []Element) Card {
	return Card{
		Config:   CardConfig{WideScreenMode: true, UpdateMulti: true},
		Header:   CardHeader{Title: CardText{Tag: "plain_text", Content: title}},
		Elements: elements,
	}
}

// --- help card ------------------------------------------------------------

// HelpCard builds the dynamic /help card. Bridge commands are always shown,
// grouped by frequency of use and numbered continuously; agentCommands
// (model/session commands specific to the selected provider) are merged into
// the session group and shown only when a provider is selected (always in v1).
// Command lines avoid inline code markup because Feishu clients render
// backtick-heavy lines poorly; examples sit on an indented line below.
func HelpCard(agentName string, agentCommands []string) Card {
	var b strings.Builder
	n := 0
	section := func(header string, entries []helpEntry) {
		b.WriteString(i18n.T(header))
		b.WriteByte('\n')
		for _, e := range entries {
			n++
			b.WriteString(fmt.Sprintf("**%d.** %s\n", n, i18n.T(e.cmd)))
			for i, ex := range e.examples {
				b.WriteString(fmt.Sprintf("　　%c. ", 'a'+i))
				b.WriteString(i18n.T(ex))
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}
	b.WriteString(i18n.T("_Chat in Feishu and let a local AI agent do the work for you._"))
	b.WriteString("\n\n")
	section("🔥 **Frequent**", frequentHelpCommands)
	section("📁 **Workspace**", workspaceHelpCommands)
	sessionEntries := make([]helpEntry, 0, len(sessionHelpCommands)+len(agentCommands))
	sessionEntries = append(sessionEntries, sessionHelpCommands...)
	for _, c := range agentCommands {
		sessionEntries = append(sessionEntries, helpEntry{cmd: c})
	}
	section("🧠 **Session & model**", sessionEntries)
	b.WriteString(i18n.T("/help — this help"))
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf(i18n.T("Anything else is sent to %s as a prompt."), agentName))
	return shell(i18n.T("Help"), []Element{markdown(b.String())})
}

// helpEntry is one numbered command line in the /help card, with
// optional example lines rendered indented below it.
type helpEntry struct {
	cmd      string
	examples []string
}

var frequentHelpCommands = []helpEntry{
	{
		cmd: "/new-issue <repo> [--last N] [--since today|24h|7d] [extra prompt] — summarize recent group messages into a GitHub issue",
		examples: []string{
			"Collect the latest 500 group messages, auto-summarize and file an issue (default): /new-issue spidernet-io/spiderpool",
			"Collect only the last 100 messages: /new-issue spidernet-io/spiderpool --last 100",
			"Extra free text guides the summary: /new-issue spidernet-io/spiderpool focus on the RDMA discussion",
		},
	},
	{cmd: "/new /reset — clear the current chat session"},
	{cmd: "/status — show current state"},
}

var workspaceHelpCommands = []helpEntry{
	{
		cmd:      "/cd <path> — switch working directory (resets session)",
		examples: []string{"e.g. /cd ~/projects/spiderpool"},
	},
	{cmd: "/pwd — print the current working directory"},
	{
		cmd:      "/ws — manage named workspace aliases (/ws save|use|remove <name>)",
		examples: []string{"e.g. /ws save spiderpool, later /ws use spiderpool"},
	},
	{cmd: "/open [path] — create/reuse a group bound to a cwd (p2p only)"},
}

var sessionHelpCommands = []helpEntry{
	{cmd: "/stop — stop the active run"},
	{cmd: "/provider — list providers; /provider <id> to switch, /provider default to reset"},
}

// --- status card ----------------------------------------------------------

// StatusInfo describes the data shown on /status.
type StatusInfo struct {
	Profile   string
	Cwd       string
	SessionID string
	Model     string
	AgentName string
	ActiveRun bool
	Scope     string
	ChatMode  string
}

func StatusCard(info StatusInfo) Card {
	sessionLine := info.SessionID
	if sessionLine == "" {
		sessionLine = i18n.T("(none)")
	} else if len(sessionLine) > 12 {
		sessionLine = sessionLine[:12] + "..."
	}
	cwdLine := info.Cwd
	if cwdLine == "" {
		cwdLine = i18n.T("(unset)")
	}
	modelLine := info.Model
	if modelLine == "" {
		modelLine = i18n.T("(agent default)")
	}
	lines := []string{
		fmt.Sprintf(i18n.T("**scope**: `%s`"), info.Scope),
		fmt.Sprintf(i18n.T("**profile**: %s"), info.Profile),
		fmt.Sprintf(i18n.T("**cwd**: `%s`"), cwdLine),
		fmt.Sprintf(i18n.T("**session**: `%s`"), sessionLine),
		fmt.Sprintf(i18n.T("**agent**: %s"), info.AgentName),
		fmt.Sprintf(i18n.T("**model**: %s"), modelLine),
		fmt.Sprintf(i18n.T("**active run**: %s"), boolStr(info.ActiveRun, i18n.T("yes"), i18n.T("no"))),
	}
	return shell(i18n.T("Status"), []Element{markdown(strings.Join(lines, "\n"))})
}

// --- models card ----------------------------------------------------------

// ModelsCard lists available models with sequence numbers and marks the
// current one. The current model is also stated up front so the user can
// see it at a glance even when the list is long.
func ModelsCard(models []ModelEntry, current string) Card {
	var b strings.Builder
	if len(models) == 0 {
		b.WriteString(i18n.T("No models available. Start a session first, or the agent did not advertise a model list."))
	} else {
		if current != "" {
			b.WriteString(fmt.Sprintf(i18n.T("**Current model:** `%s`\n\n"), current))
		} else {
			b.WriteString(i18n.T("**Current model:** _none (provider default will apply)_\n\n"))
		}
		b.WriteString(i18n.T("**Available models** (use `/model N` or `/model name` to switch):\n\n"))
		for i, m := range models {
			marker := ""
			if m.Value == current {
				marker = i18n.T("  <- current")
			}
			b.WriteString(fmt.Sprintf("**%d.** `%s` — %s%s\n", i+1, m.Value, m.Name, marker))
		}
	}
	return shell(i18n.T("Model"), []Element{markdown(b.String())})
}

// ModelEntry is one row in the models card.
type ModelEntry struct {
	Value string
	Name  string
}

// FallbackModels is used when the agent does not expose a dynamic model
// list. These are common Devin model short names.
var FallbackModels = []ModelEntry{
	{Value: "adaptive", Name: "Adaptive (auto)"},
	{Value: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{Value: "claude-opus-4-8-medium", Name: "Claude Opus 4.8 Medium"},
	{Value: "gpt-5-5-medium", Name: "GPT-5.5 Medium"},
	{Value: "gemini-3-5-flash-medium", Name: "Gemini 3.5 Flash Medium"},
	{Value: "swe-1-6", Name: "SWE-1.6"},
	{Value: "swe-1-6-fast", Name: "SWE-1.6 Fast"},
	{Value: "glm-5-2", Name: "GLM-5.2"},
	{Value: "kimi-k2-7", Name: "Kimi K2.7"},
}

// fallbackCodexModels lists common Codex CLI model ids.
var fallbackCodexModels = []ModelEntry{
	{Value: "gpt-5", Name: "GPT-5"},
	{Value: "gpt-5-codex", Name: "GPT-5 Codex"},
	{Value: "codex-mini-latest", Name: "Codex Mini"},
	{Value: "o3", Name: "o3"},
	{Value: "o4-mini", Name: "o4-mini"},
}

// fallbackCopilotModels lists common GitHub Copilot CLI model ids.
var fallbackCopilotModels = []ModelEntry{
	{Value: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5"},
	{Value: "claude-opus-4.5", Name: "Claude Opus 4.5"},
	{Value: "claude-haiku-4.5", Name: "Claude Haiku 4.5"},
	{Value: "gpt-5", Name: "GPT-5"},
	{Value: "gpt-4.1", Name: "GPT-4.1"},
	{Value: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
}

// FallbackModelsFor returns the built-in model table for a provider, used
// when the agent does not expose a dynamic model list. Unknown providers
// fall back to the Devin table for backward compatibility.
func FallbackModelsFor(providerID string) []ModelEntry {
	switch providerID {
	case "codex":
		return fallbackCodexModels
	case "copilot":
		return fallbackCopilotModels
	default:
		return FallbackModels
	}
}

// --- workspaces card ------------------------------------------------------

// WorkspaceEntry is one named alias row in the /ws card.
type WorkspaceEntry struct {
	Name    string
	Cwd     string
	Current bool
}

// WorkspacesCard lists named workspace aliases and marks the one matching
// the current cwd. The current cwd is also stated up front so the user can
// see it at a glance even when there are no aliases yet.
func WorkspacesCard(current string, entries []WorkspaceEntry) Card {
	var b strings.Builder
	if current == "" {
		b.WriteString(i18n.T("**Current directory:** _none_\n\n"))
	} else {
		b.WriteString(fmt.Sprintf(i18n.T("**Current directory:** `%s`\n\n"), current))
	}
	if len(entries) == 0 {
		b.WriteString(i18n.T("No saved workspace aliases.\n"))
		b.WriteString(i18n.T("Use `/ws save <name>` to save the current cwd as a named alias."))
	} else {
		b.WriteString(i18n.T("**Saved aliases** (use `/ws use <name>` to switch, `/ws remove <name>` to delete):\n\n"))
		for i, e := range entries {
			marker := ""
			if e.Current {
				marker = i18n.T("  <- current")
			}
			b.WriteString(fmt.Sprintf("**%d.** **%s** -> `%s`%s\n", i+1, e.Name, e.Cwd, marker))
		}
	}
	return shell(i18n.T("Workspaces"), []Element{markdown(b.String())})
}

// --- provider card --------------------------------------------------------

// ProviderRow is one entry in the /provider list.
type ProviderRow struct {
	ID          string
	DisplayName string
	Available   bool
	Current     bool
	Default     bool
}

// ProvidersCard lists registered providers. The current one (effective for
// this scope) and the default are marked so the user knows what a bare
// `/provider <id>` or `/provider default` will do.
func ProvidersCard(rows []ProviderRow) Card {
	var b strings.Builder
	b.WriteString(i18n.T("**Providers** (use `/provider <id>` to switch, `/provider default` to reset):\n\n"))
	for _, r := range rows {
		marker := ""
		switch {
		case r.Current:
			marker = i18n.T("  <- current")
		case r.Default:
			marker = i18n.T("  <- default")
		}
		state := ""
		if !r.Available {
			state = i18n.T(" (unavailable)")
		}
		b.WriteString(fmt.Sprintf("- `%s` — %s%s%s\n", r.ID, r.DisplayName, state, marker))
	}
	return shell(i18n.T("Providers"), []Element{markdown(b.String())})
}

// --- resume card ----------------------------------------------------------
// ResumeRow is one entry in the resume list.
type ResumeRow struct {
	Index     int
	Scope     string
	SessionID string
	Cwd       string
	Model     string
}

// ResumeCard lists past sessions with sequence numbers. The current scope
// is marked so the user knows which one will be resumed by `/resume N`.
func ResumeCard(rows []ResumeRow, currentScope string) Card {
	var b strings.Builder
	if len(rows) == 0 {
		b.WriteString(i18n.T("No saved sessions. Start a run first, then use `/resume` to reconnect."))
	} else {
		b.WriteString(i18n.T("**Saved sessions** (use `/resume N` to reconnect):\n\n"))
		for _, r := range rows {
			marker := ""
			if r.Scope == currentScope {
				marker = i18n.T("  <- current chat")
			}
			sid := r.SessionID
			if len(sid) > 12 {
				sid = sid[:12] + "..."
			}
			cwd := r.Cwd
			if cwd == "" {
				cwd = i18n.T("(unset)")
			}
			model := r.Model
			if model == "" {
				model = i18n.T("(default)")
			}
			b.WriteString(fmt.Sprintf("**%d.** `%s` | session `%s` | cwd `%s` | model %s%s\n",
				r.Index, r.Scope, sid, cwd, model, marker))
		}
	}
	return shell(i18n.T("Resume"), []Element{markdown(b.String())})
}

// --- provider sessions card ------------------------------------------------

// SessionRow is one entry in the /sessions list (a provider-side session).
type SessionRow struct {
	Index     int
	SessionID string
	Title     string
	Cwd       string
	UpdatedAt time.Time
	Locked    bool
	Current   bool
}

// MaxSessionRows caps how many sessions the /sessions card renders. Feishu
// cards have a per-element size limit (~30KB); a large provider session
// store (hundreds of entries, ~50KB rendered) would make the card
// unsendable. The list is sorted most-recently-updated first, so the cap
// keeps the sessions a user is most likely to pick.
const MaxSessionRows = 20

// SessionsCard lists the provider's sessions with sequence numbers. The
// session currently bound to this scope is marked so the user knows which
// one future messages continue. Locked sessions are open in another client
// and cannot be selected. total is the provider's full session count; when
// it exceeds len(rows) a footer notes the truncation.
func SessionsCard(rows []SessionRow, total int) Card {
	var b strings.Builder
	if len(rows) == 0 {
		b.WriteString(i18n.T("The provider reported no sessions."))
	} else {
		b.WriteString(i18n.T("**Provider sessions** (use `/sessions N` to continue one):\n\n"))
		for _, r := range rows {
			marker := ""
			if r.Current {
				marker = i18n.T("  <- current chat")
			}
			state := ""
			if r.Locked {
				state = i18n.T(" (in use elsewhere)")
			}
			title := r.Title
			if title == "" {
				title = i18n.T("(untitled)")
			}
			updated := i18n.T("unknown time")
			if !r.UpdatedAt.IsZero() {
				updated = r.UpdatedAt.Local().Format("2006-01-02 15:04")
			}
			cwd := r.Cwd
			if cwd == "" {
				cwd = i18n.T("(unset)")
			}
			b.WriteString(fmt.Sprintf("**%d.** **%s** | `%s` | cwd `%s` | %s%s%s\n",
				r.Index, title, r.SessionID, cwd, updated, state, marker))
		}
		if total > len(rows) {
			b.WriteString(fmt.Sprintf(i18n.T("\n_Showing the %d most recent of %d sessions._\n"), len(rows), total))
		}
	}
	return shell(i18n.T("Sessions"), []Element{markdown(b.String())})
}

// --- run card (streaming) ------------------------------------------------

// Caps that keep the streaming card bounded. Feishu cards have a per-element
// size limit (~30KB) and an overall element count limit; without caps a long
// agent turn (huge file dump, long essay, dozens of tool calls) makes the
// card grow until it either 400s on update or becomes unreadably long.
const (
	// maxTextRunes is the cap on the rendered agent-message text region.
	// We keep the TAIL (most recent output) so streaming shows what the
	// agent is writing right now; earlier content is dropped with a marker.
	maxTextRunes = 4000
	// maxPlanRunes caps the plan/reasoning region for the same reason.
	maxPlanRunes = 1500
	// maxVisibleTools is how many recent tool entries are rendered in full.
	// Older tools are collapsed into a single "+ N earlier tools" line so
	// the tool log does not grow unbounded across a long agentic turn.
	maxVisibleTools = 8
)

// RunState is the state machine for one streaming run card.
//
// Content is modeled as an ordered list of blocks so text and tool calls are
// interleaved chronologically: a tool call appears where it happened in the
// turn, and the agent's final message naturally lands at the bottom (instead
// of being pushed up by a separate "Tools" section appended after the text).
type RunState struct {
	status      runStatus
	phase       runPhase
	blocks      []block
	planText    string // current plan snapshot (replaced, not appended)
	planDropped bool   // true when an earlier plan snapshot was truncated
	usage       *usageEntry
	sessionID   string
	stopReason  string
	errMsg      string

	// Heartbeat bookkeeping. startedAt is set at creation; lastActivityAt
	// and lastActivity are updated on every Reduce so the card can show
	// "running 12s · last: tool: foo (3s ago)" while the agent is silent.
	startedAt      time.Time
	lastActivityAt time.Time
	lastActivity   string
}

// block is one chronological segment of the run. A text block accumulates
// consecutive text deltas (streaming); a tool block is one tool call.
// textBuf is a pointer so blocks can be stored in a slice without copying
// the strings.Builder (which panics on copy).
type block struct {
	kind    string // "text" | "tool"
	textBuf *strings.Builder
	tool    toolEntry
}

type runStatus int

const (
	statusRunning runStatus = iota
	statusDone
	statusError
	statusCancelled
)

// runPhase is the current high-level activity of the agent, used to drive the
// prominent status indicator (card header + status line). It is derived from
// the most recent event so the user can always tell what the agent is doing
// even while it stays silent for a while.
type runPhase int

const (
	phaseThinking    runPhase = iota // no events yet, or between steps
	phasePlanning                    // agent emitted a plan/reasoning chunk
	phaseToolRunning                 // a tool call is in flight
	phaseWriting                     // agent is streaming message text
)

type toolEntry struct {
	id      string
	name    string
	summary string // one-line input summary (command, file path, etc.)
	status  string // running | done | failed | cancelled
	input   json.RawMessage
	output  string
}

type usageEntry struct {
	inputTokens  int
	outputTokens int
	costUSD      float64
}

// NewRunState creates a RunState in the running state.
func NewRunState() *RunState {
	now := time.Now()
	return &RunState{
		status:         statusRunning,
		phase:          phaseThinking,
		startedAt:      now,
		lastActivityAt: now,
		lastActivity:   i18n.T("thinking"),
	}
}

// Reduce applies one agent event to the state.
func (s *RunState) Reduce(ev agent.Event) {
	now := time.Now()
	s.lastActivityAt = now
	switch ev.Type {
	case agent.EventText:
		// Append to the last block if it is an open text block; otherwise
		// start a new text block so tool calls stay ordered between texts.
		if n := len(s.blocks); n > 0 && s.blocks[n-1].kind == "text" {
			s.blocks[n-1].textBuf.WriteString(ev.Delta)
			trimTail(s.blocks[n-1].textBuf, maxTextRunes)
		} else {
			b := block{kind: "text", textBuf: &strings.Builder{}}
			b.textBuf.WriteString(ev.Delta)
			trimTail(b.textBuf, maxTextRunes)
			s.blocks = append(s.blocks, b)
		}
		s.phase = phaseWriting
		s.lastActivity = i18n.T("writing")
	case agent.EventThinking:
		// Two reasoning styles share this event:
		//   - Snapshot (Devin ACP plan): ev.Delta is the FULL plan on
		//     every event. Replace so the panel always shows the current
		//     snapshot; appending would stack N copies of the whole plan.
		//   - Delta (Copilot assistant.reasoning_delta): ev.Delta is one
		//     incremental chunk. Append, with the same tail cap as text so
		//     a long reasoning stream stays bounded.
		if ev.Snapshot {
			if len([]rune(s.planText)) > maxPlanRunes && len([]rune(ev.Delta)) <= maxPlanRunes {
				s.planDropped = true
			}
			s.planText = ev.Delta
			if r := len([]rune(s.planText)); r > maxPlanRunes {
				s.planText = string([]rune(s.planText)[r-maxPlanRunes:])
				s.planDropped = true
			}
		} else {
			if s.planText == "" {
				s.planText = ev.Delta
			} else {
				s.planText += ev.Delta
			}
			s.planText = trimTailString(s.planText, maxPlanRunes, &s.planDropped)
		}
		s.phase = phasePlanning
		s.lastActivity = i18n.T("planning")
	case agent.EventToolUse:
		s.blocks = append(s.blocks, block{
			kind: "tool",
			tool: toolEntry{
				id:      ev.ToolID,
				name:    ev.ToolName,
				summary: summarizeToolInput(ev.ToolName, ev.ToolInput),
				input:   ev.ToolInput,
				status:  "running",
			},
		})
		s.phase = phaseToolRunning
		s.lastActivity = i18n.T("tool: ") + ev.ToolName
	case agent.EventToolResult:
		for i := range s.blocks {
			if s.blocks[i].kind == "tool" && s.blocks[i].tool.id == ev.ToolID {
				s.blocks[i].tool.status = "done"
				if ev.ToolError {
					s.blocks[i].tool.status = "failed"
				}
				s.blocks[i].tool.output = truncate(ev.ToolOutput, 200)
			}
		}
		// The agent now processes the result before the next step.
		s.phase = phaseThinking
		s.lastActivity = i18n.T("tool done")
	case agent.EventUsage:
		s.usage = &usageEntry{inputTokens: ev.InputTokens, outputTokens: ev.OutputTokens, costUSD: ev.CostUSD}
		s.lastActivity = i18n.T("usage")
	case agent.EventDone:
		s.sessionID = ev.SessionID
		s.stopReason = ev.StopReason
		switch ev.StopReason {
		case agent.StopCancelled:
			s.status = statusCancelled
		default:
			s.status = statusDone
		}
		s.finalizeTools()
	case agent.EventError:
		s.errMsg = ""
		if ev.Err != nil {
			s.errMsg = ev.Err.Error()
		}
		s.status = statusError
		s.finalizeTools()
	}
}

// finalizeTools marks any tool still in the "running" state when the run
// terminates. Without this a cancelled run leaves tools stuck on the
// "running" spinner forever, which makes the final card misleading.
func (s *RunState) finalizeTools() {
	for i := range s.blocks {
		if s.blocks[i].kind != "tool" {
			continue
		}
		if s.blocks[i].tool.status != "running" {
			continue
		}
		switch s.status {
		case statusCancelled:
			s.blocks[i].tool.status = "cancelled"
		case statusError:
			s.blocks[i].tool.status = "failed"
		default:
			s.blocks[i].tool.status = "done"
		}
	}
}

// runningToolName returns the name of the most recent tool still in flight,
// or empty when no tool is running. Used for the "Running tool: X" header.
func (s *RunState) runningToolName() string {
	for i := len(s.blocks) - 1; i >= 0; i-- {
		if s.blocks[i].kind == "tool" && s.blocks[i].tool.status == "running" {
			return s.blocks[i].tool.name
		}
	}
	return ""
}

// IsTerminal reports whether the state machine has reached a terminal state.
func (s *RunState) IsTerminal() bool {
	return s.status != statusRunning
}

// toolBlocks returns all tool blocks in chronological order. Test helper.
func (s *RunState) toolBlocks() []toolEntry {
	var out []toolEntry
	for _, b := range s.blocks {
		if b.kind == "tool" {
			out = append(out, b.tool)
		}
	}
	return out
}

// lastTextBlock returns the content of the most recent text block, or "".
// Test helper.
func (s *RunState) lastTextBlock() string {
	for i := len(s.blocks) - 1; i >= 0; i-- {
		if s.blocks[i].kind == "text" {
			return s.blocks[i].textBuf.String()
		}
	}
	return ""
}

// Render produces the card payload for the current state. Layout:
//
//	[plan panel]      current plan snapshot (collapsible, dimmed)
//	[blocks...]       text (plain markdown) and tool calls (collapsible
//	                  panels) interleaved chronologically
//	[footer]          usage + heartbeat + status
//
// Plan and tool calls use collapsible_panel (a native Feishu card element)
// so they are visually distinct from the agent's prose and don't rely on
// lark_md's limited blockquote/code-fence support. The agent's final
// message text is plain markdown and naturally lands at the bottom.
func (s *RunState) Render() Card {
	var elements []Element

	// Plan / reasoning region. Collapsible panel, expanded while the agent
	// is still planning, collapsed once it moves on. Replaced (not appended)
	// on every plan event, so this is always the current snapshot.
	if s.planText != "" {
		prefix := ""
		if s.planDropped {
			prefix = i18n.T("_(... earlier plan omitted)_\n\n")
		}
		expanded := s.phase == phasePlanning
		elements = append(elements, collapsiblePanel(
			i18n.T("🧠 Thinking"),
			prefix+s.planText,
			expanded,
			"grey",
		))
	}

	// Tool blocks older than maxVisibleTools are folded into a single count
	// line so a long agentic turn does not grow the card unbounded.
	toolCount := 0
	for _, b := range s.blocks {
		if b.kind == "tool" {
			toolCount++
		}
	}
	toolsHidden := 0
	if toolCount > maxVisibleTools {
		toolsHidden = toolCount - maxVisibleTools
	}
	toolsSeen := 0

	for _, b := range s.blocks {
		switch b.kind {
		case "text":
			if text := b.textBuf.String(); strings.TrimSpace(text) != "" {
				if len(elements) > 0 {
					elements = append(elements, hr)
				}
				elements = append(elements, markdown(text))
			}
		case "tool":
			toolsSeen++
			if toolsSeen <= toolsHidden {
				if toolsSeen == toolsHidden {
					if len(elements) > 0 {
						elements = append(elements, hr)
					}
					elements = append(elements, md(fmt.Sprintf(i18n.T("_+ %d earlier tool calls_"), toolsHidden)))
				}
				continue
			}
			t := b.tool
			// Tool call as a collapsible panel. Expanded while running so
			// the user sees live status; collapsed when done to keep the
			// card compact. The header carries the status icon + tool name
			// + input summary; the body carries the labeled input + output
			// preview (see renderToolBody).
			title := toolIcon(t.status) + " **" + t.name + "**"
			if t.summary != "" {
				title += " — " + t.summary
			}
			body := renderToolBody(t)
			expanded := t.status == "running"
			borderColor := ""
			switch t.status {
			case "failed":
				borderColor = "red"
			case "running":
				borderColor = "blue"
			}
			if len(elements) > 0 {
				elements = append(elements, hr)
			}
			elements = append(elements, collapsiblePanel(title, body, expanded, borderColor))
		}
	}

	// Footer: usage + status.
	elements = append(elements, hr)
	var footer strings.Builder
	if s.usage != nil {
		footer.WriteString(fmt.Sprintf(i18n.T("tokens: %d | cost: $%.4f"), s.usage.inputTokens, s.usage.costUSD))
		footer.WriteString(" | ")
	}
	// While running, prepend a heartbeat so the user can tell the agent
	// is alive even when it emits no ACP updates for a while.
	if s.status == statusRunning {
		footer.WriteString(fmt.Sprintf(i18n.T("running %s · last: %s (%s ago) | "),
			durationLabel(time.Since(s.startedAt)),
			s.lastActivity,
			durationLabel(time.Since(s.lastActivityAt))))
	}
	footer.WriteString(statusLabel(s.status))
	if s.errMsg != "" {
		footer.WriteString(" — ")
		footer.WriteString(s.errMsg)
	}
	elements = append(elements, plainText(footer.String()))

	return shell(s.headerTitle(), elements)
}

// headerTitle is the prominent, always-visible status line. It is the primary
// answer to "what is the agent doing right now?".
func (s *RunState) headerTitle() string {
	switch s.status {
	case statusDone:
		return i18n.T("✅ Done")
	case statusError:
		title := i18n.T("❌ Error")
		if s.errMsg != "" {
			title += ": " + truncate(s.errMsg, 60)
		}
		return title
	case statusCancelled:
		return i18n.T("⏹️ Cancelled")
	}
	// Running: reflect the current phase.
	switch s.phase {
	case phaseWriting:
		return i18n.T("✍️ Writing response...")
	case phasePlanning:
		return i18n.T("🧠 Planning...")
	case phaseToolRunning:
		if name := s.runningToolName(); name != "" {
			return i18n.T("🛠️ Running tool: ") + name
		}
		return i18n.T("🛠️ Running tool...")
	default:
		return i18n.T("🧠 Thinking...")
	}
}

// renderToolBlock renders one tool call as an inline block in the
// chronological flow. Uses a markdown blockquote (>) so tool calls are
// visually distinct from the agent's prose, with an emoji status marker.
func renderToolBlock(t toolEntry) string {
	var b strings.Builder
	b.WriteString("> ")
	b.WriteString(toolIcon(t.status))
	b.WriteString(" **")
	b.WriteString(t.name)
	b.WriteString("**")
	if t.summary != "" {
		b.WriteString(" — ")
		b.WriteString(t.summary)
	}
	b.WriteByte('\n')
	if t.output != "" {
		b.WriteString(">\n> ```\n")
		b.WriteString(truncate(t.output, 200))
		b.WriteString("\n```")
	}
	return b.String()
}

// toolIcon returns an emoji marker for a tool's status.
func toolIcon(status string) string {
	switch status {
	case "done":
		return "✅"
	case "failed":
		return "❌"
	case "cancelled":
		return "⏹️"
	default:
		return "⏳"
	}
}

func statusLabel(s runStatus) string {
	switch s {
	case statusRunning:
		return i18n.T("running...")
	case statusDone:
		return i18n.T("✅ done")
	case statusError:
		return i18n.T("❌ error")
	case statusCancelled:
		return i18n.T("⏹️ cancelled")
	}
	return i18n.T("unknown")
}

// Caps for the per-tool body region. Even with the header summary, a single
// tool with a huge command or long output can push the panel past Feishu's
// per-element size limit (~30KB); these keep one panel bounded.
const (
	// bodyFieldMax caps a single labeled input field (command, query, ...).
	bodyFieldMax = 600
	// outputMax caps the rendered output/error block of one tool.
	outputMax = 1200
	// bodyTotalMax is the cumulative cap on a tool's full body markdown
	// (input + output + fences + labels). The last belt across the whole
	// rendered body string.
	bodyTotalMax = 2500
)

// renderToolBody builds the body markdown for one tool call panel: a labeled
// input section (Command/File/Pattern/URL/Query) followed by the output or
// error block. Mirrors the reference TS renderer so the user can see what
// the tool operated on, not just its name. Tolerates missing/empty input.
func renderToolBody(t toolEntry) string {
	var parts []string
	if in := renderToolInput(t.name, t.input); in != "" {
		parts = append(parts, in)
	}
	switch {
	case t.output != "":
		label := i18n.T("Output")
		if t.status == "failed" {
			label = i18n.T("Error")
		}
		parts = append(parts, fmt.Sprintf("**%s**\n```\n%s\n```", label, truncate(t.output, outputMax)))
	case t.status == "running":
		parts = append(parts, i18n.T("_running..._"))
	}
	if len(parts) == 0 {
		return i18n.T("_no output_")
	}
	body := strings.Join(parts, "\n\n")
	if len(body) <= bodyTotalMax {
		return body
	}
	return truncate(body, bodyTotalMax) + i18n.T("\n\n_(body truncated, see /doctor or logs)_")
}

// renderToolInput renders a tool's input as a labeled markdown block keyed by
// tool name. Returns "" when there is nothing useful to show. Covers the
// tool names used by Devin (ACP) and Copilot/Codex (Bash-style) so both
// providers get the same field-level display.
func renderToolInput(name string, input json.RawMessage) string {
	if len(input) == 0 || string(input) == "null" {
		return ""
	}
	var rec map[string]any
	if err := json.Unmarshal(input, &rec); err != nil {
		return ""
	}
	str := func(k string) string {
		if v, ok := rec[k].(string); ok {
			return v
		}
		return ""
	}
	switch name {
	case "command_execution", "Bash", "shell", "Ran command":
		if cmd := str("command"); cmd != "" {
			return fmt.Sprintf(i18n.T("**Command**\n```bash\n%s\n```"), truncate(cmd, bodyFieldMax))
		}
	case "Read", "Edit", "Write", "NotebookEdit", "Read file":
		if fp := str("file_path"); fp != "" {
			return fmt.Sprintf(i18n.T("**File** `%s`"), fp)
		}
	case "Grep", "Search for":
		var lines []string
		if pat := str("pattern"); pat != "" {
			lines = append(lines, fmt.Sprintf(i18n.T("**Pattern** `%s`"), pat))
		}
		if path := str("path"); path != "" {
			lines = append(lines, fmt.Sprintf(i18n.T("**Path** `%s`"), path))
		}
		if len(lines) > 0 {
			return strings.Join(lines, "\n")
		}
	case "Glob", "Find files matching":
		if pat := str("pattern"); pat != "" {
			return fmt.Sprintf(i18n.T("**Pattern** `%s`"), pat)
		}
	case "WebFetch":
		if u := str("url"); u != "" {
			return fmt.Sprintf(i18n.T("**URL** %s"), u)
		}
	case "WebSearch":
		if q := str("query"); q != "" {
			return fmt.Sprintf(i18n.T("**Query** `%s`"), truncate(q, bodyFieldMax))
		}
	}
	return ""
}

// summarizeToolInput extracts a one-line, human-readable summary of a tool's
// input so the user can see what the tool is operating on (which command is
// being run, which file is being edited, etc.) without expanding the full
// payload. It tolerates missing/empty input.
func summarizeToolInput(name string, input json.RawMessage) string {
	if len(input) == 0 || string(input) == "null" {
		return ""
	}
	var rec map[string]any
	if err := json.Unmarshal(input, &rec); err != nil {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := rec[k].(string); ok && v != "" {
				return oneLine(v)
			}
		}
		return ""
	}
	switch name {
	case "command_execution", "Bash", "shell", "Ran command":
		return truncate(pick("command", "cmd"), 80)
	case "Read", "Edit", "Write", "NotebookEdit", "Read file":
		return truncate(pick("file_path", "path"), 80)
	case "Grep", "Search for":
		pat := pick("pattern")
		path := pick("path")
		if pat == "" {
			return truncate(path, 80)
		}
		if path == "" {
			return truncate(pat, 80)
		}
		return truncate(pat+" in "+path, 80)
	case "Glob", "Find files matching":
		return truncate(pick("pattern"), 80)
	case "WebFetch":
		return truncate(pick("url"), 80)
	case "WebSearch":
		return truncate(pick("query"), 80)
	default:
		return truncate(pick("command", "file_path", "path", "query", "url", "pattern", "text"), 80)
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// trimTail keeps only the last maxRunes runes of a Builder's content, prefixing
// a marker when content was dropped so the reader knows the beginning was
// omitted (not lost). Used to bound the text/plan regions of the streaming
// card: we keep the tail because that is what the agent is writing right now.
func trimTail(b *strings.Builder, maxRunes int) {
	s := b.String()
	if len([]rune(s)) <= maxRunes {
		return
	}
	tail := string([]rune(s)[len([]rune(s))-maxRunes:])
	b.Reset()
	b.WriteString(i18n.T("_(... earlier content omitted)_\n\n"))
	b.WriteString(tail)
}

// trimTailString is the string-returning form of trimTail, used for the
// reasoning-delta accumulation path (which keeps planText as a plain string,
// not a Builder). Sets *dropped = true when content was truncated.
func trimTailString(s string, maxRunes int, dropped *bool) string {
	if len([]rune(s)) <= maxRunes {
		return s
	}
	*dropped = true
	tail := string([]rune(s)[len([]rune(s))-maxRunes:])
	return i18n.T("_(... earlier content omitted)_\n\n") + tail
}

func boolStr(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// durationLabel formats a duration as a compact, human-readable string
// (e.g. "3s", "1m20s", "12s"). Used by the heartbeat footer.
func durationLabel(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	return fmt.Sprintf("%dm%ds", m, s)
}

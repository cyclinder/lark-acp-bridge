package acp

import (
	"encoding/json"
	"strings"
)

// Event is the neutral, provider-agnostic representation of one ACP
// session/update notification. The Devin adapter maps this onto
// agent.AgentEvent. Keeping it in the acp package avoids importing the agent
// package here and keeps the ACP layer reusable.
type Event struct {
	Kind EventKind

	// Text (EventKindText, EventKindPlan)
	Text string

	// Tool call (EventKindToolStart, EventKindToolUpdate)
	ToolID     string
	ToolTitle  string
	ToolKind   string
	ToolStatus string
	ToolOutput string
	ToolInput  json.RawMessage

	// Usage (EventKindUsage)
	UsedTokens  int
	SizeTokens  int
	CostAmount  float64
	CostCurrency string

	// Config (EventKindConfig)
	ConfigOptions []ConfigOption
}

type EventKind int

const (
	EventKindText EventKind = iota
	EventKindPlan
	EventKindToolStart
	EventKindToolUpdate
	EventKindUsage
	EventKindConfig
	EventKindOther // unknown sessionUpdate variant; raw text in Text
)

// ParseUpdate decodes a session/update notification's params into an Event.
// Returns EventKindOther with the raw sessionUpdate name in Text when the
// variant is unrecognized, so the caller can log it without losing data.
func ParseUpdate(msg *JSONRPCMessage) (Event, bool) {
	if msg == nil || msg.Method != "session/update" {
		return Event{}, false
	}
	var p SessionUpdateParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return Event{}, false
	}
	u := p.Update
	ev := Event{}
	switch u.SessionUpdate {
	case UpdateAgentMessageChunk:
		ev.Kind = EventKindText
		ev.Text = decodeSingleContent(u.Content)
	case UpdatePlan:
		ev.Kind = EventKindPlan
		ev.Text = joinPlan(u.Entries)
	case UpdateToolCall:
		ev.Kind = EventKindToolStart
		ev.ToolID = u.ToolCallID
		ev.ToolTitle = u.Title
		ev.ToolKind = u.Kind
		ev.ToolStatus = u.Status
		ev.ToolInput = normalizeToolInput(u.Kind, u.Content)
	case UpdateToolCallUpdate:
		ev.Kind = EventKindToolUpdate
		ev.ToolID = u.ToolCallID
		ev.ToolStatus = u.Status
		ev.ToolOutput = decodeMultiContent(u.Content)
	case UpdateUsage:
		ev.Kind = EventKindUsage
		if u.Used != nil {
			ev.UsedTokens = *u.Used
		}
		if u.Size != nil {
			ev.SizeTokens = *u.Size
		}
		if u.Cost != nil {
			ev.CostAmount = u.Cost.Amount
			ev.CostCurrency = u.Cost.Currency
		}
	case UpdateConfigOption:
		ev.Kind = EventKindConfig
		ev.ConfigOptions = u.ConfigOptions
	default:
		ev.Kind = EventKindOther
		ev.Text = u.SessionUpdate
	}
	return ev, true
}

func joinPlan(entries []PlanEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n")
		}
		if e.Status != "" {
			b.WriteString("[")
			b.WriteString(e.Status)
			b.WriteString("] ")
		}
		b.WriteString(e.Content)
	}
	return b.String()
}

// decodeSingleContent extracts the text from a single-block content payload
// (agent_message_chunk). Returns "" if the payload is empty or unparseable.
func decodeSingleContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var c Content
	if err := json.Unmarshal(raw, &c); err != nil {
		return ""
	}
	return c.Text
}

// decodeMultiContent extracts concatenated text from a multi-block content
// payload (tool_call_update.content is an array of content blocks).
func decodeMultiContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// The content field can be either a single object or an array. Try
	// array first, then single object.
	var blocks []UpdateContent
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			switch {
			case blk.Content != nil && blk.Content.Text != "":
				b.WriteString(blk.Content.Text)
			case blk.Text != "":
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	var single UpdateContent
	if err := json.Unmarshal(raw, &single); err == nil {
		if single.Content != nil {
			return single.Content.Text
		}
		return single.Text
	}
	return ""
}

// toolInputResource matches the nested resource shape Devin ACP uses for
// execute tool calls:
//
//	[{"type":"content","content":{"type":"resource","resource":{"text":"...","mimeType":"text/x-shellscript","uri":"..."}}}]
//
// The command text lives at content.content.resource.text. We extract it and
// re-emit as a flat {"command":"..."} so the card layer's provider-agnostic
// summarizeToolInput can render it without knowing the ACP wire format.
type toolInputResource struct {
	Type    string `json:"type"`
	Content struct {
		Type     string `json:"type"`
		Resource struct {
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			URI      string `json:"uri"`
		} `json:"resource"`
		Text string `json:"text"`
	} `json:"content"`
}

func normalizeToolInput(kind string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var blocks []toolInputResource
	if err := json.Unmarshal(raw, &blocks); err != nil {
		// Fall back to a single object form.
		var single toolInputResource
		if err := json.Unmarshal(raw, &single); err == nil {
			blocks = []toolInputResource{single}
		} else {
			return nil
		}
	}
	key := "command"
	if kind != "" && kind != "execute" {
		key = "text"
	}
	for _, blk := range blocks {
		text := blk.Content.Resource.Text
		if text == "" {
			text = blk.Content.Text
		}
		if text == "" {
			continue
		}
		b, err := json.Marshal(map[string]string{key: text})
		if err != nil {
			return nil
		}
		return b
	}
	return nil
}

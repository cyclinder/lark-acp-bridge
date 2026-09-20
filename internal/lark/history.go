// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/commands"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ListMessages fetches up to limit messages from a chat's history via the
// im/v1/messages list endpoint (requires the im:message.group_msg scope).
// Messages are returned in ascending time order (oldest first). Recalled
// (deleted) messages are skipped. When since is non-zero, only messages
// created at or after it are returned.
func (c *Channel) ListMessages(chatID string, limit int, since time.Time) ([]commands.HistoryMessage, error) {
	if chatID == "" || limit <= 0 {
		return nil, nil
	}
	var out []commands.HistoryMessage
	pageToken := ""
	for len(out) < limit {
		builder := larkim.NewListMessageReqBuilder().
			ContainerIdType("chat").
			ContainerId(chatID).
			SortType("ByCreateTimeDesc").
			PageSize(50)
		if !since.IsZero() {
			builder.StartTime(strconv.FormatInt(since.Unix(), 10))
		}
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		resp, err := c.client.Im.Message.List(context.Background(), builder.Build())
		if err != nil {
			return nil, fmt.Errorf("list messages: %w", err)
		}
		if !resp.Success() {
			return nil, fmt.Errorf("list messages failed: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data == nil || len(resp.Data.Items) == 0 {
			break
		}
		for _, item := range resp.Data.Items {
			if len(out) >= limit {
				break
			}
			if m, ok := normalizeHistoryMessage(item); ok {
				out = append(out, m)
			}
		}
		if resp.Data.HasMore == nil || !*resp.Data.HasMore ||
			resp.Data.PageToken == nil || *resp.Data.PageToken == "" {
			break
		}
		pageToken = *resp.Data.PageToken
	}
	// The API returned newest-first; flip to ascending for the transcript.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// DownloadImage downloads one image resource attached to a message (requires
// the im:resource scope) into destDir and returns the local file path.
func (c *Channel) DownloadImage(messageID, imageKey, destDir string) (string, error) {
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(imageKey).
		Type("image").
		Build()
	resp, err := c.client.Im.MessageResource.Get(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("get message resource: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("get message resource failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	name := sanitizeFileName(imageKey)
	if ext := filepath.Ext(resp.FileName); ext != "" {
		name += ext
	} else {
		name += ".png"
	}
	path := filepath.Join(destDir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.File); err != nil {
		return "", fmt.Errorf("write image: %w", err)
	}
	return path, nil
}

// sanitizeFileName keeps a resource key safe to use as a file name.
func sanitizeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}

// normalizeHistoryMessage converts one SDK message into the commands-level
// HistoryMessage projection. Returns ok=false for messages that carry no
// usable content for a transcript (recalled, empty, or system/card types).
func normalizeHistoryMessage(item *larkim.Message) (commands.HistoryMessage, bool) {
	var m commands.HistoryMessage
	if item == nil || (item.Deleted != nil && *item.Deleted) {
		return m, false
	}
	if item.MessageId != nil {
		m.MessageID = *item.MessageId
	}
	if item.MsgType != nil {
		m.MsgType = *item.MsgType
	}
	if item.CreateTime != nil {
		if ms, err := strconv.ParseInt(*item.CreateTime, 10, 64); err == nil {
			m.CreateTime = time.UnixMilli(ms)
		}
	}
	if item.Sender != nil {
		if item.Sender.Id != nil {
			m.SenderID = *item.Sender.Id
		}
		if item.Sender.SenderType != nil {
			m.SenderType = *item.Sender.SenderType
		}
	}
	content := ""
	if item.Body != nil && item.Body.Content != nil {
		content = *item.Body.Content
	}
	m.Text, m.ImageKeys = extractMessageContent(m.MsgType, content, item.Mentions)
	if m.Text == "" && len(m.ImageKeys) == 0 {
		return m, false
	}
	return m, true
}

// extractMessageContent parses a message body's JSON content by msg_type into
// plain text plus any embedded image keys. Mention placeholders (@_user_N)
// are replaced with the mentioned user's name when available.
func extractMessageContent(msgType, content string, mentions []*larkim.Mention) (string, []string) {
	switch msgType {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &body); err != nil {
			return "", nil
		}
		return strings.TrimSpace(replaceMentions(body.Text, mentions)), nil
	case "image":
		var body struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal([]byte(content), &body); err != nil || body.ImageKey == "" {
			return "", nil
		}
		return "", []string{body.ImageKey}
	case "post":
		return extractPostContent(content, mentions)
	case "file":
		return "[文件]", nil
	case "audio":
		return "[语音]", nil
	case "media":
		return "[视频]", nil
	default:
		// interactive cards, share_chat, stickers, system messages, etc.
		// carry no transcript-worthy text.
		return "", nil
	}
}

// extractPostContent flattens a rich-text (post) body into one text blob and
// collects embedded image keys.
func extractPostContent(content string, mentions []*larkim.Mention) (string, []string) {
	var body struct {
		Title   string `json:"title"`
		Content [][]struct {
			Tag      string `json:"tag"`
			Text     string `json:"text"`
			Href     string `json:"href"`
			ImageKey string `json:"image_key"`
			UserName string `json:"user_name"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		return "", nil
	}
	var sb strings.Builder
	var images []string
	if body.Title != "" {
		sb.WriteString(body.Title)
		sb.WriteByte('\n')
	}
	for _, line := range body.Content {
		for _, el := range line {
			switch el.Tag {
			case "text", "md":
				sb.WriteString(el.Text)
			case "a":
				sb.WriteString(el.Text)
				if el.Href != "" {
					sb.WriteString(" (")
					sb.WriteString(el.Href)
					sb.WriteByte(')')
				}
			case "at":
				if el.UserName != "" {
					sb.WriteString("@" + el.UserName)
				}
			case "img":
				if el.ImageKey != "" {
					images = append(images, el.ImageKey)
				}
			}
		}
		sb.WriteByte('\n')
	}
	return strings.TrimSpace(replaceMentions(sb.String(), mentions)), images
}

// replaceMentions substitutes @_user_N placeholders with @Name using the
// message's mention list, then strips any placeholders left over.
func replaceMentions(s string, mentions []*larkim.Mention) string {
	for _, m := range mentions {
		if m == nil || m.Key == nil {
			continue
		}
		name := ""
		if m.Name != nil {
			name = *m.Name
		}
		if name != "" {
			s = strings.ReplaceAll(s, *m.Key, "@"+name)
		} else {
			s = strings.ReplaceAll(s, *m.Key, " ")
		}
	}
	return s
}

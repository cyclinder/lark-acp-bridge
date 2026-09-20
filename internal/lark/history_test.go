// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package lark

import (
	"strings"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func strp(s string) *string { return &s }

func TestExtractMessageContentText(t *testing.T) {
	mentions := []*larkim.Mention{{Key: strp("@_user_1"), Name: strp("bridge-bot")}}
	text, images := extractMessageContent("text", `{"text":"@_user_1 /new-issue 试试"}`, mentions)
	if text != "@bridge-bot /new-issue 试试" {
		t.Errorf("text = %q", text)
	}
	if len(images) != 0 {
		t.Errorf("images = %v", images)
	}
}

func TestExtractMessageContentImage(t *testing.T) {
	text, images := extractMessageContent("image", `{"image_key":"img_v2_abc"}`, nil)
	if text != "" || len(images) != 1 || images[0] != "img_v2_abc" {
		t.Errorf("got text=%q images=%v", text, images)
	}
}

func TestExtractMessageContentPost(t *testing.T) {
	content := `{"title":"错误报告","content":[[{"tag":"text","text":"日志见 "},{"tag":"a","text":"链接","href":"https://x.io"}],[{"tag":"img","image_key":"img_1"}]]}`
	text, images := extractMessageContent("post", content, nil)
	for _, want := range []string{"错误报告", "日志见", "链接 (https://x.io)"} {
		if !strings.Contains(text, want) {
			t.Errorf("post text missing %q: %q", want, text)
		}
	}
	if len(images) != 1 || images[0] != "img_1" {
		t.Errorf("images = %v", images)
	}
}

func TestExtractMessageContentSkipsCards(t *testing.T) {
	text, images := extractMessageContent("interactive", `{"elements":[]}`, nil)
	if text != "" || len(images) != 0 {
		t.Errorf("interactive should be skipped, got %q %v", text, images)
	}
}

func TestNormalizeHistoryMessageSkipsDeleted(t *testing.T) {
	deleted := true
	if _, ok := normalizeHistoryMessage(&larkim.Message{Deleted: &deleted}); ok {
		t.Error("deleted message should be skipped")
	}
	if _, ok := normalizeHistoryMessage(nil); ok {
		t.Error("nil message should be skipped")
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName("img/v2:abc_1.png"); got != "img_v2_abc_1.png" {
		t.Errorf("got %q", got)
	}
}

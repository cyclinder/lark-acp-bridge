// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 20, 15, 0, 0, 0, time.Local)

func TestParseNewIssueArgs(t *testing.T) {
	cases := []struct {
		name string
		args string
		want newIssueOpts
	}{
		{"empty", "", newIssueOpts{}},
		{"repo shorthand", "spidernet-io/spiderpool", newIssueOpts{Repo: "spidernet-io/spiderpool"}},
		{"bare project name", "spiderpool", newIssueOpts{Repo: "spiderpool"}},
		{"repo url", "https://github.com/o/r extra words", newIssueOpts{Repo: "https://github.com/o/r", Extra: "extra words"}},
		{"last flag", "--last 200", newIssueOpts{Last: 200}},
		{"last eq", "--last=42", newIssueOpts{Last: 42}},
		{"last em dash", "—last 200", newIssueOpts{Last: 200}},
		{"last en dash eq", "–last=42", newIssueOpts{Last: 42}},
		{"last colon no dash", "last:200", newIssueOpts{Last: 200}},
		{"last eq no dash", "last=42", newIssueOpts{Last: 42}},
		{"since colon no dash", "since:24h", newIssueOpts{Since: now.Add(-24 * time.Hour)}},
		{"since em dash colon", "—since:today", newIssueOpts{Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local)}},
		{"bare last stays text", "o/r last words", newIssueOpts{Repo: "o/r", Extra: "last words"}},
		{"since hours", "--since 24h", newIssueOpts{Since: now.Add(-24 * time.Hour)}},
		{"since days", "--since=7d", newIssueOpts{Since: now.AddDate(0, 0, -7)}},
		{"since today", "--since today", newIssueOpts{Since: time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local)}},
		{"combined", "o/r --last 10 请重点关注崩溃日志", newIssueOpts{Repo: "o/r", Last: 10, Extra: "请重点关注崩溃日志"}},
		{"flag before repo", "--last 10 spiderpool 附加说明", newIssueOpts{Repo: "spiderpool", Last: 10, Extra: "附加说明"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNewIssueArgs(tc.args, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseNewIssueArgsErrors(t *testing.T) {
	for _, args := range []string{"--last", "--last abc", "--last 0", "--since", "--since yesterday", "--since -1h"} {
		if _, err := parseNewIssueArgs(args, now); err == nil {
			t.Errorf("args %q: expected error, got nil", args)
		}
	}
}

func historyMsg(id, sender, senderType, text string, images ...string) HistoryMessage {
	return HistoryMessage{
		MessageID: id, SenderID: sender, SenderType: senderType,
		CreateTime: now, MsgType: "text", Text: text, ImageKeys: images,
	}
}

func TestBuildTranscriptFiltersAndAnonymizes(t *testing.T) {
	msgs := []HistoryMessage{
		historyMsg("m1", "ou_alice", "user", "spiderpool 又崩了"),
		historyMsg("m2", "app_bot", "app", "Session cleared."),
		historyMsg("m3", "ou_bob", "user", "/status"),
		historyMsg("m4", "ou_bob", "user", "我这边也能复现"),
		historyMsg("m5", "ou_alice", "user", "看下截图", "img_1"),
	}
	got, count, images := buildTranscript(msgs, 80000, 10, func(mid, key string) (string, bool) {
		return "/tmp/x/" + key + ".png", true
	})
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if images != 1 {
		t.Fatalf("images = %d, want 1", images)
	}
	for _, want := range []string{"用户A: spiderpool 又崩了", "用户B: 我这边也能复现", "[图片: /tmp/x/img_1.png]"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
	for _, banned := range []string{"ou_alice", "ou_bob", "Session cleared", "/status"} {
		if strings.Contains(got, banned) {
			t.Errorf("transcript must not contain %q:\n%s", banned, got)
		}
	}
}

func TestBuildTranscriptCharBudgetDropsOldest(t *testing.T) {
	var msgs []HistoryMessage
	for i := 0; i < 10; i++ {
		msgs = append(msgs, historyMsg(fmt.Sprintf("m%d", i), "ou_a", "user",
			fmt.Sprintf("msg-%02d %s", i, strings.Repeat("x", 100))))
	}
	got, count, _ := buildTranscript(msgs, 500, 10, nil)
	if count >= 10 {
		t.Fatalf("expected trimming, kept %d", count)
	}
	if strings.Contains(got, "msg-00") {
		t.Errorf("oldest message should be dropped:\n%s", got)
	}
	if !strings.Contains(got, "msg-09") {
		t.Errorf("newest message must be kept:\n%s", got)
	}
}

func TestBuildTranscriptMaxImagesPrefersNewest(t *testing.T) {
	msgs := []HistoryMessage{
		historyMsg("m1", "ou_a", "user", "旧图", "img_old"),
		historyMsg("m2", "ou_a", "user", "新图", "img_new"),
	}
	got, _, images := buildTranscript(msgs, 80000, 1, func(mid, key string) (string, bool) {
		return "/tmp/" + key, true
	})
	if images != 1 {
		t.Fatalf("images = %d, want 1", images)
	}
	if !strings.Contains(got, "[图片: /tmp/img_new]") || !strings.Contains(got, "[图片(未下载)]") {
		t.Errorf("newest image should win the budget:\n%s", got)
	}
}

func TestRenderNewIssuePrompt(t *testing.T) {
	got := renderNewIssuePrompt("", "o/r", "带上 bug 标签", "[..] 用户A: hi\n", 1)
	for _, want := range []string{"o/r", "共 1 条", "附加要求：带上 bug 标签", "用户A: hi", "gh issue create", "非 fork", "候选仓库列表", "ISSUE_TEMPLATE", "中文版本"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("unreplaced placeholder in prompt:\n%s", got)
	}

	plain := renderNewIssuePrompt("", "spiderpool", "", "h", 2)
	if strings.Contains(plain, "附加要求") {
		t.Errorf("empty extra should not render the extra line")
	}

	custom := renderNewIssuePrompt("repo={{repo}} n={{count}}", "o/r", "", "h", 3)
	if custom != "repo=o/r n=3" {
		t.Errorf("custom template got %q", custom)
	}
}

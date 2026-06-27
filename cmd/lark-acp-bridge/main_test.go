// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

func TestStripMentions(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		mentions []larktypes.Mention
		want     string
	}{
		{
			name:    "no mentions passthrough",
			content: "/help",
			want:    "/help",
		},
		{
			name:    "leading bot mention before slash command",
			content: "@_user_1 /help",
			mentions: []larktypes.Mention{
				{Key: "@_user_1", Name: "cyclinder", IsBot: true},
			},
			want: "/help",
		},
		{
			name:    "mention in middle of prompt",
			content: "hey @_user_2 what is 1+1",
			mentions: []larktypes.Mention{
				{Key: "@_user_2", Name: "alice"},
			},
			want: "hey what is 1+1",
		},
		{
			name:    "at all mention",
			content: "@_all /status",
			want:    "/status",
		},
		{
			name:    "multiple mentions collapsed",
			content: "@_user_1 @_user_2 /cd /tmp",
			mentions: []larktypes.Mention{
				{Key: "@_user_1"},
				{Key: "@_user_2"},
			},
			want: "/cd /tmp",
		},
		{
			name:    "mention without trailing command",
			content: "@_user_1",
			mentions: []larktypes.Mention{
				{Key: "@_user_1"},
			},
			want: "",
		},
		{
			name:    "internal double spaces collapsed",
			content: "@_user_1   /help   me",
			mentions: []larktypes.Mention{
				{Key: "@_user_1"},
			},
			want: "/help me",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripMentions(c.content, c.mentions)
			if got != c.want {
				t.Errorf("stripMentions(%q, %v) = %q, want %q", c.content, c.mentions, got, c.want)
			}
		})
	}
}

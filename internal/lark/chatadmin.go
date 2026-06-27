// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package lark

import (
	"context"
	"fmt"

	"github.com/cognition/lark-acp-bridge/internal/commands"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// CreateGroup creates a private group with the given name and adds the
// supplied member open_id as an initial member. The bot (the app itself)
// becomes the group owner and is added automatically by the platform.
//
// memberOpenID may be empty, in which case the group is created with only
// the bot in it. The caller is responsible for passing an open_id (the
// NormalizedMessage.UserID field is preferred over user_id because the
// channel normalizer favors open_id when available).
func (c *Channel) CreateGroup(name, memberOpenID string) (string, error) {
	body := larkim.NewCreateChatReqBodyBuilder().
		Name(name).
		ChatMode("group").
		ChatType("private").
		Build()
	req := larkim.NewCreateChatReqBuilder().
		UserIdType("open_id").
		Body(body).
		Build()
	resp, err := c.client.Im.Chat.Create(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("create chat: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("create chat failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	chatID := ""
	if resp.Data != nil && resp.Data.ChatId != nil {
		chatID = *resp.Data.ChatId
	}
	if chatID == "" {
		return "", fmt.Errorf("create chat returned no chat_id")
	}
	// The CreateChat API can accept an initial member list, but to keep
	// permission requirements minimal and the call idempotent we add the
	// member via the dedicated members endpoint after the group exists.
	if memberOpenID != "" {
		if err := c.EnsureMember(chatID, memberOpenID); err != nil {
			// The group was created but the caller could not be added.
			// Return the chat id alongside the error so the caller can
			// record the bind and warn the user to join manually.
			return chatID, fmt.Errorf("group created but could not add member: %w", err)
		}
	}
	return chatID, nil
}

// EnsureMember adds a user (by open_id) to a group. It is best-effort:
// adding an existing member returns an error from the platform, which we
// treat as success since the desired end state (user in group) holds.
func (c *Channel) EnsureMember(chatID, openID string) error {
	if chatID == "" || openID == "" {
		return nil
	}
	body := larkim.NewCreateChatMembersReqBodyBuilder().
		IdList([]string{openID}).
		Build()
	req := larkim.NewCreateChatMembersReqBuilder().
		ChatId(chatID).
		MemberIdType("open_id").
		Body(body).
		Build()
	resp, err := c.client.Im.ChatMembers.Create(context.Background(), req)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	if !resp.Success() {
		// 230002 means the user is already a member; the desired end state
		// holds, so treat it as success.
		if resp.Code == 230002 {
			return nil
		}
		return fmt.Errorf("add member failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// GroupExists reports whether a group with the given chatID is still
// accessible to the bot. It calls the im/v1/chats/:chat_id endpoint and
// returns false for any API error or a non-success response code, treating
// "group not found / bot not a member" as a clean false rather than an error.
func (c *Channel) GroupExists(chatID string) bool {
	if chatID == "" {
		return false
	}
	req := larkim.NewGetChatReqBuilder().
		ChatId(chatID).
		Build()
	resp, err := c.client.Im.Chat.Get(context.Background(), req)
	if err != nil {
		return false
	}
	return resp.Success()
}

// SearchGroups searches the bot's visible groups by name (server-side fuzzy
// match) and returns the matching ChatInfo entries. It is a thin wrapper
// over the im/v1/chats/search endpoint, used for name collision checks when
// /open cannot rely solely on the local chatbind store.
func (c *Channel) SearchGroups(name string) ([]commands.ChatInfo, error) {
	if name == "" {
		return nil, nil
	}
	req := larkim.NewSearchChatReqBuilder().
		Query(name).
		PageSize(50).
		Build()
	// The search endpoint accepts either user or tenant access tokens; we
	// use the tenant token (the SDK default) so no extra option is needed.
	resp, err := c.client.Im.Chat.Search(context.Background(), req)
	if err != nil {
		return nil, fmt.Errorf("search chats: %w", err)
	}
	if !resp.Success() {
		return nil, fmt.Errorf("search chats failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil {
		return nil, nil
	}
	out := make([]commands.ChatInfo, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		ci := commands.ChatInfo{}
		if item.ChatId != nil {
			ci.ChatID = *item.ChatId
		}
		if item.Name != nil {
			ci.Name = *item.Name
		}
		out = append(out, ci)
	}
	return out, nil
}

// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package lark wraps the Feishu/Lark SDK channel module, providing the
// Sender interface used by commands and run, and a message intake loop.
package lark

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cognition/lark-acp-bridge/internal/card"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// ReactionTyping is the Lark emoji type for the "typing" reaction.
const ReactionTyping = "Typing"

// Channel wraps the SDK channel and exposes the operations the bridge needs.
type Channel struct {
	client *lark.Client
	sdk    types.Channel
}

// New creates and connects a Channel from app credentials. The tenant selects
// the Feishu vs Lark domain.
func New(appID, appSecret, tenant string) (*Channel, error) {
	baseURL := lark.FeishuBaseUrl
	if tenant == "lark" {
		baseURL = lark.LarkBaseUrl
	}
	client := lark.NewClient(appID, appSecret,
		lark.WithOpenBaseUrl(baseURL),
		lark.WithLogLevel(larkcore.LogLevelInfo),
	)
	// The ws client needs an EventDispatcher to receive message events.
	// Without it, EventHandler() returns nil and the channel's OnMessage
	// registration silently no-ops, causing a nil-pointer panic when a
	// message arrives.
	eventDispatcher := dispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithDomain(baseURL),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)
	ch := channel.NewChannel(client, wsClient)
	return &Channel{client: client, sdk: ch}, nil
}

// OnMessage registers the handler for incoming normalized messages.
func (c *Channel) OnMessage(fn func(ctx context.Context, msg *types.NormalizedMessage) error) {
	c.sdk.OnMessage(fn)
}

// Start connects the WebSocket and begins dispatching events. Blocks until
// the context is cancelled or the connection fails.
func (c *Channel) Start(ctx context.Context) error {
	return c.sdk.Start(ctx)
}

// SendMarkdown sends a markdown text message, optionally replying to a
// message id.
func (c *Channel) SendMarkdown(chatID, markdown, replyTo string) error {
	input := &types.SendInput{
		ChatID:   chatID,
		Markdown: markdown,
	}
	if replyTo != "" {
		input.ReplyMessageID = replyTo
	}
	_, err := c.sdk.Send(context.Background(), input)
	return err
}

// SendCard sends an interactive card and returns the message id of the sent
// card (used for subsequent updates via StreamController).
func (c *Channel) SendCard(chatID string, cardPayload card.Card) (string, error) {
	cardJSON, err := json.Marshal(cardPayload)
	if err != nil {
		return "", fmt.Errorf("marshal card: %w", err)
	}
	input := &types.SendInput{
		ChatID: chatID,
		Card:   string(cardJSON),
	}
	resp, err := c.sdk.Send(context.Background(), input)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.MessageID == "" {
		return "", fmt.Errorf("send card returned no message id")
	}
	return resp.MessageID, nil
}

// UpdateCard is a convenience wrapper that re-sends a card as a new message.
// True in-place card updates require a StreamController obtained from
// Stream(); the run executor uses that path for streaming. This method is
// used by command handlers that need to refresh a static card.
func (c *Channel) UpdateCard(chatID, messageID string, cardPayload card.Card) error {
	// The SDK does not expose a standalone update-by-message-id method on
	// the Channel interface; updates happen via StreamController. For
	// command-driven cards we send a fresh card as a reply.
	cardJSON, err := json.Marshal(cardPayload)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}
	input := &types.SendInput{
		ChatID:         chatID,
		Card:           string(cardJSON),
		ReplyMessageID: messageID,
	}
	_, err = c.sdk.Send(context.Background(), input)
	return err
}

// SendReaction adds an emoji reaction to an existing message. It is used to
// acknowledge a user message before the bridge starts the real work.
func (c *Channel) SendReaction(messageID, emojiType string) error {
	if messageID == "" || emojiType == "" {
		return nil
	}
	emoji := larkim.NewEmojiBuilder().EmojiType(emojiType).Build()
	body := larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(emoji).Build()
	req := larkim.NewCreateMessageReactionReqBuilder().MessageId(messageID).Body(body).Build()
	resp, err := c.client.Im.MessageReaction.Create(context.Background(), req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("create reaction failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// StreamCard opens a streaming card session. The returned controller can
// append text and update the card in place. Used by the run executor for
// live-updating run cards.
func (c *Channel) StreamCard(ctx context.Context, chatID string, cardPayload card.Card) (types.StreamController, string, error) {
	cardJSON, err := json.Marshal(cardPayload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal card: %w", err)
	}
	input := &types.SendInput{
		ChatID: chatID,
		Card:   string(cardJSON),
	}
	ctrl, err := c.sdk.Stream(ctx, input)
	if err != nil {
		return nil, "", err
	}
	// The Stream API does not return a message id directly; the first
	// card is sent as the stream's initial state. We return an empty id
	// since updates go through the controller, not by message id.
	return ctrl, "", nil
}

// CardJSON serializes a card.Card to the JSON string the SDK expects.
func CardJSON(c card.Card) (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

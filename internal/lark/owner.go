// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package lark

import (
	"context"
	"fmt"

	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
)

// AppOwner returns the open_id of the app's owner (its creator) via the
// application collaborators API. The tenant access token can query its own
// app without extra scopes.
func (c *Channel) AppOwner(appID string) (string, error) {
	req := larkapplication.NewGetApplicationCollaboratorsReqBuilder().
		AppId(appID).
		UserIdType("open_id").
		Build()
	resp, err := c.client.Application.ApplicationCollaborators.Get(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("get app collaborators: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("get app collaborators failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil {
		return "", fmt.Errorf("get app collaborators returned no data")
	}
	for _, col := range resp.Data.Collaborators {
		if col == nil || col.Type == nil || col.UserId == nil {
			continue
		}
		if *col.Type == "owner" {
			return *col.UserId, nil
		}
	}
	return "", fmt.Errorf("no owner found among %d collaborators", len(resp.Data.Collaborators))
}

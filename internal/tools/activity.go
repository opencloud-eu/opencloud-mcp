package tools

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) activityTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:  "list_activities",
		Title: "List the activities",
		Description: "List what happened to a file, a folder or a whole space: who created, changed, moved, shared or " +
			"deleted what, and when.",
		Annotations: readOnly("List the activities"),
	}, t.listActivities)

	add(t, s, &mcp.Tool{
		Name:        "list_notifications",
		Title:       "List the notifications",
		Description: "List the user's notifications, newest first, like a share they received or a space they were added to, each with its id.",
		Annotations: readOnly("List the notifications"),
	}, t.listNotifications)

	add(t, s, &mcp.Tool{
		Name:        "dismiss_notifications",
		Title:       "Dismiss notifications",
		Description: "Dismiss notifications of the user by their ids.",
		Annotations: idempotent(changes("Dismiss notifications")),
	}, t.dismissNotifications)
}

type (
	history struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path,omitempty" jsonschema:"a file or folder of the space, like guides; the whole space when left out"`
		Limit int    `json:"limit,omitempty" jsonschema:"how many activities to return, newest first, 1 to 100; 20 when left out"`
	}

	dismissal struct {
		IDs []string `json:"ids" jsonschema:"the notifications' ids as list_notifications gives them"`
	}
)

type (
	activities struct {
		Activities []opencloud.Activity `json:"activities"`
	}

	notifications struct {
		Notifications []opencloud.Notification `json:"notifications"`
	}
)

func (t *tools) listActivities(ctx context.Context, req *mcp.CallToolRequest, in history) (*mcp.CallToolResult, activities, error) {
	limit := cmp.Or(in.Limit, 20)
	if limit < 1 || limit > 100 {
		return nil, activities{}, fmt.Errorf("a limit is 1 to 100, not %d", in.Limit)
	}

	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, activities{}, err
	}

	all, err := c.Activities(ctx, sp, p, limit)
	if err != nil {
		return nil, activities{}, err
	}

	var b strings.Builder

	for _, a := range all {
		fmt.Fprintf(&b, "%s %s\n", a.When.Format(time.RFC3339), a.Message)
	}

	if len(all) == 0 {
		fmt.Fprintf(&b, "Nothing happened to %s yet.", where(sp, p))
	}

	return text(b.String()), activities{Activities: all}, nil
}

func (t *tools) listNotifications(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, notifications, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, notifications{}, err
	}

	all, err := c.Notifications(ctx)
	if err != nil {
		return nil, notifications{}, err
	}

	var b strings.Builder

	for _, n := range all {
		fmt.Fprintf(&b, "%s %s: %s (id %s)\n", n.When.Format(time.RFC3339), n.Subject, n.Message, n.ID)
	}

	if len(all) == 0 {
		b.WriteString("The user has no notifications.")
	}

	return text(b.String()), notifications{Notifications: all}, nil
}

func (t *tools) dismissNotifications(ctx context.Context, req *mcp.CallToolRequest, in dismissal) (*mcp.CallToolResult, any, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	if err := c.Dismiss(ctx, in.IDs); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Dismissed %d notifications.", len(in.IDs))), nil, nil
}

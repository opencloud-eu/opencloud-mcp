package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) peopleTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:        "get_me",
		Title:       "Get the user",
		Description: "Get the signed-in user: name, mail, language and groups.",
		Annotations: readOnly("Get the user"),
	}, t.getMe)

	add(t, s, &mcp.Tool{
		Name:        "find_users",
		Title:       "Find users",
		Description: "Find users by a part of their name or mail, to share with.",
		Annotations: readOnly("Find users"),
	}, t.findUsers)

	add(t, s, &mcp.Tool{
		Name:        "find_groups",
		Title:       "Find groups",
		Description: "Find groups by a part of their name, to share with.",
		Annotations: readOnly("Find groups"),
	}, t.findGroups)
}

type (
	lookup struct {
		Query string `json:"query" jsonschema:"a part of the name or mail, like ali; the instance may ask for a few characters at least"`
	}
)

type people struct {
	People []opencloud.Person `json:"people"`
}

func (t *tools) getMe(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, opencloud.Me, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, opencloud.Me{}, err
	}

	me, err := c.Me(ctx)
	if err != nil {
		return nil, opencloud.Me{}, err
	}

	var b strings.Builder

	b.WriteString(person(me.Person) + "\n")

	if me.Language != "" {
		fmt.Fprintf(&b, "language: %s\n", me.Language)
	}

	if len(me.Groups) > 0 {
		fmt.Fprintf(&b, "groups: %s\n", strings.Join(me.Groups, ", "))
	}

	return text(b.String()), me, nil
}

func (t *tools) findUsers(ctx context.Context, req *mcp.CallToolRequest, in lookup) (*mcp.CallToolResult, people, error) {
	return t.lookUp(ctx, req, in, (*opencloud.Client).FindUsers)
}

func (t *tools) findGroups(ctx context.Context, req *mcp.CallToolRequest, in lookup) (*mcp.CallToolResult, people, error) {
	return t.lookUp(ctx, req, in, (*opencloud.Client).FindGroups)
}

func (t *tools) lookUp(ctx context.Context, req *mcp.CallToolRequest, in lookup,
	by func(*opencloud.Client, context.Context, string) ([]opencloud.Person, error),
) (*mcp.CallToolResult, people, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, people{}, err
	}

	found, err := by(c, ctx, in.Query)
	if err != nil {
		return nil, people{}, err
	}

	var b strings.Builder

	for _, p := range found {
		b.WriteString(person(p) + "\n")
	}

	if len(found) == 0 {
		fmt.Fprintf(&b, "Nobody matches %q.", in.Query)
	}

	return text(b.String()), people{People: found}, nil
}

// person is a user or group in a line a model reads.
func person(p opencloud.Person) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s (%s", p.Name, p.Kind)

	if p.Login != "" {
		fmt.Fprintf(&b, " %s", p.Login)
	}

	if p.Mail != "" {
		fmt.Fprintf(&b, ", %s", p.Mail)
	}

	fmt.Fprintf(&b, ", id %s)", p.ID)

	return b.String()
}

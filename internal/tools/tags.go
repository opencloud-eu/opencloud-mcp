package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *tools) tagTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:        "list_tags",
		Title:       "List the tags",
		Description: "List the tags in use on the files and folders the user sees.",
		Annotations: readOnly("List the tags"),
	}, t.listTags)

	add(t, s, &mcp.Tool{
		Name:        "add_tags",
		Title:       "Add tags",
		Description: "Tag a file or folder.",
		Annotations: idempotent(adds("Add tags")),
	}, t.addTags)

	add(t, s, &mcp.Tool{
		Name:        "remove_tags",
		Title:       "Remove tags",
		Description: "Take tags off a file or folder.",
		Annotations: idempotent(changes("Remove tags")),
	}, t.removeTags)
}

type (
	tagging struct {
		Space string   `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string   `json:"path" jsonschema:"a file or folder of the space, like guides/restore.md"`
		Tags  []string `json:"tags" jsonschema:"the tags, like invoice or 2026"`
	}
)

type tags struct {
	Tags []string `json:"tags"`
}

func (t *tools) listTags(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, tags, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, tags{}, err
	}

	all, err := c.Tags(ctx)
	if err != nil {
		return nil, tags{}, err
	}

	if len(all) == 0 {
		return text("No tags are in use."), tags{Tags: all}, nil
	}

	return text(strings.Join(all, ", ")), tags{Tags: all}, nil
}

func (t *tools) addTags(ctx context.Context, req *mcp.CallToolRequest, in tagging) (*mcp.CallToolResult, tags, error) {
	return t.retag(ctx, req, in, true)
}

func (t *tools) removeTags(ctx context.Context, req *mcp.CallToolRequest, in tagging) (*mcp.CallToolResult, tags, error) {
	return t.retag(ctx, req, in, false)
}

func (t *tools) retag(ctx context.Context, req *mcp.CallToolRequest, in tagging, on bool) (*mcp.CallToolResult, tags, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, tags{}, err
	}

	retag := c.Untag
	if on {
		retag = c.Tag
	}

	now, err := retag(ctx, sp, p, in.Tags)
	if err != nil {
		return nil, tags{}, err
	}

	if len(now) == 0 {
		return text(where(sp, p) + " has no tags."), tags{Tags: now}, nil
	}

	return text(fmt.Sprintf("%s has the tags %s.", where(sp, p), strings.Join(now, ", "))), tags{Tags: now}, nil
}

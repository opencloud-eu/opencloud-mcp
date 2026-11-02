package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) trashTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:  "list_trash",
		Title: "List the trash",
		Description: "List what was deleted in a space, or in every space whose trash the user sees, newest first, each with its " +
			"old path, when it was deleted and its key.",
		Annotations: readOnly("List the trash"),
	}, t.listTrash)

	add(t, s, &mcp.Tool{
		Name:        "restore_from_trash",
		Title:       "Restore from the trash",
		Description: "Bring a deleted file or folder back from the trash of its space, to its old path or another one.",
		Annotations: adds("Restore from the trash"),
	}, t.restoreFromTrash)

	add(t, s, &mcp.Tool{
		Name:        "delete_from_trash",
		Title:       "Delete from the trash",
		Description: "Delete one file or folder from the trash of a space for good.",
		Annotations: idempotent(changes("Delete from the trash")),
	}, t.deleteFromTrash)

	add(t, s, &mcp.Tool{
		Name:        "empty_trash",
		Title:       "Empty the trash",
		Description: "Delete everything in the trash of a space for good.",
		Annotations: idempotent(changes("Empty the trash")),
	}, t.emptyTrash)
}

type (
	bin struct {
		Space string `json:"space,omitempty" jsonschema:"the space, by its name or id as list_spaces gives it; every space when left out"`
	}

	trashed struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Key   string `json:"key" jsonschema:"the deleted item's key as list_trash gives it"`
		To    string `json:"to,omitempty" jsonschema:"where restore_from_trash puts it, like guides/restore.md; its old path when left out"`
	}
)

type trash struct {
	Deleted []opencloud.Deleted `json:"deleted"`
}

func (t *tools) listTrash(ctx context.Context, req *mcp.CallToolRequest, in bin) (*mcp.CallToolResult, trash, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, trash{}, err
	}

	var spaces []opencloud.Space

	if in.Space != "" {
		sp, err := c.Space(ctx, in.Space)
		if err != nil {
			return nil, trash{}, err
		}

		spaces = []opencloud.Space{sp}
	} else if spaces, err = c.Spaces(ctx); err != nil {
		return nil, trash{}, err
	}

	all := []opencloud.Deleted{}

	for _, sp := range spaces {
		// the Shares space has no trash of its own
		if in.Space == "" && sp.Type == "virtual" {
			continue
		}

		deleted, err := c.Trash(ctx, sp)
		if in.Space == "" && errors.Is(err, opencloud.ErrDenied) {
			continue
		} else if err != nil {
			return nil, trash{}, err
		}

		all = append(all, deleted...)
	}

	var b strings.Builder

	for _, d := range all {
		kind := fmt.Sprintf("%d bytes", d.Size)
		if d.Folder {
			kind = "a folder"
		}

		fmt.Fprintf(&b, "%s in %s (%s), deleted %s, key %s\n", d.Path, d.SpaceName, kind, d.Deleted.Format(time.RFC3339), d.Key)
	}

	if len(all) == 0 {
		b.WriteString("The trash is empty.")
	}

	return text(b.String()), trash{Deleted: all}, nil
}

func (t *tools) restoreFromTrash(ctx context.Context, req *mcp.CallToolRequest, in trashed) (*mcp.CallToolResult, any, error) {
	c, sp, to, err := t.at(ctx, req, in.Space, in.To)
	if err != nil {
		return nil, nil, err
	}

	at, err := c.Restore(ctx, sp, in.Key, to)
	if err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Restored %s in %s.", at, sp.Name)), nil, nil
}

func (t *tools) deleteFromTrash(ctx context.Context, req *mcp.CallToolRequest, in trashed) (*mcp.CallToolResult, any, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, nil, err
	}

	if err := c.Purge(ctx, sp, in.Key); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s is not in the trash of %s any more.", in.Key, sp.Name)), nil, nil
}

func (t *tools) emptyTrash(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, any, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, nil, err
	}

	if err := c.EmptyTrash(ctx, sp); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("The trash of %s is empty.", sp.Name)), nil, nil
}

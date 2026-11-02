package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/document"
	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) versionTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:        "list_versions",
		Title:       "List the versions of a file",
		Description: "List the earlier versions of a file, each with its key, size and when it was written.",
		Annotations: readOnly("List the versions of a file"),
	}, t.listVersions)

	add(t, s, &mcp.Tool{
		Name:        "read_version",
		Title:       "Read a version of a file",
		Description: "Read an earlier version of a file as text, the way read_file reads the current one.",
		Annotations: readOnly("Read a version of a file"),
	}, t.readVersion)

	add(t, s, &mcp.Tool{
		Name:        "restore_version",
		Title:       "Restore a version of a file",
		Description: "Make an earlier version of a file the current one; the current one becomes a version.",
		Annotations: changes("Restore a version of a file"),
	}, t.restoreVersion)
}

type (
	version struct {
		Space   string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path    string `json:"path" jsonschema:"the file's path in the space, like guides/restore.md"`
		Version string `json:"version" jsonschema:"the version's key as list_versions gives it"`
	}
)

type versions struct {
	Versions []opencloud.Version `json:"versions"`
}

func (t *tools) listVersions(ctx context.Context, req *mcp.CallToolRequest, in file) (*mcp.CallToolResult, versions, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, versions{}, err
	}

	all, err := c.Versions(ctx, sp, p)
	if err != nil {
		return nil, versions{}, err
	}

	var b strings.Builder

	for _, v := range all {
		fmt.Fprintf(&b, "%s, %d bytes, key %s\n", v.Written.Format(time.RFC3339), v.Size, v.Key)
	}

	if len(all) == 0 {
		fmt.Fprintf(&b, "%s has no earlier versions.", p)
	}

	return text(b.String()), versions{Versions: all}, nil
}

func (t *tools) readVersion(ctx context.Context, req *mcp.CallToolRequest, in version) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	b, err := c.ReadVersion(ctx, sp, p, in.Version)
	if err != nil {
		return nil, nil, err
	}

	body, err := document.Text(p, b)
	if errors.Is(err, document.ErrNotText) {
		return nil, nil, fmt.Errorf("%s is not a text file or PDF", p)
	} else if err != nil {
		return nil, nil, fmt.Errorf("the version of %s cannot be read as text: %w", p, err)
	}

	return text(asRead(body)), nil, nil
}

func (t *tools) restoreVersion(ctx context.Context, req *mcp.CallToolRequest, in version) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	if err := c.RestoreVersion(ctx, sp, p, in.Version); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s in %s has the content of the version %s now; the one it had is a version.", p, sp.Name, in.Version)), nil, nil
}

package tools

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// maxCompletions is the most values one completion gives, the protocol's own bound.
const maxCompletions = 100

// complete offers the template's space ids by the start of a space's name or id, and the paths of
// a space's folders and documents by the start of the path.
func (t *tools) complete(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	ref := req.Params.Ref
	if ref == nil || ref.Type != "ref/resource" || ref.URI != template.URITemplate {
		return completed(nil), nil
	}

	c, err := t.access(ctx, tokenInfo(ctx, req.Extra))
	if err != nil {
		return nil, err
	}

	typed := req.Params.Argument.Value

	switch req.Params.Argument.Name {
	case "space":
		return spaceCompletions(ctx, c, typed)
	case "path":
		var space string
		if req.Params.Context != nil {
			space = req.Params.Context.Arguments["space"]
		}

		return pathCompletions(ctx, c, space, typed)
	}

	return completed(nil), nil
}

func spaceCompletions(ctx context.Context, c *opencloud.Client, typed string) (*mcp.CompleteResult, error) {
	spaces, err := c.Spaces(ctx)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, sp := range spaces {
		if startsWith(sp.Name, typed) || startsWith(sp.ID, typed) {
			out = append(out, sp.ID)
		}
	}

	return completed(out), nil
}

// pathCompletions are the entries of the folder up to the typed path's last slash whose names
// start with what follows it: readable documents, and folders with a slash to go on.
func pathCompletions(ctx context.Context, c *opencloud.Client, space, typed string) (*mcp.CompleteResult, error) {
	sp, err := spaceByID(ctx, c, space)
	if errors.Is(err, opencloud.ErrSpaceNotFound) {
		return completed(nil), nil
	} else if err != nil {
		return nil, err
	}

	dir, prefix := "", typed
	if i := strings.LastIndex(typed, "/"); i >= 0 {
		dir, prefix = typed[:i], typed[i+1:]
	}

	// a path out of the space completes nothing
	if _, err := opencloud.Clean(dir); err != nil {
		return completed(nil), nil //nolint:nilerr // no path to offer is the answer, not an error
	}

	entries, err := c.List(ctx, sp, dir)
	if errors.Is(err, opencloud.ErrNotFound) || errors.Is(err, opencloud.ErrNotAFolder) {
		return completed(nil), nil
	} else if err != nil {
		return nil, err
	}

	var out []string

	for _, e := range entries {
		if !startsWith(path.Base(e.Path), prefix) {
			continue
		}

		switch {
		case e.Folder:
			out = append(out, e.Path+"/")
		case readable(e.Path):
			out = append(out, e.Path)
		}
	}

	slices.Sort(out)

	return completed(out), nil
}

func startsWith(s, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

// completed is a completion of the values, the first maxCompletions of them.
func completed(values []string) *mcp.CompleteResult {
	res := &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: []string{}, Total: len(values)}}

	if len(values) > maxCompletions {
		res.Completion.HasMore = true
		values = values[:maxCompletions]
	}

	res.Completion.Values = append(res.Completion.Values, values...)

	return res
}

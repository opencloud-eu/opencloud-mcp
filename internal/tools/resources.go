package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/document"
	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// The walk through a space for its documents stops at these bounds.
const (
	maxDepth = 8
	maxDocs  = 500
	// a title is read from this much of a document's start
	maxHead = 8 << 10
	// this many titles are read at once
	titleReads = 4
)

const uriPrefix = "opencloud:///spaces/"

// template is every document of every space.
var template = &mcp.ResourceTemplate{
	URITemplate: uriPrefix + "{space}/{+path}",
	Name:        "document",
	Title:       "A document of an OpenCloud space",
	Description: "A markdown, text, CSV or PDF document of a space, by the space's id and the document's path in it, read as text.",
}

const (
	markdown = "text/markdown"
	plain    = "text/plain"
)

// types are the readable documents by extension, with the type of the file and of its text.
var types = map[string]struct{ file, text string }{
	".md":       {markdown, markdown},
	".markdown": {markdown, markdown},
	".txt":      {plain, plain},
	".csv":      {"text/csv", "text/csv"},
	".pdf":      {"application/pdf", plain},
}

func readable(p string) bool {
	_, ok := types[strings.ToLower(path.Ext(p))]

	return ok
}

// uri is the resource of the document p in the space id, each segment path-escaped.
func uri(id, p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}

	return uriPrefix + url.PathEscape(id) + "/" + strings.Join(parts, "/")
}

// parse is the space id and document path of a resource.
func parse(raw string) (string, string, bool) {
	rest, ok := strings.CutPrefix(raw, uriPrefix)
	if !ok {
		return "", "", false
	}

	id, p, _ := strings.Cut(rest, "/")

	id, err := url.PathUnescape(id)
	if err != nil || id == "" {
		return "", "", false
	}

	p, err = url.PathUnescape(p)
	if err != nil {
		return "", "", false
	}

	p, err = opencloud.Clean(p)

	return id, p, err == nil && p != ""
}

// notFound is the JSON-RPC error of a document that is not there, -32002 as in the 2025-11-25
// revision; the go-sdk's own ResourceNotFoundError answers -32602.
func notFound(raw string) error {
	data, _ := json.Marshal(map[string]string{"uri": raw}) //nolint:errchkjson // a map of strings

	return &jsonrpc.Error{Code: -32002, Message: "Resource not found", Data: data}
}

// doc is a readable document of a space.
type doc struct {
	space opencloud.Space
	entry opencloud.Entry
	title string
}

func (d doc) resource() *mcp.Resource {
	r := &mcp.Resource{
		URI:      uri(d.space.ID, d.entry.Path),
		Name:     d.entry.Path,
		Title:    d.title,
		MIMEType: types[strings.ToLower(path.Ext(d.entry.Path))].file,
		Size:     d.entry.Size,
	}

	if !d.entry.Modified.IsZero() {
		r.Annotations = &mcp.Annotations{LastModified: d.entry.Modified.UTC().Format(time.RFC3339)}
	}

	return r
}

// documents are the readable documents of a space, found within the walk's bounds, with their
// titles.
func documents(ctx context.Context, c *opencloud.Client, sp opencloud.Space) ([]doc, error) {
	var out []doc

	var walk func(dir string, depth int) error

	walk = func(dir string, depth int) error {
		entries, err := c.List(ctx, sp, dir)
		if err != nil {
			return err
		}

		for _, e := range entries {
			switch {
			case len(out) >= maxDocs:
				return nil
			case e.Folder && depth < maxDepth:
				if err := walk(e.Path, depth+1); err != nil {
					return err
				}
			case !e.Folder && readable(e.Path) && e.Size <= opencloud.MaxRead:
				out = append(out, doc{space: sp, entry: e, title: path.Base(e.Path)})
			}
		}

		return nil
	}

	if err := walk("", 0); err != nil {
		return nil, err
	}

	titles(ctx, c, out)

	return out, nil
}

// titles reads the first heading of each markdown document; one that cannot be read keeps its
// file name.
func titles(ctx context.Context, c *opencloud.Client, docs []doc) {
	var wg sync.WaitGroup

	slots := make(chan struct{}, titleReads)

	for i := range docs {
		if types[strings.ToLower(path.Ext(docs[i].entry.Path))].text != markdown {
			continue
		}

		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()

			head, err := c.Head(ctx, docs[i].space, docs[i].entry.Path, maxHead)
			if err != nil {
				return
			}

			text := string(head)
			if len(head) == maxHead {
				// the last line may be cut off
				text = text[:strings.LastIndexByte(text, '\n')+1]
			}

			if title := document.Title(text); title != "" {
				docs[i].title = title
			}
		})
	}

	wg.Wait()
}

// listResources is resources/list, one space per page: the cursor names the next space.
func (t *tools) listResources(ctx context.Context, req *mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	c, err := t.access(ctx, tokenInfo(ctx, req.Extra))
	if err != nil {
		return nil, err
	}

	spaces, err := c.Spaces(ctx)
	if err != nil {
		return nil, err
	}

	from := 0

	if req.Params != nil && req.Params.Cursor != "" {
		id, err := base64.RawURLEncoding.DecodeString(req.Params.Cursor)
		if err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid cursor"}
		}

		from = len(spaces)

		for i, sp := range spaces {
			if sp.ID == string(id) {
				from = i
			}
		}
	}

	res := &mcp.ListResourcesResult{Resources: []*mcp.Resource{}}
	if from == 0 {
		res.Resources = append(res.Resources, language)
	}

	for i := from; i < len(spaces); i++ {
		docs, err := documents(ctx, c, spaces[i])
		if err != nil {
			return nil, err
		}

		for _, d := range docs {
			res.Resources = append(res.Resources, d.resource())
		}

		if len(docs) > 0 {
			if i+1 < len(spaces) {
				res.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(spaces[i+1].ID))
			}

			break
		}
	}

	return res, nil
}

// readResource is resources/read of a document: its text as read_file gives it.
func (t *tools) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	raw := req.Params.URI

	id, p, ok := parse(raw)
	if !ok || !readable(p) {
		return nil, notFound(raw)
	}

	c, err := t.access(ctx, tokenInfo(ctx, req.Extra))
	if err != nil {
		return nil, err
	}

	sp, err := spaceByID(ctx, c, id)
	if err != nil {
		return nil, notFound(raw)
	}

	b, err := c.Read(ctx, sp, p)
	if errors.Is(err, opencloud.ErrNotFound) {
		return nil, notFound(raw)
	} else if err != nil {
		return nil, err
	}

	body, err := document.Text(p, b)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read as text: %w", p, err)
	}

	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: raw, MIMEType: types[strings.ToLower(path.Ext(p))].text, Text: asRead(body),
	}}}, nil
}

// subscribe takes a subscription to a document the user can read.
func (t *tools) subscribe(ctx context.Context, req *mcp.SubscribeRequest) error {
	_, err := t.stat(ctx, tokenInfo(ctx, req.Extra), req.Params.URI)

	return err
}

// stat is the entry of the document a resource names.
func (t *tools) stat(ctx context.Context, info *auth.TokenInfo, raw string) (opencloud.Entry, error) {
	id, p, ok := parse(raw)
	if !ok || !readable(p) {
		return opencloud.Entry{}, notFound(raw)
	}

	c, err := t.access(ctx, info)
	if err != nil {
		return opencloud.Entry{}, err
	}

	sp, err := spaceByID(ctx, c, id)
	if err != nil {
		return opencloud.Entry{}, notFound(raw)
	}

	entries, err := c.List(ctx, sp, path.Dir(p))
	if err != nil {
		return opencloud.Entry{}, notFound(raw)
	}

	for _, e := range entries {
		if e.Path == p && !e.Folder {
			return e, nil
		}
	}

	return opencloud.Entry{}, notFound(raw)
}

func spaceByID(ctx context.Context, c *opencloud.Client, id string) (opencloud.Space, error) {
	spaces, err := c.Spaces(ctx)
	if err != nil {
		return opencloud.Space{}, err
	}

	for _, sp := range spaces {
		if sp.ID == id {
			return sp, nil
		}
	}

	return opencloud.Space{}, opencloud.ErrSpaceNotFound
}

// tokenInfo is the sign-in of a request; a subscription inside subscriptions/listen carries none
// of its own and has the listen request's in its context.
func tokenInfo(ctx context.Context, extra *mcp.RequestExtra) *auth.TokenInfo {
	if extra != nil && extra.TokenInfo != nil {
		return extra.TokenInfo
	}

	return auth.TokenInfoFromContext(ctx)
}

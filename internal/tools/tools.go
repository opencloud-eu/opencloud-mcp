// Package tools is the MCP server: the tools that let a model work in the user's OpenCloud as the
// user does, with spaces, files, shares, trash, versions, tags, activities and apps.
package tools

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// maxText bounds how many characters of a document a read returns, so a result stays within what
// a model reads of it.
const maxText = 100_000

// Access is how a session reaches OpenCloud as its user, given the TokenInfo of the request at
// hand, which is nil on stdio.
type Access func(ctx context.Context, info *auth.TokenInfo) (*opencloud.Client, error)

// As is the access of one configured user.
func As(c *opencloud.Client) Access {
	return func(context.Context, *auth.TokenInfo) (*opencloud.Client, error) { return c, nil }
}

// Options are what a server works with.
type Options struct {
	Access  Access
	Version string
	// Interval is how often an open subscriptions/listen looks at the root etags of the spaces.
	Interval time.Duration
	// Log takes what goes wrong outside a request; nothing is logged when nil.
	Log *slog.Logger
	// Toolsets are the groups of tools the server offers, all of them when empty.
	Toolsets []string
	// ReadOnly leaves out every tool that changes something.
	ReadOnly bool
}

// toolsets are the groups of tools by what they work on, and what registers each.
var toolsets = []struct {
	name     string
	register []func(*tools, *mcp.Server)
}{
	{"spaces", []func(*tools, *mcp.Server){(*tools).spaceTools}},
	{"files", []func(*tools, *mcp.Server){(*tools).fileTools}},
	{"search", []func(*tools, *mcp.Server){(*tools).documentTools, (*tools).searchTools}},
	{"trash", []func(*tools, *mcp.Server){(*tools).trashTools}},
	{"versions", []func(*tools, *mcp.Server){(*tools).versionTools}},
	{"sharing", []func(*tools, *mcp.Server){(*tools).sharingTools}},
	{"people", []func(*tools, *mcp.Server){(*tools).peopleTools}},
	{"tags", []func(*tools, *mcp.Server){(*tools).tagTools}},
	{"activity", []func(*tools, *mcp.Server){(*tools).activityTools}},
	{"apps", []func(*tools, *mcp.Server){(*tools).appTools}},
}

// Toolsets are the names of the groups of tools.
var Toolsets = func() []string {
	out := make([]string, 0, len(toolsets))
	for _, set := range toolsets {
		out = append(out, set.name)
	}

	return out
}()

// New is the MCP server: of one request over HTTP, or of the one session on stdio.
func New(opts Options) *mcp.Server {
	t := &tools{access: opts.Access, interval: opts.Interval, log: opts.Log, readOnly: opts.ReadOnly}
	if t.interval <= 0 {
		t.interval = 5 * time.Second
	}

	if t.log == nil {
		t.log = slog.New(slog.DiscardHandler)
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "opencloud-mcp", Title: "OpenCloud", Version: opts.Version}, &mcp.ServerOptions{
		Logger:             t.log,
		SubscribeHandler:   t.subscribe,
		CompletionHandler:  t.complete,
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	t.server = s

	s.AddResourceTemplate(template, t.readResource)
	s.AddReceivingMiddleware(t.middleware)

	for _, set := range toolsets {
		if len(opts.Toolsets) > 0 && !slices.Contains(opts.Toolsets, set.name) {
			continue
		}

		for _, register := range set.register {
			register(t, s)
		}
	}

	return s
}

// add is mcp.AddTool, leaving out a tool that changes something when the server is read-only.
func add[In, Out any](t *tools, s *mcp.Server, tool *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	if t.readOnly && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint) {
		return
	}

	mcp.AddTool(s, tool, h)
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: new(false)}
}

// adds is a tool that writes but removes or replaces nothing.
func adds(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: new(false), OpenWorldHint: new(false)}
}

// changes is a tool that replaces or removes something.
func changes(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: new(true), OpenWorldHint: new(false)}
}

// idempotent is a tool that a second call with the same input changes nothing more.
func idempotent(a *mcp.ToolAnnotations) *mcp.ToolAnnotations {
	a.IdempotentHint = true
	return a
}

type tools struct {
	access   Access
	server   *mcp.Server
	interval time.Duration
	log      *slog.Logger
	readOnly bool
}

// middleware answers resources/list for the request's user, whose documents are not resources
// added to the server, and watches the spaces while a subscriptions/listen is open.
func (t *tools) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch r := req.(type) {
		case *mcp.ListResourcesRequest:
			return t.listResources(ctx, r)
		case *mcp.SubscriptionsListenRequest:
			if r.Params != nil && r.Params.Notifications != nil {
				listening, end := context.WithCancelCause(ctx)
				defer end(nil)

				go t.watch(listening, end, tokenInfo(ctx, r.Extra), r.Params.Notifications)

				res, err := next(listening, method, req)
				if cause := context.Cause(listening); errors.Is(cause, errSignInGone) {
					return nil, cause
				}

				return res, err
			}
		}

		return next(ctx, method, req)
	}
}

// client is the OpenCloud client for a call, signed in as the request's identity.
func (t *tools) client(ctx context.Context, req *mcp.CallToolRequest) (*opencloud.Client, error) {
	var info *auth.TokenInfo
	if req != nil && req.Extra != nil {
		info = req.Extra.TokenInfo
	}

	return t.access(ctx, info)
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

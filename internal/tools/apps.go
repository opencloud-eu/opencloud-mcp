package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) appTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:        "list_apps",
		Title:       "List the apps",
		Description: "List the apps OpenCloud opens files with, by file type, each with its name for open_in_app.",
		Annotations: readOnly("List the apps"),
	}, t.listApps)

	add(t, s, &mcp.Tool{
		Name:        "open_in_app",
		Title:       "Open a file in an app",
		Description: "Give the link that opens a file in an app of OpenCloud, like an office editor, for the user to follow.",
		Annotations: readOnly("Open a file in an app"),
	}, t.openInApp)

	add(t, s, &mcp.Tool{
		Name:        "create_document",
		Title:       "Create an office document",
		Description: "Create an empty office document of the type its extension names, like .odt, .ods or .odp, as list_apps names them; open_in_app opens it.",
		Annotations: adds("Create an office document"),
	}, t.createDocument)
}

type (
	launch struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path" jsonschema:"the file's path in the space, like reports/q3.docx"`
		App   string `json:"app,omitempty" jsonschema:"the app, by its name as list_apps gives it; the default for the file's type when left out"`
	}

	draft struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path" jsonschema:"where the document goes in the space, with its type as extension, like reports/q4.odt"`
	}
)

type apps struct {
	Apps []opencloud.App `json:"apps"`
}

func (t *tools) listApps(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, apps, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, apps{}, err
	}

	all, err := c.Apps(ctx)
	if err != nil {
		return nil, apps{}, err
	}

	var b strings.Builder

	for _, a := range all {
		fmt.Fprintf(&b, "%s: %s; opens %s", a.Name, a.Description, strings.Join(a.Types, ", "))

		if len(a.Creates) > 0 {
			fmt.Fprintf(&b, "; makes new %s", strings.Join(a.Creates, ", "))
		}

		b.WriteString("\n")
	}

	if len(all) == 0 {
		b.WriteString("OpenCloud has no apps to open files with.")
	}

	return text(b.String()), apps{Apps: all}, nil
}

func (t *tools) openInApp(ctx context.Context, req *mcp.CallToolRequest, in launch) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	link, err := c.OpenInApp(ctx, sp, p, in.App)
	if err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s opens at %s", where(sp, p), link)), nil, nil
}

func (t *tools) createDocument(ctx context.Context, req *mcp.CallToolRequest, in draft) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	if err := c.CreateDocument(ctx, sp, p); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Made the document %s; open_in_app gives the link to edit it.", where(sp, p))), nil, nil
}

package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *tools) documentTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:  "list_documents",
		Title: "List the documents of a space",
		Description: "List the readable documents of a space (markdown, text, CSV, PDF) as resource links, each with its title, " +
			"size and last modification, and the etag of the space, which moves with any change in it.",
		Annotations: readOnly("List the documents of a space"),
	}, t.listDocuments)
}

type (
	shelf struct {
		Space     string   `json:"space"`
		ETag      string   `json:"etag,omitempty"`
		Documents []listed `json:"documents"`
	}

	listed struct {
		URI          string `json:"uri"`
		Path         string `json:"path"`
		Title        string `json:"title"`
		MIMEType     string `json:"mimeType"`
		Size         int64  `json:"size"`
		LastModified string `json:"lastModified,omitempty"`
	}
)

func (t *tools) listDocuments(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, shelf, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, shelf{}, err
	}

	sp, err := c.Space(ctx, in.Space)
	if err != nil {
		return nil, shelf{}, err
	}

	docs, err := documents(ctx, c, sp)
	if err != nil {
		return nil, shelf{}, err
	}

	out := shelf{Space: sp.ID, ETag: sp.ETag, Documents: []listed{}}
	res := &mcp.CallToolResult{}

	for _, d := range docs {
		r := d.resource()
		out.Documents = append(out.Documents, listed{URI: r.URI, Path: r.Name, Title: r.Title, MIMEType: r.MIMEType, Size: r.Size, LastModified: lastModified(r)})
		res.Content = append(res.Content, &mcp.ResourceLink{URI: r.URI, Name: r.Name, Title: r.Title, MIMEType: r.MIMEType, Size: &r.Size, Annotations: r.Annotations})
	}

	if len(docs) == 0 {
		res.Content = []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("The space %s holds no documents.", sp.Name)}}
	}

	return res, out, nil
}

func lastModified(r *mcp.Resource) string {
	if r.Annotations == nil {
		return ""
	}

	return r.Annotations.LastModified
}

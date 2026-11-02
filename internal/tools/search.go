package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/kql"
	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// maxHits bounds a search; maxExamples bounds the examples a refused query is answered with.
const (
	maxHits     = 200
	maxExamples = 8
)

// language is the query language as a resource, the examples of OpenCloud's parity report.
var language = &mcp.Resource{
	URI:   "opencloud:///search/kql",
	Name:  "kql",
	Title: "OpenCloud's search query language",
	Description: "How to write a query for search: every example ran against both of OpenCloud's search engines with the " +
		"same answer.",
	MIMEType: "text/markdown",
}

func (t *tools) searchTools(s *mcp.Server) {
	s.AddResource(language, readGuide)

	add(t, s, &mcp.Tool{
		Name:  "search",
		Title: "Search",
		Description: "Search with OpenCloud's own search, in one space or in all of them: by words in the name or, where " +
			"the instance extracts it, the text; and by folder, tags, type, modification, size and favorites. " +
			"kql takes a query of OpenCloud's query language, like " + `tag:("invoice") AND NOT mediatype:pdf` + "; " +
			language.URI + " shows it by example. Gives each file's space and path, to read with read_file.",
		Annotations: readOnly("Search"),
	}, t.search)
}

type (
	query struct {
		Space          string   `json:"space,omitempty" jsonschema:"the space, by its name or id as list_spaces gives it; every space when left out"`
		Query          string   `json:"query,omitempty" jsonschema:"words to look for in names and text, like restore snapshot"`
		KQL            string   `json:"kql,omitempty" jsonschema:"a query of OpenCloud's query language, like tag:(\"invoice\") AND size>1000000"`
		Folder         string   `json:"folder,omitempty" jsonschema:"only in this folder of the space and below, like guides; needs the space"`
		Tags           []string `json:"tags,omitempty" jsonschema:"only what carries all of these tags"`
		Type           string   `json:"type,omitempty" jsonschema:"file, folder, document, spreadsheet, presentation, pdf, image, video, audio, archive, or a MIME type like text/markdown"`
		ModifiedAfter  string   `json:"modifiedAfter,omitempty" jsonschema:"only what changed after this, as 2026-01-31 or 2026-01-31T12:00:00Z; a date is midnight UTC"`
		ModifiedBefore string   `json:"modifiedBefore,omitempty" jsonschema:"only what changed before this, as 2026-01-31 or 2026-01-31T12:00:00Z"`
		LargerThan     int64    `json:"largerThan,omitempty" jsonschema:"only what is larger than this many bytes"`
		SmallerThan    int64    `json:"smallerThan,omitempty" jsonschema:"only what is smaller than this many bytes"`
		Favorites      bool     `json:"favorites,omitempty" jsonschema:"only the user's favorites"`
		Limit          int      `json:"limit,omitempty" jsonschema:"how many results to return, 1 to 200; 20 when left out"`
	}

	favorites struct {
		Space string `json:"space,omitempty" jsonschema:"the space, by its name or id as list_spaces gives it; every space when left out"`
		Limit int    `json:"limit,omitempty" jsonschema:"how many favorites to return, 1 to 200; 200 when left out"`
	}

	found struct {
		Query string          `json:"query"`
		Hits  []opencloud.Hit `json:"hits"`
	}
)

func (t *tools) search(ctx context.Context, req *mcp.CallToolRequest, in query) (*mcp.CallToolResult, found, error) {
	f := kql.Filter{Words: in.Query, Query: in.KQL, Tags: in.Tags, Type: in.Type, Larger: in.LargerThan, Smaller: in.SmallerThan, Favorites: in.Favorites}

	var err error

	if f.After, err = moment(in.ModifiedAfter); err != nil {
		return nil, found{}, err
	}

	if f.Before, err = moment(in.ModifiedBefore); err != nil {
		return nil, found{}, err
	}

	if in.Folder != "" {
		if in.Space == "" {
			return nil, found{}, errors.New("a folder needs its space")
		}

		if f.Folder, err = opencloud.Clean(in.Folder); err != nil {
			return nil, found{}, err
		}
	}

	return t.find(ctx, req, in.Space, f, in.Limit, 20)
}

func (t *tools) listFavorites(ctx context.Context, req *mcp.CallToolRequest, in favorites) (*mcp.CallToolResult, found, error) {
	return t.find(ctx, req, in.Space, kql.Filter{Favorites: true}, in.Limit, maxHits)
}

// find runs the filter's query in the space, or in every space when it is empty.
func (t *tools) find(ctx context.Context, req *mcp.CallToolRequest, space string, f kql.Filter, limit, otherwise int) (*mcp.CallToolResult, found, error) {
	q, err := f.KQL()
	if err != nil {
		return nil, found{}, err
	}

	if err := kql.Check(q); err != nil {
		return nil, found{}, withExamples(err, q)
	}

	c, err := t.client(ctx, req)
	if err != nil {
		return nil, found{}, err
	}

	var in *opencloud.Space

	if space != "" {
		sp, err := c.Space(ctx, space)
		if err != nil {
			return nil, found{}, err
		}

		in = &sp
	}

	if limit <= 0 {
		limit = otherwise
	}

	hits, err := c.Search(ctx, in, q, min(limit, maxHits))
	if errors.Is(err, opencloud.ErrBadQuery) {
		return nil, found{}, withExamples(err, q)
	} else if err != nil {
		return nil, found{}, err
	}

	var b strings.Builder

	for _, h := range hits {
		if in == nil {
			b.WriteString(h.SpaceName + ": ")
		}

		b.WriteString(h.Path)

		if h.Folder {
			b.WriteString("/")
		}

		if len(h.Tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(h.Tags, ", "))
		}

		if h.Highlights != "" {
			b.WriteString(": " + h.Highlights)
		}

		b.WriteString("\n")
	}

	if len(hits) == 0 {
		where := "any space"
		if in != nil {
			where = in.Name
		}

		fmt.Fprintf(&b, "Nothing in %s matches %s.", where, q)

		for _, d := range kql.Doubts(q) {
			b.WriteString(" " + d + ".")
		}
	}

	return text(b.String()), found{Query: q, Hits: hits}, nil
}

// withExamples is a refused query's error with the report's examples of the fields it uses.
func withExamples(err error, q string) error {
	var b strings.Builder

	b.WriteString(err.Error())
	b.WriteString(". Queries of these fields that work:")

	for _, c := range kql.Examples(q, maxExamples) {
		b.WriteString("\n" + c.Line())
	}

	fmt.Fprintf(&b, "\nMore in %s.", language.URI)

	return errors.New(b.String())
}

// moment is a time as a date or RFC 3339; the zero time for none.
func moment(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}

	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is no date like 2026-01-31 and no time like 2026-01-31T12:00:00Z", s)
	}

	return t, nil
}

func readGuide(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: language.URI, MIMEType: language.MIMEType, Text: kql.Guide()}}}, nil
}

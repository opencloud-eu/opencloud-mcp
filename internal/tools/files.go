package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/document"
	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) fileTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:        "list_folder",
		Title:       "List a folder",
		Description: "List the files and folders in a folder of a space, with their sizes.",
		Annotations: readOnly("List a folder"),
	}, t.listFolder)

	add(t, s, &mcp.Tool{
		Name:  "read_file",
		Title: "Read a file",
		Description: "Read a file of a space as text: markdown, plain text, CSV and other text as written, a PDF by its text layer. " +
			"A document past 100000 characters is cut, a markdown one with its outline for read_section.",
		Annotations: readOnly("Read a file"),
	}, t.readFile)

	add(t, s, &mcp.Tool{
		Name:  "read_section",
		Title: "Read a section of a document",
		Description: "Read one section of a markdown document by its heading, with everything under it down to the next heading " +
			"of its level. The exact heading wins, else the first that contains the words.",
		Annotations: readOnly("Read a section of a document"),
	}, t.readSection)

	add(t, s, &mcp.Tool{
		Name:        "write_file",
		Title:       "Write a file",
		Description: "Write a text file into a space, creating its folders. Replaces a file of that path.",
		Annotations: idempotent(changes("Write a file")),
	}, t.writeFile)

	add(t, s, &mcp.Tool{
		Name:  "get_item",
		Title: "Get a file or folder",
		Description: "Get a file or folder, by its path or by its id as search, a share, an activity or a notification names it, " +
			"with its space and path, size, last modification, permissions and tags, " +
			"whether it is a favorite, how it is shared, the photo or audio details OpenCloud has of it " +
			"and the link that opens it in OpenCloud.",
		Annotations: readOnly("Get a file or folder"),
	}, t.getItem)

	add(t, s, &mcp.Tool{
		Name:        "create_folder",
		Title:       "Create a folder",
		Description: "Create a folder in a space, with the folders above it.",
		Annotations: idempotent(adds("Create a folder")),
	}, t.createFolder)

	add(t, s, &mcp.Tool{
		Name:  "move_item",
		Title: "Move a file or folder",
		Description: "Move or rename a file or folder within its space, creating the folders on the way. OpenCloud moves " +
			"nothing from one space to another: copy_item into the other space, then delete_item.",
		Annotations: changes("Move a file or folder"),
	}, t.moveItem)

	add(t, s, &mcp.Tool{
		Name:        "copy_item",
		Title:       "Copy a file or folder",
		Description: "Copy a file or folder, within its space or into another one, creating the folders on the way.",
		Annotations: changes("Copy a file or folder"),
	}, t.copyItem)

	add(t, s, &mcp.Tool{
		Name:        "delete_item",
		Title:       "Delete a file or folder",
		Description: "Delete a file or folder into the trash of its space, from where restore_from_trash brings it back.",
		Annotations: changes("Delete a file or folder"),
	}, t.deleteItem)

	add(t, s, &mcp.Tool{
		Name:        "add_favorite",
		Title:       "Add a favorite",
		Description: "Mark a file or folder as a favorite of the user.",
		Annotations: idempotent(adds("Add a favorite")),
	}, t.addFavorite)

	add(t, s, &mcp.Tool{
		Name:        "remove_favorite",
		Title:       "Remove a favorite",
		Description: "Unmark a file or folder as a favorite of the user.",
		Annotations: idempotent(changes("Remove a favorite")),
	}, t.removeFavorite)

	add(t, s, &mcp.Tool{
		Name:        "preview_image",
		Title:       "Preview an image",
		Description: "Show an image, or the thumbnail OpenCloud makes of a document, as an image.",
		Annotations: readOnly("Preview an image"),
	}, t.previewImage)

	add(t, s, &mcp.Tool{
		Name:        "list_favorites",
		Title:       "List the favorites",
		Description: "List the files and folders the user marked as favorites, in one space or in all of them.",
		Annotations: readOnly("List the favorites"),
	}, t.listFavorites)
}

type (
	folder struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path,omitempty" jsonschema:"a folder of the space, like guides; empty for its top"`
	}

	listing struct {
		Entries []opencloud.Entry `json:"entries"`
	}

	file struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path" jsonschema:"the file's path in the space, like guides/restore.md"`
	}

	opening struct {
		Space   string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path    string `json:"path" jsonschema:"the document's path in the space, like guides/restore.md"`
		Heading string `json:"heading" jsonschema:"a heading of the document, like Restore a single file"`
	}

	note struct {
		Space   string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path    string `json:"path" jsonschema:"where the file goes in the space, like notes/case-123.md"`
		Content string `json:"content" jsonschema:"the file's text"`
	}

	item struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path" jsonschema:"a file or folder of the space, like guides/restore.md"`
	}

	locator struct {
		Space string `json:"space,omitempty" jsonschema:"the space, by its name or id as list_spaces gives it; not needed with id"`
		Path  string `json:"path,omitempty" jsonschema:"a file or folder of the space, like guides/restore.md"`
		ID    string `json:"id,omitempty" jsonschema:"the item's id, as search, a share or a notification names it; instead of space and path"`
	}

	relocation struct {
		Space     string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path      string `json:"path" jsonschema:"a file or folder of the space, like guides/restore.md"`
		To        string `json:"to" jsonschema:"the new path in the same space, like archive/2026/restore.md"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"replace what is at the new path; refused when left out"`
	}

	transfer struct {
		Space     string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path      string `json:"path" jsonschema:"a file or folder of the space, like guides/restore.md"`
		To        string `json:"to" jsonschema:"the new path, like archive/2026/restore.md"`
		ToSpace   string `json:"toSpace,omitempty" jsonschema:"the space it goes to, by its name or id; its own space when left out"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"replace what is at the new path; refused when left out"`
	}

	picture struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path  string `json:"path" jsonschema:"an image or document of the space, like photos/team.jpg"`
		Size  int    `json:"size,omitempty" jsonschema:"the longer side in pixels, 32 to 1024; 512 when left out"`
	}
)

func (t *tools) listFolder(ctx context.Context, req *mcp.CallToolRequest, in folder) (*mcp.CallToolResult, listing, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, listing{}, err
	}

	sp, err := c.Space(ctx, in.Space)
	if err != nil {
		return nil, listing{}, err
	}

	entries, err := c.List(ctx, sp, in.Path)
	if err != nil {
		return nil, listing{}, err
	}

	var b strings.Builder

	for _, e := range entries {
		if e.Folder {
			fmt.Fprintf(&b, "%s/\n", e.Path)
		} else {
			fmt.Fprintf(&b, "%s (%d bytes)\n", e.Path, e.Size)
		}
	}

	if len(entries) == 0 {
		b.WriteString("The folder is empty.")
	}

	return text(b.String()), listing{Entries: entries}, nil
}

func (t *tools) readFile(ctx context.Context, req *mcp.CallToolRequest, in file) (*mcp.CallToolResult, any, error) {
	body, err := t.read(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	return text(asRead(body)), nil, nil
}

// asRead is a document's text as a read gives it: cut past maxText, a cut markdown document
// with its outline.
func asRead(body string) string {
	cut := clip(body)
	if outline := document.Outline(body); cut != body && len(outline) > 0 {
		cut += "\nIts outline: " + strings.Join(outline, "; ")
	}

	return cut
}

func (t *tools) readSection(ctx context.Context, req *mcp.CallToolRequest, in opening) (*mcp.CallToolResult, any, error) {
	body, err := t.read(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	section, ok := document.Section(body, in.Heading)
	if !ok {
		outline := document.Outline(body)
		if len(outline) == 0 {
			return nil, nil, fmt.Errorf("%s has no headings; read it whole with read_file", in.Path)
		}

		return nil, nil, fmt.Errorf("%s has no heading %q; its headings are: %s", in.Path, in.Heading, strings.Join(outline, "; "))
	}

	return text(clip(section)), nil, nil
}

func (t *tools) writeFile(ctx context.Context, req *mcp.CallToolRequest, in note) (*mcp.CallToolResult, any, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	sp, err := c.Space(ctx, in.Space)
	if err != nil {
		return nil, nil, err
	}

	if err := c.Write(ctx, sp, in.Path, []byte(in.Content)); err != nil {
		return nil, nil, err
	}

	p, err := opencloud.Clean(in.Path)
	if err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Wrote %s in %s (%d bytes).", p, sp.Name, len(in.Content))), nil, nil
}

func (t *tools) addFavorite(ctx context.Context, req *mcp.CallToolRequest, in item) (*mcp.CallToolResult, any, error) {
	return t.favorite(ctx, req, in, true)
}

func (t *tools) removeFavorite(ctx context.Context, req *mcp.CallToolRequest, in item) (*mcp.CallToolResult, any, error) {
	return t.favorite(ctx, req, in, false)
}

func (t *tools) favorite(ctx context.Context, req *mcp.CallToolRequest, in item, on bool) (*mcp.CallToolResult, any, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	sp, err := c.Space(ctx, in.Space)
	if err != nil {
		return nil, nil, err
	}

	if err := c.Favorite(ctx, sp, in.Path, on); err != nil {
		return nil, nil, err
	}

	p, err := opencloud.Clean(in.Path)
	if err != nil {
		return nil, nil, err
	}

	if on {
		return text(fmt.Sprintf("%s in %s is a favorite.", p, sp.Name)), nil, nil
	}

	return text(fmt.Sprintf("%s in %s is no favorite.", p, sp.Name)), nil, nil
}

func (t *tools) getItem(ctx context.Context, req *mcp.CallToolRequest, in locator) (*mcp.CallToolResult, opencloud.Item, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, opencloud.Item{}, err
	}

	var it opencloud.Item

	switch {
	case in.ID != "":
		it, err = c.ItemByID(ctx, in.ID)
	case in.Space == "":
		return nil, opencloud.Item{}, errors.New("name the space and the path, or the id")
	default:
		var sp opencloud.Space
		if sp, err = c.Space(ctx, in.Space); err == nil {
			it, err = c.Item(ctx, sp, in.Path)
		}
	}

	if err != nil {
		return nil, opencloud.Item{}, err
	}

	return text(describe(it)), it, nil
}

// describe is an item in lines a model reads.
func describe(it opencloud.Item) string {
	var b strings.Builder

	where := it.Path
	if where == "" {
		where = "the top"
	} else if it.Folder {
		where += "/"
	}

	fmt.Fprintf(&b, "%s in %s, id %s\n", where, cmp.Or(it.SpaceName, it.Space), it.ID)

	if it.Folder {
		b.WriteString("a folder")
	} else {
		fmt.Fprintf(&b, "%s, %d bytes", it.MIMEType, it.Size)
	}

	if !it.Modified.IsZero() {
		fmt.Fprintf(&b, ", changed %s", it.Modified.UTC().Format(time.RFC3339))
	}

	b.WriteString("\n")

	for _, l := range []struct {
		name   string
		values []string
	}{{"tags", it.Tags}, {"shared", it.Shared}, {"the user may", it.Permissions}} {
		if len(l.values) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", l.name, strings.Join(l.values, ", "))
		}
	}

	if it.Favorite {
		b.WriteString("a favorite\n")
	}

	for _, f := range []struct {
		name  string
		set   bool
		facet any
	}{
		{"image", it.Image != nil, it.Image}, {"photo", it.Photo != nil, it.Photo}, {"audio", it.Audio != nil, it.Audio},
		{"video", it.Video != nil, it.Video}, {"location", it.Location != nil, it.Location},
	} {
		if f.set {
			j, _ := json.Marshal(f.facet) //nolint:errchkjson // a facet of plain numbers and strings
			fmt.Fprintf(&b, "%s: %s\n", f.name, j)
		}
	}

	if it.Link != "" {
		fmt.Fprintf(&b, "opens at %s\n", it.Link)
	}

	return b.String()
}

func (t *tools) createFolder(ctx context.Context, req *mcp.CallToolRequest, in item) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	if err := c.MakeFolder(ctx, sp, p); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s is a folder in %s.", p, sp.Name)), nil, nil
}

func (t *tools) moveItem(ctx context.Context, req *mcp.CallToolRequest, in relocation) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	to, err := opencloud.Clean(in.To)
	if err != nil {
		return nil, nil, err
	}

	if err := c.Move(ctx, sp, p, to, in.Overwrite); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Moved %s to %s in %s.", p, to, sp.Name)), nil, nil
}

func (t *tools) copyItem(ctx context.Context, req *mcp.CallToolRequest, in transfer) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	dst := sp
	if in.ToSpace != "" {
		if dst, err = c.Space(ctx, in.ToSpace); err != nil {
			return nil, nil, err
		}
	}

	to, err := opencloud.Clean(in.To)
	if err != nil {
		return nil, nil, err
	}

	if err := c.Copy(ctx, sp, p, dst, to, in.Overwrite); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Copied %s in %s to %s in %s.", p, sp.Name, to, dst.Name)), nil, nil
}

func (t *tools) deleteItem(ctx context.Context, req *mcp.CallToolRequest, in item) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	if err := c.Delete(ctx, sp, p); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Deleted %s from %s; it is in the trash of %s now.", p, sp.Name, sp.Name)), nil, nil
}

func (t *tools) previewImage(ctx context.Context, req *mcp.CallToolRequest, in picture) (*mcp.CallToolResult, any, error) {
	size := cmp.Or(in.Size, 512)
	if size < 32 || size > 1024 {
		return nil, nil, fmt.Errorf("a preview is 32 to 1024 pixels, not %d", size)
	}

	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	b, kind, err := c.Preview(ctx, sp, p, size)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: b, MIMEType: kind}}}, nil, nil
}

// at is the client, the space and the clean path a tool's input names.
func (t *tools) at(ctx context.Context, req *mcp.CallToolRequest, space, p string) (*opencloud.Client, opencloud.Space, string, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, opencloud.Space{}, "", err
	}

	sp, err := c.Space(ctx, space)
	if err != nil {
		return nil, opencloud.Space{}, "", err
	}

	p, err = opencloud.Clean(p)
	if err != nil {
		return nil, opencloud.Space{}, "", err
	}

	return c, sp, p, nil
}

// read is a file of the space as text.
func (t *tools) read(ctx context.Context, req *mcp.CallToolRequest, space, p string) (string, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return "", err
	}

	sp, err := c.Space(ctx, space)
	if err != nil {
		return "", err
	}

	b, err := c.Read(ctx, sp, p)
	if err != nil {
		return "", err
	}

	body, err := document.Text(p, b)
	if errors.Is(err, document.ErrNotText) {
		return "", fmt.Errorf("%s is not a text file or PDF", p)
	} else if err != nil {
		return "", fmt.Errorf("%s cannot be read as text: %w", p, err)
	}

	return body, nil
}

// clip cuts s past maxText characters, saying so.
func clip(s string) string {
	if r := []rune(s); len(r) > maxText {
		return string(r[:maxText]) + fmt.Sprintf("\n\n(Cut here: the first %d of %d characters.)", maxText, len(r))
	}

	return s
}

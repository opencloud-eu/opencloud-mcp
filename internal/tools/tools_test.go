package tools

import (
	"bytes"
	"image"
	"image/png"
	"maps"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud/opencloudtest"
)

const token = "app-token-4711"

const guide = "# Restore\n\nPick a snapshot first.\n\n## A single file\n\nRun lark restore --file PATH.\n\n## Everything\n\nRun lark restore --all."

// connect is a client session on the server, working as alice on a fake OpenCloud with her personal
// space and the project space Docs, beside bob with his own personal space.
func connect(t *testing.T) (*mcp.ClientSession, *opencloudtest.Cloud) {
	cs, _, fake := connectBoth(t)

	return cs, fake
}

// connectBoth is a session as alice and one as bob on the fake OpenCloud of connect.
func connectBoth(t *testing.T) (*mcp.ClientSession, *mcp.ClientSession, *opencloudtest.Cloud) {
	t.Helper()

	fake := opencloudtest.New("alice", token)
	if err := fake.Space("personal-id", "Alice", "personal", map[string]string{"/mine.md": "Alice's own note."}); err != nil {
		t.Fatal(err)
	}

	if err := fake.Space("storage$docs", "Docs", "project", map[string]string{
		"/restore.md":          guide,
		"/guides/install.md":   "Install with apt install larkspur-agent.",
		"/guides/diagram.png":  "\x89PNG\x00",
		"/guides/plain.txt":    "No headings here.",
		"/releases/notes.json": `{"version": "1.2"}`,
	}); err != nil {
		t.Fatal(err)
	}

	fake.Account("bob", "bob-"+token)

	if err := fake.SpaceOf("bob", "bob-id", "Bob", "personal", map[string]string{"/bobs.md": "Bob's own note."}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	cloud, err := opencloud.New(opencloud.Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	return session(t, cloud, "alice", token), session(t, cloud, "bob", "bob-"+token), fake
}

// session is a client session on the server as user.
func session(t *testing.T, cloud *opencloud.Cloud, user, tok string) *mcp.ClientSession {
	t.Helper()

	c, err := cloud.AppToken(user, opencloud.Secret(tok))
	if err != nil {
		t.Fatal(err)
	}

	st, ct := mcp.NewInMemoryTransports()

	ss, err := New(Options{Access: As(c), Version: "test"}).Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// call is the text a tool answers and whether it is an error.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	var b strings.Builder

	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}

	return b.String(), res.IsError
}

// kinds is every tool by what it does to the user's OpenCloud: it reads, adds or changes.
var kinds = map[string]string{
	"list_spaces": "reads", "get_space": "reads", "create_space": "adds", "update_space": "changes",
	"disable_space": "changes", "restore_space": "adds", "delete_space": "changes",
	"list_space_members": "reads", "add_space_member": "adds", "update_space_member": "changes", "remove_space_member": "changes",

	"list_folder": "reads", "read_file": "reads", "read_section": "reads", "write_file": "changes", "get_item": "reads",
	"create_folder": "adds", "move_item": "changes", "copy_item": "changes", "delete_item": "changes",
	"add_favorite": "adds", "remove_favorite": "changes", "preview_image": "reads",

	"list_documents": "reads", "search": "reads", "list_favorites": "reads",

	"list_trash": "reads", "restore_from_trash": "adds", "delete_from_trash": "changes", "empty_trash": "changes",

	"list_versions": "reads", "read_version": "reads", "restore_version": "changes",

	"list_share_roles": "reads", "list_permissions": "reads", "share_item": "adds", "create_link": "adds",
	"update_permission": "changes", "set_link_password": "changes", "remove_permission": "changes",
	"list_shared_with_me": "reads", "list_shared_by_me": "reads", "accept_share": "adds", "decline_share": "changes",
	"hide_share": "changes",

	"get_me": "reads", "find_users": "reads", "find_groups": "reads",

	"list_tags": "reads", "add_tags": "adds", "remove_tags": "changes",

	"list_activities": "reads", "list_notifications": "reads", "dismiss_notifications": "changes",

	"list_apps": "reads", "open_in_app": "reads", "create_document": "adds",
}

func TestTheToolsSayWhatTheyChange(t *testing.T) {
	cs, _ := connect(t)

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Tools) != len(kinds) {
		t.Errorf("%d tools, want %d", len(res.Tools), len(kinds))
	}

	for _, tool := range res.Tools {
		a := tool.Annotations
		if a == nil || tool.Description == "" || tool.InputSchema == nil {
			t.Fatalf("%s lacks annotations, a description or a schema", tool.Name)
		}

		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s is marked open world", tool.Name)
		}

		got := "reads"

		switch {
		case a.ReadOnlyHint:
		case a.DestructiveHint == nil:
			t.Errorf("%s writes but does not say whether it removes", tool.Name)
		case *a.DestructiveHint:
			got = "changes"
		default:
			got = "adds"
		}

		if want, ok := kinds[tool.Name]; !ok || got != want {
			t.Errorf("%s %s, want %q", tool.Name, got, want)
		}
	}
}

func TestTheSpacesAndAFolderAreListed(t *testing.T) {
	cs, _ := connect(t)

	if out, isErr := call(t, cs, "list_spaces", nil); isErr || out != "Alice (personal, id personal-id)\nDocs (project, id storage$docs)\n" {
		t.Errorf("spaces %q", out)
	}

	out, isErr := call(t, cs, "list_folder", map[string]any{"space": "docs", "path": "guides"})
	if isErr || !strings.Contains(out, "guides/install.md (40 bytes)\n") || !strings.Contains(out, "guides/diagram.png") {
		t.Errorf("list %q", out)
	}

	if out, _ = call(t, cs, "list_folder", map[string]any{"space": "storage$docs"}); !strings.Contains(out, "guides/\n") || !strings.Contains(out, "restore.md (") {
		t.Errorf("the top by id %q", out)
	}
}

func TestAFileAndASectionAreRead(t *testing.T) {
	cs, _ := connect(t)

	if out, isErr := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "/guides/install.md"}); isErr || out != "Install with apt install larkspur-agent." {
		t.Errorf("read %q", out)
	}

	if out, isErr := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "releases/notes.json"}); isErr || out != `{"version": "1.2"}` {
		t.Errorf("other text %q", out)
	}

	if out, isErr := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "guides/diagram.png"}); !isErr || !strings.Contains(out, "not a text file or PDF") {
		t.Errorf("an image %q", out)
	}

	if out, isErr := call(t, cs, "read_section", map[string]any{"space": "Docs", "path": "restore.md", "heading": "single file"}); isErr ||
		out != "## A single file\n\nRun lark restore --file PATH." {
		t.Errorf("section %q", out)
	}

	if out, isErr := call(t, cs, "read_section", map[string]any{"space": "Docs", "path": "restore.md", "heading": "Downgrade"}); !isErr ||
		!strings.Contains(out, `restore.md has no heading "Downgrade"; its headings are: # Restore; ## A single file; ## Everything`) {
		t.Errorf("a missing heading %q", out)
	}

	if out, isErr := call(t, cs, "read_section", map[string]any{"space": "Docs", "path": "guides/plain.txt", "heading": "Intro"}); !isErr ||
		!strings.Contains(out, "has no headings") {
		t.Errorf("a document without headings %q", out)
	}
}

func TestAFileIsWrittenAndFound(t *testing.T) {
	cs, fake := connect(t)

	if out, isErr := call(t, cs, "write_file", map[string]any{"space": "Docs", "path": "/notes/case-17.md", "content": "Restore failed."}); isErr ||
		out != "Wrote notes/case-17.md in Docs (15 bytes)." {
		t.Fatalf("write %q", out)
	}

	if out, _ := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "notes/case-17.md"}); out != "Restore failed." {
		t.Errorf("read back %q", out)
	}

	out, isErr := call(t, cs, "search", map[string]any{"space": "Docs", "query": "restore"})
	if isErr || !strings.Contains(out, "restore.md") || !strings.Contains(out, "notes/case-17.md: <mark>restore</mark>") {
		t.Errorf("search %q", out)
	}

	if out, _ := call(t, cs, "search", map[string]any{"space": "Docs", "query": "nothing like it"}); out != `Nothing in Docs matches (name:"*nothing like it*" OR content:"nothing like it").` {
		t.Errorf("no hits %q", out)
	}

	// the personal space is never searched for Docs
	if out, _ := call(t, cs, "search", map[string]any{"space": "Docs", "query": "own note"}); strings.Contains(out, "mine.md") {
		t.Errorf("a search crossed spaces %q", out)
	}

	// without a space it looks in every space, and says whose each hit is
	if out, _ := call(t, cs, "search", map[string]any{"query": "own note"}); out != "Alice: mine.md: <mark>own note</mark>\n" {
		t.Errorf("across spaces %q", out)
	}

	if fake.Wrong.Load() != 0 {
		t.Errorf("%d requests went without the token", fake.Wrong.Load())
	}
}

func TestTheFiltersBecomeOneQuery(t *testing.T) {
	cs, fake := connect(t)

	_, isErr := call(t, cs, "search", map[string]any{
		"space": "Docs", "query": "report", "folder": "/guides/", "tags": []string{"final"}, "type": "pdf",
		"modifiedAfter": "2026-01-31", "modifiedBefore": "2026-02-01T12:00:00+01:00", "largerThan": 1000, "favorites": true,
	})
	if isErr {
		t.Fatal("the search failed")
	}

	if out, _ := call(t, cs, "list_favorites", nil); !strings.HasPrefix(out, "Nothing in any space matches is:favorite") {
		t.Errorf("favorites %q", out)
	}

	want := []string{
		`(name:"*report*" OR content:"report") AND path:"./guides" AND tag:("final") AND mediatype:pdf AND mtime>"2026-01-31T00:00:00Z" AND ` +
			`mtime<"2026-02-01T11:00:00Z" AND size>1000 AND is:favorite`,
		"is:favorite",
	}
	if got := fake.Searches(); !slices.Equal(got, want) {
		t.Errorf("queries %q", got)
	}
}

func TestAFavoriteIsAddedListedAndRemoved(t *testing.T) {
	cs, _ := connect(t)

	for range 2 {
		if out, isErr := call(t, cs, "add_favorite", map[string]any{"space": "Docs", "path": "/guides/install.md"}); isErr || out != "guides/install.md in Docs is a favorite." {
			t.Fatalf("add %q", out)
		}
	}

	if out, _ := call(t, cs, "list_favorites", nil); out != "Docs: guides/install.md\n" {
		t.Errorf("list %q", out)
	}

	for range 2 {
		if out, isErr := call(t, cs, "remove_favorite", map[string]any{"space": "Docs", "path": "guides/install.md"}); isErr || out != "guides/install.md in Docs is no favorite." {
			t.Fatalf("remove %q", out)
		}
	}

	if out, _ := call(t, cs, "list_favorites", map[string]any{"space": "Docs"}); out != "Nothing in Docs matches is:favorite." {
		t.Errorf("list after removing %q", out)
	}

	if out, isErr := call(t, cs, "add_favorite", map[string]any{"space": "Docs", "path": "nope.md"}); !isErr || !strings.Contains(out, "is not in the space Docs") {
		t.Errorf("a missing file %q", out)
	}
}

func TestTheFileToolsWork(t *testing.T) {
	cs, fake := connect(t)

	steps := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"create_folder", map[string]any{"space": "Docs", "path": "/archive/2026/"}, "archive/2026 is a folder in Docs."},
		{"create_folder", map[string]any{"space": "Docs", "path": "archive/2026"}, "archive/2026 is a folder in Docs."},
		{"copy_item", map[string]any{"space": "Docs", "path": "restore.md", "to": "archive/2026/restore.md"}, "Copied restore.md in Docs to archive/2026/restore.md in Docs."},
		{"copy_item", map[string]any{"space": "Docs", "path": "guides", "to": "from-docs/guides", "toSpace": "Alice"}, "Copied guides in Docs to from-docs/guides in Alice."},
		{"move_item", map[string]any{"space": "Docs", "path": "archive/2026/restore.md", "to": "archive/old.md"}, "Moved archive/2026/restore.md to archive/old.md in Docs."},
		{"delete_item", map[string]any{"space": "Docs", "path": "archive"}, "Deleted archive from Docs; it is in the trash of Docs now."},
	}

	for _, s := range steps {
		if out, isErr := call(t, cs, s.tool, s.args); isErr || out != s.want {
			t.Fatalf("%s: %q", s.tool, out)
		}
	}

	if out, _ := call(t, cs, "read_file", map[string]any{"space": "Alice", "path": "from-docs/guides/install.md"}); out != "Install with apt install larkspur-agent." {
		t.Errorf("the copy in the other space %q", out)
	}

	out, isErr := call(t, cs, "get_item", map[string]any{"space": "Docs", "path": "guides/install.md"})
	if isErr || !strings.HasPrefix(out, "guides/install.md in Docs, id storage$docs!") || !strings.Contains(out, "text/markdown, 40 bytes, changed ") ||
		!strings.Contains(out, "the user may: content/read, standard/delete, deleted/delete\n") || !strings.Contains(out, "opens at https://cloud.example.com/f/") {
		t.Errorf("get_item %q", out)
	}

	if out, _ := call(t, cs, "get_item", map[string]any{"space": "Docs"}); !strings.HasPrefix(out, "the top in Docs") || !strings.Contains(out, "a folder") {
		t.Errorf("the top %q", out)
	}

	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 2000, 1000))); err != nil {
		t.Fatal(err)
	}

	if err := fake.Put("storage$docs", "/team.png", pic.String()); err != nil {
		t.Fatal(err)
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "preview_image", Arguments: map[string]any{"space": "Docs", "path": "team.png"}})
	if err != nil || res.IsError || len(res.Content) != 1 {
		t.Fatalf("preview %+v %v", res, err)
	}

	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" {
		t.Fatalf("preview content %+v", res.Content[0])
	}

	if cfg, err := png.DecodeConfig(bytes.NewReader(img.Data)); err != nil || cfg.Width != 512 || cfg.Height != 256 {
		t.Errorf("the preview is %dx%d %v, want 512x256", cfg.Width, cfg.Height, err)
	}

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"move_item", map[string]any{"space": "Docs", "path": "restore.md", "to": "guides/install.md"}, "overwrite replaces it"},
		{"move_item", map[string]any{"space": "Docs", "path": "nope.md", "to": "x.md"}, `"nope.md" is not in the space Docs`},
		{"delete_item", map[string]any{"space": "Docs", "path": ""}, "not a file path"},
		{"create_folder", map[string]any{"space": "Docs", "path": "restore.md"}, "is a file in the space Docs, not a folder"},
		{"preview_image", map[string]any{"space": "Docs", "path": "restore.md"}, "OpenCloud makes no preview of restore.md (text/markdown)"},
		{"preview_image", map[string]any{"space": "Docs", "path": "team.png", "size": 4096}, "a preview is 32 to 1024 pixels"},
		{"get_item", map[string]any{}, "name the space and the path, or the id"},
		{"get_item", map[string]any{"id": "nothing"}, "is no id of a file or folder"},
	} {
		if out, isErr := call(t, cs, c.tool, c.args); !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %v: %q", c.tool, c.args, out)
		}
	}
}

func TestTheTrashAndTheVersionsWork(t *testing.T) {
	cs, _ := connect(t)

	for _, content := range []string{"first", "second"} {
		if out, isErr := call(t, cs, "write_file", map[string]any{"space": "Docs", "path": "guides/install.md", "content": content}); isErr {
			t.Fatal(out)
		}
	}

	out, isErr := call(t, cs, "list_versions", map[string]any{"space": "Docs", "path": "guides/install.md"})
	keys := regexp.MustCompile(`key (\S+)`).FindAllStringSubmatch(out, -1)

	if isErr || len(keys) != 2 || !strings.Contains(out, ", 5 bytes, key ") || !strings.Contains(out, ", 40 bytes, key ") {
		t.Fatalf("list_versions %q", out)
	}

	// the newest first: "first", then the seed's text
	if out, _ := call(t, cs, "read_version", map[string]any{"space": "Docs", "path": "guides/install.md", "version": keys[1][1]}); out != "Install with apt install larkspur-agent." {
		t.Errorf("read_version %q", out)
	}

	if out, isErr := call(t, cs, "restore_version", map[string]any{"space": "Docs", "path": "guides/install.md", "version": keys[0][1]}); isErr {
		t.Fatal(out)
	}

	if out, _ := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "guides/install.md"}); out != "first" {
		t.Errorf("after restore_version %q", out)
	}

	for _, p := range []string{"guides", "restore.md"} {
		if out, isErr := call(t, cs, "delete_item", map[string]any{"space": "Docs", "path": p}); isErr {
			t.Fatal(out)
		}
	}

	out, isErr = call(t, cs, "list_trash", map[string]any{})
	trashed := regexp.MustCompile(`(?m)^(\S+) in Docs \((a folder|\d+ bytes)\), deleted \S+, key (\S+)$`).FindAllStringSubmatch(out, -1)

	if isErr || len(trashed) != 2 {
		t.Fatalf("list_trash %q", out)
	}

	keyOf := map[string]string{trashed[0][1]: trashed[0][3], trashed[1][1]: trashed[1][3]}

	steps := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"restore_from_trash", map[string]any{"space": "Docs", "key": keyOf["guides"]}, "Restored guides in Docs."},
		{"restore_from_trash", map[string]any{"space": "Docs", "key": keyOf["restore.md"], "to": "old/restore.md"}, "Restored old/restore.md in Docs."},
		{"delete_item", map[string]any{"space": "Docs", "path": "old"}, "Deleted old from Docs; it is in the trash of Docs now."},
		{"empty_trash", map[string]any{"space": "Docs"}, "The trash of Docs is empty."},
		{"list_trash", map[string]any{"space": "Docs"}, "The trash is empty."},
		{"delete_from_trash", map[string]any{"space": "Docs", "key": keyOf["guides"]}, keyOf["guides"] + " is not in the trash of Docs any more."},
	}

	for _, s := range steps {
		if out, isErr := call(t, cs, s.tool, s.args); isErr || out != s.want {
			t.Fatalf("%s %v: %q", s.tool, s.args, out)
		}
	}

	if out, _ := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "guides/install.md"}); out != "first" {
		t.Errorf("the restored file %q", out)
	}

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"restore_from_trash", map[string]any{"space": "Docs", "key": keyOf["guides"]}, "is not in the trash of the space Docs"},
		{"list_versions", map[string]any{"space": "Docs", "path": "guides"}, "only a file has versions"},
		{"read_version", map[string]any{"space": "Docs", "path": "guides/install.md", "version": "nope"}, "has no version"},
		{"read_version", map[string]any{"space": "Docs", "path": "guides/install.md", "version": "../x"}, "is no key of a version"},
	} {
		if out, isErr := call(t, cs, c.tool, c.args); !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %v: %q", c.tool, c.args, out)
		}
	}
}

func TestAFileIsSharedLinkedAcceptedAndUnshared(t *testing.T) {
	alice, bob, fake := connectBoth(t)
	fake.Group("Support", "bob")

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"get_me", map[string]any{}, "Alice Example (user alice, alice@example.com, id user-alice)\nlanguage: en\n"},
		{"find_users", map[string]any{"query": "bob"}, "Bob Example (user bob, id user-bob)\n"},
		{"find_groups", map[string]any{"query": "supp"}, "Support (group, id group-Support)\n"},
		{"list_permissions", map[string]any{"space": "Docs", "path": "guides/install.md"}, "It is shared with nobody and has no links.\nroles to share with: Can view, Can edit\n"},
	} {
		if out, isErr := call(t, alice, c.tool, c.args); isErr || out != c.want {
			t.Errorf("%s: %q", c.tool, out)
		}
	}

	out, isErr := call(t, alice, "share_item", map[string]any{
		"space": "Docs", "path": "guides/install.md", "recipients": []string{"bob", "Support"}, "role": "can edit", "expires": "2027-01-31",
	})
	if isErr || !strings.HasPrefix(out, "Shared guides/install.md in Docs with:\n") ||
		!strings.Contains(out, "Bob Example (user, id user-bob): Can edit, until 2027-01-31T00:00:00Z, permission share-") ||
		!strings.Contains(out, "Support (group, id group-Support): Can edit") {
		t.Fatalf("share_item %q", out)
	}

	share := regexp.MustCompile(`permission (share-\d+)`).FindStringSubmatch(out)[1]

	if out, isErr := call(t, alice, "create_link", map[string]any{"space": "Docs", "path": "guides", "type": "view"}); !isErr || !strings.Contains(out, "password protection is enforced") {
		t.Errorf("a link without a password %q", out)
	}

	out, isErr = call(t, alice, "create_link", map[string]any{"space": "Docs", "path": "guides", "password": "Demo-Pass-2026!", "name": "For the auditors"})
	if isErr || !strings.HasPrefix(out, `Made a view link https://cloud.example.com/s/link-`) || !strings.Contains(out, `named "For the auditors", with a password`) {
		t.Fatalf("create_link %q", out)
	}

	link := regexp.MustCompile(`permission (link-\d+)`).FindStringSubmatch(out)[1]

	steps := []struct {
		user *mcp.ClientSession
		tool string
		args map[string]any
		want string
	}{
		{alice, "update_permission", map[string]any{"space": "Docs", "path": "guides/install.md", "permission": share, "role": "Can view", "expires": "never"},
			"Changed: Bob Example (user, id user-bob): Can view, permission " + share + ", of guides/install.md in Docs."},
		{alice, "update_permission", map[string]any{"space": "Docs", "path": "guides", "permission": link, "type": "edit", "name": "Renamed"},
			"Changed: an edit link https://cloud.example.com/s/" + link + ` named "Renamed", with a password, permission ` + link + ", of guides in Docs."},
		{alice, "set_link_password", map[string]any{"space": "Docs", "path": "guides", "permission": link, "password": "Other-Pass-2027!"},
			"The link " + link + " of guides in Docs has the new password."},
		{bob, "decline_share", map[string]any{"share": "install.md"}, "install.md from Alice Example, Can view, not accepted, share shares-jail!" + share + "."},
		{bob, "decline_share", map[string]any{"share": "install.md"}, "install.md from Alice Example, Can view, not accepted, share shares-jail!" + share + "."},
		{bob, "accept_share", map[string]any{"share": "shares-jail!" + share}, "install.md from Alice Example, Can view, accepted, share shares-jail!" + share + "."},
		{bob, "hide_share", map[string]any{"share": "install.md", "hidden": true}, "install.md from Alice Example, Can view, accepted, hidden, share shares-jail!" + share + "."},
		{bob, "list_shared_with_me", map[string]any{}, "install.md from Alice Example, Can view, accepted, hidden, share shares-jail!" + share + "\n"},
		{alice, "list_shared_by_me", map[string]any{"only": "links"}, "guides in Docs:\n  an edit link https://cloud.example.com/s/" + link + ` named "Renamed", with a password, permission ` + link + "\n"},
		{alice, "remove_permission", map[string]any{"space": "Docs", "path": "guides", "permission": link}, "Removed " + link + " of guides in Docs."},
	}

	for _, s := range steps {
		if out, isErr := call(t, s.user, s.tool, s.args); isErr || out != s.want {
			t.Fatalf("%s %v: %q", s.tool, s.args, out)
		}
	}

	if out, _ := call(t, alice, "list_shared_by_me", map[string]any{}); !strings.HasPrefix(out, "guides/install.md in Docs:\n  Bob Example") || strings.Contains(out, "link") {
		t.Errorf("list_shared_by_me %q", out)
	}

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"share_item", map[string]any{"space": "Docs", "path": "guides", "recipients": []string{"bob"}, "role": "Can manage"}, "has no role \"Can manage\"; its roles are: Can view, Can edit"},
		{"share_item", map[string]any{"space": "Docs", "path": "", "recipients": []string{"bob"}, "role": "Can view"}, "add_space_member gives access to the whole space"},
		{"share_item", map[string]any{"space": "Docs", "path": "guides", "recipients": []string{"nobody"}, "role": "Can view"}, `OpenCloud knows no user or group "nobody"`},
		{"share_item", map[string]any{"space": "Docs", "path": "guides", "recipients": []string{"bob"}, "role": "Can view", "expires": "soon"}, `"soon" is no date`},
		{"remove_permission", map[string]any{"space": "Docs", "path": "guides", "permission": link}, "not found"},
		{"list_shared_by_me", map[string]any{"only": "groups"}, "only is people or links"},
		{"accept_share", map[string]any{"share": "nothing"}, `nothing named "nothing" is shared with alice`},
	} {
		if out, isErr := call(t, alice, c.tool, c.args); !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %v: %q", c.tool, c.args, out)
		}
	}
}

func TestAProjectSpaceIsMadeSharedDisabledAndDeleted(t *testing.T) {
	alice, bob, _ := connectBoth(t)

	out, isErr := call(t, alice, "create_space", map[string]any{"name": "Marketing", "description": "Campaigns", "quota": 1 << 30})
	if isErr || !strings.HasPrefix(out, "Made the space Marketing (project, id storage$space-") || !strings.Contains(out, "description: Campaigns\n") ||
		!strings.Contains(out, "quota: 0 of 1073741824 bytes used, 1073741824 left\n") {
		t.Fatalf("create_space %q", out)
	}

	if out, isErr := call(t, alice, "write_file", map[string]any{"space": "Marketing", "path": ".space/readme.md", "content": "# Marketing"}); isErr {
		t.Fatal(out)
	}

	out, isErr = call(t, alice, "add_space_member", map[string]any{"space": "Marketing", "recipient": "bob", "role": "Can view"})
	if isErr || !strings.HasPrefix(out, "Shared Marketing with:\nBob Example (user, id user-bob): Can view, permission share-") {
		t.Fatalf("add_space_member %q", out)
	}

	member := regexp.MustCompile(`permission (share-\d+)`).FindStringSubmatch(out)[1]

	steps := []struct {
		user *mcp.ClientSession
		tool string
		args map[string]any
		want string
	}{
		{alice, "update_space", map[string]any{"space": "Marketing", "name": "Sales", "readme": ".space/readme.md"}, "readme: .space/readme.md\n"},
		{bob, "get_space", map[string]any{"space": "Sales"}, "Sales (project, id storage$space-"},
		{alice, "update_space_member", map[string]any{"space": "Sales", "permission": member, "role": "Can edit", "expires": "2027-01-31"},
			"Changed: Bob Example (user, id user-bob): Can edit, until 2027-01-31T00:00:00Z, permission " + member + ", of Sales."},
		{alice, "list_space_members", map[string]any{"space": "Sales"}, "Bob Example (user, id user-bob): Can edit, until 2027-01-31T00:00:00Z, permission " + member + "\n"},
		{alice, "remove_space_member", map[string]any{"space": "Sales", "permission": member}, "Removed " + member + " from Sales."},
		{alice, "disable_space", map[string]any{"space": "Sales"}, "The space Sales is disabled"},
		{alice, "list_spaces", map[string]any{}, "Sales (project, disabled, id storage$space-"},
		{alice, "restore_space", map[string]any{"space": "Sales"}, "The space Sales is enabled."},
		{alice, "restore_space", map[string]any{"space": "Sales"}, "The space Sales is enabled."},
		{alice, "disable_space", map[string]any{"space": "Sales"}, "The space Sales is disabled"},
		{alice, "delete_space", map[string]any{"space": "Sales"}, "The space Sales is deleted for good."},
	}

	for _, s := range steps {
		if out, isErr := call(t, s.user, s.tool, s.args); isErr || !strings.Contains(out, s.want) {
			t.Fatalf("%s %v: %q", s.tool, s.args, out)
		}
	}

	if out, _ := call(t, bob, "get_space", map[string]any{"space": "Sales"}); !strings.Contains(out, `there is no space named "Sales"`) {
		t.Errorf("the deleted space %q", out)
	}

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"delete_space", map[string]any{"space": "Docs"}, "disable_space comes first"},
		{"update_space", map[string]any{"space": "Docs"}, "nothing to change is given"},
		{"update_space", map[string]any{"space": "Docs", "image": "guides"}, "is a folder in the space Docs; the image is a file"},
		{"create_space", map[string]any{"name": "Big", "quota": -1}, "a quota is a number of bytes"},
	} {
		if out, isErr := call(t, alice, c.tool, c.args); !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %v: %q", c.tool, c.args, out)
		}
	}
}

func TestAFileIsTaggedAndUntagged(t *testing.T) {
	cs, _ := connect(t)

	for _, s := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_tags", map[string]any{}, "No tags are in use."},
		{"add_tags", map[string]any{"space": "Docs", "path": "restore.md", "tags": []string{"guide", "2026"}}, "restore.md in Docs has the tags 2026, guide."},
		{"add_tags", map[string]any{"space": "Docs", "path": "restore.md", "tags": []string{"guide"}}, "restore.md in Docs has the tags 2026, guide."},
		{"add_tags", map[string]any{"space": "Docs", "path": "guides", "tags": []string{"howto"}}, "guides in Docs has the tags howto."},
		{"list_tags", map[string]any{}, "2026, guide, howto"},
		{"remove_tags", map[string]any{"space": "Docs", "path": "restore.md", "tags": []string{"guide", "2026"}}, "restore.md in Docs has no tags."},
	} {
		if out, isErr := call(t, cs, s.tool, s.args); isErr || out != s.want {
			t.Fatalf("%s %v: %q", s.tool, s.args, out)
		}
	}

	if out, _ := call(t, cs, "get_item", map[string]any{"space": "Docs", "path": "guides"}); !strings.Contains(out, "tags: howto\n") {
		t.Errorf("get_item %q", out)
	}

	if out, isErr := call(t, cs, "add_tags", map[string]any{"space": "Docs", "path": "restore.md", "tags": []string{"a,b"}}); !isErr || !strings.Contains(out, "a tag holds no comma") {
		t.Errorf("a tag with a comma %q", out)
	}
}

func TestTheActivitiesNotificationsAndApps(t *testing.T) {
	alice, bob, _ := connectBoth(t)

	if out, isErr := call(t, alice, "write_file", map[string]any{"space": "Docs", "path": "guides/new.md", "content": "# New"}); isErr {
		t.Fatal(out)
	}

	if out, isErr := call(t, alice, "share_item", map[string]any{"space": "Docs", "path": "guides/new.md", "recipients": []string{"bob"}, "role": "Can view"}); isErr {
		t.Fatal(out)
	}

	out, isErr := call(t, alice, "list_activities", map[string]any{"space": "Docs", "path": "guides"})
	if isErr || !strings.HasSuffix(out, " Alice Example added new.md to Docs\n") {
		t.Errorf("list_activities %q", out)
	}

	out, isErr = call(t, bob, "list_notifications", map[string]any{})
	id := regexp.MustCompile(`\(id (notice-\d+)\)`).FindStringSubmatch(out)

	if isErr || id == nil || !strings.Contains(out, " Resource shared: Alice Example shared new.md with you (id ") {
		t.Fatalf("list_notifications %q", out)
	}

	steps := []struct {
		user *mcp.ClientSession
		tool string
		args map[string]any
		want string
	}{
		{bob, "dismiss_notifications", map[string]any{"ids": []string{id[1]}}, "Dismissed 1 notifications."},
		{bob, "list_notifications", map[string]any{}, "The user has no notifications."},
		{alice, "list_apps", map[string]any{}, "CollaboraOnline: Open office documents with Collabora; opens odt, docx; makes new odt\n"},
		{alice, "create_document", map[string]any{"space": "Docs", "path": "reports/q4.odt"}, "Made the document reports/q4.odt in Docs; open_in_app gives the link to edit it."},
		{alice, "open_in_app", map[string]any{"space": "Docs", "path": "reports/q4.odt"}, "reports/q4.odt in Docs opens at https://cloud.example.com/external?app=CollaboraOnline&fileId=storage%24docs%21"},
	}

	for _, s := range steps {
		if out, isErr := call(t, s.user, s.tool, s.args); isErr || !strings.HasPrefix(out, s.want) {
			t.Fatalf("%s %v: %q", s.tool, s.args, out)
		}
	}

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"create_document", map[string]any{"space": "Docs", "path": "reports/q4.odt"}, "the file already exists"},
		{"create_document", map[string]any{"space": "Docs", "path": "reports/q4.docx"}, `no app makes documents of the type "docx"; these it makes: odt`},
		{"open_in_app", map[string]any{"space": "Docs", "path": "reports"}, "an app opens a file"},
		{"list_activities", map[string]any{"space": "Docs", "limit": 500}, "a limit is 1 to 100"},
		{"dismiss_notifications", map[string]any{"ids": []string{" "}}, "no notification is given"},
	} {
		if out, isErr := call(t, alice, c.tool, c.args); !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %v: %q", c.tool, c.args, out)
		}
	}
}

func TestARefusedQueryIsAnsweredWithExamples(t *testing.T) {
	cs, _ := connect(t)

	out, isErr := call(t, cs, "search", map[string]any{"kql": `tag:("draft") AND (mediatype:pdf`})
	if !isErr || !strings.HasPrefix(out, `OpenCloud's search refuses (tag:("draft") AND (mediatype:pdf): invalid query syntax at 1:35.`) ||
		!strings.Contains(out, "\n`tag:(\"foo-bar\")` finds invoice.txt") || !strings.Contains(out, "`mediatype:text/markdown` finds notes.md") ||
		!strings.HasSuffix(out, "More in opencloud:///search/kql.") {
		t.Errorf("refused %q", out)
	}
}

// offered are the names of the tools a server of the options lists.
func offered(t *testing.T, opts Options) []string {
	t.Helper()

	st, ct := mcp.NewInMemoryTransports()

	ss, err := New(opts).Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}

	slices.Sort(out)

	return out
}

func TestTheTemplateCompletesSpacesAndPaths(t *testing.T) {
	cs, _ := connect(t)

	complete := func(name, value, space string) []string {
		t.Helper()

		params := &mcp.CompleteParams{
			Ref:      &mcp.CompleteReference{Type: "ref/resource", URI: "opencloud:///spaces/{space}/{+path}"},
			Argument: mcp.CompleteParamsArgument{Name: name, Value: value},
		}
		if space != "" {
			params.Context = &mcp.CompleteContext{Arguments: map[string]string{"space": space}}
		}

		res, err := cs.Complete(t.Context(), params)
		if err != nil {
			t.Fatalf("%s %q: %v", name, value, err)
		}

		return res.Completion.Values
	}

	for _, c := range []struct {
		name, value, space string
		want               []string
	}{
		{"space", "do", "", []string{"storage$docs"}},
		{"space", "STORAGE", "", []string{"storage$docs"}},
		{"space", "", "", []string{"personal-id", "storage$docs"}},
		{"path", "", "storage$docs", []string{"guides/", "releases/", "restore.md"}},
		{"path", "guides/", "storage$docs", []string{"guides/install.md", "guides/plain.txt"}},
		{"path", "guides/IN", "storage$docs", []string{"guides/install.md"}},
		{"path", "nowhere/x", "storage$docs", []string{}},
		{"path", "../", "storage$docs", []string{}},
		{"path", "r", "", []string{}},
		{"path", "r", "no-such-space", []string{}},
	} {
		got := complete(c.name, c.value, c.space)
		slices.Sort(got)

		if !slices.Equal(got, c.want) {
			t.Errorf("%s %q in %q: %v, want %v", c.name, c.value, c.space, got, c.want)
		}
	}
}

func TestEveryToolIsInOneToolset(t *testing.T) {
	var all []string

	for _, set := range Toolsets {
		names := offered(t, Options{Toolsets: []string{set}})
		if len(names) == 0 {
			t.Errorf("the toolset %s has no tools", set)
		}

		all = append(all, names...)
	}

	slices.Sort(all)

	if want := slices.Sorted(maps.Keys(kinds)); !slices.Equal(all, want) {
		t.Errorf("the toolsets hold %v, want each of %v once", all, want)
	}

	if got := offered(t, Options{Toolsets: []string{"tags", "people"}}); !slices.Equal(got, []string{"add_tags", "find_groups", "find_users", "get_me", "list_tags", "remove_tags"}) {
		t.Errorf("tags and people %v", got)
	}
}

func TestReadOnlyOffersOnlyWhatReads(t *testing.T) {
	var want []string

	for name, kind := range kinds {
		if kind == "reads" {
			want = append(want, name)
		}
	}

	slices.Sort(want)

	if got := offered(t, Options{ReadOnly: true}); !slices.Equal(got, want) {
		t.Errorf("read-only %v, want %v", got, want)
	}

	if got := offered(t, Options{ReadOnly: true, Toolsets: []string{"trash"}}); !slices.Equal(got, []string{"list_trash"}) {
		t.Errorf("read-only trash %v", got)
	}
}

func TestASearchThatFindsNothingSaysWhatItMayHaveMissed(t *testing.T) {
	cs, _ := connect(t)

	out, isErr := call(t, cs, "search", map[string]any{"space": "Docs", "kql": "colour:red"})
	if isErr || !strings.Contains(out, "Nothing in Docs matches (colour:red). OpenCloud's parity report proves no field colour") {
		t.Errorf("found %q", out)
	}
}

func TestTheErrorsAreSaid(t *testing.T) {
	cs, _ := connect(t)

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_folder", map[string]any{"space": "Nope"}, `there is no space named "Nope" that alice sees`},
		{"list_folder", map[string]any{"space": ""}, "no space is given"},
		{"read_file", map[string]any{"space": "Docs", "path": "../personal-id/mine.md"}, "leads outside the space"},
		{"write_file", map[string]any{"space": "Docs", "path": "../../x.md", "content": "x"}, "leads outside the space"},
		{"write_file", map[string]any{"space": "Docs", "path": "/", "content": "x"}, "not a file path"},
		{"read_file", map[string]any{"space": "Docs", "path": "nope.md"}, `"nope.md" is not in the space Docs`},
		{"list_folder", map[string]any{"space": "Docs", "path": "nowhere"}, `"nowhere" is not in the space Docs`},
		{"search", map[string]any{"space": "Docs", "query": `""`}, "the search names nothing to look for"},
		{"search", map[string]any{"folder": "guides"}, "a folder needs its space"},
		{"search", map[string]any{"space": "Docs", "type": "cat pictures"}, `"cat pictures" is no kind of file`},
		{"search", map[string]any{"space": "Docs", "query": "x", "modifiedAfter": "last week"}, `"last week" is no date`},
	} {
		if out, isErr := call(t, cs, c.tool, c.args); !isErr || !strings.Contains(out, c.want) {
			t.Errorf("%s %v: %q", c.tool, c.args, out)
		}
	}
}

func TestALongDocumentIsCutWithItsOutline(t *testing.T) {
	cs, _ := connect(t)
	long := "# Big\n\n" + strings.Repeat("word ", maxText/5) + "\n\n## Tail\n\nThe end."

	if out, isErr := call(t, cs, "write_file", map[string]any{"space": "Docs", "path": "big.md", "content": long}); isErr {
		t.Fatal(out)
	}

	out, isErr := call(t, cs, "read_file", map[string]any{"space": "Docs", "path": "big.md"})
	if want := "(Cut here: the first 100000 of " + strconv.Itoa(len(long)) + " characters.)\nIts outline: # Big; ## Tail"; isErr || !strings.HasSuffix(out, want) {
		t.Errorf("read ends %q", out[max(0, len(out)-120):])
	}

	if out, _ := call(t, cs, "read_section", map[string]any{"space": "Docs", "path": "big.md", "heading": "Tail"}); out != "## Tail\n\nThe end." {
		t.Errorf("section %q", out)
	}
}

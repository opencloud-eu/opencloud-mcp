package opencloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud/opencloudtest"
)

const (
	token   = "app-token-4711"
	driveID = "1284d238-aa92-42ce-bdc4-0b0000009157$a6f3-11ef-c42"
)

// cloud is a fake OpenCloud of the user alice with her personal space and the project space Docs.
func cloud(t *testing.T) (*opencloudtest.Cloud, string) {
	t.Helper()

	c := opencloudtest.New("alice", token)
	if err := c.Space("personal", "Alice", "personal", map[string]string{"/mine.md": "Alice's own note."}); err != nil {
		t.Fatal(err)
	}

	if err := c.Space(driveID, "Docs", "project", map[string]string{
		"/restore.md":        "# Restore\n\nRun lark restore --snapshot ID.",
		"/guides/install.md": "Install with apt install larkspur-agent.",
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)

	return c, srv.URL
}

func client(t *testing.T, url, tok string) *Client {
	t.Helper()

	cloud, err := New(Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	c, err := cloud.AppToken("alice", Secret(tok))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestASpaceIsFoundByNameOrID(t *testing.T) {
	_, url := cloud(t)
	c := client(t, url, token)

	for _, ref := range []string{"Docs", "docs", driveID} {
		if sp, err := c.Space(t.Context(), ref); err != nil || sp.ID != driveID || sp.Type != "project" {
			t.Errorf("%s: %+v %v", ref, sp, err)
		}
	}

	if _, err := c.Space(t.Context(), "Nope"); !errors.Is(err, ErrSpaceNotFound) || !strings.Contains(err.Error(), `no space named "Nope"`) {
		t.Errorf("a missing space: %v", err)
	}
}

func TestTwoSpacesOfOneNameAreToldApartByID(t *testing.T) {
	fake := opencloudtest.New("alice", token)
	for _, id := range []string{"a$1", "b$2"} {
		if err := fake.Space(id, "Docs", "project", nil); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	c := client(t, srv.URL, token)
	if _, err := c.Space(t.Context(), "Docs"); !errors.Is(err, ErrSpaceNotFound) || !strings.Contains(err.Error(), "a$1, b$2") {
		t.Errorf("an ambiguous name: %v", err)
	}

	if sp, err := c.Space(t.Context(), "b$2"); err != nil || sp.ID != "b$2" {
		t.Errorf("by id: %+v %v", sp, err)
	}
}

func TestARefusedTokenIsSaid(t *testing.T) {
	_, url := cloud(t)
	c := client(t, url, "wrong")

	if _, err := c.Spaces(t.Context()); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "refused the app token of alice") {
		t.Errorf("spaces: %v", err)
	}

	sp := Space{ID: driveID, Name: "Docs"}
	if _, err := c.List(t.Context(), sp, ""); !errors.Is(err, ErrRefused) {
		t.Errorf("list: %v", err)
	}

	if _, err := c.Read(t.Context(), sp, "restore.md"); !errors.Is(err, ErrRefused) {
		t.Errorf("read: %v", err)
	}

	if _, err := c.Search(t.Context(), &sp, `name:"*restore*"`, 5); !errors.Is(err, ErrRefused) {
		t.Errorf("search: %v", err)
	}
}

func TestAPathOutsideTheSpaceIsRefused(t *testing.T) {
	for p, want := range map[string]string{"": "", "/": "", "guides/": "guides", "/a/./b/../c.md": "a/c.md", "a/../b": "b"} {
		if got, err := Clean(p); err != nil || got != want {
			t.Errorf("%q: %q %v", p, got, err)
		}
	}

	for _, p := range []string{"..", "../secret.md", "/../../etc/passwd", "a/../../b", "a\x00b"} {
		if got, err := Clean(p); !errors.Is(err, ErrOutsideSpace) {
			t.Errorf("%q: %q %v", p, got, err)
		}
	}

	_, url := cloud(t)
	c := client(t, url, token)
	sp := Space{ID: driveID, Name: "Docs"}

	if _, err := c.Read(t.Context(), sp, "../personal/mine.md"); !errors.Is(err, ErrOutsideSpace) {
		t.Errorf("read: %v", err)
	}

	if err := c.Write(t.Context(), sp, "../x.md", []byte("x")); !errors.Is(err, ErrOutsideSpace) {
		t.Errorf("write: %v", err)
	}

	if _, err := c.Read(t.Context(), sp, "nope.md"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), `"nope.md" is not in the space Docs`) {
		t.Errorf("a missing file: %v", err)
	}
}

func TestAFileIsWrittenListedReadAndFound(t *testing.T) {
	fake, url := cloud(t)
	c := client(t, url, token)
	sp := Space{ID: driveID, Name: "Docs"}

	if err := c.Write(t.Context(), sp, "notes/2026/case-17.md", []byte("Restore failed at step two.")); err != nil {
		t.Fatal(err)
	}

	entries, err := c.List(t.Context(), sp, "notes/2026")
	if err != nil || len(entries) != 1 || entries[0].Path != "notes/2026/case-17.md" || entries[0].Size != 27 {
		t.Fatalf("list %+v %v", entries, err)
	}

	if _, err := c.List(t.Context(), sp, "notes/2026/case-17.md"); !errors.Is(err, ErrNotAFolder) || !strings.Contains(err.Error(), "is a file in the space Docs") {
		t.Errorf("a file listed %v", err)
	}

	if _, err := c.List(t.Context(), sp, "notes/nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing folder listed %v", err)
	}

	if b, err := c.Read(t.Context(), sp, "/notes/2026/case-17.md"); err != nil || string(b) != "Restore failed at step two." {
		t.Errorf("read %q %v", b, err)
	}

	hits, err := c.Search(t.Context(), &sp, `(name:"*restore*" OR content:"restore")`, 10)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, h := range hits {
		got[h.Path] = true
	}

	if len(hits) != 2 || !got["restore.md"] || !got["notes/2026/case-17.md"] || hits[0].Space != driveID || hits[0].SpaceName != "Docs" {
		t.Errorf("hits %+v", hits)
	}

	if s := fake.Searches(); len(s) != 1 || s[0] != `(name:"*restore*" OR content:"restore")` {
		t.Errorf("patterns %q", s)
	}
}

func TestASearchWithoutASpaceLooksInAllOfThem(t *testing.T) {
	_, url := cloud(t)
	c := client(t, url, token)

	hits, err := c.Search(t.Context(), nil, `(name:"*o*" OR content:"own note")`, 10)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, h := range hits {
		got[h.SpaceName+":"+h.Path] = h.Space
	}

	if len(got) != 2 || got["Alice:mine.md"] != "personal" || got["Docs:restore.md"] != driveID {
		t.Errorf("hits %+v", hits)
	}
}

func TestARefusedQueryIsSaidWithOpenCloudsReason(t *testing.T) {
	_, url := cloud(t)
	c := client(t, url, token)
	sp := Space{ID: driveID, Name: "Docs"}

	_, err := c.Search(t.Context(), &sp, `(mediatype:document`, 10)
	if !errors.Is(err, ErrBadQuery) || err.Error() != "OpenCloud refused the query (mediatype:document: invalid query syntax at 1:20" {
		t.Errorf("refused: %v", err)
	}

	if _, err := c.Search(t.Context(), nil, " ", 10); !errors.Is(err, ErrBadQuery) {
		t.Errorf("empty: %v", err)
	}
}

// The fake answers a second unfollow with 500 as OpenCloud 9.0.0 does, so removing twice proves the
// favorite is looked up first.
func TestAFavoriteIsMarkedFoundAndUnmarkedTwiceOver(t *testing.T) {
	fake, url := cloud(t)
	c := client(t, url, token)
	sp := Space{ID: driveID, Name: "Docs"}

	for range 2 {
		if err := c.Favorite(t.Context(), sp, "guides/install.md", true); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := c.Search(t.Context(), nil, "is:favorite", 10)
	if err != nil || len(hits) != 1 || hits[0].Path != "guides/install.md" || !hits[0].Favorite || !fake.Favorite(driveID, "guides/install.md") {
		t.Errorf("the favorites %+v %v", hits, err)
	}

	for range 2 {
		if err := c.Favorite(t.Context(), sp, "/guides/install.md", false); err != nil {
			t.Fatal(err)
		}
	}

	if fake.Favorite(driveID, "guides/install.md") {
		t.Error("still a favorite")
	}

	// a colon in a name would end the path of LibreGraph's lookup unescaped
	if err := fake.Put(driveID, "/notes/a b:c.md", "x"); err != nil {
		t.Fatal(err)
	}

	if err := c.Favorite(t.Context(), sp, "notes/a b:c.md", true); err != nil || !fake.Favorite(driveID, "notes/a b:c.md") {
		t.Errorf("a name with a colon: %v", err)
	}

	if err := c.Favorite(t.Context(), sp, "guides/missing.md", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file: %v", err)
	}

	if err := c.Favorite(t.Context(), sp, "../x", true); !errors.Is(err, ErrOutsideSpace) {
		t.Errorf("outside: %v", err)
	}
}

func TestASearchResultOfAnotherSpaceIsDropped(t *testing.T) {
	for href, want := range map[string]string{
		"/dav/spaces/" + driveID + "/guides/install.md":                    "guides/install.md",
		"/remote.php/dav/spaces/" + driveID + "/a%20b.md":                  "a b.md",
		"/dav/spaces/" + strings.ReplaceAll(driveID, "$", "%24") + "/x.md": "x.md",
		"/dav/spaces/" + driveID + "!node/y.md":                            "y.md",
		"/dav/spaces/other$space/z.md":                                     "",
		"/dav/spaces/" + driveID:                                           "",
		"/dav/spaces/" + driveID + "/../other/z.md":                        "",
	} {
		got, ok := inSpace(href, driveID)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v", href, got, ok)
		}
	}
}

func TestNoRequestLeavesTheConfiguredOpenCloud(t *testing.T) {
	var elsewhere atomic.Int64

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)

	c := client(t, redirecting.URL, token)
	sp := Space{ID: driveID, Name: "Docs"}

	if _, err := c.Spaces(t.Context()); !errors.Is(err, ErrForeignOrigin) {
		t.Errorf("spaces: %v", err)
	}

	if _, err := c.Read(t.Context(), sp, "a.md"); !errors.Is(err, ErrForeignOrigin) {
		t.Errorf("read: %v", err)
	}

	if _, err := c.Search(t.Context(), &sp, "a", 1); !errors.Is(err, ErrForeignOrigin) {
		t.Errorf("search: %v", err)
	}

	if n := elsewhere.Load(); n != 0 {
		t.Errorf("%d requests reached another host", n)
	}
}

func TestTheTokenNeverPrints(t *testing.T) {
	c := client(t, "https://cloud.example.com", token)
	secret := Secret(token)

	b, err := json.Marshal(struct{ Token Secret }{secret})
	if err != nil {
		t.Fatal(err)
	}

	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("client", "client", c, "token", secret)

	for _, s := range []string{fmt.Sprintf("%+v %#v %s %v", c, c, secret, secret), fmt.Sprintf("%#v", secret), string(b), logged.String()} {
		if strings.Contains(s, token) {
			t.Errorf("the token printed: %s", s)
		}
	}
}

func TestTheETagsMoveWithAWriteUpToTheRootOfItsSpaceOnly(t *testing.T) {
	_, url := cloud(t)
	c := client(t, url, token)
	docs := Space{ID: driveID, Name: "Docs"}

	roots := func() map[string]string {
		t.Helper()

		spaces, err := c.Spaces(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		out := map[string]string{}
		for _, s := range spaces {
			out[s.ID] = s.ETag
		}

		return out
	}

	folders := func() map[string]string {
		t.Helper()

		entries, err := c.List(t.Context(), docs, "")
		if err != nil {
			t.Fatal(err)
		}

		out := map[string]string{}
		for _, e := range entries {
			out[e.Path] = e.ETag
		}

		return out
	}

	before, top := roots(), folders()
	if before[driveID] == "" || top["guides"] == "" || top["restore.md"] == "" {
		t.Fatalf("no etags: %v %v", before, top)
	}

	if err := c.Write(t.Context(), docs, "guides/new.md", []byte("new")); err != nil {
		t.Fatal(err)
	}

	after, moved := roots(), folders()
	if after[driveID] == before[driveID] || after["personal"] != before["personal"] {
		t.Errorf("root etags before %v, after the write %v", before, after)
	}

	if moved["guides"] == top["guides"] || moved["restore.md"] != top["restore.md"] {
		t.Errorf("etags before %v, after the write %v", top, moved)
	}
}

func TestTheHeadOfAFileIsRead(t *testing.T) {
	_, url := cloud(t)
	c := client(t, url, token)
	docs := Space{ID: driveID, Name: "Docs"}

	if b, err := c.Head(t.Context(), docs, "restore.md", 9); err != nil || string(b) != "# Restore" {
		t.Errorf("head %q %v", b, err)
	}

	if b, err := c.Head(t.Context(), docs, "restore.md", 1<<10); err != nil || !strings.HasSuffix(string(b), "--snapshot ID.") {
		t.Errorf("a head past the end %q %v", b, err)
	}
}

func TestABearerTokenSignsIn(t *testing.T) {
	fake, url := cloud(t)
	fake.TrustBearer(func(tok string) (string, bool) { return "alice", tok == "for-opencloud" })

	cl, err := New(Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	if spaces, err := cl.Bearer("alice", "for-opencloud").Spaces(t.Context()); err != nil || len(spaces) != 2 {
		t.Errorf("spaces %+v %v", spaces, err)
	}

	if _, err := cl.Bearer("alice", "for-someone-else").Spaces(t.Context()); !errors.Is(err, ErrRefused) ||
		!strings.Contains(err.Error(), "OpenCloud refused the sign-in of alice") {
		t.Errorf("a token OpenCloud does not take: %v", err)
	}
}

func TestTheFileOperations(t *testing.T) {
	fake, url := cloud(t)
	c := client(t, url, token)
	docs, mine := Space{ID: driveID, Name: "Docs"}, Space{ID: "personal", Name: "Alice"}

	for range 2 {
		if err := c.MakeFolder(t.Context(), docs, "a/b"); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.MakeFolder(t.Context(), docs, "restore.md"); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("a folder over a file: %v", err)
	}

	if err := c.Copy(t.Context(), docs, "restore.md", docs, "c/d/copy.md", false); err != nil {
		t.Fatal(err)
	}

	if err := c.Copy(t.Context(), docs, "restore.md", docs, "c/d/copy.md", false); !errors.Is(err, ErrExists) || !strings.Contains(err.Error(), "overwrite replaces it") {
		t.Errorf("a copy onto a file: %v", err)
	}

	if err := c.Copy(t.Context(), docs, "guides", mine, "imported/guides", false); err != nil {
		t.Fatal(err)
	}

	if b, err := c.Read(t.Context(), mine, "imported/guides/install.md"); err != nil || !strings.HasPrefix(string(b), "Install with apt") {
		t.Errorf("the copy in the other space %q %v", b, err)
	}

	if err := c.Move(t.Context(), docs, "c/d/copy.md", "moved/renamed.md", false); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Read(t.Context(), docs, "c/d/copy.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the moved file is still there: %v", err)
	}

	if err := c.Move(t.Context(), docs, "nope.md", "x.md", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("moving a missing file: %v", err)
	}

	if err := c.Move(t.Context(), docs, "restore.md", "../x.md", false); !errors.Is(err, ErrOutsideSpace) {
		t.Errorf("moving outside: %v", err)
	}

	it, err := c.Item(t.Context(), docs, "moved/renamed.md")
	if err != nil || it.Path != "moved/renamed.md" || it.Space != driveID || it.SpaceName != "Docs" || it.MIMEType != "text/markdown" ||
		it.Size != 42 || it.Link == "" || !slices.Contains(it.Permissions, "standard/delete") {
		t.Errorf("the item %+v %v", it, err)
	}

	if byID, err := c.ItemByID(t.Context(), it.ID); err != nil || byID.Path != it.Path || byID.SpaceName != "Docs" {
		t.Errorf("by id %+v %v", byID, err)
	}

	if _, err := c.ItemByID(t.Context(), "no id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a broken id: %v", err)
	}

	if top, err := c.Item(t.Context(), docs, ""); err != nil || !top.Folder || top.Space != driveID || top.Path != "" {
		t.Errorf("the top %+v %v", top, err)
	}

	var pic bytes.Buffer
	if err := png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 400, 200))); err != nil {
		t.Fatal(err)
	}

	if err := fake.Put(driveID, "/pic.png", pic.String()); err != nil {
		t.Fatal(err)
	}

	b, kind, err := c.Preview(t.Context(), docs, "pic.png", 128)
	if err != nil || kind != "image/png" {
		t.Fatalf("the preview %s %v", kind, err)
	}

	if cfg, err := png.DecodeConfig(bytes.NewReader(b)); err != nil || cfg.Width != 128 || cfg.Height != 64 {
		t.Errorf("the preview is %dx%d %v", cfg.Width, cfg.Height, err)
	}

	if _, _, err := c.Preview(t.Context(), docs, "restore.md", 128); !errors.Is(err, ErrNoPreview) || !strings.Contains(err.Error(), "text/markdown") {
		t.Errorf("a markdown preview: %v", err)
	}

	if _, _, err := c.Preview(t.Context(), docs, "nope.png", 128); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing preview: %v", err)
	}

	if err := c.Delete(t.Context(), docs, "moved"); err != nil {
		t.Fatal(err)
	}

	if err := c.Delete(t.Context(), docs, "moved"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}

	if err := c.Delete(t.Context(), docs, "/"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting the top: %v", err)
	}
}

package opencloud

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencloud-eu/opencloud-mcp/internal/kql"
)

// live is a client of a local OpenCloud, as OPENCLOUD_URL (the demo's http://cloud.localhost when unset),
// OPENCLOUD_USER and OPENCLOUD_APP_TOKEN name it, and the user; the test skips when there is none.
func live(t *testing.T) (*Client, *Cloud, string) {
	t.Helper()

	addr := os.Getenv("OPENCLOUD_URL")
	if addr == "" {
		addr = "http://cloud.localhost"
	}

	// the tests write and delete; anything but a local instance needs OPENCLOUD_LIVE_ANY_HOST, as
	// the OpenCloud service of CI does
	anyHost, _ := strconv.ParseBool(os.Getenv("OPENCLOUD_LIVE_ANY_HOST"))

	u, err := url.Parse(addr)
	if err != nil || !anyHost && !local(u.Hostname()) {
		t.Skipf("needs: opencloud on this machine, not %q", addr)
	}

	insecure, _ := strconv.ParseBool(os.Getenv("OPENCLOUD_INSECURE_TLS"))
	probe := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // a local instance's own certificate
	}}

	resp, err := probe.Get(strings.TrimRight(addr, "/") + "/status.php")
	if err != nil {
		t.Skipf("needs: opencloud at %s (%v)", addr, err)
	}

	_ = resp.Body.Close() // only whether it answers matters

	user, token := os.Getenv("OPENCLOUD_USER"), os.Getenv("OPENCLOUD_APP_TOKEN")
	if user == "" || token == "" {
		t.Skipf("needs: opencloud at %s with OPENCLOUD_USER and OPENCLOUD_APP_TOKEN set", addr)
	}

	cloud, err := New(Config{URL: addr, InsecureTLS: insecure})
	if err != nil {
		t.Fatal(err)
	}

	c, err := cloud.AppToken(user, Secret(token))
	if err != nil {
		t.Fatal(err)
	}

	return c, cloud, user
}

// local reports whether host is this machine or a local development name.
func local(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}

	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test")
}

func TestLiveAFileIsWrittenReadAndSearchedInThePersonalSpace(t *testing.T) {
	c, cloud, user := live(t)

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var personal Space

	for _, s := range spaces {
		if s.Type == "personal" {
			personal = s
		}
	}

	if personal.ID == "" {
		t.Fatalf("no personal space among %+v", spaces)
	}

	if sp, err := c.Space(t.Context(), personal.ID); err != nil || sp.Name != personal.Name {
		t.Fatalf("by id %+v %v", sp, err)
	}

	dir := fmt.Sprintf("opencloud-mcp-live-%d", time.Now().UnixNano())
	p := dir + "/notes/check.md"
	text := "# Check\n\nWritten by the live test of opencloud-mcp."

	t.Cleanup(func() {
		if err := c.dav(personal).RemoveAll("/" + dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})

	if err := c.Write(t.Context(), personal, p, []byte(text)); err != nil {
		t.Fatal(err)
	}

	if b, err := c.Read(t.Context(), personal, p); err != nil || string(b) != text {
		t.Errorf("read %q %v", b, err)
	}

	if entries, err := c.List(t.Context(), personal, dir+"/notes"); err != nil || len(entries) != 1 || entries[0].Path != p {
		t.Errorf("list %+v %v", entries, err)
	}

	if _, err := c.List(t.Context(), personal, p); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("a file listed %v", err)
	}

	if _, err := c.List(t.Context(), personal, dir+"/nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing folder listed %v", err)
	}

	if top, err := c.List(t.Context(), personal, ""); err != nil || !slices.ContainsFunc(top, func(e Entry) bool { return e.Path == dir && e.Folder && e.ETag != "" }) {
		t.Errorf("the top %+v %v", top, err)
	}

	if _, err := c.Read(t.Context(), personal, dir+"/nope.md"); err == nil {
		t.Error("a missing file was read")
	}

	// the index is filled in the background, so only the search's answer is checked, not its hits
	if _, err := c.Search(t.Context(), &personal, `name:"*opencloud-mcp*"`, 5); err != nil {
		t.Errorf("search: %v", err)
	}

	wrong, err := cloud.AppToken(user, "not-the-token")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := wrong.Spaces(t.Context()); !errors.Is(err, ErrRefused) {
		t.Errorf("a wrong token: %v", err)
	}
}

// The project space of the compose seed lists from its top down, as a personal space does.
func TestLiveAProjectSpaceIsListed(t *testing.T) {
	c, _, _ := live(t)

	sp, err := c.Space(t.Context(), "Handbook")
	if errors.Is(err, ErrSpaceNotFound) {
		t.Skip("needs: the project space Handbook of the compose seed")
	} else if err != nil {
		t.Fatal(err)
	}

	if sp.Type != "project" {
		t.Errorf("Handbook is a %s space", sp.Type)
	}

	top, err := c.List(t.Context(), sp, "")
	if err != nil || !slices.ContainsFunc(top, func(e Entry) bool { return e.Path == "guides" && e.Folder && e.ETag != "" }) {
		t.Errorf("the top %+v %v", top, err)
	}

	guides, err := c.List(t.Context(), sp, "guides")
	if err != nil || !slices.ContainsFunc(guides, func(e Entry) bool { return e.Path == "guides/onboarding.md" && e.Size > 0 }) {
		t.Errorf("guides %+v %v", guides, err)
	}
}

// The filters of a search find over WebDAV what OpenCloud's parity report says they find.
func TestLiveTheFiltersFindWhatTheyName(t *testing.T) {
	c, _, _ := live(t)

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	i := slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })
	if i < 0 {
		t.Fatalf("no personal space among %+v", spaces)
	}

	personal := spaces[i]
	dir := fmt.Sprintf("opencloud-mcp-kql-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		if err := c.dav(personal).RemoveAll("/" + dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})

	for p, text := range map[string]string{
		dir + "/plan.md":         "# Plan",
		dir + "/budget.txt":      strings.Repeat("budget ", 100),
		dir + "/archive/old.txt": "old",
	} {
		if err := c.Write(t.Context(), personal, p, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}

	in := func(f kql.Filter) []string {
		t.Helper()

		f.Folder = dir

		q, err := f.KQL()
		if err != nil {
			t.Fatal(err)
		}

		hits, err := c.Search(t.Context(), &personal, q, 50)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}

		// path: finds the folder itself too, as PATH-01 of the parity report says
		var got []string
		for _, h := range hits {
			got = append(got, strings.TrimPrefix(strings.TrimPrefix(h.Path, dir), "/"))
		}

		slices.Sort(got)

		return got
	}

	// the index is filled in the background
	deadline := time.Now().Add(30 * time.Second)
	for len(in(kql.Filter{Type: "file"})) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}

	tag := fmt.Sprintf("kql-live-%d", time.Now().UnixNano())
	budget := in(kql.Filter{Words: "budget"})

	if len(budget) != 1 {
		t.Fatalf("budget.txt is not found: %v", budget)
	}

	hits, err := c.Search(t.Context(), &personal, fmt.Sprintf(`path:"./%s/budget.txt"`, dir), 1)
	if err != nil || len(hits) != 1 || hits[0].ID == "" {
		t.Fatalf("budget.txt's id %+v %v", hits, err)
	}

	tagged, err := http.NewRequestWithContext(t.Context(), http.MethodPut, c.base+"/graph/v1.0/extensions/org.libregraph/tags",
		strings.NewReader(fmt.Sprintf(`{"resourceId":%q,"tags":[%q]}`, hits[0].ID, tag)))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.http.Do(tagged)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("tagging %v %v", resp, err)
	}

	_ = resp.Body.Close() // only the status matters

	for time.Now().Before(deadline) && len(in(kql.Filter{Tags: []string{tag}})) == 0 {
		time.Sleep(time.Second)
	}

	if it, err := c.item(t.Context(), personal, dir+"/budget.txt", ""); err != nil || it.GetId() != hits[0].ID {
		t.Errorf("budget.txt's id %q %v, the search's %q", it.GetId(), err, hits[0].ID)
	}

	for range 2 {
		if err := c.Favorite(t.Context(), personal, dir+"/plan.md", true); err != nil {
			t.Fatal(err)
		}
	}

	for time.Now().Before(deadline) && len(in(kql.Filter{Favorites: true})) == 0 {
		time.Sleep(time.Second)
	}

	if got := in(kql.Filter{Favorites: true}); !slices.Equal(got, []string{"plan.md"}) {
		t.Errorf("the favorites %q", got)
	}

	for range 2 {
		if err := c.Favorite(t.Context(), personal, dir+"/plan.md", false); err != nil {
			t.Fatal(err)
		}
	}

	for time.Now().Before(deadline) && len(in(kql.Filter{Favorites: true})) > 0 {
		time.Sleep(time.Second)
	}

	if got := in(kql.Filter{Favorites: true}); len(got) > 0 {
		t.Errorf("the favorites after removing %q", got)
	}

	if err := c.Favorite(t.Context(), personal, dir+"/missing.md", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file's favorite: %v", err)
	}

	for _, c := range []struct {
		filter kql.Filter
		want   []string
	}{
		{kql.Filter{Type: "file"}, []string{"archive/old.txt", "budget.txt", "plan.md"}},
		{kql.Filter{Type: "folder"}, []string{"", "archive"}},
		{kql.Filter{Type: "text/markdown"}, []string{"plan.md"}},
		{kql.Filter{Type: "document"}, []string{"archive/old.txt", "budget.txt", "plan.md"}},
		{kql.Filter{Words: "PLAN"}, []string{"plan.md"}},
		{kql.Filter{Tags: []string{tag}}, []string{"budget.txt"}},
		// a folder's size is that of all in it
		{kql.Filter{Larger: 100}, []string{"", "budget.txt"}},
		{kql.Filter{Type: "file", Smaller: 100}, []string{"archive/old.txt", "plan.md"}},
		{kql.Filter{After: time.Now().Add(-time.Hour), Type: "file"}, []string{"archive/old.txt", "budget.txt", "plan.md"}},
		{kql.Filter{Before: time.Now().Add(-time.Hour)}, nil},
		{kql.Filter{Query: `name:"*old*" OR name:"*plan*"`}, []string{"archive/old.txt", "plan.md"}},
		{kql.Filter{Query: "NOT mediatype:file"}, []string{"", "archive"}},
	} {
		if got := in(c.filter); !slices.Equal(got, c.want) {
			t.Errorf("%+v: %q, want %q", c.filter, got, c.want)
		}
	}

	if _, err := c.Search(t.Context(), &personal, "(mediatype:document", 1); !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), "invalid query syntax") {
		t.Errorf("a broken query: %v", err)
	}

	everywhere, err := c.Search(t.Context(), nil, fmt.Sprintf(`tag:("%s")`, tag), 5)
	if err != nil || len(everywhere) != 1 || everywhere[0].Space != personal.ID || !slices.Contains(everywhere[0].Tags, tag) {
		t.Errorf("in every space %+v %v", everywhere, err)
	}
}

// A file is made, copied into the project space, moved, previewed, described and deleted.
func TestLiveTheFileOperations(t *testing.T) {
	c, _, _ := live(t)

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	i := slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })
	if i < 0 {
		t.Fatalf("no personal space among %+v", spaces)
	}

	personal := spaces[i]
	dir := fmt.Sprintf("opencloud-mcp-ops-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		if err := c.dav(personal).RemoveAll("/" + dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})

	for range 2 {
		if err := c.MakeFolder(t.Context(), personal, dir+"/a/b"); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.Write(t.Context(), personal, dir+"/a/note.md", []byte("# Note")); err != nil {
		t.Fatal(err)
	}

	if err := c.MakeFolder(t.Context(), personal, dir+"/a/note.md"); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("a folder over a file: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	var pic bytes.Buffer

	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}

	if err := c.Write(t.Context(), personal, dir+"/pic.png", pic.Bytes()); err != nil {
		t.Fatal(err)
	}

	if err := c.Copy(t.Context(), personal, dir+"/a/note.md", personal, dir+"/c/d/copy.md", false); err != nil {
		t.Fatal(err)
	}

	if err := c.Copy(t.Context(), personal, dir+"/a/note.md", personal, dir+"/c/d/copy.md", false); !errors.Is(err, ErrExists) {
		t.Errorf("a copy onto a file: %v", err)
	}

	if err := c.Copy(t.Context(), personal, dir+"/a/note.md", personal, dir+"/c/d/copy.md", true); err != nil {
		t.Errorf("a copy that overwrites: %v", err)
	}

	if err := c.Move(t.Context(), personal, dir+"/c/d/copy.md", dir+"/moved/renamed.md", false); err != nil {
		t.Fatal(err)
	}

	if err := c.Move(t.Context(), personal, dir+"/a", dir+"/a/b/inside", false); err == nil || !strings.Contains(err.Error(), "into one of its children") {
		t.Errorf("a folder into itself: %v", err)
	}

	it, err := c.Item(t.Context(), personal, dir+"/moved/renamed.md")
	if err != nil || it.Path != dir+"/moved/renamed.md" || it.Space != personal.ID || it.SpaceName != personal.Name ||
		it.MIMEType != "text/markdown" || it.Size != 6 || it.Link == "" || !slices.Contains(it.Permissions, "standard/delete") {
		t.Errorf("the item %+v %v", it, err)
	}

	if byID, err := c.ItemByID(t.Context(), it.ID); err != nil || byID.Path != it.Path || byID.SpaceName != personal.Name {
		t.Errorf("by id %+v %v", byID, err)
	}

	if top, err := c.Item(t.Context(), personal, ""); err != nil || !top.Folder || top.Space != personal.ID || top.Path != "" {
		t.Errorf("the top %+v %v", top, err)
	}

	b, kind, err := c.Preview(t.Context(), personal, dir+"/pic.png", 128)
	if err != nil || kind != "image/png" {
		t.Fatalf("the preview %s %v", kind, err)
	}

	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err != nil || cfg.Width != 128 || cfg.Height != 64 {
		t.Errorf("the preview is %dx%d %v, want 128x64", cfg.Width, cfg.Height, err)
	}

	if _, _, err := c.Preview(t.Context(), personal, dir+"/a/note.md", 128); !errors.Is(err, ErrNoPreview) {
		t.Errorf("a markdown preview: %v", err)
	}

	// alice manages the seed's Handbook, bob only views it
	if handbook, err := c.Space(t.Context(), "Handbook"); err == nil {
		top, err := c.Item(t.Context(), handbook, "")
		if err != nil {
			t.Fatal(err)
		}

		target := dir + "-copied.md"
		err = c.Copy(t.Context(), personal, dir+"/a/note.md", handbook, target, false)

		switch {
		case !slices.Contains(top.Permissions, "children/create"):
			if !errors.Is(err, ErrDenied) {
				t.Errorf("a copy into a space the user only views: %v", err)
			}
		case err != nil:
			t.Errorf("a copy into the project space: %v", err)
		default:
			if err := c.Delete(t.Context(), handbook, target); err != nil {
				t.Errorf("deleting the copy: %v", err)
			}
		}

	}

	if err := c.Delete(t.Context(), personal, dir+"/moved"); err != nil {
		t.Fatal(err)
	}

	if err := c.Delete(t.Context(), personal, dir+"/moved"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestLiveTheTrashAndTheVersions(t *testing.T) {
	c, _, _ := live(t)

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	i := slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })
	if i < 0 {
		t.Fatalf("no personal space among %+v", spaces)
	}

	personal := spaces[i]
	dir := fmt.Sprintf("opencloud-mcp-trash-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		if err := c.dav(personal).RemoveAll("/" + dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}

		// t.Context is done by the time a cleanup runs
		trash, err := c.Trash(context.Background(), personal)
		if err != nil {
			t.Errorf("the trash: %v", err)
		}

		for _, d := range trash {
			if strings.HasPrefix(d.Path, dir) {
				if err := c.Purge(context.Background(), personal, d.Key); err != nil {
					t.Errorf("purging %s: %v", d.Path, err)
				}
			}
		}
	})

	for _, content := range []string{"one", "two two", "three"} {
		if err := c.Write(t.Context(), personal, dir+"/v.md", []byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	versions, err := c.Versions(t.Context(), personal, dir+"/v.md")
	if err != nil || len(versions) != 2 || versions[0].Size != 7 || versions[1].Size != 3 || versions[1].Written.IsZero() {
		t.Fatalf("the versions %+v %v", versions, err)
	}

	if b, err := c.ReadVersion(t.Context(), personal, dir+"/v.md", versions[1].Key); err != nil || string(b) != "one" {
		t.Errorf("the first version %q %v", b, err)
	}

	if _, err := c.ReadVersion(t.Context(), personal, dir+"/v.md", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a version that is not there: %v", err)
	}

	if err := c.RestoreVersion(t.Context(), personal, dir+"/v.md", versions[1].Key); err != nil {
		t.Fatal(err)
	}

	if b, err := c.Read(t.Context(), personal, dir+"/v.md"); err != nil || string(b) != "one" {
		t.Errorf("after the restore %q %v", b, err)
	}

	if _, err := c.Versions(t.Context(), personal, dir); !errors.Is(err, ErrNotAFile) {
		t.Errorf("the versions of a folder: %v", err)
	}

	if err := c.Write(t.Context(), personal, dir+"/a/gone.md", []byte("bye")); err != nil {
		t.Fatal(err)
	}

	if err := c.Delete(t.Context(), personal, dir+"/a"); err != nil {
		t.Fatal(err)
	}

	trash, err := c.Trash(t.Context(), personal)
	if err != nil {
		t.Fatal(err)
	}

	j := slices.IndexFunc(trash, func(d Deleted) bool { return d.Path == dir+"/a" })
	if j < 0 || !trash[j].Folder || trash[j].Deleted.IsZero() || trash[j].SpaceName != personal.Name {
		t.Fatalf("%s/a is not in the trash %+v", dir, trash)
	}

	if _, err := c.Restore(t.Context(), personal, "00000000-0000-0000-0000-000000000000", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("a key that is not in the trash: %v", err)
	}

	if to, err := c.Restore(t.Context(), personal, trash[j].Key, dir+"/b/c"); err != nil || to != dir+"/b/c" {
		t.Fatalf("the restore to %s %v", to, err)
	}

	if b, err := c.Read(t.Context(), personal, dir+"/b/c/gone.md"); err != nil || string(b) != "bye" {
		t.Errorf("the restored file %q %v", b, err)
	}

	if err := c.Delete(t.Context(), personal, dir+"/b/c/gone.md"); err != nil {
		t.Fatal(err)
	}

	if err := c.Write(t.Context(), personal, dir+"/b/c/gone.md", []byte("new")); err != nil {
		t.Fatal(err)
	}

	trash, err = c.Trash(t.Context(), personal)
	if err != nil {
		t.Fatal(err)
	}

	j = slices.IndexFunc(trash, func(d Deleted) bool { return d.Path == dir+"/b/c/gone.md" })
	if j < 0 || trash[j].Folder || trash[j].Size != 3 {
		t.Fatalf("gone.md is not in the trash %+v", trash)
	}

	if _, err := c.Restore(t.Context(), personal, trash[j].Key, ""); !errors.Is(err, ErrExists) {
		t.Errorf("a restore onto a file: %v", err)
	}

	for range 2 {
		if err := c.Purge(t.Context(), personal, trash[j].Key); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := c.Restore(t.Context(), personal, trash[j].Key, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("a restore of what was purged: %v", err)
	}
}

// peer is a client of a second user of the local OpenCloud, as OPENCLOUD_PEER_USER and
// OPENCLOUD_PEER_APP_TOKEN name it, to share with; the test skips when there is none.
func peer(t *testing.T, cloud *Cloud) *Client {
	t.Helper()

	user, token := os.Getenv("OPENCLOUD_PEER_USER"), os.Getenv("OPENCLOUD_PEER_APP_TOKEN")
	if user == "" || token == "" {
		t.Skip("needs: a second user of opencloud, OPENCLOUD_PEER_USER and OPENCLOUD_PEER_APP_TOKEN")
	}

	c, err := cloud.AppToken(user, Secret(token))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestLiveAFileIsSharedLinkedAcceptedAndUnshared(t *testing.T) {
	c, cloud, user := live(t)
	other := peer(t, cloud)

	me, err := c.Me(t.Context())
	if err != nil || me.Login != user || me.ID == "" || me.Kind != "user" {
		t.Fatalf("me %+v %v", me, err)
	}

	them, err := other.Me(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if found, err := c.FindUsers(t.Context(), them.Login); err != nil || !slices.ContainsFunc(found, func(p Person) bool { return p.ID == them.ID }) {
		t.Errorf("finding %s: %+v %v", them.Login, found, err)
	}

	if _, err := c.FindUsers(t.Context(), "x"); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Errorf("a search of one letter: %v", err)
	}

	for _, ref := range []string{them.ID, them.Login} {
		if p, err := c.Recipient(t.Context(), ref); err != nil || p.ID != them.ID || p.Kind != "user" {
			t.Errorf("the recipient %s: %+v %v", ref, p, err)
		}
	}

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	personal := spaces[slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })]
	name := fmt.Sprintf("opencloud-mcp-share-%d.md", time.Now().UnixNano())

	if err := c.Write(t.Context(), personal, name, []byte("# Shared")); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := c.dav(personal).Remove("/" + name); err != nil {
			t.Errorf("removing %s: %v", name, err)
		}
	})

	if _, err := c.Share(t.Context(), personal, name, []Person{them.Person}, "Can manage", time.Time{}); !errors.Is(err, ErrNotFound) ||
		!strings.Contains(err.Error(), "Can view") {
		t.Errorf("a role a file cannot have: %v", err)
	}

	until := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)

	shared, err := c.Share(t.Context(), personal, name, []Person{them.Person}, "can view", until)
	if err != nil || len(shared) != 1 || shared[0].To == nil || shared[0].To.ID != them.ID || shared[0].Role != "Can view" || !shared[0].Expires.Equal(until) {
		t.Fatalf("the share %+v %v", shared, err)
	}

	if _, err := c.CreateLink(t.Context(), personal, name, "view", "", "", time.Time{}); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("a link without the password OpenCloud enforces: %v", err)
	}

	link, err := c.CreateLink(t.Context(), personal, name, "view", "Demo-Pass-2026!", "For the auditors", time.Time{})
	if err != nil || link.Link == nil || link.Link.URL == "" || !link.Link.Password || link.Link.Name != "For the auditors" {
		t.Fatalf("the link %+v %v", link, err)
	}

	if err := c.SetLinkPassword(t.Context(), personal, name, link.ID, "short"); err == nil || !strings.Contains(err.Error(), "at least 8 characters") {
		t.Errorf("a weak password: %v", err)
	}

	if err := c.SetLinkPassword(t.Context(), personal, name, link.ID, "Other-Pass-2027!"); err != nil {
		t.Errorf("a new password: %v", err)
	}

	edit, never := "Can edit", time.Time{}

	changed, err := c.UpdatePermission(t.Context(), personal, name, shared[0].ID, Change{Role: &edit, Expires: &never})
	if err != nil || changed.Role != "Can edit" || !changed.Expires.IsZero() {
		t.Errorf("the changed share %+v %v", changed, err)
	}

	perms, err := c.Permissions(t.Context(), personal, name)
	if err != nil || len(perms.Grants) != 2 || !slices.ContainsFunc(perms.Roles, func(r Role) bool { return r.Name == "Can view" }) {
		t.Errorf("the permissions %+v %v", perms, err)
	}

	byMe, err := c.SharedByMe(t.Context())
	if i := slices.IndexFunc(byMe, func(o Outgoing) bool { return o.Path == name }); err != nil || i < 0 || byMe[i].SpaceName != personal.Name || len(byMe[i].Grants) != 2 {
		t.Errorf("shared by me %+v %v", byMe, err)
	}

	withThem, err := other.SharedWithMe(t.Context())
	i := slices.IndexFunc(withThem, func(r Received) bool { return r.Name == name })

	if err != nil || i < 0 || withThem[i].By != me.Name || withThem[i].Role != "Can edit" {
		t.Fatalf("shared with %s %+v %v", them.Login, withThem, err)
	}

	for range 2 {
		if r, err := other.Decline(t.Context(), name); err != nil || r.Accepted {
			t.Fatalf("declining %+v %v", r, err)
		}
	}

	for range 2 {
		if r, err := other.Accept(t.Context(), withThem[i].ID); err != nil || !r.Accepted {
			t.Fatalf("accepting %+v %v", r, err)
		}
	}

	if _, err := other.Hide(t.Context(), name, true); err != nil {
		t.Fatal(err)
	}

	if again, err := other.SharedWithMe(t.Context()); err != nil || !slices.ContainsFunc(again, func(r Received) bool { return r.Name == name && r.Hidden && r.Accepted }) {
		t.Errorf("hidden %+v %v", again, err)
	}

	for _, g := range []string{shared[0].ID, link.ID} {
		if err := c.RemovePermission(t.Context(), personal, name, g); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.RemovePermission(t.Context(), personal, name, link.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice: %v", err)
	}

	if handbook, err := c.Space(t.Context(), "Handbook"); err == nil {
		members, err := c.Permissions(t.Context(), handbook, "")
		if err != nil || !slices.ContainsFunc(members.Grants, func(g Grant) bool { return g.To != nil && g.To.ID == me.ID }) {
			t.Errorf("the members of the Handbook %+v %v", members, err)
		}
	}
}

func TestLiveAProjectSpaceIsMadeSharedDisabledAndDeleted(t *testing.T) {
	c, cloud, _ := live(t)
	other := peer(t, cloud)

	name := fmt.Sprintf("opencloud-mcp-space-%d", time.Now().UnixNano())

	made, err := c.CreateSpace(t.Context(), name, "made by the live test", 1<<30)
	if errors.Is(err, ErrDenied) {
		t.Skipf("needs: a user who may create spaces, like the demo's alice (%v)", err)
	}

	if err != nil || made.Name != name || made.Type != "project" || made.Quota == nil || made.Quota.Total != 1<<30 {
		t.Fatalf("the space %+v %v", made, err)
	}

	t.Cleanup(func() {
		// t.Context is done by the time a cleanup runs
		ctx := context.Background()

		sp, err := c.Space(ctx, made.ID)
		if errors.Is(err, ErrSpaceNotFound) {
			return
		}

		if err == nil && !sp.Disabled {
			err = c.DisableSpace(ctx, sp)
			sp.Disabled = true
		}

		if err == nil {
			err = c.DeleteSpace(ctx, sp)
		}

		if err != nil {
			t.Errorf("removing the space %s: %v", name, err)
		}
	})

	sp, err := c.Space(t.Context(), made.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Write(t.Context(), sp, ".space/readme.md", []byte("# Readme")); err != nil {
		t.Fatal(err)
	}

	renamed, desc, quota, readme := name+"-renamed", "changed by the live test", int64(2<<30), ".space/readme.md"

	changed, err := c.UpdateSpace(t.Context(), sp, SpaceChange{Name: &renamed, Description: &desc, Quota: &quota, Readme: &readme})
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.SpaceInfo(t.Context(), sp)
	if err != nil || got.Name != renamed || got.Description != desc || got.Quota == nil || got.Quota.Total != quota || got.Readme != readme || got.Link == "" {
		t.Errorf("the changed space %+v %+v %v", changed, got, err)
	}

	if _, err := c.UpdateSpace(t.Context(), sp, SpaceChange{Image: new(".space")}); !errors.Is(err, ErrNotAFile) {
		t.Errorf("a folder as the image: %v", err)
	}

	them, err := other.Me(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	added, err := c.Share(t.Context(), sp, "", []Person{them.Person}, "Can view", time.Time{})
	if err != nil || len(added) != 1 || added[0].Role != "Can view" {
		t.Fatalf("the member %+v %v", added, err)
	}

	if _, err := other.Space(t.Context(), made.ID); err != nil {
		t.Errorf("the member does not see the space: %v", err)
	}

	edit := "Can edit"
	if m, err := c.UpdatePermission(t.Context(), sp, "", added[0].ID, Change{Role: &edit}); err != nil || m.Role != edit {
		t.Errorf("the changed member %+v %v", m, err)
	}

	if err := c.RemovePermission(t.Context(), sp, "", added[0].ID); err != nil {
		t.Fatal(err)
	}

	if err := c.DeleteSpace(t.Context(), sp); !errors.Is(err, ErrDenied) {
		t.Errorf("deleting a space that is not disabled: %v", err)
	}

	for range 2 {
		if err := c.DisableSpace(t.Context(), sp); err != nil {
			t.Fatal(err)
		}
	}

	if sp, err = c.Space(t.Context(), made.ID); err != nil || !sp.Disabled {
		t.Fatalf("the disabled space %+v %v", sp, err)
	}

	if err := c.RestoreSpace(t.Context(), sp); err != nil {
		t.Fatal(err)
	}

	if sp, err = c.Space(t.Context(), made.ID); err != nil || sp.Disabled {
		t.Fatalf("the restored space %+v %v", sp, err)
	}

	if err := c.RestoreSpace(t.Context(), sp); err != nil {
		t.Errorf("restoring twice: %v", err)
	}

	if err := c.DisableSpace(t.Context(), sp); err != nil {
		t.Fatal(err)
	}

	sp.Disabled = true
	if err := c.DeleteSpace(t.Context(), sp); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Space(t.Context(), made.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Errorf("the deleted space: %v", err)
	}
}

func TestLiveAFileIsTaggedAndUntagged(t *testing.T) {
	c, _, _ := live(t)

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	personal := spaces[slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })]
	name := fmt.Sprintf("opencloud-mcp-tags-%d.md", time.Now().UnixNano())

	if err := c.Write(t.Context(), personal, name, []byte("# Tagged")); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := c.dav(personal).Remove("/" + name); err != nil {
			t.Errorf("removing %s: %v", name, err)
		}
	})

	for range 2 {
		if tags, err := c.Tag(t.Context(), personal, name, []string{"invoice", " 2026 ", "invoice"}); err != nil || !slices.Equal(tags, []string{"2026", "invoice"}) {
			t.Fatalf("tagged %v %v", tags, err)
		}
	}

	if tags, err := c.Tag(t.Context(), personal, name, []string{"2026", "paid"}); err != nil || !slices.Equal(tags, []string{"2026", "invoice", "paid"}) {
		t.Errorf("tagged again %v %v", tags, err)
	}

	if it, err := c.Item(t.Context(), personal, name); err != nil || len(it.Tags) != 3 {
		t.Errorf("the item's tags %v %v", it.Tags, err)
	}

	if tags, err := c.Untag(t.Context(), personal, name, []string{"invoice", "never"}); err != nil || !slices.Equal(tags, []string{"2026", "paid"}) {
		t.Errorf("untagged %v %v", tags, err)
	}

	if all, err := c.Tags(t.Context()); err != nil || !slices.Contains(all, "paid") {
		t.Errorf("the tags in use %v %v", all, err)
	}

	if _, err := c.Tag(t.Context(), personal, name, []string{"a,b"}); err == nil {
		t.Error("a tag with a comma")
	}
}

func TestLiveTheActivitiesAndNotifications(t *testing.T) {
	c, cloud, _ := live(t)
	other := peer(t, cloud)

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	personal := spaces[slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })]
	name := fmt.Sprintf("opencloud-mcp-notice-%d.md", time.Now().UnixNano())

	if err := c.Write(t.Context(), personal, name, []byte("# Notice")); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := c.dav(personal).Remove("/" + name); err != nil {
			t.Errorf("removing %s: %v", name, err)
		}
	})

	them, err := other.Me(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Share(t.Context(), personal, name, []Person{them.Person}, "Can view", time.Time{}); err != nil {
		t.Fatal(err)
	}

	// both are written by services that take the events in the background
	var (
		notice     Notification
		activities []Activity
	)

	for range 20 {
		all, err := other.Notifications(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if i := slices.IndexFunc(all, func(n Notification) bool { return strings.Contains(n.Message, name) }); i >= 0 {
			notice = all[i]
		}

		if activities, err = c.Activities(t.Context(), personal, name, 10); err != nil {
			t.Fatal(err)
		}

		if notice.ID != "" && len(activities) > 0 {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if notice.ID == "" || notice.ObjectType != "share" || notice.When.IsZero() {
		t.Errorf("no notification of the share %+v", notice)
	}

	if len(activities) == 0 || !strings.Contains(activities[0].Message, name) || strings.Contains(activities[0].Message, "{") || activities[0].When.IsZero() {
		t.Errorf("the activities %+v", activities)
	}

	for range 2 {
		if err := other.Dismiss(t.Context(), []string{notice.ID}); err != nil {
			t.Fatal(err)
		}
	}

	if all, err := other.Notifications(t.Context()); err != nil || slices.ContainsFunc(all, func(n Notification) bool { return n.ID == notice.ID }) {
		t.Errorf("the dismissed notification is there %+v %v", all, err)
	}
}

func TestLiveADocumentIsMadeAndOpened(t *testing.T) {
	c, _, _ := live(t)

	apps, err := c.Apps(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	i := slices.IndexFunc(apps, func(a App) bool { return slices.Contains(a.Creates, "odt") })
	if i < 0 {
		t.Skipf("needs: an office app in opencloud, like the demo's Collabora (apps %+v)", apps)
	}

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	personal := spaces[slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })]
	dir := fmt.Sprintf("opencloud-mcp-office-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		if err := c.dav(personal).RemoveAll("/" + dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})

	if err := c.CreateDocument(t.Context(), personal, dir+"/q4.odt"); err != nil {
		t.Fatal(err)
	}

	if err := c.CreateDocument(t.Context(), personal, dir+"/q4.odt"); !errors.Is(err, ErrExists) {
		t.Errorf("a document onto a file: %v", err)
	}

	if err := c.CreateDocument(t.Context(), personal, dir+"/q4.xyz"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "odt") {
		t.Errorf("a type no app makes: %v", err)
	}

	link, err := c.OpenInApp(t.Context(), personal, dir+"/q4.odt", apps[i].Name)
	if err != nil || !strings.HasPrefix(link, c.base) || !strings.Contains(link, "app="+apps[i].Name) {
		t.Errorf("the link %q %v", link, err)
	}

	if _, err := c.OpenInApp(t.Context(), personal, dir, ""); !errors.Is(err, ErrNotAFile) {
		t.Errorf("a folder in an app: %v", err)
	}
}

// TestLiveTheTextOfADocumentIsFound needs an OpenCloud that extracts the text of documents, as the
// demo does with Tika; it skips where OpenCloud does not offer full-text search.
func TestLiveTheTextOfADocumentIsFound(t *testing.T) {
	c, _, _ := live(t)

	var caps struct {
		OCS struct {
			Data struct {
				Capabilities struct {
					Files struct {
						FullTextSearch bool `json:"full_text_search"`
					} `json:"files"`
				} `json:"capabilities"`
			} `json:"data"`
		} `json:"ocs"`
	}

	if _, err := c.getJSON(t.Context(), c.base+"/ocs/v1.php/cloud/capabilities?format=json", &caps); err != nil {
		t.Fatal(err)
	}

	if !caps.OCS.Data.Capabilities.Files.FullTextSearch {
		t.Skip("needs: opencloud with content extraction, like the demo's tika")
	}

	spaces, err := c.Spaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	personal := spaces[slices.IndexFunc(spaces, func(s Space) bool { return s.Type == "personal" })]
	dir := fmt.Sprintf("opencloud-mcp-text-%d", time.Now().UnixNano())

	t.Cleanup(func() {
		if err := c.dav(personal).RemoveAll("/" + dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})

	// a word no name holds, so only the text can find it
	word := "zanzibarite" + strings.Repeat(string(rune('a'+time.Now().UnixNano()%26)), 3)
	if err := c.Write(t.Context(), personal, dir+"/figures.md", []byte("The quarterly figures mention "+word+" twice.")); err != nil {
		t.Fatal(err)
	}

	// the text is extracted in the background
	deadline := time.Now().Add(60 * time.Second)

	for {
		hits, err := c.Search(t.Context(), &personal, fmt.Sprintf(`content:%q`, word), 5)
		if err != nil {
			t.Fatal(err)
		}

		if slices.ContainsFunc(hits, func(h Hit) bool { return h.Path == dir+"/figures.md" }) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s is not found by its text: %+v", word, hits)
		}

		time.Sleep(time.Second)
	}
}

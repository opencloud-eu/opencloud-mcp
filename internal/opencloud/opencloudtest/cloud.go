// Package opencloudtest is a fake OpenCloud for tests: LibreGraph lists each account's spaces with
// the etag of their root, each space's files are a real WebDAV tree in memory whose files and
// folders carry etags that every write moves up to the root, and the WebDAV search-files REPORT
// answers like OpenCloud's own search with content extraction on. LibreGraph lists folders and
// looks items up by their path, and its follow and unfollow mark favorites, which the search finds
// by is:favorite. A DELETE keeps what it removes in the space's trash and a PUT keeps what it
// replaces as a version, both as WebDAV serves them. LibreGraph knows the accounts and groups, and
// shares files, folders and spaces with them or by links, tags them, which the accounts it reaches accept,
// decline and hide in their Shares space and are told of. Writes and shares are activities. The
// app provider has one office app. An account signs in with its app token by basic auth, or with a bearer token the
// trusted issuer made out for this cloud.
package opencloudtest

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/webdav"
)

// Cloud is one OpenCloud of a few accounts.
type Cloud struct {
	mu       sync.Mutex
	tokens   map[string]string
	first    string
	spaces   []*space
	bearer   func(token string) (user string, ok bool)
	searches []string
	listed   map[string]int
	// favorites by the id of the file or folder
	favorites map[string]bool
	// versions by the id of the file, oldest first
	versions map[string][]version
	// keys counts the keys of trashed items, versions, shares and links
	keys   int
	grants []*grant
	// groups by their names, with the accounts in them
	groups map[string][]string
	// tags by the id of the file or folder
	tags map[string][]string
	// activities by the id of the item, oldest first
	activities map[string][]activity
	notices    []notice

	// Wrong counts the requests signed in otherwise or not at all.
	Wrong atomic.Int64
}

type space struct {
	owner, id, name, kind string
	fs                    webdav.FileSystem
	dav                   *webdav.Handler
	// etags of files and folders by their path, "/" being the root; a path not in here has 1
	etags map[string]int64
	trash []*deleted

	description   string
	quota         int64
	disabled      bool
	readme, image string
}

// New is a cloud of no spaces with the account user and its app token.
func New(user, token string) *Cloud {
	return &Cloud{tokens: map[string]string{user: token}, first: user, listed: map[string]int{}, favorites: map[string]bool{}, versions: map[string][]version{}, groups: map[string][]string{}, tags: map[string][]string{}, activities: map[string][]activity{}}
}

// Account adds the account user with its app token.
func (c *Cloud) Account(user, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tokens[user] = token
}

// TrustBearer takes a bearer token when accept names its user.
func (c *Cloud) TrustBearer(accept func(token string) (user string, ok bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.bearer = accept
}

// Space adds a space of the first account, see SpaceOf.
func (c *Cloud) Space(id, name, kind string, files map[string]string) error {
	return c.SpaceOf(c.first, id, name, kind, files)
}

// SpaceOf adds the space name of the drive type kind under id, which only owner sees, holding
// files by their paths.
func (c *Cloud) SpaceOf(owner, id, name, kind string, files map[string]string) error {
	mem := webdav.NewMemFS()
	for p, text := range files {
		if err := put(mem, p, text); err != nil {
			return err
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.spaces = append(c.spaces, &space{owner: owner, id: id, name: name, kind: kind, fs: mem, etags: map[string]int64{}, dav: &webdav.Handler{
		Prefix: "/dav/spaces/" + url.PathEscape(id), FileSystem: mem, LockSystem: webdav.NewMemLS(),
	}})

	return nil
}

// Put writes the file p into the space id as someone else would, moving the etags up to its root.
func (c *Cloud) Put(id, p, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, s := range c.spaces {
		if s.id == id {
			s.moved(p)

			return put(s.fs, p, text)
		}
	}

	return fmt.Errorf("no space %s", id)
}

// Searches are the patterns of every search so far, in order.
func (c *Cloud) Searches() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.searches...)
}

// Favorite reports whether the file or folder p of the space id is a favorite.
func (c *Cloud) Favorite(id, p string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.favorites[fileID(id, path.Clean("/"+p))]
}

// Listed is how often the folder p of the space id was listed by LibreGraph's children.
func (c *Cloud) Listed(id, p string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.listed[id+":"+path.Clean("/"+p)]
}

func (c *Cloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := c.signedIn(r)
	if !ok {
		c.Wrong.Add(1)
		w.Header().Set("WWW-Authenticate", `Basic realm="opencloud"`)
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	c.mu.Lock()

	var mine []*space

	for _, s := range c.spaces {
		if s.owner == user || c.member(s, user) {
			mine = append(mine, s)
		}
	}
	c.mu.Unlock()

	if c.people(w, r, user) || c.sharing(w, r, user) || c.tagging(w, r, mine) || c.noticed(w, r, user) || c.apps(w, r, mine) || c.permissions(w, r, user, mine) || c.drive(w, r, user, mine) {
		return
	}

	if r.URL.Path == "/graph/v1.0/me/drives" {
		c.drives(w, mine)

		return
	}

	if rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/graph/v1.0/drives/"); ok && r.Method == http.MethodGet {
		c.lookup(w, r, mine, rest)

		return
	}

	if id, ok := strings.CutPrefix(r.URL.Path, "/graph/v1.0/me/drive/items/"); ok && r.Method == http.MethodPost {
		c.follow(w, r, mine, strings.TrimSuffix(id, "/follow"), true)

		return
	}

	if id, ok := strings.CutPrefix(r.URL.Path, "/graph/v1.0/me/drive/following/"); ok && r.Method == http.MethodDelete {
		c.follow(w, r, mine, id, false)

		return
	}

	if rest, ok := strings.CutPrefix(r.URL.Path, "/dav/spaces/trash-bin/"); ok {
		c.trashBin(w, r, mine, strings.TrimSuffix(rest, "/"))

		return
	}

	if rest, ok := strings.CutPrefix(r.URL.Path, "/dav/meta/"); ok {
		c.meta(w, r, mine, rest)

		return
	}

	if r.Method == "REPORT" && r.URL.Path == "/dav/spaces/" {
		c.search(w, r, mine)

		return
	}

	for _, s := range mine {
		if r.URL.Path != s.dav.Prefix && !strings.HasPrefix(r.URL.Path, s.dav.Prefix+"/") {
			continue
		}

		p := path.Clean("/" + strings.TrimPrefix(r.URL.Path, s.dav.Prefix))

		switch r.Method {
		case "REPORT":
			c.search(w, r, []*space{s})
		case methodPropfind:
			c.propfind(w, r, s, p)
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			if r.URL.Query().Get("preview") == "1" {
				c.preview(w, r, s, p)
			} else {
				s.dav.ServeHTTP(w, r)
			}
		case methodCopy:
			// golang.org/x/net/webdav copies within its own prefix only
			if to, q, ok := elsewhere(mine, s, r.Header.Get("Destination")); ok {
				c.copyAcross(w, r, s, p, to, q)
			} else {
				c.change(w, r, s, p)
			}
		case http.MethodDelete:
			c.trash(r, s, p)
			c.change(w, r, s, p)
		case http.MethodPut:
			c.keep(r, s, p)

			c.mu.Lock()
			c.record(s, p, "{user} added {resource} to {folder}", map[string]map[string]string{
				keyUser: {keyDisplayName: displayName(user)}, "resource": {keyName: path.Base(p)}, "folder": {keyName: s.name},
			})
			c.mu.Unlock()

			c.change(w, r, s, p)
		default:
			c.change(w, r, s, p)
		}

		return
	}

	w.WriteHeader(http.StatusNotFound)
}

// change lets the space's WebDAV change p, moving the etags of p and of a destination; a
// destination in another space is a 502, as OpenCloud answers a move across spaces.
func (c *Cloud) change(w http.ResponseWriter, r *http.Request, s *space, p string) {
	// golang.org/x/net/webdav answers a missing source of a move with 403, OpenCloud with 404
	if (r.Method == methodMove || r.Method == methodCopy) && !statOK(r.Context(), s.fs, p) {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	c.mu.Lock()
	s.moved(p)

	if dest, err := url.Parse(r.Header.Get("Destination")); err == nil && dest.Path != "" {
		s.moved(strings.TrimPrefix(dest.Path, s.dav.Prefix))
	}
	c.mu.Unlock()

	s.dav.ServeHTTP(w, r)
}

// elsewhere is the space other than s and the path there that a Destination names.
func elsewhere(spaces []*space, s *space, destination string) (*space, string, bool) {
	dest, err := url.Parse(destination)
	if err != nil {
		return nil, "", false
	}

	for _, to := range spaces {
		if q, ok := strings.CutPrefix(dest.Path, to.dav.Prefix+"/"); ok && to != s {
			return to, path.Clean("/" + q), true
		}
	}

	return nil, "", false
}

// copyAcross copies p of the space from to q of the space to, as a WebDAV COPY answers.
func (c *Cloud) copyAcross(w http.ResponseWriter, r *http.Request, from *space, p string, to *space, q string) {
	ctx := r.Context()

	_, err := to.fs.Stat(ctx, q)
	exists := err == nil

	switch {
	case !statOK(ctx, from.fs, p):
		w.WriteHeader(http.StatusNotFound)

		return
	case !statOK(ctx, to.fs, path.Dir(q)):
		w.WriteHeader(http.StatusConflict)

		return
	case exists && r.Header.Get("Overwrite") == "F":
		w.WriteHeader(http.StatusPreconditionFailed)

		return
	case exists:
		if err := to.fs.RemoveAll(ctx, q); err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}
	}

	if err := copyTree(ctx, from.fs, p, to.fs, q); err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	c.mu.Lock()
	to.moved(q)
	c.mu.Unlock()

	if exists {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

func statOK(ctx context.Context, mem webdav.FileSystem, p string) bool {
	_, err := mem.Stat(ctx, p)

	return err == nil
}

// copyTree copies the file or folder p of from to q of to.
func copyTree(ctx context.Context, from webdav.FileSystem, p string, to webdav.FileSystem, q string) error {
	info, err := from.Stat(ctx, p)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		text, err := readAll(ctx, from, p)
		if err != nil {
			return err
		}

		return put(to, q, text)
	}

	if err := to.Mkdir(ctx, q, 0o755); err != nil {
		return err
	}

	return walkFS(ctx, from, p, func(child string, info fs.FileInfo, err error) error {
		if err != nil || path.Dir(child) != p {
			return err
		}

		return copyTree(ctx, from, child, to, path.Join(q, info.Name()))
	})
}

// preview answers GET ?preview=1 of a PNG with a blank PNG that fits the box x by y as OpenCloud's
// fit processor makes it, never larger than the image; anything else has no preview, a 404.
func (c *Cloud) preview(w http.ResponseWriter, r *http.Request, s *space, p string) {
	text, err := readAll(r.Context(), s.fs, p)
	if err != nil || path.Ext(p) != ".png" {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	cfg, err := png.DecodeConfig(strings.NewReader(text))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	x, _ := strconv.Atoi(r.URL.Query().Get("x"))
	y, _ := strconv.Atoi(r.URL.Query().Get("y"))
	scale := min(float64(x)/float64(cfg.Width), float64(y)/float64(cfg.Height), 1)

	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, int(float64(cfg.Width)*scale), int(float64(cfg.Height)*scale)))); err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "image/png")
	// a broken write shows in the test as an image it did not get
	_, _ = w.Write(out.Bytes())
}

func (c *Cloud) signedIn(r *http.Request) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if user, pass, ok := r.BasicAuth(); ok {
		return user, c.tokens[user] != "" && c.tokens[user] == pass
	}

	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || c.bearer == nil {
		return "", false
	}

	return c.bearer(token)
}

// moved moves the etags of p and every folder above it; the caller holds c.mu.
func (s *space) moved(p string) {
	for p = path.Clean("/" + p); ; p = path.Dir(p) {
		s.etags[p] = s.etag(p) + 1

		if p == "/" {
			return
		}
	}
}

func (s *space) etag(p string) int64 {
	if n, ok := s.etags[p]; ok {
		return n
	}

	return 1
}

func (c *Cloud) drives(w http.ResponseWriter, spaces []*space) {
	c.mu.Lock()

	drives := make([]map[string]any, 0, len(spaces))
	for _, s := range spaces {
		drives = append(drives, s.driveJSON())
	}
	c.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{keyValue: drives})
}

// follow marks the item id of the spaces as a favorite, or unmarks it, as OpenCloud 9.0.0 does: an
// unfollow of what is no favorite fails with 500.
func (c *Cloud) follow(w http.ResponseWriter, r *http.Request, spaces []*space, id string, on bool) {
	known := false

	for _, s := range spaces {
		sid, h, ok := strings.Cut(id, "!")
		if p := unhex(h); ok && sid == s.id && p != "" {
			if _, err := s.fs.Stat(r.Context(), p); err == nil {
				known = true
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case !known:
		w.WriteHeader(http.StatusBadRequest)
	case on:
		c.favorites[id] = true

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// a broken write shows in the test as an answer it did not get
		_, _ = io.WriteString(w, `{"@libre.graph.me.following":true}`)
	case !c.favorites[id]:
		w.WriteHeader(http.StatusInternalServerError)
	default:
		delete(c.favorites, id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// lookup answers LibreGraph's GET of an item, by its path or id, and of the children of a folder;
// rest is the escaped path below /drives/. The children of a file are a 500, as in OpenCloud
// 9.0.0.
func (c *Cloud) lookup(w http.ResponseWriter, r *http.Request, spaces []*space, rest string) {
	s, p, children, ok := located(spaces, rest)
	if !ok {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	var answer any

	switch {
	case !children:
		answer = c.item(s, p, info, strings.Contains(r.URL.Query().Get("$select"), "allowedValues"))
	case !info.IsDir():
		w.WriteHeader(http.StatusInternalServerError)

		return
	default:
		f, err := s.fs.OpenFile(r.Context(), p, os.O_RDONLY, 0)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		infos, err := f.Readdir(-1)
		if cerr := f.Close(); err == nil {
			err = cerr
		}

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		c.mu.Lock()
		c.listed[s.id+":"+p]++
		c.mu.Unlock()

		items := make([]map[string]any, 0, len(infos))
		for _, child := range infos {
			items = append(items, c.item(s, path.Join(p, child.Name()), child, false))
		}

		answer = map[string]any{keyValue: items}
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(answer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// types are the MIME types OpenCloud gives the files of the tests, the same on every machine
var types = map[string]string{".md": "text/markdown", ".txt": "text/plain", ".png": "image/png", ".pdf": "application/pdf"}

// located is the space and path an escaped drive path of LibreGraph names, {id}/root:/<path>[:]
// or {id}/items/{item id}, and whether it asks for the children with :/children or /children.
func located(spaces []*space, rest string) (*space, string, bool, bool) {
	var (
		drive, owner, p string
		children        bool
	)

	if d, at, ok := strings.Cut(rest, "/root:"); ok {
		at, children = strings.CutSuffix(at, ":/children")
		at = strings.TrimSuffix(at, ":")

		// as in OpenCloud, a colon left in the path starts a suffix this does not know
		unescaped, err := url.PathUnescape(at)
		if err != nil || strings.Contains(at, ":") {
			return nil, "", false, false
		}

		drive, p = d, unescaped
	} else if d, escaped, ok := strings.Cut(rest, "/items/"); ok {
		escaped, children = strings.CutSuffix(escaped, "/children")

		item, err := url.PathUnescape(escaped)
		if err != nil {
			return nil, "", false, false
		}

		// an item id without a node is the top of its space
		var node string
		owner, node, _ = strings.Cut(item, "!")
		drive, p = d, "/"

		if node != "" {
			p = unhex(node)
		}
	} else {
		return nil, "", false, false
	}

	id, err := url.PathUnescape(drive)
	i := slices.IndexFunc(spaces, func(s *space) bool { return s.id == id })

	if err != nil || i < 0 || p == "" || (owner != "" && owner != id) {
		return nil, "", false, false
	}

	return spaces[i], path.Clean("/" + p), children, true
}

// item is the file or folder p of the space as LibreGraph writes it, with the user's permissions
// when they are asked for.
func (c *Cloud) item(s *space, p string, info fs.FileInfo, permissions bool) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()

	it := map[string]any{
		"id": fileID(s.id, p), keyName: info.Name(), "eTag": fmt.Sprintf(`"%d"`, s.etag(p)),
		"lastModifiedDateTime": info.ModTime().UTC().Format(time.RFC3339Nano), "size": info.Size(),
		keyWebURL: "https://cloud.example.com/f/" + url.PathEscape(fileID(s.id, p)),
	}

	switch {
	case p == "/":
		it["root"], it[keyName] = map[string]any{}, "."
	case info.IsDir():
		it["folder"] = map[string]any{}
	default:
		it["file"] = map[string]any{"mimeType": cmp.Or(types[path.Ext(p)], "application/octet-stream")}
	}

	if p != "/" {
		it[keyParent] = map[string]any{keyDriveID: s.id, keyPath: path.Dir(p)}
	}

	if permissions {
		it["@libre.graph.permissions.actions.allowedValues"] = []string{"libre.graph/driveItem/content/read", "libre.graph/driveItem/standard/delete", "libre.graph/driveItem/deleted/delete"}
	}

	if c.favorites[fileID(s.id, p)] {
		it["@libre.graph.me.following"] = true
	}

	if tags := c.tags[fileID(s.id, p)]; len(tags) > 0 {
		it["@libre.graph.tags"] = tags
	}

	return it
}

// fileID is the id of the file or folder p of the space id: the space's id and the path in hex.
func fileID(id, p string) string { return fmt.Sprintf("%s!%x", id, p) }

func unhex(h string) string {
	// a broken id is the empty path, which follow refuses
	p, _ := hex.DecodeString(h)

	return string(p)
}

// propfind answers a PROPFIND of depth 0, as gowebdav sends it before a write, with the properties
// it reads.
func (c *Cloud) propfind(w http.ResponseWriter, r *http.Request, s *space, p string) {
	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	href := (&url.URL{Path: s.dav.Prefix + p}).EscapedPath()
	kind, size := "", fmt.Sprintf("<d:getcontentlength>%d</d:getcontentlength>", info.Size())

	if info.IsDir() {
		href, kind, size = strings.TrimSuffix(href, "/")+"/", "<d:collection/>", ""
	}

	c.mu.Lock()
	etag := s.etag(p)
	c.mu.Unlock()

	var out strings.Builder

	fmt.Fprintf(&out, `<?xml version="1.0" encoding="UTF-8"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:prop>`+
		`<d:resourcetype>%s</d:resourcetype>%s<d:getlastmodified>%s</d:getlastmodified><d:getetag>"%d"</d:getetag></d:prop>`+
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`,
		escape(href), kind, size, info.ModTime().UTC().Format(http.TimeFormat), etag)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	// a broken write shows in the test as an answer it did not get
	_, _ = io.WriteString(w, out.String())
}

var (
	namePattern    = regexp.MustCompile(`name:"\*([^"]*)\*"`)
	contentPattern = regexp.MustCompile(`content:"([^"]*)"`)
)

// search answers a search-files REPORT in the spaces: a file matches when its name holds the name
// term or its text the content term, in any case; with is:favorite only a favorite does, and every
// favorite when there is no other term. A query that ends on an operator or leaves a
// parenthesis open is refused, in OpenCloud's words.
func (c *Cloud) search(w http.ResponseWriter, r *http.Request, spaces []*space) {
	var rep struct {
		Search struct {
			Pattern string `xml:"pattern"`
			Limit   int    `xml:"limit"`
		} `xml:"search"`
	}

	if err := xml.NewDecoder(r.Body).Decode(&rep); err != nil || rep.Search.Pattern == "" {
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	c.mu.Lock()
	c.searches = append(c.searches, rep.Search.Pattern)
	c.mu.Unlock()

	if q := strings.TrimSpace(rep.Search.Pattern); strings.HasSuffix(q, " AND") || strings.Count(q, "(") != strings.Count(q, ")") {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		// as OpenCloud answers it, a broken write shows in the test as an answer it did not get
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">`+
			`<s:exception>Sabre\DAV\Exception\BadRequest</s:exception><s:message>error: bad request: invalid query syntax at 1:20</s:message></d:error>`)

		return
	}

	c.mu.Lock()
	m := matcher{limit: rep.Search.Limit, favorites: map[string]bool{}}
	for id := range c.favorites {
		m.favorites[id] = true
	}
	c.mu.Unlock()

	m.onlyFavorites = strings.Contains(rep.Search.Pattern, "is:favorite")

	if g := namePattern.FindStringSubmatch(rep.Search.Pattern); g != nil {
		m.name = strings.ToLower(g[1])
	}

	if g := contentPattern.FindStringSubmatch(rep.Search.Pattern); g != nil {
		m.content = strings.ToLower(g[1])
	}

	m.out.WriteString(`<?xml version="1.0" encoding="UTF-8"?><d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">`)

	for _, s := range spaces {
		walk := func(p string, info fs.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}

			return m.match(r.Context(), s, p, info)
		}

		if err := walkFS(r.Context(), s.fs, "/", walk); err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}
	}

	m.out.WriteString(`</d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	// a broken write shows in the test as results it did not get
	_, _ = io.WriteString(w, m.out.String())
}

// matcher writes the search's results as it walks the spaces.
type matcher struct {
	name, content string
	onlyFavorites bool
	favorites     map[string]bool
	limit, found  int
	out           strings.Builder
}

func (m *matcher) match(ctx context.Context, s *space, p string, info fs.FileInfo) error {
	if m.limit > 0 && m.found >= m.limit {
		return nil
	}

	text, err := readAll(ctx, s.fs, p)
	if err != nil {
		return err
	}

	favorite := m.favorites[fileID(s.id, p)]
	byName := m.name != "" && strings.Contains(strings.ToLower(info.Name()), m.name)
	byText := m.content != "" && strings.Contains(strings.ToLower(text), m.content)
	every := m.onlyFavorites && m.name == "" && m.content == ""

	if (!byName && !byText && !every) || (m.onlyFavorites && !favorite) {
		return nil
	}

	m.found++
	href := (&url.URL{Path: "/dav/spaces/" + s.id + p}).EscapedPath()
	highlights := ""

	if byText {
		highlights = "<mark>" + m.content + "</mark>"
	}

	fmt.Fprintf(&m.out, `<d:response><d:href>%s</d:href><d:propstat><d:prop><oc:name>%s</oc:name><d:getcontentlength>%d</d:getcontentlength>`+
		`<d:resourcetype></d:resourcetype><oc:fileid>%s</oc:fileid><oc:favorite>%s</oc:favorite><oc:score>1</oc:score><oc:highlights>%s</oc:highlights>`+
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
		escape(href), escape(info.Name()), info.Size(), escape(fileID(s.id, p)), flag(favorite), escape(highlights))

	return nil
}

// put writes text to p in mem, creating its folders.
func put(mem webdav.FileSystem, p, text string) error {
	ctx := context.Background()
	dir := "/"

	for part := range strings.SplitSeq(strings.Trim(path.Dir(p), "/"), "/") {
		if part == "" || part == "." {
			continue
		}

		dir = path.Join(dir, part)
		if err := mem.Mkdir(ctx, dir, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
	}

	f, err := mem.OpenFile(ctx, p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	if _, err := io.WriteString(f, text); err != nil {
		_ = f.Close() // the write's error is the one that counts

		return err
	}

	return f.Close()
}

// walkFS calls fn for every file and folder under dir.
func walkFS(ctx context.Context, mem webdav.FileSystem, dir string, fn func(string, fs.FileInfo, error) error) error {
	f, err := mem.OpenFile(ctx, dir, os.O_RDONLY, 0)
	if err != nil {
		return err
	}

	infos, err := f.Readdir(-1)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return err
	}

	for _, info := range infos {
		p := path.Join(dir, info.Name())
		if err := fn(p, info, nil); err != nil {
			return err
		}

		if info.IsDir() {
			if err := walkFS(ctx, mem, p, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

func readAll(ctx context.Context, mem webdav.FileSystem, p string) (string, error) {
	f, err := mem.OpenFile(ctx, p, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()

	b, err := io.ReadAll(f)

	return string(b), err
}

func flag(on bool) string {
	if on {
		return "1"
	}

	return "0"
}

func escape(s string) string {
	var b strings.Builder
	// escaping into a strings.Builder does not fail
	_ = xml.EscapeText(&b, []byte(s))

	return b.String()
}

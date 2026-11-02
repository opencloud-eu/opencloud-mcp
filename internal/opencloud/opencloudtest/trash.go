package opencloudtest

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

// the WebDAV methods beyond net/http's
const (
	methodPropfind = "PROPFIND"
	methodMove     = "MOVE"
	methodCopy     = "COPY"
)

// deleted is a file or folder in the trash of a space, its content kept in a file system of its
// own.
type deleted struct {
	key, path string
	folder    bool
	size      int64
	at        time.Time
	fs        webdav.FileSystem
}

// version is an earlier content of a file.
type version struct {
	key, text string
	at        time.Time
}

// trash takes the file or folder p of the space into its trash before a DELETE removes it.
func (c *Cloud) trash(r *http.Request, s *space, p string) {
	ctx := r.Context()

	info, err := s.fs.Stat(ctx, p)
	if err != nil {
		return
	}

	kept := webdav.NewMemFS()
	if err := copyTree(ctx, s.fs, p, kept, "/kept"); err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.keys++
	s.trash = append(s.trash, &deleted{
		key: fmt.Sprintf("%08d-0000-0000-0000-000000000000", c.keys), path: strings.TrimPrefix(p, "/"),
		folder: info.IsDir(), size: info.Size(), at: time.Now().UTC().Truncate(time.Second), fs: kept,
	})
}

// keep makes the content of the file p of the space a version before a PUT replaces it.
func (c *Cloud) keep(r *http.Request, s *space, p string) {
	text, err := readAll(r.Context(), s.fs, p)
	if err != nil {
		return
	}

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil || info.IsDir() {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.keys++
	id := fileID(s.id, p)
	c.versions[id] = append(c.versions[id], version{key: fmt.Sprintf("%s.REV.%d", id, c.keys), text: text, at: info.ModTime().UTC()})
}

// trashBin answers the WebDAV trash of a space, /dav/spaces/trash-bin/<space id>[/<key>]: a
// PROPFIND lists it, a MOVE restores, a DELETE purges one item or all. A MOVE of a key it does not
// know is a 500, as in OpenCloud 9.0.0.
func (c *Cloud) trashBin(w http.ResponseWriter, r *http.Request, spaces []*space, rest string) {
	id, key, _ := strings.Cut(rest, "/")

	i := slices.IndexFunc(spaces, func(s *space) bool { return s.id == id })
	if i < 0 {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	s := spaces[i]

	c.mu.Lock()
	defer c.mu.Unlock()

	j := slices.IndexFunc(s.trash, func(d *deleted) bool { return d.key == key })

	switch {
	case r.Method == methodPropfind && key == "":
		var out strings.Builder

		out.WriteString(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">`)
		fmt.Fprintf(&out, `<d:response><d:href>/dav/spaces/trash-bin/%s/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype>`+
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, escape(id))

		for _, d := range s.trash {
			kind, size := "", fmt.Sprintf("<d:getcontentlength>%d</d:getcontentlength>", d.size)
			if d.folder {
				kind, size = "<d:collection/>", ""
			}

			fmt.Fprintf(&out, `<d:response><d:href>/dav/spaces/trash-bin/%s/%s</d:href><d:propstat><d:prop>`+
				`<oc:trashbin-original-location>%s</oc:trashbin-original-location><oc:trashbin-delete-timestamp>%d</oc:trashbin-delete-timestamp>`+
				`<d:resourcetype>%s</d:resourcetype>%s</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
				escape(id), d.key, escape(d.path), d.at.Unix(), kind, size)
		}

		out.WriteString(`</d:multistatus>`)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		// a broken write shows in the test as an answer it did not get
		_, _ = io.WriteString(w, out.String())
	case r.Method == http.MethodDelete && key == "":
		s.trash = nil
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		if j >= 0 {
			s.trash = slices.Delete(s.trash, j, j+1)
		}

		w.WriteHeader(http.StatusNoContent)
	case r.Method == methodMove && j < 0:
		w.WriteHeader(http.StatusInternalServerError)
	case r.Method == methodMove:
		c.restore(w, r, s, j)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// restore moves the item j out of the trash of the space to the Destination; the caller holds c.mu.
func (c *Cloud) restore(w http.ResponseWriter, r *http.Request, s *space, j int) {
	dest, err := url.Parse(r.Header.Get("Destination"))
	q, ok := strings.CutPrefix(dest.Path, s.dav.Prefix+"/")

	if err != nil || !ok {
		w.WriteHeader(http.StatusBadGateway)

		return
	}

	ctx := r.Context()
	q = path.Clean("/" + q)

	switch {
	case !statOK(ctx, s.fs, path.Dir(q)):
		w.WriteHeader(http.StatusConflict)
	case statOK(ctx, s.fs, q):
		w.WriteHeader(http.StatusPreconditionFailed)
	default:
		if err := copyTree(ctx, s.trash[j].fs, "/kept", s.fs, q); err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		s.trash = slices.Delete(s.trash, j, j+1)
		s.moved(q)
		w.WriteHeader(http.StatusCreated)
	}
}

// meta answers the versions of a file, /dav/meta/<file id>/v[/<key>]: a PROPFIND lists them, a GET
// reads one, a COPY onto the file restores it.
func (c *Cloud) meta(w http.ResponseWriter, r *http.Request, spaces []*space, rest string) {
	id, key, _ := strings.Cut(strings.TrimSuffix(rest, "/"), "/v")
	key = strings.TrimPrefix(key, "/")

	sid, h, _ := strings.Cut(id, "!")
	p := unhex(h)

	i := slices.IndexFunc(spaces, func(s *space) bool { return s.id == sid })
	if i < 0 || p == "" || !statOK(r.Context(), spaces[i].fs, p) {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	s := spaces[i]

	c.mu.Lock()
	all := slices.Clone(c.versions[id])
	c.mu.Unlock()

	j := slices.IndexFunc(all, func(v version) bool { return v.key == key })

	switch {
	case r.Method == methodPropfind && key == "":
		var out strings.Builder

		out.WriteString(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">`)
		fmt.Fprintf(&out, `<d:response><d:href>/dav/meta/%s/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype>`+
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, escape(url.PathEscape(id)))

		// newest first, as OpenCloud lists them
		for _, v := range slices.Backward(all) {
			fmt.Fprintf(&out, `<d:response><d:href>/dav/meta/%s/v/%s</d:href><d:propstat><d:prop><d:getcontentlength>%d</d:getcontentlength>`+
				`<d:getlastmodified>%s</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
				escape(url.PathEscape(id)), escape(url.PathEscape(v.key)), len(v.text), v.at.Format(http.TimeFormat))
		}

		out.WriteString(`</d:multistatus>`)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		// a broken write shows in the test as an answer it did not get
		_, _ = io.WriteString(w, out.String())
	case j < 0:
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet:
		// a broken write shows in the test as a version it did not read
		_, _ = io.WriteString(w, all[j].text)
	case r.Method == methodCopy:
		c.keep(r, s, p)

		c.mu.Lock()
		c.versions[id] = slices.DeleteFunc(c.versions[id], func(v version) bool { return v.key == key })
		s.moved(p)
		c.mu.Unlock()

		if err := put(s.fs, p, all[j].text); err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

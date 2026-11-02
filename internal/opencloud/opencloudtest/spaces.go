package opencloudtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

// member reports whether a share of the space's root reaches the user; the caller holds c.mu.
func (c *Cloud) member(s *space, user string) bool {
	root := fileID(s.id, "/")

	return slices.ContainsFunc(c.grants, func(g *grant) bool { return g.item == root && slices.Contains(c.reached(g), user) })
}

// driveJSON is the space as LibreGraph writes a drive; the caller holds c.mu.
func (s *space) driveJSON() map[string]any {
	root := map[string]any{"eTag": fmt.Sprintf(`"%d"`, s.etag("/"))}
	if s.disabled {
		root["deleted"] = map[string]string{"state": "trashed"}
	}

	d := map[string]any{
		"id": s.id, keyName: s.name, "driveType": s.kind, "root": root,
		keyWebURL: "https://cloud.example.com/f/" + url.PathEscape(s.id),
	}

	if s.description != "" {
		d["description"] = s.description
	}

	if s.quota > 0 {
		d["quota"] = map[string]any{"total": s.quota, "used": 0, "remaining": s.quota, "state": "normal"}
	}

	var special []map[string]any

	for _, f := range []struct{ name, p string }{{"readme", s.readme}, {"image", s.image}} {
		if f.p != "" {
			special = append(special, map[string]any{
				keyName: path.Base(f.p), "specialFolder": map[string]string{keyName: f.name},
				keyParent: map[string]any{keyDriveID: s.id, keyPath: path.Dir(f.p)},
			})
		}
	}

	if special != nil {
		d["special"] = special
	}

	return d
}

// drive answers LibreGraph's POST /v1.0/drives, which makes a project space of the user, and the
// GET, PATCH and DELETE of /v1.0/drives/{id}: a DELETE disables a space, with Purge deletes a
// disabled one, a PATCH with Restore enables it again. False when the path is none of these.
func (c *Cloud) drive(w http.ResponseWriter, r *http.Request, user string, spaces []*space) bool {
	if r.URL.Path == "/graph/v1.0/drives" && r.Method == http.MethodPost {
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Quota       struct {
				Total int64 `json:"total"`
			} `json:"quota"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			graphError(w, http.StatusBadRequest, "invalid request body")

			return true
		}

		c.mu.Lock()
		c.keys++
		id := fmt.Sprintf("storage$space-%d", c.keys)
		c.mu.Unlock()

		if err := c.SpaceOf(user, id, body.Name, "project", nil); err != nil {
			graphError(w, http.StatusInternalServerError, err.Error())

			return true
		}

		c.mu.Lock()
		defer c.mu.Unlock()

		s := c.spaces[len(c.spaces)-1]
		s.description, s.quota = body.Description, body.Quota.Total
		writeJSON(w, http.StatusCreated, s.driveJSON())

		return true
	}

	id, ok := strings.CutPrefix(r.URL.Path, "/graph/v1.0/drives/")
	if !ok || strings.Contains(id, "/") || r.Method == http.MethodPost {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	i := slices.IndexFunc(spaces, func(s *space) bool { return s.id == id })
	if i < 0 {
		graphError(w, http.StatusNotFound, "drive not found")

		return true
	}

	s := spaces[i]

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.driveJSON())
	case http.MethodDelete:
		switch purge := r.Header.Get("Purge") == "T"; {
		case purge && !s.disabled:
			graphError(w, http.StatusBadRequest, "error: bad request: can't purge enabled space")
		case purge:
			c.spaces = slices.DeleteFunc(c.spaces, func(x *space) bool { return x == s })
			w.WriteHeader(http.StatusNoContent)
		default:
			s.disabled = true
			w.WriteHeader(http.StatusNoContent)
		}
	case http.MethodPatch:
		c.update(w, r, s)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}

	return true
}

// update answers the PATCH of a space; the caller holds c.mu.
func (c *Cloud) update(w http.ResponseWriter, r *http.Request, s *space) {
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Quota       *struct {
			Total int64 `json:"total"`
		} `json:"quota"`
		Special []struct {
			ID            string `json:"id"`
			SpecialFolder struct {
				Name string `json:"name"`
			} `json:"specialFolder"`
		} `json:"special"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		graphError(w, http.StatusBadRequest, "invalid request body: error: EOF")

		return
	}

	if r.Header.Get("Restore") == "T" {
		if !s.disabled {
			graphError(w, http.StatusInternalServerError, "grpc error")

			return
		}

		s.disabled = false
	}

	if body.Name != nil {
		s.name = *body.Name
	}

	if body.Description != nil {
		s.description = *body.Description
	}

	if body.Quota != nil {
		s.quota = body.Quota.Total
	}

	for _, sp := range body.Special {
		sid, h, _ := strings.Cut(sp.ID, "!")
		p := unhex(h)

		if sid != s.id || p == "" || !statOK(r.Context(), s.fs, p) {
			graphError(w, http.StatusBadRequest, "invalid special item")

			return
		}

		switch sp.SpecialFolder.Name {
		case "readme":
			s.readme = strings.TrimPrefix(p, "/")
		case "image":
			s.image = strings.TrimPrefix(p, "/")
		}
	}

	writeJSON(w, http.StatusOK, s.driveJSON())
}

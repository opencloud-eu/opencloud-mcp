package opencloudtest

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

// tagging answers LibreGraph's /v1.0/extensions/org.libregraph/tags: a GET lists the tags in use,
// a PUT adds tags to an item, refusing a request of none it lacks as OpenCloud 9.0.0 does, a
// DELETE takes them off. False for any other path.
func (c *Cloud) tagging(w http.ResponseWriter, r *http.Request, spaces []*space) bool {
	if r.URL.Path != "/graph/v1.0/extensions/org.libregraph/tags" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if r.Method == http.MethodGet {
		var all []string

		for _, tags := range c.tags {
			for _, t := range tags {
				if !slices.Contains(all, t) {
					all = append(all, t)
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{keyValue: all})

		return true
	}

	var body struct {
		ResourceID string   `json:"resourceId"`
		Tags       []string `json:"tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		graphError(w, http.StatusBadRequest, "invalid request body")

		return true
	}

	sid, h, _ := strings.Cut(body.ResourceID, "!")

	i := slices.IndexFunc(spaces, func(s *space) bool { return s.id == sid })
	if i < 0 || unhex(h) == "" || !statOK(r.Context(), spaces[i].fs, unhex(h)) {
		graphError(w, http.StatusNotFound, "resource not found")

		return true
	}

	has := c.tags[body.ResourceID]

	switch r.Method {
	case http.MethodPut:
		added := false

		for _, t := range body.Tags {
			if !slices.Contains(has, t) {
				has, added = append(has, t), true
			}
		}

		if !added {
			graphError(w, http.StatusBadRequest, "no new tags in createtagsrequest or maximum reached")

			return true
		}
	case http.MethodDelete:
		has = slices.DeleteFunc(has, func(t string) bool { return slices.Contains(body.Tags, t) })
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)

		return true
	}

	c.tags[body.ResourceID] = has
	w.WriteHeader(http.StatusOK)

	return true
}

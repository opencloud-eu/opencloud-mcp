package opencloudtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

// activity is something that happened to an item, kept by the id of the item and of every
// folder above it.
type activity struct {
	id, message string
	vars        map[string]map[string]string
	at          time.Time
}

// notice is a notification to an account.
type notice struct {
	id, user, subject, message, object string
	at                                 time.Time
}

// record keeps the activity of the file or folder p of the space and the folders above it; the
// caller holds c.mu.
func (c *Cloud) record(s *space, p, message string, vars map[string]map[string]string) {
	c.keys++
	a := activity{id: fmt.Sprintf("activity-%d", c.keys), message: message, vars: vars, at: time.Now().UTC()}

	for q := path.Clean("/" + p); ; q = path.Dir(q) {
		id := fileID(s.id, q)
		if q == "/" {
			id = s.id
		}

		c.activities[id] = append(c.activities[id], a)

		if q == "/" {
			return
		}
	}
}

// notify sends the user a notification; the caller holds c.mu.
func (c *Cloud) notify(user, subject, message, object string) {
	c.keys++
	c.notices = append(c.notices, notice{
		id: fmt.Sprintf("notice-%d", c.keys), user: user, subject: subject, message: message, object: object, at: time.Now().UTC(),
	})
}

// notices answers LibreGraph's activities and OCS's notifications; false for any other path.
func (c *Cloud) noticed(w http.ResponseWriter, r *http.Request, user string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch r.URL.Path {
	case "/graph/v1beta1/extensions/org.libregraph/activities":
		id := ""
		limit := 0

		for term := range strings.SplitSeq(r.URL.Query().Get("kql"), " AND ") {
			k, v, _ := strings.Cut(term, ":")
			switch k {
			case "itemid":
				id = strings.Trim(v, `"`)
			case "limit":
				fmt.Sscan(v, &limit) //nolint:errcheck // a broken limit is none
			}
		}

		all := slices.Clone(c.activities[id])
		slices.Reverse(all)

		if limit > 0 && len(all) > limit {
			all = all[:limit]
		}

		value := make([]map[string]any, 0, len(all))
		for _, a := range all {
			value = append(value, map[string]any{
				"id": a.id, "template": map[string]any{keyMessage: a.message, "variables": a.vars},
				"times": map[string]any{"recordedTime": a.at.Format(time.RFC3339Nano)},
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{keyValue: value})
	case "/ocs/v2.php/apps/notifications/api/v1/notifications":
		if r.Method == http.MethodDelete {
			var body struct {
				IDs []string `json:"ids"`
			}

			// a broken body dismisses nothing
			_ = json.NewDecoder(r.Body).Decode(&body)
			c.notices = slices.DeleteFunc(c.notices, func(n notice) bool { return n.user == user && slices.Contains(body.IDs, n.id) })
			writeJSON(w, http.StatusOK, map[string]any{"ocs": map[string]any{"data": nil}})

			return true
		}

		var data []map[string]any

		for _, n := range c.notices {
			if n.user == user {
				data = append(data, map[string]any{
					"notification_id": n.id, "subject": n.subject, keyMessage: n.message, "object_id": n.object,
					"object_type": "share", "datetime": n.at.Format(time.RFC3339Nano),
				})
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{"ocs": map[string]any{"data": data}})
	default:
		return false
	}

	return true
}

// apps answers OpenCloud's app provider, /app/list, /app/new and /app/open-with-web, with one
// office app that opens odt and docx and makes odt; false for any other path.
func (c *Cloud) apps(w http.ResponseWriter, r *http.Request, spaces []*space) bool {
	switch r.URL.Path {
	case "/app/list":
		app := []map[string]any{{keyName: "CollaboraOnline", "description": "Open office documents with Collabora"}}
		writeJSON(w, http.StatusOK, map[string]any{"mime-types": []map[string]any{
			{"ext": "odt", "allow_creation": true, "app_providers": app}, {"ext": "docx", "app_providers": app},
		}})
	case "/app/new":
		s, p, ok := byID(spaces, r.URL.Query().Get("parent_container_id"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{keyCode: "RESOURCE_NOT_FOUND", keyMessage: "the parent container is not accessible or does not exist"})

			return true
		}

		q := path.Join(p, r.URL.Query().Get("filename"))
		if statOK(r.Context(), s.fs, q) {
			writeJSON(w, http.StatusForbidden, map[string]string{keyCode: "RESOURCE_ALREADY_EXISTS", keyMessage: "the file already exists"})

			return true
		}

		if err := put(s.fs, q, ""); err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return true
		}

		writeJSON(w, http.StatusOK, map[string]string{"file_id": fileID(s.id, q)})
	case "/app/open-with-web":
		id := r.URL.Query().Get("file_id")
		if _, _, ok := byID(spaces, id); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{keyCode: "RESOURCE_NOT_FOUND", keyMessage: "file not found"})

			return true
		}

		app := r.URL.Query().Get("app_name")
		if app == "" {
			app = "CollaboraOnline"
		}

		writeJSON(w, http.StatusOK, map[string]string{"uri": "https://cloud.example.com/external?app=" + app + "&fileId=" + url.QueryEscape(id)})
	default:
		return false
	}

	return true
}

// byID is the space and path of an item id among the spaces.
func byID(spaces []*space, id string) (*space, string, bool) {
	sid, h, _ := strings.Cut(id, "!")
	p := unhex(h)

	for _, s := range spaces {
		if s.id == sid && p != "" {
			return s, p, true
		}
	}

	return nil, "", false
}

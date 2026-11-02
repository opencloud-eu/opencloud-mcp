package opencloudtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
)

// the keys of LibreGraph's JSON the fake writes more than once
const (
	keyValue       = "value"
	keyName        = "name"
	keyDisplayName = "displayName"
	keyDriveID     = "driveId"
	keyError       = "error"
	keyUser        = "user"
	keyWebURL      = "webUrl"
	keyPath        = "path"
	keyParent      = "parentReference"
	keyCode        = "code"
	keyMessage     = "message"
)

// the kinds of items a role is for
const (
	onFile   = "File"
	onFolder = "Folder"
	onRoot   = "Root"
)

// the names of the roles more than one kind of item has
const (
	canView = "Can view"
	canEdit = "Can edit"
)

// the Shares space every account has, holding what others share with it
const jail = "shares-jail"

// role is one of the sharing roles, for a file, a folder or a space ("root").
type role struct{ id, name, on string }

var roles = []role{
	{"view-file", canView, onFile}, {"edit-file", canEdit, onFile},
	{"view-folder", canView, onFolder}, {"edit-folder", canEdit, onFolder},
	{"view-space", canView, onRoot}, {"edit-space", canEdit, onRoot}, {"manage-space", "Can manage", onRoot},
}

// grant is a share with a user or group, or a link, of the item id.
type grant struct {
	id, item, owner string
	// to is a user or group, "user:alice" or "group:team"; empty for a link
	to       string
	role     string
	link     string
	name     string
	password bool
	expires  *time.Time
	// accepted and hidden by the users the share reaches
	accepted, hidden map[string]bool
}

// Group adds the group name with its members.
func (c *Cloud) Group(name string, members ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.groups[name] = members
}

func userID(user string) string { return "user-" + user }

func displayName(user string) string {
	r := []rune(user)
	r[0] = unicode.ToUpper(r[0])

	return string(r) + " Example"
}

func graphError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{keyError: map[string]string{keyCode: "error", keyMessage: msg}})
}

// people answers LibreGraph's me, users and groups; false when the path is none of them.
func (c *Cloud) people(w http.ResponseWriter, r *http.Request, user string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	p := r.URL.Path

	switch {
	case p == "/graph/v1.0/me":
		var groups []map[string]any

		for name, members := range c.groups {
			if slices.Contains(members, user) {
				groups = append(groups, map[string]any{"id": "group-" + name, keyDisplayName: name})
			}
		}

		me := c.userJSON(user)
		me["mail"], me["preferredLanguage"], me["memberOf"] = user+"@example.com", "en", groups
		writeJSON(w, http.StatusOK, me)
	case p == "/graph/v1.0/users" || p == "/graph/v1.0/groups":
		q := strings.ToLower(strings.Trim(r.URL.Query().Get("$search"), `"`))
		if len([]rune(q)) < 3 {
			graphError(w, http.StatusForbidden, "search term too short")

			return true
		}

		found := []map[string]any{}

		if p == "/graph/v1.0/users" {
			for u := range c.tokens {
				if strings.Contains(u, q) || strings.Contains(strings.ToLower(displayName(u)), q) {
					found = append(found, c.userJSON(u))
				}
			}
		} else {
			for name := range c.groups {
				if strings.Contains(strings.ToLower(name), q) {
					found = append(found, map[string]any{"id": "group-" + name, keyDisplayName: name})
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{keyValue: found})
	case strings.HasPrefix(p, "/graph/v1.0/users/"):
		u, ok := strings.CutPrefix(strings.TrimPrefix(p, "/graph/v1.0/users/"), "user-")
		if _, known := c.tokens[u]; !ok || !known {
			graphError(w, http.StatusNotFound, "not found")

			return true
		}

		writeJSON(w, http.StatusOK, c.userJSON(u))
	case strings.HasPrefix(p, "/graph/v1.0/groups/"):
		g, ok := strings.CutPrefix(strings.TrimPrefix(p, "/graph/v1.0/groups/"), "group-")
		if _, known := c.groups[g]; !ok || !known {
			graphError(w, http.StatusNotFound, "not found")

			return true
		}

		writeJSON(w, http.StatusOK, map[string]any{"id": "group-" + g, keyDisplayName: g})
	case p == "/graph/v1beta1/roleManagement/permissions/roleDefinitions":
		defs := make([]map[string]any, 0, len(roles))
		for _, ro := range roles {
			defs = append(defs, roleJSON(ro, true))
		}

		writeJSON(w, http.StatusOK, defs)
	default:
		return false
	}

	return true
}

// userJSON is the user as LibreGraph writes another user, without mail; the caller holds c.mu.
func (c *Cloud) userJSON(user string) map[string]any {
	return map[string]any{"id": userID(user), keyDisplayName: displayName(user), "onPremisesSamAccountName": user}
}

func roleJSON(ro role, permissions bool) map[string]any {
	j := map[string]any{"id": ro.id, keyDisplayName: ro.name, "description": ro.name + " a " + strings.ToLower(ro.on)}
	if permissions {
		j["rolePermissions"] = []map[string]any{{"condition": "exists @Resource." + ro.on}}
	}

	return j
}

// permissions answers LibreGraph's permissions of an item or a space's root,
// /graph/v1beta1/drives/{id}/(root|items/{id})/(permissions[/{id}[/setPassword]]|invite|createLink);
// false when the path is none of these.
func (c *Cloud) permissions(w http.ResponseWriter, r *http.Request, user string, spaces []*space) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/graph/v1beta1/drives/")
	if !ok {
		return false
	}

	drive, rest, _ := strings.Cut(rest, "/")
	if drive == jail {
		c.jail(w, r, user, rest)

		return true
	}

	i := slices.IndexFunc(spaces, func(s *space) bool { return s.id == drive })
	if i < 0 {
		graphError(w, http.StatusNotFound, "drive not found")

		return true
	}

	s := spaces[i]

	var p, op string

	if after, ok := strings.CutPrefix(rest, "root/"); ok {
		p, op = "/", after
	} else if after, ok := strings.CutPrefix(rest, "items/"); ok {
		var id string

		id, op, _ = strings.Cut(after, "/")
		sid, h, _ := strings.Cut(id, "!")
		p = unhex(h)

		if sid != s.id || p == "" {
			graphError(w, http.StatusNotFound, "item not found")

			return true
		}
	} else {
		return false
	}

	info, err := s.fs.Stat(r.Context(), p)
	if err != nil {
		graphError(w, http.StatusNotFound, "item not found")

		return true
	}

	on := onFile

	switch {
	case p == "/":
		on = onRoot
	case info.IsDir():
		on = onFolder
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	item := fileID(s.id, p)
	// permissions/{id}[/setPassword] becomes the op permission/[setPassword] of the permission id
	perm := ""

	if after, ok := strings.CutPrefix(op, "permissions/"); ok {
		perm, op, _ = strings.Cut(after, "/")
		op = "permission/" + op
	}

	var body map[string]any
	if r.Body != nil && r.ContentLength != 0 {
		// a broken body leaves the request empty, which is refused below
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	switch {
	case r.Method == http.MethodGet && op == "permissions":
		allowed := []map[string]any{}

		for _, ro := range roles {
			if ro.on == on {
				allowed = append(allowed, roleJSON(ro, false))
			}
		}

		value := []map[string]any{}

		for _, g := range c.grants {
			if g.item == item {
				value = append(value, c.grantJSON(g))
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{"@libre.graph.permissions.roles.allowedValues": allowed, keyValue: value})
	case r.Method == http.MethodPost && op == "invite":
		c.invite(w, s, item, on, body)
	case r.Method == http.MethodPost && op == "createLink":
		kind, _ := body["type"].(string)
		password, _ := body["password"].(string)
		name, _ := body[keyDisplayName].(string)

		if kind != "internal" && password == "" {
			graphError(w, http.StatusBadRequest, "password protection is enforced")

			return true
		}

		c.keys++
		g := &grant{id: fmt.Sprintf("link-%d", c.keys), item: item, owner: s.owner, link: kind, name: name, password: password != ""}
		g.expires = expiry(body)
		c.grants = append(c.grants, g)
		writeJSON(w, http.StatusOK, c.grantJSON(g))
	default:
		c.permission(w, r, item, perm, op, body)
	}

	return true
}

// permission answers the permission id of the item: a DELETE removes it, a PATCH changes it, a
// POST of setPassword sets its password; the caller holds c.mu.
func (c *Cloud) permission(w http.ResponseWriter, r *http.Request, item, id, op string, body map[string]any) {
	j := slices.IndexFunc(c.grants, func(g *grant) bool { return g.item == item && g.id == id })
	if j < 0 {
		graphError(w, http.StatusNotFound, fmt.Sprintf("error: not found: opaque_id:%q", id))

		return
	}

	g := c.grants[j]

	switch {
	case r.Method == http.MethodDelete && op == "permission/":
		c.grants = slices.Delete(c.grants, j, j+1)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && op == "permission/setPassword":
		if pw, _ := body["password"].(string); len(pw) < 8 {
			graphError(w, http.StatusBadRequest, "at least 8 characters are required")

			return
		}

		g.password = true
		writeJSON(w, http.StatusOK, c.grantJSON(g))
	case r.Method == http.MethodPatch && op == "permission/":
		if rs, ok := body["roles"].([]any); ok && len(rs) == 1 {
			g.role, _ = rs[0].(string)
		}

		if l, ok := body["link"].(map[string]any); ok {
			if kind, ok := l["type"].(string); ok {
				g.link = kind
			}

			if name, ok := l["@libre.graph.displayName"].(string); ok {
				g.name = name
			}
		}

		if _, ok := body["expirationDateTime"]; ok {
			g.expires = expiry(body)
		}

		writeJSON(w, http.StatusOK, c.grantJSON(g))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// invite shares the item with the recipients of the body; the caller holds c.mu.
func (c *Cloud) invite(w http.ResponseWriter, s *space, item, on string, body map[string]any) {
	rs, _ := body["roles"].([]any)
	ro := ""

	if len(rs) == 1 {
		ro, _ = rs[0].(string)
	}

	if !slices.ContainsFunc(roles, func(x role) bool { return x.id == ro && x.on == on }) {
		graphError(w, http.StatusBadRequest, "role not applicable to this resource")

		return
	}

	recipients, _ := body["recipients"].([]any)
	value := []map[string]any{}

	for _, x := range recipients {
		rec, _ := x.(map[string]any)
		id, _ := rec["objectId"].(string)
		kind, _ := rec["@libre.graph.recipient.type"].(string)

		to := ""

		if u, ok := strings.CutPrefix(id, "user-"); ok && kind == "user" && c.tokens[u] != "" {
			to = "user:" + u
		} else if g, ok := strings.CutPrefix(id, "group-"); ok && kind == "group" && c.groups[g] != nil {
			to = "group:" + g
		}

		if to == "" {
			graphError(w, http.StatusBadRequest, "recipient not found")

			return
		}

		c.keys++
		g := &grant{id: fmt.Sprintf("share-%d", c.keys), item: item, owner: s.owner, to: to, role: ro, accepted: map[string]bool{}, hidden: map[string]bool{}}
		g.expires = expiry(body)

		for _, u := range c.reached(g) {
			g.accepted[u] = true
			c.notify(u, "Resource shared", displayName(s.owner)+" shared "+path.Base(unhex(strings.SplitN(item, "!", 2)[1]))+" with you", item)
		}

		c.grants = append(c.grants, g)
		value = append(value, c.grantJSON(g))
	}

	writeJSON(w, http.StatusOK, map[string]any{keyValue: value})
}

func expiry(body map[string]any) *time.Time {
	s, _ := body["expirationDateTime"].(string)

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}

	return &t
}

// reached are the users a share reaches; the caller holds c.mu.
func (c *Cloud) reached(g *grant) []string {
	if u, ok := strings.CutPrefix(g.to, "user:"); ok {
		return []string{u}
	}

	if name, ok := strings.CutPrefix(g.to, "group:"); ok {
		return c.groups[name]
	}

	return nil
}

// grantJSON is the grant as LibreGraph writes a permission; the caller holds c.mu.
func (c *Cloud) grantJSON(g *grant) map[string]any {
	j := map[string]any{"id": g.id}

	if g.expires != nil {
		j["expirationDateTime"] = g.expires.UTC().Format(time.RFC3339)
	}

	if g.link != "" {
		j["hasPassword"] = g.password
		j["link"] = map[string]any{"type": g.link, "@libre.graph.displayName": g.name, keyWebURL: "https://cloud.example.com/s/" + g.id}

		return j
	}

	j["roles"] = []string{g.role}

	if u, ok := strings.CutPrefix(g.to, "user:"); ok {
		j["grantedToV2"] = map[string]any{keyUser: map[string]any{"id": userID(u), keyDisplayName: displayName(u)}}
	} else if name, ok := strings.CutPrefix(g.to, "group:"); ok {
		j["grantedToV2"] = map[string]any{"group": map[string]any{"id": "group-" + name, keyDisplayName: name}}
	}

	return j
}

// sharing answers sharedWithMe and sharedByMe; false when the path is neither.
func (c *Cloud) sharing(w http.ResponseWriter, r *http.Request, user string) bool {
	switch r.URL.Path {
	case "/graph/v1beta1/me/drive/sharedWithMe":
		c.mu.Lock()
		defer c.mu.Unlock()

		value := []map[string]any{}
		seen := map[string]bool{}

		// one item for each shared file or folder, with every share of it that reaches the user, as
		// OpenCloud lists them
		for _, g := range c.grants {
			if !slices.Contains(c.reached(g), user) || seen[g.item] {
				continue
			}

			seen[g.item] = true

			it := c.sharedItem(g)
			if it == nil {
				continue
			}

			var perms []map[string]any

			for _, other := range c.grants {
				if other.item == g.item && slices.Contains(c.reached(other), user) {
					perms = append(perms, c.grantJSON(other))
				}
			}

			remote := map[string]any{
				"id": g.item, keyName: it[keyName], "createdBy": map[string]any{keyUser: map[string]any{keyDisplayName: displayName(g.owner)}},
				"permissions": perms,
			}

			if f, ok := it["folder"]; ok {
				remote["folder"] = f
			}

			value = append(value, map[string]any{
				"id": jail + "!" + g.id, keyName: it[keyName], keyParent: map[string]any{keyDriveID: jail, "driveType": "virtual"},
				"remoteItem": remote, "@client.synchronize": g.accepted[user], "@UI.Hidden": g.hidden[user],
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{keyValue: value})
	case "/graph/v1beta1/me/drive/sharedByMe":
		c.mu.Lock()
		defer c.mu.Unlock()

		byItem := map[string][]map[string]any{}

		var order []string

		for _, g := range c.grants {
			if g.owner != user {
				continue
			}

			if _, seen := byItem[g.item]; !seen {
				order = append(order, g.item)
			}

			byItem[g.item] = append(byItem[g.item], c.grantJSON(g))
		}

		value := []map[string]any{}

		for _, item := range order {
			for _, g := range c.grants {
				if g.item == item {
					if it := c.sharedItem(g); it != nil {
						it["permissions"] = byItem[item]
						value = append(value, it)
					}

					break
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{keyValue: value})
	default:
		return false
	}

	return true
}

// sharedItem is the item of the grant as LibreGraph writes it, nil when it is gone; the caller
// holds c.mu.
func (c *Cloud) sharedItem(g *grant) map[string]any {
	sid, h, _ := strings.Cut(g.item, "!")
	p := unhex(h)

	for _, s := range c.spaces {
		if s.id != sid {
			continue
		}

		info, err := s.fs.Stat(context.Background(), p)
		if err != nil {
			return nil
		}

		it := map[string]any{"id": g.item, keyName: info.Name(), keyParent: map[string]any{keyDriveID: s.id, keyPath: path.Dir(p)}}
		if info.IsDir() {
			it["folder"] = map[string]any{}
		}

		return it
	}

	return nil
}

// jail answers accepting, declining and hiding a share in the user's Shares space:
// POST root/children, DELETE items/{id}, PATCH items/{id}.
func (c *Cloud) jail(w http.ResponseWriter, r *http.Request, user, rest string) {
	var body map[string]any
	// a broken body leaves the request empty, which is refused below
	_ = json.NewDecoder(r.Body).Decode(&body)

	c.mu.Lock()
	defer c.mu.Unlock()

	mine := func(match func(*grant) bool) []*grant {
		var out []*grant

		for _, g := range c.grants {
			if slices.Contains(c.reached(g), user) && match(g) {
				out = append(out, g)
			}
		}

		return out
	}

	switch {
	case r.Method == http.MethodPost && rest == "root/children":
		remote, _ := body["remoteItem"].(map[string]any)
		id, _ := remote["id"].(string)

		found := mine(func(g *grant) bool { return g.item == id })
		if len(found) == 0 {
			graphError(w, http.StatusBadRequest, "invalid id")

			return
		}

		if found[0].accepted[user] {
			graphError(w, http.StatusConflict, "shares already mounted")

			return
		}

		for _, g := range found {
			g.accepted[user] = true
		}

		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(rest, "items/"):
		id, _ := strings.CutPrefix(strings.TrimPrefix(rest, "items/"), jail+"!")

		found := mine(func(g *grant) bool { return g.id == id })
		if len(found) == 0 {
			graphError(w, http.StatusNotFound, "no shares found")

			return
		}

		// every share of the same file or folder goes with it
		found = mine(func(g *grant) bool { return g.item == found[0].item })
		g := found[0]

		switch r.Method {
		case http.MethodDelete:
			if !g.accepted[user] {
				graphError(w, http.StatusConflict, "shares already unmounted")

				return
			}

			for _, g := range found {
				g.accepted[user] = false
			}

			w.WriteHeader(http.StatusNoContent)
		case http.MethodPatch:
			hidden, _ := body["@UI.Hidden"].(bool)
			for _, g := range found {
				g.hidden[user] = hidden
			}

			writeJSON(w, http.StatusOK, map[string]any{"id": jail + "!" + g.id})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

package tools

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// subscription is a document a listen asked about, as last seen.
type subscription struct {
	uri, space, path string
	etag             string
	modified         time.Time
	gone             bool
}

// watcher is what one subscriptions/listen keeps while it is open, and drops with it: the root
// etag of each space, the etags of the folders down to each subscribed document, and that
// document's own etag and modification time. It keeps no document.
type watcher struct {
	t       *tools
	info    *auth.TokenInfo
	changed bool
	roots   map[string]string
	folders map[string]string
	subs    []*subscription
}

// errSignInGone ends a listen whose sign-in OpenCloud or the provider no longer takes.
var errSignInGone = errors.New("the sign-in of this listen is no longer taken; sign in again and listen anew")

// watch looks at the root etags of the user's spaces every interval until ctx ends. When one
// moves it says the resource list changed, if the listen asked for that, and re-lists only the
// folders whose etag moved on the way to each subscribed document, which it reports as updated
// when its etag or modification time moved. A sign-in that is refused ends the listen through
// end, so the client learns of it instead of waiting for notices that never come.
func (t *tools) watch(ctx context.Context, end context.CancelCauseFunc, info *auth.TokenInfo, want *mcp.NotificationSubscriptions) {
	w := &watcher{t: t, info: info, changed: want.ResourcesListChanged, folders: map[string]string{}}

	for _, raw := range want.ResourceSubscriptions {
		if id, p, ok := parse(raw); ok {
			w.subs = append(w.subs, &subscription{uri: raw, space: id, path: p})
		}
	}

	if !w.changed && len(w.subs) == 0 {
		return
	}

	if err := w.look(ctx, false); err != nil {
		end(err)

		return
	}

	tick := time.NewTicker(t.interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := w.look(ctx, true); err != nil {
				end(err)

				return
			}
		}
	}
}

// look compares the spaces with what was seen before; the first look only takes them in. It
// fails only on a refused sign-in, which ends the listen; anything else is logged and looked at
// again on the next tick.
func (w *watcher) look(ctx context.Context, tell bool) error {
	c, err := w.t.access(ctx, w.info)
	if err == nil {
		var spaces []opencloud.Space

		if spaces, err = c.Spaces(ctx); err == nil {
			w.compare(ctx, c, spaces, tell)

			return nil
		}
	}

	// a listen that ends anyway fails its look, which is no news
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	if errors.Is(err, opencloud.ErrRefused) || errors.Is(err, auth.ErrInvalidToken) {
		return fmt.Errorf("%w: %v", errSignInGone, err)
	}

	w.t.log.Warn("watching the spaces", "err", err)

	return nil
}

// compare takes in the spaces of one look and tells what moved.
func (w *watcher) compare(ctx context.Context, c *opencloud.Client, spaces []opencloud.Space, tell bool) {
	moved := map[string]opencloud.Space{}
	roots := map[string]string{}

	for _, sp := range spaces {
		roots[sp.ID] = sp.ETag
		if old, seen := w.roots[sp.ID]; !seen || old != sp.ETag {
			moved[sp.ID] = sp
		}
	}

	if tell && w.changed && (len(moved) > 0 || len(roots) != len(w.roots)) {
		// the go-sdk sends resources/list_changed on a change to the server's resources; putting
		// the one template back is that change
		w.t.server.AddResourceTemplate(template, w.t.readResource)
	}

	w.roots = roots
	listings := map[string][]opencloud.Entry{}

	for _, s := range w.subs {
		sp, ok := moved[s.space]
		if !ok {
			continue
		}

		if w.refresh(ctx, c, sp, s, listings) && tell {
			if err := w.t.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: s.uri}); err != nil {
				w.t.log.Warn("telling of an updated document", "uri", s.uri, "err", err)
			}
		}
	}
}

// refresh walks down to a subscribed document through the folders whose etag moved, and says
// whether the document moved; listings holds this look's listings, by space and folder.
func (w *watcher) refresh(ctx context.Context, c *opencloud.Client, sp opencloud.Space, s *subscription, listings map[string][]opencloud.Entry) bool {
	list := func(dir string) ([]opencloud.Entry, bool) {
		key := sp.ID + ":" + dir
		if entries, ok := listings[key]; ok {
			return entries, true
		}

		entries, err := c.List(ctx, sp, dir)
		if err != nil {
			return nil, false
		}

		listings[key] = entries

		return entries, true
	}

	find := func(entries []opencloud.Entry, p string, folder bool) (opencloud.Entry, bool) {
		for _, e := range entries {
			if e.Path == p && e.Folder == folder {
				return e, true
			}
		}

		return opencloud.Entry{}, false
	}

	dir := ""

	for _, part := range folders(s.path) {
		entries, ok := list(dir)
		if !ok {
			return false
		}

		f, ok := find(entries, part, true)
		if !ok {
			return s.vanish()
		}

		key := sp.ID + ":" + part
		if old, seen := w.folders[key]; seen && old == f.ETag {
			return false
		}

		w.folders[key] = f.ETag
		dir = part
	}

	entries, ok := list(dir)
	if !ok {
		return false
	}

	e, ok := find(entries, s.path, false)
	if !ok {
		return s.vanish()
	}

	moved := s.gone || e.ETag != s.etag || !e.Modified.Equal(s.modified)
	s.etag, s.modified, s.gone = e.ETag, e.Modified, false

	return moved
}

// vanish marks the document gone and says whether that is news.
func (s *subscription) vanish() bool {
	news := !s.gone && s.etag != ""
	s.gone = true

	return news
}

// folders are the folders above p from the top down, like a, a/b for a/b/c.md.
func folders(p string) []string {
	dir := path.Dir(p)
	if dir == "." {
		return nil
	}

	parts := strings.Split(dir, "/")
	for i := range parts {
		parts[i] = strings.Join(parts[:i+1], "/")
	}

	return parts
}

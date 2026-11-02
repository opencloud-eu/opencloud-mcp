package opencloud

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Deleted is a file or folder in the trash of a space.
type Deleted struct {
	Space     string `json:"space"`
	SpaceName string `json:"spaceName"`
	// Key names it in the trash, for Restore and Purge.
	Key string `json:"key"`
	// Path is where it was before it was deleted.
	Path    string    `json:"path"`
	Folder  bool      `json:"folder,omitempty"`
	Size    int64     `json:"size,omitempty"`
	Deleted time.Time `json:"deleted,omitzero"`
}

// Trash is what was deleted in the space, newest first, by a WebDAV PROPFIND of
// /dav/spaces/trash-bin/<space id>.
func (c *Client) Trash(ctx context.Context, sp Space) ([]Deleted, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
		`<oc:trashbin-original-location/><oc:trashbin-delete-timestamp/><d:getcontentlength/><d:resourcetype/></d:prop></d:propfind>`

	resp, err := c.davSend(ctx, "PROPFIND", c.trashURL(sp), strings.NewReader(body), map[string]string{depth: "1"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusMultiStatus:
	case http.StatusForbidden, http.StatusNotFound:
		return nil, fail(ErrDenied, "%s may not see the trash of the space %s", c.who, sp.Name)
	default:
		return nil, fmt.Errorf("the trash of the space %s: %s", sp.Name, refusal(resp.Body))
	}

	var ms multistatus
	if err := xml.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("reading the trash of the space %s: %w", sp.Name, err)
	}

	out := make([]Deleted, 0, len(ms.Responses))

	for _, r := range ms.Responses {
		u, err := url.Parse(r.Href)
		if err != nil {
			continue
		}

		// the first response is the trash itself
		key := path.Base(strings.TrimSuffix(u.Path, "/"))
		if key == sp.ID {
			continue
		}

		d := Deleted{Space: sp.ID, SpaceName: sp.Name, Key: key}

		for _, ps := range r.Propstat {
			if !strings.Contains(ps.Status, " 200 ") {
				continue
			}

			p := ps.Prop
			d.Folder = p.ResourceType.Collection != nil

			if loc := strings.TrimSpace(p.Location); loc != "" {
				d.Path = loc
			}

			// a property missing from the answer leaves its field zero
			d.Size, _ = strconv.ParseInt(strings.TrimSpace(p.Length), 10, 64)
			if ts, err := strconv.ParseInt(strings.TrimSpace(p.Timestamp), 10, 64); err == nil {
				d.Deleted = time.Unix(ts, 0).UTC()
			}
		}

		if d.Path != "" {
			out = append(out, d)
		}
	}

	slices.SortStableFunc(out, func(a, b Deleted) int { return b.Deleted.Compare(a.Deleted) })

	return out, nil
}

// Restore brings the deleted item of the key back to the path to of the space, its old path when to
// is empty, by WebDAV MOVE out of the trash, and gives the path it is at.
func (c *Client) Restore(ctx context.Context, sp Space, key, to string) (string, error) {
	d, err := c.deleted(ctx, sp, key)
	if err != nil {
		return "", err
	}

	if to == "" {
		to = d.Path
	}

	if to, err = filePath(to); err != nil {
		return "", err
	}

	// OpenCloud refuses a restore into a folder that is not there with 409
	if dir := path.Dir(to); dir != "." {
		if err := c.dav(sp).MkdirAll("/"+dir, 0o755); err != nil {
			return "", c.explain(err, sp, dir)
		}
	}

	resp, err := c.davDo(ctx, "MOVE", c.trashURL(sp)+"/"+url.PathEscape(d.Key), map[string]string{
		destination: c.spaceURL(sp) + "/" + escapePath(to),
		"Overwrite": "F",
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent:
		return to, nil
	case http.StatusPreconditionFailed:
		return "", fail(ErrExists, "%q is in the space %s already; restore it to another path", to, sp.Name)
	case http.StatusForbidden:
		return "", fail(ErrDenied, "%s may not restore from the trash of the space %s: %s", c.who, sp.Name, refusal(resp.Body))
	}

	return "", fmt.Errorf("OpenCloud refused to restore %s: %s", d.Path, refusal(resp.Body))
}

// Purge deletes the item of the key from the trash of the space for good, by WebDAV DELETE; one
// that is gone already is done.
func (c *Client) Purge(ctx context.Context, sp Space, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "/\\") {
		return fail(ErrNotFound, "%q is no key of the trash", key)
	}

	resp, err := c.davDo(ctx, http.MethodDelete, c.trashURL(sp)+"/"+url.PathEscape(key), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		return nil
	case http.StatusForbidden:
		return fail(ErrDenied, "%s may not delete from the trash of the space %s: %s", c.who, sp.Name, refusal(resp.Body))
	}

	return fmt.Errorf("OpenCloud refused to delete %s from the trash: %s", key, refusal(resp.Body))
}

// EmptyTrash deletes everything in the trash of the space for good, by WebDAV DELETE of the trash;
// only for a user whose role in the space may delete from the trash.
func (c *Client) EmptyTrash(ctx context.Context, sp Space) error {
	top, err := c.Item(ctx, sp, "")
	if err != nil {
		return err
	}

	if !slices.Contains(top.Permissions, "deleted/delete") {
		return fail(ErrDenied, "%s may not delete from the trash of the space %s", c.who, sp.Name)
	}

	resp, err := c.davDo(ctx, http.MethodDelete, c.trashURL(sp), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusForbidden:
		return fail(ErrDenied, "%s may not empty the trash of the space %s: %s", c.who, sp.Name, refusal(resp.Body))
	}

	return fmt.Errorf("OpenCloud refused to empty the trash of the space %s: %s", sp.Name, refusal(resp.Body))
}

// deleted is the item of the key in the trash of the space; OpenCloud 9.0.0 answers a restore of a
// key it does not know with 500, hence the look first.
func (c *Client) deleted(ctx context.Context, sp Space, key string) (Deleted, error) {
	key = strings.TrimSpace(key)

	all, err := c.Trash(ctx, sp)
	if err != nil {
		return Deleted{}, err
	}

	if i := slices.IndexFunc(all, func(d Deleted) bool { return d.Key == key }); i >= 0 {
		return all[i], nil
	}

	return Deleted{}, fail(ErrNotFound, "%q is not in the trash of the space %s", key, sp.Name)
}

func (c *Client) trashURL(sp Space) string {
	return c.base + "/dav/spaces/trash-bin/" + url.PathEscape(sp.ID)
}

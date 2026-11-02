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

// Version is an earlier version of a file.
type Version struct {
	// Key names it, for ReadVersion and RestoreVersion.
	Key  string `json:"key"`
	Size int64  `json:"size"`
	// Written is when this version was the file's content.
	Written time.Time `json:"written,omitzero"`
}

// Versions are the earlier versions of the file p of the space, newest first, by a WebDAV PROPFIND
// of /dav/meta/<file id>/v.
func (c *Client) Versions(ctx context.Context, sp Space, p string) ([]Version, error) {
	p, id, err := c.versioned(ctx, sp, p)
	if err != nil {
		return nil, err
	}

	resp, err := c.davSend(ctx, "PROPFIND", c.versionsURL(id), nil, map[string]string{depth: "1"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusMultiStatus:
	case http.StatusForbidden:
		return nil, fail(ErrDenied, "%s may not see the versions of %s in the space %s", c.who, p, sp.Name)
	default:
		return nil, fmt.Errorf("the versions of %s in the space %s: %s", p, sp.Name, refusal(resp.Body))
	}

	var ms multistatus
	if err := xml.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("reading the versions of %s: %w", p, err)
	}

	out := make([]Version, 0, len(ms.Responses))

	for _, r := range ms.Responses {
		u, err := url.Parse(r.Href)
		if err != nil || !strings.Contains(u.Path, "/v/") {
			continue
		}

		v := Version{Key: path.Base(u.Path)}

		for _, ps := range r.Propstat {
			if !strings.Contains(ps.Status, " 200 ") {
				continue
			}

			// a property missing from the answer leaves its field zero
			v.Size, _ = strconv.ParseInt(strings.TrimSpace(ps.Prop.Length), 10, 64)
			if t, err := time.Parse(time.RFC1123, strings.TrimSpace(ps.Prop.Modified)); err == nil {
				v.Written = t.UTC()
			}
		}

		out = append(out, v)
	}

	slices.SortStableFunc(out, func(a, b Version) int { return b.Written.Compare(a.Written) })

	return out, nil
}

// ReadVersion is the content of the version key of the file p of the space, up to MaxRead bytes,
// by a WebDAV GET of /dav/meta/<file id>/v/<key>.
func (c *Client) ReadVersion(ctx context.Context, sp Space, p, key string) ([]byte, error) {
	p, id, key, err := c.version(ctx, sp, p, key)
	if err != nil {
		return nil, err
	}

	resp, err := c.davDo(ctx, http.MethodGet, c.versionsURL(id)+"/"+url.PathEscape(key), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fail(ErrNotFound, "%s in the space %s has no version %q", p, sp.Name, key)
	case http.StatusForbidden:
		return nil, fail(ErrDenied, "%s may not read the versions of %s in the space %s", c.who, p, sp.Name)
	default:
		return nil, fmt.Errorf("the version %s of %s: %s", key, p, refusal(resp.Body))
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxRead+1))
	if err != nil {
		return nil, fmt.Errorf("reading the version %s of %s: %w", key, p, err)
	}

	if len(b) > MaxRead {
		return nil, fail(ErrTooLarge, "the version of %s is larger than the %d MiB a read takes", p, MaxRead>>20)
	}

	return b, nil
}

// RestoreVersion makes the version key of the file p of the space its content, by WebDAV COPY of
// the version onto the file; the content it had becomes a version.
func (c *Client) RestoreVersion(ctx context.Context, sp Space, p, key string) error {
	p, id, key, err := c.version(ctx, sp, p, key)
	if err != nil {
		return err
	}

	resp, err := c.davDo(ctx, "COPY", c.versionsURL(id)+"/"+url.PathEscape(key), map[string]string{
		destination: c.spaceURL(sp) + "/" + escapePath(p),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fail(ErrNotFound, "%s in the space %s has no version %q", p, sp.Name, key)
	case http.StatusForbidden:
		return fail(ErrDenied, "%s may not restore a version of %s in the space %s: %s", c.who, p, sp.Name, refusal(resp.Body))
	}

	return fmt.Errorf("OpenCloud refused to restore the version %s of %s: %s", key, p, refusal(resp.Body))
}

// version is the clean path and id of a file of the space and the version key, which names one
// version of that file only.
func (c *Client) version(ctx context.Context, sp Space, p, key string) (string, string, string, error) {
	p, id, err := c.versioned(ctx, sp, p)
	if err != nil {
		return "", "", "", err
	}

	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "/\\") {
		return "", "", "", fail(ErrNotFound, "%q is no key of a version of %s", key, p)
	}

	return p, id, key, nil
}

// versioned is the clean path and the id of the file p of the space; a folder has no versions.
func (c *Client) versioned(ctx context.Context, sp Space, p string) (string, string, error) {
	p, err := filePath(p)
	if err != nil {
		return "", "", err
	}

	it, err := c.item(ctx, sp, p, "")
	if err != nil {
		return "", "", err
	}

	if it.Folder != nil {
		return "", "", fail(ErrNotAFile, "%q is a folder in the space %s; only a file has versions", p, sp.Name)
	}

	return p, it.GetId(), nil
}

func (c *Client) versionsURL(id string) string {
	return c.base + "/dav/meta/" + url.PathEscape(id) + "/v"
}

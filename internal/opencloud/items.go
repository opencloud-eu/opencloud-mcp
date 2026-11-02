package opencloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// Item is a file or folder with what OpenCloud knows of it.
type Item struct {
	Space     string    `json:"space"`
	SpaceName string    `json:"spaceName,omitempty"`
	Path      string    `json:"path"`
	ID        string    `json:"id"`
	Folder    bool      `json:"folder,omitempty"`
	Size      int64     `json:"size,omitempty"`
	MIMEType  string    `json:"mimeType,omitempty"`
	Modified  time.Time `json:"modified,omitzero"`
	Tags      []string  `json:"tags,omitempty"`
	Favorite  bool      `json:"favorite,omitempty"`
	// Shared names how it is shared, like user or link.
	Shared []string `json:"shared,omitempty"`
	// Permissions are what the user may do with it, like content/read or standard/delete.
	Permissions []string                   `json:"permissions,omitempty"`
	Link        string                     `json:"link,omitempty"`
	Image       *libregraph.Image          `json:"image,omitempty"`
	Photo       *libregraph.Photo          `json:"photo,omitempty"`
	Audio       *libregraph.Audio          `json:"audio,omitempty"`
	Video       *libregraph.Video          `json:"video,omitempty"`
	Location    *libregraph.GeoCoordinates `json:"location,omitempty"`
}

// details asks LibreGraph for the properties it gives only when asked.
const details = "$select=@libre.graph.permissions.actions.allowedValues,@libre.graph.shareTypes"

// Item is the file or folder p of the space, the top when p is empty, by LibreGraph's path lookup.
func (c *Client) Item(ctx context.Context, sp Space, p string) (Item, error) {
	it, err := c.item(ctx, sp, p, details)
	if err != nil {
		return Item{}, err
	}

	return described(it, []Space{sp}), nil
}

// ItemByID is the file or folder of the id, by LibreGraph's GET /v1.0/drives/{id}/items/{id}.
func (c *Client) ItemByID(ctx context.Context, id string) (Item, error) {
	id = strings.TrimSpace(id)
	drive, _, _ := strings.Cut(id, "!")

	if id == "" || !strings.Contains(drive, "$") {
		return Item{}, fail(ErrNotFound, "%q is no id of a file or folder", id)
	}

	var it libregraph.DriveItem

	status, err := c.getJSON(ctx, c.base+"/graph/v1.0/drives/"+url.PathEscape(drive)+"/items/"+url.PathEscape(id)+"?"+details, &it)

	switch {
	case errors.Is(err, ErrRefused), errors.Is(err, ErrForeignOrigin):
		return Item{}, err
	case status == http.StatusNotFound, status == http.StatusBadRequest:
		return Item{}, fail(ErrNotFound, "OpenCloud knows no file or folder %s that %s sees", id, c.who)
	case err != nil:
		return Item{}, fmt.Errorf("looking up %s: %w", id, err)
	}

	spaces, err := c.Spaces(ctx)
	if err != nil {
		return Item{}, err
	}

	return described(it, spaces), nil
}

// described is the drive item as an Item of one of the spaces.
func described(it libregraph.DriveItem, spaces []Space) Item {
	file := it.GetFile()
	out := Item{
		ID:          it.GetId(),
		Folder:      it.Folder != nil || it.Root != nil,
		MIMEType:    file.GetMimeType(),
		Modified:    it.GetLastModifiedDateTime(),
		Tags:        it.LibreGraphTags,
		Favorite:    it.GetLibreGraphMeFollowing(),
		Shared:      it.LibreGraphShareTypes,
		Link:        it.GetWebUrl(),
		Image:       it.Image,
		Photo:       it.Photo,
		Audio:       it.Audio,
		Video:       it.Video,
		Location:    it.Location,
		Permissions: make([]string, 0, len(it.LibreGraphPermissionsActionsAllowedValues)),
	}

	if out.MIMEType == "" && !out.Folder {
		out.MIMEType = "application/octet-stream"
	}

	if !out.Folder {
		out.Size = it.GetSize()
	}

	for _, a := range it.LibreGraphPermissionsActionsAllowedValues {
		out.Permissions = append(out.Permissions, strings.TrimPrefix(a, "libre.graph/driveItem/"))
	}

	ref := it.GetParentReference()
	out.Space = ref.GetDriveId()

	if out.Space == "" {
		// the top of a space has no parent, its id is the space's
		out.Space, _, _ = strings.Cut(out.ID, "!")
	}

	if i := slices.IndexFunc(spaces, func(s Space) bool { return s.ID == out.Space }); i >= 0 {
		out.SpaceName = spaces[i].Name
	}

	// the top of a space is named "."
	if ref.Path != nil && it.Root == nil {
		out.Path = strings.TrimPrefix(path.Join(ref.GetPath(), it.GetName()), "/")
	}

	return out
}

// MakeFolder creates the folder p in the space with the folders above it, by WebDAV MKCOL; a
// folder that is there already is done.
func (c *Client) MakeFolder(ctx context.Context, sp Space, p string) error {
	p, err := filePath(p)
	if err != nil {
		return err
	}

	if err := c.dav(sp).MkdirAll("/"+p, 0o755); err != nil {
		return c.explain(err, sp, p)
	}

	// gowebdav takes a MKCOL refused for anything at the path as the folder being there
	it, err := c.item(ctx, sp, p, "")
	if err != nil {
		return err
	}

	if it.Folder == nil {
		return fail(ErrNotAFolder, "%q is a file in the space %s, not a folder", p, sp.Name)
	}

	return nil
}

// Move moves or renames the file or folder p of the space to the path to in the same space, by
// WebDAV MOVE, creating the folders above to; overwrite replaces what is there. OpenCloud 9.0.0
// moves nothing from one space to another.
func (c *Client) Move(ctx context.Context, sp Space, p, to string, overwrite bool) error {
	return c.transfer(ctx, "MOVE", sp, p, sp, to, overwrite)
}

// Copy copies the file or folder p of the space to the path to in the space dst, by WebDAV COPY,
// creating the folders above to; overwrite replaces what is there.
func (c *Client) Copy(ctx context.Context, sp Space, p string, dst Space, to string, overwrite bool) error {
	return c.transfer(ctx, "COPY", sp, p, dst, to, overwrite)
}

func (c *Client) transfer(ctx context.Context, method string, sp Space, p string, dst Space, to string, overwrite bool) error {
	p, err := filePath(p)
	if err != nil {
		return err
	}

	if to, err = filePath(to); err != nil {
		return err
	}

	if dir := path.Dir(to); dir != "." {
		if err := c.dav(dst).MkdirAll("/"+dir, 0o755); err != nil {
			return c.explain(err, dst, dir)
		}
	}

	flag := "F"
	if overwrite {
		flag = "T"
	}

	resp, err := c.davDo(ctx, method, c.spaceURL(sp)+"/"+escapePath(p), map[string]string{
		destination: c.spaceURL(dst) + "/" + escapePath(to),
		"Overwrite": flag,
		depth:       "infinity",
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fail(ErrNotFound, "%q is not in the space %s", p, sp.Name)
	case http.StatusPreconditionFailed:
		return fail(ErrExists, "%q is in the space %s already; overwrite replaces it", to, dst.Name)
	case http.StatusForbidden:
		return fail(ErrDenied, "%s may not %s %s to %s in the space %s: %s", c.who, strings.ToLower(method), p, to, dst.Name, refusal(resp.Body))
	}

	return fmt.Errorf("OpenCloud refused to %s %s: %s", strings.ToLower(method), p, refusal(resp.Body))
}

// Delete deletes the file or folder p of the space into the space's trash, by WebDAV DELETE.
func (c *Client) Delete(ctx context.Context, sp Space, p string) error {
	p, err := filePath(p)
	if err != nil {
		return err
	}

	resp, err := c.davDo(ctx, http.MethodDelete, c.spaceURL(sp)+"/"+escapePath(p), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fail(ErrNotFound, "%q is not in the space %s", p, sp.Name)
	case http.StatusForbidden:
		return fail(ErrDenied, "%s may not delete %s in the space %s: %s", c.who, p, sp.Name, refusal(resp.Body))
	}

	return fmt.Errorf("OpenCloud refused to delete %s: %s", p, refusal(resp.Body))
}

// MaxPreview bounds how many bytes a preview takes.
const MaxPreview = 8 << 20

// Preview is an image of the file p of the space that fits a box of size pixels, as OpenCloud
// renders it for images and documents, and its MIME type; by WebDAV GET ?preview=1.
func (c *Client) Preview(ctx context.Context, sp Space, p string, size int) ([]byte, string, error) {
	p, err := filePath(p)
	if err != nil {
		return nil, "", err
	}

	// fit keeps the whole image, the thumbnail processor the web UI asks for crops it to the box
	q := url.Values{"preview": {"1"}, "scalingup": {"0"}, "processor": {"fit"}, "x": {strconv.Itoa(size)}, "y": {strconv.Itoa(size)}}

	resp, err := c.davDo(ctx, http.MethodGet, c.spaceURL(sp)+"/"+escapePath(p)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	// OpenCloud answers a file it makes no preview of as one that is not there
	case http.StatusNotFound:
		it, err := c.item(ctx, sp, p, "")
		if err != nil {
			return nil, "", err
		}

		file := it.GetFile()

		return nil, "", fail(ErrNoPreview, "OpenCloud makes no preview of %s (%s)", p, file.GetMimeType())
	default:
		return nil, "", fmt.Errorf("the preview of %s: %s", p, refusal(resp.Body))
	}

	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(kind, "image/") {
		return nil, "", fmt.Errorf("the preview of %s is no image but %q", p, resp.Header.Get("Content-Type"))
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxPreview+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading the preview of %s: %w", p, err)
	}

	if len(b) > MaxPreview {
		return nil, "", fail(ErrTooLarge, "the preview of %s is larger than %d MiB", p, MaxPreview>>20)
	}

	return b, kind, nil
}

// davDo sends a WebDAV request; a refused sign-in is ErrRefused.
func (c *Client) davDo(ctx context.Context, method, at string, header map[string]string) (*http.Response, error) {
	return c.davSend(ctx, method, at, nil, header)
}

// davSend sends a WebDAV request with a body; a refused sign-in is ErrRefused.
func (c *Client) davSend(ctx context.Context, method, at string, body io.Reader, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, at, body)
	if err != nil {
		return nil, err
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()

		return nil, c.refused()
	}

	return resp, nil
}

// the WebDAV headers a request names its target and its reach by
const (
	destination = "Destination"
	depth       = "Depth"
)

func escapePath(p string) string { return (&url.URL{Path: p}).EscapedPath() }

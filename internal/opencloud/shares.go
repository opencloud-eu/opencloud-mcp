package opencloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// Received is a file or folder others share with the user.
type Received struct {
	// ID is the share's own id in the user's Shares space.
	ID     string `json:"id"`
	Name   string `json:"name"`
	ItemID string `json:"itemId"`
	Folder bool   `json:"folder,omitempty"`
	By     string `json:"by,omitempty"`
	Role   string `json:"role,omitempty"`
	// Accepted is a share that shows up among the user's shares and spaces.
	Accepted bool `json:"accepted"`
	Hidden   bool `json:"hidden,omitempty"`

	jail string
}

// Outgoing is a file or folder the user shares.
type Outgoing struct {
	Space     string  `json:"space"`
	SpaceName string  `json:"spaceName,omitempty"`
	Path      string  `json:"path"`
	ID        string  `json:"id"`
	Folder    bool    `json:"folder,omitempty"`
	Grants    []Grant `json:"grants"`
}

// SharedWithMe are the files and folders others share with the user, by LibreGraph's GET
// /v1beta1/me/drive/sharedWithMe.
func (c *Client) SharedWithMe(ctx context.Context) ([]Received, error) {
	var page struct {
		Value []libregraph.DriveItem `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1beta1/me/drive/sharedWithMe", nil, &page); err != nil {
		return nil, graphFail("what is shared with "+c.who, err)
	}

	roles, err := c.Roles(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Received, 0, len(page.Value))

	for _, it := range page.Value {
		remote := it.GetRemoteItem()
		parent := it.GetParentReference()
		r := Received{
			ID: it.GetId(), Name: it.GetName(), ItemID: remote.GetId(), Folder: it.Folder != nil || remote.Folder != nil,
			Accepted: it.GetClientSynchronize(), Hidden: it.GetUIHidden(), jail: parent.GetDriveId(),
		}

		if by := remote.GetCreatedBy(); by.User != nil {
			r.By = by.User.GetDisplayName()
		}

		if perms := remote.Permissions; len(perms) > 0 {
			r.Role = grant(perms[0], roles).Role
		}

		out = append(out, r)
	}

	return out, nil
}

// SharedByMe are the files and folders the user shares, each with its shares and links, by
// LibreGraph's GET /v1beta1/me/drive/sharedByMe.
func (c *Client) SharedByMe(ctx context.Context) ([]Outgoing, error) {
	var page struct {
		Value []libregraph.DriveItem `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1beta1/me/drive/sharedByMe", nil, &page); err != nil {
		return nil, graphFail("what "+c.who+" shares", err)
	}

	roles, err := c.Roles(ctx)
	if err != nil {
		return nil, err
	}

	spaces, err := c.Spaces(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Outgoing, 0, len(page.Value))

	for _, it := range page.Value {
		parent := it.GetParentReference()
		o := Outgoing{Space: storage(parent.GetDriveId()), ID: it.GetId(), Folder: it.Folder != nil, Grants: make([]Grant, 0, len(it.Permissions))}

		if parent.Path != nil {
			o.Path = strings.TrimPrefix(path.Join(parent.GetPath(), it.GetName()), "/")
		}

		if i := slices.IndexFunc(spaces, func(s Space) bool { return s.ID == o.Space }); i >= 0 {
			o.SpaceName = spaces[i].Name
		}

		for _, perm := range it.Permissions {
			o.Grants = append(o.Grants, grant(perm, roles))
		}

		out = append(out, o)
	}

	return out, nil
}

// Accept takes the share ref names among the user's shares, by LibreGraph's POST
// /v1beta1/drives/{shares}/root/children; one that is accepted already is done.
func (c *Client) Accept(ctx context.Context, ref string) (Received, error) {
	r, err := c.received(ctx, ref)
	if err != nil || r.Accepted {
		return r, err
	}

	body := map[string]any{"remoteItem": map[string]string{"id": r.ItemID}}

	if _, err := c.graphDo(ctx, http.MethodPost, c.base+"/graph/v1beta1/drives/"+url.PathEscape(r.jail)+"/root/children", body, nil); err != nil {
		return Received{}, graphFail("accepting "+r.Name, err)
	}

	r.Accepted = true

	return r, nil
}

// Decline takes the share ref names out of the user's shares, by LibreGraph's DELETE
// /v1beta1/drives/{shares}/items/{id}; one that is not accepted is done.
func (c *Client) Decline(ctx context.Context, ref string) (Received, error) {
	r, err := c.received(ctx, ref)
	if err != nil || !r.Accepted {
		return r, err
	}

	if _, err := c.graphDo(ctx, http.MethodDelete, c.shareItem(r), nil, nil); err != nil {
		return Received{}, graphFail("declining "+r.Name, err)
	}

	r.Accepted = false

	return r, nil
}

// Hide hides the share ref names from the user's list of shares, or shows it again, by
// LibreGraph's PATCH /v1beta1/drives/{shares}/items/{id}.
func (c *Client) Hide(ctx context.Context, ref string, hidden bool) (Received, error) {
	r, err := c.received(ctx, ref)
	if err != nil {
		return Received{}, err
	}

	if _, err := c.graphDo(ctx, http.MethodPatch, c.shareItem(r), map[string]bool{"@UI.Hidden": hidden}, nil); err != nil {
		return Received{}, graphFail("hiding "+r.Name, err)
	}

	r.Hidden = hidden

	return r, nil
}

// received is the share ref names among SharedWithMe: by its id or the shared item's id, else
// by its name in any case.
func (c *Client) received(ctx context.Context, ref string) (Received, error) {
	ref = strings.TrimSpace(ref)

	all, err := c.SharedWithMe(ctx)
	if err != nil {
		return Received{}, err
	}

	if i := slices.IndexFunc(all, func(r Received) bool { return ref != "" && (r.ID == ref || r.ItemID == ref) }); i >= 0 {
		return all[i], nil
	}

	var named []Received

	for _, r := range all {
		if strings.EqualFold(r.Name, ref) {
			named = append(named, r)
		}
	}

	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return Received{}, fail(ErrNotFound, "nothing named %q is shared with %s", ref, c.who)
	}

	ids := make([]string, len(named))
	for i, r := range named {
		ids[i] = fmt.Sprintf("%s (by %s)", r.ID, r.By)
	}

	return Received{}, fail(ErrNotFound, "%d shares are named %q; name one by its id: %s", len(named), ref, strings.Join(ids, ", "))
}

func (c *Client) shareItem(r Received) string {
	return c.base + "/graph/v1beta1/drives/" + url.PathEscape(r.jail) + "/items/" + url.PathEscape(r.ID)
}

package opencloud

import (
	"context"
	"net/http"
	"path"
	"strings"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// SpaceInfo is a space with its quota, readme and image.
type SpaceInfo struct {
	Space
	Quota *Quota `json:"quota,omitempty"`
	// Readme and Image are the paths in the space of the files it shows as its readme and picture.
	Readme string `json:"readme,omitempty"`
	Image  string `json:"image,omitempty"`
	Link   string `json:"link,omitempty"`
}

// Quota is how much a space may hold and holds, in bytes; Total is 0 without a limit.
type Quota struct {
	Total     int64  `json:"total"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining,omitempty"`
	State     string `json:"state,omitempty"`
}

// SpaceChange is what UpdateSpace changes; a nil field stays as it is.
type SpaceChange struct {
	Name, Description *string
	// Readme and Image are paths of files in the space.
	Readme, Image *string
	Quota         *int64
}

// SpaceInfo is the space with its quota, readme and image, by LibreGraph's GET /v1.0/drives/{id}.
func (c *Client) SpaceInfo(ctx context.Context, sp Space) (SpaceInfo, error) {
	var d libregraph.Drive

	if _, err := c.graphDo(ctx, http.MethodGet, c.drive(sp), nil, &d); err != nil {
		return SpaceInfo{}, graphFail("the space "+sp.Name, err)
	}

	return info(d, sp), nil
}

// CreateSpace makes the project space name with its description and its quota in bytes, the
// instance's default when 0, by LibreGraph's POST /v1.0/drives; the user becomes its manager.
func (c *Client) CreateSpace(ctx context.Context, name, description string, quota int64) (SpaceInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SpaceInfo{}, fail(ErrNotFound, "a space needs a name")
	}

	d := libregraph.Drive{Name: name, DriveType: new("project")}
	if description != "" {
		d.Description = &description
	}

	if quota > 0 {
		d.Quota = &libregraph.Quota{Total: &quota}
	}

	var made libregraph.Drive

	if _, err := c.graphDo(ctx, http.MethodPost, c.base+"/graph/v1.0/drives", d, &made); err != nil {
		return SpaceInfo{}, graphFail("creating the space "+name, err)
	}

	return info(made, Space{}), nil
}

// UpdateSpace changes the space, by LibreGraph's PATCH /v1.0/drives/{id}; a readme or image is a
// file of the space.
func (c *Client) UpdateSpace(ctx context.Context, sp Space, ch SpaceChange) (SpaceInfo, error) {
	patch := map[string]any{}

	if ch.Name != nil {
		patch["name"] = *ch.Name
	}

	if ch.Description != nil {
		patch["description"] = *ch.Description
	}

	if ch.Quota != nil {
		patch["quota"] = map[string]int64{"total": *ch.Quota}
	}

	var special []map[string]any

	for _, f := range []struct {
		name string
		p    *string
	}{{"readme", ch.Readme}, {"image", ch.Image}} {
		if f.p == nil {
			continue
		}

		p, err := filePath(*f.p)
		if err != nil {
			return SpaceInfo{}, err
		}

		it, err := c.item(ctx, sp, p, "")
		if err != nil {
			return SpaceInfo{}, err
		}

		if it.Folder != nil {
			return SpaceInfo{}, fail(ErrNotAFile, "%q is a folder in the space %s; the %s is a file", p, sp.Name, f.name)
		}

		special = append(special, map[string]any{"specialFolder": map[string]string{"name": f.name}, "id": it.GetId()})
	}

	if special != nil {
		patch["special"] = special
	}

	if len(patch) == 0 {
		return SpaceInfo{}, fail(ErrNotFound, "nothing to change is given")
	}

	var d libregraph.Drive

	if _, err := c.graphDo(ctx, http.MethodPatch, c.drive(sp), patch, &d); err != nil {
		return SpaceInfo{}, graphFail("changing the space "+sp.Name, err)
	}

	return info(d, sp), nil
}

// DisableSpace disables the project space, by LibreGraph's DELETE /v1.0/drives/{id}: its members
// lose access, its content stays until DeleteSpace.
func (c *Client) DisableSpace(ctx context.Context, sp Space) error {
	if _, err := c.graphDo(ctx, http.MethodDelete, c.drive(sp), nil, nil); err != nil {
		return graphFail("disabling the space "+sp.Name, err)
	}

	return nil
}

// RestoreSpace enables the disabled project space again, by LibreGraph's PATCH /v1.0/drives/{id}
// with Restore; a space that is not disabled is done.
func (c *Client) RestoreSpace(ctx context.Context, sp Space) error {
	// OpenCloud 9.0.0 answers the restore of an enabled space with 500
	if !sp.Disabled {
		return nil
	}

	if _, err := c.graphSend(ctx, http.MethodPatch, c.drive(sp), map[string]string{"Restore": "T"}, map[string]any{}, nil); err != nil {
		return graphFail("restoring the space "+sp.Name, err)
	}

	return nil
}

// DeleteSpace deletes the disabled project space for good with all its content, by LibreGraph's
// DELETE /v1.0/drives/{id} with Purge.
func (c *Client) DeleteSpace(ctx context.Context, sp Space) error {
	if !sp.Disabled {
		return fail(ErrDenied, "the space %s is not disabled; disable_space comes first", sp.Name)
	}

	if _, err := c.graphSend(ctx, http.MethodDelete, c.drive(sp), map[string]string{"Purge": "T"}, nil, nil); err != nil {
		return graphFail("deleting the space "+sp.Name, err)
	}

	return nil
}

// info is the drive as a SpaceInfo; sp fills in what the drive leaves out.
func info(d libregraph.Drive, sp Space) SpaceInfo {
	root := d.GetRoot()
	deleted := root.GetDeleted()
	out := SpaceInfo{
		Space: Space{
			ID: d.GetId(), Name: d.GetName(), Type: d.GetDriveType(), Description: d.GetDescription(), ETag: root.GetETag(),
			Disabled: deleted.GetState() == "trashed",
		},
		Link: d.GetWebUrl(),
	}

	if out.ID == "" {
		out.Space = sp
	}

	if q := d.Quota; q != nil {
		out.Quota = &Quota{Total: q.GetTotal(), Used: q.GetUsed(), Remaining: q.GetRemaining(), State: q.GetState()}
	}

	for _, s := range d.Special {
		ref := s.GetParentReference()
		p := strings.TrimPrefix(path.Join(ref.GetPath(), s.GetName()), "/")

		switch folder := s.GetSpecialFolder(); folder.GetName() {
		case "readme":
			out.Readme = p
		case "image":
			out.Image = p
		}
	}

	return out
}

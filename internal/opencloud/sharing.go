package opencloud

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// Role is a role a share or a member of a space can have.
type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// On names what it is for: file, folder or space.
	On string `json:"on,omitempty"`
}

// Grant is a share with a user or group, or a link.
type Grant struct {
	ID string `json:"id"`
	// To is who it is shared with; empty for a link.
	To *Person `json:"to,omitempty"`
	// Role is the role's name, RoleID its id; a link has none.
	Role   string `json:"role,omitempty"`
	RoleID string `json:"roleId,omitempty"`
	Link   *Link  `json:"link,omitempty"`
	// Expires is when it ends; never when zero.
	Expires time.Time `json:"expires,omitzero"`
}

// Link is a link to a file, folder or space.
type Link struct {
	// Type is its kind of access: view, edit, upload, createOnly, blocksDownload or internal.
	Type     string `json:"type"`
	URL      string `json:"url"`
	Name     string `json:"name,omitempty"`
	Password bool   `json:"password,omitempty"`
}

// Permissions are the shares and links of a file, folder or space, and the roles it can be shared
// with.
type Permissions struct {
	Grants []Grant `json:"grants"`
	Roles  []Role  `json:"roles"`
}

// Change is what UpdatePermission changes; a nil field stays as it is.
type Change struct {
	Role, Type, Name *string
	// Expires zero ends the expiry.
	Expires *time.Time
}

// Roles are the roles OpenCloud has for shares and members of spaces, by LibreGraph's GET
// /v1beta1/roleManagement/permissions/roleDefinitions.
func (c *Client) Roles(ctx context.Context) ([]Role, error) {
	var defs []libregraph.UnifiedRoleDefinition

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1beta1/roleManagement/permissions/roleDefinitions", nil, &defs); err != nil {
		return nil, graphFail("the sharing roles", err)
	}

	out := make([]Role, 0, len(defs))

	for _, d := range defs {
		r := Role{ID: d.GetId(), Name: d.GetDisplayName(), Description: d.GetDescription()}

		for _, p := range d.RolePermissions {
			switch cond := p.GetCondition(); {
			case strings.Contains(cond, "@Resource.Root"):
				r.On = "space"
			case strings.Contains(cond, "@Resource.Folder"):
				r.On = "folder"
			case strings.Contains(cond, "@Resource.File"):
				r.On = "file"
			}
		}

		out = append(out, r)
	}

	return out, nil
}

// Permissions are the shares and links of the file or folder p of the space, of the space itself
// when p is empty, by LibreGraph's GET .../permissions.
func (c *Client) Permissions(ctx context.Context, sp Space, p string) (Permissions, error) {
	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return Permissions{}, err
	}

	var page libregraph.CollectionOfPermissionsWithAllowedValues

	if _, err := c.graphDo(ctx, http.MethodGet, at+"/permissions", nil, &page); err != nil {
		return Permissions{}, graphFail("the shares of "+where(sp, p), err)
	}

	all, err := c.Roles(ctx)
	if err != nil {
		return Permissions{}, err
	}

	out := Permissions{Grants: make([]Grant, 0, len(page.Value)), Roles: make([]Role, 0, len(page.LibreGraphPermissionsRolesAllowedValues))}

	for _, perm := range page.Value {
		out.Grants = append(out.Grants, grant(perm, all))
	}

	for _, d := range page.LibreGraphPermissionsRolesAllowedValues {
		out.Roles = append(out.Roles, Role{ID: d.GetId(), Name: d.GetDisplayName(), Description: d.GetDescription()})
	}

	return out, nil
}

// Share shares the file or folder p of the space, the space itself when p is empty, with the
// recipients in the role, until expires unless it is zero, by LibreGraph's POST .../invite.
func (c *Client) Share(ctx context.Context, sp Space, p string, to []Person, role string, expires time.Time) ([]Grant, error) {
	if len(to) == 0 {
		return nil, fail(ErrNotFound, "no user or group is given to share with")
	}

	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return nil, err
	}

	r, err := c.role(ctx, sp, p, role)
	if err != nil {
		return nil, err
	}

	invite := libregraph.DriveItemInvite{Roles: []string{r.ID}}
	for _, person := range to {
		invite.Recipients = append(invite.Recipients, libregraph.DriveRecipient{ObjectId: new(person.ID), LibreGraphRecipientType: new(person.Kind)})
	}

	if !expires.IsZero() {
		invite.ExpirationDateTime = &expires
	}

	var page struct {
		Value []libregraph.Permission `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodPost, at+"/invite", invite, &page); err != nil {
		return nil, graphFail("sharing "+where(sp, p), err)
	}

	out := make([]Grant, 0, len(page.Value))
	for _, perm := range page.Value {
		out = append(out, grant(perm, []Role{r}))
	}

	return out, nil
}

// CreateLink makes a link of the kind to the file or folder p of the space, to the space itself
// when p is empty, by LibreGraph's POST .../createLink.
func (c *Client) CreateLink(ctx context.Context, sp Space, p, kind, password, name string, expires time.Time) (Grant, error) {
	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return Grant{}, err
	}

	link := libregraph.DriveItemCreateLink{Type: new(libregraph.SharingLinkType(cmp.Or(kind, "view")))}
	if password != "" {
		link.Password = &password
	}

	if name != "" {
		link.DisplayName = &name
	}

	if !expires.IsZero() {
		link.ExpirationDateTime = &expires
	}

	var perm libregraph.Permission

	if _, err := c.graphDo(ctx, http.MethodPost, at+"/createLink", link, &perm); err != nil {
		return Grant{}, graphFail("the link to "+where(sp, p), err)
	}

	return grant(perm, nil), nil
}

// UpdatePermission changes the share or link id of the file or folder p of the space, of the
// space itself when p is empty, by LibreGraph's PATCH .../permissions/{id}.
func (c *Client) UpdatePermission(ctx context.Context, sp Space, p, id string, ch Change) (Grant, error) {
	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return Grant{}, err
	}

	patch := map[string]any{}

	if ch.Role != nil {
		r, err := c.role(ctx, sp, p, *ch.Role)
		if err != nil {
			return Grant{}, err
		}

		patch["roles"] = []string{r.ID}
	}

	if ch.Type != nil || ch.Name != nil {
		link := map[string]any{}
		if ch.Type != nil {
			link["type"] = *ch.Type
		}

		if ch.Name != nil {
			link["@libre.graph.displayName"] = *ch.Name
		}

		patch["link"] = link
	}

	if ch.Expires != nil {
		patch["expirationDateTime"] = nil
		if !ch.Expires.IsZero() {
			patch["expirationDateTime"] = ch.Expires.UTC().Format(time.RFC3339)
		}
	}

	if len(patch) == 0 {
		return Grant{}, fail(ErrNotFound, "nothing to change is given")
	}

	all, err := c.Roles(ctx)
	if err != nil {
		return Grant{}, err
	}

	var perm libregraph.Permission

	if _, err := c.graphDo(ctx, http.MethodPatch, at+"/permissions/"+url.PathEscape(strings.TrimSpace(id)), patch, &perm); err != nil {
		return Grant{}, graphFail(fmt.Sprintf("the share %s of %s", id, where(sp, p)), err)
	}

	return grant(perm, all), nil
}

// SetLinkPassword sets the password of the link id of the file or folder p of the space, of the
// space itself when p is empty, by LibreGraph's POST .../permissions/{id}/setPassword.
func (c *Client) SetLinkPassword(ctx context.Context, sp Space, p, id, password string) error {
	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return err
	}

	pw := libregraph.SharingLinkPassword{Password: &password}

	if _, err := c.graphDo(ctx, http.MethodPost, at+"/permissions/"+url.PathEscape(strings.TrimSpace(id))+"/setPassword", pw, nil); err != nil {
		return graphFail(fmt.Sprintf("the password of the link %s of %s", id, where(sp, p)), err)
	}

	return nil
}

// RemovePermission stops the share or deletes the link id of the file or folder p of the space,
// of the space itself when p is empty, by LibreGraph's DELETE .../permissions/{id}.
func (c *Client) RemovePermission(ctx context.Context, sp Space, p, id string) error {
	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return err
	}

	if _, err := c.graphDo(ctx, http.MethodDelete, at+"/permissions/"+url.PathEscape(strings.TrimSpace(id)), nil, nil); err != nil {
		return graphFail(fmt.Sprintf("the share %s of %s", id, where(sp, p)), err)
	}

	return nil
}

// permissions is the LibreGraph address whose permissions are those of the file or folder p of
// the space, /v1beta1/drives/{id}/items/{id}, or of the space itself, /v1beta1/drives/{id}/root,
// and the clean path.
func (c *Client) permissions(ctx context.Context, sp Space, p string) (string, string, error) {
	p, err := Clean(p)
	if err != nil {
		return "", "", err
	}

	drive := c.base + "/graph/v1beta1/drives/" + url.PathEscape(sp.ID)
	if p == "" {
		return drive + "/root", "", nil
	}

	it, err := c.item(ctx, sp, p, "")
	if err != nil {
		return "", "", err
	}

	return drive + "/items/" + url.PathEscape(it.GetId()), p, nil
}

// role is the role ref names, by its id or its name in any case, among those the file or folder p
// of the space, or the space itself, can be shared with; names repeat across files, folders and
// spaces, so the name is only taken among these.
func (c *Client) role(ctx context.Context, sp Space, p, ref string) (Role, error) {
	at, p, err := c.permissions(ctx, sp, p)
	if err != nil {
		return Role{}, err
	}

	var page libregraph.CollectionOfPermissionsWithAllowedValues

	if _, err := c.graphDo(ctx, http.MethodGet, at+"/permissions", nil, &page); err != nil {
		return Role{}, graphFail("the roles of "+where(sp, p), err)
	}

	ref = strings.TrimSpace(ref)
	names := make([]string, 0, len(page.LibreGraphPermissionsRolesAllowedValues))

	for _, d := range page.LibreGraphPermissionsRolesAllowedValues {
		if d.GetId() == ref || strings.EqualFold(d.GetDisplayName(), ref) {
			return Role{ID: d.GetId(), Name: d.GetDisplayName(), Description: d.GetDescription()}, nil
		}

		names = append(names, d.GetDisplayName())
	}

	if len(names) == 0 {
		return Role{}, fail(ErrDenied, "%s may not share %s", c.who, where(sp, p))
	}

	return Role{}, fail(ErrNotFound, "%s has no role %q; its roles are: %s", where(sp, p), ref, strings.Join(names, ", "))
}

// grant is a LibreGraph permission as a Grant, its role named among roles.
func grant(perm libregraph.Permission, roles []Role) Grant {
	g := Grant{ID: perm.GetId()}

	if t, ok := perm.GetExpirationDateTimeOk(); ok && t != nil {
		g.Expires = t.UTC()
	}

	if to := perm.GrantedToV2; to != nil {
		switch {
		case to.User != nil:
			g.To = &Person{ID: to.User.GetId(), Name: to.User.GetDisplayName(), Kind: kindUser}
		case to.Group != nil:
			g.To = &Person{ID: to.Group.GetId(), Name: to.Group.GetDisplayName(), Kind: kindGroup}
		}
	}

	if len(perm.Roles) > 0 {
		g.RoleID = perm.Roles[0]
		g.Role = g.RoleID

		if i := slices.IndexFunc(roles, func(r Role) bool { return r.ID == g.RoleID }); i >= 0 {
			g.Role = roles[i].Name
		}
	}

	if l := perm.Link; l != nil {
		g.Link = &Link{Type: string(l.GetType()), URL: l.GetWebUrl(), Name: l.GetLibreGraphDisplayName(), Password: perm.GetHasPassword()}
	}

	return g
}

// where names the file or folder p of the space in a sentence, the space itself when p is empty.
func where(sp Space, p string) string {
	if p == "" {
		return "the space " + sp.Name
	}

	return fmt.Sprintf("%s in the space %s", path.Clean(p), sp.Name)
}

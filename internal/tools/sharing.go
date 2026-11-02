package tools

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) sharingTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:  "list_share_roles",
		Title: "List the sharing roles",
		Description: "List the roles a share or space member can have, like Can view or Can edit, " +
			"each with what it allows and its id.",
		Annotations: readOnly("List the sharing roles"),
	}, t.listShareRoles)

	add(t, s, &mcp.Tool{
		Name:  "list_permissions",
		Title: "List the shares of a file or folder",
		Description: "List who a file or folder is shared with and its links, each with its role and permission id, and the roles " +
			"it can still be shared with.",
		Annotations: readOnly("List the shares of a file or folder"),
	}, t.listPermissions)

	add(t, s, &mcp.Tool{
		Name:        "share_item",
		Title:       "Share a file or folder",
		Description: "Share a file or folder with users or groups, with a role and an expiry.",
		Annotations: adds("Share a file or folder"),
	}, t.shareItem)

	add(t, s, &mcp.Tool{
		Name:  "create_link",
		Title: "Create a public link",
		Description: "Create a public link to a file, a folder or a whole space, with its kind of access, a password and an expiry; " +
			"or an internal link, which only works for those who have access already.",
		Annotations: adds("Create a public link"),
	}, t.createLink)

	add(t, s, &mcp.Tool{
		Name:        "update_permission",
		Title:       "Update a share or link",
		Description: "Change the role, expiry or kind of access of a share or link.",
		Annotations: idempotent(changes("Update a share or link")),
	}, t.updatePermission)

	add(t, s, &mcp.Tool{
		Name:        "set_link_password",
		Title:       "Set the password of a link",
		Description: "Set or change the password of a link.",
		Annotations: idempotent(changes("Set the password of a link")),
	}, t.setLinkPassword)

	add(t, s, &mcp.Tool{
		Name:        "remove_permission",
		Title:       "Remove a share or link",
		Description: "Stop sharing a file or folder with someone, or delete a link.",
		Annotations: idempotent(changes("Remove a share or link")),
	}, t.removePermission)

	add(t, s, &mcp.Tool{
		Name:  "list_shared_with_me",
		Title: "List what is shared with me",
		Description: "List the files and folders others share with the user, " +
			"each with who shares it and whether it is accepted or hidden.",
		Annotations: readOnly("List what is shared with me"),
	}, t.listSharedWithMe)

	add(t, s, &mcp.Tool{
		Name:  "list_shared_by_me",
		Title: "List what I share",
		Description: "List the files and folders the user shares, each with whom and with its public links; or only the ones " +
			"shared with people, or only the links.",
		Annotations: readOnly("List what I share"),
	}, t.listSharedByMe)

	add(t, s, &mcp.Tool{
		Name:        "accept_share",
		Title:       "Accept a share",
		Description: "Accept a file or folder shared with the user, so it shows up in the user's shares.",
		Annotations: idempotent(adds("Accept a share")),
	}, t.acceptShare)

	add(t, s, &mcp.Tool{
		Name:        "decline_share",
		Title:       "Decline a share",
		Description: "Decline a file or folder shared with the user, or remove an accepted one from the user's shares.",
		Annotations: idempotent(changes("Decline a share")),
	}, t.declineShare)

	add(t, s, &mcp.Tool{
		Name:        "hide_share",
		Title:       "Hide a share",
		Description: "Hide a share from the user's list of shares, or show it again.",
		Annotations: idempotent(changes("Hide a share")),
	}, t.hideShare)
}

type (
	invitation struct {
		Space      string   `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path       string   `json:"path" jsonschema:"a file or folder of the space, like guides/restore.md"`
		Recipients []string `json:"recipients" jsonschema:"users or groups, by their names or ids as find_users or find_groups gives them"`
		Role       string   `json:"role" jsonschema:"the role, by its name or id as list_share_roles gives it, like Can edit"`
		Expires    string   `json:"expires,omitempty" jsonschema:"when it ends, in RFC 3339 like 2027-01-31T00:00:00Z; never when left out"`
	}

	link struct {
		Space    string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path     string `json:"path,omitempty" jsonschema:"a file or folder of the space, like guides; the whole space when left out"`
		Type     string `json:"type,omitempty" jsonschema:"view, edit, upload, createOnly, blocksDownload or internal; view when left out"`
		Password string `json:"password,omitempty" jsonschema:"a password the link asks for"`
		Name     string `json:"name,omitempty" jsonschema:"a name for the link, like For the auditors"`
		Expires  string `json:"expires,omitempty" jsonschema:"when it ends, in RFC 3339 like 2027-01-31T00:00:00Z; never when left out"`
	}

	grant struct {
		Space      string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path       string `json:"path,omitempty" jsonschema:"a file or folder of the space, like guides; the whole space when left out"`
		Permission string `json:"permission" jsonschema:"the share or link, by its permission id as list_permissions gives it"`
		Role       string `json:"role,omitempty" jsonschema:"the new role of a share, by its name or id as list_share_roles gives it"`
		Type       string `json:"type,omitempty" jsonschema:"the new kind of access of a link: view, edit, upload, createOnly, blocksDownload or internal"`
		Name       string `json:"name,omitempty" jsonschema:"the new name of a link"`
		Expires    string `json:"expires,omitempty" jsonschema:"when it ends, in RFC 3339 like 2027-01-31T00:00:00Z, or never; as it is when left out"`
	}

	password struct {
		Space      string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path       string `json:"path,omitempty" jsonschema:"a file or folder of the space, like guides; the whole space when left out"`
		Permission string `json:"permission" jsonschema:"the share or link, by its permission id as list_permissions gives it"`
		Password   string `json:"password" jsonschema:"the link's new password"`
	}

	permission struct {
		Space      string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Path       string `json:"path,omitempty" jsonschema:"a file or folder of the space, like guides; the whole space when left out"`
		Permission string `json:"permission" jsonschema:"the share or link, by its permission id as list_permissions gives it"`
	}

	outgoing struct {
		Only string `json:"only,omitempty" jsonschema:"people for the shares with users and groups, links for the public links; both when left out"`
	}

	share struct {
		Share string `json:"share" jsonschema:"the share, by its name or id as list_shared_with_me gives it"`
	}

	hiding struct {
		Share  string `json:"share" jsonschema:"the share, by its name or id as list_shared_with_me gives it"`
		Hidden bool   `json:"hidden" jsonschema:"true hides it, false shows it again"`
	}
)

type (
	roles struct {
		Roles []opencloud.Role `json:"roles"`
	}

	granted struct {
		Grants []opencloud.Grant `json:"grants"`
	}

	received struct {
		Shares []opencloud.Received `json:"shares"`
	}

	outgoings struct {
		Shared []opencloud.Outgoing `json:"shared"`
	}
)

func (t *tools) listShareRoles(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, roles, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, roles{}, err
	}

	all, err := c.Roles(ctx)
	if err != nil {
		return nil, roles{}, err
	}

	var b strings.Builder

	for _, r := range all {
		fmt.Fprintf(&b, "%s, for a %s: %s (id %s)\n", r.Name, r.On, r.Description, r.ID)
	}

	return text(b.String()), roles{Roles: all}, nil
}

func (t *tools) listPermissions(ctx context.Context, req *mcp.CallToolRequest, in item) (*mcp.CallToolResult, opencloud.Permissions, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, opencloud.Permissions{}, err
	}

	perms, err := c.Permissions(ctx, sp, p)
	if err != nil {
		return nil, opencloud.Permissions{}, err
	}

	return text(permissions(perms, "It is shared with nobody and has no links.")), perms, nil
}

// permissions are the shares and links and the roles at hand in lines a model reads, or none.
func permissions(perms opencloud.Permissions, none string) string {
	var b strings.Builder

	for _, g := range perms.Grants {
		b.WriteString(grantLine(g) + "\n")
	}

	if len(perms.Grants) == 0 {
		b.WriteString(none + "\n")
	}

	if len(perms.Roles) > 0 {
		names := make([]string, len(perms.Roles))
		for i, r := range perms.Roles {
			names[i] = r.Name
		}

		fmt.Fprintf(&b, "roles to share with: %s\n", strings.Join(names, ", "))
	}

	return b.String()
}

// grantLine is a share or link in a line a model reads.
func grantLine(g opencloud.Grant) string {
	var b strings.Builder

	switch {
	case g.Link != nil:
		article := "a"
		if g.Link.Type != "" && strings.ContainsRune("aeiou", rune(g.Link.Type[0])) {
			article = "an"
		}

		fmt.Fprintf(&b, "%s %s link %s", article, g.Link.Type, g.Link.URL)

		if g.Link.Name != "" {
			fmt.Fprintf(&b, " named %q", g.Link.Name)
		}

		if g.Link.Password {
			b.WriteString(", with a password")
		}
	case g.To != nil:
		fmt.Fprintf(&b, "%s (%s, id %s): %s", g.To.Name, g.To.Kind, g.To.ID, g.Role)
	default:
		b.WriteString(g.Role)
	}

	if !g.Expires.IsZero() {
		fmt.Fprintf(&b, ", until %s", g.Expires.Format(time.RFC3339))
	}

	fmt.Fprintf(&b, ", permission %s", g.ID)

	return b.String()
}

func (t *tools) shareItem(ctx context.Context, req *mcp.CallToolRequest, in invitation) (*mcp.CallToolResult, granted, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, granted{}, err
	}

	if p == "" {
		return nil, granted{}, fmt.Errorf("name a file or folder of %s to share; add_space_member gives access to the whole space", sp.Name)
	}

	return t.invite(ctx, c, sp, p, in.Recipients, in.Role, in.Expires)
}

// invite shares the file or folder p of the space, or the space itself when p is empty.
func (t *tools) invite(ctx context.Context, c *opencloud.Client, sp opencloud.Space, p string, recipients []string, role, expires string) (*mcp.CallToolResult, granted, error) {
	until, err := moment(expires)
	if err != nil {
		return nil, granted{}, err
	}

	to := make([]opencloud.Person, 0, len(recipients))

	for _, ref := range recipients {
		person, err := c.Recipient(ctx, ref)
		if err != nil {
			return nil, granted{}, err
		}

		to = append(to, person)
	}

	grants, err := c.Share(ctx, sp, p, to, role, until)
	if err != nil {
		return nil, granted{}, err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Shared %s with:\n", where(sp, p))

	for _, g := range grants {
		b.WriteString(grantLine(g) + "\n")
	}

	return text(b.String()), granted{Grants: grants}, nil
}

func (t *tools) createLink(ctx context.Context, req *mcp.CallToolRequest, in link) (*mcp.CallToolResult, opencloud.Grant, error) {
	until, err := moment(in.Expires)
	if err != nil {
		return nil, opencloud.Grant{}, err
	}

	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, opencloud.Grant{}, err
	}

	g, err := c.CreateLink(ctx, sp, p, in.Type, in.Password, in.Name, until)
	if err != nil {
		return nil, opencloud.Grant{}, err
	}

	return text(fmt.Sprintf("Made %s to %s.", grantLine(g), where(sp, p))), g, nil
}

func (t *tools) updatePermission(ctx context.Context, req *mcp.CallToolRequest, in grant) (*mcp.CallToolResult, opencloud.Grant, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, opencloud.Grant{}, err
	}

	return t.update(ctx, c, sp, p, in.Permission, in.Role, in.Type, in.Name, in.Expires)
}

// update changes a share or link of the file or folder p of the space, or of the space itself;
// a value left empty stays as it is.
func (t *tools) update(ctx context.Context, c *opencloud.Client, sp opencloud.Space, p, id, role, kind, name, expires string) (*mcp.CallToolResult, opencloud.Grant, error) {
	var ch opencloud.Change

	for _, f := range []struct {
		value string
		to    **string
	}{{role, &ch.Role}, {kind, &ch.Type}, {name, &ch.Name}} {
		if f.value != "" {
			*f.to = &f.value
		}
	}

	if expires != "" {
		var until time.Time

		if !strings.EqualFold(expires, "never") {
			var err error
			if until, err = moment(expires); err != nil {
				return nil, opencloud.Grant{}, err
			}
		}

		ch.Expires = &until
	}

	g, err := c.UpdatePermission(ctx, sp, p, id, ch)
	if err != nil {
		return nil, opencloud.Grant{}, err
	}

	return text(fmt.Sprintf("Changed: %s, of %s.", grantLine(g), where(sp, p))), g, nil
}

func (t *tools) setLinkPassword(ctx context.Context, req *mcp.CallToolRequest, in password) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	if err := c.SetLinkPassword(ctx, sp, p, in.Permission, in.Password); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("The link %s of %s has the new password.", in.Permission, where(sp, p))), nil, nil
}

func (t *tools) removePermission(ctx context.Context, req *mcp.CallToolRequest, in permission) (*mcp.CallToolResult, any, error) {
	c, sp, p, err := t.at(ctx, req, in.Space, in.Path)
	if err != nil {
		return nil, nil, err
	}

	if err := c.RemovePermission(ctx, sp, p, in.Permission); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Removed %s of %s.", in.Permission, where(sp, p))), nil, nil
}

func (t *tools) listSharedWithMe(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, received, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, received{}, err
	}

	all, err := c.SharedWithMe(ctx)
	if err != nil {
		return nil, received{}, err
	}

	var b strings.Builder

	for _, r := range all {
		b.WriteString(receivedLine(r) + "\n")
	}

	if len(all) == 0 {
		b.WriteString("Nothing is shared with the user.")
	}

	return text(b.String()), received{Shares: all}, nil
}

// receivedLine is a share the user received in a line a model reads.
func receivedLine(r opencloud.Received) string {
	var b strings.Builder

	b.WriteString(r.Name)

	if r.Folder {
		b.WriteString("/")
	}

	if r.By != "" {
		fmt.Fprintf(&b, " from %s", r.By)
	}

	if r.Role != "" {
		fmt.Fprintf(&b, ", %s", r.Role)
	}

	if r.Accepted {
		b.WriteString(", accepted")
	} else {
		b.WriteString(", not accepted")
	}

	if r.Hidden {
		b.WriteString(", hidden")
	}

	fmt.Fprintf(&b, ", share %s", r.ID)

	return b.String()
}

func (t *tools) listSharedByMe(ctx context.Context, req *mcp.CallToolRequest, in outgoing) (*mcp.CallToolResult, outgoings, error) {
	only := strings.ToLower(strings.TrimSpace(in.Only))
	if only != "" && only != "people" && only != "links" {
		return nil, outgoings{}, fmt.Errorf("only is people or links, not %q", in.Only)
	}

	c, err := t.client(ctx, req)
	if err != nil {
		return nil, outgoings{}, err
	}

	all, err := c.SharedByMe(ctx)
	if err != nil {
		return nil, outgoings{}, err
	}

	out := make([]opencloud.Outgoing, 0, len(all))

	var b strings.Builder

	for _, o := range all {
		kept := o.Grants[:0:0]

		for _, g := range o.Grants {
			if only == "" || (only == "links") == (g.Link != nil) {
				kept = append(kept, g)
			}
		}

		if len(kept) == 0 {
			continue
		}

		o.Grants = kept
		out = append(out, o)

		fmt.Fprintf(&b, "%s in %s:\n", cmp.Or(o.Path, "the top"), cmp.Or(o.SpaceName, o.Space))

		for _, g := range kept {
			b.WriteString("  " + grantLine(g) + "\n")
		}
	}

	if len(out) == 0 {
		b.WriteString("The user shares nothing.")
	}

	return text(b.String()), outgoings{Shared: out}, nil
}

func (t *tools) acceptShare(ctx context.Context, req *mcp.CallToolRequest, in share) (*mcp.CallToolResult, opencloud.Received, error) {
	return t.received(ctx, req, func(c *opencloud.Client) (opencloud.Received, error) { return c.Accept(ctx, in.Share) })
}

func (t *tools) declineShare(ctx context.Context, req *mcp.CallToolRequest, in share) (*mcp.CallToolResult, opencloud.Received, error) {
	return t.received(ctx, req, func(c *opencloud.Client) (opencloud.Received, error) { return c.Decline(ctx, in.Share) })
}

func (t *tools) hideShare(ctx context.Context, req *mcp.CallToolRequest, in hiding) (*mcp.CallToolResult, opencloud.Received, error) {
	return t.received(ctx, req, func(c *opencloud.Client) (opencloud.Received, error) { return c.Hide(ctx, in.Share, in.Hidden) })
}

// received does something to a share the user received and says how it is now.
func (t *tools) received(ctx context.Context, req *mcp.CallToolRequest, do func(*opencloud.Client) (opencloud.Received, error)) (*mcp.CallToolResult, opencloud.Received, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, opencloud.Received{}, err
	}

	r, err := do(c)
	if err != nil {
		return nil, opencloud.Received{}, err
	}

	return text(receivedLine(r) + "."), r, nil
}

// where names the file or folder p of the space, the space itself when p is empty.
func where(sp opencloud.Space, p string) string {
	if p == "" {
		return sp.Name
	}

	return p + " in " + sp.Name
}

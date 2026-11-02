package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

func (t *tools) spaceTools(s *mcp.Server) {
	add(t, s, &mcp.Tool{
		Name:        "list_spaces",
		Title:       "List the spaces",
		Description: "List the OpenCloud spaces the user sees: the personal space, project spaces and shares, each with its name and id.",
		Annotations: readOnly("List the spaces"),
	}, t.listSpaces)

	add(t, s, &mcp.Tool{
		Name:        "get_space",
		Title:       "Get a space",
		Description: "Get a space with its description, quota and use, and the paths of its readme and image.",
		Annotations: readOnly("Get a space"),
	}, t.getSpace)

	add(t, s, &mcp.Tool{
		Name:  "create_space",
		Title: "Create a space",
		Description: "Create a project space with a name, a description and a quota; the user becomes its manager. Needs the " +
			"permission to create spaces.",
		Annotations: adds("Create a space"),
	}, t.createSpace)

	add(t, s, &mcp.Tool{
		Name:        "update_space",
		Title:       "Update a space",
		Description: "Change the name, description, readme, image or quota of a space; a field left out stays as it is.",
		Annotations: idempotent(changes("Update a space")),
	}, t.updateSpace)

	add(t, s, &mcp.Tool{
		Name:        "disable_space",
		Title:       "Disable a space",
		Description: "Disable a project space: its members lose access until restore_space, its content stays.",
		Annotations: idempotent(changes("Disable a space")),
	}, t.disableSpace)

	add(t, s, &mcp.Tool{
		Name:        "restore_space",
		Title:       "Restore a space",
		Description: "Enable a disabled project space again.",
		Annotations: idempotent(adds("Restore a space")),
	}, t.restoreSpace)

	add(t, s, &mcp.Tool{
		Name:        "delete_space",
		Title:       "Delete a space",
		Description: "Delete a disabled project space for good, with all its content.",
		Annotations: changes("Delete a space"),
	}, t.deleteSpace)

	add(t, s, &mcp.Tool{
		Name:        "list_space_members",
		Title:       "List the members of a space",
		Description: "List who has access to a space, each with their role and the id of the permission, and its links.",
		Annotations: readOnly("List the members of a space"),
	}, t.listSpaceMembers)

	add(t, s, &mcp.Tool{
		Name:        "add_space_member",
		Title:       "Add a member to a space",
		Description: "Give a user or group access to a project space with a role.",
		Annotations: adds("Add a member to a space"),
	}, t.addSpaceMember)

	add(t, s, &mcp.Tool{
		Name:        "update_space_member",
		Title:       "Update a member of a space",
		Description: "Change the role or expiry of a member of a project space.",
		Annotations: idempotent(changes("Update a member of a space")),
	}, t.updateSpaceMember)

	add(t, s, &mcp.Tool{
		Name:        "remove_space_member",
		Title:       "Remove a member from a space",
		Description: "Take a member's access to a project space away.",
		Annotations: idempotent(changes("Remove a member from a space")),
	}, t.removeSpaceMember)
}

type (
	spaces struct {
		Spaces []opencloud.Space `json:"spaces"`
	}

	space struct {
		Space string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
	}

	project struct {
		Name        string `json:"name" jsonschema:"the space's name, like Marketing"`
		Description string `json:"description,omitempty" jsonschema:"a line on what the space is for"`
		Quota       int64  `json:"quota,omitempty" jsonschema:"the space's quota in bytes; the instance's default when left out"`
	}

	edit struct {
		Space       string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Name        string `json:"name,omitempty" jsonschema:"the space's new name"`
		Description string `json:"description,omitempty" jsonschema:"the space's new description"`
		Readme      string `json:"readme,omitempty" jsonschema:"a markdown file of the space to show as its readme, like .space/readme.md"`
		Image       string `json:"image,omitempty" jsonschema:"an image file of the space to show as its picture, like .space/image.png"`
		Quota       int64  `json:"quota,omitempty" jsonschema:"the space's new quota in bytes; needs the permission to set quotas"`
	}

	member struct {
		Space     string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Recipient string `json:"recipient" jsonschema:"a user or group, by its name or id as find_users or find_groups gives it"`
		Role      string `json:"role" jsonschema:"the role, by its name or id as list_share_roles gives it, like Can edit"`
		Expires   string `json:"expires,omitempty" jsonschema:"when it ends, in RFC 3339 like 2027-01-31T00:00:00Z; never when left out"`
	}

	membership struct {
		Space      string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Permission string `json:"permission" jsonschema:"the member's permission id as list_space_members gives it"`
		Role       string `json:"role,omitempty" jsonschema:"the new role, by its name or id as list_share_roles gives it"`
		Expires    string `json:"expires,omitempty" jsonschema:"when it ends, in RFC 3339 like 2027-01-31T00:00:00Z, or never; as it is when left out"`
	}

	revocation struct {
		Space      string `json:"space" jsonschema:"the space, by its name or id as list_spaces gives it"`
		Permission string `json:"permission" jsonschema:"the member's permission id as list_space_members gives it"`
	}
)

func (t *tools) listSpaces(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, spaces, error) {
	c, err := t.client(ctx, req)
	if err != nil {
		return nil, spaces{}, err
	}

	all, err := c.Spaces(ctx)
	if err != nil {
		return nil, spaces{}, err
	}

	var b strings.Builder

	for _, s := range all {
		state := ""
		if s.Disabled {
			state = ", disabled"
		}

		fmt.Fprintf(&b, "%s (%s%s, id %s)\n", s.Name, s.Type, state, s.ID)
	}

	if len(all) == 0 {
		b.WriteString("The user sees no spaces.")
	}

	return text(b.String()), spaces{Spaces: all}, nil
}

func (t *tools) getSpace(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, opencloud.SpaceInfo, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, opencloud.SpaceInfo{}, err
	}

	got, err := c.SpaceInfo(ctx, sp)
	if err != nil {
		return nil, opencloud.SpaceInfo{}, err
	}

	return text(spaceInfo(got)), got, nil
}

// spaceInfo is a space in lines a model reads.
func spaceInfo(sp opencloud.SpaceInfo) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s (%s, id %s)\n", sp.Name, sp.Type, sp.ID)

	if sp.Disabled {
		b.WriteString("disabled: its members have no access until restore_space\n")
	}

	if sp.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", sp.Description)
	}

	if q := sp.Quota; q != nil {
		if q.Total > 0 {
			fmt.Fprintf(&b, "quota: %d of %d bytes used, %d left\n", q.Used, q.Total, q.Remaining)
		} else {
			fmt.Fprintf(&b, "quota: %d bytes used, no limit\n", q.Used)
		}
	}

	for _, f := range []struct{ name, p string }{{"readme", sp.Readme}, {"image", sp.Image}} {
		if f.p != "" {
			fmt.Fprintf(&b, "%s: %s\n", f.name, f.p)
		}
	}

	if sp.Link != "" {
		fmt.Fprintf(&b, "opens at %s\n", sp.Link)
	}

	return b.String()
}

func (t *tools) createSpace(ctx context.Context, req *mcp.CallToolRequest, in project) (*mcp.CallToolResult, opencloud.SpaceInfo, error) {
	if in.Quota < 0 {
		return nil, opencloud.SpaceInfo{}, fmt.Errorf("a quota is a number of bytes, not %d", in.Quota)
	}

	c, err := t.client(ctx, req)
	if err != nil {
		return nil, opencloud.SpaceInfo{}, err
	}

	made, err := c.CreateSpace(ctx, in.Name, in.Description, in.Quota)
	if err != nil {
		return nil, opencloud.SpaceInfo{}, err
	}

	return text("Made the space " + spaceInfo(made)), made, nil
}

func (t *tools) updateSpace(ctx context.Context, req *mcp.CallToolRequest, in edit) (*mcp.CallToolResult, opencloud.SpaceInfo, error) {
	if in.Quota < 0 {
		return nil, opencloud.SpaceInfo{}, fmt.Errorf("a quota is a number of bytes, not %d", in.Quota)
	}

	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, opencloud.SpaceInfo{}, err
	}

	var ch opencloud.SpaceChange

	for _, f := range []struct {
		value string
		to    **string
	}{{in.Name, &ch.Name}, {in.Description, &ch.Description}, {in.Readme, &ch.Readme}, {in.Image, &ch.Image}} {
		if f.value != "" {
			*f.to = &f.value
		}
	}

	if in.Quota > 0 {
		ch.Quota = &in.Quota
	}

	got, err := c.UpdateSpace(ctx, sp, ch)
	if err != nil {
		return nil, opencloud.SpaceInfo{}, err
	}

	return text("Changed the space " + spaceInfo(got)), got, nil
}

func (t *tools) disableSpace(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, any, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, nil, err
	}

	if err := c.DisableSpace(ctx, sp); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("The space %s is disabled; restore_space enables it again, delete_space deletes it for good.", sp.Name)), nil, nil
}

func (t *tools) restoreSpace(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, any, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, nil, err
	}

	if err := c.RestoreSpace(ctx, sp); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("The space %s is enabled.", sp.Name)), nil, nil
}

func (t *tools) deleteSpace(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, any, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, nil, err
	}

	if err := c.DeleteSpace(ctx, sp); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("The space %s is deleted for good.", sp.Name)), nil, nil
}

func (t *tools) listSpaceMembers(ctx context.Context, req *mcp.CallToolRequest, in space) (*mcp.CallToolResult, opencloud.Permissions, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, opencloud.Permissions{}, err
	}

	perms, err := c.Permissions(ctx, sp, "")
	if err != nil {
		return nil, opencloud.Permissions{}, err
	}

	return text(permissions(perms, "The space has no members and no links.")), perms, nil
}

func (t *tools) addSpaceMember(ctx context.Context, req *mcp.CallToolRequest, in member) (*mcp.CallToolResult, granted, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, granted{}, err
	}

	return t.invite(ctx, c, sp, "", []string{in.Recipient}, in.Role, in.Expires)
}

func (t *tools) updateSpaceMember(ctx context.Context, req *mcp.CallToolRequest, in membership) (*mcp.CallToolResult, opencloud.Grant, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, opencloud.Grant{}, err
	}

	return t.update(ctx, c, sp, "", in.Permission, in.Role, "", "", in.Expires)
}

func (t *tools) removeSpaceMember(ctx context.Context, req *mcp.CallToolRequest, in revocation) (*mcp.CallToolResult, any, error) {
	c, sp, _, err := t.at(ctx, req, in.Space, "")
	if err != nil {
		return nil, nil, err
	}

	if err := c.RemovePermission(ctx, sp, "", in.Permission); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("Removed %s from %s.", in.Permission, sp.Name)), nil, nil
}

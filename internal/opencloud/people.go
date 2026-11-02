package opencloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// the kinds of a Person
const (
	kindUser  = "user"
	kindGroup = "group"
)

// Person is a user or a group.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is user or group.
	Kind  string `json:"kind"`
	Login string `json:"login,omitempty"`
	Mail  string `json:"mail,omitempty"`
}

// Me is the signed-in user.
type Me struct {
	Person
	Language string   `json:"language,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// Me is the signed-in user with its groups, by LibreGraph's GET /v1.0/me?$expand=memberOf.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var u libregraph.User

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1.0/me?$expand=memberOf", nil, &u); err != nil {
		return Me{}, graphFail("the signed-in user", err)
	}

	me := Me{Person: user(u), Language: u.GetPreferredLanguage()}
	for _, g := range u.MemberOf {
		me.Groups = append(me.Groups, g.GetDisplayName())
	}

	return me, nil
}

// FindUsers are the users whose name or mail holds query, by LibreGraph's GET /v1.0/users?$search.
func (c *Client) FindUsers(ctx context.Context, query string) ([]Person, error) {
	var page struct {
		Value []libregraph.User `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1.0/users?"+search(query), nil, &page); err != nil {
		return nil, graphFail(fmt.Sprintf("finding users by %q", query), err)
	}

	out := make([]Person, 0, len(page.Value))
	for _, u := range page.Value {
		out = append(out, user(u))
	}

	return out, nil
}

// FindGroups are the groups whose name holds query, by LibreGraph's GET /v1.0/groups?$search.
func (c *Client) FindGroups(ctx context.Context, query string) ([]Person, error) {
	var page struct {
		Value []libregraph.Group `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1.0/groups?"+search(query), nil, &page); err != nil {
		return nil, graphFail(fmt.Sprintf("finding groups by %q", query), err)
	}

	out := make([]Person, 0, len(page.Value))
	for _, g := range page.Value {
		out = append(out, Person{ID: g.GetId(), Name: g.GetDisplayName(), Kind: kindGroup})
	}

	return out, nil
}

// Recipient is the user or group ref names: its id, else the one user or group whose login, name
// or mail it is, else the one that a search for it finds.
func (c *Client) Recipient(ctx context.Context, ref string) (Person, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Person{}, fail(ErrNotFound, "no user or group is given")
	}

	var u libregraph.User
	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1.0/users/"+url.PathEscape(ref), nil, &u); err == nil && u.GetId() == ref {
		return user(u), nil
	}

	var g libregraph.Group
	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1.0/groups/"+url.PathEscape(ref), nil, &g); err == nil && g.GetId() == ref {
		return Person{ID: g.GetId(), Name: g.GetDisplayName(), Kind: kindGroup}, nil
	}

	users, err := c.FindUsers(ctx, ref)
	if err != nil {
		return Person{}, err
	}

	groups, err := c.FindGroups(ctx, ref)
	if err != nil {
		return Person{}, err
	}

	found := slices.Concat(users, groups)

	var exact []Person

	for _, p := range found {
		if strings.EqualFold(p.Login, ref) || strings.EqualFold(p.Name, ref) || strings.EqualFold(p.Mail, ref) {
			exact = append(exact, p)
		}
	}

	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) == 0 && len(found) == 1:
		return found[0], nil
	case len(found) == 0:
		return Person{}, fail(ErrNotFound, "OpenCloud knows no user or group %q", ref)
	}

	if len(exact) > 1 {
		found = exact
	}

	names := make([]string, len(found))
	for i, p := range found {
		names[i] = fmt.Sprintf("%s (%s, id %s)", p.Name, p.Kind, p.ID)
	}

	return Person{}, fail(ErrNotFound, "%q could be %s; name one by its id", ref, strings.Join(names, ", "))
}

func user(u libregraph.User) Person {
	return Person{ID: u.GetId(), Name: u.GetDisplayName(), Kind: kindUser, Login: u.GetOnPremisesSamAccountName(), Mail: u.GetMail()}
}

// search is the $search of query, quoted as LibreGraph takes it.
func search(query string) string {
	return url.Values{"$search": {`"` + strings.ReplaceAll(strings.TrimSpace(query), `"`, "") + `"`}}.Encode()
}

package opencloud

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
)

// App is an app OpenCloud opens files of a type with.
type App struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Types are the extensions of the file types it opens, like docx.
	Types []string `json:"types"`
	// Creates are the extensions of the types it makes new documents of, like odt.
	Creates []string `json:"creates,omitempty"`
}

type appList struct {
	MIMETypes []struct {
		Ext           string `json:"ext"`
		AllowCreation bool   `json:"allow_creation"`
		Providers     []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"app_providers"`
	} `json:"mime-types"`
}

// Apps are the apps OpenCloud opens files with, by GET /app/list.
func (c *Client) Apps(ctx context.Context) ([]App, error) {
	var list appList
	if err := c.app(ctx, http.MethodGet, "/app/list", &list); err != nil {
		return nil, fmt.Errorf("the apps: %w", err)
	}

	var out []App

	for _, m := range list.MIMETypes {
		for _, p := range m.Providers {
			i := slices.IndexFunc(out, func(a App) bool { return a.Name == p.Name })
			if i < 0 {
				out = append(out, App{Name: p.Name, Description: p.Description})
				i = len(out) - 1
			}

			if m.Ext != "" {
				out[i].Types = append(out[i].Types, m.Ext)
				if m.AllowCreation {
					out[i].Creates = append(out[i].Creates, m.Ext)
				}
			}
		}
	}

	return out, nil
}

// OpenInApp is the link that opens the file p of the space in the app, the default for its type
// when app is empty, in OpenCloud's web UI; by POST /app/open-with-web.
func (c *Client) OpenInApp(ctx context.Context, sp Space, p, app string) (string, error) {
	p, err := filePath(p)
	if err != nil {
		return "", err
	}

	it, err := c.item(ctx, sp, p, "")
	if err != nil {
		return "", err
	}

	if it.Folder != nil {
		return "", fail(ErrNotAFile, "%q is a folder in the space %s; an app opens a file", p, sp.Name)
	}

	q := url.Values{"file_id": {it.GetId()}}
	if app = strings.TrimSpace(app); app != "" {
		q.Set("app_name", app)
	}

	var answer struct {
		URI string `json:"uri"`
	}

	if err := c.app(ctx, http.MethodPost, "/app/open-with-web?"+q.Encode(), &answer); err != nil {
		return "", fmt.Errorf("opening %s: %w", where(sp, p), err)
	}

	return answer.URI, nil
}

// CreateDocument makes the empty document p of the space, of a type its extension names that an
// app makes new documents of, by POST /app/new.
func (c *Client) CreateDocument(ctx context.Context, sp Space, p string) error {
	p, err := filePath(p)
	if err != nil {
		return err
	}

	apps, err := c.Apps(ctx)
	if err != nil {
		return err
	}

	var creatable []string

	for _, a := range apps {
		for _, ext := range a.Creates {
			if !slices.Contains(creatable, ext) {
				creatable = append(creatable, ext)
			}
		}
	}

	if ext := strings.TrimPrefix(path.Ext(p), "."); !slices.Contains(creatable, strings.ToLower(ext)) {
		if len(creatable) == 0 {
			return fail(ErrNotFound, "no app of OpenCloud makes new documents")
		}

		return fail(ErrNotFound, "no app makes documents of the type %q; these it makes: %s", ext, strings.Join(creatable, ", "))
	}

	dir := path.Dir(p)
	if dir != "." {
		if err := c.MakeFolder(ctx, sp, dir); err != nil {
			return err
		}
	}

	parent, err := c.item(ctx, sp, strings.TrimPrefix(dir, "."), "")
	if err != nil {
		return err
	}

	q := url.Values{"parent_container_id": {parent.GetId()}, "filename": {path.Base(p)}}

	if err := c.app(ctx, http.MethodPost, "/app/new?"+q.Encode(), nil); err != nil {
		return fmt.Errorf("creating %s: %w", where(sp, p), err)
	}

	return nil
}

// app sends a request to OpenCloud's app provider and reads its JSON answer into out, when not
// nil; its errors say what the app provider says.
func (c *Client) app(ctx context.Context, method, at string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+at, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return c.refused()
	}

	if resp.StatusCode >= http.StatusBadRequest {
		var answer struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}

		// an answer that is no error of the app provider leaves the message empty
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)

		switch {
		case answer.Code == "RESOURCE_ALREADY_EXISTS":
			return fail(ErrExists, "%s", answer.Message)
		case resp.StatusCode == http.StatusNotFound:
			return fail(ErrNotFound, "%s", cmp.Or(answer.Message, resp.Status))
		case resp.StatusCode == http.StatusForbidden:
			return fail(ErrDenied, "%s", cmp.Or(answer.Message, resp.Status))
		}

		return fmt.Errorf("OpenCloud answered %s", cmp.Or(answer.Message, resp.Status))
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(out); err != nil {
		return fmt.Errorf("reading OpenCloud's answer: %w", err)
	}

	return nil
}

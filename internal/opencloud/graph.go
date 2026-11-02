package opencloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
)

// List is the content of a folder of the space, by LibreGraph's GET
// /v1.0/drives/{id}/root:/<path>:/children, the top's by /items/{id}/children.
func (c *Client) List(ctx context.Context, sp Space, dir string) ([]Entry, error) {
	dir, err := Clean(dir)
	if err != nil {
		return nil, err
	}

	at := c.drive(sp) + "/items/" + url.PathEscape(sp.ID) + "/children"
	if dir != "" {
		at = c.drive(sp) + "/root:/" + lookup(dir) + ":/children"
	}

	var page struct {
		Value []libregraph.DriveItem `json:"value"`
	}

	status, err := c.getJSON(ctx, at, &page)

	switch {
	case errors.Is(err, ErrRefused), errors.Is(err, ErrForeignOrigin):
		return nil, err
	case status == http.StatusNotFound:
		return nil, fail(ErrNotFound, "%q is not in the space %s", dir, sp.Name)
	// OpenCloud 9.0.0 answers the children of a file with 500
	case status == http.StatusInternalServerError && dir != "":
		if it, ierr := c.item(ctx, sp, dir, ""); ierr == nil && it.Folder == nil {
			return nil, fail(ErrNotAFolder, "%q is a file in the space %s, not a folder", dir, sp.Name)
		}

		return nil, fmt.Errorf("listing %s in the space %s: %w", dir, sp.Name, err)
	case err != nil:
		return nil, fmt.Errorf("listing %s in the space %s: %w", dir, sp.Name, err)
	}

	out := make([]Entry, 0, len(page.Value))
	for _, it := range page.Value {
		e := Entry{Path: path.Join(dir, it.GetName()), Folder: it.Folder != nil, Modified: it.GetLastModifiedDateTime(), ETag: it.GetETag()}
		if !e.Folder {
			e.Size = it.GetSize()
		}

		out = append(out, e)
	}

	return out, nil
}

// item is the file or folder p of the space as LibreGraph gives it, by the path lookup of
// GET /v1.0/drives/{id}/root:/<path>, the top by /items/{id}; query adds to the request, like
// $select of properties OpenCloud gives only when asked.
func (c *Client) item(ctx context.Context, sp Space, p, query string) (libregraph.DriveItem, error) {
	p, err := Clean(p)
	if err != nil {
		return libregraph.DriveItem{}, err
	}

	at := c.drive(sp) + "/items/" + url.PathEscape(sp.ID)
	if p != "" {
		at = c.drive(sp) + "/root:/" + lookup(p)
	}

	if query != "" {
		at += "?" + query
	}

	var it libregraph.DriveItem

	status, err := c.getJSON(ctx, at, &it)

	switch {
	case errors.Is(err, ErrRefused), errors.Is(err, ErrForeignOrigin):
		return libregraph.DriveItem{}, err
	case status == http.StatusNotFound:
		return libregraph.DriveItem{}, fail(ErrNotFound, "%q is not in the space %s", p, sp.Name)
	case err != nil:
		return libregraph.DriveItem{}, fmt.Errorf("looking up %s in the space %s: %w", p, sp.Name, err)
	case it.GetId() == "":
		return libregraph.DriveItem{}, fmt.Errorf("OpenCloud gave no id of %s in the space %s", p, sp.Name)
	}

	return it, nil
}

// drive is the address of the space in LibreGraph.
func (c *Client) drive(sp Space) string {
	return c.base + "/graph/v1.0/drives/" + url.PathEscape(sp.ID)
}

// lookup is p escaped for LibreGraph's path lookup.
func lookup(p string) string {
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		// a colon ends the path in the lookup, one in a name has to be escaped
		segments[i] = strings.ReplaceAll(url.PathEscape(seg), ":", "%3A")
	}

	return strings.Join(segments, "/")
}

// getJSON reads the answer of a GET of LibreGraph at into v; any status but 200 is an error, the
// status returned with it.
func (c *Client) getJSON(ctx context.Context, at string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, at, nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return resp.StatusCode, c.refused()
	default:
		return resp.StatusCode, fmt.Errorf("OpenCloud answered %s", resp.Status)
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(v); err != nil {
		return resp.StatusCode, fmt.Errorf("reading OpenCloud's answer: %w", err)
	}

	return resp.StatusCode, nil
}

// graphDo sends a request to LibreGraph at with in as its JSON body, when not nil, and reads a
// JSON answer into out, when not nil. A status of 400 and up is an error of OpenCloud's own words
// about it, classed by the status, the status returned with it.
func (c *Client) graphDo(ctx context.Context, method, at string, in, out any) (int, error) {
	return c.graphSend(ctx, method, at, nil, in, out)
}

// graphSend is graphDo with the request's own headers.
func (c *Client) graphSend(ctx context.Context, method, at string, header map[string]string, in, out any) (int, error) {
	var body io.Reader

	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}

		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, at, body)
	if err != nil {
		return 0, err
	}

	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return resp.StatusCode, c.graphError(resp)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("reading OpenCloud's answer: %w", err)
	}

	return resp.StatusCode, nil
}

// graphError is LibreGraph's error answer as a sentence of OpenCloud's message, classed by its
// status.
func (c *Client) graphError(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return c.refused()
	}

	var answer struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	// an answer that is no LibreGraph error leaves the message empty
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)

	msg := strings.Join(strings.Fields(answer.Error.Message), " ")
	if msg == "" {
		msg = "OpenCloud answered " + resp.Status
	}

	switch resp.StatusCode {
	case http.StatusForbidden:
		return fail(ErrDenied, "%s", msg)
	case http.StatusNotFound:
		return fail(ErrNotFound, "%s", msg)
	case http.StatusConflict:
		return fail(ErrExists, "%s", msg)
	}

	return errors.New(msg)
}

// graphFail is err of a request about what, keeping its class.
func graphFail(what string, err error) error {
	if cl, ok := errors.AsType[*classified](err); ok {
		if errors.Is(err, ErrRefused) || errors.Is(err, ErrForeignOrigin) {
			return err
		}

		return fail(cl.class, "%s: %s", what, cl.text)
	}

	return fmt.Errorf("%s: %w", what, err)
}

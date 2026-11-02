package opencloud

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// Tags are the tags in use on what the user sees, by LibreGraph's GET
// /v1.0/extensions/org.libregraph/tags.
func (c *Client) Tags(ctx context.Context) ([]string, error) {
	var page struct {
		Value []string `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodGet, c.tagsURL(), nil, &page); err != nil {
		return nil, graphFail("the tags", err)
	}

	slices.Sort(page.Value)

	return page.Value, nil
}

// Tag adds the tags to the file or folder p of the space, by LibreGraph's PUT
// /v1.0/extensions/org.libregraph/tags, and gives the tags it has then, sorted; a tag it has
// already is done.
func (c *Client) Tag(ctx context.Context, sp Space, p string, tags []string) ([]string, error) {
	return c.retag(ctx, sp, p, tags, true)
}

// Untag takes the tags off the file or folder p of the space, by LibreGraph's DELETE
// /v1.0/extensions/org.libregraph/tags, and gives the tags it has then, sorted.
func (c *Client) Untag(ctx context.Context, sp Space, p string, tags []string) ([]string, error) {
	return c.retag(ctx, sp, p, tags, false)
}

func (c *Client) retag(ctx context.Context, sp Space, p string, tags []string, on bool) ([]string, error) {
	p, err := filePath(p)
	if err != nil {
		return nil, err
	}

	var clean []string

	for _, t := range tags {
		t = strings.TrimSpace(t)

		// OpenCloud keeps the tags of an item as one text, separated by commas
		if strings.Contains(t, ",") {
			return nil, fail(ErrNotFound, "a tag holds no comma: %q", t)
		}

		if t != "" && !slices.Contains(clean, t) {
			clean = append(clean, t)
		}
	}

	if len(clean) == 0 {
		return nil, fail(ErrNotFound, "no tag is given")
	}

	it, err := c.item(ctx, sp, p, "")
	if err != nil {
		return nil, err
	}

	has := it.LibreGraphTags
	method := http.MethodDelete

	if on {
		// OpenCloud refuses a request of tags the item has all of already
		clean = slices.DeleteFunc(clean, func(t string) bool { return slices.Contains(has, t) })
		if len(clean) == 0 {
			return slices.Sorted(slices.Values(has)), nil
		}

		method = http.MethodPut
	}

	body := map[string]any{"resourceId": it.GetId(), "tags": clean}

	if _, err := c.graphDo(ctx, method, c.tagsURL(), body, nil); err != nil {
		return nil, graphFail("the tags of "+where(sp, p), err)
	}

	now := slices.DeleteFunc(slices.Clone(has), func(t string) bool { return slices.Contains(clean, t) })
	if on {
		now = append(now, clean...)
	}

	slices.Sort(now)

	return now, nil
}

func (c *Client) tagsURL() string {
	return c.base + "/graph/v1.0/extensions/org.libregraph/tags"
}

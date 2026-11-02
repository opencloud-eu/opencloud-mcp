package opencloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// Activity is something that happened to a file, folder or space.
type Activity struct {
	ID string `json:"id"`
	// Message says what happened, like "Alice Example added notes.md to Handbook".
	Message string    `json:"message"`
	By      string    `json:"by,omitempty"`
	When    time.Time `json:"when,omitzero"`
}

// Activities are the newest limit activities of the file or folder p of the space and everything
// below it, of the whole space when p is empty, newest first; by LibreGraph's GET
// /v1beta1/extensions/org.libregraph/activities.
func (c *Client) Activities(ctx context.Context, sp Space, p string, limit int) ([]Activity, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}

	id := sp.ID
	if p != "" {
		it, err := c.item(ctx, sp, p, "")
		if err != nil {
			return nil, err
		}

		id = it.GetId()
	}

	q := fmt.Sprintf(`itemid:%q AND depth:-1 AND limit:%d AND sort:desc`, id, limit)

	var page struct {
		Value []struct {
			ID       string `json:"id"`
			Template struct {
				Message   string `json:"message"`
				Variables map[string]struct {
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
				} `json:"variables"`
			} `json:"template"`
			Times struct {
				Recorded time.Time `json:"recordedTime"`
			} `json:"times"`
		} `json:"value"`
	}

	if _, err := c.graphDo(ctx, http.MethodGet, c.base+"/graph/v1beta1/extensions/org.libregraph/activities?kql="+url.QueryEscape(q), nil, &page); err != nil {
		return nil, graphFail("the activities of "+where(sp, p), err)
	}

	out := make([]Activity, 0, len(page.Value))

	for _, a := range page.Value {
		vars := a.Template.Variables
		msg := placeholder.ReplaceAllStringFunc(a.Template.Message, func(m string) string {
			v, ok := vars[m[1:len(m)-1]]
			if !ok {
				return m
			}

			if v.DisplayName != "" {
				return v.DisplayName
			}

			return v.Name
		})

		out = append(out, Activity{ID: a.ID, Message: msg, By: vars["user"].DisplayName, When: a.Times.Recorded.UTC()})
	}

	return out, nil
}

// placeholder is a variable in the template of an activity, like {user}
var placeholder = regexp.MustCompile(`\{[a-z]+\}`)

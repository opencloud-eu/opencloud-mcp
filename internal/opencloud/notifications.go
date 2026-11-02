package opencloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Notification is a notice to the user, like a share they received.
type Notification struct {
	ID      string    `json:"id"`
	Subject string    `json:"subject"`
	Message string    `json:"message"`
	When    time.Time `json:"when,omitzero"`
	// Object is the id of what it is about, ObjectType its kind, like share or space.
	Object     string `json:"object,omitempty"`
	ObjectType string `json:"objectType,omitempty"`
}

const notificationsPath = "/ocs/v2.php/apps/notifications/api/v1/notifications?format=json"

// Notifications are the user's notifications, newest first, by OCS's GET of
// /ocs/v2.php/apps/notifications/api/v1/notifications.
func (c *Client) Notifications(ctx context.Context) ([]Notification, error) {
	var data []struct {
		ID         string    `json:"notification_id"`
		Subject    string    `json:"subject"`
		Message    string    `json:"message"`
		When       time.Time `json:"datetime"`
		Object     string    `json:"object_id"`
		ObjectType string    `json:"object_type"`
	}

	if err := c.ocs(ctx, http.MethodGet, nil, &data); err != nil {
		return nil, fmt.Errorf("the notifications of %s: %w", c.who, err)
	}

	out := make([]Notification, 0, len(data))
	for _, n := range data {
		out = append(out, Notification{ID: n.ID, Subject: n.Subject, Message: n.Message, When: n.When.UTC(), Object: n.Object, ObjectType: n.ObjectType})
	}

	slices.SortStableFunc(out, func(a, b Notification) int { return b.When.Compare(a.When) })

	return out, nil
}

// Dismiss dismisses the user's notifications of the ids, by OCS's DELETE of
// /ocs/v2.php/apps/notifications/api/v1/notifications; an id that is gone is done.
func (c *Client) Dismiss(ctx context.Context, ids []string) error {
	var clean []string

	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			clean = append(clean, id)
		}
	}

	if len(clean) == 0 {
		return fail(ErrNotFound, "no notification is given")
	}

	if err := c.ocs(ctx, http.MethodDelete, map[string][]string{"ids": clean}, nil); err != nil {
		return fmt.Errorf("dismissing the notifications of %s: %w", c.who, err)
	}

	return nil
}

// ocs sends a request to the notifications of OCS with in as its JSON body, when not nil, and
// reads the data of the answer into out, when not nil.
func (c *Client) ocs(ctx context.Context, method string, in, out any) error {
	var body io.Reader

	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}

		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+notificationsPath, body)
	if err != nil {
		return err
	}

	req.Header.Set("Ocs-Apirequest", "true")

	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return c.refused()
	case resp.StatusCode >= http.StatusBadRequest:
		return errors.New("OpenCloud answered " + resp.Status)
	case out == nil:
		return nil
	}

	var answer struct {
		OCS struct {
			Data json.RawMessage `json:"data"`
		} `json:"ocs"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(&answer); err != nil {
		return fmt.Errorf("reading OpenCloud's answer: %w", err)
	}

	// no notifications are a null
	if len(answer.OCS.Data) == 0 || string(answer.OCS.Data) == "null" {
		return nil
	}

	return json.Unmarshal(answer.OCS.Data, out)
}

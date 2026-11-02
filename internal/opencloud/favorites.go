package opencloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Favorite marks the file or folder p of the space as a favorite of the user, or unmarks it, by
// LibreGraph's POST /v1.0/me/drive/items/{id}/follow and DELETE /v1.0/me/drive/following/{id};
// either is done when it already is so.
func (c *Client) Favorite(ctx context.Context, sp Space, p string, on bool) error {
	p, err := filePath(p)
	if err != nil {
		return err
	}

	it, err := c.item(ctx, sp, p, "")
	if err != nil || it.GetLibreGraphMeFollowing() == on {
		return err
	}

	var resp *http.Response
	if on {
		_, resp, err = c.graph.MeDriveApi.FollowDriveItem(ctx, it.GetId()).Execute()
	} else {
		// OpenCloud 9.0.0 answers the unfollow of what is no favorite with 500, hence the look first
		resp, err = c.graph.MeDriveApi.UnfollowDriveItem(ctx, it.GetId()).Execute()
	}

	if resp != nil {
		// the client has read the body already; closing it is only good form
		defer resp.Body.Close()
	}

	switch {
	case resp != nil && resp.StatusCode == http.StatusUnauthorized:
		return c.refused()
	case errors.Is(err, ErrForeignOrigin):
		return err
	case err != nil:
		return fmt.Errorf("marking %s in the space %s: %w", p, sp.Name, err)
	}

	return nil
}

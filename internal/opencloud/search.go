package opencloud

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Hit is a file or folder a search found.
type Hit struct {
	// Space is the id of the hit's space as Spaces gives it, SpaceName its name.
	Space      string   `json:"space"`
	SpaceName  string   `json:"spaceName"`
	Path       string   `json:"path"`
	ID         string   `json:"id,omitempty"`
	Folder     bool     `json:"folder,omitempty"`
	Size       int64    `json:"size,omitempty"`
	MIMEType   string   `json:"mimeType,omitempty"`
	Modified   string   `json:"modified,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Favorite   bool     `json:"favorite,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Highlights string   `json:"highlights,omitempty"`
}

// Search asks OpenCloud's search what the KQL query finds in the space, or in every space the
// user sees when sp is nil. It is the WebDAV REPORT search-files the web client sends, which hands
// the query to the search service as it is; a query the service refuses is ErrBadQuery.
func (c *Client) Search(ctx context.Context, sp *Space, query string, limit int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fail(ErrBadQuery, "the search has no query")
	}

	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><oc:search-files xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">`)
	body.WriteString(`<d:prop><oc:name/><oc:fileid/><d:getcontentlength/><oc:size/><d:resourcetype/><d:getcontenttype/>` +
		`<d:getlastmodified/><oc:tags/><oc:favorite/><oc:score/><oc:highlights/></d:prop><oc:search><oc:pattern>`)

	if err := xml.EscapeText(&body, []byte(query)); err != nil {
		return nil, fmt.Errorf("writing the search: %w", err)
	}

	fmt.Fprintf(&body, `</oc:pattern><oc:limit>%d</oc:limit></oc:search></oc:search-files>`, limit)

	where, at := "every space", c.base+"/dav/spaces/"
	if sp != nil {
		where, at = "the space "+sp.Name, c.spaceURL(*sp)
	}

	req, err := http.NewRequestWithContext(ctx, "REPORT", at, &body)
	if err != nil {
		return nil, fmt.Errorf("the search: %w", err)
	}

	req.Header.Set("Content-Type", "application/xml; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching %s: %w", where, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusMultiStatus:
	case http.StatusBadRequest:
		return nil, fail(ErrBadQuery, "OpenCloud refused the query %s: %s", query, refusal(resp.Body))
	case http.StatusUnauthorized:
		return nil, c.refused()
	case http.StatusNotFound:
		return nil, fail(ErrSpaceNotFound, "OpenCloud does not know %s", where)
	default:
		return nil, fmt.Errorf("OpenCloud's search answered %s", resp.Status)
	}

	var ms multistatus
	if err := xml.NewDecoder(io.LimitReader(resp.Body, MaxRead)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("reading the search's answer: %w", err)
	}

	var spaces []Space
	if sp != nil {
		spaces = []Space{*sp}
	} else if spaces, err = c.Spaces(ctx); err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, len(ms.Responses))

	for _, r := range ms.Responses {
		h, ok := hit(r, spaces)
		if ok {
			hits = append(hits, h)
		}
	}

	return hits, nil
}

// hit is a result of the search as a Hit of one of the spaces; a result of none of them is not one.
func hit(r response, spaces []Space) (Hit, bool) {
	var h Hit

	for _, sp := range spaces {
		if p, ok := inSpace(r.Href, sp.ID); ok {
			h = Hit{Space: sp.ID, SpaceName: sp.Name, Path: p}

			break
		}
	}

	if h.Path == "" {
		return h, false
	}

	for _, ps := range r.Propstat {
		if !strings.Contains(ps.Status, " 200 ") {
			continue
		}

		p := ps.Prop
		h.ID = strings.TrimSpace(p.FileID)
		h.Folder = p.ResourceType.Collection != nil
		h.MIMEType = strings.TrimSpace(p.ContentType)
		h.Favorite = strings.TrimSpace(p.Favorite) == "1"
		h.Highlights = strings.TrimSpace(p.Highlights)
		// a property missing from the answer leaves its field zero
		h.Size, _ = strconv.ParseInt(strings.TrimSpace(p.Length+p.Size), 10, 64)
		h.Score, _ = strconv.ParseFloat(strings.TrimSpace(p.Score), 64)

		if t, err := time.Parse(time.RFC1123, strings.TrimSpace(p.Modified)); err == nil {
			h.Modified = t.UTC().Format(time.RFC3339)
		}

		for t := range strings.SplitSeq(p.Tags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				h.Tags = append(h.Tags, t)
			}
		}
	}

	return h, true
}

var message = regexp.MustCompile(`<s:message>([^<]*)</s:message>`)

// refusal is what OpenCloud says about a query it refused, like "the operator 'AND' is missing an
// operand".
func refusal(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, 64<<10))
	if err != nil {
		return "the answer could not be read"
	}

	m := message.FindSubmatch(b)
	if m == nil {
		return "no reason given"
	}

	return strings.TrimPrefix(html.UnescapeString(string(m[1])), "error: bad request: ")
}

// multistatus is the part of a PROPFIND or search answer read here; tags without a namespace match any.
type multistatus struct {
	Responses []response `xml:"response"`
}

type response struct {
	Href     string `xml:"href"`
	Propstat []struct {
		Status string `xml:"status"`
		Prop   struct {
			FileID       string `xml:"fileid"`
			Length       string `xml:"getcontentlength"`
			Size         string `xml:"size"`
			ContentType  string `xml:"getcontenttype"`
			Modified     string `xml:"getlastmodified"`
			Tags         string `xml:"tags"`
			Favorite     string `xml:"favorite"`
			Score        string `xml:"score"`
			Highlights   string `xml:"highlights"`
			Location     string `xml:"trashbin-original-location"`
			Timestamp    string `xml:"trashbin-delete-timestamp"`
			ResourceType struct {
				Collection *struct{} `xml:"collection"`
			} `xml:"resourcetype"`
		} `xml:"prop"`
	} `xml:"propstat"`
}

// inSpace is the path in the space of a result's href, /dav/spaces/<space id>/<path>; a result
// of any other space is not the space's.
func inSpace(href, spaceID string) (string, bool) {
	u, err := url.Parse(href)
	if err != nil {
		return "", false
	}

	_, rest, ok := strings.Cut(u.Path, "/dav/spaces/")
	if !ok {
		return "", false
	}

	id, p, _ := strings.Cut(rest, "/")
	if storage(id) != storage(spaceID) {
		return "", false
	}

	p, err = Clean(p)

	return p, err == nil && p != ""
}

// storage is a space id without the node it may name, <storage>$<space>.
func storage(id string) string {
	s, _, _ := strings.Cut(id, "!")

	return s
}

// Package opencloud is a client for the users of one OpenCloud: spaces, folders and favorites
// through LibreGraph, the content of files and OpenCloud's own search through WebDAV, every request
// signed in as that user and held to the configured instance.
package opencloud

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	libregraph "github.com/opencloud-eu/libre-graph-api-go"
	"github.com/studio-b12/gowebdav"
)

// MaxRead bounds how many bytes a read takes from a file.
const MaxRead = 32 << 20

// Config is how to reach one OpenCloud.
type Config struct {
	// URL is the instance, like https://cloud.example.com.
	URL string
	// InsecureTLS skips certificate checks, for a local instance's own certificate only.
	InsecureTLS bool
	// Timeout bounds every request; a minute when zero.
	Timeout time.Duration
}

// Cloud is one OpenCloud, reached as any of its users.
type Cloud struct {
	base    *url.URL
	tr      http.RoundTripper
	timeout time.Duration
}

// Client reaches one OpenCloud as one user.
type Client struct {
	base string
	// who names the user in what a model reads
	who      string
	sentence string
	http     *http.Client
	graph    *libregraph.APIClient
}

// Space is a space the user sees.
type Space struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	// ETag is the etag of the space's root, which moves with anything inside the space.
	ETag string `json:"etag,omitempty"`
	// Disabled is a project space its manager disabled, see DisableSpace.
	Disabled bool `json:"disabled,omitempty"`
}

// Entry is a file or folder of a space.
type Entry struct {
	Path     string    `json:"path"`
	Folder   bool      `json:"folder,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitzero"`
	// ETag moves with the file's content, and for a folder with anything inside it.
	ETag string `json:"-"`
}

// New is the OpenCloud cfg names.
func New(cfg Config) (*Cloud, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.URL), "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil {
		return nil, errors.New("the OpenCloud URL must be an address like https://cloud.example.com")
	}

	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's default
	if cfg.InsecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opted into for a local instance's own certificate
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = time.Minute
	}

	return &Cloud{base: base, tr: tr, timeout: timeout}, nil
}

// URL is the instance's address, without a trailing slash.
func (c *Cloud) URL() string { return c.base.String() }

// AppToken is a client signed in as user with the user's app token, by basic auth.
func (c *Cloud) AppToken(user string, token Secret) (*Client, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil, errors.New("no user is set, the account the app token belongs to")
	}

	if token == "" {
		return nil, errors.New("no app token is set")
	}

	return c.client(user, "the app token of "+user, func(r *http.Request) { r.SetBasicAuth(user, token.Reveal()) }), nil
}

// Bearer is a client signed in with an access token OpenCloud's identity provider issued for
// OpenCloud; who names the user in errors.
func (c *Cloud) Bearer(who string, token Secret) *Client {
	return c.client(who, "the sign-in of "+who, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token.Reveal()) })
}

func (c *Cloud) client(who, credential string, sign func(*http.Request)) *Client {
	hc := &http.Client{Timeout: c.timeout, Transport: &guard{origin: c.base, sign: sign, next: c.tr}}

	gc := libregraph.NewConfiguration()
	gc.Servers = libregraph.ServerConfigurations{{URL: c.base.String() + "/graph"}}
	gc.HTTPClient = hc

	return &Client{base: c.base.String(), who: who, sentence: credential, http: hc, graph: libregraph.NewAPIClient(gc)}
}

// Who names the user the client is signed in as.
func (c *Client) Who() string { return c.who }

// Spaces are the spaces the user sees, by LibreGraph's GET /me/drives.
func (c *Client) Spaces(ctx context.Context) ([]Space, error) {
	drives, resp, err := c.graph.MeDrivesApi.ListMyDrives(ctx).Execute()
	if resp != nil {
		// the client has read the body already; closing it is only good form
		defer resp.Body.Close()
	}

	switch {
	case resp != nil && resp.StatusCode == http.StatusUnauthorized:
		return nil, c.refused()
	case errors.Is(err, ErrForeignOrigin):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("listing the spaces: %w", err)
	}

	spaces := make([]Space, 0, len(drives.GetValue()))
	for _, d := range drives.GetValue() {
		root := d.GetRoot()
		deleted := root.GetDeleted()
		spaces = append(spaces, Space{
			ID: d.GetId(), Name: d.GetName(), Type: d.GetDriveType(), Description: d.GetDescription(), ETag: root.GetETag(),
			Disabled: deleted.GetState() == "trashed",
		})
	}

	return spaces, nil
}

// Space is the space ref names: its id, else its name in any case, among Spaces.
func (c *Client) Space(ctx context.Context, ref string) (Space, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Space{}, fail(ErrSpaceNotFound, "no space is given; name one of the spaces %s sees", c.who)
	}

	spaces, err := c.Spaces(ctx)
	if err != nil {
		return Space{}, err
	}

	if i := slices.IndexFunc(spaces, func(s Space) bool { return s.ID == ref }); i >= 0 {
		return spaces[i], nil
	}

	var named []Space

	for _, s := range spaces {
		if strings.EqualFold(s.Name, ref) {
			named = append(named, s)
		}
	}

	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return Space{}, fail(ErrSpaceNotFound, "there is no space named %q that %s sees", ref, c.who)
	}

	ids := make([]string, len(named))
	for i, s := range named {
		ids[i] = s.ID
	}

	return Space{}, fail(ErrSpaceNotFound, "%d spaces are named %q; name one by its id: %s", len(named), ref, strings.Join(ids, ", "))
}

// Read is the content of a file of the space, up to MaxRead bytes, by a WebDAV GET.
func (c *Client) Read(_ context.Context, sp Space, p string) ([]byte, error) {
	p, err := filePath(p)
	if err != nil {
		return nil, err
	}

	rc, err := c.dav(sp).ReadStream("/" + p)
	if err != nil {
		return nil, c.explain(err, sp, p)
	}
	defer rc.Close()

	b, err := io.ReadAll(io.LimitReader(rc, MaxRead+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}

	if len(b) > MaxRead {
		return nil, fail(ErrTooLarge, "%s is larger than the %d MiB a read takes", p, MaxRead>>20)
	}

	return b, nil
}

// Head is the first n bytes of a file of the space, or all of a shorter one, by a WebDAV GET with a
// range.
func (c *Client) Head(_ context.Context, sp Space, p string, n int64) ([]byte, error) {
	p, err := filePath(p)
	if err != nil {
		return nil, err
	}

	rc, err := c.dav(sp).ReadStreamRange("/"+p, 0, n)
	if err != nil {
		return nil, c.explain(err, sp, p)
	}
	defer rc.Close()

	b, err := io.ReadAll(io.LimitReader(rc, n))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}

	return b, nil
}

// Write creates the file p in the space with its folders, or replaces it, by WebDAV MKCOL and PUT.
func (c *Client) Write(_ context.Context, sp Space, p string, data []byte) error {
	p, err := filePath(p)
	if err != nil {
		return err
	}

	dav := c.dav(sp)

	if dir := path.Dir(p); dir != "." {
		if err := dav.MkdirAll("/"+dir, 0o755); err != nil {
			return c.explain(err, sp, dir)
		}
	}

	if err := dav.Write("/"+p, data, 0o644); err != nil {
		return c.explain(err, sp, p)
	}

	return nil
}

// dav is a WebDAV client of the space; the transport signs every request in.
func (c *Client) dav(sp Space) *gowebdav.Client {
	dav := gowebdav.NewAuthClient(c.spaceURL(sp), gowebdav.NewEmptyAuth())
	dav.SetTransport(c.http.Transport)
	dav.SetTimeout(c.http.Timeout)

	return dav
}

func (c *Client) spaceURL(sp Space) string {
	return c.base + "/dav/spaces/" + url.PathEscape(sp.ID)
}

func (c *Client) refused() error {
	return fail(ErrRefused, "OpenCloud refused %s", c.sentence)
}

// explain turns a WebDAV failure into a sentence a model can pass on.
func (c *Client) explain(err error, sp Space, p string) error {
	// gowebdav nests the status a level deeper when its sign-in fails, out of IsErrCode's reach
	var status gowebdav.StatusError

	errors.As(err, &status)

	switch {
	case status.Status == http.StatusNotFound:
		return fail(ErrNotFound, "%q is not in the space %s", p, sp.Name)
	case status.Status == http.StatusUnauthorized:
		return c.refused()
	case errors.Is(err, ErrForeignOrigin):
		return err
	}

	return fmt.Errorf("%s in the space %s: %w", p, sp.Name, err)
}

// Clean is p as a path inside a space, without a leading slash; "" is the space's top. A path
// that leads out of the space is refused, never quietly folded back in.
func Clean(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.ContainsRune(p, 0) {
		return "", fail(ErrOutsideSpace, "%q is not a path in the space", p)
	}

	clean := path.Clean(strings.TrimLeft(p, "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fail(ErrOutsideSpace, "%q leads outside the space", p)
	}

	if clean == "." {
		return "", nil
	}

	return clean, nil
}

func filePath(p string) (string, error) {
	clean, err := Clean(p)
	if err != nil {
		return "", err
	}

	if clean == "" {
		return "", fail(ErrNotFound, "%q is not a file path in the space", p)
	}

	return clean, nil
}

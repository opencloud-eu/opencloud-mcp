// Package signin takes the sign-in of each HTTP request with no user configured on the server: an
// app token by basic auth, passed on to OpenCloud as that user, or an access token the OpenID
// Connect provider issued for this server, which is exchanged for one issued for OpenCloud and
// never passed on itself.
package signin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// realm names this server in the basic auth challenge.
const realm = `Basic realm="opencloud-mcp", charset="UTF-8"`

// Options are the ways in.
type Options struct {
	Cloud *opencloud.Cloud
	// OIDC takes access tokens when set; app tokens are always taken.
	OIDC *OIDC
}

// credentials are what a request signed in with, kept in the request's TokenInfo; they print as
// hidden.
type credentials struct {
	user   string
	app    opencloud.Secret
	access opencloud.Secret
}

const credentialsKey = "credentials"

// fmt reads the fields above without their own String, so the struct hides itself whole.
func (credentials) String() string       { return "[hidden]" }
func (credentials) GoString() string     { return "[hidden]" }
func (credentials) LogValue() slog.Value { return slog.StringValue("[hidden]") }

// Handler lets a request reach next once it signed in, with its TokenInfo in the context. Every
// request signs in on its own; nothing of it is kept for the next.
func Handler(next http.Handler, opts Options) http.Handler {
	// the go-sdk sets TokenInfo only through RequireBearerToken, so basic auth goes through it too
	basic := auth.RequireBearerToken(opts.verifyBasic, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(next)

	var bearer http.Handler
	if opts.OIDC != nil {
		bearer = auth.RequireBearerToken(opts.verifyBearer, &auth.RequireBearerTokenOptions{ResourceMetadataURL: opts.OIDC.MetadataURL()})(next)
	}

	// a failed basic auth names the bearer token too, which RequireBearerToken names on its own
	var also string
	if opts.OIDC != nil {
		also = fmt.Sprintf("Bearer resource_metadata=%q", opts.OIDC.MetadataURL())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, payload, _ := strings.Cut(r.Header.Get("Authorization"), " ")

		switch {
		case strings.EqualFold(scheme, "basic"):
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+payload)
			basic.ServeHTTP(&challenging{ResponseWriter: w, also: also}, r)
		case bearer != nil:
			bearer.ServeHTTP(&challenging{ResponseWriter: w}, r)
		default:
			basic.ServeHTTP(&challenging{ResponseWriter: w}, r)
		}
	})
}

// challenging names basic auth in the WWW-Authenticate of a 401, and also a further challenge.
type challenging struct {
	http.ResponseWriter
	also string
}

func (c *challenging) WriteHeader(status int) {
	if status == http.StatusUnauthorized {
		if c.also != "" {
			c.Header().Add("WWW-Authenticate", c.also)
		}

		c.Header().Add("WWW-Authenticate", realm)
	}

	c.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the writer underneath, for streaming.
func (c *challenging) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (o Options) verifyBasic(ctx context.Context, payload string, r *http.Request) (*auth.TokenInfo, error) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: basic auth that is not base64", auth.ErrInvalidToken)
	}

	user, token, ok := strings.Cut(string(raw), ":")
	if !ok || user == "" || token == "" {
		return nil, fmt.Errorf("%w: basic auth needs the user and the app token", auth.ErrInvalidToken)
	}

	info := &auth.TokenInfo{
		UserID: "app-token:" + user,
		Extra:  map[string]any{credentialsKey: credentials{user: user, app: opencloud.Secret(token)}},
	}

	return info, o.checkFirstContact(ctx, r, info)
}

func (o Options) verifyBearer(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	info, err := o.OIDC.verify(ctx, token)
	if err != nil {
		return nil, err
	}

	return info, o.checkFirstContact(ctx, r, info)
}

// checkFirstContact asks OpenCloud for the user's spaces on a client's first request, discovery
// or the older initialize, so credentials OpenCloud refuses end there with a 401.
func (o Options) checkFirstContact(ctx context.Context, r *http.Request, info *auth.TokenInfo) error {
	if m := method(r); m != "server/discover" && m != "initialize" {
		return nil
	}

	c, err := o.Access()(ctx, info)
	if err != nil {
		return err
	}

	_, err = c.Spaces(ctx)
	if errors.Is(err, opencloud.ErrRefused) {
		return fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	return err
}

// Access is how one request reaches OpenCloud: as the identity of its TokenInfo. An access token
// is exchanged once and the exchanged token kept for the request, which matters for a long one
// such as subscriptions/listen, until it expires.
func (o Options) Access() func(context.Context, *auth.TokenInfo) (*opencloud.Client, error) {
	var (
		mu        sync.Mutex
		subject   opencloud.Secret
		exchanged opencloud.Secret
		until     time.Time
	)

	return func(ctx context.Context, info *auth.TokenInfo) (*opencloud.Client, error) {
		if info == nil {
			return nil, errors.New("the request carries no sign-in")
		}

		cred, ok := info.Extra[credentialsKey].(credentials)
		if !ok {
			return nil, errors.New("the request carries no sign-in")
		}

		if cred.app != "" {
			return o.Cloud.AppToken(cred.user, cred.app)
		}

		if o.OIDC == nil {
			return nil, errors.New("this server takes no access tokens")
		}

		mu.Lock()
		defer mu.Unlock()

		if cred.access != subject || time.Now().After(until) {
			token, exp, err := o.OIDC.exchange(ctx, cred.access, o.Cloud.URL())
			if err != nil {
				return nil, err
			}

			subject, exchanged, until = cred.access, token, exp
		}

		return o.Cloud.Bearer(cred.user, exchanged), nil
	}
}

// method is the JSON-RPC method of a request: the Mcp-Method header of 2026-07-28, else read
// from the body, which is put back for the handler.
func method(r *http.Request) string {
	if m := r.Header.Get("Mcp-Method"); m != "" {
		return m
	}

	if r.Body == nil {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPeek))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))

	var msg struct {
		Method string `json:"method"`
	}

	if err != nil || json.Unmarshal(body, &msg) != nil {
		return ""
	}

	return msg.Method
}

// maxPeek bounds how much of a body is read for its method; an initialize is far smaller.
const maxPeek = 64 << 10

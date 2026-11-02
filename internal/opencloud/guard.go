package opencloud

import (
	"net/http"
	"net/url"
	"strings"
)

// guard lets requests reach the configured OpenCloud only, redirects included, and signs each in
// as the client's user, so the credential never goes anywhere else.
type guard struct {
	origin *url.URL
	sign   func(*http.Request)
	next   http.RoundTripper
}

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.EqualFold(r.URL.Scheme, g.origin.Scheme) || !strings.EqualFold(r.URL.Host, g.origin.Host) {
		if r.Body != nil {
			_ = r.Body.Close() // a RoundTripper closes the body even when it refuses
		}

		return nil, fail(ErrForeignOrigin, "a request to %s was stopped: only %s is reached", r.URL.Redacted(), g.origin.Redacted())
	}

	r = r.Clone(r.Context())
	g.sign(r)

	return g.next.RoundTrip(r)
}

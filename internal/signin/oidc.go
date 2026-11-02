package signin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
)

// OIDCConfig is the OpenID Connect provider and how this server is known to it.
type OIDCConfig struct {
	// Issuer is the provider, exactly as its discovery document names it.
	Issuer string
	// Resource is this server's MCP endpoint as clients reach it, like https://mcp.example.com/mcp;
	// an access token must name it as its audience.
	Resource string
	// ClientID and ClientSecret are this server's confidential client at the provider, which may
	// exchange a person's token for one with OpenCloud's audience.
	ClientID     string
	ClientSecret opencloud.Secret
	// HTTPClient reaches the provider; http.DefaultClient when nil.
	HTTPClient *http.Client
}

// OIDC checks access tokens of one provider and exchanges them for OpenCloud.
type OIDC struct {
	cfg      OIDCConfig
	verifier *oidc.IDTokenVerifier
	tokenURL string
}

// NewOIDC discovers the provider of cfg; ctx carries the HTTP client for its keys later on too.
func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDC, error) {
	u, err := url.Parse(cfg.Resource)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Fragment != "" {
		return nil, fmt.Errorf("the resource %q is not this server's address, like https://mcp.example.com/mcp", cfg.Resource)
	}

	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("the server's client at the provider needs its id and secret, for the token exchange")
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}

	ctx = oidc.ClientContext(context.WithoutCancel(ctx), cfg.HTTPClient)

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering the OpenID Connect provider %s: %w", cfg.Issuer, err)
	}

	tokenURL := provider.Endpoint().TokenURL
	if tokenURL == "" {
		return nil, fmt.Errorf("the OpenID Connect provider %s names no token endpoint", cfg.Issuer)
	}

	// the audience is checked against the resource below, not against a client id
	verifier := provider.VerifierContext(ctx, &oidc.Config{SkipClientIDCheck: true})

	return &OIDC{cfg: cfg, verifier: verifier, tokenURL: tokenURL}, nil
}

// MetadataPath is where the protected resource metadata is served, RFC 9728's well-known path.
const MetadataPath = "/.well-known/oauth-protected-resource"

// MetadataURL is the address of the protected resource metadata.
func (o *OIDC) MetadataURL() string {
	u, _ := url.Parse(o.cfg.Resource) // NewOIDC checked it

	return u.Scheme + "://" + u.Host + MetadataPath
}

// Metadata is this server as a protected resource (RFC 9728).
func (o *OIDC) Metadata() *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               o.cfg.Resource,
		AuthorizationServers:   []string{o.cfg.Issuer},
		BearerMethodsSupported: []string{"header"},
		// a client asks for these instead of every scope the provider knows; OpenCloud blanks the
		// mail of a known user whose token has none
		ScopesSupported: []string{"openid", "profile", "email"},
		ResourceName:    "OpenCloud",
	}
}

func (o *OIDC) verify(ctx context.Context, token string) (*auth.TokenInfo, error) {
	t, err := o.verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	if !oauthex.MatchesResource(t.Audience, o.cfg.Resource) {
		return nil, fmt.Errorf("%w: the token is not issued for %s", auth.ErrInvalidToken, o.cfg.Resource)
	}

	var claims struct {
		Scope             string `json:"scope"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
	}

	if err := t.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	who := cmp.Or(claims.PreferredUsername, claims.Email, t.Subject)

	return &auth.TokenInfo{
		Scopes:     strings.Fields(claims.Scope),
		Expiration: t.Expiry,
		UserID:     "oidc:" + t.Issuer + ":" + t.Subject,
		Extra:      map[string]any{credentialsKey: credentials{user: who, access: opencloud.Secret(token)}},
	}, nil
}

// accessToken is the token type URN of an OAuth access token (RFC 8693).
const accessToken = "urn:ietf:params:oauth:token-type:access_token" //nolint:gosec // a token type, not a credential

// exchange trades a person's access token for one issued for OpenCloud (RFC 8693). It is
// oauthex.ExchangeToken without the audience beside the resource, which a provider such as Rauthy
// refuses, and without its demand for issued_token_type, which Rauthy leaves out.
func (o *OIDC) exchange(ctx context.Context, subject opencloud.Secret, opencloudURL string) (opencloud.Secret, time.Time, error) {
	form := url.Values{
		"grant_type":           {oauthex.GrantTypeTokenExchange},
		"requested_token_type": {accessToken},
		"subject_token":        {subject.Reveal()},
		"subject_token_type":   {accessToken},
		"resource":             {opencloudURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("exchanging the token for OpenCloud: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// RFC 6749 2.3.1 form-encodes the client id and secret before basic auth
	req.SetBasicAuth(url.QueryEscape(o.cfg.ClientID), url.QueryEscape(o.cfg.ClientSecret.Reveal()))

	resp, err := o.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("exchanging the token for OpenCloud: %w", err)
	}
	defer resp.Body.Close()

	var answer struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}

	decoded := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer)

	switch {
	case resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError:
		return "", time.Time{}, fmt.Errorf("%w: the provider would not exchange the token for OpenCloud: %d %s %s",
			auth.ErrInvalidToken, resp.StatusCode, answer.Error, answer.Description)
	case resp.StatusCode != http.StatusOK:
		return "", time.Time{}, fmt.Errorf("exchanging the token for OpenCloud: the provider answered %s", resp.Status)
	case decoded != nil || answer.AccessToken == "":
		return "", time.Time{}, errors.New("exchanging the token for OpenCloud: the provider answered no access token")
	}

	until := time.Now().Add(time.Minute)
	if answer.ExpiresIn > 0 {
		until = time.Now().Add(time.Duration(answer.ExpiresIn) * time.Second)
	}

	// a minute before its end the exchanged token counts as gone, so no call starts with one about to expire
	return opencloud.Secret(answer.AccessToken), until.Add(-time.Minute), nil
}

package opencloudtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// alg is the one signing algorithm of the issuer.
const alg = "RS256"

// Issuer is a fake OpenID Connect provider: discovery, its signing key as JWKS, and an RFC 8693
// token exchange for one confidential client. It answers an exchange the way Rauthy does, with
// the target named by resource and without issued_token_type.
type Issuer struct {
	URL string

	srv *httptest.Server
	key *rsa.PrivateKey

	mu                     sync.Mutex
	client, secret, target string

	// Exchanges counts the token exchanges answered with a token.
	Exchanges atomic.Int64
}

// StartIssuer is an issuer serving on a loopback address until Close.
func StartIssuer() (*Issuer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	iss := &Issuer{key: key}
	iss.srv = httptest.NewServer(iss)
	iss.URL = iss.srv.URL

	return iss, nil
}

// Close stops the issuer.
func (iss *Issuer) Close() { iss.srv.Close() }

// AllowExchange lets the client with secret exchange a token for one with the audience target.
func (iss *Issuer) AllowExchange(client, secret, target string) {
	iss.mu.Lock()
	defer iss.mu.Unlock()

	iss.client, iss.secret, iss.target = client, secret, target
}

// Token is an access token of user for the audiences aud that expires after ttl, like the one an
// MCP client gets when it signs the user in.
func (iss *Issuer) Token(user string, aud []string, ttl time.Duration) string {
	return iss.sign(map[string]any{
		"iss": iss.URL, "sub": "sub-" + user, "preferred_username": user, "aud": aud,
		"iat": time.Now().Unix(), "exp": time.Now().Add(ttl).Unix(),
	})
}

// Accepts is how a resource server of audience aud takes a token: the user it names, when the
// issuer signed it for aud and it has not expired.
func (iss *Issuer) Accepts(aud string) func(token string) (string, bool) {
	return func(token string) (string, bool) {
		claims, err := iss.verify(token)
		if err != nil || !slices.Contains(claims.Aud, aud) {
			return "", false
		}

		return claims.User, true
	}
}

type claims struct {
	Sub  string   `json:"sub"`
	User string   `json:"preferred_username"`
	Aud  []string `json:"aud"`
	Exp  int64    `json:"exp"`
}

func (iss *Issuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration", "/.well-known/oauth-authorization-server":
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer": iss.URL, "jwks_uri": iss.URL + "/jwks", "token_endpoint": iss.URL + "/token",
			"authorization_endpoint": iss.URL + "/authorize", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{alg},
		})
	case "/jwks":
		e := big.NewInt(int64(iss.key.E)).Bytes()
		writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": alg, "use": "sig", "kid": "test",
			"n": base64.RawURLEncoding.EncodeToString(iss.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	case "/token":
		iss.exchange(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (iss *Issuer) exchange(w http.ResponseWriter, r *http.Request) {
	refuse := func(code, text string) {
		writeJSON(w, http.StatusBadRequest, map[string]string{keyError: code, "error_description": text})
	}

	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" {
		refuse("unsupported_grant_type", "only the token exchange")

		return
	}

	client, secret, ok := r.BasicAuth()
	if !ok {
		client, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}

	iss.mu.Lock()
	allowed, target := iss.client != "" && client == iss.client && secret == iss.secret, iss.target
	iss.mu.Unlock()

	switch {
	case !allowed:
		writeJSON(w, http.StatusUnauthorized, map[string]string{keyError: "invalid_client"})

		return
	case r.PostForm.Has("code"):
		// Rauthy validates a code wherever it is sent, an empty one too
		refuse("BadRequest", "Payload validation error: code")

		return
	case r.PostForm.Get("audience") != "" && r.PostForm.Get("resource") != "":
		refuse("invalid_target", "'audience' and 'resource' must not be given at the same time")

		return
	case r.PostForm.Get("resource") != target:
		refuse("invalid_target", "not an allowed resource")

		return
	}

	subject, err := iss.verify(r.PostForm.Get("subject_token"))
	if err != nil {
		refuse("invalid_grant", "the given 'subject_token' is not a valid access token")

		return
	}

	iss.Exchanges.Add(1)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": iss.sign(map[string]any{
			"iss": iss.URL, "sub": subject.Sub, "preferred_username": subject.User, "aud": []string{client, target},
			"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		}),
		"token_type": "Bearer", "expires_in": 300,
	})
}

func (iss *Issuer) sign(payload map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT", "kid": "test"}) //nolint:errchkjson // a map of strings
	body, _ := json.Marshal(payload)                                                      //nolint:errchkjson // plain values only

	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(input))

	sig, err := rsa.SignPKCS1v15(nil, iss.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err) // signing with a fresh RSA key does not fail
	}

	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (iss *Issuer) verify(token string) (claims, error) {
	var c claims

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("not a JWT")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return c, err
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&iss.key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return c, err
	}

	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, err
	}

	if err := json.Unmarshal(body, &c); err != nil {
		return c, err
	}

	if time.Now().Unix() >= c.Exp {
		return c, errors.New("expired")
	}

	return c, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// a broken write shows in the test as an answer it did not get
	_ = json.NewEncoder(w).Encode(v) //nolint:errchkjson // maps of plain values only
}

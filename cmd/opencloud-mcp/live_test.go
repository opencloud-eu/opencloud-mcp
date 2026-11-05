package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
	"github.com/opencloud-eu/opencloud-mcp/internal/signin"
)

// The compose stack's addresses and its public local secrets, see compose.yaml.
const (
	liveCloud    = "http://cloud.localhost"
	liveIssuer   = "http://id.localhost/auth/v1/"
	liveResource = "http://mcp.localhost/mcp"
	liveSecret   = "local-compose-opencloud-mcp-client-secret-that-is-64-characters-" //nolint:gosec // the compose stack's public local secret
)

// TestLiveSignInWithOIDC signs alice in at the compose stack's Rauthy, and reaches its OpenCloud
// through this server with the token; it skips without the stack.
func TestLiveSignInWithOIDC(t *testing.T) {
	// the stack is on this machine, never behind a proxy such as the one of CI
	probe := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}

	for _, u := range []string{liveIssuer + ".well-known/openid-configuration", liveCloud + "/status.php"} {
		resp, err := probe.Get(u)
		if err != nil {
			t.Skipf("needs: docker compose up opencloud (%v)", err)
		}

		_ = resp.Body.Close() // only whether it answers matters
	}

	// the password grant takes no resource; the live-test client names the MCP server by default
	resp, err := probe.PostForm(liveIssuer+"oidc/token", url.Values{
		"grant_type": {"password"}, "client_id": {"live-test"}, "username": {"alice@example.com"}, "password": {"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var tok struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("no token for alice: %s %v", resp.Status, err)
	}

	cloud, err := opencloud.New(opencloud.Config{URL: liveCloud})
	if err != nil {
		t.Fatal(err)
	}

	logs := &lockedBuffer{}
	cfg := config{interval: time.Second, oidc: signin.OIDCConfig{
		Issuer: liveIssuer, Resource: liveResource, ClientID: "opencloud-mcp", ClientSecret: liveSecret,
	}}

	h, err := handler(t.Context(), cfg, cloud, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	s := &server{url: srv.URL, log: logs}

	if _, err := s.connect(t, "Bearer not-a-token", nil); err == nil {
		t.Error("a made-up token connected")
	}

	cs := s.must(t, "Bearer "+tok.AccessToken, nil)

	got, isErr := text(t, cs, "list_spaces", nil)
	if isErr || !strings.Contains(got, "alice") && !strings.Contains(got, "Alice") {
		t.Errorf("alice's spaces: %s", got)
	}

	s.neverLogged(t, tok.AccessToken, liveSecret)
}

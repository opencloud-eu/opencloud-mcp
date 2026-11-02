package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud/opencloudtest"
	"github.com/opencloud-eu/opencloud-mcp/internal/signin"
)

const (
	token    = "app-token-4711"
	bobToken = "app-token-0815"
	secret   = "client-secret-of-the-mcp-server"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestTheConfigurationComesFromFlagsOverTheEnvironment(t *testing.T) {
	e := env(map[string]string{envURL: "https://env.example.com", envUser: "alice", envToken: token, envInsecureTLS: "true", envInterval: "1m"})

	cfg, err := configure([]string{"--url", "https://flag.example.com", "--listen", "127.0.0.1:8090"}, e, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.cloud.URL != "https://flag.example.com" || cfg.user != "alice" || cfg.token.Reveal() != token ||
		!cfg.cloud.InsecureTLS || cfg.listen != "127.0.0.1:8090" || cfg.interval != time.Minute {
		t.Errorf("config %+v", cfg)
	}

	cfg, err = configure([]string{"--listen", ":8090"}, env(map[string]string{envURL: "https://cloud.example.com"}), io.Discard)
	if err != nil || !cfg.perRequest() || cfg.interval != 5*time.Second || cfg.toolsets != nil || cfg.readOnly {
		t.Errorf("per request %+v %v", cfg, err)
	}

	e = env(map[string]string{envURL: "https://cloud.example.com", envUser: "alice", envToken: token, envToolsets: "trash", envReadOnly: "true"})

	cfg, err = configure([]string{"--toolsets", " Files, search,,"}, e, io.Discard)
	if err != nil || !slices.Equal(cfg.toolsets, []string{"files", "search"}) || !cfg.readOnly {
		t.Errorf("toolsets %+v %v", cfg, err)
	}
}

func TestTheSecretsAreNeverFlags(t *testing.T) {
	e := env(map[string]string{envURL: "https://cloud.example.com", envUser: "alice"})

	for _, f := range []string{"--token", "--app-token", "--oidc-client-secret"} {
		var out strings.Builder
		if _, err := configure([]string{f, token}, e, &out); err == nil {
			t.Errorf("%s was taken", f)
		}

		if strings.Contains(out.String(), token) {
			t.Errorf("the usage repeated the token: %s", out.String())
		}
	}

	if _, err := configure([]string{"-h"}, e, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("help: %v", err)
	}
}

func TestAMissingOrClashingSettingIsNamed(t *testing.T) {
	oidc := map[string]string{envIssuer: "https://id.example.com", envResource: "https://mcp.example.com/mcp", envClientID: "mcp", envClientSecret: secret}

	with := func(base map[string]string, more map[string]string) map[string]string {
		out := map[string]string{envURL: "https://cloud.example.com"}
		for _, m := range []map[string]string{base, more} {
			for k, v := range m {
				out[k] = v
			}
		}

		return out
	}

	for _, c := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{nil, map[string]string{envUser: "alice", envToken: token}, "no OpenCloud address"},
		{nil, with(nil, map[string]string{envUser: "alice"}), "no app token for alice"},
		{nil, with(nil, map[string]string{envToken: token}), "an app token without its user"},
		{nil, with(nil, nil), "on stdio the server works as one user"},
		{nil, with(map[string]string{envUser: "alice", envToken: token}, map[string]string{envInsecureTLS: "maybe"}), `OPENCLOUD_INSECURE_TLS is "maybe"`},
		{nil, with(map[string]string{envUser: "alice", envToken: token}, map[string]string{envInterval: "often"}), `OPENCLOUD_MCP_WATCH_INTERVAL is "often"`},
		{[]string{"--watch-interval", "0s"}, with(map[string]string{envUser: "alice", envToken: token}, nil), "longer than zero"},
		{[]string{"--toolsets", "files,mail"}, with(map[string]string{envUser: "alice", envToken: token}, nil), `there is no toolset "mail"; there are spaces, files,`},
		{nil, with(map[string]string{envUser: "alice", envToken: token}, map[string]string{envReadOnly: "mostly"}), `OPENCLOUD_MCP_READ_ONLY is "mostly"`},
		{[]string{"--listen", ":0"}, with(oidc, map[string]string{envUser: "alice", envToken: token}), "OpenID Connect is for HTTP without a configured user"},
		{[]string{"--listen", ":0"}, with(oidc, map[string]string{envClientSecret: ""}), "needs all of"},
		{[]string{"--listen", ":8090"}, with(nil, map[string]string{envUser: "alice", envToken: token}), "loopback only, not :8090"},
		{[]string{"--listen", "0.0.0.0:8090"}, with(nil, map[string]string{envUser: "alice", envToken: token}), "loopback only"},
		{[]string{"--listen", "cloud.example.com:8090"}, with(nil, map[string]string{envUser: "alice", envToken: token}), "loopback only"},
	} {
		if _, err := configure(c.args, env(c.env), io.Discard); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v %v: %v", c.args, c.env, err)
		}
	}
}

// server is the HTTP side of cfg on a fake OpenCloud; its log is kept for a look at what it wrote.
type server struct {
	url  string
	fake *opencloudtest.Cloud
	log  *lockedBuffer
}

// alice and bob each own a space; alice's holds documents and other files.
func start(t *testing.T, cfg config, setup func(fake *opencloudtest.Cloud, cloudURL string)) *server {
	t.Helper()

	fake := opencloudtest.New("alice", token)
	fake.Account("bob", bobToken)

	if err := fake.Space("alice-id", "Alice", "personal", map[string]string{
		"/notes.md":          "# Notes of Alice\n\nBuy milk.",
		"/plain.txt":         "No headings here.",
		"/a/b/deep.md":       "Deep down.",
		"/a/other.md":        "Beside the folder b.",
		"/c/far.csv":         "x,y\n1,2",
		"/pictures/cat.png":  "\x89PNG\x00",
		"/releases/v1.json":  `{"version": "1"}`,
		"/with space/odd.md": "# Odd path",
	}); err != nil {
		t.Fatal(err)
	}

	if err := fake.SpaceOf("alice", "empty-id", "Empty", "project", nil); err != nil {
		t.Fatal(err)
	}

	if err := fake.SpaceOf("alice", "docs-id", "Docs", "project", map[string]string{"/guide.md": "# Guide"}); err != nil {
		t.Fatal(err)
	}

	if err := fake.SpaceOf("bob", "bob-id", "Bob", "personal", map[string]string{"/bob.md": "Bob's note."}); err != nil {
		t.Fatal(err)
	}

	cloud := httptest.NewServer(fake)
	t.Cleanup(cloud.Close)

	if setup != nil {
		setup(fake, cloud.URL)
	}

	cfg.cloud.URL = cloud.URL
	if cfg.interval == 0 {
		cfg.interval = 20 * time.Millisecond
	}

	c, err := opencloud.New(cfg.cloud)
	if err != nil {
		t.Fatal(err)
	}

	logs := &lockedBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// the resource names the server's own address, which only exists once it listens
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	url := "http://" + ln.Addr().String()
	if cfg.oidc.Issuer != "" {
		cfg.oidc.Resource = url + "/mcp"
	}

	h, err := handler(t.Context(), cfg, c, log)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	return &server{url: url, fake: fake, log: logs}
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.Buffer.String()
}

// signing puts one Authorization header on every request.
type signing struct{ authorization string }

func (s signing) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if s.authorization != "" {
		r.Header.Set("Authorization", s.authorization)
	}

	return http.DefaultTransport.RoundTrip(r)
}

func basic(user, token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
}

func (s *server) connect(t *testing.T, authorization string, opts *mcp.ClientOptions) (*mcp.ClientSession, error) {
	t.Helper()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, opts).Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: s.url + "/mcp", HTTPClient: &http.Client{Transport: signing{authorization}},
	}, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}

	return cs, err
}

func (s *server) must(t *testing.T, authorization string, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()

	cs, err := s.connect(t, authorization, opts)
	if err != nil {
		t.Fatal(err)
	}

	return cs
}

// post is one raw JSON-RPC request of method.
func (s *server) post(t *testing.T, authorization, method string) *http.Response {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Method", method)
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")

	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

// neverLogged fails when the log holds any of secrets.
func (s *server) neverLogged(t *testing.T, secrets ...string) {
	t.Helper()

	for _, x := range secrets {
		if strings.Contains(s.log.String(), x) {
			t.Errorf("the log holds a secret: %s", s.log.String())
		}
	}
}

func text(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	var b strings.Builder

	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}

	return b.String(), res.IsError
}

func TestOneConfiguredUserIsServedOverStreamableHTTP(t *testing.T) {
	s := start(t, config{user: "alice", token: token}, nil)

	cs := s.must(t, "", nil)
	if got, isErr := text(t, cs, "read_file", map[string]any{"space": "Alice", "path": "plain.txt"}); isErr || got != "No headings here." {
		t.Errorf("read over HTTP %q", got)
	}

	if resp := s.post(t, "", "tools/list"); resp.StatusCode != http.StatusOK {
		t.Errorf("one configured user asks no sign-in, got %d", resp.StatusCode)
	}

	s.neverLogged(t, token)
}

func TestARequestWithoutSignInIsToldBothWays(t *testing.T) {
	iss := issuer(t)
	s := start(t, oidcConfig(iss), trust(iss))

	resp := s.post(t, "", "server/discover")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}

	challenges := strings.Join(resp.Header.Values("WWW-Authenticate"), "\n")
	if !strings.Contains(challenges, `Bearer resource_metadata="`+s.url+`/.well-known/oauth-protected-resource"`) ||
		!strings.Contains(challenges, `Basic realm="opencloud-mcp"`) {
		t.Errorf("challenges %s", challenges)
	}

	resp = s.post(t, basic("alice", "not-the-token"), "server/discover")
	if challenges := strings.Join(resp.Header.Values("WWW-Authenticate"), "\n"); resp.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(challenges, "Bearer resource_metadata=") || !strings.Contains(challenges, "Basic realm=") {
		t.Errorf("a refused app token: %d %s", resp.StatusCode, challenges)
	}

	meta, err := http.Get(s.url + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Body.Close()

	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}

	if err := json.NewDecoder(meta.Body).Decode(&prm); err != nil {
		t.Fatal(err)
	}

	if prm.Resource != s.url+"/mcp" || len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != iss.URL ||
		strings.Join(prm.ScopesSupported, " ") != "openid profile email" {
		t.Errorf("metadata %+v", prm)
	}
}

func TestWithoutOIDCOnlyBasicIsNamed(t *testing.T) {
	s := start(t, config{}, nil)

	resp := s.post(t, "", "server/discover")
	if challenges := resp.Header.Values("WWW-Authenticate"); resp.StatusCode != http.StatusUnauthorized ||
		len(challenges) != 1 || !strings.HasPrefix(challenges[0], "Basic realm=") {
		t.Errorf("%d %v", resp.StatusCode, challenges)
	}

	if resp := s.post(t, "Bearer some-token", "server/discover"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a bearer token without a provider: %d", resp.StatusCode)
	}

	if resp, err := http.Get(s.url + "/.well-known/oauth-protected-resource"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("metadata without a provider: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}
}

func TestEachRequestSignsInWithItsAppToken(t *testing.T) {
	s := start(t, config{}, nil)

	if _, err := s.connect(t, basic("alice", "not-the-token"), nil); err == nil {
		t.Error("a refused app token connected")
	}

	if resp := s.post(t, basic("alice", "not-the-token"), "server/discover"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a refused app token on first contact: %d", resp.StatusCode)
	}

	alice := s.must(t, basic("alice", token), nil)
	bob := s.must(t, basic("bob", bobToken), nil)

	if got, _ := text(t, alice, "list_spaces", nil); !strings.Contains(got, "alice-id") || strings.Contains(got, "bob-id") {
		t.Errorf("alice sees %s", got)
	}

	if got, _ := text(t, bob, "list_spaces", nil); !strings.Contains(got, "bob-id") || strings.Contains(got, "alice-id") {
		t.Errorf("bob sees %s", got)
	}

	s.neverLogged(t, token, bobToken)
}

func issuer(t *testing.T) *opencloudtest.Issuer {
	t.Helper()

	iss, err := opencloudtest.StartIssuer()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(iss.Close)

	return iss
}

func oidcConfig(iss *opencloudtest.Issuer) config {
	return config{oidc: signin.OIDCConfig{Issuer: iss.URL, ClientID: "opencloud-mcp", ClientSecret: secret}}
}

// trust lets the fake OpenCloud take the issuer's tokens for itself and the server exchange for them.
func trust(iss *opencloudtest.Issuer) func(*opencloudtest.Cloud, string) {
	return func(fake *opencloudtest.Cloud, cloudURL string) {
		fake.TrustBearer(iss.Accepts(cloudURL))
		iss.AllowExchange("opencloud-mcp", secret, cloudURL)
	}
}

func TestAnAccessTokenIsExchangedNeverPassedOn(t *testing.T) {
	iss := issuer(t)
	s := start(t, oidcConfig(iss), trust(iss))

	foreign := iss.Token("alice", []string{"https://other.example.com/mcp"}, time.Minute)
	if _, err := s.connect(t, "Bearer "+foreign, nil); err == nil {
		t.Error("a token for another audience connected")
	}

	expired := iss.Token("alice", []string{s.url + "/mcp"}, -time.Minute)
	if resp := s.post(t, "Bearer "+expired, "server/discover"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an expired token: %d", resp.StatusCode)
	}

	if iss.Exchanges.Load() != 0 {
		t.Errorf("a refused token was exchanged")
	}

	access := iss.Token("alice", []string{s.url + "/mcp"}, time.Hour)
	cs := s.must(t, "Bearer "+access, nil)

	if got, isErr := text(t, cs, "read_file", map[string]any{"space": "Alice", "path": "plain.txt"}); isErr || got != "No headings here." {
		t.Errorf("read with an access token: %q", got)
	}

	if iss.Exchanges.Load() == 0 {
		t.Error("the access token reached OpenCloud without an exchange")
	}

	// OpenCloud takes only tokens for itself, so the person's own would have been refused
	if s.fake.Wrong.Load() != 0 {
		t.Errorf("OpenCloud refused %d requests", s.fake.Wrong.Load())
	}

	s.neverLogged(t, access, foreign, secret)
}

func TestTheDocumentsAreResources(t *testing.T) {
	s := start(t, config{}, nil)
	cs := s.must(t, basic("alice", token), nil)

	var pages [][]*mcp.Resource

	cursor := ""

	for {
		res, err := cs.ListResources(t.Context(), &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}

		pages = append(pages, res.Resources)

		if cursor = res.NextCursor; cursor == "" {
			break
		}
	}

	// the first page leads with the search's query language
	if len(pages) != 2 || len(pages[0]) != 7 || len(pages[1]) != 1 || pages[0][0].URI != "opencloud:///search/kql" {
		t.Fatalf("pages %v", pages)
	}

	byName := map[string]*mcp.Resource{}
	for _, r := range pages[0] {
		byName[r.Name] = r
	}

	language, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "opencloud:///search/kql"})
	if err != nil || len(language.Contents) != 1 || !strings.Contains(language.Contents[0].Text, "`tag:(\"foo-bar\")` finds invoice.txt") {
		t.Errorf("the query language %v %v", language, err)
	}

	notes := byName["notes.md"]
	if notes == nil || notes.URI != "opencloud:///spaces/alice-id/notes.md" || notes.Title != "Notes of Alice" ||
		notes.MIMEType != "text/markdown" || notes.Size != int64(len("# Notes of Alice\n\nBuy milk.")) {
		t.Errorf("notes %+v", notes)
	}

	if notes.Annotations == nil {
		t.Fatal("no lastModified")
	}

	if _, err := time.Parse(time.RFC3339, notes.Annotations.LastModified); err != nil {
		t.Errorf("lastModified %q: %v", notes.Annotations.LastModified, err)
	}

	if odd := byName["with space/odd.md"]; odd == nil || odd.URI != "opencloud:///spaces/alice-id/with%20space/odd.md" || odd.Title != "Odd path" {
		t.Errorf("odd %+v", odd)
	}

	if csv := byName["c/far.csv"]; csv == nil || csv.MIMEType != "text/csv" || csv.Title != "far.csv" {
		t.Errorf("csv %+v", csv)
	}

	if byName["pictures/cat.png"] != nil || byName["releases/v1.json"] != nil {
		t.Error("a file that is no document is listed")
	}

	if g := pages[1][0]; g.URI != "opencloud:///spaces/docs-id/guide.md" {
		t.Errorf("second page %+v", g)
	}

	read, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "opencloud:///spaces/alice-id/with%20space/odd.md"})
	if err != nil || len(read.Contents) != 1 || read.Contents[0].Text != "# Odd path" || read.Contents[0].MIMEType != "text/markdown" {
		t.Errorf("read %+v %v", read, err)
	}

	for _, missing := range []string{
		"opencloud:///spaces/alice-id/gone.md",
		"opencloud:///spaces/bob-id/bob.md",
		"opencloud:///spaces/alice-id/pictures/cat.png",
	} {
		var rpc *jsonrpc.Error
		if _, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: missing}); !errors.As(err, &rpc) || rpc.Code != -32002 {
			t.Errorf("%s: %v", missing, err)
		}
	}

	templates, err := cs.ListResourceTemplates(t.Context(), nil)
	if err != nil || len(templates.ResourceTemplates) != 1 || templates.ResourceTemplates[0].URITemplate != "opencloud:///spaces/{space}/{+path}" {
		t.Errorf("templates %+v %v", templates, err)
	}
}

func TestTheDocumentsOfOneSpaceAreListedByATool(t *testing.T) {
	s := start(t, config{}, nil)
	cs := s.must(t, basic("alice", token), nil)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_documents", Arguments: map[string]any{"space": "Docs"}})
	if err != nil || res.IsError {
		t.Fatalf("%+v %v", res, err)
	}

	var shelf struct {
		ETag      string `json:"etag"`
		Documents []struct {
			URI   string `json:"uri"`
			Title string `json:"title"`
		} `json:"documents"`
	}

	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(b, &shelf); err != nil {
		t.Fatal(err)
	}

	if shelf.ETag == "" || len(shelf.Documents) != 1 || shelf.Documents[0].URI != "opencloud:///spaces/docs-id/guide.md" || shelf.Documents[0].Title != "Guide" {
		t.Errorf("shelf %s", b)
	}

	if link, ok := res.Content[0].(*mcp.ResourceLink); !ok || link.URI != "opencloud:///spaces/docs-id/guide.md" {
		t.Errorf("content %+v", res.Content[0])
	}

	if got, _ := text(t, cs, "list_documents", map[string]any{"space": "Empty"}); got != "The space Empty holds no documents." {
		t.Errorf("empty %q", got)
	}
}

func TestAChangeIsToldToTheListen(t *testing.T) {
	s := start(t, config{}, nil)

	updated := make(chan string, 10)
	changed := make(chan struct{}, 10)

	cs := s.must(t, basic("alice", token), &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, r *mcp.ResourceUpdatedNotificationRequest) { updated <- r.Params.URI },
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) {
			changed <- struct{}{}
		},
	})

	deep := "opencloud:///spaces/alice-id/a/b/deep.md"
	if err := cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: deep}); err != nil {
		t.Fatal(err)
	}

	// both listens take their first look before anything changes
	time.Sleep(200 * time.Millisecond)

	if err := s.fake.Put("alice-id", "/a/other.md", "Beside b, changed."); err != nil {
		t.Fatal(err)
	}

	wait(t, changed, "the list change")

	before := s.fake.Listed("alice-id", "/a/b")

	time.Sleep(200 * time.Millisecond)

	if got := s.fake.Listed("alice-id", "/a/b"); got != before {
		t.Errorf("the folder a/b did not move, yet was listed %d times more", got-before)
	}

	select {
	case u := <-updated:
		t.Errorf("updated %s without a change", u)
	default:
	}

	if err := s.fake.Put("alice-id", "/a/b/deep.md", "Deeper."); err != nil {
		t.Fatal(err)
	}

	if u := wait(t, updated, "the update"); u != deep {
		t.Errorf("updated %s", u)
	}

	if err := cs.Unsubscribe(t.Context(), &mcp.UnsubscribeParams{URI: deep}); err != nil {
		t.Fatal(err)
	}

	s.neverLogged(t, token)
}

func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no word of %s", what)
	}

	var zero T

	return zero
}

func TestAClientOfAnOlderRevisionSignsInToo(t *testing.T) {
	s := start(t, config{}, nil)

	connect := func(authorization string) (*mcp.ClientSession, error) {
		return mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{
			Endpoint: s.url + "/mcp", HTTPClient: &http.Client{Transport: signing{authorization}},
		}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	}

	if _, err := connect(basic("alice", "not-the-token")); err == nil {
		t.Error("a refused app token initialized")
	}

	cs, err := connect(basic("alice", token))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cs.Close() })

	if v := cs.InitializeResult().ProtocolVersion; v != "2025-11-25" {
		t.Errorf("revision %s", v)
	}

	if got, isErr := text(t, cs, "read_file", map[string]any{"space": "Alice", "path": "plain.txt"}); isErr || got != "No headings here." {
		t.Errorf("read %q", got)
	}
}

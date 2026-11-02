// Command opencloud-mcp is an MCP server for OpenCloud: it serves the spaces of OpenCloud users to
// MCP clients, on stdio as one configured user, or as streamable HTTP with --listen, there as one
// configured user or as whoever signs in with each request.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/opencloud-eu/opencloud-mcp/internal/opencloud"
	"github.com/opencloud-eu/opencloud-mcp/internal/signin"
	"github.com/opencloud-eu/opencloud-mcp/internal/tools"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// The environment the server reads; the secrets are read from there only, never from a flag.
const (
	envURL          = "OPENCLOUD_URL"
	envUser         = "OPENCLOUD_USER"
	envToken        = "OPENCLOUD_APP_TOKEN" //nolint:gosec // the variable's name, not a credential
	envInsecureTLS  = "OPENCLOUD_INSECURE_TLS"
	envListen       = "OPENCLOUD_MCP_LISTEN"
	envIssuer       = "OPENCLOUD_MCP_OIDC_ISSUER"
	envResource     = "OPENCLOUD_MCP_RESOURCE"
	envClientID     = "OPENCLOUD_MCP_OIDC_CLIENT_ID"
	envClientSecret = "OPENCLOUD_MCP_OIDC_CLIENT_SECRET" //nolint:gosec // the variable's name, not a credential
	envInterval     = "OPENCLOUD_MCP_WATCH_INTERVAL"
	envToolsets     = "OPENCLOUD_MCP_TOOLSETS"
	envReadOnly     = "OPENCLOUD_MCP_READ_ONLY"
)

type config struct {
	cloud    opencloud.Config
	user     string
	token    opencloud.Secret
	listen   string
	oidc     signin.OIDCConfig
	interval time.Duration
	toolsets []string
	readOnly bool
	version  bool
}

// perRequest says whether each HTTP request signs in on its own, with no user configured.
func (c config) perRequest() bool { return c.user == "" }

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := configure(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	} else if err != nil {
		fmt.Fprintln(os.Stderr, "opencloud-mcp:", err)
		os.Exit(2)
	}

	if cfg.version {
		fmt.Println(version)

		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, log); err != nil {
		log.Error("opencloud-mcp", "err", err)
		os.Exit(1)
	}
}

// configure reads the flags, each falling back to its environment variable, and the secrets from
// the environment.
func configure(args []string, getenv func(string) string, out io.Writer) (config, error) {
	var cfg config

	insecure, err := envBool(getenv, envInsecureTLS)
	if err != nil {
		return cfg, err
	}

	readOnly, err := envBool(getenv, envReadOnly)
	if err != nil {
		return cfg, err
	}

	var toolsets string

	interval := 5 * time.Second
	if v := getenv(envInterval); v != "" {
		if interval, err = time.ParseDuration(v); err != nil {
			return cfg, fmt.Errorf("%s is %q, not a duration like 5s", envInterval, v)
		}
	}

	fs := flag.NewFlagSet("opencloud-mcp", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&cfg.cloud.URL, "url", getenv(envURL), "the OpenCloud address, like https://cloud.example.com (env "+envURL+")")
	fs.StringVar(&cfg.user, "user", getenv(envUser), "the user the app token belongs to; without it, each HTTP request signs in (env "+envUser+")")
	fs.BoolVar(&cfg.cloud.InsecureTLS, "insecure-tls", insecure, "skip TLS certificate checks of OpenCloud and the OpenID Connect provider, for a local instance only (env "+envInsecureTLS+")")
	fs.StringVar(&cfg.listen, "listen", getenv(envListen), "serve streamable HTTP on this address, like 127.0.0.1:8090, instead of stdio (env "+envListen+")")
	fs.StringVar(&cfg.oidc.Issuer, "oidc-issuer", getenv(envIssuer), "the OpenID Connect provider people sign in at, as its discovery names it (env "+envIssuer+")")
	fs.StringVar(&cfg.oidc.Resource, "resource", getenv(envResource), "this server's MCP endpoint as clients reach it, like https://mcp.example.com/mcp (env "+envResource+")")
	fs.StringVar(&cfg.oidc.ClientID, "oidc-client-id", getenv(envClientID), "this server's client at the provider, for the token exchange (env "+envClientID+")")
	fs.DurationVar(&cfg.interval, "watch-interval", interval, "how often an open subscription looks at the spaces for changes (env "+envInterval+")")
	fs.StringVar(&toolsets, "toolsets", getenv(envToolsets), "the groups of tools to offer, comma separated, all when empty: "+strings.Join(tools.Toolsets, ", ")+" (env "+envToolsets+")")
	fs.BoolVar(&cfg.readOnly, "read-only", readOnly, "offer only the tools that change nothing (env "+envReadOnly+")")
	fs.BoolVar(&cfg.version, "version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage: opencloud-mcp [flags]\n\nThe app token is read from %s only, the client secret from %s only.\n\n", envToken, envClientSecret)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected arguments %q; the app token goes in %s", fs.Args(), envToken)
	}

	for name := range strings.SplitSeq(toolsets, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name == "" {
			continue
		}

		if !slices.Contains(tools.Toolsets, name) {
			return cfg, fmt.Errorf("there is no toolset %q; there are %s", name, strings.Join(tools.Toolsets, ", "))
		}

		cfg.toolsets = append(cfg.toolsets, name)
	}

	cfg.token = opencloud.Secret(getenv(envToken))
	cfg.oidc.ClientSecret = opencloud.Secret(getenv(envClientSecret))

	if cfg.version {
		return cfg, nil
	}

	return cfg, check(cfg)
}

func check(cfg config) error {
	oidc := cfg.oidc.Issuer != "" || cfg.oidc.Resource != "" || cfg.oidc.ClientID != "" || cfg.oidc.ClientSecret != ""

	switch {
	case cfg.cloud.URL == "":
		return fmt.Errorf("no OpenCloud address: set --url or %s", envURL)
	case cfg.interval <= 0:
		return errors.New("the watch interval must be longer than zero")
	case cfg.user != "" && cfg.token == "":
		return fmt.Errorf("no app token for %s: set %s", cfg.user, envToken)
	case cfg.user == "" && cfg.token != "":
		return fmt.Errorf("an app token without its user: set --user or %s", envUser)
	case cfg.listen == "" && cfg.perRequest():
		return fmt.Errorf("on stdio the server works as one user: set --user or %s and %s", envUser, envToken)
	case oidc && !cfg.perRequest():
		return errors.New("sign-in with OpenID Connect is for HTTP without a configured user; leave out --user and the app token")
	case oidc && (cfg.oidc.Issuer == "" || cfg.oidc.Resource == "" || cfg.oidc.ClientID == "" || cfg.oidc.ClientSecret == ""):
		return fmt.Errorf("sign-in with OpenID Connect needs all of --oidc-issuer, --resource, --oidc-client-id and %s", envClientSecret)
	case cfg.listen != "" && !cfg.perRequest() && !loopback(cfg.listen):
		return fmt.Errorf("as one user the endpoint takes no sign-in, so it listens on loopback only, not %s: "+
			"listen on 127.0.0.1, or leave out --user and the app token for sign-in per request", cfg.listen)
	}

	return nil
}

// loopback reports whether the listen address is this machine only; an address net.Listen cannot
// take passes, for its own error.
func loopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return true
	}

	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

func envBool(getenv func(string) string, key string) (bool, error) {
	v := getenv(key)
	if v == "" {
		return false, nil
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s is %q, not true or false", key, v)
	}

	return b, nil
}

func run(ctx context.Context, cfg config, log *slog.Logger) error {
	cloud, err := opencloud.New(cfg.cloud)
	if err != nil {
		return err
	}

	if cfg.cloud.InsecureTLS {
		log.Warn("TLS certificates of OpenCloud and the OpenID Connect provider are not checked", "url", cfg.cloud.URL)
	}

	if cfg.listen == "" {
		c, err := cloud.AppToken(cfg.user, cfg.token)
		if err != nil {
			return err
		}

		s := tools.New(tools.Options{Access: tools.As(c), Version: version, Interval: cfg.interval, Log: log, Toolsets: cfg.toolsets, ReadOnly: cfg.readOnly})
		if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			return err
		}

		return nil
	}

	h, err := handler(ctx, cfg, cloud, log)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return err
	}

	addr := ln.Addr().String()

	switch {
	case !cfg.perRequest():
		log.Info("serving MCP over streamable HTTP as one user", "url", "http://"+addr+"/mcp", "opencloud", cfg.cloud.URL, "user", cfg.user)
	case cfg.oidc.Issuer != "":
		log.Info("serving MCP over streamable HTTP, signing in with an app token or OpenID Connect", "url", "http://"+addr+"/mcp",
			"opencloud", cfg.cloud.URL, "issuer", cfg.oidc.Issuer, "resource", cfg.oidc.Resource)
	default:
		log.Info("serving MCP over streamable HTTP, signing in with an app token", "url", "http://"+addr+"/mcp", "opencloud", cfg.cloud.URL)
	}

	return serve(ctx, h, ln)
}

// handler is the HTTP side: streamable MCP at /mcp, stateless as the 2026-07-28 revision has it,
// behind the sign-in when no user is configured, and the protected resource metadata when people
// sign in with OpenID Connect.
func handler(ctx context.Context, cfg config, cloud *opencloud.Cloud, log *slog.Logger) (http.Handler, error) {
	mux := http.NewServeMux()
	opts := &mcp.StreamableHTTPOptions{Stateless: true, Logger: log}

	if !cfg.perRequest() {
		c, err := cloud.AppToken(cfg.user, cfg.token)
		if err != nil {
			return nil, err
		}

		mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return tools.New(tools.Options{Access: tools.As(c), Version: version, Interval: cfg.interval, Log: log, Toolsets: cfg.toolsets, ReadOnly: cfg.readOnly})
		}, opts)
		// a web page the user visits cannot call the tools from the browser
		mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(mcpHandler))

		return mux, nil
	}

	in := signin.Options{Cloud: cloud}

	if cfg.oidc.Issuer != "" {
		oc := cfg.oidc
		oc.HTTPClient = providerClient(cfg.cloud.InsecureTLS)

		o, err := signin.NewOIDC(ctx, oc)
		if err != nil {
			return nil, err
		}

		in.OIDC = o
		mux.Handle(signin.MetadataPath, auth.ProtectedResourceMetadataHandler(o.Metadata()))
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return tools.New(tools.Options{Access: tools.Access(in.Access()), Version: version, Interval: cfg.interval, Log: log, Toolsets: cfg.toolsets, ReadOnly: cfg.readOnly})
	}, opts)
	mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(signin.Handler(mcpHandler, in)))

	return mux, nil
}

// providerClient reaches the OpenID Connect provider, past its own certificate when insecure.
func providerClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's default
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opted into for a local instance's own certificate
	}

	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

// serve serves h on ln until ctx ends.
func serve(ctx context.Context, h http.Handler, ln net.Listener) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}

	done := make(chan error, 1)

	go func() { done <- srv.Serve(ln) }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// an open subscriptions/listen holds its connection until the shutdown gives up on it
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	return nil
}

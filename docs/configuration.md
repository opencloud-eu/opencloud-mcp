# Configuration

| Flag               | Environment                        | Meaning                                                                          |
| ------------------ | ---------------------------------- | -------------------------------------------------------------------------------- |
| `--url`            | `OPENCLOUD_URL`                    | the OpenCloud address, like `https://cloud.example.com`                          |
| `--user`           | `OPENCLOUD_USER`                   | the user the app token belongs to; without it each HTTP request signs in         |
|                    | `OPENCLOUD_APP_TOKEN`              | the user's app token; read from the environment only, never a flag               |
| `--insecure-tls`   | `OPENCLOUD_INSECURE_TLS`           | skip TLS certificate checks of OpenCloud and the provider, local instances only  |
| `--listen`         | `OPENCLOUD_MCP_LISTEN`             | serve streamable HTTP on this address instead of stdio                           |
| `--oidc-issuer`    | `OPENCLOUD_MCP_OIDC_ISSUER`        | the OpenID Connect provider people sign in at, exactly as its discovery names it |
| `--resource`       | `OPENCLOUD_MCP_RESOURCE`           | this server's MCP endpoint as clients reach it, like `https://mcp.example.com/mcp` |
| `--oidc-client-id` | `OPENCLOUD_MCP_OIDC_CLIENT_ID`     | this server's confidential client at the provider, for the token exchange       |
|                    | `OPENCLOUD_MCP_OIDC_CLIENT_SECRET` | that client's secret; read from the environment only, never a flag               |
| `--watch-interval` | `OPENCLOUD_MCP_WATCH_INTERVAL`     | how often an open subscription looks for changes, `5s` by default                |
| `--toolsets`       | `OPENCLOUD_MCP_TOOLSETS`           | the groups of tools to offer, comma separated, all by default; see [Tools](tools.md) |
| `--read-only`      | `OPENCLOUD_MCP_READ_ONLY`          | offer only the tools that change nothing                                         |

A flag wins over its environment variable. No token or secret is ever logged or printed.

The settings make one of three modes:

- stdio: `--url`, `--user` and the app token.
- HTTP as one user: the same with `--listen`. The endpoint takes no sign-in, anyone who reaches it
  acts as that user, so it listens on loopback only (`127.0.0.1`, `[::1]`, `localhost`).
- HTTP with sign-in per request: `--listen` without `--user`. App tokens are always taken, the four
  OpenID Connect settings add MCP authorization, all four or none; see [Sign-in](sign-in.md).

## App tokens

Create an app token through OpenCloud's auth-app service (see its
[README](https://github.com/opencloud-eu/opencloud/blob/main/services/auth-app/README.md)), for
example on the server with:

``` console
opencloud auth-app create --user-name=alice --expiration=720h
```

or, signed in as the user, with a `POST` to `/auth-app/tokens?expiry=720h`.

App tokens work with basic auth behind any identity provider, and revoking one does not touch the
account's password. In the stdio and one-user modes the token is the server's; with sign-in per
request it suits a service account an admin creates for an automated client, while people sign in
through OpenID Connect.

Every request goes to the configured OpenCloud only: a request or redirect to any other host is
stopped before it leaves, so the token cannot be sent anywhere else.

## Connect a client

### Claude Desktop (stdio)

Open `opencloud-mcp-<version>.mcpb` from the release, an MCP bundle with the binaries for macOS,
Windows and Linux. Claude Desktop asks for the address, the user, the app token and whether to
offer only the tools that change nothing; the token goes into the system's keychain.

Or, with the binary, in `claude_desktop_config.json`:

``` json
{
  "mcpServers": {
    "opencloud": {
      "command": "/path/to/opencloud-mcp",
      "env": {
        "OPENCLOUD_URL": "https://cloud.example.com",
        "OPENCLOUD_USER": "alice",
        "OPENCLOUD_APP_TOKEN": "<app token>"
      }
    }
  }
}
```

### Claude Code (stdio)

``` console
claude mcp add opencloud \
  -e OPENCLOUD_URL=https://cloud.example.com -e OPENCLOUD_USER=alice -e OPENCLOUD_APP_TOKEN=<app token> \
  -- /path/to/opencloud-mcp
```

### Streamable HTTP, one user

``` console
OPENCLOUD_URL=https://cloud.example.com OPENCLOUD_USER=alice OPENCLOUD_APP_TOKEN=<app token> \
  opencloud-mcp --listen 127.0.0.1:8090

claude mcp add --transport http opencloud http://127.0.0.1:8090/mcp
```

### Streamable HTTP, sign-in per request

``` console
OPENCLOUD_URL=https://cloud.example.com \
OPENCLOUD_MCP_OIDC_ISSUER=https://id.example.com/ OPENCLOUD_MCP_RESOURCE=https://mcp.example.com/mcp \
OPENCLOUD_MCP_OIDC_CLIENT_ID=opencloud-mcp OPENCLOUD_MCP_OIDC_CLIENT_SECRET=<client secret> \
  opencloud-mcp --listen :8090
```

An MCP client that does MCP authorization finds the provider through the 401 and signs the person
in there. At the provider, the client needs `--resource` among the resources it may ask for, and
the server's client may exchange tokens for the OpenCloud address.

Requests from web pages in a browser are refused (cross-origin protection), and so are requests
to a loopback address under another host name (DNS rebinding).

With the container image, stdio needs `docker run -i`:

``` console
docker run -i --rm -e OPENCLOUD_URL -e OPENCLOUD_USER -e OPENCLOUD_APP_TOKEN opencloud-mcp:dev
```

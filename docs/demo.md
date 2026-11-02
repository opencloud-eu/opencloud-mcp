# Demo

`compose.yaml` runs a demo on this machine: OpenCloud, Rauthy as its provider, this server with
sign-in per request, [LibreChat](https://github.com/danny-avila/LibreChat) as a chat client,
[Collabora Online](https://www.collaboraonline.com) for `open_in_app` and `create_document`, and
[Apache Tika](https://tika.apache.org), so that search finds the words inside documents.
Every password is `demo`. Plain HTTP with public secrets: local use only.

1. `cp .env.example .env` and fill in the model API.

2. `mise run demo`: all of it in the foreground, Ctrl-C stops it and the data stays
3. Open http://localhost and sign in as `alice@example.com` / `demo`. A new chat starts with the
   model spec OpenCloud, which has the MCP server opencloud selected; on its first use, sign in at
   Rauthy with the same `alice@example.com` / `demo`. Another model of the API needs opencloud
   picked in the chat's MCP menu.

| Address                     | What        | Sign in                                                |
| --------------------------- | ----------- | ------------------------------------------------------ |
| http://localhost            | LibreChat   | `alice@example.com`, `demo`                            |
| http://cloud.localhost      | OpenCloud   | `alice@example.com` or `bob@example.com`, `demo`       |
| http://id.localhost/auth/v1 | Rauthy      | `admin@localhost`, `demo`                              |
| http://mcp.localhost/mcp    | this server | through LibreChat, or `mise run demo:tokens`           |
| http://collabora.localhost  | Collabora   | through OpenCloud                                      |

A [Caddy](https://caddyserver.com) proxy on `127.0.0.1:80` serves all of them, so port 80 must be
free. Open each address exactly as written: OpenCloud's web UI loads its config from
`cloud.localhost` and fails under another name with "Missing or invalid config", and Rauthy lets
a dynamically registered client such as LibreChat redirect to plain HTTP on `localhost` only,
which is why LibreChat has no name of its own. For the same reason LibreChat keeps its own
sign-in: its OpenID login takes no plain HTTP provider.

Rauthy takes its clients from `compose/rauthy/bootstrap` on the first start only; after a change
there, `mise run demo:reset` starts the demo afresh.

In a second terminal, while it runs:

``` console
mise run demo:tokens   # alice's and bob's app tokens as exports
mise run demo:reset    # stops it and deletes the data and the server's image
```

Collabora signs its requests to OpenCloud with a WOPI proof key, made once by
`collabora-proof-key` and kept in a volume.

`DEFAULT_MODELS` takes several models, comma separated, and lists them even when the API's
`/models` names none, as a gateway's wildcard route does. `OPENAI_API_HEADERS` adds headers, like
`{"x-example":"demo"}`. Compose writes all of it into LibreChat's config on every
`docker compose up`, with this server as an MCP server behind OAuth 2.1; LibreChat registers
itself at Rauthy through the server's protected resource metadata.

alice and bob each get an app token valid for a hundred years, made once and kept in a volume;
`mise run demo:tokens` prints them as two `export` lines, alice as `OPENCLOUD_USER` and
bob as `OPENCLOUD_PEER_USER`, ready for basic auth, stdio or the live test. Both share the project space Handbook, alice as its
manager and bob as a viewer, with `guides/onboarding.md` in it. alice is a space admin, who may
create project spaces, and bob is in the group Support. OpenCloud's `admin` sets it up on the
first start.

To work on the server itself, run only OpenCloud and its provider, and the server from source on
port 9202:

``` console
docker compose up opencloud app-tokens
OPENCLOUD_URL=http://cloud.localhost OPENCLOUD_MCP_OIDC_ISSUER=http://id.localhost/auth/v1/ \
OPENCLOUD_MCP_RESOURCE=http://mcp.localhost:9202/mcp OPENCLOUD_MCP_OIDC_CLIENT_ID=opencloud-mcp \
OPENCLOUD_MCP_OIDC_CLIENT_SECRET=local-compose-opencloud-mcp-client-secret-that-is-64-characters- \
  go run ./cmd/opencloud-mcp --listen 127.0.0.1:9202
```

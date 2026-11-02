# Sign-in

With sign-in per request every HTTP request carries its own credentials; nothing is kept from one
request to the next, so there is no session to bind a user to.

- `Authorization: Basic base64(user:app token)`: the app token goes to OpenCloud as that user.
- `Authorization: Bearer <access token>`: MCP authorization (revision 2025-11-25 and later), with
  this server as the resource server only. The token must be signed by the provider's keys, name
  it as issuer, not be expired, and name `--resource` as its audience. It is never passed on:
  the server exchanges it at the provider (RFC 8693, its own client id and secret, `resource` =
  the OpenCloud address) for a token issued for OpenCloud, and calls OpenCloud with that.

| Request                                             | Answer                                                                                              |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| no credentials                                      | `401`, `WWW-Authenticate: Bearer resource_metadata="..."` (with OIDC) and `Basic realm="opencloud-mcp"` |
| a malformed, foreign, expired or wrong-audience token | `401`, same challenges                                                                            |
| credentials OpenCloud refuses                       | `401` on the client's first request (`server/discover` or `initialize`), checked by listing the spaces |
| a token the provider will not exchange              | `401`                                                                                               |

The protected resource metadata (RFC 9728) is at `/.well-known/oauth-protected-resource`: the
resource, the provider as authorization server, and the scopes `openid profile email`.

OpenCloud's proxy takes a bearer token from its configured issuer: it checks the JWT's signature,
its audience when `PROXY_OIDC_AUDIENCES` is set, and then asks the userinfo endpoint who it is
(`services/proxy/pkg/middleware/oidc_auth.go`, `pkg/oidc/client.go`). Behind it the provider must
therefore issue tokens for OpenCloud on exchange. OpenCloud's built-in provider cannot: it knows
only the authorization code and refresh token grants and no dynamic client registration
(`services/idp`). The [demo](demo.md) uses [Rauthy](https://github.com/sebadob/rauthy), which does
both; any provider with RFC 8693 exchange and RFC 8707 resources works the same way.

## Without a provider of its own

With OpenCloud's built-in provider, or any setup where the provider cannot exchange tokens, the
server takes app tokens only: stdio, HTTP as one user, or HTTP with sign-in per request and
`Authorization: Basic` alone, without the four OpenID Connect settings. OpenCloud 9.0.0 runs its
auth-app service and takes app tokens (`PROXY_ENABLE_APP_AUTH`) by default; each person, or an
admin for a service account, makes a token as [Configuration](configuration.md#app-tokens) shows.

App tokens stay useful beside a provider too: for stdio, for a client without MCP authorization,
and for an automated client. People with a client that does MCP authorization need none.

## Another provider

What the server asks of a provider, in the order a sign-in meets it:

- discovery: OpenID Connect or RFC 8414 metadata at the issuer, with the token endpoint and the
  keys; `--oidc-issuer` is the issuer exactly as the metadata names it.
- the MCP client's registration: dynamic (RFC 7591), a client ID metadata document, or a client
  made by hand that the MCP client is configured with; plain HTTP redirects only where the
  provider allows them.
- the MCP client's token: a signed JWT whose `aud` holds `--resource`, from the RFC 8707
  `resource` the client sends or a fixed audience.
- the exchange: this server's confidential client (`--oidc-client-id` and its secret) may exchange
  that token (RFC 8693, subject and requested type `access_token`); the server sends
  `resource` = the OpenCloud address.
- OpenCloud's side: `OC_OIDC_ISSUER` is the same issuer, `PROXY_OIDC_AUDIENCES` (if set) holds an
  audience of the exchanged token, and the provider's userinfo answers for the exchanged token,
  with the claim `PROXY_USER_OIDC_CLAIM` names.

### Keycloak

Taken from Keycloak's guides for 26.6 and later
([MCP](https://www.keycloak.org/securing-apps/mcp-authz-server),
[token exchange](https://www.keycloak.org/securing-apps/token-exchange)), not run against this
server:

- the resource parameter is experimental, `--features=resource-indicators`; without it Keycloak
  ignores `resource`, and the audiences come from client scopes with an Audience mapper instead.
- the MCP clients' tokens: a client scope, default for the clients that register, with an
  Audience mapper that adds `--resource` as Included Custom Audience and `opencloud-mcp` as
  Included Client Audience. Keycloak's standard exchange takes only a token whose `aud` holds the
  client that exchanges it.
- this server's client: confidential, with Standard token exchange switched on
  (`token-exchange-standard:v2` is on by default), and an Audience mapper that adds OpenCloud's
  audience to the tokens it gets; the `resource` the server sends does not set it.
- registration: dynamic registration through the Client Registration policies of anonymous
  access (Trusted Hosts, Allowed Client Scopes), or client ID metadata documents with
  `--features=cimd` and a client policy with the `client-id-metadata-document` executor.

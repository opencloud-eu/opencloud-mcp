# Development

``` console
mise run test       # unit tests against an in-repo fake OpenCloud, and the live test
mise run test:live  # the same with the live test against the demo, as alice and bob
mise run lint       # go vet and golangci-lint
mise run run        # the server on stdio
```

The live test runs against a local OpenCloud (`OPENCLOUD_URL`, the demo's `http://cloud.localhost`
when unset; it only accepts a loopback, `localhost` or `.test` address) with `OPENCLOUD_USER` and
`OPENCLOUD_APP_TOKEN` set, and `OPENCLOUD_INSECURE_TLS=true` for its own certificate. It writes
files into the user's personal space, searches them with every filter of `search`, goes through
their versions and the trash, and removes them again. Without such an instance it skips with
`needs: opencloud`. Sharing needs a second user, `OPENCLOUD_PEER_USER` and
`OPENCLOUD_PEER_APP_TOKEN`, like the demo's bob; without one that test skips. Finding a word by a
document's text needs OpenCloud's full-text search, the demo's Tika; without it that test skips.
`mise run test:live` sets all of them from `docker compose logs app-tokens`.

A second live test signs alice in at the demo's Rauthy with a password and reaches its OpenCloud
through the server with that token; it skips without `docker compose up opencloud`.

Dependencies beyond the go-sdk: [go-oidc](https://github.com/coreos/go-oidc) checks access tokens
against the provider's discovery and rotating keys, which the go-sdk leaves to the server; it is
the version OpenCloud itself uses. The OpenCloud module gives its KQL parser, `pkg/kql`, the only
package of it that is built in; it stays at the release in `internal/kql/version`, so the parser
and the parity report are of the same OpenCloud.

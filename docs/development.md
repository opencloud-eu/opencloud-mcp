# Development

``` console
mise run test       # unit tests against an in-repo fake OpenCloud, and the live test
mise run test:live  # the same with the live test against the demo, as alice and bob
mise run lint       # go vet and golangci-lint
mise run run        # the server on stdio
```

The live test runs against a local OpenCloud (`OPENCLOUD_URL`, the demo's `http://cloud.localhost`
when unset; it only accepts a loopback, `localhost` or `.test` address) with `OPENCLOUD_USER` and
`OPENCLOUD_APP_TOKEN` set, and `OPENCLOUD_INSECURE_TLS=true` for its own certificate.
`OPENCLOUD_LIVE_ANY_HOST=true` lifts the address check, for the OpenCloud service of CI. It writes
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

## CI and releases

Woodpecker runs the workflows in `.woodpecker/`, with the same mise tasks and tools as here:

- `test`: `mise run tidy`, `lint` and `test`, on every pull request and push to main; the
  live tests run against `opencloud-rolling:daily` as a service, pulled anew each time, which
  `compose/opencloud/ci.sh` gives alice, bob, the demo's seed and their app tokens; the tests
  that need the demo's Rauthy, Collabora or Tika skip
- `build`: `mise run mcpb`, the binaries and the bundle a release would put on GitHub, beside the
  tests; the task validates the bundle and starts its launcher
- `container`: the image of the `Dockerfile` for linux/amd64 and linux/arm64, built but not
  pushed, beside the tests; on a tag pushed to `opencloudeu/opencloud-mcp` on Docker Hub and
  `quay.io/opencloudeu/opencloud-mcp`, a release tagged with the version (`0.1.1`), its minor
  (`0.1`) and `latest`, a pre-release (`-alpha.`, `-beta.`, `-rc.`) only with its version
- `release`: on main, [ready-release-go][rrg] keeps a `next-release/main` pull request with the
  changelog; merging it tags the release, and the tag builds the binaries with `mise run dist`,
  for Linux, macOS and Windows on amd64 and arm64, and the MCP bundle for Claude Desktop with
  `mise run mcpb`, and puts them on the GitHub release with their SHA-256 checksums

The bundle (`mcpb/`) holds the manifest, the binaries for macOS and Linux on both architectures
and the Windows amd64 binary, which Windows on ARM runs in emulation: a bundle chooses its
command by system only, so on macOS and Linux the entry point is a launcher script that starts
the binary for the machine's architecture. Node and the mcpb CLI in `mise.toml` are for the bundle
only; CI installs just them and Go.

A tag runs only the image and the release, no tests: everything reaches main through a tested
pull request.

The changelog is made of the pull request titles, grouped by their `Type:*` label
(`release-config.ts`), so the title is the entry and the label decides the version bump.

[rrg]: https://github.com/woodpecker-ci/plugin-ready-release-go

A workflow runs locally with the woodpecker-cli, from the repository:

``` console
woodpecker-cli exec --repo-path . --secrets ci_http_proxy= .woodpecker/test.yaml
```

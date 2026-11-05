# opencloud-mcp

An MCP server for OpenCloud in Go. The user-facing docs are in `README.md` and `docs/`.

## Tasks

Everything runs through mise (`mise.toml`):

- `mise run build`, `mise run lint`, `mise run test`
- `mise run test:live`: all tests, the live ones against the demo as alice, with bob as the second user
- `mise run demo`: the compose demo in the foreground; `demo:tokens` and `demo:reset` beside it

Run golangci-lint through mise (`mise exec -- golangci-lint run ./...`); a system binary may be
older than the config.

## Layout

- `cmd/opencloud-mcp`: flags, environment, stdio and HTTP serving
- `internal/opencloud`: the client of OpenCloud (LibreGraph, WebDAV, OCS, app provider), its
  errors classed (`ErrNotFound`, `ErrDenied`, ...); `opencloudtest` is the fake OpenCloud of the
  unit tests
- `internal/tools`: the MCP tools, resources, completion and subscriptions; the toolsets and
  what registers each are in `tools.go`
- `internal/signin`: app tokens and OpenID Connect per request, the token exchange
- `internal/kql`: the search query language, OpenCloud's parity report and parser
- `internal/document`: documents as text and their sections
- `compose*`, `compose/`: the demo

## Working here

- Behavior of OpenCloud goes into the fake only once it is seen on a real one: probe the demo,
  then mirror it, then cover it with a live test in `internal/opencloud/live_test.go`.
- OpenCloud quirks (a 500 for an unknown trash key, a 400 for tags already there, ...) are
  handled in the client, with a one-line comment saying what OpenCloud answers.
- A new tool goes into the `kinds` map of `internal/tools/tools_test.go` and the tables of
  `docs/tools.md`; its annotations (`readOnly`, `adds`, `changes`) decide `--read-only`.
- goconst makes a string used three times a constant; gocyclo stops at 35.
- App tokens are words with spaces: quote them in the shell.

## Moving to another OpenCloud release

The release is pinned in several places that move together:

1. `internal/kql/version`, then `mise run kql` for its parity report
2. `github.com/opencloud-eu/opencloud` in `go.mod`, the pseudo-version of the release tag (the
   module has no major version path): `go get github.com/opencloud-eu/opencloud@<tag commit>`
3. `github.com/opencloud-eu/libre-graph-api-go` at the version that release's `go.mod` names
4. the `opencloudeu/opencloud-rolling` image in `compose.yaml`
5. "Tested with OpenCloud ..." in `README.md`

Then `mise run demo:reset`, `mise run demo` and, beside it, `mise run test:live`.

The Go version is in `mise.toml` and in `GO_VERSION` of the `Dockerfile`; they move together. CI
takes Go and golangci-lint from `mise.toml` too, through the `jdxcode/mise` image of `.woodpecker/`.

## CI and releases

Woodpecker runs `.woodpecker/` with the mise tasks. On every PR and push to main: `test` (tidy,
lint, unit and live tests, the live ones against `opencloud-rolling:daily`), and beside it `build`
and `container`, which builds the image for amd64 and arm64 without pushing it. `release` keeps
the next-release PR of ready-release-go up to date on main, and on its tag puts the binaries of
`mise run dist` on the GitHub release. It runs no tests: everything reaches main through a tested
PR. The PR title and its `Type:*` label are the changelog entry (`release-config.ts`).

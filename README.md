# OpenCloud MCP Server

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

An [MCP](https://modelcontextprotocol.io) server for [OpenCloud](https://github.com/opencloud-eu/opencloud).
It lets an MCP client work with the spaces of an OpenCloud user: list them, look into folders,
read documents (markdown, text, PDF), open one section of a document by its heading, search with
OpenCloud's own search and its query language, write files, create folders, copy, move and delete,
bring back what is in the trash and earlier versions of a file, share with people and by links,
accept what others share, tag, mark favorites and see previews of images and documents. A space
admin makes and manages project spaces and their members. The documents are MCP resources too,
with notice when one changes.

On stdio it works as one user with an app token. Over streamable HTTP it serves either one
configured user or every user of the instance, each request signing in on its own with an app
token or through MCP authorization with OpenID Connect. It keeps no state of its own: no index, no
cache, no background copy. Every request goes straight to OpenCloud as the signed-in user.

It speaks MCP 2026-07-28 (stateless, `server/discover`, `subscriptions/listen`) and the older
revisions as far as the [go-sdk](https://github.com/modelcontextprotocol/go-sdk) serves them.

## Install

Tested with OpenCloud 9.0.0. Earlier versions are not supported; later ones should work but are
not checked.

From source, with [mise](https://mise.jdx.dev) providing Go and the linter:

``` console
mise install
mise run build      # bin/opencloud-mcp
```

Or as a container image:

``` console
mise run image      # opencloud-mcp:dev
```

## Quick start

With an [app token](docs/configuration.md#app-tokens) of the user, in Claude Code:

``` console
claude mcp add opencloud \
  -e OPENCLOUD_URL=https://cloud.example.com -e OPENCLOUD_USER=alice -e OPENCLOUD_APP_TOKEN=<app token> \
  -- /path/to/opencloud-mcp
```

Other clients, streamable HTTP and every setting are in [Configuration](docs/configuration.md).

## Try it

`compose.yaml` runs OpenCloud, an OpenID Connect provider, this server and LibreChat on this
machine:

``` console
cp .env.example .env    # then fill in the model API
mise run demo
```

Open http://localhost and sign in as `alice@example.com` / `demo`. The rest is in
[Demo](docs/demo.md).

## Docs

- [Configuration](docs/configuration.md): the settings, the three modes, app tokens, connecting a client
- [Tools](docs/tools.md): every tool, the documents as resources, and search
- [Sign-in](docs/sign-in.md): app tokens and OpenID Connect per HTTP request
- [Demo](docs/demo.md): the compose demo and its users
- [Development](docs/development.md): the tests and the live test against the demo
- [Evaluation](docs/evaluation.md): questions for a model, to see whether the tools guide it

## License

Apache 2.0, see [LICENSE](LICENSE).

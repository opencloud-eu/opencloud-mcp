# Tools

The space is an ordinary argument of each tool that works in one, by its name or its id as
`list_spaces` gives it.

Each tool carries MCP annotations that say what it does to the user's OpenCloud: one that reads
has `readOnlyHint`, one that adds has `destructiveHint` false, one that replaces or removes
something has `destructiveHint` true; `idempotentHint` where a second call changes nothing more.
The server asks for no confirmation itself; approving a writing call is the client's job.

Each section below is a toolset: `spaces`, `files`, `search`, `trash`, `versions`, `sharing`,
`people`, `tags`, `activity` and `apps`. `--toolsets` offers only the ones it names, which keeps
what a model reads of the tools short; `--read-only` offers only the tools with `readOnlyHint`, a
limit the client cannot talk the server out of. The resources stay either way.

API is what the tool talks to: Graph is LibreGraph under `/graph`, WebDAV the spaces under
`/dav/spaces`, OCS the notifications under `/ocs`, the app provider `/app`. Graph comes first, it finds an item by its path
(`/v1.0/drives/{id}/root:/<path>`); WebDAV only where OpenCloud 9.0.0 serves no working Graph
route: reading and writing content, creating folders, moving, copying, deleting, search, trash,
versions and previews.

The errors say what went wrong in a sentence a model can pass on: the token was refused, there is
no such space, the file is not in the space, the path leads outside the space (`../` is refused,
never folded back in), the path is taken and nothing is to be overwritten, or the user may not do
it there, like a viewer writing.

## Spaces

| Tool                  | Kind    | API   | What it does                                                                       |
| --------------------- | ------- | ----- | ---------------------------------------------------------------------------------- |
| `list_spaces`         | reads   | Graph | the spaces the user sees: personal, project spaces, shares, with name, id and etag |
| `get_space`           | reads   | Graph | one space with description, quota and use, readme and image                        |
| `create_space`        | adds    | Graph | a project space with name, description and quota                                   |
| `update_space`        | changes | Graph | name, description, readme, image or quota of a space                               |
| `disable_space`       | changes | Graph | disables a project space, its content stays                                        |
| `restore_space`       | adds    | Graph | enables a disabled project space again                                             |
| `delete_space`        | changes | Graph | deletes a disabled project space for good                                          |
| `list_space_members`  | reads   | Graph | who has access to a space, with role, and its links                                |
| `add_space_member`    | adds    | Graph | gives a user or group access to a project space with a role                        |
| `update_space_member` | changes | Graph | role or expiry of a member                                                         |
| `remove_space_member` | changes | Graph | takes a member's access away                                                       |

## Files and folders

| Tool              | Kind    | API    | What it does                                                                                         |
| ----------------- | ------- | ------ | ---------------------------------------------------------------------------------------------------- |
| `list_folder`     | reads   | Graph  | the files and folders in a folder of a space, with their sizes                                       |
| `read_file`       | reads   | WebDAV | a file as text: markdown, text, CSV as written, a PDF by its text layer                              |
| `read_section`    | reads   | WebDAV | one section of a markdown document by its heading                                                    |
| `write_file`      | changes | WebDAV | creates a text file with its folders, or replaces one                                                |
| `get_item`        | reads   | Graph  | a file or folder by path or id, with permissions, tags, favorite, shares, photo details and its link |
| `create_folder`   | adds    | WebDAV | a folder with the folders above it                                                                   |
| `move_item`       | changes | WebDAV | moves or renames within its space; across spaces copy, then delete                                   |
| `copy_item`       | changes | WebDAV | copies, also into another space                                                                      |
| `delete_item`     | changes | WebDAV | deletes into the trash                                                                               |
| `add_favorite`    | adds    | Graph  | marks a favorite                                                                                     |
| `remove_favorite` | changes | Graph  | unmarks a favorite                                                                                   |
| `list_favorites`  | reads   | WebDAV | the user's favorites, in one space or all                                                            |
| `preview_image`   | reads   | WebDAV | an image or a document as a picture that fits a box, 512 pixels by default                           |

## Documents and search

| Tool             | Kind  | API    | What it does                                                                                                                  |
| ---------------- | ----- | ------ | ----------------------------------------------------------------------------------------------------------------------------- |
| `list_documents` | reads | Graph  | the documents of one space as resource links, with title, size and modification                                               |
| `search`         | reads | WebDAV | OpenCloud's own search in one space or all, by words, folder, tags, type, date, size, favorites or KQL; see [Search](#search) |

## Trash

| Tool                 | Kind    | API    | What it does                                   |
| -------------------- | ------- | ------ | ---------------------------------------------- |
| `list_trash`         | reads   | WebDAV | what was deleted in a space, or in all of them |
| `restore_from_trash` | adds    | WebDAV | brings a deleted item back                     |
| `delete_from_trash`  | changes | WebDAV | deletes one item from the trash for good       |
| `empty_trash`        | changes | WebDAV | deletes everything in the trash for good       |

## Versions

| Tool              | Kind    | API    | What it does                             |
| ----------------- | ------- | ------ | ---------------------------------------- |
| `list_versions`   | reads   | WebDAV | the earlier versions of a file           |
| `read_version`    | reads   | WebDAV | an earlier version as text               |
| `restore_version` | changes | WebDAV | makes an earlier version the current one |

## Sharing

| Tool                  | Kind    | API   | What it does                                                                                   |
| --------------------- | ------- | ----- | ---------------------------------------------------------------------------------------------- |
| `list_share_roles`    | reads   | Graph | the roles a share or member can have                                                           |
| `list_permissions`    | reads   | Graph | the shares and links of a file or folder                                                       |
| `share_item`          | adds    | Graph | shares with users or groups, with a role and an expiry                                         |
| `create_link`         | adds    | Graph | a public link to a file, folder or space, with access, password and expiry; or an internal one |
| `update_permission`   | changes | Graph | role, expiry, access or name of a share or link                                                |
| `set_link_password`   | changes | Graph | sets the password of a link                                                                    |
| `remove_permission`   | changes | Graph | stops a share, deletes a link                                                                  |
| `list_shared_with_me` | reads   | Graph | what others share with the user                                                                |
| `list_shared_by_me`   | reads   | Graph | what the user shares, with people, as public links or both                                     |
| `accept_share`        | adds    | Graph | accepts a share                                                                                |
| `decline_share`       | changes | Graph | declines a share, or removes an accepted one                                                   |
| `hide_share`          | changes | Graph | hides a share, or shows it again                                                               |

## People

| Tool          | Kind  | API   | What it does                                |
| ------------- | ----- | ----- | ------------------------------------------- |
| `get_me`      | reads | Graph | the signed-in user with language and groups |
| `find_users`  | reads | Graph | users by a part of name or mail             |
| `find_groups` | reads | Graph | groups by a part of the name                |

## Tags

| Tool          | Kind    | API   | What it does          |
| ------------- | ------- | ----- | --------------------- |
| `list_tags`   | reads   | Graph | the tags in use       |
| `add_tags`    | adds    | Graph | tags a file or folder |
| `remove_tags` | changes | Graph | takes tags off        |

## Activity and notifications

| Tool                    | Kind    | API   | What it does                                       |
| ----------------------- | ------- | ----- | -------------------------------------------------- |
| `list_activities`       | reads   | Graph | what happened to a file, folder or space, newest first |
| `list_notifications`    | reads   | OCS   | the user's notifications                           |
| `dismiss_notifications` | changes | OCS   | dismisses notifications                            |

## Apps

| Tool              | Kind  | API          | What it does                                                |
| ----------------- | ----- | ------------ | ----------------------------------------------------------- |
| `list_apps`       | reads | app provider | the apps OpenCloud opens files with, by file type           |
| `open_in_app`     | reads | app provider | the link that opens a file in an app, like an office editor |
| `create_document` | adds  | app provider | an empty office document of a type an app makes             |

## Left out

On purpose, no tool for:

- documents from a template: OpenCloud 9.0.0 makes only empty ones (`/app/new` answers "template is not implemented")
- admin and education routes, guest invitations, federation (OCM, ScienceMesh), WOPI: not a
  user's business, or between servers
- app tokens and passwords: an assistant does not make or change credentials
- locks: a lock an assistant forgets blocks people
- archives: a zip is no use to a model
- the event stream: `subscriptions/listen` carries the changes already
- personal data export, settings and language
- binary files: a model writes text; an upload from a URL would let the server fetch from anywhere
- shortcuts, duplicating a space, a space from chosen files: a model does them with `write_file`,
  `create_space` and `copy_item`

## Resources

Every markdown, text, CSV or PDF document is a resource:

```
opencloud:///spaces/<space id>/<path>
```

each segment path-escaped, also served as the template `opencloud:///spaces/{space}/{+path}`.
The template completes (`completion/complete`): `space` to the ids of the spaces whose name or id
starts with what is typed, `path` to the documents and folders, a folder with a trailing `/`, in
the folder up to the typed path's last `/`.

- `resources/list` gives the documents of one space per page; the cursor names the next space.
  For one space alone, `list_documents` gives the same entries.
- A resource has `name` (its path), `title` (a markdown document's first heading, else the file
  name), `mimeType`, `size` and `annotations.lastModified` (RFC 3339).
- `resources/read` gives one text content as `read_file` does; a PDF reads as `text/plain`. A
  document that is not there, or that the user does not see, answers `-32002`.
- Changes are told on `subscriptions/listen` (2026-07-28): `notifications/resources/list_changed`
  when a space's root etag moved, and `notifications/resources/updated` for a subscribed document
  whose etag or modification time moved. While a listen is open, the server compares the root
  etags every `--watch-interval` and lists only the folders whose etag moved on the way to a
  subscribed document. Nothing outlives the listen.
- A client can notice changes cheaply without a listen: the `etag` of a space in `list_spaces` and
  `list_documents` moves with anything inside it, and each resource's `size` and `lastModified`
  move with the document.

The older `resources/subscribe` is answered, but the stateless server has no stream to tell a
change on; a client of that revision re-reads.

## Search

`search` is OpenCloud's search service, the same one the web UI's search bar uses: a WebDAV
`REPORT` with `search-files` on `/dav/spaces/<space id>` for one space, or on `/dav/spaces/` for
all of them. The service takes OpenCloud's query language, KQL, as it is.

The inputs become one query, each a term of a form that OpenCloud's search parity report proves
on both of its engines, bleve and OpenSearch, joined by `AND`:

| Input                             | Term                                                           |
| --------------------------------- | -------------------------------------------------------------- |
| `query`                           | `(name:"*words*" OR content:"words")`                          |
| `kql`                             | `(the query)`, as it is                                        |
| `folder`                          | `path:"./folder"`, the folder and below                        |
| `tags`                            | `tag:("tag")` for each                                         |
| `type`                            | `mediatype:pdf`, a kind or a MIME type                         |
| `modifiedAfter`, `modifiedBefore` | `mtime>"2026-01-31T00:00:00Z"`, `mtime<"2026-02-01T00:00:00Z"` |
| `largerThan`, `smallerThan`       | `size>1000`, `size<1000`                                       |
| `favorites`                       | `is:favorite`, which the service makes the user's favorites    |

`list_favorites` is that last term alone. The report itself is `internal/kql/parity.md`, as
OpenCloud writes it for the version in `internal/kql/version`; `mise run kql` fetches it again
after that version moves. The server shows the model the language by the report's examples, as
the resource `opencloud:///search/kql`, and answers a query OpenCloud refuses with OpenCloud's
reason and the report's examples of the fields the query used.

Each query goes through OpenCloud's own parser (`pkg/kql` of the OpenCloud module) before it is
sent, so a query OpenCloud cannot read is refused at once with OpenCloud's reason. Two mistakes
OpenCloud answers with an empty result instead of an error: a range of no number or date
(`size>abc`, which the parser reads as words) and a field it does not know. A search that finds
nothing names either, a field counting as unknown when the report uses it nowhere.

What it finds depends on the instance. Out of the box OpenCloud indexes names and metadata only,
so a search finds files by name. With content extraction (Apache Tika, `SEARCH_EXTRACTOR_TYPE=tika`)
it finds words inside documents too; the [demo](demo.md) runs Tika. Indexing runs in the
background, so a file just written may not be found at once.

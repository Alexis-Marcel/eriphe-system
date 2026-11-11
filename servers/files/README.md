# eriphe-files

MCP server for the folders the user gives to Eriphe in their cloud storage: one tool set for every
provider, Google Drive first; Dropbox and OneDrive plug in behind the same `Provider` interface
(`provider.go`). It reaches **only the places it is configured with and what is inside them**: folders
of an account, or a whole account. It reads, adds and modifies files and moves them to the trash; it never deletes anything for good.
What Eriphe may do in each folder (Lire, Modifier, Supprimer, each free, on approval or never) is
eriphe-core's to decide, call by call, like any tool's access: the server keeps every call inside
the folders, and an account core marks `read_only` to reading. Its OAuth token is lent by eriphe-core, which ran the authorization
(the server never runs an OAuth flow, like eriphe-mail).

In Eriphe it runs as a local service on a node, created by the workspace (Settings → Tools →
Google Drive, see core/README.md, "Google Drive"): the token comes from the vault in its
environment, never through a task.

## Tools

| Tool | What | Hints |
|---|---|---|
| `list_folders` | the places given to Eriphe, with their account (`all: true` for a whole account) | read-only |
| `list_files` | the files and subfolders of a place, or of one of its subfolders (by id), folders first, each with its `place` | read-only |
| `search_files` | files whose name or content holds a text, in one place (its nested places included) or all, each with its `place` | read-only |
| `read_file` | a file's content: Google Docs as markdown (or text, pdf), Sheets as CSV of the first sheet (or pdf), Slides as text (or pdf), Drawings as png (or pdf); Word documents as their text; text files as they are; images and PDFs as such. Text is fenced as untrusted | read-only |
| `write_file` | a new file in the folder or one of its subfolders (`parent`), plain or converted to a Google Doc / Sheet; a taken name gets a number, nothing is ever replaced | not destructive (Modifier) |
| `update_file` | a file's new content, as a new version (Drive keeps the old ones): a text file as it is, a Google Doc from markdown, a Google Sheet from CSV (its first sheet) | not destructive (Modifier) |
| `trash_file` | a file or a subfolder to the trash, where it stays 30 days; never the folder itself | destructive (Supprimer) |
| `browse_places` | setup only: an account's folders (names, no files) from My Drive, Shared with me and the shared drives, for the workspace to offer; each with `parent`, the id of the folder it was browsed in (the call's `parent`, absent for the top places), so the workspace can draw the tree. Marked `_meta: {"eriphe/setup": true}`: eriphe-core leaves it out of the server's tools, so no task and not the brain ever get it | read-only |
| `test_account` | setup only: sign one account in (its token refreshed and its drive read) and say whether it works, or why not, never the token | read-only |

Every id a tool takes is checked against the place it names (`folder`): its parents are walked up
through the Drive API (cached for a minute) and the places met are noted, or it is refused ("this
item is not in the folder"). A shortcut is judged by its target, a trashed file is out, a file moved
out of the place is out within the minute. Drive's search cannot be limited to a folder's
descendants, so `search_files` searches the account and keeps what is inside a place (up to 5 pages
of 100). Nothing outside the places is ever returned, not even a name.

**A whole account.** An account with `all: true` is one more place, named after the account (its
`name`), which holds every item the provider can read: My Drive, the shared drives and what was
shared with the user (even from a folder they cannot see). Its top level lists My Drive, Shared with
me and the shared drives; a file written at its top level goes to the root of My Drive; nothing can
be written in Shared with me itself. Like a folder, it is never changed itself: My Drive's root and
Shared with me are never modified nor trashed.

**Nested places: the deepest decides.** Places may be inside one another, or inside a whole
account. For any item a call touches (read, list a subfolder, write into a parent, update, trash),
the place that holds it is the deepest one met walking its parents up (a whole account is the
shallowest of all). A call that names another place, an ancestor, is refused with the place to
name: `this item is in folder "Backend": name that folder`. eriphe-core decides by the place a call
names, so a subfolder's own rules hold. Listing a place shows what is in it, a nested place's folder
included (entering it through the ancestor is refused the same way), and every item of
`list_files` and `search_files` carries `place`, the deepest place holding it, the one to name
next. A folder that holds a nested place cannot be trashed through its ancestor (that would trash
the nested place past its own rules). `search_files` without `folder` searches every place, the
whole accounts included.

The provider interface has no call that deletes an item for good: the only writes are `CreateFile`,
`UpdateFile` (a new version) and `Trash`. The three tools that write refuse an account marked
`read_only`, or connected without the scope that writes, and never touch the place itself.

## Configuration

JSON in `TOOL_CONFIG`, or a file in `TOOL_CONFIG_FILE` (or `-config`); eriphe-core writes it:

```json
{
  "accounts": [
    {"name": "sam@example.org", "provider": "google", "auth": {"token_env": "TOOL_TOKEN"},
     "scopes": ["https://www.googleapis.com/auth/drive"], "read_only": false, "all": true}
  ],
  "places": [
    {"name": "Compta", "account": "sam@example.org", "id": "1AbC…"},
    {"name": "Factures", "account": "sam@example.org", "id": "1XyZ…", "parents": ["1AbC…"]}
  ]
}
```

- `auth`: exactly one of `token_env`, `token_file`, holding
  `{"client_id", "client_secret", "refresh_token", "token_url"}` (`token_url` is where to refresh
  it, Google's when absent). The access token is refreshed in memory
  (`golang.org/x/oauth2`) and never written; a revoked token gives an error that says to reconnect
  in the workspace.
- `scopes`: what the user granted. `drive.readonly` reads; adding, modifying or trashing needs `drive`.
- `read_only`: the account only reads, whatever its scopes (eriphe-core sets it from the account's access).
- `all`: the whole account is a place, named after the account (no other place may take its name).
- `places`: a name, an account (may be left out with one account), the folder's id and, optionally,
  `parents`: the ids of its ancestor folders from the top of the account down (empty or absent for
  a top folder), as eriphe-core saw them. It is a hint only: the guard never trusts it and walks
  the provider's real parents.
- `setup`: `false` leaves the setup tools (`browse_places`, `test_account`) out. Limits: `max_chars` (50000), `max_bytes` per file
  read or written (20 MB), `timeout_seconds` (60).

## Running

```sh
docker run -i --rm --read-only --cap-drop ALL -e TOOL_CONFIG -e TOOL_TOKEN \
  ghcr.io/alexis-marcel/eriphe-files                  # stdio
```

`-transport stdio|http` (`MCP_TRANSPORT`), `-addr` (`MCP_ADDR`, default `:8080`), bearer token
optional on HTTP (`MCP_TOKEN` or `MCP_TOKEN_FILE`); `GET /health`. Logs go to stderr: tool name,
duration and error; never arguments, file names, contents or tokens.

```sh
go build ./... && go vet ./... && go test -race ./...   # against an in-process fake Drive API
```

The tests run a fake Drive (`fake_test.go`) and its token endpoint: a file outside the folders
refused (elsewhere in the drive, the parent, a shortcut to outside, an unknown id), ancestry two
levels down, a file moved out, search keeping only the folders' files, the exports of Docs and
Sheets, writing in the folder or a subfolder (never replacing, a name that cannot climb out, never
outside), modifying a text file, a Doc and a Sheet, trashing (never the folder itself, never a
deletion), the refusals of a read-only account and of a read-only scope, the token refresh
(short-lived, reused, revoked), a whole account (anything in reach, Shared with me included, its
top folders never changed; with `all` off only the places), nested places (an item of `Backend`
named through `Engineering` refused with the name to use, for reading, listing, writing, updating
and trashing; served through `Backend`), each item's `place` in listings and searches, and
`browse_places`' parent ids.

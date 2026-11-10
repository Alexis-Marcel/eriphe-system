# eriphe-files

MCP server for the folders the user gives to Eriphe in their cloud storage: one tool set for every
provider, Google Drive first; Dropbox and OneDrive plug in behind the same `Provider` interface
(`provider.go`). It reaches **only the folders it is configured with and what is inside them**,
reads, adds and modifies files and moves them to the trash; it never deletes anything for good.
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
| `list_folders` | the folders given to Eriphe, with their account | read-only |
| `list_files` | the files and subfolders of a folder, or of one of its subfolders (by id), folders first | read-only |
| `search_files` | files whose name or content holds a text, in one folder or all | read-only |
| `read_file` | a file's content: Google Docs as markdown (or text, pdf), Sheets as CSV of the first sheet (or pdf), Slides as text (or pdf), Drawings as png (or pdf); Word documents as their text; text files as they are; images and PDFs as such. Text is fenced as untrusted | read-only |
| `write_file` | a new file in the folder or one of its subfolders (`parent`), plain or converted to a Google Doc / Sheet; a taken name gets a number, nothing is ever replaced | not destructive (Modifier) |
| `update_file` | a file's new content, as a new version (Drive keeps the old ones): a text file as it is, a Google Doc from markdown, a Google Sheet from CSV (its first sheet) | not destructive (Modifier) |
| `trash_file` | a file or a subfolder to the trash, where it stays 30 days; never the folder itself | destructive (Supprimer) |
| `browse_places` | setup only: an account's folders (names, no files) from My Drive, Shared with me and the shared drives, for the workspace to offer. Marked `_meta: {"eriphe/setup": true}`: eriphe-core leaves it out of the server's tools, so no task and not the brain ever get it | read-only |
| `test_account` | setup only: sign one account in (its token refreshed and its drive read) and say whether it works, or why not, never the token | read-only |

Every id a tool takes is checked against the folder it names: its parents are walked up through
the Drive API (cached for a minute) until the folder is found, or refused ("this item is not in
the folder"). A shortcut is judged by its target, a trashed file is out, a file moved out of the
folder is out within the minute. Drive's search cannot be limited to a folder's descendants, so
`search_files` searches the account and keeps what is inside a folder (up to 5 pages of 100).
Nothing outside the folders is ever returned, not even a name.

The provider interface has no call that deletes an item for good: the only writes are `CreateFile`,
`UpdateFile` (a new version) and `Trash`. The three tools that write refuse an account marked
`read_only`, or connected without the scope that writes, and never touch the folder itself.

## Configuration

JSON in `TOOL_CONFIG`, or a file in `TOOL_CONFIG_FILE` (or `-config`); eriphe-core writes it:

```json
{
  "accounts": [
    {"name": "sam@example.org", "provider": "google", "auth": {"token_env": "TOOL_TOKEN"},
     "scopes": ["https://www.googleapis.com/auth/drive"], "read_only": false}
  ],
  "places": [
    {"name": "Compta", "account": "sam@example.org", "id": "1AbC…"},
    {"name": "Photos", "account": "sam@example.org", "id": "1XyZ…"}
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
- `places`: a name, an account (may be left out with one account) and the folder's id.
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
deletion), the refusals of a read-only account and of a read-only scope, and the token refresh
(short-lived, reused, revoked).

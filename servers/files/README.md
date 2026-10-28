# eriphe-files

MCP server for the folders the user gives to Eriphe in their cloud storage: one tool set for every
provider, Google Drive first; Dropbox and OneDrive plug in behind the same `Provider` interface
(`provider.go`). It reaches **only the folders it is configured with and what is inside them**,
reads files, and adds new files to a folder's `Eriphe/` subfolder when its access allows; it never
modifies or deletes anything. Its OAuth token is lent by eriphe-core, which ran the authorization
(the server never runs an OAuth flow, like eriphe-mail).

In Eriphe it runs as a local service on a node, created by the workspace (Settings → Tools →
Google Drive, see core/README.md, "Google Drive"): the token comes from the vault in its
environment, never through a task.

## Tools

| Tool | What | Hints |
|---|---|---|
| `list_folders` | the folders given to Eriphe, with their access (`read`, or `drop`) | read-only |
| `list_files` | the files and subfolders of a folder, or of one of its subfolders (by id), folders first | read-only |
| `search_files` | files whose name or content holds a text, in one folder or all | read-only |
| `read_file` | a file's content: Google Docs as markdown (or text, pdf), Sheets as CSV of the first sheet (or pdf), Slides as text (or pdf), Drawings as png (or pdf); Word documents as their text; text files as they are; images and PDFs as such. Text is fenced as untrusted | read-only |
| `write_file` | a new file in the folder's `Eriphe/` subfolder (made if needed), plain or converted to a Google Doc / Sheet; a taken name gets a number, nothing is ever replaced | not destructive |
| `browse_folders` | setup only: an account's folders (names, no files) from My Drive, Shared with me and the shared drives, for the workspace to offer. Marked `_meta: {"eriphe/setup": true}`: eriphe-core leaves it out of the server's tools, so no task and not the brain ever get it | read-only |

Every id a tool takes is checked against the folder it names: its parents are walked up through
the Drive API (cached for a minute) until the folder is found, or refused ("this item is not in
the folder"). A shortcut is judged by its target, a trashed file is out, a file moved out of the
folder is out within the minute. Drive's search cannot be limited to a folder's descendants, so
`search_files` searches the account and keeps what is inside a folder (up to 5 pages of 100).
Nothing outside the folders is ever returned, not even a name.

The provider interface has no call that updates or deletes an item: the only writes are
`CreateFolder` (the `Eriphe` subfolder) and `CreateFile`, and `write_file` refuses a folder whose
access is `read`, or an account connected without the scope that writes.

## Configuration

JSON in `FILES_CONFIG`, or a file in `FILES_CONFIG_FILE` (or `-config`); eriphe-core writes it:

```json
{
  "accounts": [
    {"name": "google", "provider": "google", "auth": {"token_env": "FILES_GOOGLE_TOKEN"},
     "scopes": ["https://www.googleapis.com/auth/drive"]}
  ],
  "folders": [
    {"name": "Compta", "account": "google", "id": "1AbC…", "access": "drop"},
    {"name": "Photos", "account": "google", "id": "1XyZ…"}
  ]
}
```

- `auth`: exactly one of `token_env`, `token_file`, holding
  `{"client_id", "client_secret", "refresh_token"}`. The access token is refreshed in memory
  (`golang.org/x/oauth2`) and never written; a revoked token gives an error that says to reconnect
  in the workspace.
- `scopes`: what the user granted. `drive.readonly` reads; dropping files needs `drive`.
- `folders`: `access` is `read` (default) or `drop`. `account` may be left out with one account.
- `setup`: `false` leaves `browse_folders` out. Limits: `max_chars` (50000), `max_bytes` per file
  read or written (20 MB), `timeout_seconds` (60).

## Running

```sh
docker run -i --rm --read-only --cap-drop ALL -e FILES_CONFIG -e FILES_GOOGLE_TOKEN \
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
Sheets, writing only into `Eriphe/` (made once, never replacing, a name that cannot climb out,
no call but creations), the refusals of a read folder and of a read-only scope, and the token
refresh (short-lived, reused, revoked).

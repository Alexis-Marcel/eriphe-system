# eriphe-mail

MCP server for the user's mail: one tool set for every provider and every account, over IMAP
(`go-imap` v2) and SMTP. Only the way to log in changes per provider: an app password, or an OAuth
access token lent by eriphe-core and used with XOAUTH2 (the server never runs an OAuth flow).

## Tools

| Tool | What | Hints |
|---|---|---|
| `list_accounts` | configured accounts | read-only |
| `list_folders` | folders with their role (inbox, drafts, sent, trash, archive, junk, all) and counts | read-only |
| `search_messages` | search one or all accounts (text, from, to, subject, since, before, unread, flagged, folder, limit), newest first, with snippets; from the cache when it can | read-only |
| `read_message` | headers, text body (HTML converted), attachments listed, not downloaded; body fenced as untrusted; from the cache when it can | read-only |
| `create_draft` | plain text draft appended to Drafts; `reply_to` sets In-Reply-To/References, recipients and subject | not destructive |
| `send_message` | send a draft (`draft_id`) or a new message; a separate tool so the policy can leave it to the user | destructive, open world |
| `mark` | read/unread, flagged/unflagged | idempotent |
| `move` | archive, inbox or any folder but the trash and the junk folder, known by their role or name, subfolders included | not destructive |
| `trash` | to the trash, or with `junk` to the junk folder, where the server may delete it | destructive |

Message ids are opaque (account, folder, UIDVALIDITY, UID). A folder can be named by its role, so
"archive" is `[Gmail]/All Mail` on Gmail and `Archive` elsewhere.

Every tool takes an optional `account` (its `name`). The reads without one cover every account;
`create_draft` and `send_message` need one when there are several. `read_message`, `mark`, `move`
and `trash` work from the ids, and refuse an id of another account than the one named, so a caller
that names the account it means cannot act on another by mistake. An account with `read_only`
can be searched and read, never written from: no draft, no send, no mark, no move, no trash
(`list_accounts` shows it, with `can_send` false).

A message found in several accounts or folders (sent from one account to another, forwarded, sent
to both) is shown once by `search_messages`: the newest copy, the others in its `copies` (id,
account, folder, unread), matched by Message-ID; a message without one is never matched. `mark`,
`move` and `trash` act on the id given only.

Dates are shown in the zone of the process (`TZ`, which eriphe-core sets to the user's zone, e.g.
`Europe/Paris`) with their offset, e.g. `2026-10-19T10:00:00+02:00`; search and read results also
name that zone in `timezone`. Without `TZ` they are in UTC.

## Cache

Reads are answered from memory when they can be, so `search_messages` and `read_message` take
milliseconds instead of a 1-2 s IMAP session. Nothing is written to disk: the cache starts empty
at each start, and the container stays read-only.

- **What**: for each account, the headers (from, to, cc, reply-to, subject, date, flags,
  Message-ID, In-Reply-To, attachments, snippet) of the messages received in INBOX and Sent over
  the last 30 days, up to 2000 per folder (the newest); and the decoded text of bodies, fetched
  on first read (and ahead for the 20 newest INBOX messages), kept up to 50 MB for all accounts
  together, least recently read first out.
- **Fresh**: one connection per account stays open. With IDLE (Gmail, iCloud, most servers) a
  new message, a flag change or an expunge in INBOX is seen within a second; every cached
  folder is also checked every 2 minutes (only what changed is fetched: UIDs, then flags,
  `CHANGEDSINCE` with CONDSTORE; nothing at all when `HIGHESTMODSEQ` did not move). Writes
  (`mark`, `move`, `trash`, drafts, `send_message`) still go to the server, update the cache at once and
  trigger a sync. A dropped connection is retried with backoff (5 s doubling to 5 min).
- **Fallback**: the server answers what the cache cannot answer for sure: a `text` search
  (bodies), another folder, a `since` older than the window or fewer matches than the limit in
  the window when the folder has older mail, a cache not warm yet (right after start) or not
  synced for two poll periods (connection down).
- **Events**: each sync that finds something reports a change per folder (new, gone, flags):
  `Mailer.OnChange`, the hook for change notifications, and one log line with counts and
  duration (no subjects, addresses or bodies are ever logged).
- **New mail for eriphe-core**: a message that lands in an account's INBOX after the cache's
  first sync, received less than 24 hours ago, is a `mail.received` event (key: the account;
  payload: account, id, from, subject, date). The server offers its last 200 events as the MCP
  resource `eriphe://events`, JSON `{"epoch", "events": [{"seq", "kind", "key", "at",
  "payload"}]}`, oldest first; `seq` grows within an `epoch`, a new one at each start.
  eriphe-core reads it every minute into its event journal (core/README.md, Events). Without
  the cache there are no events.
- Gmail labels and categories are not cached: `go-imap` v2 does not fetch `X-GM-LABELS`.

| Env | Default | |
|---|---|---|
| `MAIL_CACHE` | on | `off` serves everything live |
| `MAIL_CACHE_DAYS` | 30 | window, by date received |
| `MAIL_CACHE_MAX_MESSAGES` | 2000 | per folder |
| `MAIL_CACHE_FOLDERS` | `inbox,sent` | names or roles; IDLE watches INBOX |
| `MAIL_CACHE_BODY_MB` | 50 | bodies kept, all accounts |
| `MAIL_CACHE_PREFETCH` | 20 | newest INBOX bodies fetched ahead; 0 for none |
| `MAIL_CACHE_POLL` | `2m` | check of every cached folder (at least `10s`) |
| `MAIL_CACHE_IDLE` | on | `off` polls only |

Memory: about 1-2 KB per cached header, so some 5 MB for an account with two full folders, plus
the bodies (50 MB at most).

## Configuration

JSON in `MAIL_CONFIG`, or a file in `MAIL_CONFIG_FILE` (or `-config`). With a known provider (given
or guessed from the address), an account needs only `address` and `auth`:

```json
{
  "accounts": [
    {"name": "perso", "address": "someone@gmail.com", "display_name": "Some One",
     "auth": {"password_env": "GMAIL_APP_PASSWORD"}},
    {"name": "work", "address": "me@corp.example", "provider": "office365",
     "auth": {"oauth_token_file": "/data/ms-token"}},
    {"name": "own", "address": "me@self.example",
     "imap": {"host": "mail.self.example", "port": 993},
     "smtp": {"host": "mail.self.example", "port": 587},
     "auth": {"password_file": "/run/secrets/own"}, "folders": {"archive": "Old"}}
  ]
}
```

- Presets: `gmail`, `icloud`, `outlook` (Outlook.com, Hotmail), `office365`, `orange`, `ovh`,
  `fastmail`. Guessed from gmail.com, icloud.com/me.com/mac.com, outlook.*/hotmail.*/live.*,
  orange.fr/wanadoo.fr, fastmail.com.
- `auth`: exactly one of `password_env`, `password_file`, `oauth_token_env`, `oauth_token_file`. The
  secret is read again at each connection, so a token file refreshed by eriphe-core is picked up.
  A token file may hold the bare token or an OAuth JSON response with `access_token`. It may also
  hold a refresh grant, `{"client_id", "client_secret", "refresh_token", "token_url"}`, as
  eriphe-core lends it for an account connected with a provider's sign-in (`token_url` is where to
  refresh it, Google's when absent, as in tokens lent before it was given; Gmail with XOAUTH2, scope `https://mail.google.com/`): the access token is
  then refreshed here, in memory (`refresh.go`), never written nor logged; a refresh token Google
  refuses gives an error that says to reconnect the account in Eriphe.
- `read_only`: the account is only read (eriphe-core sets it for an account it gives read-only
  access).
- `security`: `tls` (implicit, default on 993/465) or `starttls` (default otherwise). There is no
  plaintext mode; certificates are verified (`ca_file` adds roots for a self-hosted server).
- `username` defaults to the address; `save_sent` (copy sent mail to Sent) defaults to false for
  Gmail, Outlook, Office 365 and Fastmail, which file SMTP submissions themselves.
- Limits: `max_results` (50), `max_body_chars` (20000), `timeout_seconds` (60); 100 recipients,
  500 ids per call, 4 MB read per body part, 25 MB per draft sent.

## Running

```sh
docker run -i --rm --read-only --cap-drop ALL -e MAIL_CONFIG -e GMAIL_APP_PASSWORD \
  ghcr.io/alexis-marcel/eriphe-mail                  # stdio
docker run --rm --read-only --cap-drop ALL -p 8080:8080 -e MAIL_CONFIG -e GMAIL_APP_PASSWORD \
  -e MCP_TOKEN_FILE=/run/secrets/token ghcr.io/alexis-marcel/eriphe-mail -transport http
```

`-transport stdio|http` (`MCP_TRANSPORT`), `-addr` (`MCP_ADDR`, default `:8080`), bearer token
optional on HTTP (`MCP_TOKEN` or `MCP_TOKEN_FILE`); `GET /health`. The server answers at once at
start; the cache fills in the background. Logs go to stderr: tool name, duration and error, and
one line per cache sync with counts; never arguments, subjects, addresses, bodies or secrets.

```sh
go build ./... && go vet ./... && go test -race ./...   # tests run in-process IMAP and SMTP servers
```

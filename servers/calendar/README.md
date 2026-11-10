# eriphe-calendar

MCP server for the user's calendars: one tool set for every account, over CalDAV (`go-webdav`) or
Microsoft Graph. Secrets are app passwords or OAuth access tokens lent by eriphe-core.

## Tools

| Tool | What | Hints |
|---|---|---|
| `list_accounts` | accounts and the default time zone | read-only |
| `test_account` | setup only (`_meta: {"eriphe/setup": true}`, never given to a task or the brain): sign one account in and list its calendars as the server will, and say whether it works, or why not, never the secret | read-only |
| `list_calendars` | calendars of one or all accounts | read-only |
| `list_events` | events in a range (default today + 7 days), all calendars unless one is given, recurring events expanded, optional text filter; from the cache within its window | read-only |
| `get_event` | one event with its description | read-only |
| `create_event` | timed (start + end or duration) or all-day (dates) | not destructive |
| `update_event` | only the fields given; moving the start keeps the duration; whole series for recurring events | destructive |
| `delete_event` | whole series for recurring events | destructive |
| `find_free_time` | free slots within working hours (default 09:00-18:00, weekdays), all calendars; free events and all-day events do not block by default | read-only |

Times without an offset are read in the configured zone (or a `timezone` argument) and shown with
their offset. All-day events are dates, with an inclusive end (a one-day event has start = end).
New CalDAV events are written in UTC; recurring events the server does not expand are expanded
here (RRULE, EXDATE, moved occurrences), and an unknown TZID (Windows zone names) falls back to the
configured zone.

Every tool takes an optional `account` (its `name`). The reads without one cover every account;
`create_event` needs one (and a calendar) when several calendars can be written. `get_event`,
`update_event` and `delete_event` work from the event id, and refuse an id of another account than
the one named. An account with `read_only` is read, never written: no event is created, changed or
deleted in it (`list_accounts` shows it).

An event in several calendars or accounts (an invitation sent to two addresses, a shared or
subscribed calendar) is shown once by `list_events`, the others in its `copies` (id, account,
calendar), matched by iCalendar UID and start, so each occurrence of a recurring event stays one;
an event without a UID is never matched. `update_event` and `delete_event` act on the id given
only; `find_free_time` is the same either way.

## Cache

Reads are answered from memory when they can be, so `list_events`, `list_calendars` and
`find_free_time` take milliseconds instead of one request per calendar. Nothing is written to
disk.

- **What**: the calendars of every account and their events from 30 days back to 90 days ahead
  (whole days in the configured zone), recurring events expanded as for a live read.
- **Fresh**: refreshed every 5 minutes. A CalDAV calendar is fetched again only when it changed:
  its CTag (iCloud, Nextcloud, Fastmail...) or sync-token (RFC 6578) is compared first, and when
  the server has neither, a digest of the ETags of the window's events (one light REPORT).
  Microsoft Graph calendars have no such tag and are fetched at each refresh. A create, update or
  delete made through this server drops that calendar's entry (reads go live) and refreshes it
  at once. The window moves with the day.
- **Fallback**: a range outside the window, a cache not warm yet (right after start: the server
  answers at once, live), or an account not refreshed for two periods (provider unreachable) is
  served live. `get_event` stays live (it returns the series, with its full description).
- **Events**: each calendar a refresh found changed, and each write, is reported to
  `Calendars.OnChange`, the hook for change notifications. Each refresh logs one line per
  account with counts (calendars, fetched, unchanged, failed, events) and duration; no titles or
  descriptions.

| Env | Default | |
|---|---|---|
| `CALENDAR_CACHE` | on | `off` serves everything live |
| `CALENDAR_CACHE_PAST_DAYS` | 30 | days before today |
| `CALENDAR_CACHE_FUTURE_DAYS` | 90 | days after today |
| `CALENDAR_CACHE_REFRESH` | `5m` | at least `30s` |

Memory: about 1 KB per occurrence (more with long descriptions): a few MB for a busy account.

## Configuration

JSON in `TOOL_CONFIG`, or a file in `TOOL_CONFIG_FILE` (or `-config`):

```json
{
  "timezone": "Europe/Paris",
  "accounts": [
    {"name": "icloud", "provider": "icloud", "username": "someone@icloud.com",
     "auth": {"password_env": "ICLOUD_APP_PASSWORD"}},
    {"name": "google", "provider": "google", "username": "someone@gmail.com",
     "auth": {"token_file": "/data/google-token"}},
    {"name": "work", "provider": "microsoft", "auth": {"token_file": "/data/ms-token"}},
    {"name": "cloud", "provider": "nextcloud", "url": "https://cloud.example/remote.php/dav",
     "username": "me", "auth": {"password_env": "NEXTCLOUD_PASSWORD"}}
  ]
}
```

- Providers: `icloud` (caldav.icloud.com, app password), `google` (CalDAV with an OAuth token,
  scope `https://www.googleapis.com/auth/calendar`), `fastmail` (app password), `nextcloud` or
  `caldav` (any server, `url` required, https only), `microsoft` (Graph, token with
  `Calendars.ReadWrite`).
- `auth`: exactly one of `password_env`, `password_file`, `token_env`, `token_file`,
  read again at each request (a refreshed token file is picked up). An OAuth secret may also be a
  refresh grant, `{"client_id", "client_secret", "refresh_token", "token_url"}`, as eriphe-core
  lends it for an account connected with a provider's sign-in (`token_url` is where to refresh it,
  required): the access token is then refreshed here, in memory (`refresh.go`), never written
  nor logged; a refresh token Google refuses gives an error that says to reconnect the account in
  Eriphe. When Google refuses a request (403), its reason is read from the body (`refusal.go`): the
  CalDAV API turned off in the Google Cloud project of the user's Google app (it is not the Google
  Calendar API), or the account connected without Google Calendar, each with what to do. Any other
  refusal stays "403 Forbidden".
- `read_only`: the account is only read (eriphe-core sets it for an account it gives read-only
  access).
- Limits: `max_events` (500), `timeout_seconds` (30), ranges up to 92 days (62 for free time).

## Running

```sh
docker run -i --rm --read-only --cap-drop ALL -e TOOL_CONFIG -e ICLOUD_APP_PASSWORD \
  ghcr.io/alexis-marcel/eriphe-calendar               # stdio
```

Same transports and logging as eriphe-mail: `-transport stdio|http`, `-addr`, optional bearer
(`MCP_TOKEN`, `MCP_TOKEN_FILE`), `GET /health`, no arguments or secrets in logs.

```sh
go build ./... && go vet ./... && go test -race ./...   # tests run an in-process CalDAV server and a fake Graph
```

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testEnv struct {
	cfg    *Config
	hc     *http.Client
	perso  *memCalDAV
	family *memCalDAV
	graph  *fakeGraph
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{perso: newMemCalDAV("Personnel", "Travail"), family: newMemCalDAV("Famille"), graph: newFakeGraph("ms-token-1")}
	s1 := serveCalDAV(t, e.perso, "me@icloud.example", "app-pw")
	s2 := serveCalDAV(t, e.family, "me", "nc-pw")
	s3 := e.graph.serve(t)
	e.hc = s1.Client()
	t.Setenv("ICLOUD_PW", "app-pw")
	t.Setenv("NC_PW", "nc-pw")
	t.Setenv("MS_TOKEN", "ms-token-1")
	cfg, err := LoadConfig(`{"timezone":"Europe/Paris","accounts":[
	  {"name":"icloud","provider":"caldav","url":"`+s1.URL+`/","username":"me@icloud.example","auth":{"password_env":"ICLOUD_PW"}},
	  {"name":"cloud","provider":"nextcloud","url":"`+s2.URL+`/","username":"me","auth":{"password_env":"NC_PW"}},
	  {"name":"work","provider":"microsoft","graph_url":"`+s3.URL+`/v1.0","auth":{"oauth_token_env":"MS_TOKEN"}}]}`, "")
	if err != nil {
		t.Fatal(err)
	}
	e.cfg = cfg
	return e
}

func (e *testEnv) connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	c, err := NewCalendars(e.cfg, e.hc)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := newServer(c, "test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callRaw(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func call[T any](t *testing.T, cs *mcp.ClientSession, name string, args any) T {
	t.Helper()
	res := callRaw(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, text(res))
	}
	var out T
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: %v in %s", name, err, b)
	}
	return out
}

func callErr(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()
	res := callRaw(t, cs, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an error, got %s", name, text(res))
	}
	return text(res)
}

func text(res *mcp.CallToolResult) string {
	var s []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			s = append(s, tc.Text)
		}
	}
	return strings.Join(s, "\n")
}

func titles(evs []Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Title)
	}
	return strings.Join(s, ",")
}

func TestListCalendars(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	out := call[listCalendarsOut](t, cs, "list_calendars", map[string]any{})
	var got []string
	for _, c := range out.Calendars {
		got = append(got, c.Account+"/"+c.Name)
	}
	if strings.Join(got, ",") != "icloud/Personnel,icloud/Travail,cloud/Famille,work/Calendar,work/Holidays" || len(out.Errors) > 0 {
		t.Fatalf("calendars: %v %v", got, out.Errors)
	}
	if !out.Calendars[4].ReadOnly {
		t.Error("Holidays should be read-only")
	}
}

func TestCalDAVCreateUpdateDelete(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	ev := call[Event](t, cs, "create_event", map[string]any{
		"account": "icloud", "calendar": "Travail", "title": "Dentiste", "start": "2026-10-27T14:00", "duration_minutes": 45,
		"location": "Lille", "description": "Apporter la carte",
	})
	if ev.Start != "2026-10-27T14:00:00+01:00" || ev.End != "2026-10-27T14:45:00+01:00" || ev.Calendar != "Travail" {
		t.Fatalf("created: %+v", ev)
	}
	paths := e.perso.paths()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "/u/calendars/travail/") {
		t.Fatalf("stored at %v", paths)
	}
	raw := e.perso.raw(t, paths[0])
	if !strings.Contains(raw, "DTSTART:20261027T130000Z") || !strings.Contains(raw, "SUMMARY:Dentiste") {
		t.Errorf("stored:\n%s", raw)
	}

	l := call[listEventsOut](t, cs, "list_events", map[string]any{"from": "2026-10-26", "days": 7, "timezone": "America/New_York"})
	if len(l.Events) != 1 || l.Events[0].Start != "2026-10-27T09:00:00-04:00" || l.Timezone != "America/New_York" {
		t.Fatalf("list: %+v", l)
	}
	got := call[Event](t, cs, "get_event", map[string]any{"id": ev.ID})
	if got.Description != "Apporter la carte" || got.Location != "Lille" {
		t.Errorf("get: %+v", got)
	}

	up := call[Event](t, cs, "update_event", map[string]any{"id": ev.ID, "title": "Dentiste (déplacé)", "start": "2026-10-28T09:30:00+01:00"})
	if up.Start != "2026-10-28T09:30:00+01:00" || up.End != "2026-10-28T10:15:00+01:00" || up.Title != "Dentiste (déplacé)" || up.Location != "Lille" {
		t.Fatalf("update: %+v", up)
	}
	if raw := e.perso.raw(t, paths[0]); !strings.Contains(raw, "SEQUENCE:1") {
		t.Errorf("sequence not bumped:\n%s", raw)
	}
	if up.ID != ev.ID {
		t.Error("id changed on update")
	}
	call[deleteOut](t, cs, "delete_event", map[string]any{"id": ev.ID})
	if len(e.perso.paths()) != 0 {
		t.Fatal("not deleted")
	}
	if msg := callErr(t, cs, "get_event", map[string]any{"id": ev.ID}); !strings.Contains(msg, "not found") {
		t.Errorf("get deleted: %s", msg)
	}
	if msg := callErr(t, cs, "get_event", map[string]any{"id": "nope"}); !strings.Contains(msg, "invalid event id") {
		t.Errorf("bad id: %s", msg)
	}
}

func TestAllDayEvents(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	ev := call[Event](t, cs, "create_event", map[string]any{"account": "cloud", "title": "Vacances", "start": "2026-10-30", "end": "2026-11-01"})
	if !ev.AllDay || ev.Start != "2026-10-30" || ev.End != "2026-11-01" {
		t.Fatalf("created: %+v", ev)
	}
	raw := e.family.raw(t, e.family.paths()[0])
	if !strings.Contains(raw, "DTSTART;VALUE=DATE:20261030") || !strings.Contains(raw, "DTEND;VALUE=DATE:20261102") {
		t.Errorf("stored:\n%s", raw)
	}
	one := call[Event](t, cs, "create_event", map[string]any{"account": "cloud", "title": "Anniversaire", "start": "2026-11-05"})
	if one.Start != "2026-11-05" || one.End != "2026-11-05" {
		t.Errorf("one day: %+v", one)
	}
	l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "cloud", "from": "2026-11-01", "to": "2026-11-02"})
	if titles(l.Events) != "Vacances" {
		t.Errorf("overlap on the last day: %s", titles(l.Events))
	}
	if msg := callErr(t, cs, "create_event", map[string]any{"account": "cloud", "title": "x", "start": "2026-11-05", "end": "2026-11-05T10:00"}); !strings.Contains(msg, "both be dates") {
		t.Errorf("mixed: %s", msg)
	}
}

const recurring = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:standup@example.org
DTSTAMP:20261001T000000Z
DTSTART;TZID=Europe/Paris:20261023T090000
DTEND;TZID=Europe/Paris:20261023T091500
RRULE:FREQ=DAILY;COUNT=6
EXDATE;TZID=Europe/Paris:20261026T090000
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:standup@example.org
DTSTAMP:20261001T000000Z
RECURRENCE-ID;TZID=Europe/Paris:20261027T090000
DTSTART;TZID=Europe/Paris:20261027T100000
DTEND;TZID=Europe/Paris:20261027T101500
SUMMARY:Standup (late)
END:VEVENT
END:VCALENDAR
`

const windowsTZ = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:win@example.org
DTSTAMP:20261001T000000Z
DTSTART;TZID=Romance Standard Time:20261029T150000
DTEND;TZID=Romance Standard Time:20261029T160000
SUMMARY:Invitation Outlook
TRANSP:TRANSPARENT
END:VEVENT
END:VCALENDAR
`

func TestRecurrenceAndTimeZones(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/personnel/standup.ics", recurring)
	e.perso.put(t, "/u/calendars/personnel/win.ics", windowsTZ)
	cs := e.connect(t)
	l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-24", "to": "2026-10-30"})
	var got []string
	for _, ev := range l.Events {
		got = append(got, ev.Title+"@"+ev.Start)
	}
	// Daily at 09:00 Paris across the end of summer time (25 October),
	// without the excluded day, with the moved occurrence.
	want := "Standup@2026-10-24T09:00:00+02:00,Standup@2026-10-25T09:00:00+01:00,Standup (late)@2026-10-27T10:00:00+01:00," +
		"Standup@2026-10-28T09:00:00+01:00,Invitation Outlook@2026-10-29T15:00:00+01:00"
	if strings.Join(got, ",") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
	if !l.Events[0].Recurring || !l.Events[4].Free {
		t.Errorf("flags: %+v", l.Events)
	}
	q := call[listEventsOut](t, cs, "list_events", map[string]any{"from": "2026-10-24", "to": "2026-10-30", "query": "late"})
	if titles(q.Events) != "Standup (late)" {
		t.Errorf("query: %s", titles(q.Events))
	}
}

func TestGraph(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	if msg := callErr(t, cs, "create_event", map[string]any{"account": "work", "calendar": "Holidays", "title": "x", "start": "2026-10-27T10:00"}); !strings.Contains(msg, "no writable") {
		t.Errorf("read-only calendar: %s", msg)
	}
	a := call[Event](t, cs, "create_event", map[string]any{"account": "work", "title": "Revue", "start": "2026-10-27T10:00", "end": "2026-10-27T11:30", "location": "Salle 2"})
	b := call[Event](t, cs, "create_event", map[string]any{"account": "work", "title": "Séminaire", "start": "2026-10-29", "end": "2026-10-30"})
	if a.Start != "2026-10-27T10:00:00+01:00" || a.End != "2026-10-27T11:30:00+01:00" || a.Location != "Salle 2" {
		t.Fatalf("created: %+v", a)
	}
	if !b.AllDay || b.Start != "2026-10-29" || b.End != "2026-10-30" {
		t.Fatalf("all-day: %+v", b)
	}
	e.graph.mu.Lock()
	for _, ev := range e.graph.events {
		if ev["subject"] == "Revue" && ev["start"].(map[string]any)["dateTime"] != "2026-10-27T09:00:00" {
			t.Errorf("sent to Graph: %v", ev["start"])
		}
	}
	e.graph.mu.Unlock()

	// Two events, one per page: the next link is followed.
	l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "work", "from": "2026-10-26", "days": 7})
	if titles(l.Events) != "Revue,Séminaire" {
		t.Fatalf("list: %+v", l.Events)
	}
	up := call[Event](t, cs, "update_event", map[string]any{"id": a.ID, "start": "2026-10-27T15:00"})
	if up.Start != "2026-10-27T15:00:00+01:00" || up.End != "2026-10-27T16:30:00+01:00" || up.Title != "Revue" {
		t.Fatalf("update: %+v", up)
	}
	call[deleteOut](t, cs, "delete_event", map[string]any{"id": a.ID})
	if msg := callErr(t, cs, "get_event", map[string]any{"id": a.ID}); !strings.Contains(msg, "not found") {
		t.Errorf("deleted: %s", msg)
	}

	// eriphe-core refreshes the token in a file.
	f := filepath.Join(t.TempDir(), "ms")
	os.WriteFile(f, []byte(`{"access_token":"ms-token-2"}`), 0o600)
	e.cfg.Accounts[2].Auth = AuthConfig{OAuthTokenFile: f}
	e.graph.mu.Lock()
	e.graph.token = "ms-token-2"
	e.graph.mu.Unlock()
	cs = e.connect(t)
	call[Event](t, cs, "get_event", map[string]any{"id": b.ID})
	e.graph.mu.Lock()
	e.graph.token = "ms-token-3"
	e.graph.mu.Unlock()
	callErr(t, cs, "get_event", map[string]any{"id": b.ID})
	os.WriteFile(f, []byte("ms-token-3"), 0o600)
	call[Event](t, cs, "get_event", map[string]any{"id": b.ID})
}

func TestMultiAccount(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	call[Event](t, cs, "create_event", map[string]any{"account": "icloud", "calendar": "Personnel", "title": "B", "start": "2026-10-27T12:00"})
	call[Event](t, cs, "create_event", map[string]any{"account": "cloud", "title": "A", "start": "2026-10-27T08:00"})
	call[Event](t, cs, "create_event", map[string]any{"account": "work", "calendar": "Calendar", "title": "C", "start": "2026-10-27T18:00"})
	l := call[listEventsOut](t, cs, "list_events", map[string]any{"from": "2026-10-27", "days": 1})
	if titles(l.Events) != "A,B,C" || len(l.Errors) > 0 {
		t.Fatalf("all accounts: %s %v", titles(l.Events), l.Errors)
	}
	one := call[listEventsOut](t, cs, "list_events", map[string]any{"from": "2026-10-27", "days": 1, "calendar": "famille"})
	if titles(one.Events) != "A" {
		t.Errorf("by calendar name: %s", titles(one.Events))
	}
	if msg := callErr(t, cs, "create_event", map[string]any{"title": "x", "start": "2026-10-27T10:00"}); !strings.Contains(msg, "several calendars") {
		t.Errorf("ambiguous: %s", msg)
	}
	// One account failing does not hide the others.
	t.Setenv("NC_PW", "wrong")
	l = call[listEventsOut](t, cs, "list_events", map[string]any{"from": "2026-10-27", "days": 1})
	if titles(l.Events) != "B,C" || len(l.Errors) != 1 || l.Errors[0].Account != "cloud" {
		t.Errorf("partial: %s %+v", titles(l.Events), l.Errors)
	}
}

func TestFindFreeTime(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	for _, ev := range []map[string]any{
		{"account": "icloud", "calendar": "Travail", "title": "Réunion", "start": "2026-10-27T10:00", "end": "2026-10-27T11:00"},
		{"account": "work", "calendar": "Calendar", "title": "Déjeuner", "start": "2026-10-27T12:30", "end": "2026-10-27T14:00"},
		{"account": "cloud", "title": "Congé", "start": "2026-10-27"},
	} {
		call[Event](t, cs, "create_event", ev)
	}
	e.perso.put(t, "/u/calendars/personnel/win.ics", windowsTZ) // free, does not block
	out := call[freeOut](t, cs, "find_free_time", map[string]any{"from": "2026-10-27", "to": "2026-10-28", "duration_minutes": 60})
	var got []string
	for _, s := range out.Slots {
		got = append(got, s.Start[11:16]+"-"+s.End[11:16])
	}
	if strings.Join(got, ",") != "09:00-10:00,11:00-12:30,14:00-18:00" {
		t.Errorf("slots: %v", got)
	}
	blocked := call[freeOut](t, cs, "find_free_time", map[string]any{"from": "2026-10-27", "to": "2026-10-28", "all_day_blocks": true})
	if len(blocked.Slots) != 0 {
		t.Errorf("all-day blocking: %+v", blocked.Slots)
	}
	weekend := call[freeOut](t, cs, "find_free_time", map[string]any{"from": "2026-10-31", "to": "2026-11-02"})
	if len(weekend.Slots) != 0 {
		t.Errorf("weekend: %+v", weekend.Slots)
	}
	short := call[freeOut](t, cs, "find_free_time", map[string]any{"from": "2026-10-27", "to": "2026-10-28", "duration_minutes": 120, "buffer_minutes": 15})
	if len(short.Slots) != 1 || short.Slots[0].Start != "2026-10-27T14:15:00+01:00" {
		t.Errorf("long slot with buffer: %+v", short.Slots)
	}
}

func TestToolAnnotations(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.ToolAnnotations{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl.Annotations
	}
	if len(got) != 8 {
		t.Errorf("tools: %v", got)
	}
	for _, n := range []string{"list_accounts", "list_calendars", "list_events", "get_event", "find_free_time"} {
		if got[n] == nil || !got[n].ReadOnlyHint {
			t.Errorf("%s should be read-only", n)
		}
	}
	for _, n := range []string{"update_event", "delete_event"} {
		if a := got[n]; a == nil || a.ReadOnlyHint || !*a.DestructiveHint {
			t.Errorf("%s should be destructive", n)
		}
	}
	if a := got["create_event"]; a == nil || a.ReadOnlyHint || *a.DestructiveHint {
		t.Errorf("create_event: %+v", a)
	}
}

func TestConfig(t *testing.T) {
	cfg, err := LoadConfig(`{"accounts":[
	  {"name":"icloud","provider":"icloud","username":"me@icloud.com","auth":{"password_env":"ICLOUD_APP_PASSWORD"}},
	  {"provider":"google","username":"me@gmail.com","auth":{"oauth_token_file":"/data/google-token"}},
	  {"provider":"fastmail","username":"me@fastmail.com","auth":{"password_file":"/run/secrets/fm"}},
	  {"provider":"microsoft","auth":{"oauth_token_env":"MS"}}]}`, "")
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Accounts
	if a[0].URL != "https://caldav.icloud.com" || a[1].Name != "google" || a[1].URL == "" || a[3].GraphURL != "https://graph.microsoft.com/v1.0" {
		t.Errorf("presets: %+v", a)
	}
	if cfg.Timezone == "" || cfg.loc == nil {
		t.Error("no default timezone")
	}
	for want, js := range map[string]string{
		"url is required":     `{"accounts":[{"provider":"nextcloud","username":"u","auth":{"password_env":"X"}}]}`,
		"must be https":       `{"accounts":[{"provider":"caldav","url":"http://x","username":"u","auth":{"password_env":"X"}}]}`,
		"needs an OAuth":      `{"accounts":[{"provider":"microsoft","auth":{"password_env":"X"}}]}`,
		"username is require": `{"accounts":[{"provider":"icloud","auth":{"password_env":"X"}}]}`,
		"unknown provider":    `{"accounts":[{"provider":"yahoo","auth":{"password_env":"X"}}]}`,
		"timezone":            `{"timezone":"Mars/Olympus","accounts":[{"provider":"microsoft","auth":{"oauth_token_env":"X"}}]}`,
		"exactly one":         `{"accounts":[{"provider":"microsoft","auth":{}}]}`,
	} {
		if _, err := LoadConfig(js, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

// iCloud expands recurring events itself: instances come back with a
// RECURRENCE-ID, in UTC, without RRULE.
func TestServerExpandedInstances(t *testing.T) {
	ics := strings.ReplaceAll(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:x@example.org
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261027T080000Z
DTSTART:20261027T080000Z
DTEND:20261027T090000Z
SUMMARY:Yoga
END:VEVENT
BEGIN:VEVENT
UID:x@example.org
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261103T080000Z
DTSTART:20261103T080000Z
DTEND:20261103T090000Z
SUMMARY:Yoga
END:VEVENT
END:VCALENDAR
`, "\n", "\r\n")
	ic, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	paris, _ := time.LoadLocation("Europe/Paris")
	d := &CalDAV{loc: paris}
	from := time.Date(2026, 10, 26, 0, 0, 0, 0, paris)
	got := d.occurrences(ic, "/c/x.ics", "/c/", from, from.AddDate(0, 0, 7))
	if len(got) != 1 || !got[0].Recurring || got[0].Start.In(paris).Hour() != 9 {
		t.Fatalf("got %+v", got)
	}
}

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// vevent is an event of the given length starting at t (UTC).
func vevent(uid, title string, t time.Time, d time.Duration, extra string) string {
	const f = "20060102T150405Z"
	return fmt.Sprintf("BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//test//EN\nBEGIN:VEVENT\nUID:%s\nDTSTAMP:20260101T000000Z\n"+
		"DTSTART:%s\nDTEND:%s\nSUMMARY:%s\n%sEND:VEVENT\nEND:VCALENDAR\n", uid, t.UTC().Format(f), t.Add(d).UTC().Format(f), title, extra)
}

func tomorrowAt(hour int) time.Time {
	loc, _ := time.LoadLocation("Europe/Paris")
	d := time.Now().In(loc).AddDate(0, 0, 1)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, loc)
}

// connectCached serves the tools with a cache filled once, synchronously:
// later refreshes are the test's to call.
func (e *testEnv) connectCached(t *testing.T) (*mcp.ClientSession, *Calendars) {
	t.Helper()
	c, err := NewCalendars(e.cfg, e.hc)
	if err != nil {
		t.Fatal(err)
	}
	c.cache = newCalCache(CacheOptions{PastDays: 30, FutureDays: 90, Refresh: time.Hour}, nil)
	c.refreshAll(context.Background())
	return e.serve(t, c), c
}

func (e *testEnv) serve(t *testing.T, c *Calendars) *mcp.ClientSession {
	t.Helper()
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

func TestCacheServesEvents(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/personnel/dentist.ics", vevent("dentist-1", "Dentist", tomorrowAt(10), time.Hour, ""))
	e.perso.put(t, "/u/calendars/travail/weekly.ics", vevent("weekly-1", "Weekly", tomorrowAt(14), 30*time.Minute, "RRULE:FREQ=WEEKLY;COUNT=4\n"))
	live := call[listEventsOut](t, e.connect(t), "list_events", map[string]any{"days": 30})
	cs, c := e.connectCached(t)

	n := e.perso.fetches("")
	l := call[listEventsOut](t, cs, "list_events", map[string]any{"days": 30})
	if titles(l.Events) != "Dentist,Weekly,Weekly,Weekly,Weekly" || titles(l.Events) != titles(live.Events) || len(l.Errors) > 0 {
		t.Fatalf("cached %s, live %s, %v", titles(l.Events), titles(live.Events), l.Errors)
	}
	for i := range l.Events {
		if l.Events[i] != live.Events[i] {
			t.Errorf("cached %+v\nlive   %+v", l.Events[i], live.Events[i])
		}
	}
	day := tomorrowAt(0).Format(time.DateOnly)
	free := call[freeOut](t, cs, "find_free_time", map[string]any{"from": day, "to": tomorrowAt(0).AddDate(0, 0, 1).Format(time.DateOnly), "include_weekends": true})
	var got []string
	for _, s := range free.Slots {
		got = append(got, s.Start[11:16]+"-"+s.End[11:16])
	}
	if strings.Join(got, ",") != "09:00-10:00,11:00-14:00,14:30-18:00" {
		t.Errorf("free time: %v", got)
	}
	if e.perso.fetches("") != n {
		t.Fatalf("reads within the window fetched events: %d", e.perso.fetches("")-n)
	}

	// Outside the window: live.
	far := call[listEventsOut](t, cs, "list_events", map[string]any{"from": tomorrowAt(0).AddDate(0, 0, 120).Format(time.DateOnly), "days": 7})
	if len(far.Events) != 0 || e.perso.fetches("") == n {
		t.Errorf("outside the window: %s, fetches %d", titles(far.Events), e.perso.fetches("")-n)
	}

	// Without CTag, unchanged ETags skip the fetch; a change is fetched.
	n = e.perso.fetches("")
	c.refreshAll(context.Background())
	if e.perso.fetches("") != n {
		t.Errorf("unchanged calendars fetched again: %d", e.perso.fetches("")-n)
	}
	p, w := e.perso.fetches("/u/calendars/personnel/"), e.perso.fetches("/u/calendars/travail/")
	e.perso.put(t, "/u/calendars/personnel/call.ics", vevent("call-1", "Call", tomorrowAt(16), time.Hour, ""))
	c.refreshAll(context.Background())
	if e.perso.fetches("/u/calendars/personnel/") != p+1 || e.perso.fetches("/u/calendars/travail/") != w {
		t.Errorf("fetches: %v", e.perso.reports)
	}
	l = call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "calendar": "Personnel", "days": 3})
	if titles(l.Events) != "Dentist,Call" {
		t.Errorf("after refresh: %s", titles(l.Events))
	}
}

func TestCacheCTagSkips(t *testing.T) {
	e := newEnv(t)
	e.perso.ctag = true
	e.perso.put(t, "/u/calendars/personnel/dentist.ics", vevent("dentist-1", "Dentist", tomorrowAt(10), time.Hour, ""))
	_, c := e.connectCached(t)
	var mu sync.Mutex
	var changes []CalendarChange
	c.cache.onChange = func(ch CalendarChange) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, ch)
	}
	n := e.perso.fetches("")
	c.refreshAll(context.Background())
	if e.perso.fetches("") != n || len(changes) != 0 {
		t.Fatalf("same CTag, fetched %d, changes %v", e.perso.fetches("")-n, changes)
	}
	e.perso.put(t, "/u/calendars/personnel/call.ics", vevent("call-1", "Call", tomorrowAt(16), time.Hour, ""))
	c.refreshAll(context.Background())
	// The fake's CTag is per server: both calendars are fetched, one changed.
	if e.perso.fetches("") != n+2 || len(changes) != 1 || changes[0] != (CalendarChange{"icloud", "/u/calendars/personnel/"}) {
		t.Fatalf("new CTag: fetched %d, changes %v", e.perso.fetches("")-n, changes)
	}
}

func TestCacheAfterWrites(t *testing.T) {
	e := newEnv(t)
	c, err := NewCalendars(e.cfg, e.hc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.StartCache(ctx, CacheOptions{PastDays: 30, FutureDays: 90, Refresh: time.Hour})
	cs := e.serve(t, c) // answers at once, live until warm
	if l := call[listCalendarsOut](t, cs, "list_calendars", map[string]any{}); len(l.Calendars) != 5 {
		t.Fatalf("calendars: %+v", l)
	}
	warm := func() bool {
		c.cache.mu.RLock()
		defer c.cache.mu.RUnlock()
		return len(c.cache.at) == len(c.accounts) && c.cache.entries[calKey{"icloud", "/u/calendars/travail/"}] != nil
	}
	waitFor(t, warm)

	ev := call[Event](t, cs, "create_event", map[string]any{"account": "icloud", "calendar": "Travail", "title": "Review",
		"start": tomorrowAt(11).Format("2006-01-02T15:04"), "duration_minutes": 30})
	// Right after the write, whether live or refreshed already, it is there.
	l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "days": 3})
	if titles(l.Events) != "Review" {
		t.Fatalf("after create: %s", titles(l.Events))
	}
	waitFor(t, warm)
	n := e.perso.fetches("")
	l = call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "days": 3})
	if titles(l.Events) != "Review" || e.perso.fetches("") != n {
		t.Fatalf("refreshed after create: %s, fetches %d", titles(l.Events), e.perso.fetches("")-n)
	}
	call[deleteOut](t, cs, "delete_event", map[string]any{"id": ev.ID})
	if l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "days": 3}); len(l.Events) != 0 {
		t.Fatalf("after delete: %s", titles(l.Events))
	}

	// Microsoft Graph is cached too (fetched again at each refresh).
	call[Event](t, cs, "create_event", map[string]any{"account": "work", "calendar": "Calendar", "title": "Sync",
		"start": tomorrowAt(9).Format("2006-01-02T15:04")})
	waitFor(t, func() bool {
		_, ok := c.items(c.accounts[2], "AAMk-cal/1==", tomorrowAt(0), tomorrowAt(23))
		return ok
	})
	if l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "work", "days": 3}); titles(l.Events) != "Sync" {
		t.Fatalf("graph: %s", titles(l.Events))
	}
}

func TestCacheStaleGoesLive(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/personnel/dentist.ics", vevent("dentist-1", "Dentist", tomorrowAt(10), time.Hour, ""))
	cs, c := e.connectCached(t)
	c.cache.mu.Lock()
	for a := range c.cache.at {
		c.cache.at[a] = time.Now().Add(-5 * time.Hour) // the provider has been unreachable
	}
	c.cache.mu.Unlock()
	n := e.perso.fetches("")
	if l := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "days": 3}); titles(l.Events) != "Dentist" || e.perso.fetches("") == n {
		t.Fatalf("stale cache answered: %s", titles(l.Events))
	}
}

func TestCacheOptionsFromEnv(t *testing.T) {
	t.Setenv("CALENDAR_CACHE_PAST_DAYS", "7")
	t.Setenv("CALENDAR_CACHE_REFRESH", "10m")
	o, ok, err := cacheOptionsFromEnv()
	if err != nil || !ok || o.PastDays != 7 || o.FutureDays != 90 || o.Refresh != 10*time.Minute {
		t.Fatalf("%+v %v %v", o, ok, err)
	}
	t.Setenv("CALENDAR_CACHE_REFRESH", "1s")
	if _, _, err := cacheOptionsFromEnv(); err == nil {
		t.Error("a 1s refresh was accepted")
	}
	t.Setenv("CALENDAR_CACHE", "off")
	if _, ok, _ := cacheOptionsFromEnv(); ok {
		t.Error("CALENDAR_CACHE=off ignored")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}

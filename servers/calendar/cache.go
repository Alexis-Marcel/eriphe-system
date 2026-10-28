package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The cache keeps, in memory, the events of every calendar of every account
// over a window around today (30 days back, 90 ahead), recurring events
// expanded, so that list_events and find_free_time answer at once. It is
// refreshed every 5 minutes, cheaply: a CalDAV calendar whose CTag,
// sync-token or ETags did not change is not fetched again. A write made by
// this server drops the calendar's entry (reads go live) and refreshes it at
// once. Ranges outside the window, and everything until the first refresh is
// done, are served live as before.

// CacheOptions are the cache knobs, read from the environment.
type CacheOptions struct {
	PastDays, FutureDays int
	Refresh              time.Duration
}

// cacheOptionsFromEnv reads CALENDAR_CACHE_*; ok is false when
// CALENDAR_CACHE=off.
func cacheOptionsFromEnv() (o CacheOptions, ok bool, err error) {
	switch strings.ToLower(os.Getenv("CALENDAR_CACHE")) {
	case "off", "false", "0", "no":
		return o, false, nil
	}
	o = CacheOptions{PastDays: 30, FutureDays: 90, Refresh: 5 * time.Minute}
	for _, v := range []struct {
		key string
		n   *int
	}{{"CALENDAR_CACHE_PAST_DAYS", &o.PastDays}, {"CALENDAR_CACHE_FUTURE_DAYS", &o.FutureDays}} {
		if s := os.Getenv(v.key); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 366 {
				return o, false, fmt.Errorf("%s: a number of days, 1 to 366", v.key)
			}
			*v.n = n
		}
	}
	if s := os.Getenv("CALENDAR_CACHE_REFRESH"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 30*time.Second {
			return o, false, errors.New("CALENDAR_CACHE_REFRESH: a duration of at least 30s, e.g. 5m")
		}
		o.Refresh = d
	}
	return o, true, nil
}

// CalendarChange is what a refresh found changed: the event a change
// notification would carry.
type CalendarChange struct {
	Account, Calendar string // calendar id
}

type calKey struct{ account, cal string }

type calEntry struct {
	from, to time.Time
	version  string // CTag, sync-token or ETag digest; empty: unknown
	items    []Item
}

type calCache struct {
	opts     CacheOptions
	onChange func(CalendarChange)

	mu      sync.RWMutex
	cals    map[*account][]CalendarInfo
	entries map[calKey]*calEntry
	gen     map[calKey]int // bumped by writes: a refresh started before is not stored
	at      map[*account]time.Time

	kick chan *account
}

// StartCache fills the cache in the background and keeps it fresh; it
// returns at once and tools are served live until it is warm.
func (c *Calendars) StartCache(ctx context.Context, o CacheOptions) {
	c.cache = newCalCache(o, c.OnChange)
	go c.cacheLoop(ctx)
}

func newCalCache(o CacheOptions, onChange func(CalendarChange)) *calCache {
	return &calCache{opts: o, onChange: onChange, cals: map[*account][]CalendarInfo{},
		entries: map[calKey]*calEntry{}, gen: map[calKey]int{}, at: map[*account]time.Time{}, kick: make(chan *account, 16)}
}

func (c *Calendars) cacheLoop(ctx context.Context) {
	c.refreshAll(ctx)
	t := time.NewTicker(c.cache.opts.Refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.refreshAll(ctx)
		case a := <-c.cache.kick:
			c.refresh(ctx, a)
		}
	}
}

// window is the cached range: whole days in the configured zone.
func (cc *calCache) window(now time.Time, loc *time.Location) (time.Time, time.Time) {
	d := now.In(loc)
	today := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	return today.AddDate(0, 0, -cc.opts.PastDays), today.AddDate(0, 0, cc.opts.FutureDays+1)
}

func (c *Calendars) refreshAll(ctx context.Context) {
	each(len(c.accounts), func(i int) { c.refresh(ctx, c.accounts[i]) })
}

// versioner is a provider that can tell cheaply whether a calendar changed.
type versioner interface {
	Version(ctx context.Context, cal CalendarInfo, from, to time.Time) (string, error)
}

// refresh updates the cache of one account and logs one line.
func (c *Calendars) refresh(ctx context.Context, a *account) {
	cc := c.cache
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Duration(c.cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	cals, err := a.p.Calendars(ctx)
	if err != nil {
		log.Printf("calendar sync account=%s error=%q ms=%d", a.cfg.Name, err.Error(), time.Since(start).Milliseconds())
		return
	}
	from, to := cc.window(start, c.cfg.loc)
	var fetched, unchanged, failed, events int
	var mu sync.Mutex
	each(len(cals), func(i int) {
		cal := cals[i]
		k := calKey{a.cfg.Name, cal.ID}
		cc.mu.RLock()
		old, gen := cc.entries[k], cc.gen[k]
		cc.mu.RUnlock()
		var version string
		if v, ok := a.p.(versioner); ok {
			version, _ = v.Version(ctx, cal, from, to)
		}
		if old != nil && version != "" && version == old.version && old.from.Equal(from) && old.to.Equal(to) {
			mu.Lock()
			unchanged++
			events += len(old.items)
			mu.Unlock()
			return
		}
		items, err := a.p.Events(ctx, cal, from, to)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failed++
			return
		}
		fetched++
		events += len(items)
		cc.mu.Lock()
		stored := cc.gen[k] == gen
		if stored {
			cc.entries[k] = &calEntry{from: from, to: to, version: version, items: items}
		}
		cc.mu.Unlock()
		if stored && old != nil && cc.onChange != nil && !sameItems(old.items, items) {
			cc.onChange(CalendarChange{a.cfg.Name, cal.ID})
		}
	})
	cc.mu.Lock()
	cc.cals[a] = cals
	cc.at[a] = time.Now()
	for k := range cc.entries {
		if k.account == a.cfg.Name && !hasCalendar(cals, k.cal) {
			delete(cc.entries, k)
		}
	}
	cc.mu.Unlock()
	log.Printf("calendar sync account=%s calendars=%d fetched=%d unchanged=%d failed=%d events=%d ms=%d",
		a.cfg.Name, len(cals), fetched, unchanged, failed, events, time.Since(start).Milliseconds())
}

func hasCalendar(cals []CalendarInfo, id string) bool {
	for _, c := range cals {
		if c.ID == id {
			return true
		}
	}
	return false
}

// sameItems compares two event lists, in any order.
func sameItems(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(l []Item) []string {
		out := make([]string, len(l))
		for i, it := range l {
			out[i] = fmt.Sprintf("%s|%d|%d|%t|%t|%t|%t|%q|%q|%q", it.ID, it.Start.Unix(), it.End.Unix(),
				it.AllDay, it.Recurring, it.Free, it.Cancelled, it.Title, it.Location, it.Description)
		}
		sort.Strings(out)
		return out
	}
	ka, kb := key(a), key(b)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// fresh tells whether the account was refreshed lately: while its provider
// is unreachable, requests go live (caller holds mu).
func (cc *calCache) fresh(a *account) bool {
	at, ok := cc.at[a]
	return ok && time.Since(at) < 2*cc.opts.Refresh+time.Minute
}

// calendars returns the account's calendars from the cache when it is warm.
func (c *Calendars) calendars(ctx context.Context, a *account) ([]CalendarInfo, error) {
	if cc := c.cache; cc != nil {
		cc.mu.RLock()
		cals, ok := cc.cals[a]
		ok = ok && cc.fresh(a)
		cc.mu.RUnlock()
		if ok {
			return cals, nil
		}
	}
	return a.p.Calendars(ctx)
}

// items returns the cached events of a calendar overlapping [from, to), if
// the window covers that range.
func (c *Calendars) items(a *account, cal string, from, to time.Time) ([]Item, bool) {
	cc := c.cache
	if cc == nil {
		return nil, false
	}
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	e := cc.entries[calKey{a.cfg.Name, cal}]
	if e == nil || !cc.fresh(a) || from.Before(e.from) || to.After(e.to) {
		return nil, false
	}
	var out []Item
	for _, it := range e.items {
		if it.End.After(from) && it.Start.Before(to) || (it.Start.Equal(it.End) && !it.Start.Before(from) && it.Start.Before(to)) {
			out = append(out, it)
		}
	}
	return out, true
}

// changed is called after a write to a calendar: reads go live until the
// calendar is fetched again, which is asked at once.
func (c *Calendars) changed(a *account, cal string) {
	cc := c.cache
	if cc == nil {
		return
	}
	k := calKey{a.cfg.Name, cal}
	cc.mu.Lock()
	delete(cc.entries, k)
	cc.gen[k]++
	cc.mu.Unlock()
	select {
	case cc.kick <- a:
	default:
	}
	if cc.onChange != nil {
		cc.onChange(CalendarChange{a.cfg.Name, cal})
	}
}

// --- CalDAV versions ---

// Version is the calendar's CTag or sync-token (RFC 6578) when the server
// gives one, otherwise a digest of the ETags of the events in [from, to):
// what changes whenever an event of the calendar changes.
func (d *CalDAV) Version(ctx context.Context, cal CalendarInfo, from, to time.Time) (string, error) {
	const body = `<d:propfind xmlns:d="DAV:" xmlns:cs="http://calendarserver.org/ns/"><d:prop><cs:getctag/><d:sync-token/></d:prop></d:propfind>`
	var ms struct {
		Responses []struct {
			CTag      string `xml:"propstat>prop>getctag"`
			SyncToken string `xml:"propstat>prop>sync-token"`
		} `xml:"response"`
	}
	if err := d.multistatus(ctx, "PROPFIND", cal.ID, body, "0", &ms); err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		if r.CTag = strings.TrimSpace(r.CTag); r.CTag != "" {
			return "ctag:" + r.CTag, nil
		}
		if r.SyncToken = strings.TrimSpace(r.SyncToken); r.SyncToken != "" {
			return "sync:" + r.SyncToken, nil
		}
	}
	const stamp = "20060102T150405Z"
	query := fmt.Sprintf(`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`+
		`<d:prop><d:getetag/></d:prop><c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">`+
		`<c:time-range start="%s" end="%s"/></c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`,
		from.UTC().Format(stamp), to.UTC().Format(stamp))
	var tags struct {
		Responses []struct {
			Href string `xml:"href"`
			ETag string `xml:"propstat>prop>getetag"`
		} `xml:"response"`
	}
	if err := d.multistatus(ctx, "REPORT", cal.ID, query, "1", &tags); err != nil {
		return "", err
	}
	var lines []string
	for _, r := range tags.Responses {
		if r.ETag == "" {
			return "", nil // no ETags: no way to tell
		}
		href := r.Href
		if u, err := url.Parse(href); err == nil {
			href = u.Path
		}
		lines = append(lines, href+" "+r.ETag)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "etags:" + hex.EncodeToString(sum[:]), nil
}

func (d *CalDAV) multistatus(ctx context.Context, method, path, body, depth string, v any) error {
	resp, err := d.do(ctx, method, path, []byte(body), map[string]string{
		"Content-Type": "application/xml; charset=utf-8", "Depth": depth,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return statusError(d.acct.Name, method, resp)
	}
	return xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

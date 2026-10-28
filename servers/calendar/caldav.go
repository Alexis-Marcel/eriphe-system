package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

// CalDAV is a Provider over CalDAV: iCloud, Google, Fastmail, Nextcloud and
// other servers.
type CalDAV struct {
	acct     *AccountConfig
	hc       *authClient
	client   *caldav.Client
	endpoint *url.URL
	loc      *time.Location

	mu   sync.Mutex
	base *url.URL // host serving the calendars (iCloud: pNN-caldav.icloud.com)
	cals []CalendarInfo
	at   time.Time
}

func newCalDAV(acct *AccountConfig, hc *http.Client, loc *time.Location) (*CalDAV, error) {
	ac := &authClient{acct: acct, http: hc}
	c, err := caldav.NewClient(ac, acct.URL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(acct.URL)
	if err != nil {
		return nil, err
	}
	return &CalDAV{acct: acct, hc: ac, client: c, endpoint: u, base: u, loc: loc}, nil
}

func (d *CalDAV) Calendars(ctx context.Context) ([]CalendarInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cals != nil && time.Since(d.at) < 15*time.Minute {
		return d.cals, nil
	}
	var principal string
	if d.acct.Provider == "google" && d.acct.Username != "" {
		principal = "/caldav/v2/" + url.PathEscape(d.acct.Username) + "/user"
	} else {
		p, err := d.client.FindCurrentUserPrincipal(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: calendar login: %w", d.acct.Name, err)
		}
		principal = p
	}
	home, err := d.client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.acct.Name, err)
	}
	all, err := d.client.FindCalendars(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.acct.Name, err)
	}
	if u, err := d.endpoint.Parse(home); err == nil && u.Host != "" {
		d.base = &url.URL{Scheme: u.Scheme, Host: u.Host}
	}
	var cals []CalendarInfo
	for _, c := range all {
		events := len(c.SupportedComponentSet) == 0
		for _, comp := range c.SupportedComponentSet {
			events = events || strings.EqualFold(comp, ical.CompEvent)
		}
		if events {
			name := c.Name
			if name == "" {
				name = strings.Trim(c.Path[strings.LastIndex(strings.TrimSuffix(c.Path, "/"), "/")+1:], "/")
			}
			cals = append(cals, CalendarInfo{ID: c.Path, Name: name})
		}
	}
	sort.Slice(cals, func(i, j int) bool { return cals[i].Name < cals[j].Name })
	d.cals, d.at = cals, time.Now()
	return cals, nil
}

func (d *CalDAV) url(path string) (string, error) {
	d.mu.Lock()
	base := d.base
	d.mu.Unlock()
	u, err := base.Parse(path)
	if err != nil {
		return "", err
	}
	if u.Host != base.Host && u.Host != d.endpoint.Host {
		return "", fmt.Errorf("refusing a path on another host: %s", u.Host)
	}
	return u.String(), nil
}

func (d *CalDAV) do(ctx context.Context, method, path string, body []byte, hdr map[string]string) (*http.Response, error) {
	// Discovery first: it tells which host serves the calendars.
	d.mu.Lock()
	discovered := d.cals != nil
	d.mu.Unlock()
	if !discovered {
		if _, err := d.Calendars(ctx); err != nil {
			return nil, err
		}
	}
	u, err := d.url(path)
	if err != nil {
		return nil, err
	}
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.acct.Name, err)
	}
	return resp, nil
}

func statusError(acct, what string, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return errNotFound
	case http.StatusPreconditionFailed:
		return fmt.Errorf("%s: %s: the event changed meanwhile, read it again", acct, what)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: %s: not allowed (%s)", acct, what, resp.Status)
	}
	return fmt.Errorf("%s: %s: %s", acct, what, resp.Status)
}

// Events sends the calendar-query REPORT by hand: iCloud returns empty events
// for the query go-webdav builds. The server is asked to expand recurring
// events; when it does not, they are expanded here.
func (d *CalDAV) Events(ctx context.Context, cal CalendarInfo, from, to time.Time) ([]Item, error) {
	const stamp = "20060102T150405Z"
	body := fmt.Sprintf(`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`+
		`<d:prop><d:getetag/><c:calendar-data><c:expand start="%[1]s" end="%[2]s"/></c:calendar-data></d:prop>`+
		`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">`+
		`<c:time-range start="%[1]s" end="%[2]s"/></c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`,
		from.UTC().Format(stamp), to.UTC().Format(stamp))
	resp, err := d.do(ctx, "REPORT", cal.ID, []byte(body), map[string]string{
		"Content-Type": "application/xml; charset=utf-8", "Depth": "1",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, statusError(d.acct.Name, "calendar "+cal.Name, resp)
	}
	var ms struct {
		Responses []struct {
			Href string `xml:"href"`
			Data string `xml:"propstat>prop>calendar-data"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("%s: calendar %s: %w", d.acct.Name, cal.Name, err)
	}
	var out []Item
	for _, r := range ms.Responses {
		if r.Data == "" {
			continue
		}
		ic, err := ical.NewDecoder(strings.NewReader(r.Data)).Decode()
		if err != nil {
			continue
		}
		href := r.Href
		if u, err := url.Parse(href); err == nil {
			href = u.Path
		}
		out = append(out, d.occurrences(ic, href, cal.ID, from, to)...)
	}
	return out, nil
}

// occurrences turns a calendar object into the event instances within
// [from, to).
func (d *CalDAV) occurrences(ic *ical.Calendar, href, calID string, from, to time.Time) []Item {
	var out []Item
	overridden := map[int64]bool{}
	var masters []ical.Event
	for _, ev := range ic.Events() {
		if ev.Props.Get(ical.PropRecurrenceID) != nil {
			it := d.item(ev, href, calID)
			it.Recurring = true
			if rid, err := propTime(ev.Props.Get(ical.PropRecurrenceID), d.loc); err == nil {
				overridden[rid.Unix()] = true
			}
			out = append(out, it)
			continue
		}
		masters = append(masters, ev)
	}
	for _, ev := range masters {
		it := d.item(ev, href, calID)
		if ev.Props.Get(ical.PropRecurrenceRule) == nil {
			out = append(out, it)
			continue
		}
		set, err := ev.RecurrenceSet(d.loc)
		if err != nil || set == nil {
			out = append(out, it)
			continue
		}
		dur := it.End.Sub(it.Start)
		for _, t := range set.Between(from.Add(-dur), to, true) {
			if overridden[t.Unix()] {
				continue
			}
			occ := it
			occ.Start, occ.End, occ.Recurring = t, t.Add(dur), true
			if occ.AllDay {
				occ.Start = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, d.loc)
				occ.End = occ.Start.AddDate(0, 0, int((dur+time.Hour)/(24*time.Hour)))
			}
			out = append(out, occ)
		}
	}
	var kept []Item
	for _, it := range out {
		if it.Cancelled {
			continue
		}
		if it.End.After(from) && it.Start.Before(to) || (it.Start.Equal(it.End) && !it.Start.Before(from) && it.Start.Before(to)) {
			kept = append(kept, it)
		}
	}
	return kept
}

// propTime reads a date or date-time; an unknown TZID (e.g. a Windows zone
// name) falls back to the configured zone.
func propTime(p *ical.Prop, loc *time.Location) (time.Time, error) {
	if p == nil {
		return time.Time{}, errors.New("missing")
	}
	t, err := p.DateTime(loc)
	if err != nil && p.Params.Get(ical.PropTimezoneID) != "" {
		q := *p
		q.Params = ical.Params{}
		for k, v := range p.Params {
			if k != ical.PropTimezoneID {
				q.Params[k] = v
			}
		}
		return q.DateTime(loc)
	}
	return t, err
}

func isDate(p *ical.Prop) bool {
	return p != nil && (p.ValueType() == ical.ValueDate || len(p.Value) == 8)
}

func (d *CalDAV) item(ev ical.Event, href, calID string) Item {
	it := Item{ID: href, CalendarID: calID}
	it.Title, _ = ev.Props.Text(ical.PropSummary)
	it.Location, _ = ev.Props.Text(ical.PropLocation)
	it.Description, _ = ev.Props.Text(ical.PropDescription)
	if v, _ := ev.Props.Text(ical.PropTransparency); strings.EqualFold(v, "TRANSPARENT") {
		it.Free = true
	}
	if v, _ := ev.Props.Text(ical.PropStatus); strings.EqualFold(v, "CANCELLED") {
		it.Cancelled = true
	}
	it.Recurring = ev.Props.Get(ical.PropRecurrenceRule) != nil
	start := ev.Props.Get(ical.PropDateTimeStart)
	it.AllDay = isDate(start)
	it.Start, _ = propTime(start, d.loc)
	if end := ev.Props.Get(ical.PropDateTimeEnd); end != nil {
		it.End, _ = propTime(end, d.loc)
	} else if t, err := ev.DateTimeEnd(d.loc); err == nil {
		it.End = t
	}
	if it.End.Before(it.Start) || it.End.IsZero() {
		it.End = it.Start
		if it.AllDay {
			it.End = it.Start.AddDate(0, 0, 1)
		}
	}
	return it
}

// object fetches a calendar object and its ETag.
func (d *CalDAV) object(ctx context.Context, path string) (*ical.Calendar, string, error) {
	resp, err := d.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", statusError(d.acct.Name, "read event", resp)
	}
	ic, err := ical.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode()
	if err != nil {
		return nil, "", fmt.Errorf("%s: event: %w", d.acct.Name, err)
	}
	return ic, resp.Header.Get("ETag"), nil
}

func master(ic *ical.Calendar) *ical.Component {
	for _, c := range ic.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) == nil {
			return c
		}
	}
	return nil
}

func (d *CalDAV) Get(ctx context.Context, calID, id string) (*Item, error) {
	ic, _, err := d.object(ctx, id)
	if err != nil {
		return nil, err
	}
	m := master(ic)
	if m == nil {
		return nil, errNotFound
	}
	it := d.item(ical.Event{Component: m}, id, calID)
	return &it, nil
}

func (d *CalDAV) put(ctx context.Context, path string, ic *ical.Calendar, hdr map[string]string) error {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(ic); err != nil {
		return err
	}
	hdr["Content-Type"] = "text/calendar; charset=utf-8"
	resp, err := d.do(ctx, http.MethodPut, path, buf.Bytes(), hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusError(d.acct.Name, "save event", resp)
	}
	return nil
}

func (d *CalDAV) Create(ctx context.Context, calID string, in EventInput) (*Item, error) {
	uid := randomID()
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, uid+"@eriphe")
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	d.setTimes(ev.Component, in.Start, in.End, in.AllDay)
	setText(ev.Component, ical.PropSummary, in.Title)
	setText(ev.Component, ical.PropLocation, in.Location)
	setText(ev.Component, ical.PropDescription, in.Description)
	ic := ical.NewCalendar()
	ic.Props.SetText(ical.PropVersion, "2.0")
	ic.Props.SetText(ical.PropProductID, "-//Eriphe//Calendar//EN")
	ic.Children = append(ic.Children, ev.Component)
	path := strings.TrimSuffix(calID, "/") + "/" + uid + ".ics"
	if err := d.put(ctx, path, ic, map[string]string{"If-None-Match": "*"}); err != nil {
		return nil, err
	}
	it := d.item(*ev, path, calID)
	return &it, nil
}

func (d *CalDAV) Update(ctx context.Context, calID, id string, p EventPatch) (*Item, error) {
	ic, etag, err := d.object(ctx, id)
	if err != nil {
		return nil, err
	}
	m := master(ic)
	if m == nil {
		return nil, errNotFound
	}
	cur := d.item(ical.Event{Component: m}, id, calID)
	start, end, allDay := cur.Start, cur.End, cur.AllDay
	if p.AllDay != nil {
		allDay = *p.AllDay
	}
	if p.Start != nil {
		end = p.Start.Add(end.Sub(start))
		start = *p.Start
	}
	if p.End != nil {
		end = *p.End
	}
	if p.Start != nil || p.End != nil || p.AllDay != nil {
		d.setTimes(m, start, end, allDay)
	}
	if p.Title != nil {
		setText(m, ical.PropSummary, *p.Title)
	}
	if p.Location != nil {
		setText(m, ical.PropLocation, *p.Location)
	}
	if p.Description != nil {
		setText(m, ical.PropDescription, *p.Description)
	}
	m.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	m.Props.SetDateTime(ical.PropLastModified, time.Now().UTC())
	seq := 0
	if sp := m.Props.Get(ical.PropSequence); sp != nil {
		seq, _ = sp.Int()
	}
	sp := ical.NewProp(ical.PropSequence)
	sp.Value = fmt.Sprint(seq + 1)
	m.Props.Set(sp)
	hdr := map[string]string{}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	if err := d.put(ctx, id, ic, hdr); err != nil {
		return nil, err
	}
	it := d.item(ical.Event{Component: m}, id, calID)
	return &it, nil
}

func (d *CalDAV) Delete(ctx context.Context, calID, id string) error {
	resp, err := d.do(ctx, http.MethodDelete, id, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusError(d.acct.Name, "delete event", resp)
	}
	return nil
}

// setTimes writes DTSTART and DTEND: dates for all-day events, UTC times
// otherwise (no VTIMEZONE needed, every server accepts them).
func (d *CalDAV) setTimes(c *ical.Component, start, end time.Time, allDay bool) {
	c.Props.Del(ical.PropDuration)
	if allDay {
		s, e := start.In(d.loc), end.In(d.loc)
		c.Props.SetDate(ical.PropDateTimeStart, s)
		c.Props.SetDate(ical.PropDateTimeEnd, e)
		return
	}
	c.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
	c.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())
}

func setText(c *ical.Component, name, v string) {
	if v == "" {
		c.Props.Del(name)
		return
	}
	c.Props.SetText(name, v)
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

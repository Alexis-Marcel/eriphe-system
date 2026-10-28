package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

// --- CalDAV ---

// memCalDAV is an in-memory CalDAV backend for one user.
type memCalDAV struct {
	mu   sync.Mutex
	cals []caldav.Calendar
	objs map[string]*caldav.CalendarObject
	n    int

	// ctag makes the calendars answer a CTag (getctag), as iCloud does;
	// go-webdav has none, so the cache falls back to ETags without it.
	ctag    bool
	reports map[string]int // calendar-data REPORTs per calendar path
}

func newMemCalDAV(names ...string) *memCalDAV {
	m := &memCalDAV{objs: map[string]*caldav.CalendarObject{}, reports: map[string]int{}}
	for _, n := range names {
		m.cals = append(m.cals, caldav.Calendar{Path: "/u/calendars/" + strings.ToLower(n) + "/", Name: n,
			SupportedComponentSet: []string{ical.CompEvent}})
	}
	// A task list, not shown.
	m.cals = append(m.cals, caldav.Calendar{Path: "/u/calendars/tasks/", Name: "Tasks", SupportedComponentSet: []string{ical.CompToDo}})
	return m
}

func (m *memCalDAV) CurrentUserPrincipal(context.Context) (string, error) { return "/u/", nil }
func (m *memCalDAV) CalendarHomeSetPath(context.Context) (string, error)  { return "/u/calendars/", nil }
func (m *memCalDAV) CreateCalendar(context.Context, *caldav.Calendar) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.New("no"))
}
func (m *memCalDAV) ListCalendars(context.Context) ([]caldav.Calendar, error) { return m.cals, nil }
func (m *memCalDAV) GetCalendar(_ context.Context, p string) (*caldav.Calendar, error) {
	for _, c := range m.cals {
		if c.Path == p {
			return &c, nil
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("no calendar"))
}

func (m *memCalDAV) GetCalendarObject(_ context.Context, p string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.objs[p]; ok {
		return o, nil
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, errors.New("no object"))
}

func (m *memCalDAV) ListCalendarObjects(_ context.Context, p string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []caldav.CalendarObject
	for k, o := range m.objs {
		if strings.HasPrefix(k, p) {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (m *memCalDAV) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	all, err := m.ListCalendarObjects(ctx, p, nil)
	if err != nil {
		return nil, err
	}
	// Filter one by one: an object go-webdav cannot evaluate (a Windows
	// time zone name) is returned anyway, as a real server would.
	var out []caldav.CalendarObject
	for _, o := range all {
		r, err := caldav.Filter(q, []caldav.CalendarObject{o})
		if err != nil || len(r) > 0 {
			out = append(out, o)
		}
	}
	return out, nil
}

func (m *memCalDAV) PutCalendarObject(_ context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, exists := m.objs[p]
	if opts != nil && opts.IfNoneMatch.IsWildcard() && exists {
		return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("exists"))
	}
	if opts != nil && opts.IfMatch.IsSet() {
		if ok, _ := opts.IfMatch.MatchETag(cur.ETag); !exists || !ok {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("etag"))
		}
	}
	m.n++
	o := &caldav.CalendarObject{Path: p, ETag: "e" + strconv.Itoa(m.n), ModTime: time.Now(), Data: cal}
	m.objs[p] = o
	return o, nil
}

func (m *memCalDAV) DeleteCalendarObject(_ context.Context, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objs[p]; !ok {
		return webdav.NewHTTPError(http.StatusNotFound, errors.New("no object"))
	}
	delete(m.objs, p)
	m.n++
	return nil
}

// ctagOf changes whenever an event of the calendar changes (here: of any).
func (m *memCalDAV) ctagOf(string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return "c" + strconv.Itoa(m.n)
}

// fetches counts the event fetches (calendar-data REPORTs) so far.
func (m *memCalDAV) fetches(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path != "" {
		return m.reports[path]
	}
	n := 0
	for _, v := range m.reports {
		n += v
	}
	return n
}

// put stores raw iCalendar text.
func (m *memCalDAV) put(t *testing.T, path, ics string) {
	t.Helper()
	ic, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(ics, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutCalendarObject(context.Background(), path, ic, nil); err != nil {
		t.Fatal(err)
	}
}

func (m *memCalDAV) raw(t *testing.T, path string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objs[path]
	if !ok {
		return ""
	}
	var b strings.Builder
	ical.NewEncoder(&b).Encode(o.Data)
	return b.String()
}

func (m *memCalDAV) paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var p []string
	for k := range m.objs {
		p = append(p, k)
	}
	sort.Strings(p)
	return p
}

// serveCalDAV serves the backend over HTTPS with basic auth.
func serveCalDAV(t *testing.T, b *memCalDAV, user, pass string) *httptest.Server {
	h := &caldav.Handler{Backend: b}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		switch {
		case r.Method == "PROPFIND" && b.ctag && strings.Contains(string(body), "getctag"):
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprintf(w, `<d:multistatus xmlns:d="DAV:" xmlns:cs="http://calendarserver.org/ns/"><d:response><d:href>%s</d:href>`+
				`<d:propstat><d:prop><cs:getctag>%s</cs:getctag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`,
				r.URL.Path, b.ctagOf(r.URL.Path))
			return
		case r.Method == "REPORT" && strings.Contains(string(body), "calendar-data"):
			b.mu.Lock()
			b.reports[r.URL.Path]++
			b.mu.Unlock()
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- Microsoft Graph ---

type fakeGraph struct {
	mu     sync.Mutex
	token  string
	cals   []map[string]any
	events map[string]map[string]any // id -> event (start/end in UTC)
	calOf  map[string]string
	n      int
	page   int
}

func newFakeGraph(token string) *fakeGraph {
	return &fakeGraph{token: token, events: map[string]map[string]any{}, calOf: map[string]string{}, page: 1,
		cals: []map[string]any{{"id": "AAMk-cal/1==", "name": "Calendar", "canEdit": true}, {"id": "AAMk-hol", "name": "Holidays", "canEdit": false}}}
}

func (f *fakeGraph) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv
}

func graphErr(w http.ResponseWriter, code int, c string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": c, "message": c}})
}

func (f *fakeGraph) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		graphErr(w, 401, "InvalidAuthenticationToken")
		return
	}
	if !strings.Contains(r.Header.Get("Prefer"), `outlook.timezone="UTC"`) {
		graphErr(w, 400, "NoPrefer")
		return
	}
	p := strings.TrimPrefix(r.URL.EscapedPath(), "/v1.0")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "GET" && p == "/me/calendars":
		json.NewEncoder(w).Encode(map[string]any{"value": f.cals})
	case r.Method == "GET" && strings.HasSuffix(p, "/calendarView"):
		cal, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(p, "/me/calendars/"), "/calendarView"))
		from, _ := time.Parse(time.RFC3339, r.URL.Query().Get("startDateTime"))
		to, _ := time.Parse(time.RFC3339, r.URL.Query().Get("endDateTime"))
		var ids []string
		for id, e := range f.events {
			s, en := gtime(e["start"]), gtime(e["end"])
			if f.calOf[id] == cal && en.After(from) && s.Before(to) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		skip, _ := strconv.Atoi(r.URL.Query().Get("$skip"))
		page := map[string]any{"value": []any{}}
		var vals []any
		for i := skip; i < len(ids) && i < skip+f.page; i++ {
			vals = append(vals, f.events[ids[i]])
		}
		if vals != nil {
			page["value"] = vals
		}
		if skip+f.page < len(ids) {
			q := r.URL.Query()
			q.Set("$skip", strconv.Itoa(skip+f.page))
			page["@odata.nextLink"] = "https://" + r.Host + "/v1.0" + p + "?" + q.Encode()
		}
		json.NewEncoder(w).Encode(page)
	case r.Method == "POST" && strings.HasSuffix(p, "/events"):
		cal, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(p, "/me/calendars/"), "/events"))
		var e map[string]any
		json.NewDecoder(r.Body).Decode(&e)
		f.n++
		id := fmt.Sprintf("AAMkEv%d-x_y==", f.n)
		e["id"], e["type"], e["showAs"] = id, "singleInstance", "busy"
		f.events[id], f.calOf[id] = e, cal
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(e)
	case strings.HasPrefix(p, "/me/events/"):
		id, _ := url.PathUnescape(strings.TrimPrefix(p, "/me/events/"))
		e, ok := f.events[id]
		if !ok {
			graphErr(w, 404, "ErrorItemNotFound")
			return
		}
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(e)
		case "PATCH":
			var patch map[string]any
			json.NewDecoder(r.Body).Decode(&patch)
			for k, v := range patch {
				e[k] = v
			}
			json.NewEncoder(w).Encode(e)
		case "DELETE":
			delete(f.events, id)
			w.WriteHeader(204)
		}
	default:
		graphErr(w, 400, "BadRequest "+r.Method+" "+p)
	}
}

func gtime(v any) time.Time {
	m, _ := v.(map[string]any)
	s, _ := m["dateTime"].(string)
	t, _ := time.Parse("2006-01-02T15:04:05", s[:min(len(s), 19)])
	return t
}

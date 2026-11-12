package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// textSurface writes a surface of one text, by the brain.
func textSurface(t *testing.T, c *core, id, text string) {
	t.Helper()
	raw, _ := json.Marshal(text)
	_, errs, err := c.surfaceWrite(surfaceAuthorBrain, surfaceIn{ID: id, Title: "Titre " + id,
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":` + string(raw) + `}]`)})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
}

// a2uiStream reads the "a2ui" events of an event stream.
type a2uiStream struct {
	t      *testing.T
	events chan a2uiEvent
}

func openA2UI(t *testing.T, tw *testWeb, query string) *a2uiStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events"+query, nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: %d", resp.StatusCode)
	}
	t.Cleanup(func() { resp.Body.Close() })
	s := &a2uiStream{t: t, events: make(chan a2uiEvent, 100)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "a2ui" {
				var ev a2uiEvent
				if json.Unmarshal([]byte(data), &ev) == nil {
					s.events <- ev
				}
			}
		}
		close(s.events)
	}()
	return s
}

func (s *a2uiStream) next() a2uiEvent {
	s.t.Helper()
	select {
	case ev, ok := <-s.events:
		if !ok {
			s.t.Fatal("stream closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		s.t.Fatal("no a2ui event")
	}
	return a2uiEvent{}
}

// messages reads events until n messages came; their seqs must grow.
func (s *a2uiStream) messages(n int, last int64) []a2ui.Message {
	s.t.Helper()
	var out []a2ui.Message
	for len(out) < n {
		ev := s.next()
		if ev.Reset || ev.Seq < last+int64(len(ev.Messages)) {
			s.t.Fatalf("event out of order after seq %d: %+v", last, ev)
		}
		last = ev.Seq
		out = append(out, ev.Messages...)
	}
	return out
}

func kinds(msgs []a2ui.Message) string {
	var out []string
	for _, m := range msgs {
		switch {
		case m.CreateSurface != nil:
			out = append(out, "create "+m.CreateSurface.SurfaceID)
		case m.UpdateComponents != nil:
			out = append(out, "components "+m.UpdateComponents.SurfaceID)
		case m.UpdateDataModel != nil:
			out = append(out, "data "+m.UpdateDataModel.SurfaceID)
		case m.DeleteSurface != nil:
			out = append(out, "delete "+m.DeleteSurface.SurfaceID)
		}
	}
	return strings.Join(out, ", ")
}

// The workspace's event stream sends the surfaces: a snapshot that resets
// the page on connect, then every change in order; after it fell behind,
// a new snapshot. A stream that did not ask gets none.
func TestSurfaceStream(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	textSurface(t, tw.c, "first", "Un")

	st := openA2UI(t, tw, "?a2ui=1")
	snap := st.next()
	if !snap.Reset || kinds(snap.Messages) != "create first" {
		t.Fatalf("snapshot: %+v", snap)
	}
	meta := string(snap.Messages[0].CreateSurface.Metadata)
	if !strings.Contains(meta, `"author":"brain"`) || !strings.Contains(meta, `"title":"Titre first"`) {
		t.Errorf("metadata: %s", meta)
	}
	raw, _ := json.Marshal(snap.Messages)
	if errs := a2ui.Validate(raw); len(errs) > 0 {
		t.Fatalf("snapshot not A2UI: %v", errs)
	}

	// Changes, in the order they were made.
	textSurface(t, tw.c, "second", "Deux")
	textSurface(t, tw.c, "first", "Un, encore")
	if err := tw.c.surfaceRemove("first", surfaceByUser); err != nil {
		t.Fatal(err)
	}
	got := st.messages(4, snap.Seq)
	if k := kinds(got); k != "create second, components first, data first, delete first" {
		t.Fatalf("changes: %s", k)
	}
	all, _ := json.Marshal(append(snap.Messages, got...))
	if errs := a2ui.Validate(all); len(errs) > 0 {
		t.Fatalf("stream not A2UI: %v", errs)
	}

	// The hub drops the stream, as when it falls behind: a new snapshot,
	// that resets the page.
	tw.c.surf.mu.Lock()
	for sub := range tw.c.surf.subs {
		delete(tw.c.surf.subs, sub)
		close(sub)
	}
	tw.c.surf.mu.Unlock()
	again := st.next()
	if !again.Reset || kinds(again.Messages) != "create second" {
		t.Fatalf("after falling behind: %+v", again)
	}
	// And it follows again.
	textSurface(t, tw.c, "third", "Trois")
	if k := kinds(st.messages(1, again.Seq)); k != "create third" {
		t.Fatalf("after the reset: %s", k)
	}

	// A reconnecting page starts from a snapshot, empty or not.
	if err := tw.c.surfaceRemove("second", surfaceByUser); err != nil {
		t.Fatal(err)
	}
	if err := tw.c.surfaceRemove("third", surfaceByUser); err != nil {
		t.Fatal(err)
	}
	st2 := openA2UI(t, tw, "?a2ui=1")
	if ev := st2.next(); !ev.Reset || len(ev.Messages) != 0 || ev.Messages == nil {
		t.Fatalf("empty snapshot: %+v", ev)
	}

	// The conversation's stream does not ask: it subscribes to nothing.
	tw.c.surf.mu.Lock()
	before := len(tw.c.surf.subs)
	tw.c.surf.mu.Unlock()
	openA2UI(t, tw, "")
	time.Sleep(200 * time.Millisecond)
	tw.c.surf.mu.Lock()
	after := len(tw.c.surf.subs)
	tw.c.surf.mu.Unlock()
	if after != before {
		t.Fatalf("subscribers %d, then %d", before, after)
	}
}

// A feed that falls behind the hub (more changes than its buffer, unread)
// starts again from a snapshot.
func TestSurfaceFeedFallsBehind(t *testing.T) {
	tw := newTestWeb(t)
	textSurface(t, tw.c, "busy", "Un")
	feed, first := tw.c.feedSurfaces()
	defer feed.close()
	if !first.Reset || len(first.Messages) != 1 {
		t.Fatalf("snapshot: %+v", first)
	}
	tw.c.surf.mu.Lock()
	for i := range surfaceSubBuffer + 10 {
		raw, _ := json.Marshal(i)
		tw.c.sendSurface(a2ui.Message{Version: a2ui.Version, UpdateDataModel: &a2ui.UpdateDataModel{SurfaceID: "busy", Path: "/n", Value: raw}})
	}
	tw.c.surf.mu.Unlock()
	ch, ok := <-feed.changes
	ev := feed.next(ch, ok)
	if !ev.Reset || kinds(ev.Messages) != "create busy" || ev.Seq != first.Seq+surfaceSubBuffer+10 {
		t.Fatalf("after falling behind: reset %v, seq %d, %s", ev.Reset, ev.Seq, kinds(ev.Messages))
	}
	// The new subscription follows.
	textSurface(t, tw.c, "busy", "Deux")
	ch, ok = <-feed.changes
	if ev := feed.next(ch, ok); ev.Reset || ev.Seq <= first.Seq+surfaceSubBuffer+10 || kinds(ev.Messages) != "components busy, data busy" {
		t.Fatalf("after: %+v", ev)
	}
}

func postJSON(tw *testWeb, path, body, csrf string) (int, string) {
	tw.t.Helper()
	h := map[string]string{"Content-Type": "application/json"}
	if csrf != "" {
		h["X-CSRF-Token"] = csrf
	}
	req, _ := http.NewRequest("POST", tw.srv.URL+path, strings.NewReader(body))
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := tw.hc.Do(req)
	if err != nil {
		tw.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	bufio.NewReader(resp.Body).WriteTo(&b)
	return resp.StatusCode, b.String()
}

func actionBody(name, surface string) string {
	return `{"version":"1.0","action":{"name":"` + name + `","surfaceId":"` + surface +
		`","sourceComponentId":"root","timestamp":"2026-11-12T08:00:00Z","context":{}}}`
}

// POST /api/espace/action takes an A2UI action of the session's, checked:
// "open" reads the surface's sources again; a name outside the grammar is
// refused (surface_actions_test.go for each kind). An action done is
// recorded on the surface.
func TestSurfaceActionEndpoint(t *testing.T) {
	c, l := surfaceCore(t)
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	s, errs, err := c.surfaceWrite("skill:releves", surfaceIn{Title: "Comptes", Sources: balances, Components: accountsSurface(),
		Lifetime: surfaceLifetime{Until: time.Now().Add(time.Hour)}})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}

	for _, tc := range []struct {
		name, body, csrf string
		want             int
	}{
		{"no CSRF", actionBody("open", s.ID), "", http.StatusForbidden},
		{"wrong CSRF", actionBody("open", s.ID), "nope", http.StatusForbidden},
		{"not JSON", `{`, csrf, http.StatusBadRequest},
		{"not an action", `{"version":"1.0","action":{"name":"open"}}`, csrf, http.StatusBadRequest},
		{"an error", `{"version":"1.0","error":{"code":"X","surfaceId":"` + s.ID + `","message":"m"}}`, csrf, http.StatusBadRequest},
		{"other version", strings.Replace(actionBody("open", s.ID), `"1.0"`, `"0.9"`, 1), csrf, http.StatusBadRequest},
		{"no surface", actionBody("open", "s-none"), csrf, http.StatusNotFound},
		{"outside the grammar", actionBody("summarize", s.ID), csrf, http.StatusBadRequest},
		{"local", actionBody("expand", s.ID), csrf, http.StatusNoContent},
	} {
		if code, body := postJSON(tw, "/api/espace/action", tc.body, tc.csrf); code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, code, body, tc.want)
		}
	}
	if got, _ := c.surface(s.ID); got.Look.Action == nil || got.Look.Action.Name != "expand" || got.Look.Action.Component != "root" {
		t.Fatalf("not recorded: %+v", got.Look.Action)
	}

	// Opened, its sources read a while ago: read again, the change sent.
	c.store.db.Exec(`UPDATE surfaces SET look = '{}' WHERE id = ?`, s.ID)
	_, ch, cancel := c.surfaceSubscribe()
	defer cancel()
	l.set(func(l *fakeLedger) { l.accounts = append(l.accounts, ledgerAccount{Name: "Épargne", Balance: 80}) })
	if code, body := postJSON(tw, "/api/espace/action", actionBody("open", s.ID), csrf); code != http.StatusNoContent {
		t.Fatalf("open: %d %s", code, body)
	}
	if u := received(t, ch, 1)[0].Message.UpdateDataModel; u == nil || u.SurfaceID != s.ID || !strings.Contains(string(u.Value), "Épargne") {
		t.Fatalf("update %+v", u)
	}
	waitFor(t, func() bool { _, busy := c.surf.reading.Load(s.ID); return !busy })
	if got, _ := c.surface(s.ID); got.Look.Action == nil || got.Look.Action.Name != "open" {
		t.Fatalf("open not recorded: %+v", got.Look.Action)
	}

	// Without a session: 401, not a page.
	jar, _ := cookiejar.New(nil)
	tw.hc.Jar = jar
	for _, path := range []string{"/api/espace/action", "/api/espace/error", "/events?a2ui=1"} {
		method := "POST"
		if strings.HasPrefix(path, "/events") {
			method = "GET"
		}
		if code, _, _ := tw.do(method, path, nil, nil); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", method, path, code)
		}
	}
}

// POST /api/espace/error takes an A2UI error about a surface, checked, and
// keeps it on the surface for its author; nothing goes to the page.
func TestSurfaceErrorEndpoint(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	textSurface(t, tw.c, "broken", "Un")
	errBody := func(sid string) string {
		return `{"version":"1.0","error":{"code":"VALIDATION_FAILED","surfaceId":"` + sid + `","path":"/components/0","message":"no such component"}}`
	}
	for _, tc := range []struct {
		name, body, csrf string
		want             int
	}{
		{"no CSRF", errBody("broken"), "", http.StatusForbidden},
		{"not JSON", `nope`, csrf, http.StatusBadRequest},
		{"not an error", `{"version":"1.0","error":{"surfaceId":"broken"}}`, csrf, http.StatusBadRequest},
		{"an action", actionBody("open", "broken"), csrf, http.StatusBadRequest},
		{"no surface named", `{"version":"1.0","error":{"code":"X","functionCallId":"f1","message":"m"}}`, csrf, http.StatusBadRequest},
		{"no surface", errBody("s-none"), csrf, http.StatusNotFound},
	} {
		if code, body := postJSON(tw, "/api/espace/error", tc.body, tc.csrf); code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, code, body, tc.want)
		}
	}
	_, ch, cancel := tw.c.surfaceSubscribe()
	defer cancel()
	if code, body := postJSON(tw, "/api/espace/error", errBody("broken"), csrf); code != http.StatusNoContent {
		t.Fatalf("error: %d %s", code, body)
	}
	got, _ := tw.c.surface("broken")
	if e := got.Look.LastError; e == nil || e.Code != "VALIDATION_FAILED" || e.Path != "/components/0" || e.Message != "no such component" {
		t.Fatalf("not kept: %+v", e)
	}
	select {
	case m := <-ch:
		t.Fatalf("an error sent something to the page: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
	// Written again, the surface starts clean.
	textSurface(t, tw.c, "broken", "Deux")
	if got, _ := tw.c.surface("broken"); got.Look.LastError != nil {
		t.Fatalf("kept after a rewrite: %+v", got.Look.LastError)
	}
}

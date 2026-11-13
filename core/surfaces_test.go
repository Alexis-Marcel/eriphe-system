package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeLedger is a server nobody wrote anything for: its name, its tools
// and its resource appear nowhere in eriphe-core. balances reads (with an
// outputSchema), move_funds acts, ledger://latest is a resource it tells
// the updates of.
type fakeLedger struct {
	mu       sync.Mutex
	accounts []ledgerAccount
	latest   string
	calls    int
	hold     chan struct{} // the next read of the resource waits on it
	gate     chan struct{} // while set, every read of the resource waits on it
	inflight int           // reads of the resource waiting on the gate
	most     int           // the most of them at once
	srv      *mcp.Server
}

type ledgerAccount struct {
	Name    string  `json:"name"`
	Balance float64 `json:"balance"`
	Photo   string  `json:"photo,omitempty"`
}

type ledgerOut struct {
	Accounts []ledgerAccount `json:"accounts"`
	Total    float64         `json:"total"`
}

func newFakeLedger() *fakeLedger {
	l := &fakeLedger{accounts: []ledgerAccount{{Name: "Courant", Balance: 120}}, latest: "first"}
	s := mcp.NewServer(&mcp.Implementation{Name: "ledger", Version: "1"}, &mcp.ServerOptions{
		SubscribeHandler:   func(context.Context, *mcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	mcp.AddTool(s, &mcp.Tool{Name: "balances", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closedWorld}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ledgerOut, error) {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.calls++
			out := ledgerOut{Accounts: append([]ledgerAccount{}, l.accounts...)}
			for _, a := range l.accounts {
				out.Total += a.Balance
			}
			return nil, out, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "move_funds"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"ok": true}, nil
		})
	s.AddResource(&mcp.Resource{URI: "ledger://latest", Name: "latest", MIMEType: "application/json"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			l.mu.Lock()
			l.calls++
			raw, _ := json.Marshal(map[string]any{"entry": l.latest})
			hold, gate := l.hold, l.gate
			l.hold = nil
			if gate != nil {
				l.inflight++
				l.most = max(l.most, l.inflight)
			}
			l.mu.Unlock()
			if hold != nil {
				<-hold // the test holds this read
			}
			if gate != nil {
				<-gate
				l.set(func(l *fakeLedger) { l.inflight-- })
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "ledger://latest", Text: string(raw)}}}, nil
		})
	l.srv = s
	return l
}

func (l *fakeLedger) set(f func(l *fakeLedger)) {
	l.mu.Lock()
	f(l)
	l.mu.Unlock()
}

func (l *fakeLedger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// surfaceCore is a core whose catalog links the fake ledger, lent to the
// brain, its reads free.
func surfaceCore(t *testing.T) (*core, *fakeLedger) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	cfg := &Config{User: "Sam", DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: secret}},
		Agents:    map[string]Agent{"small": {Runtime: "claude-code", Provider: "p"}},
		Servers: map[string]Server{"ledger": {URL: "https://ledger.example.org/mcp", Auth: &ServerAuth{None: true},
			Use: "accounts", Access: accessReads}}}
	if err := cfg.checkServers(); err != nil {
		t.Fatal(err)
	}
	c := newCore(cfg, store)
	l := newFakeLedger()
	c.pool.dial = func(ctx context.Context, u *upstream) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := l.srv.Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.pool.run(ctx)
	waitStatus(t, c.pool.get("ledger"), statusReady)
	waitFor(t, func() bool { return len(c.pool.get("ledger").resourceURIs()) > 0 })
	setBrain(c, &BrainConfig{Agent: "small", Tools: []string{"ledger"}})
	return c, l
}

// tickSurfacesWait ticks the surfaces and waits for the refreshes it
// handed to the worker.
func tickSurfacesWait(c *core, at time.Time) {
	c.tickSurfaces(context.Background(), at)
	c.surf.refreshing.Wait()
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// accountsSurface binds a total and a list of accounts to the source /ledger.
func accountsSurface(extra ...string) json.RawMessage {
	comps := []string{
		`{"id":"root","component":"Column","children":["total","list"]}`,
		`{"id":"total","component":"Metric","label":"Total","value":{"@path":"/ledger/total"}}`,
		`{"id":"list","component":"List","children":{"path":"/ledger/accounts","componentId":"row"}}`,
		`{"id":"row","component":"Metric","label":{"@path":"name"},"value":{"@path":"balance"}}`,
	}
	return json.RawMessage("[" + strings.Join(append(comps, extra...), ",") + "]")
}

var balances = map[string]surfaceSource{"/ledger": {Tool: "ledger/balances"}}

// received collects what a subscriber gets.
func received(t *testing.T, ch <-chan surfaceChange, n int) []surfaceChange {
	t.Helper()
	var out []surfaceChange
	timeout := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatal("closed")
			}
			out = append(out, m)
		case <-timeout:
			t.Fatalf("got %d changes, want %d", len(out), n)
		}
	}
	return out
}

func validStream(t *testing.T, changes ...surfaceChange) {
	t.Helper()
	var msgs []a2ui.Message
	for _, ch := range changes {
		msgs = append(msgs, ch.Message)
	}
	raw, _ := json.Marshal(msgs)
	if errs := a2ui.Validate(raw); len(errs) > 0 {
		t.Fatalf("%s: %v", raw, errs)
	}
}

// A server linked today, that no code was written for, serves a live
// surface: its tool's result is put in the data as it came, its new values
// pushed by path, and no model is called.
func TestSurfaceFromAnyTool(t *testing.T) {
	c, l := surfaceCore(t)
	snap, ch, cancel := c.surfaceSubscribe()
	defer cancel()
	if len(snap) != 0 {
		t.Fatalf("snapshot %v", snap)
	}
	s, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "Comptes", Components: accountsSurface(), Sources: balances, Why: "Sam asked"})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	var data map[string]any
	json.Unmarshal(s.Data, &data)
	if v, _ := getPointer(data, "/ledger/total"); v != 120.0 {
		t.Fatalf("data %s", s.Data)
	}
	if s.Tainted != "ledger/balances" || s.Refresh.Every != surfaceEveryDef || s.Lifetime.Until.IsZero() || s.Author != "brain" {
		t.Fatalf("surface %+v", s)
	}
	create := received(t, ch, 1)[0]
	if create.Message.CreateSurface == nil || create.Message.CreateSurface.SurfaceID != s.ID ||
		!strings.Contains(string(create.Message.CreateSurface.Metadata), `"eriphe":{"title":"Comptes","author":"brain"`) {
		t.Fatalf("create %+v", create.Message.CreateSurface)
	}

	// Its interval comes: a new account goes out as the source's path only.
	l.set(func(l *fakeLedger) { l.accounts = append(l.accounts, ledgerAccount{Name: "Livret", Balance: 30}) })
	tickSurfacesWait(c, time.Now().Add(time.Duration(surfaceEveryDef)*time.Minute))
	upd := received(t, ch, 1)[0]
	if u := upd.Message.UpdateDataModel; u == nil || u.Path != "/ledger" || !strings.Contains(string(u.Value), `"total":150`) || upd.Seq != create.Seq+1 {
		t.Fatalf("update %d %+v", upd.Seq, upd.Message)
	}
	validStream(t, create, upd)
	// Read again with nothing new: nothing goes out; not its time: no call.
	c.refreshSurface(context.Background(), s.ID, nil, time.Now())
	calls := l.count()
	tickSurfacesWait(c, time.Now())
	if l.count() != calls {
		t.Fatal("read before its time")
	}
	select {
	case m := <-ch:
		t.Fatalf("sent %+v", m.Message)
	default:
	}
	// A late subscriber starts from the surface as it is.
	snap2, _, cancel2 := c.surfaceSubscribe()
	defer cancel2()
	if len(snap2) != 1 || snap2[0].Seq != upd.Seq || !strings.Contains(string(snap2[0].Message.CreateSurface.DataModel), `"Livret"`) {
		t.Fatalf("snapshot %+v", snap2)
	}

	// Written again with one more component: its components and data;
	// with one less, or another title: deleted and created again (A2UI
	// removes no component). Each step a valid stream.
	extra := `{"id":"note","component":"Text","text":"Sam"}`
	more := json.RawMessage(strings.Replace(string(accountsSurface(extra)), `["total","list"]`, `["total","list","note"]`, 1))
	if _, errs, err := c.surfaceWrite("brain", surfaceIn{ID: s.ID, Title: "Comptes", Components: more, Sources: balances, Why: "Sam asked"}); err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	rw := received(t, ch, 2)
	if rw[0].Message.UpdateComponents == nil || rw[1].Message.UpdateDataModel == nil || rw[1].Seq != rw[0].Seq+1 {
		t.Fatalf("rewrite %+v", rw)
	}
	validStream(t, create, upd, rw[0], rw[1])
	if _, _, err := c.surfaceWrite("brain", surfaceIn{ID: s.ID, Title: "Mes comptes", Components: accountsSurface(), Sources: balances}); err != nil {
		t.Fatal(err)
	}
	again := received(t, ch, 2)
	if again[0].Message.DeleteSurface == nil || again[1].Message.CreateSurface == nil {
		t.Fatalf("rewrite %+v", again)
	}
	validStream(t, again[1])
}

// A source is a free read of the brain's, or a resource the server lists:
// a tool that acts, a server or resource not there, a bad pointer are
// refused; a read the grid no longer frees waits, nothing called.
func TestSurfaceSourceGuards(t *testing.T) {
	c, l := surfaceCore(t)
	for name, srcs := range map[string]map[string]surfaceSource{
		"a tool that acts":   {"/x": {Tool: "ledger/move_funds"}},
		"no such server":     {"/x": {Tool: "nowhere/balances"}},
		"no such resource":   {"/x": {Resource: "ledger", URI: "ledger://nope"}},
		"a resource's args":  {"/x": {Resource: "ledger", URI: "ledger://latest", Args: map[string]any{"a": 1}}},
		"tool and resource":  {"/x": {Tool: "ledger/balances", Resource: "ledger", URI: "ledger://latest"}},
		"no server in tool":  {"/x": {Tool: "balances"}},
		"the root":           {"/": {Tool: "ledger/balances"}},
		"not a pointer":      {"x": {Tool: "ledger/balances"}},
		"a bad escape":       {"/a~2": {Tool: "ledger/balances"}},
		"one inside another": {"/a": {Tool: "ledger/balances"}, "/a/b": {Resource: "ledger", URI: "ledger://latest"}},
	} {
		comps := json.RawMessage(`[{"id":"root","component":"Text","text":"x"}]`)
		if _, _, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: comps, Sources: srcs}); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	for _, bad := range []surfaceIn{
		{Title: "", Components: accountsSurface(), Sources: balances},
		{Title: "x", Components: accountsSurface(), Sources: balances, Refresh: surfaceRefresh{Every: 2000}},
		{Title: "x", Components: accountsSurface(), Sources: balances, Lifetime: surfaceLifetime{Kept: true, While: "task:t-1"}},
		{Title: "x", Components: accountsSurface(), Sources: balances, Lifetime: surfaceLifetime{While: "task:t-none"}},
		{Title: "x", Components: accountsSurface(), Sources: balances, Data: json.RawMessage(`[1]`)},
	} {
		if _, _, err := c.surfaceWrite("brain", bad); err == nil {
			t.Errorf("%+v: written", bad)
		}
	}
	if _, _, err := c.surfaceWrite("someone", surfaceIn{Title: "x", Components: accountsSurface(), Sources: balances}); err == nil {
		t.Error("an unknown author wrote")
	}

	s, _, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: accountsSurface(), Sources: balances})
	if err != nil {
		t.Fatal(err)
	}
	u := c.pool.get("ledger") // the user sets the tool to ask first: no free read
	u.mu.Lock()
	u.srv.Access = accessAsk
	u.mu.Unlock()
	calls := l.count()
	tickSurfacesWait(c, time.Now().Add(time.Hour))
	s, _ = c.surface(s.ID)
	if l.count() != calls || s.Look.Sources["/ledger"].Waiting == "" {
		t.Fatalf("calls %d → %d, look %+v", calls, l.count(), s.Look)
	}
}

// The paths the components bind must exist: in what the source gave, or
// in its tool's outputSchema, or in the composed data.
func TestSurfaceBindingCheck(t *testing.T) {
	c, _ := surfaceCore(t)
	_, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Sources: balances, Components: json.RawMessage(`[
		{"id":"root","component":"Column","children":["a","b","list","c"]},
		{"id":"a","component":"Metric","label":"x","value":{"@path":"/ledger/sum"}},
		{"id":"b","component":"Metric","label":"x","value":{"@path":"/ledger/total"}},
		{"id":"list","component":"List","children":{"path":"/ledger/accounts","componentId":"row"}},
		{"id":"row","component":"Text","text":{"@path":"iban"}},
		{"id":"c","component":"Text","text":{"@path":"/note"}}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 3 {
		t.Fatalf("errors %v", errs)
	}
	if errs[0].Path != "/components/1/value/@path" || !strings.Contains(errs[0].Message, "/ledger/sum") ||
		!strings.Contains(errs[0].Message, "accounts, total") || errs[0].Code != a2ui.CodeValidationFailed {
		t.Errorf("first %+v", errs[0])
	}
	if errs[1].Path != "/components/4/text/@path" || !strings.Contains(errs[1].Message, "/ledger/accounts/*/iban") {
		t.Errorf("second %+v", errs[1])
	}
	if !strings.Contains(errs[2].Message, "/note") || !strings.Contains(errs[2].Message, "data") {
		t.Errorf("third %+v", errs[2])
	}
	// photo is never in this result (omitempty), but the outputSchema has
	// it; /note is in the composed data.
	_, errs, err = c.surfaceWrite("brain", surfaceIn{Title: "x", Sources: balances, Data: json.RawMessage(`{"note":"Sam"}`),
		Components: json.RawMessage(`[
		{"id":"root","component":"Column","children":["list","c"]},
		{"id":"list","component":"List","children":{"path":"/ledger/accounts","componentId":"pic"}},
		{"id":"pic","component":"Image","url":{"@path":"photo"}},
		{"id":"c","component":"Text","text":{"@path":"/note"}}]`)})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	// Bad A2UI comes back as A2UI's errors.
	_, errs, _ = c.surfaceWrite("brain", surfaceIn{Title: "x", Components: json.RawMessage(`[{"id":"root","component":"Nope"}]`)})
	if len(errs) == 0 {
		t.Fatal("a wrong component was written")
	}
}

// Content from a server not trusted taints the surface; trusted, only the
// author's own turn does.
func TestSurfaceTaint(t *testing.T) {
	c, _ := surfaceCore(t)
	c.setServerTrust("ledger", true, "Sam")
	s, _, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: accountsSurface(), Sources: balances})
	if err != nil || s.Tainted != "" {
		t.Fatalf("%v, tainted %q", err, s.Tainted)
	}
	// No longer trusted: its next read taints it, and the page is told
	// (created again, its metadata tainted).
	_, ch, cancel := c.surfaceSubscribe()
	defer cancel()
	c.setServerTrust("ledger", false, "Sam")
	c.refreshSurface(context.Background(), s.ID, nil, time.Now())
	got := received(t, ch, 2)
	if got[0].Message.DeleteSurface == nil || got[1].Message.CreateSurface == nil ||
		!strings.Contains(string(got[1].Message.CreateSurface.Metadata), `"tainted":true`) {
		t.Fatalf("taint not sent: %+v", got)
	}
	c.setServerTrust("ledger", true, "Sam")
	c.refreshSurface(context.Background(), s.ID, nil, time.Now())
	received(t, ch, 2)
	s, _, err = c.surfaceWrite("brain", surfaceIn{ID: s.ID, Title: "x", Components: accountsSurface(), Sources: balances,
		Tainted: []string{"web.fetch"}})
	if err != nil || s.Tainted != "web.fetch" {
		t.Fatalf("%v, tainted %q", err, s.Tainted)
	}
	c.setServerTrust("ledger", false, "Sam")
	c.refreshSurface(context.Background(), s.ID, nil, time.Now())
	s, _ = c.surface(s.ID)
	if s.Tainted != "ledger/balances, web.fetch" {
		t.Fatalf("tainted %q", s.Tainted)
	}
}

// Refreshed by code: a resource's update told by its server, and the
// surface opened.
func TestSurfaceRefreshTriggers(t *testing.T) {
	c, l := surfaceCore(t)
	s, errs, err := c.surfaceWrite("skill:releves", surfaceIn{Title: "Dernière écriture", Lifetime: surfaceLifetime{Until: time.Now().Add(time.Hour)},
		Sources:    map[string]surfaceSource{"/latest": {Resource: "ledger", URI: "ledger://latest"}},
		Components: json.RawMessage(`[{"id":"root","component":"Text","text":{"@path":"/latest/entry"}}]`)})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	waitFor(t, func() bool { u := c.pool.get("ledger"); u.mu.Lock(); defer u.mu.Unlock(); return len(u.followed) == 1 })
	_, ch, cancel := c.surfaceSubscribe()
	defer cancel()
	l.set(func(l *fakeLedger) { l.latest = "second" })
	if err := l.srv.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{URI: "ledger://latest"}); err != nil {
		t.Fatal(err)
	}
	if u := received(t, ch, 1)[0].Message.UpdateDataModel; u == nil || u.Path != "/latest" || string(u.Value) != `{"entry":"second"}` {
		t.Fatalf("update %+v", u)
	}
	// Opened just after its read: nothing read again; later, read again.
	waitFor(t, func() bool { _, busy := c.surf.reading.Load(s.ID); return !busy })
	calls := l.count()
	c.surfaceOpened(context.Background(), s.ID)
	if l.count() != calls {
		t.Fatal("read again at once")
	}
	c.store.db.Exec(`UPDATE surfaces SET look = '{}' WHERE id = ?`, s.ID)
	l.set(func(l *fakeLedger) { l.latest = "third" })
	if err := c.surfaceOpened(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	if u := received(t, ch, 1)[0].Message.UpdateDataModel; u == nil || string(u.Value) != `{"entry":"third"}` {
		t.Fatalf("update %+v", u)
	}
	if c.surfaceOpened(context.Background(), "s-none") == nil {
		t.Fatal("opened a surface that is not there")
	}

	// An update told while a read is under way is not lost: one more read
	// follows it.
	waitFor(t, func() bool { _, busy := c.surf.reading.Load(s.ID); return !busy })
	hold := make(chan struct{})
	l.set(func(l *fakeLedger) { l.latest, l.hold = "fourth", hold })
	notify := func() {
		if err := l.srv.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{URI: "ledger://latest"}); err != nil {
			t.Fatal(err)
		}
	}
	notify()
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.hold == nil }) // the first read is held
	l.set(func(l *fakeLedger) { l.latest = "fifth" })
	notify()
	waitFor(t, func() bool { v, ok := c.surf.reading.Load(s.ID); return ok && v.(*atomic.Bool).Load() })
	close(hold)
	for i, want := range []string{"fourth", "fifth"} {
		if u := received(t, ch, 1)[0].Message.UpdateDataModel; u == nil || string(u.Value) != `{"entry":"`+want+`"}` {
			t.Fatalf("update %d %+v", i, u)
		}
	}
}

// A read that lands after its surface was written again with another
// source at its pointer is dropped.
func TestSurfaceStaleRead(t *testing.T) {
	c, l := surfaceCore(t)
	comps := json.RawMessage(`[{"id":"root","component":"Text","text":{"@path":"/x/entry"}}]`)
	s, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: comps,
		Sources: map[string]surfaceSource{"/x": {Resource: "ledger", URI: "ledger://latest"}}})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	hold := make(chan struct{})
	l.set(func(l *fakeLedger) { l.latest, l.hold = "old source", hold })
	done := make(chan struct{})
	go func() { c.refreshSurface(context.Background(), s.ID, nil, time.Now()); close(done) }()
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.hold == nil })
	other := json.RawMessage(`[{"id":"root","component":"Metric","label":"x","value":{"@path":"/x/total"}}]`)
	if _, errs, err := c.surfaceWrite("brain", surfaceIn{ID: s.ID, Title: "x", Components: other, Sources: map[string]surfaceSource{"/x": {Tool: "ledger/balances"}}}); err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	close(hold)
	<-done
	s, _ = c.surface(s.ID)
	if strings.Contains(string(s.Data), "old source") || !strings.Contains(string(s.Data), `"total"`) {
		t.Fatalf("data %s", s.Data)
	}
}

// Code removes a surface when its lifetime ends; only its author, the user
// or code removes it, and only its author writes it again.
func TestSurfaceLifetime(t *testing.T) {
	c, _ := surfaceCore(t)
	comps := json.RawMessage(`[{"id":"root","component":"Text","text":"En préparation"}]`)
	c.store.insertTask(&TaskRow{ID: "t-1", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "x"})
	until, _, _ := c.surfaceWrite("brain", surfaceIn{Title: "a", Components: comps, Lifetime: surfaceLifetime{Until: time.Now().Add(time.Hour)}})
	while, _, err := c.surfaceWrite("brain", surfaceIn{Title: "b", Components: comps, Lifetime: surfaceLifetime{While: "task:t-1"}})
	if err != nil {
		t.Fatal(err)
	}
	kept, _, _ := c.surfaceWrite("skill:matin", surfaceIn{Title: "c", Components: comps, Lifetime: surfaceLifetime{Kept: true}})
	mine, _, _ := c.surfaceWrite("brain", surfaceIn{Title: "d", Components: comps, Lifetime: surfaceLifetime{Kept: true}})
	if _, _, err := c.surfaceWrite("task:t-1", surfaceIn{ID: mine.ID, Title: "e", Components: comps}); err == nil {
		t.Fatal("another author wrote the brain's surface")
	}
	if err := c.surfaceRemove(mine.ID, "task:t-1"); err == nil {
		t.Fatal("another author removed the brain's surface")
	}
	_, ch, cancel := c.surfaceSubscribe()
	defer cancel()

	tickSurfacesWait(c, time.Now())
	all, _ := c.surfaces()
	if len(all) != 3 { // the skill is not there: its kept surface goes
		t.Fatalf("surfaces %v", all)
	}
	if d := received(t, ch, 1)[0].Message.DeleteSurface; d == nil || d.SurfaceID != kept.ID {
		t.Fatalf("delete %+v", d)
	}
	c.store.updateTask("t-1", map[string]any{"state": stateStopped})
	tickSurfacesWait(c, time.Now().Add(2*time.Hour))
	all, _ = c.surfaces()
	if len(all) != 1 || all[0].ID != mine.ID {
		t.Fatalf("surfaces %v", all)
	}
	for _, d := range received(t, ch, 2) {
		if id := d.Message.DeleteSurface.SurfaceID; id != until.ID && id != while.ID {
			t.Fatalf("deleted %s", id)
		}
	}
	if err := c.surfaceRemove(mine.ID, surfaceByUser); err != nil {
		t.Fatal(err)
	}
}

// A Link's address is never built (A2UI #4045): a literal http(s) address,
// or a field that is one as a whole, in a source or the author's data; a
// tainted author may still show one (it opens on the user's click only,
// shown in clear). What a source gives later that is not an address goes
// to the page as "": no link.
func TestSurfaceLinks(t *testing.T) {
	c, l := surfaceCore(t)
	l.set(func(l *fakeLedger) {
		l.accounts = []ledgerAccount{{Name: "a", Photo: "https://example.org/a"}, {Name: "b", Photo: "https://example.org/b?tab=1"}}
	})
	comps := json.RawMessage(`[
		{"id":"root","component":"Column","children":["list","home","more"]},
		{"id":"list","component":"List","children":{"path":"/ledger/accounts","componentId":"open"}},
		{"id":"open","component":"Link","label":{"@path":"name"},"url":{"@path":"photo"}},
		{"id":"home","component":"Link","url":"https://example.org/projets"},
		{"id":"more","component":"Link","label":"Plus","url":{"@path":"/more"}}]`)
	s, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: comps, Sources: balances,
		Data: json.RawMessage(`{"more":"https://example.org/plus"}`), Tainted: []string{"mail.read"}})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	view := c.surfaceView(s)
	if u, _ := getPointer(view.data, "/ledger/accounts/1/photo"); u != "https://example.org/b?tab=1" {
		t.Fatalf("a source's address: %v", u)
	}
	raw, _ := json.Marshal(view.components)
	if !strings.Contains(string(raw), `"url":"https://example.org/projets"`) || strings.Contains(string(raw), "/espace/media") {
		t.Fatalf("the author's address is sent as it is: %s", raw)
	}

	refused := func(name, comps, data, path, words string) {
		t.Helper()
		_, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: json.RawMessage(comps), Sources: balances, Data: json.RawMessage(data)})
		if err != nil || len(errs) == 0 || errs[0].Path != path || !strings.Contains(errs[0].Message, words) {
			t.Errorf("%s: %v %v", name, err, errs)
		}
	}
	refused("a script", `[{"id":"root","component":"Link","url":"javascript:alert(1)"}]`, ``, "/components/0/url", "http(s)")
	refused("a password", `[{"id":"root","component":"Link","url":"https://sam:secret@example.org/"}]`, ``, "/components/0/url", "no user or password")
	refused("built by formatString", `[{"id":"root","component":"Link","url":{"@call":"formatString","args":{"value":"https://example.org/?q=${/ledger/total}"}}}]`,
		``, "/components/0/url", "")
	refused("a field that is not an address", `[{"id":"root","component":"List","children":{"path":"/ledger/accounts","componentId":"open"}},
		{"id":"open","component":"Link","url":{"@path":"name"}}]`, ``, "/components/1/url/@path", "already an http(s) address")
	refused("the author's data, not an address", `[{"id":"root","component":"Link","url":{"@path":"/more"}}]`,
		`{"more":"ftp://example.org/a"}`, "/components/0/url/@path", "already an http(s) address")

	// A source gives something else later: no link, the rest as it was.
	s.Data, _ = json.Marshal(map[string]any{"ledger": map[string]any{"accounts": []any{
		map[string]any{"name": "a", "photo": "javascript:alert(1)"}, map[string]any{"name": "b", "photo": "https://example.org/b"}}},
		"more": "https://sam:secret@example.org/"})
	view = c.surfaceView(s)
	for ptr, want := range map[string]string{"/ledger/accounts/0/photo": "", "/ledger/accounts/1/photo": "https://example.org/b", "/more": "", "/ledger/accounts/0/name": "a"} {
		if got, _ := getPointer(view.data, ptr); got != want {
			t.Errorf("%s: %v, want %q", ptr, got, want)
		}
	}
}

// Media addresses go through eriphe-core: from a source, rewritten to the
// endpoint; written by a tainted author, dropped; anything but http(s),
// dropped. The endpoint serves only what it signed, an image or a video,
// within its size, and never a private address by default.
func TestSurfaceMedia(t *testing.T) {
	c, l := surfaceCore(t)
	l.set(func(l *fakeLedger) {
		l.accounts = []ledgerAccount{{Name: "a", Photo: "https://img.example.org/a.png"}, {Name: "b", Photo: "javascript:alert(1)"}}
	})
	comps := json.RawMessage(`[
		{"id":"root","component":"Column","children":["list","logo"]},
		{"id":"list","component":"List","children":{"path":"/ledger/accounts","componentId":"pic"}},
		{"id":"pic","component":"Image","url":{"@path":"photo"}},
		{"id":"logo","component":"Image","url":"https://img.example.org/logo.png?d=secret"}]`)
	s, errs, err := c.surfaceWrite("brain", surfaceIn{Title: "x", Components: comps, Sources: balances, Tainted: []string{"mail.read"}})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	view := c.surfaceView(s)
	a, _ := getPointer(view.data, "/ledger/accounts/0/photo")
	b, _ := getPointer(view.data, "/ledger/accounts/1/photo")
	want := c.mediaOrDrop("https://img.example.org/a.png", true)
	if a != want || !strings.HasPrefix(want, "/espace/media?") || b != "" {
		t.Fatalf("photos %v %v", a, b)
	}
	raw, _ := json.Marshal(view.components)
	if strings.Contains(string(raw), "example.org") {
		t.Fatalf("a tainted author's address kept: %s", raw)
	}
	// Untainted, the author's address goes through the endpoint too.
	s, _, _ = c.surfaceWrite("brain", surfaceIn{ID: s.ID, Title: "x", Components: comps, Sources: balances})
	raw, _ = json.Marshal(c.surfaceView(s).components)
	if !strings.Contains(string(raw), "/espace/media?") {
		t.Fatalf("components %s", raw)
	}

	// The endpoint.
	img := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.png":
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Error("credentials sent")
			}
			rw.Header().Set("Content-Type", "image/png")
			rw.Write([]byte("\x89PNG"))
		case "/b.svg":
			rw.Header().Set("Content-Type", "image/svg+xml")
			rw.Write([]byte("<svg/>"))
		case "/big.png":
			rw.Header().Set("Content-Type", "image/png")
			rw.Write(make([]byte, mediaMax+1))
		default:
			rw.Header().Set("Content-Type", "text/html")
			rw.Write([]byte("<html>"))
		}
	}))
	defer img.Close()
	// The default client refuses this server: a loopback address.
	req := httptest.NewRequest("GET", "/", nil)
	if _, _, err := fetchMedia(req, img.URL+"/a.png"); err == nil {
		t.Fatal("the default client fetched a loopback address")
	}
	old := mediaClient
	mediaClient = img.Client()
	t.Cleanup(func() { mediaClient = old })

	tw := newTestWebFor(t, c)
	tw.setup()
	code, body, h := tw.do("GET", c.mediaOrDrop(img.URL+"/a.png", true), nil, nil)
	if code != 200 || body != "\x89PNG" || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Cache-Control"), "max-age") {
		t.Fatalf("%d %q %v", code, body, h)
	}
	for path, want := range map[string]int{
		mediaPath + "?" + url.Values{"u": {img.URL + "/a.png"}, "s": {"forged"}}.Encode(): 403,
		c.mediaOrDrop(img.URL+"/b.svg", true):                                             502,
		c.mediaOrDrop(img.URL+"/big.png", true):                                           502,
		c.mediaOrDrop(img.URL+"/page", true):                                              502,
	} {
		if code, _, _ := tw.do("GET", path, nil, nil); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
}

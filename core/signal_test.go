package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sgNumber   = "+15555550100" // Eriphe's
	sgUserUUID = "0b1c2d3e-0000-4000-8000-000000000001"
	sgUserNum  = "+15555550101"
	sgStranger = "0b1c2d3e-0000-4000-8000-000000000002"
)

// fakeSignal is eriphe-signal (servers/signal), its setup tools as the
// channel calls them.
type fakeSignal struct {
	mu       sync.Mutex
	number   string // registered
	pending  string
	pin      string
	removed  bool
	sent     []map[string]any
	ts       int64
	seq      int64
	inbox    []map[string]any
	captchas int // registers refused for a captcha
}

func ok(v map[string]any) (*mcp.CallToolResult, map[string]any, error) { return nil, v, nil }

func (f *fakeSignal) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "eriphe-signal", Version: "test"}, nil)
	setup := mcp.Meta{"eriphe/setup": true}
	type args = map[string]any
	add := func(name string, h func(in args) (*mcp.CallToolResult, map[string]any, error)) {
		mcp.AddTool(s, &mcp.Tool{Name: name, Meta: setup}, func(_ context.Context, _ *mcp.CallToolRequest, in args) (*mcp.CallToolResult, map[string]any, error) {
			if name == "receive" {
				return h(in)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			return h(in)
		})
	}
	add("status", func(args) (*mcp.CallToolResult, map[string]any, error) {
		return ok(map[string]any{"number": f.number, "version": "0.14.10"})
	})
	add("register", func(in args) (*mcp.CallToolResult, map[string]any, error) {
		if in["captcha"] == nil {
			f.captchas++
			return ok(map[string]any{"ok": false, "error": "Captcha required", "captcha": true})
		}
		f.pending = in["number"].(string)
		return ok(map[string]any{"ok": true})
	})
	add("verify", func(in args) (*mcp.CallToolResult, map[string]any, error) {
		if in["code"] != "123-456" || in["number"] != f.pending {
			return ok(map[string]any{"ok": false, "error": "Verification failed"})
		}
		f.number = f.pending
		return ok(map[string]any{"ok": true})
	})
	add("set_pin", func(in args) (*mcp.CallToolResult, map[string]any, error) {
		f.pin = in["pin"].(string)
		return ok(map[string]any{"ok": true})
	})
	add("remove", func(args) (*mcp.CallToolResult, map[string]any, error) {
		f.number, f.removed = "", true
		return ok(map[string]any{"ok": true})
	})
	add("send", func(in args) (*mcp.CallToolResult, map[string]any, error) {
		f.sent = append(f.sent, in)
		f.ts++
		return ok(map[string]any{"timestamp": f.ts})
	})
	add("receive", func(in args) (*mcp.CallToolResult, map[string]any, error) {
		after, _ := in["after"].(float64)
		for end := time.Now().Add(50 * time.Millisecond); ; time.Sleep(2 * time.Millisecond) {
			f.mu.Lock()
			var out []map[string]any
			last := int64(after)
			for _, m := range f.inbox {
				if seq := m["seq"].(int64); seq > int64(after) {
					out = append(out, m)
					last = seq
				}
			}
			f.mu.Unlock()
			if len(out) > 0 || time.Now().After(end) {
				if out == nil {
					out = []map[string]any{}
				}
				return ok(map[string]any{"messages": out, "last": last})
			}
		}
	})
	return s
}

func (f *fakeSignal) receives(m map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	m["seq"] = f.seq
	f.inbox = append(f.inbox, m)
}

func (f *fakeSignal) says(uuid, number, text string, group bool) {
	f.receives(map[string]any{"from": uuid, "number": number, "name": "Sam", "text": text, "group": group, "timestamp": 1})
}

func (f *fakeSignal) reacts(uuid, number, emoji string, ts int64) {
	f.receives(map[string]any{"from": uuid, "number": number, "timestamp": 2,
		"reaction": map[string]any{"emoji": emoji, "target": ts}})
}

func (f *fakeSignal) sentNow() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent...)
}

func (f *fakeSignal) lastTS() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ts
}

// signalWeb is the workspace with the Signal channel, its service on the
// fake.
func signalWeb(t *testing.T) (*testWeb, *botChannel, *fakeSignal, string) {
	t.Helper()
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	f := &fakeSignal{ts: 1700000000000}
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	b := c.bot(sgChannel)
	b.svc.dial = func(context.Context, string) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := f.server().Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	t.Cleanup(b.svc.close)
	b.sleep = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
	b.base = func() string { return "https://eriphe.example.org" }
	t.Cleanup(b.stop)
	return tw, b, f, csrf
}

// Turning Signal on adds its service, on amd64, with a volume; the number
// is registered from the page, through a captcha, the code and a PIN; the
// account that sends the linking code in a private chat is the only one
// heard.
func TestSignalSetupAndTalk(t *testing.T) {
	tw, b, f, csrf := signalWeb(t)
	c := b.c
	if _, body, _ := tw.do("GET", "/settings/access/signal", nil, nil); !strings.Contains(body, `/channels/signal/add`) {
		t.Fatal("no Turn Signal on")
	}
	if code, _, _ := tw.post("/channels/signal/add", nil, csrf); code != 200 {
		t.Fatalf("add: %d", code)
	}
	// Its own service, not a tool: in no catalog, in no pool.
	svc, err := c.store.service("channel-signal")
	if err != nil || svc.Spec.Arch != "amd64" || !svc.Spec.Volume || !strings.HasPrefix(svc.Spec.Image, sgImage) {
		t.Fatalf("service %+v %v", svc, err)
	}
	for name := range c.conf().Servers {
		if strings.Contains(name, "signal") {
			t.Fatalf("a catalog server %s", name)
		}
	}
	if c.pool.get("channel-signal") != nil || c.pool.get("signal") != nil {
		t.Fatal("in the pool")
	}

	_, body, _ := tw.post("/channels/signal/register", url.Values{"number": {"+1 555 555 0100"}, "voice": {"1"}}, csrf)
	if !strings.Contains(body, "signalcaptchas.org") || f.captchas != 1 {
		t.Fatalf("no captcha asked: %s", body)
	}
	_, body, _ = tw.post("/channels/signal/register", url.Values{"number": {sgNumber}, "captcha": {"signalcaptcha://x"}}, csrf)
	if !strings.Contains(body, `name="code"`) || b.get("pending") != sgNumber {
		t.Fatalf("no code asked: %s", body)
	}
	_, body, _ = tw.post("/channels/signal/verify", url.Values{"code": {"999-999"}}, csrf)
	if !strings.Contains(body, "Verification failed") || b.linked() || b.get("bot") != "" {
		t.Fatalf("a wrong code: %s", body)
	}
	_, body, _ = tw.post("/channels/signal/verify", url.Values{"code": {"123-456"}, "lock": {"2468"}}, csrf)
	if b.get("bot") != sgNumber || f.pin != "2468" || !b.running() || !strings.Contains(body, "15555550100") ||
		!strings.Contains(body, "/channels/signal/code") {
		t.Fatalf("not registered: %q %q %s", b.get("bot"), f.pin, body)
	}

	code := b.newCode("fr")
	if p := b.page(); p.Open != "https://signal.me/#p/"+sgNumber {
		t.Fatalf("open %q", p.Open)
	}
	f.says(sgStranger, "+15555550102", "NOPE", false)
	f.says(sgUserUUID, sgUserNum, strings.ToLower(code), false)
	waitUntil(t, b.linked)
	if b.get("user") != sgUserUUID || b.get("chat") != sgUserNum || b.get("name") != "Sam" {
		t.Fatalf("linked to %q %q %q", b.get("user"), b.get("chat"), b.get("name"))
	}
	waitUntil(t, func() bool { return len(f.sentNow()) == 1 }) // "C'est fait"

	before := c.store.lastMessageSeq()
	f.says(sgUserUUID, sgUserNum, "dans un groupe", true)
	f.says(sgStranger, "+15555550102", "moi aussi", false)
	f.says(sgUserUUID, sgUserNum, "Quoi de neuf ?", false)
	waitUntil(t, func() bool { return c.store.lastMessageSeq() > before })
	msgs, _ := c.store.messagesAfter(before, 10)
	if len(msgs) != 1 || msgs[0].Text != "Quoi de neuf ?" || msgs[0].Via != viaSignal {
		t.Fatalf("conversation %+v", msgs)
	}
	if n := b.getInt("ignored"); n != 3 {
		t.Errorf("%d ignored, want 3 (the wrong code, the group, the stranger)", n)
	}
	for _, m := range f.sentNow() {
		if m["to"] != sgUserNum {
			t.Fatalf("sent %v", m)
		}
	}

	// Removing the channel unregisters the number, then the service goes.
	tw.post("/channels/signal/remove", nil, csrf)
	if _, err := c.store.service("channel-signal"); err == nil || !f.removed || b.get("bot") != "" || b.running() {
		t.Fatalf("still there: %v %v %q", err, f.removed, b.get("bot"))
	}
}

// signalLinked is the channel with a number registered and the user's
// account linked.
func signalLinked(t *testing.T) (*botChannel, *fakeSignal) {
	t.Helper()
	_, b, f, csrf := signalWeb(t)
	_ = csrf
	if err := b.setService(true); err != nil {
		t.Fatal(err)
	}
	b.set("user", sgUserUUID)
	b.set("chat", sgUserNum)
	b.c.store.set(channelSeenKey(sgChannel), fmt.Sprint(b.c.store.lastMessageSeq()))
	f.number = sgNumber
	sgOf(b).registered(sgNumber)
	return b, f
}

// A notification ends with its buttons' legend; a reaction with one of its
// emojis, from the user only, is the tap.
func TestSignalNotifyAndReactions(t *testing.T) {
	b, f := signalLinked(t)
	c := b.c
	c.store.set(notifyChannelsKey, sgChannel)
	ownerSays(c, "Préviens-moi quand la PR est fusionnée")
	out, err := c.brainNotify(notifyIn{Text: "La PR 42 est fusionnée.", Weight: 5, Why: "Tu m'as demandé.", Ref: "project:site"})
	if err != nil {
		t.Fatal(err)
	}
	b.pending.Wait()
	id := out["id"].(int64)
	sent := f.sentNow()
	text := fmt.Sprint(sent[len(sent)-1]["text"])
	if !strings.Contains(text, "La PR 42") || !strings.HasSuffix(text, "👍 Utile · 👎 Pas utile · ⏳ Pas maintenant") {
		t.Fatalf("sent %q", text)
	}
	ts := f.lastTS()

	f.reacts(sgStranger, "+15555550102", "👍", ts)
	f.reacts(sgUserUUID, sgUserNum, "😂", ts)  // no button
	f.reacts(sgUserUUID, sgUserNum, "👎🏽", ts) // a skin tone is the same emoji
	waitUntil(t, func() bool { n, _ := c.notificationByID(id); return n.Feedback == "down" })
	waitUntil(t, func() bool { return len(f.sentNow()) == len(sent)+1 })
	if got := fmt.Sprint(f.sentNow()[len(sent)]["text"]); got != "→ "+tr("fr", "notif.fb.thanks") {
		t.Fatalf("answered %q", got)
	}

	// A task's permission: 👍 allows it, and the message's reactions are
	// no longer taps.
	addTask(t, c, "t-1", stateRunning)
	c.store.addEvents("t-1", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": "p-1", "tool_name": "mcp__github__merge_pull_request"}}})
	b.post(botMessage{Text: "La question", Buttons: [][]botButton{{
		{Text: "✓ Autoriser", Data: "pm:t-1:p-1:y"}, {Text: "✕ Refuser", Data: "pm:t-1:p-1:n"}}}})
	b.pending.Wait()
	ts = f.lastTS()
	if got := fmt.Sprint(f.sentNow()[len(f.sentNow())-1]["text"]); got != "La question\n\n👍 Autoriser · 👎 Refuser" {
		t.Fatalf("sent %q", got)
	}
	f.reacts(sgUserUUID, sgUserNum, "👍", ts)
	waitUntil(t, func() bool { return c.store.answered("t-1", "p-1") })
	waitUntil(t, func() bool { return !strings.Contains(b.get("buttons"), fmt.Sprint(ts)) })
}

// The channel's service is wanted while Signal is on, from the store
// alone: a reconciliation before the channel starts keeps it, and one by
// the catalog never drops it.
func TestSignalServiceOwned(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	c.store.set("signal_service", "on")
	if err := c.reconcileServices(); err != nil { // no channel started
		t.Fatal(err)
	}
	if _, err := c.store.service("channel-signal"); err != nil {
		t.Fatal(err)
	}
	if err := c.putServer(putCreate, "time", Server{Use: "time", Run: marshalRun(RunSpec{Image: "mcp/time", Transport: transportStdio})}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.service("channel-signal"); err != nil {
		t.Fatalf("the catalog dropped it: %v", err)
	}
	c.store.set("signal_service", "")
	c.reconcileServices()
	if _, err := c.store.service("channel-signal"); err == nil {
		t.Fatal("still wanted")
	}
}

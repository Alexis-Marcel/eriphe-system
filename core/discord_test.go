package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeDiscordToken has a Discord token's shape, built here so that no
// scanner takes the source for a leaked one.
var fakeDiscordToken = strings.Join([]string{"FAKE" + strings.Repeat("x", 20), "FAKE", "not_a_real_token_for_tests_00"}, ".")

const (
	dcBotID      = "900"
	dcUserID     = "1001"
	dcStrangerID = "6666"
)

// fakeDiscord is Discord's REST API and Gateway, as eriphe-core uses them.
type fakeDiscord struct {
	srv *httptest.Server

	mu        sync.Mutex
	sent      []map[string]any // channel messages, with "channel"
	callbacks []map[string]any // interaction callbacks, with "auth"
	opens     []map[string]any // what each connection opened with: op 2 or op 6
	beats     int
	seq       int64
	limited   int // messages answered 429 before one goes through

	out chan []byte // what the open connection sends next
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	f := &fakeDiscord{out: make(chan []byte, 64)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDiscord) serve(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/gateway/" || r.URL.Path == "/gateway" {
		f.gateway(rw, r)
		return
	}
	var in map[string]any
	json.NewDecoder(r.Body).Decode(&in)
	reply := func(code int, v any) {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(code)
		json.NewEncoder(rw).Encode(v)
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v10")
	if strings.HasPrefix(path, "/interactions/") {
		f.mu.Lock()
		in["auth"] = r.Header.Get("Authorization")
		in["path"] = path
		f.callbacks = append(f.callbacks, in)
		f.mu.Unlock()
		rw.WriteHeader(204)
		return
	}
	if r.Header.Get("Authorization") != "Bot "+fakeDiscordToken {
		reply(401, map[string]any{"message": "401: Unauthorized", "code": 0})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "GET" && path == "/users/@me":
		reply(200, dcUser{ID: dcBotID, Username: "eriphe_bot", Bot: true})
	case r.Method == "POST" && strings.HasPrefix(path, "/channels/") && strings.HasSuffix(path, "/messages"):
		if f.limited > 0 {
			f.limited--
			reply(429, map[string]any{"message": "You are being rate limited.", "retry_after": 0.5, "global": false})
			return
		}
		in["channel"] = strings.TrimSuffix(strings.TrimPrefix(path, "/channels/"), "/messages")
		f.sent = append(f.sent, in)
		reply(200, map[string]any{"id": fmt.Sprint(len(f.sent))})
	default:
		reply(404, map[string]any{"message": "404: Not Found"})
	}
}

// gateway is one connection: Hello, then what it is told to send; a
// heartbeat is acknowledged, Identify answered with READY.
func (f *fakeDiscord) gateway(rw http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(rw, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context()) // ends with the connection: no writer outlives it
	defer cancel()
	conn.Write(ctx, websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":50}}`))
	go func() {
		defer cancel()
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var p map[string]any
			json.Unmarshal(raw, &p)
			switch p["op"] {
			case 1.0:
				f.mu.Lock()
				f.beats++
				f.mu.Unlock()
				conn.Write(ctx, websocket.MessageText, []byte(`{"op":11}`))
			case 2.0, 6.0:
				f.mu.Lock()
				f.opens = append(f.opens, p)
				f.mu.Unlock()
				if p["op"] == 2.0 {
					f.dispatch("READY", map[string]any{"session_id": "s1", "resume_gateway_url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/gateway",
						"user": map[string]any{"id": dcBotID, "username": "eriphe_bot", "bot": true}})
				}
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case raw := <-f.out:
			var p map[string]any
			json.Unmarshal(raw, &p)
			if code, ok := p["close"].(float64); ok {
				conn.Close(websocket.StatusCode(code), "test")
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
				return
			}
		}
	}
}

// dispatch sends an event on the open connection.
func (f *fakeDiscord) dispatch(t string, d any) {
	f.mu.Lock()
	f.seq++
	s := f.seq
	f.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"op": 0, "t": t, "s": s, "d": d})
	f.out <- raw
}

func (f *fakeDiscord) op(op int, d any) {
	raw, _ := json.Marshal(map[string]any{"op": op, "d": d})
	f.out <- raw
}

func (f *fakeDiscord) closeWith(code int) {
	raw, _ := json.Marshal(map[string]any{"close": code})
	f.out <- raw
}

func (f *fakeDiscord) dm(from, text string) {
	f.dispatch("MESSAGE_CREATE", map[string]any{"id": "m", "channel_id": "dm-" + from, "content": text,
		"author": map[string]any{"id": from, "username": "sam", "bot": from == dcBotID}})
}

func (f *fakeDiscord) tap(from, data string) {
	f.dispatch("INTERACTION_CREATE", map[string]any{"id": "i-" + data, "token": "itoken", "type": 3, "channel_id": "dm-" + from,
		"user": map[string]any{"id": from}, "data": map[string]any{"custom_id": data},
		"message": map[string]any{"id": "1", "channel_id": "dm-" + from, "content": "La question", "author": map[string]any{"id": dcBotID}}})
}

func (f *fakeDiscord) sentNow() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent...)
}

func (f *fakeDiscord) callbacksNow() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.callbacks...)
}

func (f *fakeDiscord) opensNow() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.opens...)
}

// discordCore is notifyCore with a vault and a Discord channel on the fake.
func discordCore(t *testing.T) (*core, *botChannel, *fakeDiscord) {
	t.Helper()
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	openTestVault(t, c.store)
	f := newFakeDiscord(t)
	b := newBotChannel(c, dcChannel)
	d := b.tr.(*discord)
	d.api, d.gateway, d.client = f.srv.URL+"/api/v10", "ws"+strings.TrimPrefix(f.srv.URL, "http")+"/gateway", f.srv.Client()
	b.base = func() string { return "https://eriphe.example.org" }
	b.sleep = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
	c.setBot(b)
	t.Cleanup(b.stop)
	return c, b, f
}

// The token is checked, the Gateway opened with the DM intent; the account
// that sends the code in a DM is linked and the only one heard; the bot's
// own messages are not events.
func TestDiscordLinkAndTalk(t *testing.T) {
	c, b, f := discordCore(t)
	if err := b.setToken(context.Background(), strings.Replace(fakeDiscordToken, "FAKE.", "WRONG.", 1)); err == nil || b.token() != "" {
		t.Fatal("a wrong token was kept")
	}
	if err := b.setToken(context.Background(), fakeDiscordToken); err != nil {
		t.Fatal(err)
	}
	if b.get("bot") != "eriphe_bot" || b.get("bot_id") != dcBotID || !b.running() {
		t.Fatalf("bot %q %q", b.get("bot"), b.get("bot_id"))
	}
	if p := b.page(); !strings.Contains(p.Invite, "client_id="+dcBotID) || !strings.Contains(p.Invite, "scope=bot") {
		t.Fatalf("invite %q", p.Invite)
	}
	waitUntil(t, func() bool { return len(f.opensNow()) == 1 })
	open := f.opensNow()[0]
	d, _ := open["d"].(map[string]any)
	if open["op"] != 2.0 || d["token"] != fakeDiscordToken || d["intents"] != float64(dcIntents) {
		t.Fatalf("opened with %v", open)
	}

	code := b.newCode("fr")
	f.dm(dcStrangerID, "NOPE")
	f.dm(dcUserID, strings.ToLower(code))
	waitUntil(t, b.linked)
	if b.get("user") != dcUserID || b.get("chat") != "dm-"+dcUserID || b.get("name") != "@sam" {
		t.Fatalf("linked to %q %q %q", b.get("user"), b.get("chat"), b.get("name"))
	}
	waitUntil(t, func() bool { return len(f.sentNow()) == 1 }) // "C'est fait"

	before := c.store.lastMessageSeq()
	f.dm(dcBotID, "C'est fait : ce compte est relié") // its own: not an event
	f.dispatch("MESSAGE_CREATE", map[string]any{"id": "g", "channel_id": "general", "guild_id": "g1", "content": "dans un serveur",
		"author": map[string]any{"id": dcUserID, "username": "sam"}})
	f.dm(dcUserID, "Quoi de neuf ?")
	waitUntil(t, func() bool { return c.store.lastMessageSeq() > before })
	msgs, _ := c.store.messagesAfter(before, 10)
	if len(msgs) != 1 || msgs[0].Text != "Quoi de neuf ?" || msgs[0].Via != viaDiscord {
		t.Fatalf("conversation %+v", msgs)
	}
	if n := b.getInt("ignored"); n != 2 {
		t.Errorf("%d ignored, want 2 (the wrong code, the server)", n)
	}
	for _, m := range f.sentNow() {
		if m["channel"] != "dm-"+dcUserID {
			t.Fatalf("someone else was answered: %v", m)
		}
	}
}

// A notification goes as a DM with its buttons and no mention; a tap is
// answered within the interaction, without the bot's token.
func TestDiscordNotifyAndButtons(t *testing.T) {
	c, b, f := discordCore(t)
	linkDiscord(t, c, b)
	c.store.set(notifyChannelsKey, dcChannel)
	ownerSays(c, "Préviens-moi quand la PR est fusionnée")
	out, err := c.brainNotify(notifyIn{Text: "La PR 42 est fusionnée.", Weight: 5, Why: "Tu m'as demandé.", Ref: "project:site"})
	if err != nil {
		t.Fatal(err)
	}
	b.pending.Wait()
	id := out["id"].(int64)
	sent := f.sentNow()
	last := sent[len(sent)-1]
	raw, _ := json.Marshal(last)
	if !strings.Contains(fmt.Sprint(last["content"]), "La PR 42") || !strings.Contains(string(raw), fmt.Sprintf(`"custom_id":"fb:%d:down"`, id)) ||
		!strings.Contains(string(raw), `"allowed_mentions":{"parse":[]}`) {
		t.Fatalf("sent %s", raw)
	}

	f.tap(dcStrangerID, fmt.Sprintf("fb:%d:up", id))
	f.tap(dcUserID, fmt.Sprintf("fb:%d:down", id))
	waitUntil(t, func() bool { return len(f.callbacksNow()) == 1 })
	if n, _ := c.notificationByID(id); n.Feedback != "down" {
		t.Fatalf("feedback %q", n.Feedback)
	}
	cb := f.callbacksNow()[0]
	data, _ := cb["data"].(map[string]any)
	if cb["type"] != 4.0 || data["flags"] != 64.0 || cb["auth"] != "" || !strings.Contains(fmt.Sprint(cb["path"]), "/i-fb:") {
		t.Fatalf("callback %v", cb)
	}

	// A task's permission: Allow updates the message, without its buttons.
	addTask(t, c, "t-1", stateRunning)
	c.store.addEvents("t-1", []EventRow{{Seq: 1, Time: time.Now(), Type: "permission",
		Data: map[string]any{"id": "p-1", "tool_name": "mcp__github__merge_pull_request"}}})
	f.tap(dcUserID, "pm:t-1:p-1:y")
	waitUntil(t, func() bool { return c.store.answered("t-1", "p-1") })
	waitUntil(t, func() bool { return len(f.callbacksNow()) == 2 })
	cb = f.callbacksNow()[1]
	data, _ = cb["data"].(map[string]any)
	if cb["type"] != 7.0 || !strings.Contains(fmt.Sprint(data["content"]), "La question\n\n→ "+tr("fr", "perm.allowed")) ||
		fmt.Sprint(data["components"]) != "[]" {
		t.Fatalf("callback %v", cb)
	}
}

// linkDiscord sets the token and links the user's account, as the code
// would.
func linkDiscord(t *testing.T, c *core, b *botChannel) {
	t.Helper()
	currentVault().set(b.secret(), fakeDiscordToken)
	b.set("user", dcUserID)
	b.set("chat", "dm-"+dcUserID)
	c.store.set(channelSeenKey(dcChannel), fmt.Sprint(c.store.lastMessageSeq()))
	b.restart()
}

// Discord asks to reconnect: the session resumes where it was. A refused
// token ends the session, says so without the token, and the next
// connection identifies again.
func TestDiscordResumeAndClose(t *testing.T) {
	c, b, f := discordCore(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	linkDiscord(t, c, b)
	waitUntil(t, func() bool { return len(f.opensNow()) == 1 })
	f.dm(dcUserID, "un")
	waitUntil(t, func() bool {
		msgs, _ := c.store.messagesAfter(0, 50)
		return len(msgs) > 0 && msgs[len(msgs)-1].Text == "un"
	})
	waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.beats > 0 })

	f.op(7, nil)
	waitUntil(t, func() bool { return len(f.opensNow()) == 2 })
	resume := f.opensNow()[1]
	d, _ := resume["d"].(map[string]any)
	if resume["op"] != 6.0 || d["session_id"] != "s1" || d["seq"] != 2.0 || d["token"] != fakeDiscordToken {
		t.Fatalf("resumed with %v", resume)
	}
	if b.err() != "" {
		t.Errorf("a reconnect is not a failure: %q", b.err())
	}

	f.closeWith(4004)
	waitUntil(t, func() bool { return len(f.opensNow()) == 3 })
	if again := f.opensNow()[2]; again["op"] != 2.0 {
		t.Fatalf("after a refused token: %v", again)
	}
	waitUntil(t, func() bool { return strings.Contains(logs.String(), "refused") })
	if strings.Contains(logs.String(), fakeDiscordToken) {
		t.Fatal("the token is in the logs")
	}
}

// Discord's 429 says how long to wait: the bot channel waits that long.
func TestDiscordRateLimit(t *testing.T) {
	_, b, f := discordCore(t)
	f.mu.Lock()
	f.limited = 1
	f.mu.Unlock()
	err := b.tr.send(context.Background(), fakeDiscordToken, "dm-1", botMessage{Text: "x"})
	if d := nextBackoff(0, err); d != 500*time.Millisecond {
		t.Fatalf("wait %s after %v", d, err)
	}
	if err := b.tr.send(context.Background(), fakeDiscordToken, "dm-1", botMessage{Text: "x"}); err != nil {
		t.Fatal(err)
	}
}

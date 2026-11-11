package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChat is a messaging service that exists only in tests: a second bot
// channel, registered with a transport and its words and nothing else, to
// prove that is all a new channel needs. Its ids are strings, as
// Discord's snowflakes are.
type fakeChat struct {
	mu       sync.Mutex
	events   []botEvent
	sent     []fakeSent
	answered []string // toast|done
	fail     error    // what the next listen returns
}

type fakeSent struct {
	chat string
	m    botMessage
}

const fakeChatToken = "fake.chat.token.0123456789"

func (f *fakeChat) check(_ context.Context, token string) (string, string, error) {
	if token != fakeChatToken {
		return "", "", errors.New("fakechat: 401 bad token " + token) // a careless service
	}
	return "sam_bot", "42", nil
}

func (f *fakeChat) listen(ctx context.Context, token string, on func(botEvent)) error {
	f.mu.Lock()
	evs, err := f.events, f.fail
	f.events, f.fail = nil, nil
	f.mu.Unlock()
	if err != nil {
		return err
	}
	for _, ev := range evs {
		on(ev)
	}
	if len(evs) == 0 {
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Millisecond):
		}
	}
	return nil
}

func (f *fakeChat) send(_ context.Context, _ string, chat string, m botMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, fakeSent{chat, m})
	return nil
}

func (f *fakeChat) answer(_ context.Context, _ string, _ botEvent, toast, done string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = append(f.answered, toast+"|"+done)
	return nil
}

func (f *fakeChat) push(ev botEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeChat) sentNow() []fakeSent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

func (f *fakeChat) answers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.answered)
}

const (
	fakeUser     = "112233445566778899"
	fakeStranger = "998877665544332211"
)

// registerFakeChat registers the fake channel for one test, its words too.
func registerFakeChat(t *testing.T) *fakeChat {
	t.Helper()
	f := &fakeChat{}
	before := len(channelKinds)
	registerBot(botSpec{ID: "fakechat", Label: "Fakechat", Icon: "chat", TextMax: 2000, DataMax: 100,
		Token: regexp.MustCompile(`^[a-z0-9.]{20,}$`), Placeholder: "token",
		New: func(*botChannel) botTransport { return f }})
	words := map[string]string{"acc.kind.fakechat": "Fakechat", "acc.kind.fakechat.desc": "Parle depuis Fakechat.",
		"fakechat.how": "Crée un bot Fakechat.", "fakechat.privacy": "Fakechat lit tout."}
	for _, lang := range []string{"fr", "en"} {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
	t.Cleanup(func() {
		channelKinds = channelKinds[:before]
		delete(botSpecs, "fakechat")
		for _, lang := range []string{"fr", "en"} {
			for k := range words {
				delete(webStrings[lang], k)
			}
		}
	})
	return f
}

// A second bot channel needs only its transport and its words: its page,
// its row, the link, the conversation, notifications and their buttons, a
// task's permission and the routing come with registerBot.
func TestBotChannelFromATransport(t *testing.T) {
	f := registerFakeChat(t)
	tw := newTestWeb(t)
	csrf := tw.setup()
	openTestVault(t, tw.c.store)
	b := tw.c.bot("fakechat")
	if b == nil {
		t.Fatal("not started")
	}
	t.Cleanup(b.stop)
	b.base = func() string { return "https://eriphe.example.org" }
	if !slices.Contains(sendChannels(), "fakechat") || !slices.Contains(routeLines(), "fakechat") {
		t.Fatalf("channels %v, lines %v", sendChannels(), routeLines())
	}

	// Settings → Access: in the catalog, then its page.
	if _, body, _ := tw.do("GET", "/settings/access/new", nil, nil); !strings.Contains(body, `href="/settings/access/new/fakechat"`) {
		t.Fatal("not in the catalog")
	}
	code, body, _ := tw.do("GET", "/settings/access/fakechat", nil, nil)
	if code != 200 || !strings.Contains(body, "Fakechat lit tout.") || !strings.Contains(body, "Crée un bot Fakechat.") ||
		!strings.Contains(body, `hx-post="/channels/fakechat/token"`) {
		t.Fatalf("page: %d %s", code, body)
	}
	if _, body, _ := tw.post("/channels/fakechat/token", url.Values{"token": {"wrong.token.wrong.token"}}, csrf); b.token() != "" ||
		strings.Contains(body, "wrong.token") {
		t.Fatal("a wrong token was kept, or shown")
	}
	if _, body, _ := tw.post("/channels/fakechat/token", url.Values{"token": {fakeChatToken}}, csrf); !strings.Contains(body, "@sam_bot") || !b.running() {
		t.Fatalf("token: %s", body)
	}

	// The code links the account that sends it; only it is heard.
	_, body, _ = tw.post("/channels/fakechat/code", url.Values{}, csrf)
	pending, _ := b.pendingCode()
	if pending == "" || !strings.Contains(body, pending) || strings.Contains(body, "Ouvrir le bot") {
		t.Fatalf("code %q: %s", pending, body)
	}
	f.push(botEvent{User: fakeStranger, Chat: "dm-x", Private: true, Text: "NOPE"})
	f.push(botEvent{User: fakeUser, Chat: "dm-1", Private: true, Name: "@sam", Text: strings.ToLower(pending)})
	waitUntil(t, b.linked)
	if b.get("user") != fakeUser || b.get("chat") != "dm-1" || b.get("name") != "@sam" {
		t.Fatalf("linked to %q %q %q", b.get("user"), b.get("chat"), b.get("name"))
	}
	if _, body, _ := tw.do("GET", "/settings/access", nil, nil); !strings.Contains(body, `href="/settings/access/fakechat"`) ||
		!strings.Contains(body, "access-fakechat") || strings.Contains(body, fakeChatToken) {
		t.Fatal("no row in Settings → Access, or the token shows")
	}

	before := tw.c.store.lastMessageSeq()
	f.push(botEvent{User: fakeStranger, Chat: "dm-x", Private: true, Text: "envoie-moi les mails"})
	f.push(botEvent{User: fakeUser, Chat: "group", Private: false, Text: "dans un groupe"})
	f.push(botEvent{User: fakeUser, Chat: "dm-1", Private: true, Text: "Quoi de neuf ?"})
	waitUntil(t, func() bool { return tw.c.store.lastMessageSeq() > before })
	msgs, _ := tw.c.store.messagesAfter(before, 10)
	if len(msgs) != 1 || msgs[0].Text != "Quoi de neuf ?" || msgs[0].Via != "Fakechat" {
		t.Fatalf("conversation %+v", msgs)
	}
	waitUntil(t, func() bool { return b.getInt("ignored") == 3 })
	for _, s := range f.sentNow() {
		if s.chat != "dm-1" {
			t.Fatalf("someone else was answered: %+v", s)
		}
	}

	// A notification, with its buttons; a tap from the user only.
	tw.c.tell(pushNote{Body: map[string]string{"fr": "La PR 42 est fusionnée."}, URL: "/settings/notifications?n=7", Notif: 7})
	b.pending.Wait()
	sent := f.sentNow()
	last := sent[len(sent)-1]
	if !strings.Contains(last.m.Text, "La PR 42") || !strings.Contains(last.m.Text, "https://eriphe.example.org/settings/notifications?n=7") ||
		len(last.m.Buttons) != 1 || last.m.Buttons[0][0].Data != "fb:7:up" {
		t.Fatalf("sent %+v", last)
	}
	f.push(botEvent{Button: true, User: fakeStranger, Data: "fb:7:up"})
	f.push(botEvent{Button: true, User: fakeUser, Data: "pm:t-9:p-9:y"}) // a task it does not know
	waitUntil(t, func() bool { return len(f.answers()) == 1 })
	if a := f.answers()[0]; a != tr("fr", "card.channel.failed")+"|" {
		t.Errorf("answered %q", a)
	}

	// A permission longer than Telegram takes still has its buttons here.
	note := waitingNote("github.merge", "/#perm", "perm-x")
	note.Perm = &notePerm{Task: strings.Repeat("t", 40), ID: strings.Repeat("p", 40)}
	tw.c.tell(note)
	b.pending.Wait()
	if last := f.sentNow()[len(f.sentNow())-1]; len(last.m.Buttons) != 1 || !strings.HasPrefix(last.m.Buttons[0][0].Data, "pm:") {
		t.Fatalf("permission sent %+v", last)
	}

	// Alerts only, from Notifications: a nudge and a link.
	tw.post("/channels/settings", url.Values{"detail-fakechat": {"alert"}, "channel": {"push", "fakechat"}}, csrf)
	tw.c.tell(pushNote{Body: map[string]string{"fr": "Solde 1234 €"}, URL: "/", Notif: 8})
	b.pending.Wait()
	if last := f.sentNow()[len(f.sentNow())-1]; strings.Contains(last.m.Text, "1234") || len(last.m.Buttons) != 0 {
		t.Fatalf("alert %+v", last)
	}

	tw.post("/channels/fakechat/remove", url.Values{}, csrf)
	if b.token() != "" || b.linked() || b.running() {
		t.Fatal("not removed")
	}
}

// Whatever a transport puts in its errors, the token never shows: not in
// a returned error, the page, nor the logs.
func TestBotChannelScrubsTheToken(t *testing.T) {
	f := registerFakeChat(t)
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	openTestVault(t, c.store)
	b := newBotChannel(c, "fakechat")
	c.setBot(b)
	t.Cleanup(b.stop)
	var waits []time.Duration
	var mu sync.Mutex
	b.sleep = func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err() == nil
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	if err := b.setToken(context.Background(), "another.token.of.the.user.0123"); err == nil || strings.Contains(err.Error(), "another.token") {
		t.Fatalf("error %v", err)
	}
	f.mu.Lock()
	f.fail = &fakeRetry{msg: "fakechat: 429 for " + fakeChatToken, wait: 30 * time.Second}
	f.mu.Unlock()
	if err := b.setToken(context.Background(), fakeChatToken); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(waits) > 0 })
	mu.Lock()
	if waits[0] != 30*time.Second {
		t.Errorf("the service's wait was not followed: %v", waits)
	}
	mu.Unlock()
	b.stop()
	if strings.Contains(b.err(), fakeChatToken) || strings.Contains(logs.String(), fakeChatToken) || !strings.Contains(logs.String(), "429") {
		t.Fatalf("err %q, logs %s", b.err(), logs.String())
	}
}

type fakeRetry struct {
	msg  string
	wait time.Duration
}

func (e *fakeRetry) Error() string             { return e.msg }
func (e *fakeRetry) retryAfter() time.Duration { return e.wait }

// Nothing outside a transport's own files names its channel by its id (a
// key, a route, a class, a file): the rest goes through the channel
// registry and the bot channel. The service's name in prose ("Discord" in
// the gallery's list of apps) is not the channel.
func TestBotChannelsNamedByTheirFilesOnly(t *testing.T) {
	own := map[string][]string{"telegram": {"telegram.go", "web_text_telegram.go"},
		"discord": {"discord.go", "web_text_discord.go"}}
	files, _ := filepath.Glob("*.go")
	tmpl, _ := filepath.Glob("web/templates/*.html")
	for _, file := range append(files, tmpl...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for id, mine := range own {
			if !slices.Contains(mine, file) && strings.Contains(text, id) {
				t.Errorf("%s names %s: only %v may", file, id, mine)
			}
		}
	}
	for id := range botSpecs {
		if _, ok := own[id]; !ok && id != "fakechat" {
			t.Errorf("bot channel %s: add its files to this test", id)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const samMail = `From: Sam <sam@example.org>
To: alice@example.com
Subject: Lunch
Date: Tue, 27 Oct 2026 12:00:00 +0100
Message-ID: <lunch-1@example.org>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8

Lunch on Friday?`

func testCacheOptions() CacheOptions {
	return CacheOptions{Days: 30, MaxMessages: 100, BodyBytes: 1 << 20, Poll: time.Hour, Folders: []string{"inbox", "sent"}}
}

type cachedEnv struct {
	*testEnv
	m       *Mailer
	cs      *mcp.ClientSession
	changes chan MailboxChange
}

// connectCached starts a server with its cache and waits for it to be warm.
func connectCached(t *testing.T, e *testEnv, o CacheOptions) *cachedEnv {
	t.Helper()
	m, err := NewMailer(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ce := &cachedEnv{testEnv: e, m: m, changes: make(chan MailboxChange, 100)}
	m.OnChange = func(c MailboxChange) { ce.changes <- c }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.StartCache(ctx, o)
	for _, a := range m.accounts {
		select {
		case <-a.cache.ready:
		case <-time.After(10 * time.Second):
			t.Fatal("cache not warm")
		}
	}
	for len(ce.changes) > 0 {
		<-ce.changes // the first sync
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := newServer(m, "test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	if ce.cs, err = mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ce.cs.Close() })
	return ce
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// change waits for a change in a folder of an account.
func (ce *cachedEnv) change(t *testing.T, account, folder string) MailboxChange {
	t.Helper()
	for {
		select {
		case c := <-ce.changes:
			if c.Account == account && c.Folder == folder {
				return c
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no change in %s/%s", account, folder)
		}
	}
}

// logins counts IMAP sessions opened so far: a request answered from the
// cache opens none.
func (ce *cachedEnv) logins() int { return len(ce.imap.loginsSeen()) }

// onServer runs fn on the user's INBOX through a separate session, as
// another mail client would.
func onServer(t *testing.T, e *testEnv, fn func(c *imapclient.Client)) {
	t.Helper()
	acct := mustAccount(t, e.cfg.Accounts[0])
	acct.timeout = 10 * time.Second
	if err := acct.session(context.Background(), func(c *imapclient.Client) error {
		if _, err := c.Select("INBOX", nil).Wait(); err != nil {
			return err
		}
		fn(c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func deliverAt(t *testing.T, u *imapmemserver.User, folder, raw string, at time.Time) {
	t.Helper()
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	if _, err := u.Append(folder, literal{bytes.NewReader([]byte(raw))}, &imap.AppendOptions{Time: at}); err != nil {
		t.Fatal(err)
	}
}

func TestCacheAnswersSearchAndRead(t *testing.T) {
	ce := connectCached(t, newEnv(t, rev2), testCacheOptions())
	n := ce.logins()

	out := call[searchOut](t, ce.cs, "search_messages", map[string]any{"from": "durand"})
	if len(out.Messages) != 2 || out.Messages[0].Subject != "Réunion" || !strings.Contains(out.Messages[0].Snippet, "La réunion est déplacée") ||
		out.Messages[1].From != "Jean Durand <jean.durand@example.org>" || !out.Messages[1].Unread {
		t.Fatalf("from cache: %+v", out.Messages)
	}
	unread := call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "unread": true})
	if len(unread.Messages) != 1 || unread.Messages[0].Subject != "Devis cuisine" {
		t.Errorf("unread: %+v", unread.Messages)
	}
	since := call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "since": time.Now().AddDate(0, 0, -3).Format(time.DateOnly)})
	if len(since.Messages) != 0 {
		t.Errorf("since: %+v", since.Messages)
	}
	if ce.logins() != n {
		t.Fatalf("searches went to the server: %d logins", ce.logins()-n)
	}

	// Full text needs the server.
	txt := call[searchOut](t, ce.cs, "search_messages", map[string]any{"text": "cuisine"})
	if len(txt.Messages) != 1 || ce.logins() == n {
		t.Errorf("text search: %+v, logins %d", txt.Messages, ce.logins()-n)
	}

	// The first read fetches the body, the next ones do not.
	id := unread.Messages[0].ID
	first := call[Message](t, ce.cs, "read_message", map[string]any{"id": id})
	n = ce.logins()
	again := call[Message](t, ce.cs, "read_message", map[string]any{"id": id})
	if ce.logins() != n {
		t.Error("second read went to the server")
	}
	if again.Body != first.Body || !strings.Contains(again.Body, "voici le devis") || again.MessageID != "devis-1@example.org" || !again.Unread {
		t.Errorf("cached read: %+v", again)
	}
	short := call[Message](t, ce.cs, "read_message", map[string]any{"id": id, "max_chars": 10})
	if !short.Truncated || ce.logins() != n {
		t.Errorf("max_chars on a cached body: %+v", short)
	}
}

func TestCachePrefetchesBodies(t *testing.T) {
	o := testCacheOptions()
	o.Prefetch = 5
	ce := connectCached(t, newEnv(t, rev2), o)
	s := call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "subject": "promo"})
	waitFor(t, "prefetch", func() bool { return ce.m.accounts[0].cache.bodies.has(s.Messages[0].ID) })
	n := ce.logins()
	m := call[Message](t, ce.cs, "read_message", map[string]any{"id": s.Messages[0].ID})
	if ce.logins() != n || !strings.Contains(m.Body, "Hello Alice") || m.BodyFormat != "html converted to text" || len(m.Attachments) != 1 {
		t.Errorf("prefetched read: %+v", m)
	}
}

func TestCacheFollowsIdle(t *testing.T) {
	e := newEnv(t, rev2)
	ce := connectCached(t, e, testCacheOptions())

	deliver(t, e.alice, "INBOX", samMail)
	if c := ce.change(t, "perso", "INBOX"); c.New != 1 {
		t.Fatalf("new mail: %+v", c)
	}
	n := ce.logins()
	s := call[searchOut](t, ce.cs, "search_messages", map[string]any{"from": "sam@example.org"})
	if len(s.Messages) != 1 || !s.Messages[0].Unread || ce.logins() != n {
		t.Fatalf("new mail from cache: %+v", s.Messages)
	}

	// Another client reads it, then deletes it.
	onServer(t, e, func(c *imapclient.Client) {
		res, _ := c.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: "sam"}}}, nil).Wait()
		set := imap.UIDSetNum(res.AllUIDs()...)
		c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
	})
	if c := ce.change(t, "perso", "INBOX"); c.Flags != 1 {
		t.Fatalf("flag change: %+v", c)
	}
	n = ce.logins()
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"from": "sam@example.org"})
	if len(s.Messages) != 1 || s.Messages[0].Unread {
		t.Fatalf("after read elsewhere: %+v", s.Messages)
	}
	onServer(t, e, func(c *imapclient.Client) {
		res, _ := c.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: "sam"}}}, nil).Wait()
		set := imap.UIDSetNum(res.AllUIDs()...)
		c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
		c.Expunge().Close()
	})
	if c := ce.change(t, "perso", "INBOX"); c.Gone != 1 {
		t.Fatalf("expunge: %+v", c)
	}
	n = ce.logins()
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso"})
	if len(s.Messages) != 2 || ce.logins() != n {
		t.Fatalf("after expunge: %+v", s.Messages)
	}
}

func TestCachePollsWithoutIdle(t *testing.T) {
	e := newEnv(t, rev2)
	o := testCacheOptions()
	o.NoIdle, o.Poll = true, 50*time.Millisecond
	ce := connectCached(t, e, o)
	deliver(t, e.bob, "INBOX", samMail)
	if c := ce.change(t, "pro", "INBOX"); c.New != 1 {
		t.Fatalf("polled: %+v", c)
	}
}

func TestCacheWindowAndCap(t *testing.T) {
	e := newEnv(t, rev2)
	deliverAt(t, e.alice, "INBOX", strings.Replace(samMail, "27 Oct 2026", "6 Jan 2025", 1), time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC))
	ce := connectCached(t, e, testCacheOptions())

	// Older than the window: the server answers, and finds it.
	n := ce.logins()
	s := call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "since": "2025-01-01"})
	if len(s.Messages) != 3 || ce.logins() == n {
		t.Fatalf("old mail: %d messages, %d logins", len(s.Messages), ce.logins()-n)
	}
	// Two recent messages in cache, three in the folder: two newest are sure.
	n = ce.logins()
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "limit": 2})
	if len(s.Messages) != 2 || ce.logins() != n {
		t.Fatalf("limit within the window: %+v", s.Messages)
	}
	// Three asked: the third is older than the window.
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "limit": 3})
	if len(s.Messages) != 3 || ce.logins() == n {
		t.Fatalf("limit beyond the window: %+v", s.Messages)
	}
	// A folder not cached.
	n = ce.logins()
	call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "folder": "trash"})
	if ce.logins() == n {
		t.Error("trash answered from the cache")
	}

	// Capped to one message: only the newest is kept.
	o := testCacheOptions()
	o.MaxMessages = 1
	capped := connectCached(t, e, o)
	mc := capped.m.accounts[0].cache
	mc.mu.RLock()
	cached := len(mc.folders["INBOX"].msgs)
	mc.mu.RUnlock()
	if cached != 1 {
		t.Fatalf("cap: %d messages cached", cached)
	}
	n = capped.logins()
	s = call[searchOut](t, capped.cs, "search_messages", map[string]any{"account": "perso", "limit": 1})
	if len(s.Messages) != 1 || s.Messages[0].Subject != "Promo" || capped.logins() != n {
		t.Fatalf("capped newest: %+v", s.Messages)
	}
	call[searchOut](t, capped.cs, "search_messages", map[string]any{"account": "perso", "limit": 2})
	if capped.logins() == n {
		t.Error("capped cache answered beyond what it holds")
	}
}

func TestCacheFollowsWrites(t *testing.T) {
	e := newEnv(t, rev2)
	ce := connectCached(t, e, testCacheOptions())
	s := call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "subject": "devis"})
	call[markOut](t, ce.cs, "mark", map[string]any{"ids": []string{s.Messages[0].ID}, "read": true, "flagged": true})
	n := ce.logins()
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "subject": "devis"})
	if len(s.Messages) != 1 || s.Messages[0].Unread || !s.Messages[0].Flagged || ce.logins() != n {
		t.Fatalf("after mark: %+v", s.Messages)
	}
	call[moveOut](t, ce.cs, "move", map[string]any{"ids": []string{s.Messages[0].ID}, "to": "archive"})
	n = ce.logins()
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso"})
	if len(s.Messages) != 1 || s.Messages[0].Subject != "Promo" || ce.logins() != n {
		t.Fatalf("after move: %+v", s.Messages)
	}
	// Sent is cached too: a sent copy shows up after the sync the send asks for.
	call[sendOut](t, ce.cs, "send_message", map[string]any{"account": "perso", "to": []string{"sam@example.org"}, "subject": "Hi", "body": "Hello"})
	ce.change(t, "perso", "Sent")
	n = ce.logins()
	s = call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso", "folder": "sent"})
	if len(s.Messages) != 1 || s.Messages[0].Subject != "Hi" || ce.logins() != n {
		t.Fatalf("sent from cache: %+v", s.Messages)
	}
}

func TestBodyLRU(t *testing.T) {
	l := newBodyLRU(3 * (&bodyItem{"k1", body{text: strings.Repeat("x", 100)}}).cost())
	for _, k := range []string{"k1", "k2", "k3"} {
		l.put(k, body{text: strings.Repeat("x", 100)})
	}
	l.get("k1") // k2 is now the least recently used
	l.put("k4", body{text: strings.Repeat("x", 100)})
	if l.has("k2") || !l.has("k1") || !l.has("k3") || !l.has("k4") {
		t.Errorf("eviction: k1 %v k2 %v k3 %v k4 %v", l.has("k1"), l.has("k2"), l.has("k3"), l.has("k4"))
	}
	l.put("big", body{text: strings.Repeat("x", 1000)})
	if l.has("big") || l.used() > l.max {
		t.Errorf("a body larger than the cap was kept: %d/%d", l.used(), l.max)
	}
	l.drop("k1")
	if l.has("k1") || l.used() != 2*(&bodyItem{"k1", body{text: strings.Repeat("x", 100)}}).cost() {
		t.Errorf("drop: %d", l.used())
	}
}

func TestCacheOptionsFromEnv(t *testing.T) {
	t.Setenv("MAIL_CACHE_DAYS", "7")
	t.Setenv("MAIL_CACHE_BODY_MB", "10")
	t.Setenv("MAIL_CACHE_FOLDERS", "inbox, Archive")
	t.Setenv("MAIL_CACHE_POLL", "5m")
	o, ok, err := cacheOptionsFromEnv()
	if err != nil || !ok || o.Days != 7 || o.BodyBytes != 10<<20 || o.Poll != 5*time.Minute ||
		strings.Join(o.Folders, "|") != "inbox|Archive" || o.MaxMessages != 2000 || o.NoIdle {
		t.Fatalf("%+v %v %v", o, ok, err)
	}
	t.Setenv("MAIL_CACHE_POLL", "1s")
	if _, _, err := cacheOptionsFromEnv(); err == nil {
		t.Error("a 1s poll was accepted")
	}
	t.Setenv("MAIL_CACHE", "off")
	if _, ok, _ := cacheOptionsFromEnv(); ok {
		t.Error("MAIL_CACHE=off ignored")
	}
}

// Dates are shown in the zone of the process (TZ), with the offset. The zone
// is read once at start, so the check runs in a child process.
func TestDatesInLocalZone(t *testing.T) {
	if os.Getenv("ERIPHE_TZ_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDatesInLocalZone$")
		cmd.Env = append(os.Environ(), "ERIPHE_TZ_CHILD=1", "TZ=Europe/Paris")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, b)
		}
		return
	}
	e := newEnv(t, rev2)
	cs := connect(t, e.cfg)
	s := call[searchOut](t, cs, "search_messages", map[string]any{"account": "perso", "subject": "devis"})
	// Sent at 10:00 +02:00: summer time in Paris.
	if s.Timezone != "Europe/Paris" || s.Messages[0].Date != "2026-10-19T10:00:00+02:00" {
		t.Fatalf("zone %q, date %q", s.Timezone, s.Messages[0].Date)
	}
	m := call[Message](t, cs, "read_message", map[string]any{"id": s.Messages[0].ID})
	if m.Timezone != "Europe/Paris" || m.Date != "2026-10-19T10:00:00+02:00" {
		t.Fatalf("read: zone %q, date %q", m.Timezone, m.Date)
	}
}

func TestCacheStaleGoesLive(t *testing.T) {
	ce := connectCached(t, newEnv(t, rev2), testCacheOptions())
	mc := ce.m.accounts[0].cache
	mc.mu.Lock()
	mc.syncedAt = time.Now().Add(-3 * time.Hour) // the connection has been down
	mc.mu.Unlock()
	n := ce.logins()
	s := call[searchOut](t, ce.cs, "search_messages", map[string]any{"account": "perso"})
	if len(s.Messages) != 2 || ce.logins() == n {
		t.Fatalf("stale cache answered: %+v", s.Messages)
	}
}

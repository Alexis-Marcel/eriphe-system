package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func readReceived(t *testing.T, cs *mcp.ClientSession) []receivedMail {
	t.Helper()
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: receivedURI})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []receivedMail `json:"messages"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Messages
}

// A message that lands in the INBOX after the first sync is listed in the
// resource mail://received; the first sync's messages are not.
func TestReceivedMail(t *testing.T) {
	e := newEnv(t, rev2)
	m, err := NewMailer(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan MailboxChange, 100)
	m.OnChange = func(c MailboxChange) { changes <- c }
	server := newServer(m, "test") // before the cache, as main does
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.StartCache(ctx, testCacheOptions())
	for _, a := range m.accounts {
		select {
		case <-a.cache.ready:
		case <-time.After(10 * time.Second):
			t.Fatal("cache not warm")
		}
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	if got := readReceived(t, cs); len(got) != 0 {
		t.Fatalf("before any new mail: %+v", got)
	}
	deliver(t, e.alice, "INBOX", samMail)
	var got []receivedMail
	waitFor(t, "the message", func() bool { got = readReceived(t, cs); return len(got) == 1 })
	if r := got[0]; r.Account != "perso" || r.Subject != "Lunch" || r.From == "" || r.ID == "" {
		t.Fatalf("message %+v", r)
	}
}

// Only the INBOX counts, a message received long ago is not news, and the
// list keeps its last messages.
func TestReceivedLog(t *testing.T) {
	l := &receivedLog{}
	now := time.Now()
	l.mailChanged(MailboxChange{Account: "perso", Folder: "Sent", Added: []Added{{Summary{ID: "1"}, now}}}, now)
	l.mailChanged(MailboxChange{Account: "perso", Folder: "INBOX", Added: []Added{
		{Summary{ID: "2", Subject: "old"}, now.Add(-48 * time.Hour)}, {Summary{ID: "3", Subject: "new"}, now}}}, now)
	if len(l.mails) != 1 || l.mails[0].ID != "3" {
		t.Fatalf("%+v", l.mails)
	}
	for i := 0; i < receivedKept+5; i++ {
		l.add(receivedMail{ID: "x"})
	}
	if len(l.mails) != receivedKept || l.mails[0].ID != "x" {
		t.Fatalf("kept %d, first %+v", len(l.mails), l.mails[0])
	}
}

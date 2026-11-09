package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func readEvents(t *testing.T, cs *mcp.ClientSession) (epoch string, events []serverEvent) {
	t.Helper()
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: eventsURI})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Epoch  string        `json:"epoch"`
		Events []serverEvent `json:"events"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Epoch, doc.Events
}

// A message that lands in the INBOX after the first sync is a
// mail.received event of the resource eriphe://events; the first sync's
// messages are not.
func TestEventsOfNewMail(t *testing.T) {
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

	epoch, evs := readEvents(t, cs)
	if epoch == "" || len(evs) != 0 {
		t.Fatalf("before any new mail: %q %+v", epoch, evs)
	}
	deliver(t, e.alice, "INBOX", samMail)
	waitFor(t, "the event", func() bool { _, evs = readEvents(t, cs); return len(evs) == 1 })
	ev := evs[0]
	if ev.Seq != 1 || ev.Kind != "mail.received" || ev.Key != "perso" || ev.Payload["subject"] != "Lunch" ||
		ev.Payload["from"] == "" || ev.Payload["id"] == "" {
		t.Fatalf("event %+v", ev)
	}
	if again, _ := readEvents(t, cs); again != epoch {
		t.Fatal("the epoch changed within a run")
	}
}

// Only the INBOX counts, a message received long ago is not news, and the
// log keeps its last events.
func TestEventLog(t *testing.T) {
	l := newEventLog()
	now := time.Now()
	l.mailChanged(MailboxChange{Account: "perso", Folder: "Sent", Added: []Added{{Summary{ID: "1"}, now}}}, now)
	l.mailChanged(MailboxChange{Account: "perso", Folder: "INBOX", Added: []Added{
		{Summary{ID: "2", Subject: "old"}, now.Add(-48 * time.Hour)}, {Summary{ID: "3", Subject: "new"}, now}}}, now)
	if len(l.events) != 1 || l.events[0].Payload["id"] != "3" {
		t.Fatalf("%+v", l.events)
	}
	for i := 0; i < eventsKept+5; i++ {
		l.add("mail.received", "perso", now, nil)
	}
	if len(l.events) != eventsKept || l.events[0].Seq != 7 || l.events[eventsKept-1].Seq != int64(eventsKept+6) {
		t.Fatalf("kept %d, from %d to %d", len(l.events), l.events[0].Seq, l.events[len(l.events)-1].Seq)
	}
}

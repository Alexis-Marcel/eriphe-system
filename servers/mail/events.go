package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The server's events, for eriphe-core's event journal: each message that
// lands in an account's INBOX after the cache's first sync is a
// mail.received event. They are offered as the resource eriphe://events,
// which eriphe-core reads (core/event_sources.go):
//
//	{"epoch": "…", "events": [{"seq": 1, "kind": "mail.received", "key": "<account>",
//	  "at": "…", "payload": {"account", "id", "from", "subject", "date"}}]}
//
// The last eventsKept events, oldest first; seq grows within an epoch, a new
// epoch at each start. Nothing is sent without the cache: it is what sees
// new messages (IDLE, or its poll).

const (
	eventsURI  = "eriphe://events"
	eventsKept = 200
	eventsMax  = 24 * time.Hour // a message received longer ago than this is not news
)

type serverEvent struct {
	Seq     int64          `json:"seq"`
	Kind    string         `json:"kind"`
	Key     string         `json:"key"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload"`
}

type eventLog struct {
	mu     sync.Mutex
	epoch  string
	seq    int64
	events []serverEvent
}

func newEventLog() *eventLog {
	b := make([]byte, 8)
	rand.Read(b)
	return &eventLog{epoch: hex.EncodeToString(b)}
}

func (l *eventLog) add(kind, key string, at time.Time, payload map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.events = append(l.events, serverEvent{Seq: l.seq, Kind: kind, Key: key, At: at.UTC(), Payload: payload})
	if len(l.events) > eventsKept {
		l.events = append([]serverEvent{}, l.events[len(l.events)-eventsKept:]...)
	}
}

func (l *eventLog) doc() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return json.Marshal(map[string]any{"epoch": l.epoch, "events": append([]serverEvent{}, l.events...)})
}

// mailChanged turns a cache sync's new INBOX messages into events.
func (l *eventLog) mailChanged(ch MailboxChange, now time.Time) {
	if !strings.EqualFold(ch.Folder, "INBOX") {
		return
	}
	for _, s := range ch.Added {
		if !s.Received.IsZero() && now.Sub(s.Received) > eventsMax {
			continue // an old message moved back to the inbox
		}
		l.add("mail.received", ch.Account, now, map[string]any{
			"account": s.Account, "id": s.ID, "from": s.From, "subject": s.Subject, "date": s.Date})
	}
}

// addEvents offers the log as the resource eriphe://events, fed by the
// cache's changes (before any OnChange set earlier).
func addEvents(s *mcp.Server, m *Mailer, l *eventLog) {
	prev := m.OnChange
	m.OnChange = func(ch MailboxChange) {
		l.mailChanged(ch, time.Now())
		if prev != nil {
			prev(ch)
		}
	}
	s.AddResource(&mcp.Resource{URI: eventsURI, Name: "events", MIMEType: "application/json",
		Description: "The last messages received in each account's INBOX, as events for eriphe-core."},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			raw, err := l.doc()
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: eventsURI, MIMEType: "application/json", Text: string(raw)}}}, nil
		})
}

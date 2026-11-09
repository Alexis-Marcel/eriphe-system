package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func eventsOf(t *testing.T, c *core, kind string) []journalEvent {
	t.Helper()
	all, err := c.eventsSince(time.Unix(0, 0), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []journalEvent
	for _, e := range all {
		if kind == "" || e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// emit checks the kind, cuts the payload, hands the event to the
// subscribers and keeps it; since names it, quoting an external payload
// only on request; the purge drops the old ones.
func TestEventJournal(t *testing.T) {
	c, _, now := pollCore(t)
	var heard []journalEvent
	c.subscribeEvents(func(e journalEvent) { heard = append(heard, e) })

	if _, err := c.emit(journalEvent{Kind: "Mail Received", Source: "x"}); err == nil {
		t.Error("a bad kind accepted")
	}
	long := strings.Repeat("é", 2000)
	e, err := c.emit(journalEvent{Kind: "mail.received", Key: "perso", Source: "server:mail", External: true, At: *now,
		Payload: map[string]any{"subject": "Rendez-vous le 8", "body": long, "a": long, "b": long, "c": long, "d": long, "e": long, "f": long, "g": long}})
	if err != nil || e.ID == 0 {
		t.Fatal(e, err)
	}
	if len(heard) != 1 || heard[0].ID != e.ID {
		t.Fatalf("subscribers heard %+v", heard)
	}
	raw, _ := json.Marshal(e.Payload)
	if len(raw) > eventPayloadMax || e.Payload["truncated"] != true {
		t.Fatalf("payload of %d bytes: %v", len(raw), e.Payload["truncated"])
	}
	if s, _ := e.Payload["a"].(string); len([]rune(s)) > eventStringMax+1 {
		t.Fatalf("a string of %d runes kept", len([]rune(s)))
	}
	c.emit(journalEvent{Kind: evTaskEnded, Key: "t-1", Source: "task", At: *now, Payload: map[string]any{"state": "succeeded"}})

	out, err := c.brainSince(sinceIn{T: now.Add(-time.Hour).Format(time.RFC3339), Kinds: []string{feedEvent}})
	if err != nil {
		t.Fatal(err)
	}
	items := out["items"].([]happeningView)
	if len(items) != 2 || items[0].Trust != feedExternal || strings.Contains(items[0].Summary, "éé") ||
		!strings.Contains(items[0].Summary, "mail.received") || !strings.Contains(items[1].Summary, "succeeded") {
		t.Fatalf("since: %+v", items)
	}
	if len(c.turnTaint()) != 0 {
		t.Fatal("naming an external event tainted the turn")
	}
	out, _ = c.brainSince(sinceIn{T: now.Add(-time.Hour).Format(time.RFC3339), Kinds: []string{feedEvent}, IncludeExternal: true})
	if items := out["items"].([]happeningView); !strings.Contains(items[0].Summary, "éé") || len(c.turnTaint()) == 0 {
		t.Fatalf("include_external: %+v, taint %v", items, c.turnTaint())
	}

	c.purgeEvents(now.Add(eventKeep + time.Hour))
	if got := eventsOf(t, c, ""); len(got) != 0 {
		t.Fatalf("after the purge: %+v", got)
	}
}

// A source past its hourly budget has its events dropped.
func TestEventBudget(t *testing.T) {
	c, _, now := pollCore(t)
	for i := 0; i < eventPerSourceHr; i++ {
		if _, err := c.emit(journalEvent{Kind: "x.y", Source: "noisy", At: *now}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := c.emit(journalEvent{Kind: "x.y", Source: "noisy", At: *now}); err == nil {
		t.Fatal("past the budget, kept")
	}
	if _, err := c.emit(journalEvent{Kind: "x.y", Source: "quiet", At: *now}); err != nil {
		t.Fatal("another source dropped:", err)
	}
	if _, err := c.emit(journalEvent{Kind: "x.y", Source: "noisy", At: now.Add(time.Hour)}); err != nil {
		t.Fatal("the next hour dropped:", err)
	}
}

// An outil rule's confirmed transitions are tool events, external unless
// the tool's server is trusted.
func TestPollEvents(t *testing.T) {
	c, mon, now := pollCore(t)
	pollWatch(t, c, &pollCond{Kind: condCompare, Path: "data.state", Op: "ne", Value: "up"})
	ctx := context.Background()
	mon.set("down", "2.0.1")
	c.ruleTick(ctx)
	if got := eventsOf(t, c, ""); len(got) != 0 {
		t.Fatalf("an unconfirmed transition emitted: %+v", got)
	}
	*now = now.Add(pollConfirm)
	c.ruleTick(ctx)
	got := eventsOf(t, c, evToolAlert)
	if len(got) != 1 || !got[0].External || got[0].Key != "Le site" || !strings.Contains(got[0].Payload["result"].(string), "down") {
		t.Fatalf("tool.alert: %+v", got)
	}
	c.setServerTrust("monitor", true, trustByUser)
	mon.set("up", "2.0.1")
	*now = now.Add(time.Minute)
	c.ruleTick(ctx)
	*now = now.Add(pollConfirm)
	c.ruleTick(ctx)
	if got := eventsOf(t, c, evToolRecovered); len(got) != 1 || got[0].External {
		t.Fatalf("tool.recovered on a trusted tool: %+v", got)
	}
}

// An alerte rule's webhook calls are alert events, resolved when the body
// says so; always external.
func TestAlertEvents(t *testing.T) {
	c, _, _ := pollCore(t)
	seq := ownerSays(c, "Préviens-moi quand Grafana envoie une alerte")
	out, err := c.brainEventRule(ruleEventIn{Name: "getout", Source: "alert", Prompt: "Regarde", SourceRef: ref(seq),
		Urgency: urgencyNormal, NoAction: true})
	if err != nil {
		t.Fatal(err)
	}
	token, _, _ := c.newWatchHook(out["rule"].(eventRuleView).ID)
	if r := c.receiveAlert(token, http.Header{}, []byte(`{"status": "firing", "title": "5xx > 2%"}`)); r.Status != http.StatusAccepted {
		t.Fatal(r)
	}
	c.receiveAlert(token, http.Header{}, []byte(`{"status": "resolved"}`))
	rec, res := eventsOf(t, c, evAlertReceived), eventsOf(t, c, evAlertResolved)
	if len(rec) != 1 || len(res) != 1 || rec[0].Key != "getout" || !rec[0].External ||
		!strings.Contains(rec[0].Payload["detail"].(string), "5xx") {
		t.Fatalf("received %+v resolved %+v", rec, res)
	}
}

// A worker task's end is task.ended.
func TestTaskEndedEvent(t *testing.T) {
	c, _, _ := pollCore(t)
	c.store.insertTask(&TaskRow{ID: "t-w", Kind: "worker", Runtime: "claude-code", State: stateRunning, Prompt: "x"})
	c.taskEnded("t-w", "succeeded")
	c.taskEnded("t-brain", "failed") // not a worker
	if got := eventsOf(t, c, evTaskEnded); len(got) != 1 || got[0].Key != "t-w" || got[0].Payload["state"] != "succeeded" {
		t.Fatalf("%+v", got)
	}
}

// The user's return after an absence is emitted once.
func TestUserReturnedEvent(t *testing.T) {
	c, _, now := pollCore(t)
	say := func(when time.Time) {
		seq := ownerSays(c, "salut")
		c.store.db.Exec(`UPDATE messages SET time = ? WHERE seq = ?`, when.Unix(), seq)
	}
	say(now.Add(-5 * time.Hour))
	say(now.Add(-2 * time.Minute))
	c.tickReturned(*now)
	c.tickReturned(now.Add(time.Minute))
	got := eventsOf(t, c, evUserReturned)
	if len(got) != 1 || got[0].Payload["away_minutes"].(float64) < 290 {
		t.Fatalf("%+v", got)
	}
	// Still away: nothing.
	*now = now.Add(3 * time.Hour)
	c.tickReturned(*now)
	if got := eventsOf(t, c, evUserReturned); len(got) != 1 {
		t.Fatalf("away, yet: %+v", got)
	}
}

// Home Assistant's signals: a change of value is ha.changed; the first
// publish only records them.
func TestHAChangedEvents(t *testing.T) {
	c, _, _ := pollCore(t)
	c.emitHAChanges([]ctxSignal{{Kind: sigPlace, Value: "home"}, {Kind: sigRoom, Value: "salon"}})
	if got := eventsOf(t, c, evHAChanged); len(got) != 0 {
		t.Fatalf("first publish: %+v", got)
	}
	c.emitHAChanges([]ctxSignal{{Kind: sigPlace, Value: "away"}, {Kind: sigRoom, Value: "salon"}})
	c.emitHAChanges([]ctxSignal{{Kind: sigPlace, Value: "away"}})
	got := eventsOf(t, c, evHAChanged)
	if len(got) != 2 || got[0].Key != sigPlace || got[0].Payload["value"] != "away" || got[0].External ||
		got[1].Key != sigRoom || got[1].Payload["value"] != "" {
		t.Fatalf("%+v", got)
	}
}

// eventServer is a catalog server that offers eriphe://events.
type eventServer struct {
	doc serverEventsDoc
}

func (s *eventServer) set(epoch string, events ...string) {
	s.doc = serverEventsDoc{Epoch: epoch}
	base := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	for i, kind := range events {
		s.doc.Events = append(s.doc.Events, struct {
			Seq     int64          `json:"seq"`
			Kind    string         `json:"kind"`
			Key     string         `json:"key"`
			At      time.Time      `json:"at"`
			Payload map[string]any `json:"payload"`
		}{Seq: int64(i + 1), Kind: kind, Key: "perso", At: base.Add(time.Duration(i) * time.Minute),
			Payload: map[string]any{"subject": "n°" + string(rune('1'+i))}})
	}
}

func (s *eventServer) session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "mail", Version: "1"}, nil)
	srv.AddResource(&mcp.Resource{URI: serverEventsURI, Name: "events"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			raw, _ := json.Marshal(s.doc)
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: serverEventsURI, Text: string(raw)}}}, nil
		})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "eriphe-core"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// A server's events: its backlog skipped at first sight, then the new
// ones within its epoch, and after a restart those after the last taken;
// eriphe-core's own kinds refused; external unless the server is trusted.
func TestServerEvents(t *testing.T) {
	c, mon, _ := pollCore(t)
	ctx := context.Background()
	es := &eventServer{}
	es.set("e1", "mail.received", "mail.received")
	sess := es.session(t)
	c.watchServerEvents(ctx, "mail", sess)
	if got := eventsOf(t, c, ""); len(got) != 0 {
		t.Fatalf("the backlog was taken: %+v", got)
	}
	if !c.events.servers["mail"] {
		t.Fatal("the server's events are not followed")
	}
	es.set("e1", "mail.received", "mail.received", "mail.received", "user.returned")
	c.readServerEvents(ctx, "mail", sess)
	got := eventsOf(t, c, "")
	if len(got) != 1 || got[0].Kind != evMailReceived || got[0].Source != "server:mail" || !got[0].External ||
		got[0].Payload["subject"] != "n°3" {
		t.Fatalf("new in the epoch: %+v", got)
	}
	c.readServerEvents(ctx, "mail", sess)
	if got := eventsOf(t, c, ""); len(got) != 1 {
		t.Fatalf("read twice: %+v", got)
	}
	// Restarted: a new epoch, its seqs from 1; only what is later is new.
	es.set("e2", "mail.received", "mail.received", "mail.received", "mail.received", "mail.received")
	c.setServerTrust("mail", true, trustByUser)
	c.readServerEvents(ctx, "mail", sess)
	got = eventsOf(t, c, evMailReceived)
	if len(got) != 2 || got[1].Payload["subject"] != "n°5" || got[1].External {
		t.Fatalf("after a restart: %+v", got)
	}
	// A server without the resource is not followed.
	c.watchServerEvents(ctx, "monitor", monSession(t, mon))
	if c.events.servers["monitor"] {
		t.Fatal("a server without events is followed")
	}
}

func monSession(t *testing.T, mon *fakeMonitor) *mcp.ClientSession {
	ct, st := mcp.NewInMemoryTransports()
	if _, err := mon.server().Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "eriphe-core"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

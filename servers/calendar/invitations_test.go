package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func readInvitations(t *testing.T, cs *mcp.ClientSession) []invitation {
	t.Helper()
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: invitationsURI})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Invitations []invitation `json:"invitations"`
	}
	if len(res.Contents) != 1 || json.Unmarshal([]byte(res.Contents[0].Text), &doc) != nil || doc.Invitations == nil {
		t.Fatalf("resource: %+v", res.Contents)
	}
	return doc.Invitations
}

const (
	invitedMe  = "ORGANIZER;CN=Sam:mailto:sam@example.org\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:me@icloud.example\n"
	answeredMe = "ORGANIZER;CN=Sam:mailto:sam@example.org\nATTENDEE;PARTSTAT=ACCEPTED:mailto:me@icloud.example\n"
)

func TestInvitationsResource(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/travail/weekly.ics", vevent("weekly-1@example.org", "Marketing", tomorrowAt(15), time.Hour, invitedMe+"RRULE:FREQ=WEEKLY;COUNT=3\n"))
	e.perso.put(t, "/u/calendars/personnel/party.ics", vevent("party-1@example.org", "Fête", tomorrowAt(20), time.Hour, invitedMe))
	e.perso.put(t, "/u/calendars/personnel/done.ics", vevent("done-1@example.org", "Déjà répondu", tomorrowAt(9), time.Hour, answeredMe))
	e.perso.put(t, "/u/calendars/personnel/past.ics", vevent("past-1@example.org", "Passé", tomorrowAt(9).AddDate(0, 0, -3), time.Hour, invitedMe))
	e.perso.put(t, "/u/calendars/personnel/alone.ics", vevent("alone-1@example.org", "Seul", tomorrowAt(11), time.Hour, ""))

	// Without the cache, nothing.
	if got := readInvitations(t, e.connect(t)); len(got) != 0 {
		t.Fatalf("without the cache: %+v", got)
	}

	cs, c := e.connectCached(t)
	got := readInvitations(t, cs)
	if len(got) != 2 || got[0].Title != "Marketing" || got[1].Title != "Fête" {
		t.Fatalf("invitations: %+v", got)
	}
	mk := got[0]
	if !mk.Recurring || mk.Pending != 3 || mk.Account != "icloud" || mk.Calendar != "Travail" ||
		mk.Organizer == nil || mk.Organizer.Email != "sam@example.org" || !strings.HasPrefix(mk.ID, "inv-") ||
		mk.Start != tomorrowAt(15).Format(time.RFC3339) {
		t.Fatalf("recurring: %+v", mk)
	}
	// event is an id the tools take.
	if ev := call[Event](t, cs, "get_event", map[string]any{"id": mk.Event}); ev.Title != "Marketing" || ev.MyStatus != "needs-action" {
		t.Fatalf("event: %+v", ev)
	}

	// Answering one occurrence keeps the invitation, same id, one fewer waiting.
	call[Event](t, cs, "respond_event", map[string]any{"id": mk.Event, "response": "accept", "occurrence": mk.Start})
	c.refreshAll(context.Background())
	// Its next waiting occurrence is a week later: the party comes first now.
	after := readInvitations(t, cs)
	if len(after) != 2 || after[0].ID != got[1].ID || after[1].ID != mk.ID || after[1].Pending != 2 ||
		after[1].Start != tomorrowAt(15).Add(7*24*time.Hour).Format(time.RFC3339) {
		t.Fatalf("after one occurrence: %+v", after)
	}
	// Answering the party takes it out.
	call[Event](t, cs, "respond_event", map[string]any{"id": got[1].Event, "response": "decline"})
	c.refreshAll(context.Background())
	if after := readInvitations(t, cs); len(after) != 1 || after[0].ID != mk.ID {
		t.Fatalf("after the party: %+v", after)
	}
}

func TestInvitationsResourceListed(t *testing.T) {
	cs := newEnv(t).connect(t)
	res, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 1 || res.Resources[0].URI != invitationsURI || res.Resources[0].MIMEType != "application/json" {
		t.Fatalf("resources: %+v", res.Resources)
	}
}

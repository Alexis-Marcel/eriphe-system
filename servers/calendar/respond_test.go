package main

import (
	"strings"
	"testing"
)

// weeklyInvite is a weekly meeting Sam sent to the user (me@icloud.example)
// and Alex, with one occurrence moved.
const weeklyInvite = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:weekly-1@example.org
DTSTAMP:20261001T000000Z
DTSTART:20261027T140000Z
DTEND:20261027T150000Z
RRULE:FREQ=WEEKLY;COUNT=4
SUMMARY:Marketing
ORGANIZER;CN=Sam:mailto:sam@example.org
ATTENDEE;CN=Sam;PARTSTAT=ACCEPTED:mailto:sam@example.org
ATTENDEE;CN=Me;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:ME@icloud.example
ATTENDEE;CN=Alex;ROLE=OPT-PARTICIPANT:mailto:alex@example.org
END:VEVENT
BEGIN:VEVENT
UID:weekly-1@example.org
DTSTAMP:20261001T000000Z
RECURRENCE-ID:20261103T140000Z
DTSTART:20261103T160000Z
DTEND:20261103T170000Z
SUMMARY:Marketing
ORGANIZER;CN=Sam:mailto:sam@example.org
ATTENDEE;CN=Me;PARTSTAT=NEEDS-ACTION:mailto:me@icloud.example
END:VEVENT
END:VCALENDAR
`

// mine is a meeting the user organizes.
const mine = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:mine-1@example.org
DTSTAMP:20261001T000000Z
DTSTART:20261028T090000Z
DTEND:20261028T100000Z
SUMMARY:Point
ORGANIZER:mailto:me@icloud.example
ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:sam@example.org
END:VEVENT
END:VCALENDAR
`

// others is a meeting the user is not invited to (a shared calendar).
const others = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//test//EN
BEGIN:VEVENT
UID:others-1@example.org
DTSTAMP:20261001T000000Z
DTSTART:20261029T090000Z
DTEND:20261029T100000Z
SUMMARY:Board
ORGANIZER:mailto:sam@example.org
ATTENDEE;PARTSTAT=ACCEPTED:mailto:alex@example.org
END:VEVENT
END:VCALENDAR
`

func byTitle(evs []Event, title string) []Event {
	var out []Event
	for _, e := range evs {
		if e.Title == title {
			out = append(out, e)
		}
	}
	return out
}

func TestCalDAVAttendeesAndRespond(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/travail/weekly.ics", weeklyInvite)
	e.perso.put(t, "/u/calendars/travail/mine.ics", mine)
	e.perso.put(t, "/u/calendars/travail/others.ics", others)
	cs := e.connect(t)

	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-27", "days": 14})
	mk := byTitle(list.Events, "Marketing")
	if len(mk) != 2 { // 27/10 and the moved 03/11
		t.Fatalf("marketing: %+v", mk)
	}
	first := mk[0]
	if first.Organizer == nil || first.Organizer.Email != "sam@example.org" || first.Organizer.Name != "Sam" ||
		first.MyStatus != "needs-action" || first.Count != 3 || first.Attendees != nil {
		t.Fatalf("listed invitation: %+v", first)
	}
	if p := byTitle(list.Events, "Point"); len(p) != 1 || p[0].MyStatus != "organizer" {
		t.Fatalf("own meeting: %+v", p)
	}
	if b := byTitle(list.Events, "Board"); len(b) != 1 || b[0].MyStatus != "" || b[0].Count != 1 {
		t.Fatalf("others' meeting: %+v", b)
	}

	got := call[Event](t, cs, "get_event", map[string]any{"id": first.ID})
	if len(got.Attendees) != 3 || got.Attendees[2].Email != "alex@example.org" || !got.Attendees[2].Optional ||
		got.Attendees[2].Status != "needs-action" || got.Attendees[0].Status != "accepted" {
		t.Fatalf("attendees: %+v", got.Attendees)
	}

	if msg := callErr(t, cs, "respond_event", map[string]any{"id": first.ID, "response": "maybe"}); !strings.Contains(msg, "accept, decline or tentative") {
		t.Errorf("bad response: %s", msg)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": byTitle(list.Events, "Point")[0].ID, "response": "decline"}); !strings.Contains(msg, "organizes") {
		t.Errorf("own meeting: %s", msg)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": byTitle(list.Events, "Board")[0].ID, "response": "decline"}); !strings.Contains(msg, "not among the attendees") {
		t.Errorf("not invited: %s", msg)
	}

	ans := call[Event](t, cs, "respond_event", map[string]any{"id": first.ID, "response": "decline", "comment": "Absent ce samedi", "series": true})
	if ans.MyStatus != "declined" {
		t.Fatalf("answered: %+v", ans)
	}
	raw := strings.ReplaceAll(e.perso.raw(t, "/u/calendars/travail/weekly.ics"), "\r\n ", "")
	if strings.Count(raw, "PARTSTAT=DECLINED") != 2 || strings.Contains(raw, "RSVP") ||
		!strings.Contains(raw, "COMMENT:Absent ce samedi") || !strings.Contains(raw, "CN=Sam;PARTSTAT=ACCEPTED") && !strings.Contains(raw, "PARTSTAT=ACCEPTED;CN=Sam") {
		t.Fatalf("saved:\n%s", raw)
	}
	// The organizer's own data is not changed: no new SEQUENCE.
	if strings.Contains(raw, "SEQUENCE") {
		t.Errorf("an answer must not bump the sequence:\n%s", raw)
	}
	list = call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-27", "days": 14})
	for _, ev := range byTitle(list.Events, "Marketing") {
		if ev.MyStatus != "declined" {
			t.Errorf("after the answer: %+v", ev)
		}
	}
}

func TestRespondReadOnly(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/travail/weekly.ics", weeklyInvite)
	e.cfg.Accounts[0].ReadOnly = true
	cs := e.connect(t)
	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-27", "days": 1})
	if len(list.Events) != 1 {
		t.Fatalf("events: %+v", list.Events)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "response": "accept"}); !strings.Contains(msg, "read") {
		t.Errorf("read-only: %s", msg)
	}
	if raw := e.perso.raw(t, "/u/calendars/travail/weekly.ics"); strings.Contains(raw, "ACCEPTED:mailto:ME") {
		t.Errorf("written anyway")
	}
}

// The user found by another address of theirs.
func TestRespondByAlias(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/travail/weekly.ics", strings.ReplaceAll(weeklyInvite, "me@icloud.example", "sam.alias@example.org"))
	e.cfg.Accounts[0].Emails = []string{"Sam.Alias@example.org"}
	cs := e.connect(t)
	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-27", "days": 1})
	if len(list.Events) != 1 || list.Events[0].MyStatus != "needs-action" {
		t.Fatalf("events: %+v", list.Events)
	}
	if ans := call[Event](t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "response": "tentative", "series": true}); ans.MyStatus != "tentative" {
		t.Fatalf("answered: %+v", ans)
	}
}

// googleInvite is an invitation as Google's CalDAV gives it to the
// invited user: Google's attendee parameters, the organizer elsewhere,
// the object named by a UID with an @.
const googleInvite = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Google Inc//Google Calendar 70.9054//EN
BEGIN:VEVENT
DTSTART:20261123T131000Z
DTEND:20261123T141000Z
DTSTAMP:20261120T090000Z
ORGANIZER;CN=Sam:mailto:sam@example.org
UID:7kq3p0c2v1@google.com
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;CN=Sam;X-NUM-GUESTS=0:mailto:sam@example.org
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE;CN=me@icloud.example;X-NUM-GUESTS=0:mailto:me@icloud.example
CREATED:20261120T090000Z
LAST-MODIFIED:20261120T090000Z
SEQUENCE:0
STATUS:CONFIRMED
SUMMARY:Picnic
TRANSP:OPAQUE
END:VEVENT
END:VCALENDAR
`

// An invitation as Google's CalDAV gives it is answered like any other:
// listed as waiting, accepted, its other attendees and Google's
// parameters kept.
func TestRespondGoogleInvite(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/travail/7kq3p0c2v1@google.com.ics", googleInvite)
	cs := e.connect(t)
	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-11-23", "days": 1})
	if len(list.Events) != 1 || list.Events[0].MyStatus != "needs-action" {
		t.Fatalf("events: %+v", list.Events)
	}
	if ans := call[Event](t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "account": "icloud", "response": "accept"}); ans.MyStatus != "accepted" {
		t.Fatalf("answered: %+v", ans)
	}
	raw := strings.ReplaceAll(e.perso.raw(t, "/u/calendars/travail/7kq3p0c2v1@google.com.ics"), "\r\n ", "")
	if !strings.Contains(raw, "PARTSTAT=ACCEPTED") || strings.Count(raw, "X-NUM-GUESTS=0") != 2 || strings.Contains(raw, "NEEDS-ACTION") {
		t.Fatalf("saved:\n%s", raw)
	}
}

// What stops an answer is said in words a person acts on: no address
// set for the account, an invitation sent to an address the account does
// not know, a server that refuses the change.
func TestRespondSaysWhatIsWrong(t *testing.T) {
	e := newEnv(t)
	e.family.put(t, "/u/calendars/famille/picnic.ics", googleInvite)
	e.perso.put(t, "/u/calendars/travail/alias.ics", strings.NewReplacer("me@icloud.example", "sam.alias@example.org", "7kq3p0c2v1", "alias-1").Replace(googleInvite))
	e.perso.put(t, "/u/calendars/travail/picnic.ics", googleInvite)
	cs := e.connect(t)
	first := func(account, from string) string {
		t.Helper()
		list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": account, "from": from, "days": 1})
		if len(list.Events) == 0 {
			t.Fatalf("%s: no event", account)
		}
		return list.Events[0].ID
	}
	// The Nextcloud account's username is not an address.
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": first("cloud", "2026-11-23"), "response": "accept"}); !strings.Contains(msg, "no address is set") || !strings.Contains(msg, "emails") {
		t.Errorf("no address: %s", msg)
	}
	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-11-23", "days": 1})
	var alias, picnic string
	for _, ev := range list.Events {
		if ev.MyStatus == "" { // the user not found among its attendees
			alias = ev.ID
		} else {
			picnic = ev.ID
		}
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": alias, "response": "accept"}); !strings.Contains(msg, "me@icloud.example") || !strings.Contains(msg, "add it to the account's addresses") {
		t.Errorf("another address: %s", msg)
	}
	e.perso.mu.Lock()
	e.perso.refuse = true
	e.perso.mu.Unlock()
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": picnic, "response": "accept"}); !strings.Contains(msg, "refused") || !strings.Contains(msg, "403") {
		t.Errorf("refused: %s", msg)
	}
}

func TestGraphAttendeesAndRespond(t *testing.T) {
	e := newEnv(t)
	people := []any{
		map[string]any{"type": "required", "status": map[string]any{"response": "organizer"}, "emailAddress": map[string]any{"name": "Sam", "address": "sam@example.org"}},
		map[string]any{"type": "optional", "status": map[string]any{"response": "none"}, "emailAddress": map[string]any{"name": "Me", "address": "me@example.org"}},
		map[string]any{"type": "resource", "status": map[string]any{"response": "accepted"}, "emailAddress": map[string]any{"name": "Room 2", "address": "room2@example.org"}},
	}
	ev := func(id, typ string, start string) map[string]any {
		return map[string]any{"id": id, "subject": "Revue", "type": typ, "seriesMasterId": "AAMk-master",
			"start": map[string]any{"dateTime": start, "timeZone": "UTC"}, "end": map[string]any{"dateTime": start[:11] + "15:00:00", "timeZone": "UTC"},
			"isOrganizer": false, "organizer": map[string]any{"emailAddress": map[string]any{"name": "Sam", "address": "sam@example.org"}},
			"attendees": people, "responseStatus": map[string]any{"response": "notResponded"}}
	}
	e.graph.events["AAMk-occ1"], e.graph.calOf["AAMk-occ1"] = ev("AAMk-occ1", "occurrence", "2026-10-27T14:00:00"), "AAMk-cal/1=="
	e.graph.events["AAMk-master"], e.graph.calOf["AAMk-master"] = ev("AAMk-master", "seriesMaster", "2026-10-20T14:00:00"), "AAMk-other"
	own := ev("AAMk-own", "singleInstance", "2026-10-28T14:00:00")
	own["isOrganizer"], own["seriesMasterId"] = true, ""
	e.graph.events["AAMk-own"], e.graph.calOf["AAMk-own"] = own, "AAMk-cal/1=="
	cs := e.connect(t)

	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "work", "from": "2026-10-27", "days": 2})
	if len(list.Events) != 2 {
		t.Fatalf("events: %+v", list.Events)
	}
	occ := list.Events[0]
	if occ.MyStatus != "needs-action" || occ.Count != 2 || occ.Organizer == nil || occ.Organizer.Email != "sam@example.org" || occ.Attendees != nil {
		t.Fatalf("occurrence: %+v", occ)
	}
	if list.Events[1].MyStatus != "organizer" {
		t.Fatalf("own: %+v", list.Events[1])
	}
	got := call[Event](t, cs, "get_event", map[string]any{"id": occ.ID})
	if len(got.Attendees) != 2 || !got.Attendees[1].Optional || got.Attendees[0].Status != "organizer" {
		t.Fatalf("attendees: %+v", got.Attendees)
	}

	if msg := callErr(t, cs, "respond_event", map[string]any{"id": list.Events[1].ID, "response": "decline"}); !strings.Contains(msg, "organizes") {
		t.Errorf("own: %s", msg)
	}
	ans := call[Event](t, cs, "respond_event", map[string]any{"id": occ.ID, "response": "decline", "comment": "Pas dispo", "series": true})
	if ans.MyStatus != "declined" {
		t.Fatalf("answered: %+v", ans)
	}
	// Answered through the series, the reply sent.
	if strings.Join(e.graph.answers, ";") != "AAMk-master decline Pas dispo true" {
		t.Fatalf("answers: %v", e.graph.answers)
	}
}

func TestCalDAVRespondOccurrence(t *testing.T) {
	e := newEnv(t)
	e.perso.put(t, "/u/calendars/travail/weekly.ics", weeklyInvite)
	cs := e.connect(t)
	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-27", "days": 21})
	mk := byTitle(list.Events, "Marketing")
	if len(mk) != 3 {
		t.Fatalf("marketing: %+v", mk)
	}
	// Recurring: nothing is guessed.
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": mk[0].ID, "response": "decline"}); !strings.Contains(msg, "give occurrence") {
		t.Errorf("no scope: %s", msg)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": mk[0].ID, "response": "decline", "occurrence": mk[0].Start, "series": true}); !strings.Contains(msg, "not both") {
		t.Errorf("both: %s", msg)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": mk[0].ID, "response": "decline", "occurrence": "2026-10-28T15:00"}); !strings.Contains(msg, "no occurrence") {
		t.Errorf("wrong date: %s", msg)
	}

	// One occurrence of the series: a new override, the series untouched.
	ans := call[Event](t, cs, "respond_event", map[string]any{"id": mk[2].ID, "response": "decline", "occurrence": mk[2].Start, "comment": "Absent"})
	if ans.MyStatus != "declined" || ans.Start != "2026-11-10T15:00:00+01:00" {
		t.Fatalf("answered: %+v", ans)
	}
	raw := strings.ReplaceAll(e.perso.raw(t, "/u/calendars/travail/weekly.ics"), "\r\n ", "")
	if strings.Count(raw, "BEGIN:VEVENT") != 3 || strings.Count(raw, "PARTSTAT=DECLINED") != 1 ||
		!strings.Contains(raw, "RECURRENCE-ID:20261110T140000Z") || strings.Count(raw, "RRULE") != 1 {
		t.Fatalf("saved:\n%s", raw)
	}

	// The moved occurrence, by the start list_events shows: its override.
	call[Event](t, cs, "respond_event", map[string]any{"id": mk[1].ID, "response": "tentative", "occurrence": mk[1].Start})
	raw = strings.ReplaceAll(e.perso.raw(t, "/u/calendars/travail/weekly.ics"), "\r\n ", "")
	if strings.Count(raw, "BEGIN:VEVENT") != 3 || strings.Count(raw, "PARTSTAT=TENTATIVE") != 1 {
		t.Fatalf("moved:\n%s", raw)
	}

	got := map[string]string{}
	for _, ev := range byTitle(call[listEventsOut](t, cs, "list_events", map[string]any{"account": "icloud", "from": "2026-10-27", "days": 21}).Events, "Marketing") {
		got[ev.Start[:10]] = ev.MyStatus
	}
	if got["2026-10-27"] != "needs-action" || got["2026-11-03"] != "tentative" || got["2026-11-10"] != "declined" || len(got) != 3 {
		t.Fatalf("after: %v", got)
	}
}

func TestGraphRespondOccurrence(t *testing.T) {
	e := newEnv(t)
	people := []any{
		map[string]any{"type": "required", "status": map[string]any{"response": "none"}, "emailAddress": map[string]any{"name": "Me", "address": "me@example.org"}},
	}
	ev := func(id, typ, start, orig string) map[string]any {
		return map[string]any{"id": id, "subject": "Revue", "type": typ, "seriesMasterId": "AAMk-master", "originalStart": orig,
			"start": map[string]any{"dateTime": start, "timeZone": "UTC"}, "end": map[string]any{"dateTime": start[:11] + "18:00:00", "timeZone": "UTC"},
			"isOrganizer": false, "organizer": map[string]any{"emailAddress": map[string]any{"name": "Sam", "address": "sam@example.org"}},
			"attendees": people, "responseStatus": map[string]any{"response": "notResponded"}}
	}
	for _, x := range []map[string]any{
		ev("AAMk-occ1", "occurrence", "2026-10-27T14:00:00", "2026-10-27T14:00:00Z"),
		ev("AAMk-occ2", "exception", "2026-11-03T16:00:00", "2026-11-03T14:00:00Z"), // moved
		ev("AAMk-master", "seriesMaster", "2026-10-20T14:00:00", ""),
	} {
		id := x["id"].(string)
		e.graph.events[id], e.graph.calOf[id] = x, "AAMk-cal/1=="
	}
	e.graph.events["AAMk-master"]["seriesMasterId"] = ""
	e.graph.calOf["AAMk-master"] = "AAMk-other"
	cs := e.connect(t)
	list := call[listEventsOut](t, cs, "list_events", map[string]any{"account": "work", "from": "2026-10-27", "days": 14})
	if len(list.Events) != 2 {
		t.Fatalf("events: %+v", list.Events)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "response": "decline"}); !strings.Contains(msg, "give occurrence") {
		t.Errorf("no scope: %s", msg)
	}
	// The id is that occurrence: answered on it, not on the series.
	call[Event](t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "response": "decline", "occurrence": list.Events[0].Start})
	// Another occurrence through this id: the moved one, by its original start.
	call[Event](t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "response": "accept", "occurrence": "2026-11-03T15:00"})
	if strings.Join(e.graph.answers, ";") != "AAMk-occ1 decline  true;AAMk-occ2 accept  true" {
		t.Fatalf("answers: %v", e.graph.answers)
	}
	if msg := callErr(t, cs, "respond_event", map[string]any{"id": list.Events[0].ID, "response": "accept", "occurrence": "2026-11-04T15:00"}); !strings.Contains(msg, "no occurrence") {
		t.Errorf("wrong date: %s", msg)
	}
}

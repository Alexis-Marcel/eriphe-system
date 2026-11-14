package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Provider is a calendar backend: CalDAV (iCloud, Google, Fastmail,
// Nextcloud...) or Microsoft Graph. Times are absolute instants; all-day
// events carry their dates at midnight in the configured time zone, with an
// exclusive end.
type Provider interface {
	Calendars(ctx context.Context) ([]CalendarInfo, error)
	// Events returns the events of a calendar overlapping [from, to), with
	// recurring events expanded into occurrences.
	Events(ctx context.Context, cal CalendarInfo, from, to time.Time) ([]Item, error)
	Get(ctx context.Context, calID, eventID string) (*Item, error)
	Create(ctx context.Context, calID string, in EventInput) (*Item, error)
	Update(ctx context.Context, calID, eventID string, p EventPatch) (*Item, error)
	Delete(ctx context.Context, calID, eventID string) error
	// Respond answers an invitation as the user: the organizer is told.
	// A recurring event is answered for the whole series, or for one
	// occurrence when r.Occurrence is set.
	Respond(ctx context.Context, calID, eventID string, r Response) (*Item, error)
}

// Response is the user's answer to an invitation.
type Response struct {
	Status  string // accepted, declined or tentative
	Comment string // a word for the organizer; may be empty
	// Occurrence is the start of the one occurrence answered, as
	// list_events shows it (a moved one: its new start); nil answers the
	// whole event, every occurrence of a recurring one.
	Occurrence *time.Time
}

// Person is someone of an event: its organizer or an attendee.
type Person struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Attendee is someone invited, with their answer: accepted, declined,
// tentative, needs-action (not answered yet) or delegated.
type Attendee struct {
	Person
	Status   string `json:"status,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type CalendarInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Item is an event as a provider returns it.
type Item struct {
	ID          string // provider id: CalDAV object path, Graph event id
	Series      string // what its occurrences share: the CalDAV object, Graph's series master (else its id)
	UID         string // iCalendar UID: the same in every calendar the event was sent to
	CalendarID  string
	Title       string
	Start, End  time.Time
	AllDay      bool
	Location    string
	Description string
	Recurring   bool
	Free        bool // transparent / "show as free": does not block time
	Cancelled   bool

	// The people, only for an event with attendees (a meeting).
	Organizer     *Person
	Attendees     []Attendee // nil in lists: see light
	AttendeeCount int
	MyStatus      string // the user's own answer (an Attendee status), or organizer
}

// light drops the attendees of listed events, keeping their count: a
// meeting can have hundreds, and get_event gives them.
func light(items []Item) []Item {
	for i := range items {
		items[i].Attendees = nil
	}
	return items
}

// meeting fills an item's people: count and, from the user's addresses,
// their own status. Without attendees it is not a meeting: nothing is kept.
func (it *Item) meeting(me []string, organizer *Person, attendees []Attendee) {
	if len(attendees) == 0 {
		return
	}
	it.Organizer, it.Attendees, it.AttendeeCount = organizer, attendees, len(attendees)
	if organizer != nil && isMe(me, organizer.Email) {
		it.MyStatus = "organizer"
		return
	}
	for _, a := range attendees {
		if isMe(me, a.Email) {
			it.MyStatus = a.Status
			return
		}
	}
}

func isMe(me []string, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, m := range me {
		if email != "" && m == email {
			return true
		}
	}
	return false
}

type EventInput struct {
	Title       string
	Start, End  time.Time
	AllDay      bool
	Location    string
	Description string
}

// EventPatch changes only the fields that are set.
type EventPatch struct {
	Title, Location, Description *string
	Start, End                   *time.Time
	AllDay                       *bool
}

var errNotFound = errors.New("event not found")

// eventRef is the opaque id the tools hand out: account, calendar and the
// provider's own id.
type eventRef struct{ Account, Calendar, Event string }

const refSep = "\x1f"

func (r eventRef) String() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join([]string{"e1", r.Account, r.Calendar, r.Event}, refSep)))
}

func parseEventRef(s string) (eventRef, error) {
	bad := errors.New("invalid event id: use an id returned by list_events or create_event")
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return eventRef{}, bad
	}
	p := strings.Split(string(b), refSep)
	if len(p) != 4 || p[0] != "e1" || p[3] == "" {
		return eventRef{}, bad
	}
	return eventRef{p[1], p[2], p[3]}, nil
}

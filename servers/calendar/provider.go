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
}

type CalendarInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Item is an event as a provider returns it.
type Item struct {
	ID          string // provider id: CalDAV object path, Graph event id
	CalendarID  string
	Title       string
	Start, End  time.Time
	AllDay      bool
	Location    string
	Description string
	Recurring   bool
	Free        bool // transparent / "show as free": does not block time
	Cancelled   bool
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

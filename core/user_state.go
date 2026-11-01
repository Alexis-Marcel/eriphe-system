package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// The user's state, at the head of each of the brain's turns beside the
// time ([maintenant …]): present or away and since when, busy until when,
// the quiet hours now or next, and the "now" notifications left today. The
// code reads it from what eriphe-core records (the activity signals of
// notify_quiet.go, the rules, the cap): the model neither guesses it nor
// has to ask for it.

const (
	// awayAfter: no activity for this long, and the user is away; a gap
	// this long between two activities is an absence (since("away")).
	awayAfter = time.Hour
	// activityLookback is how far back the activity is read.
	activityLookback = 30 * 24 * time.Hour
)

// activityTimes are the user's activities before now within the lookback,
// oldest first: their messages, a tab in front of them, their reactions.
func (c *core) activityTimes(now time.Time) []time.Time {
	var out []time.Time
	for _, src := range activitySources {
		got, err := src(c, now.Add(-activityLookback))
		if err != nil {
			log.Printf("notify: activity: %v", err)
			continue
		}
		for _, s := range got {
			if !s.Unwelcome && !s.At.After(now) {
				out = append(out, s.At)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// absence is the user's last time away: from their last activity before
// it (Left) to their first one after (Back, zero while they are still away).
type absence struct {
	Left, Back time.Time
}

// lastAbsence is the user's last absence before now: the last gap of
// awayAfter or more in their activity, or the one going on. False when they
// were never seen, or never away within the lookback.
func (c *core) lastAbsence(now time.Time) (absence, bool) {
	return lastAbsenceIn(c.activityTimes(now), now)
}

func lastAbsenceIn(ts []time.Time, now time.Time) (absence, bool) {
	if len(ts) == 0 {
		return absence{}, false
	}
	if last := ts[len(ts)-1]; now.Sub(last) >= awayAfter {
		return absence{Left: last}, true
	}
	for i := len(ts) - 1; i > 0; i-- {
		if ts[i].Sub(ts[i-1]) >= awayAfter {
			return absence{Left: ts[i-1], Back: ts[i]}, true
		}
	}
	return absence{}, false
}

// busySource tells until when the user is busy at now, from times only,
// never an event's title. None is wired yet: reading a connected calendar
// on every turn would cost a call to its server each time, and its events
// are external content, which would taint every turn. A source that keeps
// free/busy times on its own (synced, not read in the turn) joins here.
type busySource func(c *core, now time.Time) (until time.Time, ok bool)

var busySources []busySource

// userState is what the state line says.
type userState struct {
	Now      time.Time
	Seen     bool      // any activity within the lookback
	Last     time.Time // the last activity
	Away     bool      // no activity for awayAfter
	Absence  absence   // the last absence, if Absent
	Absent   bool
	Busy     time.Time // busy until, from a busySource
	InQuiet  bool
	Quiet    notifyRule // in force now, or the next one
	QuietOn  time.Time  // when that one ends (InQuiet) or starts
	Left     int        // "now" notifications left today
	Cap      int
	Returned bool // back from an absence within the last awayAfter
}

func (c *core) userState() (userState, error) {
	cx, err := c.notifyContext()
	if err != nil {
		return userState{}, err
	}
	now := cx.Now
	ts := c.activityTimes(now)
	s := userState{Now: now, Cap: cx.Cap, Left: max(cx.Cap-cx.SentNow, 0)}
	if len(ts) > 0 {
		s.Seen, s.Last = true, ts[len(ts)-1]
		s.Away = now.Sub(s.Last) >= awayAfter
	}
	s.Absence, s.Absent = lastAbsenceIn(ts, now)
	s.Returned = s.Absent && !s.Away && now.Sub(s.Absence.Back) < awayAfter
	for _, src := range busySources {
		if until, ok := src(c, now); ok && until.After(s.Busy) {
			s.Busy = until
		}
	}
	s.Quiet, s.InQuiet = quietAt(cx.Rules, now)
	if s.InQuiet {
		s.QuietOn = clockAfter(now, s.Quiet.To)
	} else {
		s.Quiet = nextQuiet(cx.Rules, now)
		s.QuietOn = clockAfter(now, s.Quiet.From)
	}
	return s, nil
}

// nextQuiet is the next quiet window when none is in force at now.
func nextQuiet(rules []notifyRule, now time.Time) notifyRule {
	today := quietFor(rules, now.Weekday())
	if m := now.Hour()*60 + now.Minute(); today.From < today.To && m < today.From {
		return today // a window that starts after midnight, later today
	}
	return quietFor(rules, now.AddDate(0, 0, 1).Weekday())
}

// clockAfter is the first time at minute m of a day, after now.
func clockAfter(now time.Time, m int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), m/60, m%60, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// line is the state as the brain reads it, in French, on one line.
func (s userState) line(user string) string {
	var parts []string
	switch {
	case !s.Seen:
		parts = append(parts, "pas vu actif depuis 30 jours")
	case s.Away:
		parts = append(parts, fmt.Sprintf("absent depuis %s (%s)", s.at(s.Last), span(s.Now.Sub(s.Last))))
	case s.Returned:
		parts = append(parts, fmt.Sprintf("présent, de retour à %s après %s d'absence (parti à %s)",
			s.at(s.Absence.Back), span(s.Absence.Back.Sub(s.Absence.Left)), s.at(s.Absence.Left)))
	default:
		parts = append(parts, "présent")
	}
	if !s.Busy.IsZero() {
		parts = append(parts, "occupé jusqu'à "+s.at(s.Busy)+" (agenda)")
	}
	if s.InQuiet {
		parts = append(parts, fmt.Sprintf("heures calmes en cours jusqu'à %s", s.at(s.QuietOn)))
	} else {
		parts = append(parts, fmt.Sprintf("heures calmes à %s (%s)", s.at(s.QuietOn), s.Quiet.Window()))
	}
	parts = append(parts, fmt.Sprintf("notifications « maintenant » restantes aujourd'hui : %d sur %d", s.Left, s.Cap))
	return fmt.Sprintf("[état de %s : %s]", user, strings.Join(parts, " · "))
}

// at is a time of the line: its clock today, its date otherwise.
func (s userState) at(t time.Time) string {
	t = t.In(s.Now.Location())
	if y, m, d := t.Date(); y == s.Now.Year() && m == s.Now.Month() && d == s.Now.Day() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

// span words a duration: 35 min, 4 h 50, 2 j 3 h.
func span(d time.Duration) string {
	m := int(d.Minutes())
	switch {
	case m < 60:
		return fmt.Sprintf("%d min", m)
	case m < 24*60:
		if m%60 == 0 {
			return fmt.Sprintf("%d h", m/60)
		}
		return fmt.Sprintf("%d h %02d", m/60, m%60)
	}
	return fmt.Sprintf("%d j %d h", m/(24*60), m%(24*60)/60)
}

// turnHead opens each of the brain's turns: the time, then the user's state.
func (c *core) turnHead(now time.Time) string {
	head := nowLine(now)
	s, err := c.userState()
	if err != nil {
		log.Printf("brain: user state: %v", err)
		return head
	}
	return head + "\n" + s.line(c.conf().User)
}

package main

import (
	"log"
	"sort"
	"time"
)

// The quiet hours adapt, but never by themselves. eriphe-core keeps cheap
// signals of when the user is active (their messages, a workspace tab in
// front of them, their reactions to notifications) and of when they did not
// want to be told (« pas maintenant »), and suggests a window from the last
// two weeks, without a model: the typical last activity of the evening
// and first of the morning, with a margin. The brain sees the suggestion
// (the notifications tool) and, when it differs from the rule in force,
// proposes a quiet rule, which the user approves in Settings.
//
// More signals feed the same suggestion by joining activitySources: a Home
// Assistant sleep sensor, the phone's focus mode.

const presenceSchema = `CREATE TABLE IF NOT EXISTS presence_seen (at INTEGER PRIMARY KEY);`

func init() { extraSchema = append(extraSchema, presenceSchema) }

const (
	activityDays   = 14            // how far back the suggestion looks
	presenceBucket = 10 * 60       // a tab in front is recorded once per 10 minutes
	dayTurn        = 5 * time.Hour // an activity day starts at 05:00: 01:30 is the evening before
	quietMargin    = 30            // minutes after the typical last activity
	quietShift     = 60            // a suggestion this far from the rule is worth proposing
	unwelcomeNear  = 2 * 60        // « pas maintenant » this close outside a window widens it
	minWeekNights  = 5             // samples needed for the nights before a weekday
	minWeekendRuns = 3             // and before a weekend day
	presenceKeep   = 30 * 24 * 60 * 60
)

// activitySignal is a moment of the user's: active, or not wanting to be told.
type activitySignal struct {
	At        time.Time
	Unwelcome bool
}

type activitySource func(c *core, since time.Time) ([]activitySignal, error)

// activitySources feed the suggestion; a new signal is one more here.
var activitySources = []activitySource{messageActivity, presenceActivity, feedbackActivity}

// messageActivity: the user's messages, but not their home's automations.
func messageActivity(c *core, since time.Time) ([]activitySignal, error) {
	rows, err := c.store.db.Query(`SELECT time FROM messages WHERE role = 'user' AND via NOT IN ('Home Assistant', ?) AND time >= ?`,
		firstRunVia, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activitySignal
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, activitySignal{At: time.Unix(at, 0)})
	}
	return out, rows.Err()
}

// presenceActivity: a workspace tab in front of the user.
func presenceActivity(c *core, since time.Time) ([]activitySignal, error) {
	rows, err := c.store.db.Query(`SELECT at FROM presence_seen WHERE at >= ?`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activitySignal
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, activitySignal{At: time.Unix(at, 0)})
	}
	return out, rows.Err()
}

// feedbackActivity: a reaction is activity, and « pas maintenant » says
// when the notification came, the user did not want it.
func feedbackActivity(c *core, since time.Time) ([]activitySignal, error) {
	rows, err := c.store.db.Query(`SELECT created, feedback, feedback_at FROM notifications
		WHERE feedback != '' AND feedback_at >= ?`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activitySignal
	for rows.Next() {
		var created, at int64
		var fb string
		if err := rows.Scan(&created, &fb, &at); err != nil {
			return nil, err
		}
		out = append(out, activitySignal{At: time.Unix(at, 0)})
		if fb == "later" {
			out = append(out, activitySignal{At: time.Unix(created, 0), Unwelcome: true})
		}
	}
	return out, rows.Err()
}

// seenPresence records that a tab is in front of the user, once a bucket.
func (c *core) seenPresence(at time.Time) {
	b := at.Unix() / presenceBucket * presenceBucket
	if _, err := c.store.db.Exec(`INSERT OR IGNORE INTO presence_seen (at) VALUES (?)`, b); err != nil {
		log.Printf("notify: presence: %v", err)
		return
	}
	c.store.db.Exec(`DELETE FROM presence_seen WHERE at < ?`, b-presenceKeep)
}

// lastSeen is the user's last activity before now, from the same signals,
// within the last 30 days (a tab in front counts at its 10-minute bucket);
// false if none. A return after an absence (step 2's "le point à ton
// retour") is a new signal after a long gap: awaySince tells the gap.
func (c *core) lastSeen(now time.Time) (time.Time, bool) {
	var last time.Time
	for _, src := range activitySources {
		got, err := src(c, now.AddDate(0, 0, -30))
		if err != nil {
			log.Printf("notify: activity: %v", err)
			continue
		}
		for _, s := range got {
			if !s.Unwelcome && !s.At.After(now) && s.At.After(last) {
				last = s.At
			}
		}
	}
	return last, !last.IsZero()
}

// awaySince is how long the user has been away at now: since their last
// activity before it; 0 if they were never seen.
func (c *core) awaySince(now time.Time) (time.Time, time.Duration) {
	last, ok := c.lastSeen(now)
	if !ok {
		return time.Time{}, 0
	}
	return last, now.Sub(last)
}

// quietSuggestion is a window the signals suggest for some nights, and
// the rule it would replace.
type quietSuggestion struct {
	Days    string `json:"days"`
	From    string `json:"from"`
	To      string `json:"to"`
	Current string `json:"current"` // the window in force for those nights
	Nights  int    `json:"nights"`  // how many nights it is drawn from
}

// suggestQuiet reads the signals and suggests what differs from the rules.
func (c *core) suggestQuiet() []quietSuggestion {
	now := c.notifyTime()
	since := now.AddDate(0, 0, -activityDays)
	var signals []activitySignal
	for _, src := range activitySources {
		got, err := src(c, since)
		if err != nil {
			log.Printf("notify: activity: %v", err)
			continue
		}
		signals = append(signals, got...)
	}
	rules, err := c.notifyRules(ruleActive)
	if err != nil {
		return nil
	}
	return suggestQuietFrom(signals, rules, now.Location())
}

// suggestQuietFrom is the suggestion itself, pure: per kind of night, the
// median last activity of the evening before and first of the morning.
func suggestQuietFrom(signals []activitySignal, rules []notifyRule, loc *time.Location) []quietSuggestion {
	type span struct{ first, last int } // minutes after the activity day's start
	days := map[string]*span{}
	var unwelcome []time.Time
	for _, s := range signals {
		if s.Unwelcome {
			unwelcome = append(unwelcome, s.At.In(loc))
			continue
		}
		t := s.At.In(loc).Add(-dayTurn)
		key, m := t.Format(time.DateOnly), t.Hour()*60+t.Minute()
		if d := days[key]; d == nil {
			days[key] = &span{m, m}
		} else {
			d.first, d.last = min(d.first, m), max(d.last, m)
		}
	}
	turn := int(dayTurn / time.Minute)
	evenings, mornings := map[string][]int{}, map[string][]int{}
	for key, d := range days {
		day, _ := time.ParseInLocation(time.DateOnly, key, loc)
		// The morning of this day ends the night before it; the evening
		// starts the night that ends tomorrow.
		mornings[nightDays(day.Weekday())] = append(mornings[nightDays(day.Weekday())], d.first+turn)
		next := day.AddDate(0, 0, 1).Weekday()
		evenings[nightDays(next)] = append(evenings[nightDays(next)], d.last+turn)
	}
	var out []quietSuggestion
	for _, kind := range []string{daysWeekdays, daysWeekend} {
		need := minWeekNights
		if kind == daysWeekend {
			need = minWeekendRuns
		}
		ev, mo := evenings[kind], mornings[kind]
		if len(ev) < need || len(mo) < need {
			continue // too little to say
		}
		from := (roundUp30(median(ev)) + quietMargin) % 1440
		to := roundUp30(median(mo)) % 1440
		from, to = widenForUnwelcome(from, to, unwelcome, kind)
		if n := quietLength(from, to); n < quietMin || n > quietMax {
			continue
		}
		cur := quietFor(rules, sampleDay(kind))
		if clockGap(from, cur.From) < quietShift && clockGap(to, cur.To) < quietShift {
			continue // close enough to the rule in force
		}
		out = append(out, quietSuggestion{Days: kind, From: clockText(from), To: clockText(to), Current: cur.Window(),
			Nights: min(len(ev), len(mo))})
	}
	return out
}

// widenForUnwelcome moves a window's edge over the « pas maintenant » that
// came just outside it, when there were at least two on that side.
func widenForUnwelcome(from, to int, unwelcome []time.Time, kind string) (int, int) {
	var before, after []int
	for _, t := range unwelcome {
		m := t.Hour()*60 + t.Minute()
		night := t
		if m >= 12*60 {
			night = t.AddDate(0, 0, 1) // an evening belongs to the night ending tomorrow
		}
		if nightDays(night.Weekday()) != kind {
			continue
		}
		if d := quietLength(m, from); d > 0 && d <= unwelcomeNear {
			before = append(before, m)
		}
		if d := quietLength(to, m); d >= 0 && d < unwelcomeNear {
			after = append(after, m)
		}
	}
	if len(before) >= 2 {
		earliest := before[0]
		for _, m := range before {
			if quietLength(m, from) > quietLength(earliest, from) {
				earliest = m
			}
		}
		from = earliest / 30 * 30
	}
	if len(after) >= 2 {
		latest := after[0]
		for _, m := range after {
			if quietLength(to, m) > quietLength(to, latest) {
				latest = m
			}
		}
		to = roundUp30(latest+1) % 1440
	}
	return from, to
}

func median(xs []int) int {
	s := append([]int(nil), xs...)
	sort.Ints(s)
	return s[len(s)/2]
}

func roundUp30(m int) int { return (m + 29) / 30 * 30 }

// clockGap is how far apart two times of day are, around the clock.
func clockGap(a, b int) int { return min(quietLength(a, b), quietLength(b, a)) }

// sampleDay is a day whose night is of that kind.
func sampleDay(kind string) time.Weekday {
	if kind == daysWeekend {
		return time.Saturday
	}
	return time.Wednesday
}

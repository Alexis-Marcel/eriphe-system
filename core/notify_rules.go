package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The user's notification rules, as data the code applies (decideNotify):
//
//   - a topic rule: what to do with what is about a ref (project:getout →
//     now, brief or never), with "even at night", the one way through the
//     quiet hours and the daily cap;
//   - a quiet rule: the quiet window, for all nights, or for the nights
//     before a weekday or before a weekend day. Without one, the seed
//     22:00–08:00 holds: there is always a quiet window.
//
// The brain writes a rule only when the user asked for it in this very
// turn, a clean one; otherwise the rule is only proposed, and the user
// approves it in Settings, where every rule is shown, edited and deleted.

const notifyRulesSchema = `CREATE TABLE IF NOT EXISTS notify_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, match TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL DEFAULT '', night INTEGER NOT NULL DEFAULT 0, days TEXT NOT NULL DEFAULT 'all',
  quiet_from INTEGER NOT NULL DEFAULT 0, quiet_to INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL, why TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '',
  tainted TEXT NOT NULL DEFAULT '', by TEXT NOT NULL, created INTEGER NOT NULL, updated INTEGER NOT NULL);`

func init() {
	extraSchema = append(extraSchema, notifyRulesSchema)
	// Added with the channels (channels.go): where a topic's notifications go.
	extraColumns = append(extraColumns, `ALTER TABLE notify_rules ADD COLUMN channels TEXT NOT NULL DEFAULT ''`)
	// Added with the router (route.go): lines a topic adds, or forbids.
	extraColumns = append(extraColumns, `ALTER TABLE notify_rules ADD COLUMN also TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notify_rules ADD COLUMN never_on TEXT NOT NULL DEFAULT ''`)
}

const (
	ruleTopic = "topic"
	ruleQuiet = "quiet"

	ruleActive   = "active"
	ruleProposed = "proposed"

	daysAll      = "all"
	daysWeekdays = "weekdays" // the nights that end on Monday to Friday
	daysWeekend  = "weekend"  // the nights that end on Saturday or Sunday

	seedQuietFrom = 22 * 60
	seedQuietTo   = 8 * 60
	// A quiet window lasts this long at least, and at most: always one.
	quietMin = 4 * 60
	quietMax = 16 * 60
)

var (
	ruleOutcomes = []string{levelNow, levelBrief, levelNever}
	ruleDays     = []string{daysAll, daysWeekdays, daysWeekend}
	ruleRefRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._@/-]*(:[a-z0-9._@/-]+)*$`)
)

// notifyRule is one rule.
type notifyRule struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Match     string    `json:"match,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	Night     bool      `json:"night,omitempty"`
	Channels  string    `json:"channels,omitempty"` // for a topic: where it goes; the default channels when empty
	Also      string    `json:"also,omitempty"`     // for a topic: lines added to the first one (route.go)
	Never     string    `json:"never,omitempty"`    // for a topic: lines it never goes by
	Days      string    `json:"days,omitempty"`
	From      int       `json:"-"` // minutes after midnight
	To        int       `json:"-"`
	State     string    `json:"state"`
	Why       string    `json:"why,omitempty"`
	SourceRef string    `json:"source_ref,omitempty"`
	Tainted   string    `json:"tainted,omitempty"` // proposed in a turn that read this
	By        string    `json:"by"`
	Created   time.Time `json:"created"`
}

// Window is a quiet rule's hours, HH:MM–HH:MM.
func (r notifyRule) Window() string { return clockText(r.From) + "–" + clockText(r.To) }

// FromText and ToText are a quiet rule's hours for its form; the seed's
// for a topic rule.
func (r notifyRule) FromText() string { return clockText(r.clockOr(r.From, seedQuietFrom)) }
func (r notifyRule) ToText() string   { return clockText(r.clockOr(r.To, seedQuietTo)) }

func (r notifyRule) clockOr(m, seed int) int {
	if r.Kind == ruleQuiet || m != 0 {
		return m
	}
	return seed
}

func clockText(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// parseClock reads HH:MM (or H:MM, or HHhMM) as minutes after midnight.
func parseClock(s string) (int, error) {
	var h, m int
	s = strings.Replace(strings.TrimSpace(s), "h", ":", 1)
	if strings.HasSuffix(s, ":") {
		s += "00"
	}
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("%q is not a time HH:MM", s)
	}
	return h*60 + m, nil
}

// quietLength is how long a window lasts, in minutes, across midnight or not.
func quietLength(from, to int) int { return ((to-from)%1440 + 1440) % 1440 }

// check validates a rule and normalises it.
func (r *notifyRule) check() error {
	switch r.Kind {
	case ruleTopic:
		r.Match = strings.ToLower(strings.TrimSpace(r.Match))
		if r.Match == "" || len(r.Match) > 80 || !ruleRefRe.MatchString(r.Match) {
			return errors.New("match is a ref like project:getout, person:lina or source:mail")
		}
		if !containsStr(ruleOutcomes, r.Outcome) {
			return errors.New("outcome is now, brief or never")
		}
		if r.Night && r.Outcome != levelNow {
			return errors.New("night goes with the outcome now only")
		}
		r.Days, r.From, r.To = "", 0, 0
		ch, err := checkChannels(r.Channels)
		if err != nil {
			return err
		}
		r.Channels = ch
		if r.Also, err = checkSteps(r.Also); err != nil {
			return err
		}
		if r.Never, err = checkSteps(r.Never); err != nil {
			return err
		}
	case ruleQuiet:
		if r.Days == "" {
			r.Days = daysAll
		}
		if !containsStr(ruleDays, r.Days) {
			return errors.New("days is all, weekdays or weekend")
		}
		if n := quietLength(r.From, r.To); n < quietMin || n > quietMax {
			return fmt.Errorf("quiet hours last between %d and %d hours", quietMin/60, quietMax/60)
		}
		r.Match, r.Outcome, r.Night, r.Channels, r.Also, r.Never = "", "", false, "", "", ""
	default:
		return errors.New("kind is topic or quiet")
	}
	r.Why = truncate(strings.TrimSpace(r.Why), notifyWhyMax)
	return nil
}

// same tells the rule another replaces: the same topic, or the same nights.
func (r notifyRule) same(o notifyRule) bool {
	return r.Kind == o.Kind && r.Match == o.Match && r.Days == o.Days
}

const ruleCols = `id, kind, match, outcome, night, days, quiet_from, quiet_to, state, why, source_ref, tainted, by, created, channels, also, never_on`

func (c *core) notifyRules(states ...string) ([]notifyRule, error) {
	q := `SELECT ` + ruleCols + ` FROM notify_rules`
	var args []any
	if len(states) > 0 {
		q += ` WHERE state IN (?` + strings.Repeat(", ?", len(states)-1) + `)`
		for _, s := range states {
			args = append(args, s)
		}
	}
	rows, err := c.store.db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notifyRule
	for rows.Next() {
		var r notifyRule
		var created int64
		if err := rows.Scan(&r.ID, &r.Kind, &r.Match, &r.Outcome, &r.Night, &r.Days, &r.From, &r.To, &r.State, &r.Why,
			&r.SourceRef, &r.Tainted, &r.By, &created, &r.Channels, &r.Also, &r.Never); err != nil {
			return nil, err
		}
		r.Created = time.Unix(created, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *core) notifyRule(id int64) (notifyRule, error) {
	rules, err := c.notifyRules()
	if err != nil {
		return notifyRule{}, err
	}
	for _, r := range rules {
		if r.ID == id {
			return r, nil
		}
	}
	return notifyRule{}, fmt.Errorf("no rule %d", id)
}

// addNotifyRule keeps a checked rule. Active, it replaces the active rule
// it is the same as (the same topic, the same nights).
func (c *core) addNotifyRule(r notifyRule) (notifyRule, error) {
	if err := r.check(); err != nil {
		return r, err
	}
	now := c.notifyTime().Unix()
	tx, err := c.store.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	if r.State == ruleActive {
		if _, err := tx.Exec(`DELETE FROM notify_rules WHERE state = 'active' AND kind = ? AND match = ? AND days = ?`,
			r.Kind, r.Match, r.Days); err != nil {
			return r, err
		}
	}
	res, err := tx.Exec(`INSERT INTO notify_rules (kind, match, outcome, night, days, quiet_from, quiet_to, state, why, source_ref,
		tainted, by, created, updated, channels, also, never_on) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Kind, r.Match, r.Outcome, r.Night, r.Days, r.From, r.To, r.State, r.Why, r.SourceRef, r.Tainted, r.By, now, now, r.Channels, r.Also, r.Never)
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	r.Created = time.Unix(now, 0)
	return r, tx.Commit()
}

// approveNotifyRule makes a proposed rule active: the user's say.
func (c *core) approveNotifyRule(id int64) (notifyRule, error) {
	r, err := c.notifyRule(id)
	if err != nil {
		return r, err
	}
	if r.State != ruleProposed {
		return r, errors.New("not proposed")
	}
	if _, err := c.store.db.Exec(`DELETE FROM notify_rules WHERE id = ?`, id); err != nil {
		return r, err
	}
	r.State = ruleActive
	return c.addNotifyRule(r)
}

func (c *core) deleteNotifyRule(id int64) error {
	res, err := c.store.db.Exec(`DELETE FROM notify_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no rule %d", id)
	}
	return nil
}

// topicRule is the active topic rule for a ref: the longest match, a
// match being the ref itself or one of its leading parts (project:getout
// for project:getout:prod).
func topicRule(rules []notifyRule, ref string) *notifyRule {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return nil
	}
	var best *notifyRule
	for i, r := range rules {
		if r.Kind != ruleTopic || r.State != ruleActive {
			continue
		}
		if ref != r.Match && !strings.HasPrefix(ref, r.Match+":") {
			continue
		}
		if best == nil || len(r.Match) > len(best.Match) {
			best = &rules[i]
		}
	}
	return best
}

// The quiet window of a night is named by the day it ends on: the
// weekend's nights are Friday's and Saturday's. A window that does not
// cross midnight (01:00–09:00) lies in the morning of that day.

func nightDays(day time.Weekday) string {
	if day == time.Saturday || day == time.Sunday {
		return daysWeekend
	}
	return daysWeekdays
}

// quietFor is the window of the night that ends on day: the rule for those
// nights, else the rule for all nights, the newest first, else the seed.
func quietFor(rules []notifyRule, day time.Weekday) notifyRule {
	var all, own *notifyRule
	for i, r := range rules {
		if r.Kind != ruleQuiet || r.State != ruleActive {
			continue
		}
		switch r.Days {
		case daysAll:
			all = &rules[i]
		case nightDays(day):
			own = &rules[i]
		}
	}
	switch {
	case own != nil:
		return *own
	case all != nil:
		return *all
	}
	return notifyRule{Kind: ruleQuiet, Days: daysAll, From: seedQuietFrom, To: seedQuietTo, State: ruleActive, By: "seed"}
}

// quietAt tells whether now is in a quiet window, and which.
func quietAt(rules []notifyRule, now time.Time) (notifyRule, bool) {
	m := now.Hour()*60 + now.Minute()
	today := quietFor(rules, now.Weekday())
	if today.From > today.To && m < today.To || today.From < today.To && m >= today.From && m < today.To {
		return today, true
	}
	if tonight := quietFor(rules, now.AddDate(0, 0, 1).Weekday()); tonight.From > tonight.To && m >= tonight.From {
		return tonight, true
	}
	return notifyRule{}, false
}

// Who may write a rule. The brain writes it only when the user asked in
// this clean turn, in their own words about being told or not; otherwise,
// or on a tainted turn, it is only proposed.

var ruleRequest = regexp.MustCompile(`(?i)(pr[ée]vien|pr[ée]venir|notif|r[ée]veill|d[ée]rang|alerte|sonne|la nuit|nuits?\b|heures? calmes?|brief|rappel|interromp|ping|tais-toi|silence|plus de messages?|notify|wake|disturb|quiet hours|alert|remind|bother|interrupt)`)

// ruleWrite is how a brain's rule is recorded: active, or only proposed and why.
func (c *core) ruleWrite(sourceRef string, scan scanResult) (state, why string, err error) {
	if scan.Reject != "" {
		return "", "", fmt.Errorf("refused: %s", scan.Reject)
	}
	if tainted := c.turnTaint(); len(tainted) > 0 {
		return ruleProposed, "this turn read external content (" + strings.Join(tainted, ", ") + ")", nil
	}
	from, to, _, _ := c.turn.snapshot()
	msg, owner := c.ownerMessage(sourceRef)
	switch {
	case !owner:
		return ruleProposed, "no message of the user's asks for it (source_ref msg:N)", nil
	case from == 0 || msg.Seq < from || msg.Seq > to:
		return ruleProposed, "the user's message it cites is not of this turn", nil
	case !ruleRequest.MatchString(msg.Text):
		return ruleProposed, "the user's message it cites does not speak of being told", nil
	}
	return ruleActive, "", nil
}

package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// The brain speaks on its own (notify), and the code decides when. The
// model writes the words and weighs them, 1 to 5 (the attention scale:
// attention.go); what happens is decideNotify's: the user's rules (a floor
// and a ceiling of weight per subject or kind, notify_rules.go), then the
// quiet hours and the daily cap. Then it enters the attention queue
// (attention.go), the one path to the user, at its decided weight: from
// weightSent it is delivered at once (outcome now), lighter it is held
// for a breakpoint, 1 and 2 for the user's return, the brief (outcome
// held). Only a notification decided now at weight 5 is pushed to a
// device. Models are wrong about one time in two on when to interrupt
// (ProactiveBench), so the decision is code, measured on a labelled corpus
// (notify_eval_test.go).
//
//   - There is always a quiet window: a quiet rule of the user's, else the
//     seed 22:00–08:00. Nothing from the model alone, nor from external
//     content, breaks it; only a user rule marked "even at night" does, or
//     the fixed message of a watch the user marked urgent, which eriphe-core
//     sends itself (watch_urgent.go).
//   - A weight from the model alone never goes through them: only a user
//     rule marked "even at night" does, or a fixed urgent text.
//     Held in them, it waits for the user's return (weight 2).
//   - At most notifyCap pushes a day (4 by default, Settings), besides what
//     the user asked for themselves (a reminder from their own words); over
//     it, a 5 waits for the next breakpoint (weight 3).
//   - What a tainted turn says (one that read external content) never
//     interrupts: it waits for the next breakpoint at most (weight 3), and
//     is marked so. It only speaks to the user; nothing it says can act.
//   - What is held is listed for the brain (notifications) and in the
//     workspace, and waits in the attention queue.
//
// Every notification is kept with its why: the push shows the text, and
// opening it shows why, with 👍 / 👎 / « pas maintenant ». That feedback goes
// back to the brain, which proposes rules from it.

const notifySchema = `CREATE TABLE IF NOT EXISTS notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT, created INTEGER NOT NULL,
  text TEXT NOT NULL, why TEXT NOT NULL, ref TEXT NOT NULL DEFAULT '', source TEXT NOT NULL,
  weight INTEGER NOT NULL DEFAULT 0, outcome TEXT NOT NULL, reason TEXT NOT NULL, rule_id INTEGER,
  asked INTEGER NOT NULL DEFAULT 0, tainted TEXT NOT NULL DEFAULT '', msg INTEGER,
  held INTEGER NOT NULL DEFAULT 0, feedback TEXT NOT NULL DEFAULT '', feedback_at INTEGER,
  token TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS notifications_created ON notifications (created);`

func init() {
	extraSchema = append(extraSchema, notifySchema)
	schemaFixes = append(schemaFixes, notifyWeightColumn)
}

// notifyWeightColumn rewrites once a table made when a notification had a
// level (now or brief) instead of a weight: sent is 5, the rest 2.
func notifyWeightColumn(db *sql.DB) error {
	has := func(col string) bool {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notifications') WHERE name = ?`, col).Scan(&n)
		return n > 0
	}
	if !has("level") {
		_, err := db.Exec(`UPDATE notifications SET outcome = 'held' WHERE outcome = 'brief'`) // « brief » is the return only
		return err
	}
	if !has("weight") {
		if _, err := db.Exec(`ALTER TABLE notifications ADD COLUMN weight INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`UPDATE notifications SET weight = CASE WHEN outcome = 'now' THEN 5 ELSE 2 END WHERE weight = 0`); err != nil {
		return err
	}
	_, err := db.Exec(`ALTER TABLE notifications DROP COLUMN level`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE notifications SET outcome = 'held' WHERE outcome = 'brief'`)
	return err
}

const (
	// What the code decided: the outcome column.
	outcomeNow   = "now"   // delivered at once; pushed at weight 5
	outcomeHeld  = "held"  // waits in the queue for its breakpoint
	outcomeNever = "never" // a rule of the user's says never

	weightSent = 4 // from this weight a notification is delivered at once

	notifyFromBrain = "brain" // the notify tool
	notifyFromWake  = "wake"  // the brain's answer to a due intention (brain_wake.go)

	defaultNotifyCap = 4
	maxNotifyCap     = 20
	notifyTextMax    = 500
	notifyWhyMax     = 300

	// Why the code decided as it did: the reason column, shown in the
	// workspace (notif.reason.*) and told to the brain (notifyReasons).
	reasonNow     = "now"
	reasonPause   = "pause"   // weighed 3: the next breakpoint
	reasonBrief   = "brief"   // weighed 1 or 2: the user's return
	reasonRule    = "rule"    // a rule of the user's raised or lowered its weight
	reasonTainted = "tainted" // external content never interrupts alone
	reasonQuiet   = "quiet"
	reasonCap     = "cap"
	reasonUrgent  = "urgent" // a rule "even at night" let it through
	reasonLater   = "later"  // the user said « pas maintenant »
	reasonWatch   = "watch"  // the fixed message of a watch the user marked urgent
)

var notifyReasons = map[string]string{
	reasonNow:     "delivered now (pushed to a device at weight 5)",
	reasonPause:   "held for the next pause: weighed 3",
	reasonBrief:   "held for the user's return, the brief: weighed 1 or 2",
	reasonRule:    "weighed again by a rule of the user's",
	reasonTainted: "held for the next pause: this turn read external content, which never interrupts on its own",
	reasonQuiet:   "held for the user's return: quiet hours",
	reasonCap:     "held for the next pause: today's cap of pushes is reached",
	reasonUrgent:  "delivered now: a rule of the user's allows it even at night",
	reasonLater:   "the user said not now: held for their return",
	reasonWatch:   "sent now, at any hour: the fixed message of a watch the user marked urgent",
}

// notification is one thing the brain told, or asked to tell, the user.
type notification struct {
	ID         int64     `json:"id"`
	Created    time.Time `json:"created"`
	Text       string    `json:"text"`
	Why        string    `json:"why"`
	Ref        string    `json:"ref,omitempty"`     // the record it is about: intention:12, skill:<name>
	Subject    string    `json:"subject,omitempty"` // what it is about, kind:slug (subject.go)
	Source     string    `json:"source"`
	Weight     int       `json:"weight"`  // as decided: the asked weight, through the rules
	Outcome    string    `json:"outcome"` // what the code decided: now, brief or never
	Reason     string    `json:"reason"`
	RuleID     int64     `json:"rule,omitempty"`
	Asked      bool      `json:"asked,omitempty"`   // the user asked for it themselves
	Tainted    string    `json:"tainted,omitempty"` // what the turn had read, if anything
	Msg        int       `json:"msg,omitempty"`     // its message in the conversation
	Held       bool      `json:"held,omitempty"`    // waits in the attention queue
	Feedback   string    `json:"feedback,omitempty"`
	FeedbackAt time.Time `json:"feedback_at,omitzero"`
	token      string    // lets a push's buttons answer without a session
}

// notifyItem is what decideNotify judges.
type notifyItem struct {
	Weight  int    // 1 to 5, as asked
	Subject string // what it is about: project:getout…
	Ref     string // the record it is about: intention:12, skill:<name>…
	Asked   bool   // the user asked for it in their own words: not counted in the cap
	Tainted bool   // drawn from external content
	Where   string // the line the writer would try first (where_now.go); "" for the router's order
	// Spoken: the caller writes it in the conversation itself when it is
	// delivered now (the brain's own words): its item enters delivered.
	Spoken bool
	// Urgent is set by eriphe-core only, never by the brain: the fixed
	// message of a watch the user marked urgent, on its condition
	// (watch_urgent.go). It goes through the quiet hours and the cap.
	Urgent bool
}

// notifyContext is the state it is judged in.
type notifyContext struct {
	Now     time.Time // in the user's zone
	Rules   []notifyRule
	SentNow int // pushes today, besides what the user asked for
	Cap     int
}

type notifyDecision struct {
	Outcome string
	Reason  string
	RuleID  int64
	Weight  int // the weight asked, through the rule
}

// decideNotify decides, without a model: never, now or held. The
// rule for its subject (or kind) raises the weight to its floor and lowers
// it to its ceiling, a ceiling of 0 being never; a tainted turn may be
// lowered, never raised. From weightSent it is sent, unless the turn read
// external content, the quiet hours or the daily cap hold it: only a rule
// "even at night" goes through those two, or eriphe-core's own urgent text.
func decideNotify(it notifyItem, cx notifyContext) notifyDecision {
	w := min(max(it.Weight, 1), weightInterrupts)
	if it.Urgent && !it.Tainted && w == weightInterrupts {
		return notifyDecision{outcomeNow, reasonWatch, 0, weightInterrupts}
	}
	r := itemRule(cx.Rules, it)
	var ruleID int64
	reason := ""
	if r != nil {
		ruleID = r.ID
		if r.Ceiling == 0 {
			return notifyDecision{outcomeNever, reasonRule, r.ID, 0}
		}
		if w < r.Floor && !it.Tainted { // the model chose the subject: a tainted turn may lower, never raise
			w, reason = r.Floor, reasonRule
		}
		if w > r.Ceiling {
			w, reason = r.Ceiling, reasonRule
		}
	}
	switch {
	case w < weightProposal:
		return notifyDecision{outcomeHeld, cmpOr(reason, reasonBrief), ruleID, w}
	case w < weightSent:
		return notifyDecision{outcomeHeld, cmpOr(reason, reasonPause), ruleID, w}
	case it.Tainted:
		return notifyDecision{outcomeHeld, reasonTainted, ruleID, weightProposal}
	}
	urgent := r != nil && r.Night && r.Floor == weightInterrupts
	if _, quiet := quietAt(cx.Rules, cx.Now); quiet {
		if !urgent {
			return notifyDecision{outcomeHeld, reasonQuiet, ruleID, weightBrief}
		}
		return notifyDecision{outcomeNow, reasonUrgent, ruleID, w}
	}
	if w == weightInterrupts && !it.Asked && !urgent && cx.SentNow >= cx.Cap {
		return notifyDecision{outcomeHeld, reasonCap, ruleID, weightProposal}
	}
	return notifyDecision{outcomeNow, cmpOr(reason, reasonNow), ruleID, w}
}

// The settings: the user's zone and the daily cap (Settings → notifications).

func (c *core) notifyZone() *time.Location {
	if name := c.store.get("notify_zone"); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.Local
}

func (c *core) notifyCap() int {
	if n, err := strconv.Atoi(c.store.get("notify_cap")); err == nil && n >= 0 && n <= maxNotifyCap {
		return n
	}
	return defaultNotifyCap
}

// notifyClock is the time of the decisions; a test sets it.
func (c *core) notifyTime() time.Time {
	if c.notifyClock != nil {
		return c.notifyClock().In(c.notifyZone())
	}
	return time.Now().In(c.notifyZone())
}

// notifyContext reads the state a notification is judged in.
func (c *core) notifyContext() (notifyContext, error) {
	now := c.notifyTime()
	rules, err := c.notifyRules(ruleActive)
	if err != nil {
		return notifyContext{}, err
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var sent int
	err = c.store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE outcome = 'now' AND weight >= ? AND asked = 0 AND reason NOT IN (?, ?)
		AND created >= ?`, weightInterrupts, reasonUrgent, reasonWatch, day.Unix()).Scan(&sent)
	return notifyContext{Now: now, Rules: rules, SentNow: sent, Cap: c.notifyCap()}, err
}

// notify decides on n, keeps it, puts it in the attention queue (not when
// never), and pushes it when it is delivered now at weight 5.
func (c *core) notify(n *notification, it notifyItem) (*notification, error) {
	// A subject merged is kept under the other (subjects.go).
	n.Subject = c.store.keepSubject(n.Subject, "")
	it.Subject, it.Ref = n.Subject, n.Ref
	cx, err := c.notifyContext()
	if err != nil {
		return nil, err
	}
	d := decideNotify(it, cx)
	n.Created, n.Weight, n.Outcome, n.Reason, n.RuleID, n.Asked = cx.Now, d.Weight, d.Outcome, d.Reason, d.RuleID, it.Asked
	n.Held = d.Outcome == outcomeHeld
	n.token = randomToken(16)
	res, err := c.store.db.Exec(`INSERT INTO notifications (created, text, why, ref, subject, source, weight, outcome, reason, rule_id,
		asked, tainted, msg, held, token) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Created.Unix(), n.Text, n.Why, n.Ref, n.Subject, n.Source, n.Weight, n.Outcome, n.Reason, nullID(n.RuleID),
		n.Asked, n.Tainted, nullID(int64(n.Msg)), n.Held, n.token)
	if err != nil {
		return nil, err
	}
	n.ID, _ = res.LastInsertId()
	log.Printf("notify: %d weight %d → %d %s (%s)", n.ID, it.Weight, d.Weight, d.Outcome, d.Reason)
	if d.Outcome != outcomeNever {
		c.attendNote(n, it.Spoken && d.Outcome == outcomeNow) // the one path to the user (attention.go)
	}
	if d.Outcome == outcomeNow && d.Weight == weightInterrupts {
		note := notifyNote(n)
		note.Route = routeNote{Level: outcomeNow, Tainted: it.Tainted, Fixed: n.Source == notifyFromUrgent, First: it.Where}
		if r := itemRule(cx.Rules, it); r != nil {
			note.Channels = r.Channels // the rule's channels, else the default (channels.go)
			note.Route.Only, note.Route.Also, note.Route.Never = r.Channels, r.Also, r.Never
			note.Route.Urgent = r.Night && r.Floor == weightInterrupts
		}
		if d.Reason == reasonWatch {
			note.Route.Urgent = true // the shorter delay, and escalation in the quiet hours
		}
		c.tell(note)
	}
	return n, nil
}

// notifyNote is the push of a notification: its text; opening it shows why.
func notifyNote(n *notification) pushNote {
	tag := fmt.Sprintf("notify-%d", n.ID)
	if n.Source == notifyFromWake {
		tag = "wake"
	}
	return pushNote{Body: map[string]string{"fr": n.Text, "en": n.Text}, URL: notificationURL(n.ID), Tag: tag,
		Topic: tag, Urgent: true, React: fmt.Sprintf("/notify/react?id=%d&k=%s", n.ID, n.token), Notif: n.ID, Why: n.Why}
}

func notificationURL(id int64) string {
	return fmt.Sprintf("/settings/notifications?n=%d#notif-%d", id, id)
}

const notifyCols = `id, created, text, why, ref, subject, source, weight, outcome, reason, COALESCE(rule_id, 0), asked, tainted,
	COALESCE(msg, 0), held, feedback, COALESCE(feedback_at, 0), token`

func scanNotification(row interface{ Scan(...any) error }) (*notification, error) {
	var n notification
	var created, fbAt int64
	if err := row.Scan(&n.ID, &created, &n.Text, &n.Why, &n.Ref, &n.Subject, &n.Source, &n.Weight, &n.Outcome, &n.Reason, &n.RuleID,
		&n.Asked, &n.Tainted, &n.Msg, &n.Held, &n.Feedback, &fbAt, &n.token); err != nil {
		return nil, err
	}
	n.Created = time.Unix(created, 0)
	if fbAt > 0 {
		n.FeedbackAt = time.Unix(fbAt, 0)
	}
	return &n, nil
}

func (c *core) notificationsWhere(where string, args ...any) ([]*notification, error) {
	rows, err := c.store.db.Query(`SELECT `+notifyCols+` FROM notifications WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (c *core) notificationByID(id int64) (*notification, error) {
	return scanNotification(c.store.db.QueryRow(`SELECT `+notifyCols+` FROM notifications WHERE id = ?`, id))
}

// heldNotifications are those waiting in the attention queue, oldest first.
func (c *core) heldNotifications() ([]*notification, error) {
	return c.notificationsWhere(`held = 1 ORDER BY id LIMIT 100`)
}

// notificationsSince are those made since a time, oldest first, all or
// only what still waits in the queue: what a brief, or the point made on
// the user's return, summarises (each with its source, time, why, ref,
// outcome and feedback).
func (c *core) notificationsSince(since time.Time, heldOnly bool) ([]*notification, error) {
	if heldOnly {
		return c.notificationsWhere(`created >= ? AND held = 1 ORDER BY id LIMIT 200`, since.Unix())
	}
	return c.notificationsWhere(`created >= ? ORDER BY id LIMIT 200`, since.Unix())
}

// recentNotifications are the last ones, newest first.
func (c *core) recentNotifications(n int) ([]*notification, error) {
	return c.notificationsWhere(`1 ORDER BY id DESC LIMIT ?`, n)
}

var feedbacks = []string{"up", "down", "later"}

// notificationFeedback keeps the user's answer to a notification: 👍, 👎,
// or « pas maintenant », which puts it back in the brief.
func (c *core) notificationFeedback(id int64, fb string) (*notification, error) {
	if !containsStr(feedbacks, fb) {
		return nil, fmt.Errorf("feedback is up, down or later")
	}
	q := `UPDATE notifications SET feedback = ?, feedback_at = ? WHERE id = ? AND outcome != 'never'`
	if fb == "later" {
		q = `UPDATE notifications SET feedback = ?, feedback_at = ?, held = 1 WHERE id = ? AND outcome != 'never'`
	}
	res, err := c.store.db.Exec(q, fb, c.notifyTime().Unix(), id)
	if err != nil {
		return nil, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return nil, sql.ErrNoRows
	}
	c.ackNotification(id, ackFeedback) // seen: no next device (route_send.go)
	n, err := c.notificationByID(id)
	if err == nil && fb == "later" {
		c.noteLater(n) // held again for the user's return (attention.go)
	}
	return n, err
}

// reactWithToken is a push button's answer: no session, the notification's
// own token instead, which only its push carried.
func (c *core) reactWithToken(id int64, token, fb string) error {
	n, err := c.notificationByID(id)
	if err != nil || n.token == "" || subtle.ConstantTimeCompare([]byte(n.token), []byte(token)) != 1 {
		return errors.New("unknown notification")
	}
	_, err = c.notificationFeedback(id, fb)
	return err
}

// The wake's answer is a notification too: a brain message written in the
// turn a due intention woke it for reaches the phone, question or not
// (pushWaiting only pushed questions). The wake turn is kept (kv
// wake_turn) from its delivery to the next delivery of the conversation.

type wakeTurn struct {
	EventSeq int     `json:"event_seq"` // the brain task's last event before the wake
	IDs      []int64 `json:"ids"`
	Text     string  `json:"text"`     // the first intention's text, for the why
	External bool    `json:"external"` // an intention drawn from external content
	Asked    bool    `json:"asked"`    // all in the user's own words
}

const wakeTurnKey = "wake_turn"

func (c *core) setWakeTurn(w *wakeTurn) {
	if w == nil {
		c.store.set(wakeTurnKey, "")
		return
	}
	raw, _ := json.Marshal(w)
	c.store.set(wakeTurnKey, string(raw))
}

// notifiedKey marks a brain message that went through notify: pushWaiting
// does not push it again as a question.
func notifiedKey(seq int) string { return fmt.Sprintf("notified_msg:%d", seq) }

// notifyWake makes the first brain message of a wake turn a notification.
// Called with relayMu held, right after the message is written.
func (c *core) notifyWake(seq, eventSeq int, text string) {
	raw := c.store.get(wakeTurnKey)
	var w wakeTurn
	if raw == "" || json.Unmarshal([]byte(raw), &w) != nil || eventSeq <= w.EventSeq {
		return
	}
	c.setWakeTurn(nil) // its first message only
	c.store.set(notifiedKey(seq), "1")
	ref := ""
	if len(w.IDs) > 0 {
		ref = fmt.Sprintf("intention:%d", w.IDs[0])
	}
	why := "Une intention est due : « " + truncate(w.Text, 160) + " »"
	if len(w.IDs) > 1 {
		why = fmt.Sprintf("%d intentions sont dues, dont « %s »", len(w.IDs), truncate(w.Text, 160))
	}
	// Its taint is the intentions': a reminder approved by the user is not
	// held back because the brain read a mail earlier in the turn; that
	// turn's taint is still kept with it.
	subject := "" // the first intention's
	if len(w.IDs) > 0 && c.mem != nil {
		if x, err := c.mem.intention(w.IDs[0]); err == nil {
			subject = x.Subject
		}
	}
	n := &notification{Text: truncate(strings.TrimSpace(text), notifyTextMax), Why: why, Ref: ref, Subject: subject, Source: notifyFromWake,
		Msg: seq, Tainted: strings.Join(c.turnTaint(), ", ")}
	if _, err := c.notify(n, notifyItem{Weight: weightInterrupts, Ref: ref, Asked: w.Asked, Tainted: w.External}); err != nil {
		log.Printf("notify: wake: %v", err)
	}
}

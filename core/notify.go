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
// model writes the words and asks for a level, now or for the brief; what
// happens is decideNotify's: the quiet hours, the daily cap and the user's
// rules (notify_rules.go). Models are wrong about one time in two on when to
// interrupt (ProactiveBench), so the decision is code, measured on a labelled
// corpus (notify_eval_test.go).
//
//   - There is always a quiet window: a quiet rule of the user's, else the
//     seed 22:00–08:00. Nothing from the model alone, nor from external
//     content, breaks it; only a user rule marked "even at night" does.
//   - At most notifyCap "now" a day (4 by default, Settings), besides what
//     the user asked for themselves (a reminder from their own words).
//   - What a tainted turn says (one that read external content) never
//     interrupts: it goes to the brief at most, and is marked so. It only
//     speaks to the user; nothing it says can act.
//   - A "now" refused goes to the brief: it is held, listed for the brain
//     (notifications) and in the workspace, until the brief (step 2) says it.
//
// Every notification is kept with its why: the push shows the text, and
// opening it shows why, with 👍 / 👎 / « pas maintenant ». That feedback goes
// back to the brain, which proposes rules from it.

const notifySchema = `CREATE TABLE IF NOT EXISTS notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT, created INTEGER NOT NULL,
  text TEXT NOT NULL, why TEXT NOT NULL, ref TEXT NOT NULL DEFAULT '', source TEXT NOT NULL,
  level TEXT NOT NULL, outcome TEXT NOT NULL, reason TEXT NOT NULL, rule_id INTEGER,
  asked INTEGER NOT NULL DEFAULT 0, tainted TEXT NOT NULL DEFAULT '', msg INTEGER,
  held INTEGER NOT NULL DEFAULT 0, feedback TEXT NOT NULL DEFAULT '', feedback_at INTEGER,
  token TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS notifications_created ON notifications (created);`

func init() { extraSchema = append(extraSchema, notifySchema) }

const (
	levelNow   = "now"
	levelBrief = "brief"
	levelNever = "never"

	notifyFromBrain = "brain" // the notify tool
	notifyFromWake  = "wake"  // the brain's answer to a due intention (brain_wake.go)

	defaultNotifyCap = 4
	maxNotifyCap     = 20
	notifyTextMax    = 500
	notifyWhyMax     = 300

	// Why the code decided as it did: the reason column, shown in the
	// workspace (notif.reason.*) and told to the brain (notifyReasons).
	reasonNow     = "now"
	reasonBrief   = "brief"   // asked for the brief
	reasonRule    = "rule"    // a rule of the user's decided
	reasonTainted = "tainted" // external content never interrupts alone
	reasonQuiet   = "quiet"
	reasonCap     = "cap"
	reasonUrgent  = "urgent" // a rule "even at night" let it through
	reasonLater   = "later"  // the user said « pas maintenant »
)

var notifyReasons = map[string]string{
	reasonNow:     "sent now",
	reasonBrief:   "kept for the brief, as asked",
	reasonRule:    "decided by a rule of the user's",
	reasonTainted: "kept for the brief: this turn read external content, which never interrupts on its own",
	reasonQuiet:   "kept for the brief: quiet hours",
	reasonCap:     "kept for the brief: today's cap of interruptions is reached",
	reasonUrgent:  "sent now: a rule of the user's allows it even at night",
	reasonLater:   "the user said not now: kept for the brief",
}

// notification is one thing the brain told, or asked to tell, the user.
type notification struct {
	ID         int64     `json:"id"`
	Created    time.Time `json:"created"`
	Text       string    `json:"text"`
	Why        string    `json:"why"`
	Ref        string    `json:"ref,omitempty"`
	Source     string    `json:"source"`
	Level      string    `json:"level"`   // what was asked: now or brief
	Outcome    string    `json:"outcome"` // what the code decided: now, brief or never
	Reason     string    `json:"reason"`
	RuleID     int64     `json:"rule,omitempty"`
	Asked      bool      `json:"asked,omitempty"`   // the user asked for it themselves
	Tainted    string    `json:"tainted,omitempty"` // what the turn had read, if anything
	Msg        int       `json:"msg,omitempty"`     // its message in the conversation
	Held       bool      `json:"held,omitempty"`    // waits for the brief
	Feedback   string    `json:"feedback,omitempty"`
	FeedbackAt time.Time `json:"feedback_at,omitzero"`
	token      string    // lets a push's buttons answer without a session
}

// notifyItem is what decideNotify judges.
type notifyItem struct {
	Level   string // now or brief, as asked
	Ref     string // what it is about: project:getout, intention:12…
	Asked   bool   // the user asked for it in their own words: not counted in the cap
	Tainted bool   // drawn from external content
}

// notifyContext is the state it is judged in.
type notifyContext struct {
	Now     time.Time // in the user's zone
	Rules   []notifyRule
	SentNow int // "now" sent today, besides what the user asked for
	Cap     int
}

type notifyDecision struct {
	Outcome string
	Reason  string
	RuleID  int64
}

// decideNotify decides, without a model: never, now or for the brief.
func decideNotify(it notifyItem, cx notifyContext) notifyDecision {
	level := it.Level
	r := topicRule(cx.Rules, it.Ref)
	var ruleID int64
	if r != nil {
		ruleID = r.ID
		switch r.Outcome {
		case levelNever:
			return notifyDecision{levelNever, reasonRule, r.ID}
		case levelBrief:
			return notifyDecision{levelBrief, reasonRule, r.ID}
		case levelNow:
			if !it.Tainted { // the model chose the ref: a tainted turn may lower, never raise
				level = levelNow
			}
		}
	}
	if level != levelNow {
		return notifyDecision{levelBrief, reasonBrief, ruleID}
	}
	if it.Tainted {
		return notifyDecision{levelBrief, reasonTainted, ruleID}
	}
	urgent := r != nil && r.Outcome == levelNow && r.Night
	if _, quiet := quietAt(cx.Rules, cx.Now); quiet {
		if !urgent {
			return notifyDecision{levelBrief, reasonQuiet, ruleID}
		}
		return notifyDecision{levelNow, reasonUrgent, ruleID}
	}
	if !it.Asked && !urgent && cx.SentNow >= cx.Cap {
		return notifyDecision{levelBrief, reasonCap, ruleID}
	}
	if r != nil && r.Outcome == levelNow {
		return notifyDecision{levelNow, reasonRule, ruleID}
	}
	return notifyDecision{levelNow, reasonNow, ruleID}
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
	err = c.store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE outcome = 'now' AND asked = 0 AND reason != ? AND created >= ?`,
		reasonUrgent, day.Unix()).Scan(&sent)
	return notifyContext{Now: now, Rules: rules, SentNow: sent, Cap: c.notifyCap()}, err
}

// notify decides on n, keeps it, and pushes it when it is for now.
func (c *core) notify(n *notification, it notifyItem) (*notification, error) {
	cx, err := c.notifyContext()
	if err != nil {
		return nil, err
	}
	d := decideNotify(it, cx)
	n.Created, n.Level, n.Outcome, n.Reason, n.RuleID, n.Asked = cx.Now, it.Level, d.Outcome, d.Reason, d.RuleID, it.Asked
	n.Held = d.Outcome == levelBrief
	n.token = randomToken(16)
	res, err := c.store.db.Exec(`INSERT INTO notifications (created, text, why, ref, source, level, outcome, reason, rule_id,
		asked, tainted, msg, held, token) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Created.Unix(), n.Text, n.Why, n.Ref, n.Source, n.Level, n.Outcome, n.Reason, nullID(n.RuleID),
		n.Asked, n.Tainted, nullID(int64(n.Msg)), n.Held, n.token)
	if err != nil {
		return nil, err
	}
	n.ID, _ = res.LastInsertId()
	log.Printf("notify: %d %s → %s (%s)", n.ID, it.Level, d.Outcome, d.Reason)
	if d.Outcome == levelNow {
		c.tell(notifyNote(n))
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
		Topic: tag, Urgent: true, React: fmt.Sprintf("/notify/react?id=%d&k=%s", n.ID, n.token)}
}

func notificationURL(id int64) string {
	return fmt.Sprintf("/settings/notifications?n=%d#notif-%d", id, id)
}

const notifyCols = `id, created, text, why, ref, source, level, outcome, reason, COALESCE(rule_id, 0), asked, tainted,
	COALESCE(msg, 0), held, feedback, COALESCE(feedback_at, 0), token`

func scanNotification(row interface{ Scan(...any) error }) (*notification, error) {
	var n notification
	var created, fbAt int64
	if err := row.Scan(&n.ID, &created, &n.Text, &n.Why, &n.Ref, &n.Source, &n.Level, &n.Outcome, &n.Reason, &n.RuleID,
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

// heldNotifications are those waiting for the brief, oldest first.
func (c *core) heldNotifications() ([]*notification, error) {
	return c.notificationsWhere(`held = 1 ORDER BY id LIMIT 100`)
}

// notificationsSince are those made since a time, oldest first, all or
// only what still waits for the brief: what a brief, or the point made on
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
	return c.notificationByID(id)
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
	n := &notification{Text: truncate(strings.TrimSpace(text), notifyTextMax), Why: why, Ref: ref, Source: notifyFromWake,
		Msg: seq, Tainted: strings.Join(c.turnTaint(), ", ")}
	if _, err := c.notify(n, notifyItem{Level: levelNow, Ref: ref, Asked: w.Asked, Tainted: w.External}); err != nil {
		log.Printf("notify: wake: %v", err)
	}
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Urgent watches: "wake me at any hour if the shop's prod is down". The
// wake turn of an outil or alerte watch is tainted (what it read is
// external), and a tainted turn never interrupts; so the brain cannot wake
// the user at night for it, whatever it reads. An urgent watch is the one
// way through, bound to that watch, its condition and its text:
//
//   - the user asks for it in a clean turn, and the watch carries a fixed
//     message they validated (« Alerte boutique : la prod ne répond
//     plus »). A proposal, a tainted turn, a message of the user's that
//     does not ask to be woken: none sets or raises urgent;
//   - when the condition becomes true (outil: eriphe-core's deterministic
//     test, confirmed twice; alerte: a call to its authenticated webhook),
//     eriphe-core itself sends that fixed message, now, through the router,
//     over the quiet hours and the daily cap. Nothing of what the tool or
//     the sender says enters it;
//   - once per incident: not again until it recovers (outil: the condition
//     false again; alerte: a call whose top-level status is "resolved", as
//     Grafana and Alertmanager send). The recovery says « <name> : revenu à
//     la normale », with the usual rules unless the user asked for it to be
//     urgent too. A change, a pause or a resume of the watch starts afresh;
//   - the brain is then woken as before, tainted, to look: what it says goes
//     through notify, with no urgency of its own.

const (
	urgentTextMax = 160

	notifyFromUrgent = "urgent" // what eriphe-core sends itself for an urgent watch
	urgentRecovered  = " : revenu à la normale"
)

// urgentRequest: wording of the user's that asks to be woken, or reached at
// any hour.
var urgentRequest = regexp.MustCompile(`(?i)(r[ée]veill|wake|m[êe]me la nuit|la nuit|en pleine nuit|nuit|toute heure|n'importe quelle heure|n'importe quand|any hour|any time|anytime|at night|urgen)`)

// checkUrgent validates an urgent watch's fixed message, for a watch of
// this source and condition.
func checkUrgent(w *watch, text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	switch {
	case w.Source != sourcePoll && w.Source != sourceAlert:
		return "", errors.New("only an outil or alerte watch can be urgent")
	case w.Source == sourcePoll && (w.Spec.Cond == nil || w.Spec.Cond.Kind == condChanged):
		return "", errors.New("an urgent outil watch needs a condition that recovers: compare or status")
	case text == "" || utf8.RuneCountInString(text) > urgentTextMax:
		return "", fmt.Errorf("urgent_message is 1 to %d characters: the fixed text the user validated", urgentTextMax)
	case urlRe2.MatchString(text):
		return "", errors.New("urgent_message is plain text, without a link")
	}
	if scan := scanMemoryText(text); scan.Reject != "" {
		return "", fmt.Errorf("refused: %s", scan.Reject)
	}
	return text, nil
}

// urgentWrite tells whether the brain may make or raise an urgent watch:
// only in a clean turn, citing the user's message of it that asks to be
// woken.
func (c *core) urgentWrite(sourceRef string) error {
	if tainted := c.turnTaint(); len(tainted) > 0 {
		return fmt.Errorf("refused: this turn read external content (%s): a watch is made urgent only in a turn that read nothing from outside, in %s's own words", strings.Join(tainted, ", "), c.conf().User)
	}
	from, to, _, _ := c.turn.snapshot()
	msg, owner := c.ownerMessage(sourceRef)
	switch {
	case !owner || from == 0 || msg.Seq < from || msg.Seq > to:
		return errors.New("refused: an urgent watch needs the user's message of this turn that asks for it (source_ref msg:N)")
	case !urgentRequest.MatchString(msg.Text):
		return errors.New("refused: the user's message it cites does not ask to be woken or reached at any hour")
	}
	return nil
}

// setWatchUrgent makes an active watch urgent with its fixed message, or
// not urgent (text ""). Its incident starts afresh.
func (c *core) setWatchUrgent(w *watch, text string, recovery bool) error {
	if text != "" && w.State != watchActive {
		return errors.New("refused: the watch is not active: the user approves it first")
	}
	recovery = recovery && text != ""
	_, err := c.store.db.Exec(`UPDATE watches SET urgent = ?, urgent_recovery = ?, incident = 0, updated = ? WHERE id = ?`,
		text, recovery, c.notifyTime().Unix(), w.ID)
	if err == nil {
		w.Urgent, w.UrgentRecovery, w.Incident = text, recovery, 0
	}
	return err
}

// urgentTransition is called when an urgent watch's condition becomes true
// (hit) or false again, before the brain is woken. It sends the fixed
// message once per incident, or the recovery when one was open, and
// returns the notification sent, 0 if none. Nothing external enters it.
func (c *core) urgentTransition(w *watch, hit bool) int64 {
	if w.Urgent == "" {
		return 0
	}
	now := c.notifyTime().Unix()
	q := `UPDATE watches SET incident = ? WHERE id = ? AND urgent != '' AND incident = 0`
	args := []any{now, w.ID}
	if !hit {
		q, args = `UPDATE watches SET incident = 0 WHERE id = ? AND incident != 0`, []any{w.ID}
	}
	res, err := c.store.db.Exec(q, args...)
	if err != nil {
		log.Printf("watch %d: urgent: %v", w.ID, err)
		return 0
	}
	if k, _ := res.RowsAffected(); k != 1 {
		return 0 // already sent for this incident, or none to recover from
	}
	text, urgent := w.Urgent, true
	why := "Ta veille « " + w.Name + " » est urgente : tu as demandé à être prévenu à toute heure, avec ce message."
	if !hit {
		text, urgent = truncate(w.Name+urgentRecovered, notifyTextMax), w.UrgentRecovery
		why = "Ta veille urgente « " + w.Name + " » est revenue à la normale."
	}
	n := &notification{Text: text, Why: why, Ref: fmt.Sprintf("watch:%d", w.ID), Source: notifyFromUrgent}
	n, err = c.notify(n, notifyItem{Level: levelNow, Ref: n.Ref, Asked: true, Urgent: urgent})
	if err != nil {
		log.Printf("watch %d: urgent: %v", w.ID, err)
		return 0
	}
	log.Printf("watch %d: urgent message %d (%s, %s)", w.ID, n.ID, map[bool]string{true: "incident", false: "recovered"}[hit], n.Outcome)
	return n.ID
}

// alertResolved tells whether a webhook call says its alert is resolved:
// a top-level "status": "resolved" (Grafana, Alertmanager). Read by code,
// never shown.
func alertResolved(body []byte) bool {
	var v struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(body, &v) == nil && strings.EqualFold(strings.TrimSpace(v.Status), "resolved")
}

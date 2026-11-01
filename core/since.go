package main

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// What happened since a time, from every source, in one chronological
// feed: the tasks (started, finished with their receipt's verdict, waiting,
// failed, stopped), the brain's notifications (sent, held, dropped, and the
// user's feedback), the intentions (due, done, snoozed, cancelled) and the
// conversation's messages. The brain reads it with since (since_tool.go),
// the user in « Ce qui s'est passé » (web_since.go). Each item is written
// by eriphe-core from what it recorded, so the model can neither forget
// nor invent what happened.
//
// Each item carries its trust: own (the user's words, the brain's in a
// clean turn, what the code recorded) or external (drawn from outside: a
// notification or a brain message written in a tainted turn, an intention
// drawn from a mail, a task started on a tainted turn). An external item's
// text is quoted only when asked for, and that taints the turn, as reading
// an external memory does.

// task_log is what happened to each worker task, as the brain was told:
// a state, and for a turn that ended its receipt's verdict, in numbers.
const taskLogSchema = `CREATE TABLE IF NOT EXISTS task_log (
  seq INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, at INTEGER NOT NULL, state TEXT NOT NULL,
  receipt TEXT NOT NULL DEFAULT '', verified INTEGER NOT NULL DEFAULT 0, open INTEGER NOT NULL DEFAULT 0,
  mismatches INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS task_log_at ON task_log (at);`

func init() { extraSchema = append(extraSchema, taskLogSchema) }

const (
	feedTask         = "task"
	feedNotification = "notification"
	feedIntention    = "intention"
	feedMessage      = "message"
	feedWatch        = "watch" // the watches' runs (watch.go)

	feedOwn      = "own"
	feedExternal = "external"

	feedTextMax    = 160
	feedDefault    = 50
	feedMax        = 200
	lastBriefKey   = "last_brief" // when the last brief was given, unix; a brief watch writes it (watch_tick.go)
	sinceAway      = "away"
	sinceLastBrief = "last_brief"
	taintSince     = "since.external"
)

var feedKinds = []string{feedTask, feedNotification, feedIntention, feedMessage, feedWatch}

// happening is one item of the feed.
type happening struct {
	At    time.Time
	Kind  string // feedTask…
	Event string // started, finished…: with Kind, the summary's words
	Ref   string // for drill-down: task:<id>, notification:<id>, intention:<id>, msg:<seq>
	Args  []any  // the summary's details, written by code; a trKey is translated
	Text  string // what it quotes: a prompt, a notification, an intention, a message
	Trust string
}

// trKey is an argument of a summary that is itself a text to translate.
type trKey string

// summary words the item in lang; an external text is quoted only if quote.
func (h happening) summary(lang string, quote bool) string {
	args := make([]any, len(h.Args))
	for i, a := range h.Args {
		if k, ok := a.(trKey); ok {
			a = tr(lang, string(k))
		}
		args[i] = a
	}
	s := fmt.Sprintf(tr(lang, "since."+h.Kind+"."+h.Event), args...)
	switch {
	case h.Text == "":
		return s
	case h.Trust == feedExternal && !quote:
		return s + tr(lang, "since.unquoted")
	}
	return s + fmt.Sprintf(tr(lang, "since.quote"), h.Text)
}

func feedText(s string) string { return truncate(packQuote(s), feedTextMax) }

// feedSource reads one source's items since from, oldest first, at most limit.
type feedSource func(c *core, from time.Time, limit int) ([]happening, error)

var feedSources = map[string]feedSource{
	feedTask:         taskHappenings,
	feedNotification: notificationHappenings,
	feedIntention:    intentionHappenings,
	feedMessage:      messageHappenings,
	feedWatch:        watchHappenings,
}

// feed is what happened since from, of these kinds (all if none), oldest
// first, at most limit; more tells that there is more after the last one.
func (c *core) feed(from time.Time, kinds []string, limit int) (items []happening, more bool, err error) {
	if len(kinds) == 0 {
		kinds = feedKinds
	}
	for _, k := range kinds {
		read, ok := feedSources[k]
		if !ok {
			return nil, false, fmt.Errorf("kind %q is not one of %s", k, strings.Join(feedKinds, ", "))
		}
		got, err := read(c, from, limit+1)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", k, err)
		}
		items = append(items, got...)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At.Before(items[j].At) })
	if len(items) > limit {
		return items[:limit], true, nil
	}
	return items, false, nil
}

// logTask records what the brain is told of a worker task (notifyBrain).
// A state is kept once until it changes; a turn that ended keeps its
// receipt's verdict.
func (c *core) logTask(id, state string, r *checkedReceipt) {
	t, err := c.store.task(id)
	if err != nil || t.Kind != "worker" {
		return
	}
	var last string
	c.store.db.QueryRow(`SELECT state FROM task_log WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, id).Scan(&last)
	if last == state && state != stateIdle {
		return
	}
	var status string
	var verified, open, mismatches int
	if !r.missing() {
		status, verified, open, mismatches = r.Status, len(r.Verified), r.Open(), len(r.Mismatches)
	}
	if _, err := c.store.db.Exec(`INSERT INTO task_log (task_id, at, state, receipt, verified, open, mismatches)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, time.Now().Unix(), state, status, verified, open, mismatches); err != nil {
		log.Printf("since: task log: %v", err)
	}
}

func taskHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	var out []happening
	// Its taint is read in the same query: the store has one connection.
	rows, err := c.store.db.Query(`SELECT t.id, t.agent, t.prompt, t.created, COALESCE(k.value, '') FROM tasks t
		LEFT JOIN kv k ON k.key = 'task_taint:' || t.id
		WHERE t.kind = 'worker' AND t.created >= ? ORDER BY t.created LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, agent, prompt, taint string
		var at int64
		if err := rows.Scan(&id, &agent, &prompt, &at, &taint); err != nil {
			return nil, err
		}
		var who any = agent
		if agent == "" {
			who = trKey("since.agent.default")
		}
		h := happening{At: time.Unix(at, 0), Kind: feedTask, Event: "started", Ref: "task:" + id, Args: []any{id, who},
			Text: feedText(prompt), Trust: feedOwn}
		if taint != "" {
			h.Trust = feedExternal // written on a tainted turn
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	logs, err := c.store.db.Query(`SELECT task_id, at, state, receipt, verified, open, mismatches FROM task_log WHERE at >= ?
		ORDER BY seq LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer logs.Close()
	for logs.Next() {
		var id, state, receipt string
		var at int64
		var verified, open, mismatches int
		if err := logs.Scan(&id, &at, &state, &receipt, &verified, &open, &mismatches); err != nil {
			return nil, err
		}
		h := happening{At: time.Unix(at, 0), Kind: feedTask, Ref: "task:" + id, Args: []any{id}, Trust: feedOwn}
		switch state {
		case stateIdle:
			h.Event = "finished_bare"
			if receipt != "" {
				h.Event = "finished"
				h.Args = append(h.Args, trKey("since.receipt."+receipt), verified, open, mismatches)
			}
		case stateWaitingPermission:
			h.Event = "waiting"
		case stateStopped:
			h.Event = "stopped"
		default:
			h.Event = "failed"
		}
		out = append(out, h)
	}
	return out, logs.Err()
}

func notificationHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	made, err := c.notificationsWhere(`created >= ? ORDER BY id LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	answered, err := c.notificationsWhere(`feedback != '' AND feedback_at >= ? ORDER BY feedback_at LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	var out []happening
	for _, n := range made {
		event := map[string]string{levelNow: "sent", levelBrief: "held", levelNever: "dropped"}[n.Outcome]
		h := happening{At: n.Created, Kind: feedNotification, Event: event, Ref: fmt.Sprintf("notification:%d", n.ID),
			Args: []any{n.ID}, Text: feedText(n.Text), Trust: feedOwn}
		if n.Tainted != "" {
			h.Trust = feedExternal // it may repeat what a tainted turn read
		}
		out = append(out, h)
	}
	for _, n := range answered {
		out = append(out, happening{At: n.FeedbackAt, Kind: feedNotification, Event: "feedback",
			Ref: fmt.Sprintf("notification:%d", n.ID), Args: []any{n.ID, trKey("notif.fb." + n.Feedback)}, Trust: feedOwn})
	}
	return out, nil
}

var intentionEvents = map[string]string{intentDue: "due", intentDone: "done", intentSnoozed: "snoozed", intentCancelled: "cancelled"}

func intentionHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	if c.mem == nil {
		return nil, nil
	}
	rows, err := c.mem.db.Query(`SELECT l.time, l.intention_id, l.to_state, i.text, i.trust
		FROM intention_log l JOIN intentions i ON i.id = l.intention_id
		WHERE l.time >= ? AND l.from_state != l.to_state AND l.to_state IN ('due', 'done', 'snoozed', 'cancelled')
		ORDER BY l.seq LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []happening
	for rows.Next() {
		var at, id int64
		var state, text, trust string
		if err := rows.Scan(&at, &id, &state, &text, &trust); err != nil {
			return nil, err
		}
		h := happening{At: time.Unix(at, 0), Kind: feedIntention, Event: intentionEvents[state],
			Ref: fmt.Sprintf("intention:%d", id), Args: []any{id}, Text: feedText(text), Trust: feedOwn}
		if trust == trustExternal {
			h.Trust = feedExternal
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// messageHappenings are the user's messages and the brain's, short; a brain
// message that went out as a notification is that notification already.
func messageHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	msgs, err := c.store.queryMessages(`SELECT seq, time, role, text, via FROM messages
		WHERE role IN ('user', 'brain') AND time >= ? AND seq NOT IN (SELECT msg FROM notifications WHERE msg IS NOT NULL)
		ORDER BY seq LIMIT ?`, from.Unix(), limit)
	if err != nil {
		return nil, err
	}
	var out []happening
	for _, m := range msgs {
		h := happening{At: m.Time, Kind: feedMessage, Event: m.Role, Ref: fmt.Sprintf("msg:%d", m.Seq),
			Text: feedText(m.Text), Trust: feedOwn}
		if m.Role == roleBrain {
			if t := strings.TrimSpace(m.Text); t == "Prêt." || t == "Prêt" {
				continue // the answer to a start
			}
			h.Args = []any{assistantName()}
			if strings.HasPrefix(c.store.get(taintKey(m.Seq)), "brain:") {
				h.Trust = feedExternal // written in a tainted turn (markBrainMessage)
			}
		} else {
			h.Args = []any{c.conf().User}
		}
		out = append(out, h)
	}
	return out, nil
}

// resolveSince reads t: a time (RFC3339, or YYYY-MM-DD[THH:MM] in the
// user's zone), "away" (when the user left, before their return, or since
// they are away) or "last_brief". It says what it took, for the brain.
func (c *core) resolveSince(t string, now time.Time) (time.Time, string, error) {
	t = strings.TrimSpace(t)
	var from time.Time
	var basis string
	switch t {
	case "":
		return time.Time{}, "", errors.New("t is needed: a time, away or last_brief")
	case sinceAway:
		a, ok := c.lastAbsence(now)
		if !ok {
			return time.Time{}, "", errors.New("the user was not away in the last 30 days (or never seen): give a time")
		}
		from = a.Left
		basis = "since the user left, at " + a.Left.In(now.Location()).Format("2006-01-02 15:04")
		if a.Back.IsZero() {
			basis += "; they are still away"
		} else {
			basis += "; back at " + a.Back.In(now.Location()).Format("2006-01-02 15:04")
		}
	case sinceLastBrief:
		var unix int64
		if _, err := fmt.Sscan(c.store.get(lastBriefKey), &unix); err != nil || unix <= 0 {
			return time.Time{}, "", errors.New("no brief has been given yet: use away or a time")
		}
		from, basis = time.Unix(unix, 0), "since the last brief"
	default:
		var err error
		if from, err = time.Parse(time.RFC3339, t); err != nil {
			if from, err = time.ParseInLocation("2006-01-02T15:04", strings.Replace(t, " ", "T", 1), now.Location()); err != nil {
				if from, err = time.ParseInLocation(time.DateOnly, t, now.Location()); err != nil {
					return time.Time{}, "", errors.New("t is an RFC3339 time, YYYY-MM-DD, YYYY-MM-DDTHH:MM, away or last_brief")
				}
			}
		}
	}
	switch {
	case from.After(now):
		return time.Time{}, "", errors.New("t is in the future")
	case now.Sub(from) > activityLookback:
		return time.Time{}, "", errors.New("t is at most 30 days ago")
	}
	return from, basis, nil
}

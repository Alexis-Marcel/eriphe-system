package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Typed intentions: what is owed, by whom, by when, and when to say so.
// Prospective memory as "schema-constrained state tracking" (Typed
// Intention Stores, arXiv 2609.01272): the model only Forms intentions from
// text (the extraction, the brain's tools) and Revises them with typed
// patches (done, cancelled, rescheduled); the lifecycle is code. Every
// minute, without a model, open intentions whose reminder time has come,
// and snoozed ones whose snooze is over, become due (tickIntentions); an
// intention whose cue is a person becomes due when my message names them
// (cueIntentions). A due intention wakes the brain once through the clock
// trigger (brain_wake.go), and once more at its deadline if still due.
//
//	proposed ──approve──▶ open ──remind_at──▶ due ──▶ done / cancelled
//	    └──reject──▶ cancelled      │            └──snooze──▶ snoozed ──until──▶ due
//	                                └──▶ done / cancelled / snoozed
//
// An intention from external content (a mail that says "pay before
// Friday") stays proposed until I approve it; approved, it may remind,
// never act: the turn that reads it is tainted.

const (
	intentPromise    = "promise"     // I owe someone
	intentCommitment = "commitment"  // the assistant owes me
	intentWaiting    = "waiting_for" // someone owes me
	intentReminder   = "reminder"    // something to remember to do

	triggerTime   = "time"   // remind_at
	triggerPerson = "person" // the next time my message names cue
	triggerNone   = "none"   // listed, never woken for

	intentProposed  = "proposed"
	intentOpen      = "open"
	intentDue       = "due"
	intentSnoozed   = "snoozed"
	intentDone      = "done"
	intentCancelled = "cancelled"

	intentTextMax = 500
)

var (
	intentKinds    = []string{intentPromise, intentCommitment, intentWaiting, intentReminder}
	intentTriggers = []string{triggerTime, triggerPerson, triggerNone}
	intentLive     = []string{intentProposed, intentOpen, intentDue, intentSnoozed}
	// What may follow what; the minute timer moves open and snoozed to due.
	intentMoves = map[string][]string{
		intentProposed:  {intentOpen, intentCancelled},
		intentOpen:      {intentDue, intentDone, intentCancelled, intentSnoozed},
		intentDue:       {intentDone, intentCancelled, intentSnoozed, intentOpen},
		intentSnoozed:   {intentDue, intentDone, intentCancelled, intentOpen},
		intentDone:      {intentOpen},
		intentCancelled: {intentOpen},
	}
)

// Intention is one thing owed or to remember, with its lifecycle.
type Intention struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Text         string    `json:"text"`
	Owner        string    `json:"owner"`             // who owes: owner, assistant, person:<slug>
	OwedTo       string    `json:"owed_to,omitempty"` // to whom
	Subject      string    `json:"subject,omitempty"`
	Trigger      string    `json:"trigger"`
	Cue          string    `json:"cue,omitempty"` // for a person trigger: the name(s) that bring it up
	DueAt        time.Time `json:"due_at,omitzero"`
	RemindAt     time.Time `json:"remind_at,omitzero"`
	State        string    `json:"state"`
	SnoozedUntil time.Time `json:"snoozed_until,omitzero"`
	SurfacedAt   time.Time `json:"surfaced_at,omitzero"`
	Surfaced     int       `json:"surfaced,omitempty"`
	NextCheckAt  time.Time `json:"next_check_at,omitzero"`
	SourceKind   string    `json:"source_kind"`
	SourceRef    string    `json:"source_ref,omitempty"`
	Trust        string    `json:"trust"`
	Basis        string    `json:"basis,omitempty"`
	MemoryID     int64     `json:"memory_id,omitempty"` // the commitment memory it came from
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ClosedAt     time.Time `json:"closed_at,omitzero"`
	Flags        string    `json:"flags,omitempty"`
}

// Overdue tells whether its deadline has passed while it is still owed.
func (x *Intention) Overdue(now time.Time) bool {
	return !x.DueAt.IsZero() && x.DueAt.Before(now) && containsStr([]string{intentOpen, intentDue, intentSnoozed}, x.State)
}

const intentionsSchema = `CREATE TABLE intentions (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  kind TEXT NOT NULL, text TEXT NOT NULL, owner TEXT NOT NULL DEFAULT 'owner', owed_to TEXT NOT NULL DEFAULT '',
	  subject TEXT NOT NULL DEFAULT '', trigger TEXT NOT NULL DEFAULT 'time', cue TEXT NOT NULL DEFAULT '',
	  due_at INTEGER, remind_at INTEGER, state TEXT NOT NULL, snoozed_until INTEGER,
	  surfaced_at INTEGER, surfaced INTEGER NOT NULL DEFAULT 0, next_check_at INTEGER,
	  source_kind TEXT NOT NULL, source_ref TEXT NOT NULL DEFAULT '', trust TEXT NOT NULL, basis TEXT NOT NULL DEFAULT '',
	  memory_id INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, closed_at INTEGER,
	  flags TEXT NOT NULL DEFAULT '');
	CREATE INDEX intentions_state ON intentions (state, next_check_at);
	CREATE TABLE intention_log (
	  seq INTEGER PRIMARY KEY AUTOINCREMENT, intention_id INTEGER NOT NULL, time INTEGER NOT NULL,
	  who TEXT NOT NULL, op TEXT NOT NULL, from_state TEXT NOT NULL DEFAULT '', to_state TEXT NOT NULL DEFAULT '',
	  detail TEXT NOT NULL DEFAULT '');
	CREATE INDEX intention_log_intention ON intention_log (intention_id);
	CREATE TRIGGER intention_log_append_only BEFORE UPDATE ON intention_log BEGIN
	  SELECT RAISE(ABORT, 'intention_log is append-only'); END;`

// migrateIntentions (schema 4) creates the table, and moves the commitment
// memories into it: their state kept (dropped is cancelled), each memory
// archived with a log entry naming its intention. Nothing is deleted.
func migrateIntentions(tx *sql.Tx) error {
	if _, err := tx.Exec(intentionsSchema); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT ` + memColumns + ` FROM memories WHERE kind = 'commitment' AND status IN ('active', 'proposed')`)
	if err != nil {
		return err
	}
	var olds []*Memory
	for rows.Next() {
		x, err := scanMemoryWith(rows)
		if err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, x)
	}
	rows.Close()
	now := time.Now()
	for _, x := range olds {
		in := &Intention{Kind: intentReminder, Text: x.Text, Owner: "owner", Subject: x.Subject, DueAt: x.DueAt,
			SourceKind: x.SourceKind, SourceRef: x.SourceRef, Trust: x.Trust, Basis: x.Basis, MemoryID: x.ID, Flags: x.Flags}
		switch {
		case x.Status == memProposed:
			in.State = intentProposed
		case x.State == stateDone:
			in.State = intentDone
		case x.State == stateDropped:
			in.State = intentCancelled
		default:
			in.State = intentOpen
		}
		if !x.Current(now) && in.State == intentOpen {
			in.State = intentCancelled // no longer true: it was closed by valid_to
		}
		if err := normalizeIntention(in, now); err != nil {
			in.Text = truncate(in.Text, intentTextMax-1)
			if err := normalizeIntention(in, now); err != nil {
				return fmt.Errorf("commitment %d: %w", x.ID, err)
			}
		}
		if err := insertIntention(tx, in, now, "migration", fmt.Sprintf("from commitment memory %d", x.ID)); err != nil {
			return err
		}
		after := *x
		after.Status, after.UpdatedAt = memArchived, now
		if _, err := tx.Exec(`UPDATE memories SET status = 'archived', updated_at = ? WHERE id = ?`, now.Unix(), x.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO memory_log (memory_id, time, who, op, before, after, reason) VALUES (?, ?, 'eriphe-core', 'archive', ?, ?, ?)`,
			x.ID, now.Unix(), memoryJSON(x), memoryJSON(&after), fmt.Sprintf("moved to intention %d", in.ID)); err != nil {
			return err
		}
	}
	return nil
}

// defaultRemind is when to remind of a deadline nobody gave a time for: a
// deadline on a day (midnight) is reminded at 9:00 that day; one at a time,
// at that time.
func defaultRemind(due time.Time) time.Time {
	if due.IsZero() {
		return due
	}
	if due.Hour() == 0 && due.Minute() == 0 {
		return due.Add(9 * time.Hour)
	}
	return due
}

// normalizeIntention checks an intention and fills what follows from it.
func normalizeIntention(x *Intention, now time.Time) error {
	x.Text = strings.TrimSpace(x.Text)
	x.Kind = strings.TrimSpace(x.Kind)
	if x.Kind == "" {
		x.Kind = intentReminder
	}
	if !containsStr(intentKinds, x.Kind) {
		return fmt.Errorf("kind is %s", strings.Join(intentKinds, ", "))
	}
	if x.Text == "" {
		return errors.New("text is required")
	}
	if n := utf8.RuneCountInString(x.Text); n > intentTextMax {
		return fmt.Errorf("text is %d characters, at most %d", n, intentTextMax)
	}
	x.Owner = normalizeSubject(x.Owner)
	if x.Owner == "" {
		x.Owner = "owner"
		if x.Kind == intentCommitment {
			x.Owner = "assistant"
		}
	}
	x.OwedTo, x.Subject = normalizeSubject(x.OwedTo), normalizeSubject(x.Subject)
	x.Cue = strings.Join(strings.Fields(x.Cue), " ")
	if x.Trigger == "" {
		switch {
		case x.Cue != "":
			x.Trigger = triggerPerson
		case !x.DueAt.IsZero() || !x.RemindAt.IsZero():
			x.Trigger = triggerTime
		default:
			x.Trigger = triggerNone
		}
	}
	if !containsStr(intentTriggers, x.Trigger) {
		return fmt.Errorf("trigger is %s", strings.Join(intentTriggers, ", "))
	}
	if x.Trigger == triggerPerson && x.Cue == "" {
		return errors.New("a person trigger needs a cue: the name that brings it up")
	}
	if x.Trigger == triggerTime && x.RemindAt.IsZero() {
		x.RemindAt = defaultRemind(x.DueAt)
	}
	if x.Trigger == triggerTime && x.RemindAt.IsZero() {
		return errors.New("a time trigger needs due_at or remind_at")
	}
	if x.State == "" {
		x.State = intentOpen
	}
	if _, ok := intentMoves[x.State]; !ok {
		return errors.New("unknown state " + x.State)
	}
	if x.Trust == "" || x.SourceKind == "" {
		return errors.New("an intention says where it comes from")
	}
	// When the timer looks at it next: its reminder, or nothing.
	x.NextCheckAt = time.Time{}
	if x.State == intentOpen && x.Trigger == triggerTime {
		x.NextCheckAt = x.RemindAt
	}
	return nil
}

func insertIntention(q querier, x *Intention, now time.Time, who, detail string) error {
	x.CreatedAt, x.UpdatedAt = now, now
	res, err := q.Exec(`INSERT INTO intentions (kind, text, owner, owed_to, subject, trigger, cue, due_at, remind_at, state, snoozed_until,
		next_check_at, source_kind, source_ref, trust, basis, memory_id, created_at, updated_at, flags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.Kind, x.Text, x.Owner, x.OwedTo, x.Subject, x.Trigger, x.Cue, unixOrNil(x.DueAt), unixOrNil(x.RemindAt), x.State,
		unixOrNil(x.SnoozedUntil), unixOrNil(x.NextCheckAt), x.SourceKind, x.SourceRef, x.Trust, x.Basis, nullID(x.MemoryID),
		now.Unix(), now.Unix(), x.Flags)
	if err != nil {
		return err
	}
	x.ID, _ = res.LastInsertId()
	return intentionLog(q, x.ID, now, who, "add", "", x.State, detail)
}

func intentionLog(q querier, id int64, now time.Time, who, op, from, to, detail string) error {
	_, err := q.Exec(`INSERT INTO intention_log (intention_id, time, who, op, from_state, to_state, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, now.Unix(), who, op, from, to, detail)
	return err
}

const intentionColumns = `id, kind, text, owner, owed_to, subject, trigger, cue, due_at, remind_at, state, snoozed_until, surfaced_at,
	surfaced, next_check_at, source_kind, source_ref, trust, basis, memory_id, created_at, updated_at, closed_at, flags`

func scanIntention(sc interface{ Scan(...any) error }) (*Intention, error) {
	var x Intention
	var due, remind, snoozed, surfaced, next, memID, closed sql.NullInt64
	var created, updated int64
	if err := sc.Scan(&x.ID, &x.Kind, &x.Text, &x.Owner, &x.OwedTo, &x.Subject, &x.Trigger, &x.Cue, &due, &remind, &x.State,
		&snoozed, &surfaced, &x.Surfaced, &next, &x.SourceKind, &x.SourceRef, &x.Trust, &x.Basis, &memID, &created, &updated,
		&closed, &x.Flags); err != nil {
		return nil, err
	}
	x.DueAt, x.RemindAt, x.SnoozedUntil, x.SurfacedAt = fromNull(due), fromNull(remind), fromNull(snoozed), fromNull(surfaced)
	x.NextCheckAt, x.ClosedAt = fromNull(next), fromNull(closed)
	x.MemoryID, x.CreatedAt, x.UpdatedAt = memID.Int64, time.Unix(created, 0), time.Unix(updated, 0)
	return &x, nil
}

var errNoIntention = errors.New("no such intention")

func (m *memStore) intention(id int64) (*Intention, error) {
	x, err := scanIntention(m.db.QueryRow(`SELECT `+intentionColumns+` FROM intentions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoIntention
	}
	return x, err
}

// intentions lists those in the given states (the live ones by default),
// the due first, then by deadline.
func (m *memStore) intentions(states ...string) ([]*Intention, error) {
	if len(states) == 0 {
		states = intentLive
	}
	args := []any{}
	for _, s := range states {
		args = append(args, s)
	}
	rows, err := m.db.Query(`SELECT `+intentionColumns+` FROM intentions WHERE state IN (`+placeholders(len(states))+`)
		ORDER BY state != 'due', due_at IS NULL, due_at, remind_at, id LIMIT 300`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Intention
	for rows.Next() {
		x, err := scanIntention(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// addIntention records an intention, unless the same one is already live
// (then that one is returned).
func (m *memStore) addIntention(x *Intention, who string) (*Intention, error) {
	now := m.now()
	if err := normalizeIntention(x, now); err != nil {
		return nil, err
	}
	var id int64
	m.db.QueryRow(`SELECT id FROM intentions WHERE LOWER(TRIM(text)) = LOWER(TRIM(?)) AND state IN ('proposed', 'open', 'due', 'snoozed')
		LIMIT 1`, x.Text).Scan(&id)
	if id != 0 {
		return m.intention(id)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := insertIntention(tx, x, now, who, ""); err != nil {
		return nil, err
	}
	return x, tx.Commit()
}

// intentionChange is a typed patch: a move to another state, a snooze, a
// new deadline or reminder, a corrected text.
type intentionChange struct {
	State    string
	Until    time.Time // for snoozed
	DueAt    *time.Time
	RemindAt *time.Time
	Text     *string
}

// changeIntention applies a patch, if the lifecycle allows it.
func (m *memStore) changeIntention(id int64, ch intentionChange, who, why string) (*Intention, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	x, err := scanIntention(tx.QueryRow(`SELECT `+intentionColumns+` FROM intentions WHERE id = ?`, id))
	if err != nil {
		return nil, errNoIntention
	}
	now := m.now()
	from := x.State
	var detail []string
	if ch.Text != nil {
		x.Text = *ch.Text
		detail = append(detail, "text")
	}
	rescheduled := false
	if ch.DueAt != nil {
		x.DueAt = *ch.DueAt
		if ch.RemindAt == nil {
			x.RemindAt = defaultRemind(x.DueAt)
		}
		rescheduled = true
		detail = append(detail, "due "+x.DueAt.Format("2006-01-02 15:04"))
	}
	if ch.RemindAt != nil {
		x.RemindAt = *ch.RemindAt
		rescheduled = true
		detail = append(detail, "remind "+x.RemindAt.Format("2006-01-02 15:04"))
	}
	to := ch.State
	if to == "" && rescheduled && containsStr([]string{intentDue, intentSnoozed}, x.State) {
		to = intentOpen // a new time: it waits for it again
	}
	if to != "" && to != x.State {
		if !containsStr(intentMoves[x.State], to) {
			return nil, fmt.Errorf("intention %d is %s: it cannot become %s", id, x.State, to)
		}
		x.State = to
	}
	switch x.State {
	case intentSnoozed:
		if !ch.Until.After(now) {
			return nil, errors.New("a snooze needs a time to come back, in the future")
		}
		x.SnoozedUntil = ch.Until
		detail = append(detail, "until "+ch.Until.Format("2006-01-02 15:04"))
	case intentOpen:
		x.SnoozedUntil, x.SurfacedAt, x.Surfaced = time.Time{}, time.Time{}, 0
	}
	if x.State == intentDone || x.State == intentCancelled {
		x.ClosedAt = now
	} else {
		x.ClosedAt = time.Time{}
	}
	if err := normalizeIntention(x, now); err != nil {
		return nil, err
	}
	if x.State == intentSnoozed {
		x.NextCheckAt = x.SnoozedUntil
	}
	if x.State == intentOpen && x.Trigger == triggerTime && !x.RemindAt.After(now) {
		x.State, x.NextCheckAt = intentDue, time.Time{} // its time has already come
	}
	x.UpdatedAt = now
	if _, err := tx.Exec(`UPDATE intentions SET kind = ?, text = ?, due_at = ?, remind_at = ?, state = ?, snoozed_until = ?, surfaced_at = ?,
		surfaced = ?, next_check_at = ?, updated_at = ?, closed_at = ? WHERE id = ?`, x.Kind, x.Text, unixOrNil(x.DueAt), unixOrNil(x.RemindAt),
		x.State, unixOrNil(x.SnoozedUntil), unixOrNil(x.SurfacedAt), x.Surfaced, unixOrNil(x.NextCheckAt), now.Unix(), unixOrNil(x.ClosedAt), id); err != nil {
		return nil, err
	}
	if why != "" {
		detail = append(detail, why)
	}
	if err := intentionLog(tx, id, now, who, "change", from, x.State, strings.Join(detail, "; ")); err != nil {
		return nil, err
	}
	return x, tx.Commit()
}

// tickIntentions is the minute timer's work, without a model: open
// intentions whose reminder has come and snoozed ones whose snooze is over
// become due. It returns those that just did.
func (m *memStore) tickIntentions() ([]*Intention, error) {
	now := m.now()
	rows, err := m.db.Query(`SELECT id FROM intentions WHERE state IN ('open', 'snoozed') AND trigger != 'person'
		AND next_check_at IS NOT NULL AND next_check_at <= ?`, now.Unix())
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []*Intention
	for _, id := range ids {
		x, err := m.becomeDue(id, "clock", "its time came")
		if err != nil {
			return out, err
		}
		out = append(out, x)
	}
	return out, nil
}

// becomeDue moves a live intention to due; its deadline, if later, is the
// next time it is said again.
func (m *memStore) becomeDue(id int64, who, why string) (*Intention, error) {
	now := m.now()
	x, err := m.intention(id)
	if err != nil {
		return nil, err
	}
	from := x.State
	if !containsStr([]string{intentOpen, intentSnoozed}, from) {
		return x, nil
	}
	x.State, x.SnoozedUntil, x.SurfacedAt, x.NextCheckAt, x.UpdatedAt = intentDue, time.Time{}, time.Time{}, time.Time{}, now
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE intentions SET state = 'due', snoozed_until = NULL, surfaced_at = NULL, next_check_at = NULL, updated_at = ?
		WHERE id = ? AND state = ?`, now.Unix(), id, from); err != nil {
		return nil, err
	}
	if err := intentionLog(tx, id, now, who, "due", from, intentDue, why); err != nil {
		return nil, err
	}
	return x, tx.Commit()
}

// toSurface are the due intentions the brain has not been told of yet, or
// whose deadline has come since it was.
func (m *memStore) toSurface() ([]*Intention, error) {
	now := m.now()
	rows, err := m.db.Query(`SELECT `+intentionColumns+` FROM intentions WHERE state = 'due'
		AND (surfaced_at IS NULL OR (next_check_at IS NOT NULL AND next_check_at <= ?)) ORDER BY due_at IS NULL, due_at, id`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Intention
	for rows.Next() {
		x, err := scanIntention(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// surfaced records that the brain was told: once more at the deadline if
// it is later, else never again (it stays due, listed, until closed).
func (m *memStore) surfaced(xs []*Intention, how string) {
	now := m.now()
	for _, x := range xs {
		var next any
		if x.DueAt.After(now) {
			next = x.DueAt.Unix()
		}
		m.db.Exec(`UPDATE intentions SET surfaced_at = ?, surfaced = surfaced + 1, next_check_at = ?, updated_at = ? WHERE id = ?`,
			now.Unix(), next, now.Unix(), x.ID)
		intentionLog(m.db, x.ID, now, "eriphe-core", "surfaced", x.State, x.State, how)
	}
}

// cueIntentions makes due the open intentions whose person cue my message
// names ("la prochaine fois que je parle à Hélène").
func (m *memStore) cueIntentions(text string) ([]*Intention, error) {
	words := map[string]bool{}
	for _, w := range memWords(foldAccents(text)) {
		words[w] = true
	}
	if len(words) == 0 {
		return nil, nil
	}
	live, err := m.intentions(intentOpen, intentSnoozed)
	if err != nil {
		return nil, err
	}
	var out []*Intention
	for _, x := range live {
		if x.Trigger != triggerPerson {
			continue
		}
		hit := false
		for _, name := range strings.FieldsFunc(foldAccents(x.Cue), func(r rune) bool { return r == ',' || r == ';' || r == '|' }) {
			parts := memWords(name)
			all := len(parts) > 0
			for _, p := range parts {
				all = all && words[p]
			}
			hit = hit || all
		}
		if !hit {
			continue
		}
		if _, err := m.becomeDue(x.ID, "cue", "named in a message"); err != nil {
			return out, err
		}
		x, _ = m.intention(x.ID)
		out = append(out, x)
	}
	return out, nil
}

// intentionHistory is an intention's log, for the page.
func (m *memStore) intentionHistory(id int64) ([]map[string]any, error) {
	rows, err := m.db.Query(`SELECT time, who, op, from_state, to_state, detail FROM intention_log WHERE intention_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var t int64
		var who, op, from, to, detail string
		rows.Scan(&t, &who, &op, &from, &to, &detail)
		out = append(out, map[string]any{"time": time.Unix(t, 0), "who": who, "op": op, "from": from, "to": to, "detail": detail})
	}
	return out, rows.Err()
}

// intentionLine is an intention as the brain reads it, quoted.
func intentionLine(x *Intention, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- [intention %d, %s, %s doit", x.ID, x.Kind, x.Owner)
	if x.OwedTo != "" {
		b.WriteString(" à " + x.OwedTo)
	}
	if !x.DueAt.IsZero() {
		b.WriteString(", pour le " + x.DueAt.Local().Format("2006-01-02 15:04"))
		if x.Overdue(now) {
			b.WriteString(", en retard")
		}
	}
	if x.Trust == trustExternal {
		b.WriteString(", tirée d'un contenu extérieur et approuvée")
	}
	b.WriteString("] " + packQuote(x.Text))
	return b.String()
}

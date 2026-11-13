package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// The learning journal: what Eriphe learned, said in one place, each entry
// undoable. Every change to a memory that steers how Eriphe behaves (a
// lesson, a preference, a decision, the profile) and every change of a
// skill's approved version is an entry: what changed, how (added, changed,
// removed, reverted), where it comes from (its provenance), how risky it is,
// why, the evidence it rests on (msg:N, task:<id>), and its status.
//
// The entry points at the data rather than copying it: a memory's versions
// are the memories themselves (supersede) and memory_log's before/after
// (an update), a skill's versions are its bundles (skills.go), kept a while
// after they are replaced so that an entry can be undone. Undoing an entry
// is itself an entry (reverted), and the undone one is marked reverted.
//
// The statuses are those of the learning loop: trial (in force, not
// confirmed), confirmed (the user's own, or approved by them), dormant (in
// force but unused; set by the consolidation, later) and reverted. The
// memory's writes are recorded in their own transaction (memory.go); the
// skills' after their change (skills are in eriphe-core's database).

const learningSchema = `CREATE TABLE IF NOT EXISTS learning_log (
	  id INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL,
	  kind TEXT NOT NULL,
	  memory_id INTEGER, prev_id INTEGER, log_seq INTEGER,
	  skill TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', prev_version TEXT NOT NULL DEFAULT '',
	  action TEXT NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL, risk TEXT NOT NULL,
	  reason TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL DEFAULT '[]', who TEXT NOT NULL,
	  reverts INTEGER, undo TEXT NOT NULL DEFAULT '', undo_why TEXT NOT NULL DEFAULT '');
	CREATE INDEX IF NOT EXISTS learning_log_time ON learning_log (time);
	CREATE INDEX IF NOT EXISTS learning_log_memory ON learning_log (memory_id);
	CREATE TRIGGER IF NOT EXISTS learning_log_fixed BEFORE UPDATE OF time, kind, memory_id, prev_id, log_seq, skill, version, prev_version,
	  action, source, risk, reason, evidence, who, reverts ON learning_log BEGIN
	  SELECT RAISE(ABORT, 'a learning entry is fixed: only its status changes'); END;`

const (
	learnAdded    = "added"
	learnChanged  = "changed"
	learnRemoved  = "removed"
	learnReverted = "reverted" // an action, and a status

	learnTrial     = "trial"
	learnConfirmed = "confirmed"
	learnDormant   = "dormant"

	// Provenance: where what was learned comes from.
	learnFromUser          = "user"       // the user's own message or hand
	learnFromBrain         = "brain"      // Eriphe's own conclusion, in a clean turn
	learnFromTask          = "task"       // a task's verified receipt
	learnFromExternal      = "external"   // read in a mail, a page, a task's content
	learnFromExtraction    = "extraction" // the nightly extraction of the conversation
	learnFromConsolidation = "consolidation"

	riskLow  = "low"
	riskHigh = "high"

	kindSkill = "skill"

	undoProposed = "proposed" // the brain proposed to undo it, in a tainted turn
	undoRejected = "rejected"

	// whoUndo prefixes who undoes an entry, in memory_log: its own change
	// is the revert entry, not a new one.
	whoUndo = "undo:"

	learnedMax = 200
)

var (
	learnStatuses = []string{learnTrial, learnConfirmed, learnDormant, learnReverted}
	learnKinds    = []string{kindProfile, kindPreference, kindLesson, kindDecision, kindSkill}
)

// learnEntry is one entry of the journal; the fields after Undo are read
// with it, from the memory it points at.
type learnEntry struct {
	ID          int64
	At          time.Time
	Kind        string // the memory's kind, or skill
	MemoryID    int64  // the memory this entry wrote (or removed)
	PrevID      int64  // the memory it replaced
	LogSeq      int64  // the memory_log row holding the state before
	Skill       string
	Version     string // the skill's version in force after it ("" once removed)
	PrevVersion string // and before it
	Action      string
	Status      string
	Source      string
	Risk        string
	Reason      string
	Evidence    []string
	Who         string
	Reverts     int64
	Undo        string // proposed or rejected
	UndoWhy     string

	Subject string
	Text    string // the memory's text as this entry left it
	Before  string // its text before, for a change
	Trust   string
	Gone    bool // the memory was erased for good
}

// Target names what the entry is about, for the brain and the page.
func (e *learnEntry) Target() string {
	if e.Kind == kindSkill {
		return "skill " + e.Skill
	}
	return fmt.Sprintf("memory #%d", e.MemoryID)
}

// learnable tells whether a change to these memories is something learned:
// it steers how Eriphe behaves.
func learnable(xs ...*Memory) bool {
	for _, x := range xs {
		if x == nil {
			continue
		}
		if x.Pinned || x.Kind == kindProfile || x.Kind == kindPreference || x.Kind == kindLesson || x.Kind == kindDecision {
			return true
		}
	}
	return false
}

// memoryRisk is a first, simple risk class: what is in the profile or is a
// lesson steers every turn; the rest is narrower.
func memoryRisk(xs ...*Memory) string {
	for _, x := range xs {
		if x != nil && (x.Pinned || x.Kind == kindProfile || x.Kind == kindLesson) {
			return riskHigh
		}
	}
	return riskLow
}

// memorySource is where a memory comes from, as the journal says it.
func memorySource(x *Memory) string {
	switch {
	case x.Trust == trustExternal:
		return learnFromExternal
	case x.Trust == trustOwner || x.SourceKind == srcOwner:
		return learnFromUser
	case x.SourceKind == srcTask || strings.HasPrefix(x.SourceRef, "task:"):
		return learnFromTask
	case x.SourceKind == srcExtraction:
		return learnFromExtraction
	}
	return learnFromBrain
}

// changeSource is where a change to a memory comes from: the user's hand
// (the workspace) or message, or who made it.
func changeSource(who, ref string) string {
	switch {
	case who == "owner" || strings.HasPrefix(ref, "msg:"):
		return learnFromUser
	case strings.HasPrefix(who, "extraction"):
		return learnFromExtraction
	}
	return learnFromBrain
}

func learnStatus(source, who string) string {
	if source == learnFromUser || who == "owner" {
		return learnConfirmed
	}
	return learnTrial
}

// refs are the evidence among these: msg:N, task:<id>, and the like.
func refs(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); strings.Contains(x, ":") && !containsStr(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func (m *memStore) lastLogSeq(q querier, id int64) int64 {
	var seq int64
	q.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM memory_log WHERE memory_id = ?`, id).Scan(&seq)
	return seq
}

func (m *memStore) insertLearning(q querier, e *learnEntry) error {
	ev, _ := json.Marshal(e.Evidence)
	if e.Evidence == nil {
		ev = []byte("[]")
	}
	e.At = m.now()
	res, err := q.Exec(`INSERT INTO learning_log (time, kind, memory_id, prev_id, log_seq, skill, version, prev_version, action, status,
		source, risk, reason, evidence, who, reverts) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.Unix(), e.Kind, nullID(e.MemoryID), nullID(e.PrevID), nullID(e.LogSeq), e.Skill, e.Version, e.PrevVersion, e.Action,
		e.Status, e.Source, e.Risk, truncate(e.Reason, 300), string(ev), e.Who, nullID(e.Reverts))
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// learnWrite records a memory made active: added, or replacing old.
func (m *memStore) learnWrite(q querier, x, old *Memory, who, reason string) error {
	if strings.HasPrefix(who, whoUndo) || !learnable(x, old) {
		return nil
	}
	src := memorySource(x)
	e := &learnEntry{Kind: x.Kind, MemoryID: x.ID, Action: learnAdded, Source: src, Status: learnStatus(src, who),
		Risk: memoryRisk(x, old), Reason: reason, Evidence: refs(x.SourceRef), Who: who, LogSeq: m.lastLogSeq(q, x.ID)}
	if old != nil {
		e.Action, e.PrevID, e.LogSeq = learnChanged, old.ID, m.lastLogSeq(q, old.ID) // its supersede: the old one as it was
	}
	return m.insertLearning(q, e)
}

// learnUpdate records a change in place.
func (m *memStore) learnUpdate(q querier, before, after *Memory, who, reason, ref string) error {
	if strings.HasPrefix(who, whoUndo) || !learnable(before, after) || before.Status == memProposed && after.Status == memProposed {
		return nil
	}
	src := changeSource(who, ref)
	return m.insertLearning(q, &learnEntry{Kind: after.Kind, MemoryID: after.ID, LogSeq: m.lastLogSeq(q, after.ID), Action: learnChanged,
		Source: src, Status: learnStatus(src, who), Risk: memoryRisk(before, after), Reason: reason, Evidence: refs(ref), Who: who})
}

// learnForget records a memory in force forgotten (a proposal rejected was
// never learned).
func (m *memStore) learnForget(q querier, before *Memory, who, reason, ref string) error {
	if strings.HasPrefix(who, whoUndo) || !learnable(before) || before.Status != memActive {
		return nil
	}
	src := changeSource(who, ref)
	return m.insertLearning(q, &learnEntry{Kind: before.Kind, MemoryID: before.ID, LogSeq: m.lastLogSeq(q, before.ID), Action: learnRemoved,
		Source: src, Status: learnStatus(src, who), Risk: memoryRisk(before), Reason: reason, Evidence: refs(ref), Who: who})
}

// learnSkill records a change of a skill's approved version.
func (m *memStore) learnSkill(name, version, prev, who, reason, risk string) (*learnEntry, error) {
	action := learnChanged
	switch {
	case prev == "":
		action = learnAdded
	case version == "":
		action = learnRemoved
	}
	e := &learnEntry{Kind: kindSkill, Skill: name, Version: version, PrevVersion: prev, Action: action, Source: learnFromUser,
		Status: learnConfirmed, Risk: risk, Reason: reason, Who: who}
	return e, m.insertLearning(m.db, e)
}

const learnColumns = `l.id, l.time, l.kind, COALESCE(l.memory_id, 0), COALESCE(l.prev_id, 0), COALESCE(l.log_seq, 0), l.skill, l.version,
	l.prev_version, l.action, l.status, l.source, l.risk, l.reason, l.evidence, l.who, COALESCE(l.reverts, 0), l.undo, l.undo_why,
	COALESCE(x.subject, ''), COALESCE(x.text, ''), COALESCE(x.trust, ''), x.id IS NULL`

func scanLearning(sc interface{ Scan(...any) error }) (*learnEntry, error) {
	var e learnEntry
	var at int64
	var ev string
	err := sc.Scan(&e.ID, &at, &e.Kind, &e.MemoryID, &e.PrevID, &e.LogSeq, &e.Skill, &e.Version, &e.PrevVersion, &e.Action, &e.Status,
		&e.Source, &e.Risk, &e.Reason, &ev, &e.Who, &e.Reverts, &e.Undo, &e.UndoWhy, &e.Subject, &e.Text, &e.Trust, &e.Gone)
	if err != nil {
		return nil, err
	}
	e.At = time.Unix(at, 0)
	json.Unmarshal([]byte(ev), &e.Evidence)
	e.Gone = e.Gone && e.Kind != kindSkill
	return &e, nil
}

// learnFilter is what learned lists.
type learnFilter struct {
	Kinds    []string
	Statuses []string
	Subject  string
	Query    string // words of the topic: any of them in the text, the subject or the skill's name
	Since    time.Time
	Until    time.Time
	AfterID  int64
	NotWho   string // leave out what this one did (the user's own hand, for the brief)
	Limit    int
}

var errNoLearning = errors.New("no such learning entry")

// learnings lists the journal, newest first.
func (m *memStore) learnings(f learnFilter) ([]*learnEntry, error) {
	where, args := []string{"1 = 1"}, []any{}
	in := func(col string, vals []string) {
		if len(vals) > 0 {
			where = append(where, col+` IN (`+placeholders(len(vals))+`)`)
			for _, v := range vals {
				args = append(args, v)
			}
		}
	}
	in("l.kind", f.Kinds)
	in("l.status", f.Statuses)
	if s, _, _ := subjectFilter(f.Subject); s != "" {
		where = append(where, `(x.subject = ? OR x.subject LIKE ? ESCAPE '\')`)
		args = append(args, s, likeEscape(strings.TrimSuffix(s, ":"))+":%")
	}
	if !f.Since.IsZero() {
		where, args = append(where, `l.time >= ?`), append(args, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		where, args = append(where, `l.time <= ?`), append(args, f.Until.Unix())
	}
	if f.AfterID > 0 {
		where, args = append(where, `l.id > ?`), append(args, f.AfterID)
	}
	if f.NotWho != "" {
		where, args = append(where, `l.who != ?`), append(args, f.NotWho)
	}
	limit := f.Limit
	if limit <= 0 || limit > learnedMax {
		limit = learnedMax
	}
	words := memWords(foldAccents(f.Query))
	// A topic is filtered here, on the few entries a journal holds.
	sqlLimit := limit
	if len(words) > 0 {
		sqlLimit = learnedMax * 5
	}
	rows, err := m.db.Query(`SELECT `+learnColumns+` FROM learning_log l LEFT JOIN memories x ON x.id = l.memory_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY l.id DESC LIMIT ?`, append(args, sqlLimit)...)
	if err != nil {
		return nil, err
	}
	var out []*learnEntry
	for rows.Next() {
		e, err := scanLearning(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if len(words) > 0 && !matchesAny(foldAccents(strings.ToLower(e.Text+" "+e.Subject+" "+e.Skill)), words) {
			continue
		}
		if out = append(out, e); len(out) == limit {
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, e := range out {
		m.fillBefore(e)
	}
	return out, nil
}

func matchesAny(text string, words []string) bool {
	for _, w := range words {
		if !queryStop(w) && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// fillBefore reads what a change replaced: the memory it superseded, or
// the state before its update.
func (m *memStore) fillBefore(e *learnEntry) {
	if e.Kind == kindSkill || e.Action != learnChanged {
		return
	}
	if e.PrevID != 0 {
		if x, err := m.memory(e.PrevID); err == nil {
			e.Before = x.Text
		}
		return
	}
	before, after := m.logStates(m.db, e.LogSeq)
	if before != nil && after != nil {
		e.Text = after.Text // as this change left it
		if before.Text != after.Text {
			e.Before = before.Text
		}
	}
}

// logStates reads the before and after of a memory_log row.
func (m *memStore) logStates(q querier, seq int64) (before, after *Memory) {
	var b, a sql.NullString
	if seq == 0 || q.QueryRow(`SELECT before, after FROM memory_log WHERE seq = ?`, seq).Scan(&b, &a) != nil {
		return nil, nil
	}
	if b.Valid && json.Unmarshal([]byte(b.String), &before) != nil {
		before = nil
	}
	if a.Valid && json.Unmarshal([]byte(a.String), &after) != nil {
		after = nil
	}
	return before, after
}

func (m *memStore) learning(id int64) (*learnEntry, error) {
	e, err := scanLearning(m.db.QueryRow(`SELECT `+learnColumns+` FROM learning_log l LEFT JOIN memories x ON x.id = l.memory_id
		WHERE l.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoLearning
	}
	if err == nil {
		m.fillBefore(e)
	}
	return e, err
}

// undoable says why an entry cannot be undone, or "" if it can: not twice,
// not a revert, and only the newest change of what it touched still in force.
func (m *memStore) undoable(e *learnEntry) string {
	switch {
	case e.Status == learnReverted:
		return "it is already undone"
	case e.Action == learnReverted:
		return "it is itself an undo: change it again instead"
	case e.Gone:
		return "the memory is erased for good"
	}
	var later int64
	if e.Kind == kindSkill {
		m.db.QueryRow(`SELECT COALESCE(MIN(id), 0) FROM learning_log WHERE id > ? AND kind = 'skill' AND skill = ? AND status != 'reverted'
			AND action != 'reverted'`, e.ID, e.Skill).Scan(&later)
	} else {
		ids := []any{e.ID, e.MemoryID, nullID(e.PrevID), e.MemoryID, nullID(e.PrevID)}
		m.db.QueryRow(`SELECT COALESCE(MIN(id), 0) FROM learning_log WHERE id > ? AND kind != 'skill' AND status != 'reverted'
			AND action != 'reverted' AND (memory_id IN (?, ?) OR prev_id IN (?, ?))`, ids...).Scan(&later)
	}
	if later != 0 {
		return fmt.Sprintf("a newer change of the same thing came after (learned #%d): undo that one first", later)
	}
	return ""
}

// undoMemory undoes a memory's entry and records it, in one transaction.
func (m *memStore) undoMemory(e *learnEntry, who, reason string, evidence []string) (*learnEntry, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	by := whoUndo + who
	why := fmt.Sprintf("undo learned #%d", e.ID)
	if reason != "" {
		why += ": " + reason
	}
	current := func(id int64) (*Memory, error) {
		x, err := scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, id))
		if err != nil {
			return nil, fmt.Errorf("memory %d: %w", id, errNoMemory)
		}
		return x, nil
	}
	x, err := current(e.MemoryID)
	if err != nil {
		return nil, err
	}
	switch {
	case e.Action == learnRemoved:
		if x.Status == memDeleted {
			if err := m.restoreTx(tx, x, by); err != nil {
				return nil, err
			}
		}
	case e.Action == learnAdded || e.PrevID != 0:
		if x.Status != memDeleted {
			if err := m.forgetTx(tx, x.ID, by, why, ""); err != nil {
				return nil, err
			}
		}
		if e.PrevID != 0 {
			if err := m.unretire(tx, e, by, why); err != nil {
				return nil, err
			}
		}
	default: // changed in place: back to the state before
		before, _ := m.logStates(tx, e.LogSeq)
		if before == nil {
			return nil, errors.New("the state before this change is no longer kept")
		}
		validTo, dueAt := before.ValidTo, before.DueAt
		p := memoryPatch{Text: &before.Text, Subject: &before.Subject, Links: &before.Links, Kind: &before.Kind, ValidTo: &validTo,
			DueAt: &dueAt, State: &before.State, Pinned: &before.Pinned, Status: &before.Status, Keywords: &before.Keywords,
			Importance: &before.Importance}
		if _, err := m.updateTx(tx, x.ID, p, by, why); err != nil {
			return nil, err
		}
	}
	r := &learnEntry{Kind: e.Kind, MemoryID: e.MemoryID, PrevID: e.PrevID, LogSeq: m.lastLogSeq(tx, e.MemoryID), Action: learnReverted,
		Status: learnConfirmed, Source: learnFromUser, Risk: e.Risk, Reason: reason, Evidence: append(evidence, fmt.Sprintf("learned:%d", e.ID)),
		Who: who, Reverts: e.ID}
	if err := m.insertLearning(tx, r); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE learning_log SET status = 'reverted', undo = '' WHERE id = ?`, e.ID); err != nil {
		return nil, err
	}
	return r, tx.Commit()
}

// unretire makes the memory a change replaced current again, as it was.
func (m *memStore) unretire(tx *sql.Tx, e *learnEntry, who, reason string) error {
	before, after := m.logStates(tx, e.LogSeq)
	if before == nil || after == nil || before.ID != e.PrevID {
		return errors.New("the memory this change replaced is no longer kept as it was")
	}
	old, err := scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, e.PrevID))
	if err != nil {
		return fmt.Errorf("memory %d: %w", e.PrevID, errNoMemory)
	}
	x := *old
	x.ValidTo, x.SupersededBy, x.Pinned, x.UpdatedAt = before.ValidTo, 0, before.Pinned, m.now()
	if _, err := tx.Exec(`UPDATE memories SET valid_to = ?, superseded_by = NULL, pinned = ?, updated_at = ? WHERE id = ?`,
		unixOrNil(x.ValidTo), x.Pinned, x.UpdatedAt.Unix(), x.ID); err != nil {
		return err
	}
	return m.log(tx, x.ID, who, "update", old, &x, reason)
}

// recordSkillUndo records the undo of a skill's entry, once done.
func (m *memStore) recordSkillUndo(e *learnEntry, who, reason string, evidence []string) (*learnEntry, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r := &learnEntry{Kind: kindSkill, Skill: e.Skill, Version: e.PrevVersion, PrevVersion: e.Version, Action: learnReverted,
		Status: learnConfirmed, Source: learnFromUser, Risk: e.Risk, Reason: reason, Evidence: append(evidence, fmt.Sprintf("learned:%d", e.ID)),
		Who: who, Reverts: e.ID}
	if err := m.insertLearning(tx, r); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE learning_log SET status = 'reverted', undo = '' WHERE id = ?`, e.ID); err != nil {
		return nil, err
	}
	return r, tx.Commit()
}

// setUndo marks an entry's undo proposed (with why) or rejected.
func (m *memStore) setUndo(id int64, state, why string) error {
	_, err := m.db.Exec(`UPDATE learning_log SET undo = ?, undo_why = ? WHERE id = ?`, state, truncate(why, 300), id)
	return err
}

// The core's side: undoing an entry, the skills' changes, and the brief.

// unlearn undoes an entry, as who (owner, or brain on the user's message).
func (c *core) unlearn(id int64, who, reason string, evidence []string) (*learnEntry, error) {
	if c.mem == nil {
		return nil, errMemoryOff
	}
	learnMu.Lock()
	defer learnMu.Unlock()
	e, err := c.mem.learning(id)
	if err != nil {
		return nil, err
	}
	if why := c.mem.undoable(e); why != "" {
		return nil, fmt.Errorf("learned #%d cannot be undone: %s", id, why)
	}
	if e.Kind != kindSkill {
		return c.mem.undoMemory(e, who, reason, evidence)
	}
	if err := c.store.setSkillApproved(e.Skill, e.Version, e.PrevVersion); err != nil {
		return nil, err
	}
	return c.mem.recordSkillUndo(e, who, reason, evidence)
}

// approveSkill approves a skill's pending version and records it.
func (c *core) approveSkill(name, sha, who string) error {
	prev, err := c.store.approveSkillVersion(name, sha)
	if err != nil {
		return err
	}
	c.learnSkill(name, sha, prev, who, "approved")
	return nil
}

// deleteSkill deletes a skill and records it, if it had an approved version.
func (c *core) deleteSkill(name, who string) error {
	prev, err := c.store.deleteSkillVersion(name)
	if err != nil {
		return err
	}
	if prev != "" {
		c.learnSkill(name, "", prev, who, "deleted")
	}
	return nil
}

func (c *core) learnSkill(name, version, prev, who, reason string) {
	if c.mem == nil {
		return
	}
	risk := riskLow
	if v, err := c.store.skillVersion(orStr(version, prev)); err == nil && len(v.Scripts()) > 0 {
		risk = riskHigh // its code runs in tasks
	}
	if _, err := c.mem.learnSkill(name, version, prev, who, reason, risk); err != nil {
		log.Printf("learning: skill %s: %v", name, err)
	}
}

// learnedToldKey is the last entry told to the user, by a wake or a brief.
const learnedToldKey = "learned_told"

// learnedToTell is what Eriphe learned since the last brief that it has
// not told yet: not what the user did by hand, nor what was undone.
func (c *core) learnedToTell(now time.Time) []*learnEntry {
	if c.mem == nil {
		return nil
	}
	from := now.Add(-24 * time.Hour)
	var unix int64
	if _, err := fmt.Sscan(c.store.get(lastBriefKey), &unix); err == nil && unix > 0 {
		from = time.Unix(unix, 0)
	}
	var told int64
	fmt.Sscan(c.mem.get(learnedToldKey), &told)
	xs, err := c.mem.learnings(learnFilter{Since: from, AfterID: told, NotWho: "owner", Statuses: []string{learnTrial, learnConfirmed},
		Limit: 20})
	if err != nil {
		return nil
	}
	var out []*learnEntry
	for _, e := range xs {
		if e.Action != learnReverted {
			out = append(out, e)
		}
	}
	return out
}

// wakePacket is the memory's wake packet with what was learned since the
// last brief and not told yet.
func (c *core) wakePacket() (*wakePacket, error) {
	p, err := c.mem.wakePacket()
	if err != nil {
		return nil, err
	}
	p.Learned = c.learnedToTell(c.mem.now())
	return p, nil
}

// learnedTold marks these entries told.
func (c *core) learnedTold(xs []*learnEntry) {
	var top int64
	for _, e := range xs {
		top = max(top, e.ID)
	}
	var told int64
	fmt.Sscan(c.mem.get(learnedToldKey), &told)
	if top > told {
		c.mem.set(learnedToldKey, fmt.Sprint(top))
	}
}

// learnedLine is an entry in a line, in French, for the brain's packets:
// an external memory is named, not quoted.
func learnedLine(e *learnEntry) string {
	what := e.Kind
	if e.Subject != "" {
		what += ", " + e.Subject
	}
	var text string
	switch {
	case e.Kind == kindSkill:
		text = "skill « " + e.Skill + " »"
		if e.Version != "" {
			text += " version " + shortSHA(e.Version)
		}
	case e.Trust == trustExternal:
		text = "(tiré d'un contenu extérieur : learned le nomme sans le citer)"
	default:
		text = "« " + oneLine(e.Text) + " »"
		if e.Before != "" {
			text += " (avant : « " + oneLine(e.Before) + " »)"
		}
	}
	return fmt.Sprintf("- [appris #%d, %s, %s] %s ; source %s, %s", e.ID, frAction[e.Action], what, text, e.Source, frStatus[e.Status])
}

var (
	frAction = map[string]string{learnAdded: "ajouté", learnChanged: "changé", learnRemoved: "retiré", learnReverted: "annulé"}
	frStatus = map[string]string{learnTrial: "à l'essai", learnConfirmed: "confirmé", learnDormant: "en sommeil", learnReverted: "annulé"}
)

// learnedHappenings are the journal's entries, for since and the Journal.
func learnedHappenings(c *core, from time.Time, limit int) ([]happening, error) {
	if c.mem == nil {
		return nil, nil
	}
	xs, err := c.mem.learnings(learnFilter{Since: from, Limit: limit})
	if err != nil {
		return nil, err
	}
	var out []happening
	for i := len(xs) - 1; i >= 0; i-- { // oldest first
		e := xs[i]
		h := happening{At: e.At, Kind: feedLearned, Event: e.Action, Ref: fmt.Sprintf("learned:%d", e.ID),
			Args: []any{e.ID, trKey("learned.kind." + e.Kind), trKey("since.learned.src." + e.Source)}, Trust: feedOwn}
		switch {
		case e.Kind == kindSkill:
			h.Text = feedText(e.Skill)
		case e.Gone:
		default:
			h.Text = feedText(e.Text)
			if e.Trust == trustExternal {
				h.Trust = feedExternal
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// learnMu makes undoing one thing at a time: a click and the brain's
// unlearn at the same moment undo it once.
var learnMu sync.Mutex

func shortSHA(s string) string { return s[:min(12, len(s))] }

package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Eriphe's memory: what it keeps between conversations, in its own SQLite
// file next to eriphe-core's (memory.db). It points at the vault (links)
// rather than copying it. Facts are dated and replaced, never overwritten:
// a superseded memory gets a valid_to and the id of what replaced it. Every
// change is in memory_log. Who may write what is decided in code
// (memory_trust.go), not by the brain.

// MemoryConfig tunes the memory; it works without one.
type MemoryConfig struct {
	DB string `json:"db,omitempty"` // memory.db next to eriphe-core's database by default
	// The agent of the extraction task: the catalog's "haiku" if there is
	// one, else the default agent.
	Agent       string `json:"agent,omitempty"`
	ExtractAt   string `json:"extract_at,omitempty"`   // local time of the nightly run, 03:00 by default
	IdleMinutes int    `json:"idle_minutes,omitempty"` // a busy day is extracted once the conversation is idle this long, 30 by default
	BusyDay     int    `json:"busy_day,omitempty"`     // messages that make a busy day, 20 by default
	Extraction  *bool  `json:"extraction,omitempty"`   // false turns the automatic extraction off
	BackupDir   string `json:"backup_dir,omitempty"`   // nightly copies; memory-backups next to memory.db by default
	BackupKeep  int    `json:"backup_keep,omitempty"`  // nightly copies kept, 14 by default
	// Catalog servers whose results do not taint the brain's turn (their
	// content is the user's own, e.g. the vault). None by default.
	TrustedServers []string `json:"trusted_servers,omitempty"`
}

func (c *Config) memoryConfig() MemoryConfig {
	var m MemoryConfig
	if c.Memory != nil {
		m = *c.Memory
	}
	if m.DB == "" {
		m.DB = filepath.Join(filepath.Dir(c.DB), "memory.db")
	}
	if m.ExtractAt == "" {
		m.ExtractAt = "03:00"
	}
	if m.IdleMinutes <= 0 {
		m.IdleMinutes = 30
	}
	if m.BusyDay <= 0 {
		m.BusyDay = 20
	}
	if m.BackupDir == "" {
		m.BackupDir = filepath.Join(filepath.Dir(m.DB), "memory-backups")
	}
	if m.BackupKeep <= 0 {
		m.BackupKeep = 14
	}
	return m
}

// Kinds of memories.
const (
	kindProfile    = "profile"
	kindPreference = "preference"
	kindFact       = "fact"
	kindPerson     = "person"
	kindEpisode    = "episode"
	kindCommitment = "commitment"
	kindLesson     = "lesson"
)

var memoryKinds = []string{kindProfile, kindPreference, kindFact, kindPerson, kindEpisode, kindCommitment, kindLesson}

// ownerKinds are written only from my own words.
func ownerKind(kind string) bool {
	return kind == kindProfile || kind == kindPreference || kind == kindLesson
}

// Statuses, trust levels and where a memory comes from.
const (
	memActive   = "active"
	memProposed = "proposed" // waits for my approval
	memArchived = "archived"
	memDeleted  = "deleted" // forgotten, purged after memoryPurgeAfter

	trustOwner      = "owner"
	trustFirstParty = "first_party"
	trustExternal   = "external"

	srcOwner      = "owner"
	srcBrain      = "brain"
	srcExtraction = "extraction"
	srcTask       = "task"
	srcImport     = "import"

	stateOpen    = "open"
	stateDone    = "done"
	stateDropped = "dropped"
)

const (
	profileBudget    = 2500 // characters of pinned memories, the profile
	memoryTextMax    = 2000
	episodeTextMax   = 4000
	subjectMax       = 100
	linksMax         = 10
	linkMax          = 300
	memoryPurgeAfter = 30 * 24 * time.Hour
	episodeHalfLife  = 30 * 24 * time.Hour
)

// Memory is one thing Eriphe remembers.
type Memory struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Subject      string    `json:"subject,omitempty"`
	Text         string    `json:"text"`
	Links        []string  `json:"links,omitempty"`
	OccurredAt   time.Time `json:"occurred_at,omitzero"`
	ValidFrom    time.Time `json:"valid_from"`
	ValidTo      time.Time `json:"valid_to,omitzero"`
	RecordedAt   time.Time `json:"recorded_at"`
	SupersededBy int64     `json:"superseded_by,omitempty"`
	Supersedes   int64     `json:"supersedes,omitempty"`
	DueAt        time.Time `json:"due_at,omitzero"`
	State        string    `json:"state,omitempty"`
	SourceKind   string    `json:"source_kind"`
	SourceRef    string    `json:"source_ref,omitempty"`
	Trust        string    `json:"trust"`
	Status       string    `json:"status"`
	Pinned       bool      `json:"pinned,omitempty"`
	Confidence   float64   `json:"confidence"`
	UseCount     int       `json:"use_count"`
	LastUsedAt   time.Time `json:"last_used_at,omitzero"`
	Flags        string    `json:"flags,omitempty"` // what the scanner noticed
	DeletedAt    time.Time `json:"deleted_at,omitzero"`
	UpdatedAt    time.Time `json:"updated_at"`

	Score float64 `json:"-"`
}

// Current tells whether the memory is still true now.
func (m *Memory) Current(now time.Time) bool {
	return m.ValidTo.IsZero() || m.ValidTo.After(now)
}

type memStore struct {
	db  *sql.DB
	now func() time.Time
}

// memoryMigrations are applied in order; meta.schema_version is how many ran.
var memoryMigrations = []string{
	`CREATE TABLE memories (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  kind TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', text TEXT NOT NULL,
	  links TEXT NOT NULL DEFAULT '[]',
	  occurred_at INTEGER, valid_from INTEGER NOT NULL, valid_to INTEGER, recorded_at INTEGER NOT NULL,
	  superseded_by INTEGER, supersedes INTEGER,
	  due_at INTEGER, state TEXT NOT NULL DEFAULT '',
	  source_kind TEXT NOT NULL, source_ref TEXT NOT NULL DEFAULT '',
	  trust TEXT NOT NULL, status TEXT NOT NULL,
	  pinned INTEGER NOT NULL DEFAULT 0, confidence REAL NOT NULL DEFAULT 1,
	  use_count INTEGER NOT NULL DEFAULT 0, last_used_at INTEGER,
	  flags TEXT NOT NULL DEFAULT '', deleted_at INTEGER, updated_at INTEGER NOT NULL,
	  embedding BLOB);
	CREATE INDEX memories_status_kind ON memories (status, kind);
	CREATE INDEX memories_subject ON memories (subject);
	CREATE INDEX memories_due ON memories (due_at) WHERE kind = 'commitment';
	CREATE VIRTUAL TABLE memories_fts USING fts5(text, subject, content='memories', content_rowid='id',
	  tokenize='unicode61 remove_diacritics 2');
	CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
	  INSERT INTO memories_fts (rowid, text, subject) VALUES (new.id, new.text, new.subject); END;
	CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
	  INSERT INTO memories_fts (memories_fts, rowid, text, subject) VALUES ('delete', old.id, old.text, old.subject); END;
	CREATE TRIGGER memories_au AFTER UPDATE OF text, subject ON memories BEGIN
	  INSERT INTO memories_fts (memories_fts, rowid, text, subject) VALUES ('delete', old.id, old.text, old.subject);
	  INSERT INTO memories_fts (rowid, text, subject) VALUES (new.id, new.text, new.subject); END;
	CREATE TABLE memory_log (
	  seq INTEGER PRIMARY KEY AUTOINCREMENT, memory_id INTEGER NOT NULL, time INTEGER NOT NULL,
	  who TEXT NOT NULL, op TEXT NOT NULL, before TEXT, after TEXT, reason TEXT NOT NULL DEFAULT '');
	CREATE INDEX memory_log_memory ON memory_log (memory_id);
	CREATE TRIGGER memory_log_append_only BEFORE UPDATE ON memory_log BEGIN
	  SELECT RAISE(ABORT, 'memory_log is append-only'); END;
	CREATE TABLE extraction_runs (
	  id INTEGER PRIMARY KEY AUTOINCREMENT, day TEXT NOT NULL, upto INTEGER NOT NULL,
	  task_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
	  started INTEGER NOT NULL, finished INTEGER, applied INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
}

func openMemory(path string) (*memStore, error) {
	if dir := filepath.Dir(path); dir != "" {
		os.MkdirAll(dir, 0o700)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	m := &memStore{db: db, now: time.Now}
	if err := m.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: %w", err)
	}
	return m, nil
}

func (m *memStore) migrate() error {
	if _, err := m.db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	version := 0
	var v string
	if m.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v) == nil {
		fmt.Sscan(v, &version)
	}
	if version > len(memoryMigrations) {
		return fmt.Errorf("memory.db is at schema %d, newer than this eriphe-core (%d)", version, len(memoryMigrations))
	}
	for i := version; i < len(memoryMigrations); i++ {
		tx, err := m.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(memoryMigrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (m *memStore) schemaVersion() int {
	var n int
	m.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'schema_version'`).Scan(&n)
	return n
}

func (m *memStore) get(key string) string {
	var v string
	m.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	return v
}

func (m *memStore) set(key, value string) {
	m.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
}

// Times are unix seconds; a zero time is NULL.
func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func fromNull(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}

const memColumns = `id, kind, subject, text, links, occurred_at, valid_from, valid_to, recorded_at, superseded_by, supersedes,
	due_at, state, source_kind, source_ref, trust, status, pinned, confidence, use_count, last_used_at, flags, deleted_at, updated_at`

func scanMemory(sc interface{ Scan(...any) error }) (*Memory, error) {
	var x Memory
	var links string
	var occurred, validTo, superseded, supersedes, due, used, deleted sql.NullInt64
	var from, recorded, updated int64
	err := sc.Scan(&x.ID, &x.Kind, &x.Subject, &x.Text, &links, &occurred, &from, &validTo, &recorded, &superseded, &supersedes,
		&due, &x.State, &x.SourceKind, &x.SourceRef, &x.Trust, &x.Status, &x.Pinned, &x.Confidence, &x.UseCount, &used,
		&x.Flags, &deleted, &updated)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(links), &x.Links)
	x.OccurredAt, x.ValidTo, x.DueAt, x.LastUsedAt, x.DeletedAt = fromNull(occurred), fromNull(validTo), fromNull(due), fromNull(used), fromNull(deleted)
	x.ValidFrom, x.RecordedAt, x.UpdatedAt = time.Unix(from, 0), time.Unix(recorded, 0), time.Unix(updated, 0)
	x.SupersededBy, x.Supersedes = superseded.Int64, supersedes.Int64
	return &x, nil
}

var errNoMemory = errors.New("no such memory")

func (m *memStore) memory(id int64) (*Memory, error) {
	x, err := scanMemory(m.db.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoMemory
	}
	return x, err
}

type querier interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

func memoryJSON(x *Memory) string {
	if x == nil {
		return ""
	}
	raw, _ := json.Marshal(x)
	return string(raw)
}

func (m *memStore) log(q querier, id int64, who, op string, before, after *Memory, reason string) error {
	var b, a any
	if before != nil {
		b = memoryJSON(before)
	}
	if after != nil {
		a = memoryJSON(after)
	}
	_, err := q.Exec(`INSERT INTO memory_log (memory_id, time, who, op, before, after, reason) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, m.now().Unix(), who, op, b, a, reason)
	return err
}

// normalize checks a memory before it is written: its kind, sizes and
// dates. The trust rules come before it (memory_trust.go).
func (m *memStore) normalize(x *Memory) error {
	x.Text = strings.TrimSpace(x.Text)
	x.Subject = normalizeSubject(x.Subject)
	if !containsStr(memoryKinds, x.Kind) {
		return fmt.Errorf("kind must be one of %s", strings.Join(memoryKinds, ", "))
	}
	if x.Text == "" {
		return errors.New("text is required")
	}
	limit := memoryTextMax
	if x.Kind == kindEpisode {
		limit = episodeTextMax
	}
	if n := utf8.RuneCountInString(x.Text); n > limit {
		return fmt.Errorf("text is %d characters, at most %d: keep one memory per thing", n, limit)
	}
	if utf8.RuneCountInString(x.Subject) > subjectMax {
		return fmt.Errorf("subject is longer than %d characters", subjectMax)
	}
	var links []string
	for _, l := range x.Links {
		if l = strings.TrimSpace(l); l != "" && !containsStr(links, l) {
			if utf8.RuneCountInString(l) > linkMax {
				return fmt.Errorf("a link is longer than %d characters", linkMax)
			}
			links = append(links, l)
		}
	}
	if len(links) > linksMax {
		return fmt.Errorf("at most %d links", linksMax)
	}
	x.Links = links
	now := m.now()
	if x.ValidFrom.IsZero() {
		x.ValidFrom = now
	}
	if x.Kind == kindEpisode && x.OccurredAt.IsZero() {
		x.OccurredAt = now
	}
	if x.Kind == kindCommitment {
		if x.State == "" {
			x.State = stateOpen
		}
		if !containsStr([]string{stateOpen, stateDone, stateDropped}, x.State) {
			return errors.New("state must be open, done or dropped")
		}
	} else {
		x.State, x.DueAt = "", time.Time{}
	}
	if x.Kind == kindProfile {
		x.Pinned = true
	}
	if x.Confidence <= 0 || x.Confidence > 1 {
		x.Confidence = 1
	}
	if x.Status == "" {
		x.Status = memActive
	}
	return nil
}

// normalizeSubject keeps subjects comparable: lower case, no spaces around
// the colon of person:paul, single spaces.
func normalizeSubject(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	if k, v, ok := strings.Cut(s, ":"); ok {
		s = strings.TrimSpace(k) + ":" + strings.ReplaceAll(strings.TrimSpace(v), " ", "-")
	}
	return s
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// profileChars is the size of the profile, without the memory except.
func (m *memStore) profileChars(q querier, except int64) int {
	var n int
	q.QueryRow(`SELECT COALESCE(SUM(LENGTH(text)), 0) FROM memories
		WHERE pinned = 1 AND status = 'active' AND (valid_to IS NULL OR valid_to > ?) AND id != ?`, m.now().Unix(), except).Scan(&n)
	return n
}

var errProfileFull = errors.New("the profile is full")

func (m *memStore) checkBudget(q querier, x *Memory, except int64) error {
	if !x.Pinned || x.Status != memActive || !x.Current(m.now()) {
		return nil
	}
	used := m.profileChars(q, max(x.ID, except))
	if n := utf8.RuneCountInString(x.Text); used+n > profileBudget {
		return fmt.Errorf("%w: %d of %d characters used, this one needs %d; unpin or shorten something first", errProfileFull, used, profileBudget, n)
	}
	return nil
}

func (m *memStore) insert(q querier, x *Memory, who string, except int64) error {
	now := m.now()
	x.RecordedAt, x.UpdatedAt = now, now
	if x.Status == memDeleted || x.Status == memArchived {
		return errors.New("a new memory is active or proposed")
	}
	if err := m.checkBudget(q, x, except); err != nil {
		return err
	}
	links, _ := json.Marshal(x.Links)
	if x.Links == nil {
		links = []byte("[]")
	}
	res, err := q.Exec(`INSERT INTO memories (kind, subject, text, links, occurred_at, valid_from, valid_to, recorded_at, supersedes,
		due_at, state, source_kind, source_ref, trust, status, pinned, confidence, flags, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.Kind, x.Subject, x.Text, string(links), unixOrNil(x.OccurredAt), x.ValidFrom.Unix(), unixOrNil(x.ValidTo), now.Unix(),
		nullID(x.Supersedes), unixOrNil(x.DueAt), x.State, x.SourceKind, x.SourceRef, x.Trust, x.Status, x.Pinned, x.Confidence,
		x.Flags, now.Unix())
	if err != nil {
		return err
	}
	x.ID, _ = res.LastInsertId()
	op := "add"
	if x.Status == memProposed {
		op = "propose"
	}
	return m.log(q, x.ID, who, op, nil, x, "")
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// add writes a new memory, or replaces one (x.Supersedes) when it is active.
func (m *memStore) add(x *Memory, who string) error {
	if err := m.normalize(x); err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old *Memory
	if x.Supersedes != 0 {
		old, err = scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, x.Supersedes))
		if err != nil {
			return fmt.Errorf("supersedes %d: %w", x.Supersedes, errNoMemory)
		}
		if old.Status != memActive || old.SupersededBy != 0 {
			return fmt.Errorf("memory %d is not a current one: it cannot be superseded", old.ID)
		}
		if x.Subject == "" {
			x.Subject = old.Subject
		}
		if x.Status == memActive && old.Pinned {
			x.Pinned = true // the replacement keeps its place in the profile
		}
	}
	// A replacement of a pinned memory is checked without the one it replaces.
	var except int64
	if old != nil {
		except = old.ID
	}
	if err := m.insert(tx, x, who, except); err != nil {
		return err
	}
	if old != nil && x.Status == memActive {
		if err := m.retire(tx, old, x, who); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// retire ends an old memory where a new one starts: it stays, dated.
func (m *memStore) retire(q querier, old, by *Memory, who string) error {
	end := by.ValidFrom
	if !end.After(old.ValidFrom) {
		end = m.now()
	}
	after := *old
	after.ValidTo, after.SupersededBy, after.Pinned, after.UpdatedAt = end, by.ID, false, m.now()
	if _, err := q.Exec(`UPDATE memories SET valid_to = ?, superseded_by = ?, pinned = 0, updated_at = ? WHERE id = ?`,
		end.Unix(), by.ID, after.UpdatedAt.Unix(), old.ID); err != nil {
		return err
	}
	return m.log(q, old.ID, who, "supersede", old, &after, fmt.Sprintf("replaced by %d", by.ID))
}

// memoryPatch is a change to a memory; nil fields stay.
type memoryPatch struct {
	Text    *string
	Subject *string
	Links   *[]string
	Kind    *string
	ValidTo *time.Time // zero time: true again
	DueAt   *time.Time
	State   *string
	Pinned  *bool
	Status  *string
}

func (m *memStore) update(id int64, p memoryPatch, who, reason string) (*Memory, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	before, err := scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, id))
	if err != nil {
		return nil, errNoMemory
	}
	if before.Status == memDeleted {
		return nil, fmt.Errorf("memory %d is forgotten", id)
	}
	x := *before
	if p.Text != nil {
		x.Text = *p.Text
	}
	if p.Subject != nil {
		x.Subject = *p.Subject
	}
	if p.Links != nil {
		x.Links = *p.Links
	}
	if p.Kind != nil {
		x.Kind = *p.Kind
	}
	if p.ValidTo != nil {
		x.ValidTo = *p.ValidTo
	}
	if p.DueAt != nil {
		x.DueAt = *p.DueAt
	}
	if p.State != nil {
		x.State = *p.State
	}
	if p.Pinned != nil {
		x.Pinned = *p.Pinned
	}
	if p.Status != nil {
		x.Status = *p.Status
	}
	if x.Kind != kindProfile && before.Kind == kindProfile && p.Pinned == nil {
		x.Pinned = before.Pinned
	}
	if err := m.normalize(&x); err != nil {
		return nil, err
	}
	if x.Kind == kindProfile && p.Pinned != nil && !*p.Pinned {
		return nil, errors.New("a profile memory is always pinned: change its kind or archive it")
	}
	if err := m.checkBudget(tx, &x, 0); err != nil {
		return nil, err
	}
	x.UpdatedAt = m.now()
	links, _ := json.Marshal(x.Links)
	if x.Links == nil {
		links = []byte("[]")
	}
	if _, err := tx.Exec(`UPDATE memories SET kind = ?, subject = ?, text = ?, links = ?, valid_to = ?, due_at = ?, state = ?,
		pinned = ?, status = ?, updated_at = ? WHERE id = ?`, x.Kind, x.Subject, x.Text, string(links), unixOrNil(x.ValidTo),
		unixOrNil(x.DueAt), x.State, x.Pinned, x.Status, x.UpdatedAt.Unix(), id); err != nil {
		return nil, err
	}
	if err := m.log(tx, id, who, "update", before, &x, reason); err != nil {
		return nil, err
	}
	return &x, tx.Commit()
}

// approve makes a proposed memory active, as the owner, and replaces what
// it was meant to replace.
func (m *memStore) approve(id int64, who string) (*Memory, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	x, err := scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, id))
	if err != nil {
		return nil, errNoMemory
	}
	if x.Status != memProposed {
		return nil, fmt.Errorf("memory %d is not waiting for approval", id)
	}
	before := *x
	x.Status = memActive
	var old *Memory
	if x.Supersedes != 0 {
		if o, err := scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, x.Supersedes)); err == nil &&
			o.Status == memActive && o.SupersededBy == 0 {
			old = o
			if o.Pinned {
				x.Pinned = true
			}
		}
	}
	except := int64(0)
	if old != nil {
		except = old.ID
	}
	if x.Pinned {
		used := m.profileChars(tx, except)
		if used+utf8.RuneCountInString(x.Text) > profileBudget {
			return nil, fmt.Errorf("%w: %d of %d characters used", errProfileFull, used, profileBudget)
		}
	}
	x.UpdatedAt = m.now()
	if _, err := tx.Exec(`UPDATE memories SET status = 'active', pinned = ?, updated_at = ? WHERE id = ?`, x.Pinned, x.UpdatedAt.Unix(), id); err != nil {
		return nil, err
	}
	if err := m.log(tx, id, who, "approve", &before, x, ""); err != nil {
		return nil, err
	}
	if old != nil {
		if err := m.retire(tx, old, x, who); err != nil {
			return nil, err
		}
	}
	return x, tx.Commit()
}

// forget hides a memory at once and purges it after memoryPurgeAfter.
func (m *memStore) forget(id int64, who, reason string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := scanMemory(tx.QueryRow(`SELECT `+memColumns+` FROM memories WHERE id = ?`, id))
	if err != nil {
		return errNoMemory
	}
	if before.Status == memDeleted {
		return nil
	}
	x := *before
	x.Status, x.Pinned, x.DeletedAt, x.UpdatedAt = memDeleted, false, m.now(), m.now()
	if _, err := tx.Exec(`UPDATE memories SET status = 'deleted', pinned = 0, deleted_at = ?, updated_at = ? WHERE id = ?`,
		x.DeletedAt.Unix(), x.UpdatedAt.Unix(), id); err != nil {
		return err
	}
	if err := m.log(tx, id, who, "forget", before, nil, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// restore brings back a forgotten memory before it is purged.
func (m *memStore) restore(id int64, who string) error {
	x, err := m.memory(id)
	if err != nil {
		return err
	}
	if x.Status != memDeleted {
		return nil
	}
	_, err = m.db.Exec(`UPDATE memories SET status = 'active', deleted_at = NULL, updated_at = ? WHERE id = ?`, m.now().Unix(), id)
	if err == nil {
		after := *x
		after.Status = memActive
		m.log(m.db, id, who, "restore", nil, &after, "")
	}
	return err
}

// purge removes for good what was forgotten long enough ago, and its history.
func (m *memStore) purge() (int, error) {
	cut := m.now().Add(-memoryPurgeAfter).Unix()
	tx, err := m.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM memories WHERE status = 'deleted' AND deleted_at < ?`, cut)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		tx.Exec(`DELETE FROM memory_log WHERE memory_id = ?`, id)
		tx.Exec(`DELETE FROM memories WHERE id = ?`, id)
		tx.Exec(`UPDATE memories SET superseded_by = NULL WHERE superseded_by = ?`, id)
		tx.Exec(`UPDATE memories SET supersedes = NULL WHERE supersedes = ?`, id)
		tx.Exec(`INSERT INTO memory_log (memory_id, time, who, op, reason) VALUES (?, ?, 'eriphe-core', 'purge', 'forgotten 30 days ago')`,
			id, m.now().Unix())
	}
	return len(ids), tx.Commit()
}

// recallQuery is what recall looks for.
type recallQuery struct {
	Query       string
	Kinds       []string
	Subject     string
	Since       time.Time
	Until       time.Time
	AsOf        time.Time
	IncludePast bool
	Statuses    []string // active by default
	Limit       int
	Pinned      bool // only the profile
	Touch       bool // count the use
}

// ftsQuery turns free text into an FTS5 query: words, OR between them (bm25
// ranks the memories that match more of them first), a prefix match for
// words of four letters or more. FTS syntax in the input is not honoured.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if w == "OR" || w == "AND" || w == "NOT" || w == "ou" || w == "et" {
			continue
		}
		t := `"` + strings.ToLower(w) + `"`
		if utf8.RuneCountInString(w) >= 4 {
			t += "*"
		}
		if !containsStr(terms, t) {
			terms = append(terms, t)
		}
	}
	return strings.Join(terms, " OR ")
}

func (m *memStore) recall(q recallQuery) ([]*Memory, error) {
	now := m.now()
	if q.Limit <= 0 {
		q.Limit = 10
	}
	q.Limit = min(q.Limit, 100)
	statuses := q.Statuses
	if len(statuses) == 0 {
		statuses = []string{memActive}
	}
	var where []string
	var args []any
	match := ftsQuery(q.Query)
	from := `memories m`
	sel := `0.0`
	if match != "" {
		from = `memories_fts f JOIN memories m ON m.id = f.rowid`
		where = append(where, `memories_fts MATCH ?`)
		args = append(args, match)
		sel = `bm25(memories_fts, 1.0, 0.5)`
	}
	where = append(where, `m.status IN (`+placeholders(len(statuses))+`)`)
	for _, s := range statuses {
		args = append(args, s)
	}
	if len(q.Kinds) > 0 {
		where = append(where, `m.kind IN (`+placeholders(len(q.Kinds))+`)`)
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	if s := normalizeSubject(q.Subject); s != "" {
		if strings.HasSuffix(s, ":") || strings.HasSuffix(s, "*") {
			where = append(where, `m.subject LIKE ? ESCAPE '\'`)
			args = append(args, likeEscape(strings.TrimSuffix(s, "*"))+"%")
		} else {
			where = append(where, `m.subject = ?`)
			args = append(args, s)
		}
	}
	when := `COALESCE(m.occurred_at, m.valid_from)`
	if !q.Since.IsZero() {
		where = append(where, when+` >= ?`)
		args = append(args, q.Since.Unix())
	}
	if !q.Until.IsZero() {
		where = append(where, when+` <= ?`)
		args = append(args, q.Until.Unix())
	}
	switch {
	case !q.AsOf.IsZero():
		where = append(where, `m.valid_from <= ? AND (m.valid_to IS NULL OR m.valid_to > ?)`)
		args = append(args, q.AsOf.Unix(), q.AsOf.Unix())
	case !q.IncludePast:
		where = append(where, `(m.valid_to IS NULL OR m.valid_to > ?)`)
		args = append(args, now.Unix())
	}
	if q.Pinned {
		where = append(where, `m.pinned = 1`)
	}
	var colList []string
	for _, c := range strings.Split(memColumns, ",") {
		colList = append(colList, "m."+strings.TrimSpace(c))
	}
	cols := strings.Join(colList, ", ")
	// Enough candidates for the boosts to reorder.
	query := `SELECT ` + cols + `, ` + sel + ` FROM ` + from + ` WHERE ` + strings.Join(where, " AND ")
	if match != "" {
		query += ` ORDER BY ` + sel + ` LIMIT ?`
		args = append(args, max(q.Limit*5, 50))
	} else {
		query += ` ORDER BY COALESCE(m.occurred_at, m.recorded_at) DESC, m.id DESC LIMIT ?`
		args = append(args, max(q.Limit*5, 50))
	}
	rows, err := m.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var out []*Memory
	words := queryWords(q.Query)
	for rows.Next() {
		var bm float64
		x, err := scanMemoryWith(rows, &bm)
		if err != nil {
			rows.Close()
			return nil, err
		}
		score := 1.0
		if match != "" {
			score = -bm // bm25: lower is better
			if score <= 0 {
				score = 1e-6
			}
		}
		x.Score = score * boost(x, words, now)
		out = append(out, x)
	}
	rows.Close()
	if match != "" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	} else {
		// Without a query: newest first, the profile and fresh episodes ahead.
		sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	}
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	if q.Touch && len(out) > 0 {
		for _, x := range out {
			m.db.Exec(`UPDATE memories SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`, now.Unix(), x.ID)
		}
	}
	return out, nil
}

// boost weighs a match: its subject named in the query, its place in the
// profile, and for episodes their age (half-life of 30 days).
func boost(x *Memory, words []string, now time.Time) float64 {
	b := 1.0
	if x.Subject != "" && len(words) > 0 {
		_, name, ok := strings.Cut(x.Subject, ":")
		if !ok {
			name = x.Subject
		}
		for _, part := range strings.FieldsFunc(foldAccents(name), func(r rune) bool { return r == '-' || r == ' ' || r == '_' }) {
			if containsStr(words, part) {
				b *= 1.5
				break
			}
		}
	}
	if x.Pinned {
		b *= 1.3
	}
	if x.Kind == kindEpisode {
		t := x.OccurredAt
		if t.IsZero() {
			t = x.RecordedAt
		}
		if age := now.Sub(t); age > 0 {
			b *= math.Pow(0.5, float64(age)/float64(episodeHalfLife))
		}
	}
	return b
}

func queryWords(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(foldAccents(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out = append(out, w)
	}
	return out
}

// foldAccents lowers and strips the accents of Latin letters, like the FTS tokenizer.
func foldAccents(s string) string {
	r := strings.NewReplacer("à", "a", "â", "a", "ä", "a", "á", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
		"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u", "ÿ", "y", "ñ", "n",
		"œ", "oe", "æ", "ae")
	return r.Replace(strings.ToLower(s))
}

func scanMemoryWith(rows *sql.Rows, extra ...any) (*Memory, error) {
	var x Memory
	var links string
	var occurred, validTo, superseded, supersedes, due, used, deleted sql.NullInt64
	var from, recorded, updated int64
	dest := []any{&x.ID, &x.Kind, &x.Subject, &x.Text, &links, &occurred, &from, &validTo, &recorded, &superseded, &supersedes,
		&due, &x.State, &x.SourceKind, &x.SourceRef, &x.Trust, &x.Status, &x.Pinned, &x.Confidence, &x.UseCount, &used,
		&x.Flags, &deleted, &updated}
	if err := rows.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(links), &x.Links)
	x.OccurredAt, x.ValidTo, x.DueAt, x.LastUsedAt, x.DeletedAt = fromNull(occurred), fromNull(validTo), fromNull(due), fromNull(used), fromNull(deleted)
	x.ValidFrom, x.RecordedAt, x.UpdatedAt = time.Unix(from, 0), time.Unix(recorded, 0), time.Unix(updated, 0)
	x.SupersededBy, x.Supersedes = superseded.Int64, supersedes.Int64
	return &x, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// list reads memories for the workspace, without counting a use.
func (m *memStore) list(where string, args []any, order string, limit int) ([]*Memory, error) {
	q := `SELECT ` + memColumns + ` FROM memories`
	if where != "" {
		q += ` WHERE ` + where
	}
	if order == "" {
		order = `recorded_at DESC, id DESC`
	}
	q += ` ORDER BY ` + order + ` LIMIT ?`
	rows, err := m.db.Query(q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		x, err := scanMemoryWith(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// commitments are the open ones, the most urgent first (no due date last).
func (m *memStore) commitments(dueBefore time.Time) ([]*Memory, error) {
	where := `kind = 'commitment' AND status = 'active' AND state = 'open' AND (valid_to IS NULL OR valid_to > ?)`
	args := []any{m.now().Unix()}
	if !dueBefore.IsZero() {
		where += ` AND due_at IS NOT NULL AND due_at <= ?`
		args = append(args, dueBefore.Unix())
	}
	return m.list(where, args, `due_at IS NULL, due_at, id`, 200)
}

// profile is the pinned, current, active memories.
func (m *memStore) profile() ([]*Memory, error) {
	return m.list(`pinned = 1 AND status = 'active' AND (valid_to IS NULL OR valid_to > ?)`, []any{m.now().Unix()},
		`CASE kind WHEN 'profile' THEN 0 WHEN 'preference' THEN 1 WHEN 'lesson' THEN 2 ELSE 3 END, id`, 200)
}

// logEntry is one change of a memory.
type logEntry struct {
	Seq    int64
	Time   time.Time
	Who    string
	Op     string
	Before *Memory
	After  *Memory
	Reason string
}

func (m *memStore) history(id int64) ([]logEntry, error) {
	rows, err := m.db.Query(`SELECT seq, time, who, op, COALESCE(before, ''), COALESCE(after, ''), reason FROM memory_log
		WHERE memory_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []logEntry
	for rows.Next() {
		var e logEntry
		var t int64
		var b, a string
		if err := rows.Scan(&e.Seq, &t, &e.Who, &e.Op, &b, &a, &e.Reason); err != nil {
			return nil, err
		}
		e.Time = time.Unix(t, 0)
		if b != "" {
			e.Before = &Memory{}
			json.Unmarshal([]byte(b), e.Before)
		}
		if a != "" {
			e.After = &Memory{}
			json.Unmarshal([]byte(a), e.After)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// counts says how many memories there are by status, for the page.
func (m *memStore) count(where string, args ...any) int {
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE `+where, args...).Scan(&n)
	return n
}

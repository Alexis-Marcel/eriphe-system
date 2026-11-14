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
	kindLesson     = "lesson"
	// A choice and its reason, compiled from the raw history. Where a
	// subject stands is its thread's summary, never a memory
	// (thread_progress.go).
	kindDecision = "decision"
	// Where the user is or what they do for a while, with its end: shown
	// to the brain and every task while it holds (situations.go).
	kindSituation = "situation"
)

var memoryKinds = []string{kindProfile, kindPreference, kindFact, kindPerson, kindEpisode, kindLesson, kindDecision, kindSituation}

// How a derived memory was read (Graphify's labels): said in so many
// words, deduced, or unclear. Beside the trust, which says who it comes from.
const (
	basisStated    = "stated"
	basisInferred  = "inferred"
	basisAmbiguous = "ambiguous"
)

// basisKnown tells one of the three bases: discrete, as models follow a
// rubric better than a continuous range.
func basisKnown(b string) bool {
	return b == basisStated || b == basisInferred || b == basisAmbiguous
}

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
)

const (
	profileBudget    = 2500 // characters of pinned memories, the profile
	memoryTextMax    = 2000
	episodeTextMax   = 4000
	keywordsMax      = 200
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
	UseCount     int       `json:"use_count"`
	LastUsedAt   time.Time `json:"last_used_at,omitzero"`
	Flags        string    `json:"flags,omitempty"`      // what the scanner noticed
	Keywords     string    `json:"keywords,omitempty"`   // variants and synonyms, indexed with the text
	Importance   int       `json:"importance,omitempty"` // 1 to 5, 3 by default
	Basis        string    `json:"basis,omitempty"`      // stated, inferred, ambiguous; "" if not derived
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
	// The user's name: in nearly every memory, so not a word to look for.
	owner func() string
	// The known subjects (subjects.go): a subject written is noted there,
	// and kept under the one it was merged into. nil: as given.
	subjects subjectBook
}

// memorySchemaVersion is the schema memory.db is at; meta.schema_version
// says it. A new file is made at it; a file at 5 is brought to 6 (its
// subjects keyed, subject_rewrite.go), one at 6 to 7 (the columns nothing
// read gone, the tasks' episodes forgotten); a file at another is refused,
// never changed.
const memorySchemaVersion = 7

// memorySchema is memory.db's own tables; the intentions' and the
// lessons' are with them (intentionsSchema, learningSchema).
const memorySchema = `CREATE TABLE memories (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  kind TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', text TEXT NOT NULL,
	  links TEXT NOT NULL DEFAULT '[]',
	  occurred_at INTEGER, valid_from INTEGER NOT NULL, valid_to INTEGER, recorded_at INTEGER NOT NULL,
	  superseded_by INTEGER, supersedes INTEGER,
	  due_at INTEGER, state TEXT NOT NULL DEFAULT '',
	  source_kind TEXT NOT NULL, source_ref TEXT NOT NULL DEFAULT '',
	  trust TEXT NOT NULL, status TEXT NOT NULL,
	  pinned INTEGER NOT NULL DEFAULT 0,
	  use_count INTEGER NOT NULL DEFAULT 0, last_used_at INTEGER,
	  flags TEXT NOT NULL DEFAULT '', deleted_at INTEGER, updated_at INTEGER NOT NULL,
	  keywords TEXT NOT NULL DEFAULT '',   -- the extraction's, for the full-text index
	  importance INTEGER NOT NULL DEFAULT 3,
	  stems TEXT NOT NULL DEFAULT '',      -- the French stems, a shadow column of the index (memoryStems)
	  basis TEXT NOT NULL DEFAULT '');
	CREATE INDEX memories_status_kind ON memories (status, kind);
	CREATE INDEX memories_subject ON memories (subject);
	CREATE VIRTUAL TABLE memories_fts USING fts5(text, subject, keywords, stems, content='memories', content_rowid='id',
	  tokenize='unicode61 remove_diacritics 2');
	CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
	  INSERT INTO memories_fts (rowid, text, subject, keywords, stems) VALUES (new.id, new.text, new.subject, new.keywords, new.stems); END;
	CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
	  INSERT INTO memories_fts (memories_fts, rowid, text, subject, keywords, stems)
	    VALUES ('delete', old.id, old.text, old.subject, old.keywords, old.stems); END;
	CREATE TRIGGER memories_au AFTER UPDATE OF text, subject, keywords, stems ON memories BEGIN
	  INSERT INTO memories_fts (memories_fts, rowid, text, subject, keywords, stems)
	    VALUES ('delete', old.id, old.text, old.subject, old.keywords, old.stems);
	  INSERT INTO memories_fts (rowid, text, subject, keywords, stems) VALUES (new.id, new.text, new.subject, new.keywords, new.stems); END;
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
	CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);`

// memoryStems is a memory's stems column: its text, the name in its
// subject (person:paul: paul) and its keywords.
func memoryStems(text, subject, keywords string) string {
	if _, name, ok := strings.Cut(subject, ":"); ok {
		subject = name
	}
	return stemText(text, subject, keywords)
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
	switch version {
	case memorySchemaVersion:
		return nil
	case 5:
		if err := m.migrate5(); err != nil {
			return err
		}
		return m.migrate6()
	case 6:
		return m.migrate6()
	case 0:
	default:
		return fmt.Errorf("memory.db is at schema %d, this eriphe-core reads %d", version, memorySchemaVersion)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{memorySchema, intentionsSchema, learningSchema} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, fmt.Sprint(memorySchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrate5 brings a memory.db at schema 5 to 6: the subjects as keys, the
// index of the old commitment kind and the facts tables gone.
func (m *memStore) migrate5() error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DROP INDEX IF EXISTS memories_due`, `DROP TABLE IF EXISTS facts`, `DROP TABLE IF EXISTS facts_log`} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if err := rekeyMemory(tx); err != nil {
		return fmt.Errorf("memory.db subject keys: %w", err)
	}
	if _, err := tx.Exec(`UPDATE meta SET value = '6' WHERE key = 'schema_version'`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrate6 brings a memory.db at schema 6 to 7: the embedding and
// confidence columns, which nothing read, dropped; the episodes of tasks
// forgotten (their log and their thread's timeline keep what they did),
// purged with the other forgotten memories.
func (m *memStore) migrate6() error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, col := range []string{"embedding", "confidence"} {
		var n int
		tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name = ?`, col).Scan(&n)
		if n == 0 {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE memories DROP COLUMN ` + col); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT id FROM memories WHERE kind = 'episode' AND status != 'deleted'
		AND subject != 'topic:journal' AND (source_kind = 'task' OR source_ref LIKE 'task:%')`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := m.forgetTx(tx, id, "migration", "a task's episode: its log keeps what it did", ""); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE meta SET value = '7' WHERE key = 'schema_version'`); err != nil {
		return err
	}
	return tx.Commit()
}

// proposalExpiry is how long a proposed memory or intention waits for the
// user: undecided after it, it is dropped (expireProposals).
const proposalExpiry = 14 * 24 * time.Hour

// expireProposals forgets the proposed memories and cancels the proposed
// intentions nobody decided within proposalExpiry; their cards, if any,
// then read as rejected. It returns how many.
func (m *memStore) expireProposals() (int, error) {
	cut := m.now().Add(-proposalExpiry).Unix()
	const why = "undecided for 14 days"
	ids := func(q string) ([]int64, error) {
		rows, err := m.db.Query(q, cut)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			out = append(out, id)
		}
		return out, rows.Err()
	}
	mems, err := ids(`SELECT id FROM memories WHERE status = 'proposed' AND recorded_at < ?`)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range mems {
		if err := m.forget(id, "expiry", why); err != nil {
			return n, err
		}
		n++
	}
	ints, err := ids(`SELECT id FROM intentions WHERE state = 'proposed' AND created_at < ?`)
	if err != nil {
		return n, err
	}
	for _, id := range ints {
		if _, err := m.changeIntention(id, intentionChange{State: intentCancelled}, "expiry", why); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
	due_at, state, source_kind, source_ref, trust, status, pinned, use_count, last_used_at, flags, deleted_at, updated_at,
	keywords, importance, basis`

func scanMemory(sc interface{ Scan(...any) error }) (*Memory, error) {
	var x Memory
	var links string
	var occurred, validTo, superseded, supersedes, due, used, deleted sql.NullInt64
	var from, recorded, updated int64
	err := sc.Scan(&x.ID, &x.Kind, &x.Subject, &x.Text, &links, &occurred, &from, &validTo, &recorded, &superseded, &supersedes,
		&due, &x.State, &x.SourceKind, &x.SourceRef, &x.Trust, &x.Status, &x.Pinned, &x.UseCount, &used,
		&x.Flags, &deleted, &updated, &x.Keywords, &x.Importance, &x.Basis)
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
	subject, err := subjectKey(x.Subject)
	if err != nil {
		return fmt.Errorf("subject: %w", err)
	}
	x.Subject = m.keepSubject(subject)
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
	x.Keywords = strings.Join(strings.Fields(x.Keywords), " ")
	if utf8.RuneCountInString(x.Keywords) > keywordsMax {
		return fmt.Errorf("keywords are longer than %d characters: a few variants and synonyms", keywordsMax)
	}
	switch {
	case x.Importance == 0:
		x.Importance = 3
	case x.Importance < 1 || x.Importance > 5:
		return errors.New("importance is 1 (trivial) to 5 (essential)")
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
	switch x.Kind {
	case kindSituation:
		if x.Subject == "" {
			x.Subject = "owner"
		}
		if x.ValidTo.IsZero() || !x.ValidTo.After(x.ValidFrom) {
			return errors.New("a situation needs valid_to, its end, after valid_from")
		}
	default:
		x.State, x.DueAt = "", time.Time{}
	}
	if x.Basis != "" && !basisKnown(x.Basis) {
		return errors.New("basis is stated, inferred or ambiguous")
	}
	if x.Kind == kindProfile {
		x.Pinned = true
	}
	if x.Status == "" {
		x.Status = memActive
	}
	return nil
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
		due_at, state, source_kind, source_ref, trust, status, pinned, flags, updated_at, keywords, importance, stems, basis)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.Kind, x.Subject, x.Text, string(links), unixOrNil(x.OccurredAt), x.ValidFrom.Unix(), unixOrNil(x.ValidTo), now.Unix(),
		nullID(x.Supersedes), unixOrNil(x.DueAt), x.State, x.SourceKind, x.SourceRef, x.Trust, x.Status, x.Pinned,
		x.Flags, now.Unix(), x.Keywords, x.Importance, memoryStems(x.Text, x.Subject, x.Keywords), x.Basis)
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
	if x.Status == memActive {
		if err := m.learnWrite(tx, x, old, who, ""); err != nil {
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

	Keywords   *string
	Importance *int

	Ref string // what asked for the change (msg:N), for the learning journal; not kept on the memory
}

func (m *memStore) update(id int64, p memoryPatch, who, reason string) (*Memory, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	x, err := m.updateTx(tx, id, p, who, reason)
	if err != nil {
		return nil, err
	}
	return x, tx.Commit()
}

func (m *memStore) updateTx(tx *sql.Tx, id int64, p memoryPatch, who, reason string) (*Memory, error) {
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
	if p.Keywords != nil {
		x.Keywords = *p.Keywords
	}
	if p.Importance != nil {
		x.Importance = *p.Importance
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
		pinned = ?, status = ?, updated_at = ?, keywords = ?, importance = ?, stems = ? WHERE id = ?`, x.Kind, x.Subject, x.Text,
		string(links), unixOrNil(x.ValidTo), unixOrNil(x.DueAt), x.State, x.Pinned, x.Status, x.UpdatedAt.Unix(), x.Keywords,
		x.Importance, memoryStems(x.Text, x.Subject, x.Keywords), id); err != nil {
		return nil, err
	}
	if err := m.log(tx, id, who, "update", before, &x, reason); err != nil {
		return nil, err
	}
	if err := m.learnUpdate(tx, before, &x, who, reason, p.Ref); err != nil {
		return nil, err
	}
	return &x, nil
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
	if err := m.learnWrite(tx, x, old, who, "approved"); err != nil {
		return nil, err
	}
	return x, tx.Commit()
}

// forget hides a memory at once and purges it after memoryPurgeAfter.
func (m *memStore) forget(id int64, who, reason string) error {
	return m.forgetRef(id, who, reason, "")
}

// forgetRef is forget, with what asked for it (msg:N) for the learning journal.
func (m *memStore) forgetRef(id int64, who, reason, ref string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := m.forgetTx(tx, id, who, reason, ref); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *memStore) forgetTx(tx *sql.Tx, id int64, who, reason, ref string) error {
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
	return m.learnForget(tx, before, who, reason, ref)
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
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := m.restoreTx(tx, x, who); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *memStore) restoreTx(tx *sql.Tx, x *Memory, who string) error {
	if _, err := tx.Exec(`UPDATE memories SET status = 'active', deleted_at = NULL, updated_at = ? WHERE id = ?`, m.now().Unix(), x.ID); err != nil {
		return err
	}
	after := *x
	after.Status, after.DeletedAt = memActive, time.Time{}
	if err := m.log(tx, x.ID, who, "restore", nil, &after, ""); err != nil {
		return err
	}
	return m.learnWrite(tx, &after, nil, who, "restored")
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

// ftsQuery turns free text into an FTS5 query: its words but the question
// words ("quel est le nom de"), OR between them (bm25 ranks first the
// memories that match more of them), each as typed (a prefix for words of
// four letters or more) or by its stem (the stems column). FTS syntax in
// the input is not honoured.
func ftsQuery(q string, skip ...string) string {
	var terms []string
	add := func(t string) {
		if !containsStr(terms, t) {
			terms = append(terms, t)
		}
	}
	for _, w := range memWords(q) {
		if queryStop(w) || containsStr(skip, foldAccents(w)) {
			continue
		}
		t := `"` + w + `"`
		if utf8.RuneCountInString(w) >= 4 {
			t += "*"
		}
		add(t)
		if s := stemWord(w); s != "" {
			add(`stems : "` + s + `"`)
		}
	}
	return strings.Join(terms, " OR ")
}

// Ranking weights (docs/memory-design.md): relevance first, then bounded
// factors, so that an old memory that matches well still comes back.
const (
	bm25Weights      = `bm25(memories_fts, 1.0, 0.5, 0.6, 0.8)` // text, subject, keywords, stems
	episodeRecencyLo = 0.5                                      // an old episode keeps half its relevance
	inRangeBoost     = 1.6                                      // dated in the question's time
	outOfRangeFactor = 0.6                                      // a fact not true then
	pastOutFactor    = 0.3                                      // a fact no longer true, nor true then
	rangeOnlyScore   = 0.15                                     // in the time asked, without a word in common
)

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
	text := q.Query
	var skip []string // the user's name: a question about them says it
	if m.owner != nil {
		skip = memWords(foldAccents(m.owner()))
	}
	// Time in the question: its words are not what to look for; when the
	// brain gave no dates, the range reorders.
	var tr *timeRange
	if r, rest, ok := queryTime(text, now); ok && strings.TrimSpace(text) != "" {
		text = rest
		if q.AsOf.IsZero() && q.Since.IsZero() && q.Until.IsZero() {
			tr = &r
			if r.Month != 0 && ftsQuery(text, append(skip, eventWords...)...) != "" {
				text += " " + frenchMonth[r.Month] // what the memories say of that time
			}
		}
	}
	if ftsQuery(text, append(skip, eventWords...)...) == "" {
		text = "" // "que s'est-il passé hier ?": the dates are the question
	}
	match := ftsQuery(text, skip...)
	// How much an episode's age weighs: fully in a listing, the newest
	// first; down to a floor when it matched words; not at all when the
	// question is a time ("que s'est-il passé le 19 ?"): that time is chosen.
	ageFloor := 0.0
	switch {
	case match != "":
		ageFloor = episodeRecencyLo
	case tr != nil || !q.Since.IsZero() || !q.Until.IsZero():
		ageFloor = 1
	}
	if tr != nil && match == "" {
		// Nothing but the time: it is the question.
		q.Since, q.Until, tr = tr.From, tr.To, nil
	}
	var where []string
	var args []any
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
	if s, prefix, err := subjectFilter(q.Subject); err != nil {
		return nil, err
	} else if s != "" {
		if prefix {
			where = append(where, `m.subject LIKE ? ESCAPE '\'`)
			args = append(args, likeEscape(s)+"%")
		} else {
			where = append(where, `m.subject = ?`)
			args = append(args, m.canonSubject(s))
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
	case !q.IncludePast && tr == nil:
		// A question about another time also looks at what was true then.
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
	pool := max(q.Limit*5, 50) // enough candidates for the factors to reorder
	byID := map[int64]*Memory{}
	var out []*Memory
	collect := func(query string, args []any, relevance func(float64) float64) error {
		rows, err := m.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw float64
			x, err := scanMemoryWith(rows, &raw)
			if err != nil {
				return err
			}
			if byID[x.ID] != nil {
				continue
			}
			x.Score = relevance(raw)
			byID[x.ID] = x
			out = append(out, x)
		}
		return rows.Err()
	}
	base := strings.Join(where, " AND ")
	if match != "" {
		query := `SELECT ` + cols + `, ` + bm25Weights + ` FROM memories_fts f JOIN memories m ON m.id = f.rowid
			WHERE memories_fts MATCH ? AND ` + base + ` ORDER BY ` + bm25Weights + ` LIMIT ?`
		if err := collect(query, append(append([]any{match}, args...), pool), func(bm float64) float64 { return max(-bm, 1e-6) }); err != nil {
			return nil, err
		}
		// Relevance on [0, 1]: the best match is 1.
		best := 0.0
		for _, x := range out {
			best = max(best, x.Score)
		}
		for _, x := range out {
			x.Score /= best
		}
		if tr != nil {
			// What is dated in the time asked comes too, words or not.
			query := `SELECT ` + cols + `, 0.0 FROM memories m WHERE ` + base + ` AND ((m.occurred_at >= ? AND m.occurred_at < ?)
				OR (m.occurred_at IS NULL AND m.valid_from < ? AND (m.valid_to IS NULL OR m.valid_to > ?)))
				ORDER BY COALESCE(m.occurred_at, m.valid_from) DESC LIMIT ?`
			rangeArgs := append(append([]any{}, args...), tr.From.Unix(), tr.To.Unix(), tr.To.Unix(), tr.From.Unix(), pool)
			if err := collect(query, rangeArgs, func(float64) float64 { return rangeOnlyScore }); err != nil {
				return nil, err
			}
		}
	} else {
		query := `SELECT ` + cols + `, 1.0 FROM memories m WHERE ` + base + ` ORDER BY COALESCE(m.occurred_at, m.recorded_at) DESC, m.id DESC LIMIT ?`
		if err := collect(query, append(args, pool), func(float64) float64 { return 1 }); err != nil {
			return nil, err
		}
	}
	words := queryWords(text)
	for _, x := range out {
		x.Score *= boost(x, words, now, ageFloor)
		if tr != nil {
			x.Score *= timeFactor(x, *tr, now)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
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

// boost weighs a memory: its subject named in the query, its place in the
// profile, its importance, and for episodes their age (half-life of 30
// days), which lowers them no further than ageFloor: with a query, an old
// memory that matches well still comes back.
func boost(x *Memory, words []string, now time.Time, ageFloor float64) float64 {
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
	if x.Importance > 0 {
		b *= 0.8 + 0.1*float64(x.Importance-1) // 0.8 to 1.2
	}
	if x.Kind == kindEpisode {
		t := x.OccurredAt
		if t.IsZero() {
			t = x.RecordedAt
		}
		if age := now.Sub(t); age > 0 {
			fresh := math.Pow(0.5, float64(age)/float64(episodeHalfLife))
			b *= ageFloor + (1-ageFloor)*fresh
		}
	}
	return b
}

// timeFactor weighs a memory against the time a question asks about: an
// episode of that time, a fact true then.
func timeFactor(x *Memory, r timeRange, now time.Time) float64 {
	if x.Kind == kindEpisode || !x.OccurredAt.IsZero() {
		if r.contains(x.OccurredAt) {
			return inRangeBoost
		}
		return 1
	}
	switch {
	case r.overlaps(x.ValidFrom, x.ValidTo):
		return inRangeBoost
	case !x.Current(now):
		return pastOutFactor
	}
	return outOfRangeFactor
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
		&due, &x.State, &x.SourceKind, &x.SourceRef, &x.Trust, &x.Status, &x.Pinned, &x.UseCount, &used,
		&x.Flags, &deleted, &updated, &x.Keywords, &x.Importance, &x.Basis}
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

// profile is the pinned, current, active memories.
func (m *memStore) profile() ([]*Memory, error) {
	return m.list(`pinned = 1 AND status = 'active' AND (valid_to IS NULL OR valid_to > ?)`, []any{m.now().Unix()},
		`CASE kind WHEN 'profile' THEN 0 WHEN 'preference' THEN 1 WHEN 'lesson' THEN 2 ELSE 3 END, id`, 200)
}

// aboutOwner is what the assistant knows about the user: the profile (its
// pinned memories) and the other current memories about them (subject
// owner), the pinned ones first. Episodes are not.
func (m *memStore) aboutOwner() ([]*Memory, error) {
	return m.list(`status = 'active' AND (valid_to IS NULL OR valid_to > ?) AND kind != 'episode'
		AND (pinned = 1 OR subject = 'owner')`, []any{m.now().Unix()},
		`pinned DESC, CASE kind WHEN 'profile' THEN 0 WHEN 'preference' THEN 1 WHEN 'lesson' THEN 2 ELSE 3 END, id`, 100)
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

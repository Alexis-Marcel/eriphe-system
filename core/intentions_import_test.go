package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// oldMemorySchema is memory.db as an older eriphe-core made it: its
// intentions with memory_id, its meta and kv, and a table of the memories
// it no longer reads.
const oldMemorySchema = `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	INSERT INTO meta VALUES ('schema_version', '8');
	CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	INSERT INTO kv VALUES ('night_done', '2026-11-26');
	CREATE TABLE memories (id INTEGER PRIMARY KEY, text TEXT);
	INSERT INTO memories (text) VALUES ('Le site est hébergé sur le NAS');
	CREATE TABLE intentions (
	  id INTEGER PRIMARY KEY AUTOINCREMENT,
	  kind TEXT NOT NULL, text TEXT NOT NULL, owner TEXT NOT NULL DEFAULT 'owner', owed_to TEXT NOT NULL DEFAULT '',
	  about TEXT NOT NULL DEFAULT '', trigger TEXT NOT NULL DEFAULT 'time', cue TEXT NOT NULL DEFAULT '',
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

// oldMemoryRows: an open intention with a deadline, a proposed one drawn
// from a mail, a done one; ids with a gap, dates left NULL; and their log.
const oldMemoryRows = `INSERT INTO intentions (id, kind, text, owner, owed_to, about, trigger, cue, due_at, remind_at, state, snoozed_until,
	  surfaced_at, surfaced, next_check_at, source_kind, source_ref, trust, basis, memory_id, created_at, updated_at, closed_at, flags) VALUES
	  (3, 'promise', 'Rendre le livre à Sam', 'owner', 'person:sam', 'node:n-garage', 'time', '', 1795000000, 1794990000, 'open', NULL,
	   NULL, 0, 1794990000, 'brain', 'msg:12', 'owner', '', 41, 1794000000, 1794000100, NULL, ''),
	  (7, 'reminder', 'Payer la facture du fournisseur', 'owner', '', '', 'none', '', NULL, NULL, 'proposed', NULL,
	   NULL, 0, NULL, 'extraction', 'task:t-mail', 'external', 'a mail of the supplier', NULL, 1794100000, 1794100000, NULL, 'imperative'),
	  (8, 'waiting_for', 'Le devis du garage', 'person:sam', 'owner', 'project:garage', 'person', 'Sam', NULL, NULL, 'done', NULL,
	   1794200000, 2, NULL, 'brain', '', 'first_party', '', NULL, 1794150000, 1794300000, 1794300000, '');
	INSERT INTO intention_log (seq, intention_id, time, who, op, from_state, to_state, detail) VALUES
	  (1, 3, 1794000000, 'brain', 'add', '', 'open', ''),
	  (2, 7, 1794100000, 'extraction', 'add', '', 'proposed', ''),
	  (5, 8, 1794150000, 'brain', 'add', '', 'open', ''),
	  (6, 8, 1794300000, 'owner', 'change', 'due', 'done', 'done');`

// oldMemory makes a memory.db at path, in WAL, and leaves it open: what it
// wrote may still be in its -wal, as after a stop that did not checkpoint.
func oldMemory(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{`PRAGMA wal_autocheckpoint = 0`, oldMemorySchema, oldMemoryRows} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return db
}

func tableRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := readRows(db, query)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// At the first start, the intentions of an older eriphe-core's memory.db
// and their log come into core.db as they were: ids, states, dates,
// abouts; memory.db is left as it was. A second start imports nothing.
func TestIntentionsImportedOnce(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "memory.db")
	old := oldMemory(t, memPath)
	sum := fileSum(t, memPath)
	s, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	got, err := importIntentions(s, (&Config{DB: filepath.Join(dir, "core.db")}).memoryDB())
	if err != nil {
		t.Fatal(err)
	}
	if got != (importedIntentions{Intentions: 3, Log: 4}) {
		t.Fatalf("imported: %+v", got)
	}
	for _, q := range []string{`SELECT ` + importIntentionColumns + ` FROM intentions ORDER BY id`,
		`SELECT ` + importLogColumns + ` FROM intention_log ORDER BY seq`} {
		if want, have := tableRows(t, old, q), tableRows(t, s.db, q); !reflect.DeepEqual(want, have) {
			t.Errorf("%s:\nmemory.db %v\ncore.db   %v", q, want, have)
		}
	}
	// Read as intentions: the proposed one is still proposed, the open one keeps its deadline.
	m := &intentStore{db: s.db, now: time.Now}
	if x, err := m.intention(7); err != nil || x.State != intentProposed || x.Trust != trustExternal || x.SourceKind != "extraction" {
		t.Errorf("the proposed one: %+v %v", x, err)
	}
	if x, err := m.intention(3); err != nil || x.About != "node:n-garage" || x.DueAt.Unix() != 1795000000 || x.State != intentOpen {
		t.Errorf("the open one: %+v %v", x, err)
	}
	// A new one comes after them; the log stays append-only.
	x, err := m.addIntention(&Intention{Text: "Appeler le garage", SourceKind: srcOwner, Trust: trustOwner}, "test")
	if err != nil || x.ID <= 8 {
		t.Fatalf("a new one: %+v %v", x, err)
	}
	if _, err := s.db.Exec(`UPDATE intention_log SET detail = 'x' WHERE seq = 1`); err == nil {
		t.Error("the imported log was changed")
	}
	// memory.db: as it was, its rows there.
	if fileSum(t, memPath) != sum {
		t.Error("memory.db was written")
	}
	var n int
	old.QueryRow(`SELECT COUNT(*) FROM intentions`).Scan(&n)
	if n != 3 {
		t.Errorf("memory.db's intentions: %d", n)
	}
	// A second start: nothing more, even with core.db's intentions gone.
	s.db.Exec(`DELETE FROM intentions`)
	if got, err := importIntentions(s, memPath); err != nil || got != (importedIntentions{}) {
		t.Fatalf("a second start: %+v %v", got, err)
	}
	if s.db.QueryRow(`SELECT COUNT(*) FROM intentions`).Scan(&n); n != 0 {
		t.Errorf("imported twice: %d", n)
	}
}

// Nothing is imported into a core.db that has intentions, nor from a
// memory.db that is not there; "db" in core.json's "memory" object names
// another file.
func TestIntentionsImportSkips(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	if got, err := importIntentions(s, filepath.Join(dir, "memory.db")); err != nil || got != (importedIntentions{}) {
		t.Fatalf("no memory.db: %+v %v", got, err)
	}
	other := filepath.Join(t.TempDir(), "elsewhere.db")
	oldMemory(t, other)
	cfg := &Config{DB: filepath.Join(dir, "core.db"), Memory: &BackupConfig{MemoryDB: other}}
	if cfg.memoryDB() != other {
		t.Fatalf("configured: %s", cfg.memoryDB())
	}
	m := &intentStore{db: s.db, now: time.Now}
	if _, err := m.addIntention(&Intention{Text: "Déjà là", SourceKind: srcOwner, Trust: trustOwner}, "test"); err != nil {
		t.Fatal(err)
	}
	if got, err := importIntentions(s, cfg.memoryDB()); err != nil || got != (importedIntentions{}) {
		t.Fatalf("core.db has intentions: %+v %v", got, err)
	}
	var n int
	if s.db.QueryRow(`SELECT COUNT(*) FROM intentions`).Scan(&n); n != 1 {
		t.Errorf("intentions: %d", n)
	}
}

// core.db is copied into the backup folder once a night, from the backup's
// hour, next to the graph's copies, the same number of days kept.
func TestStoreNight(t *testing.T) {
	c, clock := testIntentsCore(t)
	backups := filepath.Join(t.TempDir(), "backups")
	cfg := *c.base
	cfg.Memory = &BackupConfig{ExtractAt: "03:00", BackupDir: backups, BackupKeep: 2}
	c.snap.Store(&cfg)
	mustIntend(t, c.intents, &Intention{Text: "Rendre le livre"})
	copies := func() []string {
		files, _ := filepath.Glob(filepath.Join(backups, "core-*.db"))
		for i, f := range files {
			files[i] = filepath.Base(f)
		}
		return files
	}
	day := time.Date(2026, 11, 17, 2, 59, 0, 0, time.Local)
	c.storeNight(day)
	if len(copies()) != 0 {
		t.Fatalf("before the hour: %v", copies())
	}
	c.storeNight(day.Add(2 * time.Minute))
	if got := copies(); !reflect.DeepEqual(got, []string{"core-2026-11-17.db"}) {
		t.Fatalf("the night: %v", got)
	}
	path := filepath.Join(backups, "core-2026-11-17.db")
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	copied, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	copied.QueryRow(`SELECT text FROM intentions`).Scan(&text)
	copied.Close()
	if text != "Rendre le livre" {
		t.Errorf("the copy's intention: %q", text)
	}
	// Once a night: a later minute leaves the copy as it is.
	os.Chtimes(path, day, day)
	c.storeNight(day.Add(time.Hour))
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(day) {
		t.Error("copied twice the same night")
	}
	// The last two nights are kept.
	for i := 1; i <= 3; i++ {
		c.storeNight(day.AddDate(0, 0, i).Add(time.Hour))
	}
	if got := copies(); !reflect.DeepEqual(got, []string{"core-2026-11-19.db", "core-2026-11-20.db"}) {
		t.Fatalf("kept: %v", got)
	}
	// Run by the intentions' clock.
	os.RemoveAll(backups)
	clock.t = day.AddDate(0, 0, 4).Add(time.Hour)
	round, stop := context.WithCancel(context.Background())
	stop()
	c.runIntentions(round)
	if got := copies(); !reflect.DeepEqual(got, []string{"core-2026-11-21.db"}) {
		t.Fatalf("the clock's night: %v", got)
	}
}

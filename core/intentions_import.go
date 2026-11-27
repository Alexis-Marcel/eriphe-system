package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// An eriphe-core before this one kept the intentions in memory.db, a file
// of their own next to core.db. At start, once, they and their log are
// copied into core.db as they are: same ids, states, dates and abouts.
// memory.db is opened read-only for it, never changed nor deleted, and
// never opened again: core.db's kv says the import was made
// (intentionsImported), and core.db then holds the intentions.

// intentionsImported is the kv key that says when memory.db was read.
const intentionsImported = "intentions_imported"

// memoryDB is the memory.db of an older eriphe-core: "db" in core.json's
// "memory" object, else next to core.db.
func (c *Config) memoryDB() string {
	if c.Memory != nil && c.Memory.MemoryDB != "" {
		return c.Memory.MemoryDB
	}
	return filepath.Join(filepath.Dir(c.DB), "memory.db")
}

// importedIntentions is what an import copied.
type importedIntentions struct {
	Intentions, Log int
}

// importIntentions copies the intentions and their log from the memory.db
// at path into s, when s holds none yet and has not read it before. It
// gives what it copied; nothing when there was nothing to do. An error
// leaves core.db as it was.
func importIntentions(s *Store, path string) (importedIntentions, error) {
	var got importedIntentions
	if s.get(intentionsImported) != "" {
		return got, nil
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM intentions`).Scan(&n); err != nil {
		return got, err
	}
	if n > 0 {
		return got, s.set(intentionsImported, "core.db had intentions already")
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return got, nil
	} else if err != nil {
		return got, err
	}
	old, err := sql.Open("sqlite", "file:"+uriPath(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return got, err
	}
	defer old.Close()
	intents, err := readRows(old, `SELECT `+importIntentionColumns+` FROM intentions ORDER BY id`)
	if err != nil {
		return got, fmt.Errorf("%s: %w", path, err)
	}
	logRows, err := readRows(old, `SELECT `+importLogColumns+` FROM intention_log ORDER BY seq`)
	if err != nil {
		return got, fmt.Errorf("%s: %w", path, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return got, err
	}
	defer tx.Rollback()
	for _, r := range intents {
		if _, err := tx.Exec(`INSERT INTO intentions (`+importIntentionColumns+`) VALUES (`+placeholders(len(r))+`)`, r...); err != nil {
			return got, err
		}
	}
	for _, r := range logRows {
		if _, err := tx.Exec(`INSERT INTO intention_log (`+importLogColumns+`) VALUES (`+placeholders(len(r))+`)`, r...); err != nil {
			return got, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		intentionsImported, time.Now().UTC().Format(time.RFC3339)+" from "+path); err != nil {
		return got, err
	}
	if err := tx.Commit(); err != nil {
		return got, err
	}
	return importedIntentions{Intentions: len(intents), Log: len(logRows)}, nil
}

// The columns copied: every one of today's tables (memory_id, the
// memory an intention came from, is gone with the memories).
const (
	importIntentionColumns = `id, kind, text, owner, owed_to, about, trigger, cue, due_at, remind_at, state, snoozed_until, surfaced_at,
	surfaced, next_check_at, source_kind, source_ref, trust, basis, created_at, updated_at, closed_at, flags`
	importLogColumns = `seq, intention_id, time, who, op, from_state, to_state, detail`
)

// readRows reads every row of a query, each as its values.
func readRows(db *sql.DB, query string) ([][]any, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]any
	for rows.Next() {
		r := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range r {
			ptrs[i] = &r[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// uriPath is a file path as an SQLite URI's path: what would end it or
// read as an escape is escaped.
func uriPath(path string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
}

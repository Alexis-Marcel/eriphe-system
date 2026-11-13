package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// sessionsToThreads moves the database of an eriphe-core that had work
// sessions (before #465) to threads, once: the tables and their columns
// renamed, every session a led thread, its phase kept and its state
// derived from it, and the refs to them rewritten where they are kept:
// cards, the attention queue, notifications, the journal, surfaces (their
// lifetime and components), kv values, skill runs, and the running leads'
// triggers. The schema made the new tables, empty, before: they go first.
func sessionsToThreads(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&n); err != nil || n == 0 {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	steps := []string{
		`DROP TABLE IF EXISTS threads`,
		`DROP TABLE IF EXISTS thread_steps`,
		`DROP TABLE IF EXISTS thread_asks`,
		`DROP TABLE IF EXISTS thread_log`,
		`DROP INDEX IF EXISTS session_steps_task`,
		`DROP INDEX IF EXISTS session_log_session`,
		`ALTER TABLE sessions RENAME TO threads`,
		`ALTER TABLE threads ADD COLUMN mode TEXT NOT NULL DEFAULT 'light'`,
		`ALTER TABLE threads ADD COLUMN subject TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN phase TEXT NOT NULL DEFAULT ''`,
		`UPDATE threads SET mode = 'led', phase = state, state = CASE state
			WHEN 'running' THEN 'open' WHEN 'proposed' THEN 'waiting' WHEN 'paused' THEN 'waiting'
			WHEN 'done' THEN 'done' ELSE 'dropped' END`,
		`ALTER TABLE session_steps RENAME TO thread_steps`,
		`ALTER TABLE thread_steps RENAME COLUMN session TO thread`,
		`ALTER TABLE session_asks RENAME TO thread_asks`,
		`ALTER TABLE thread_asks RENAME COLUMN session TO thread`,
		`ALTER TABLE session_log RENAME TO thread_log`,
		`ALTER TABLE thread_log RENAME COLUMN session TO thread`,
		`UPDATE convo_cards SET kind = 'thread', ref = 'thread:' || substr(ref, 9) WHERE kind = 'session'`,
		`UPDATE attention SET ref = 'thread:' || substr(ref, 9) WHERE ref LIKE 'session:%'`,
		`UPDATE attention SET source = 'thread:' || substr(source, 9) WHERE source LIKE 'session:%'`,
		`UPDATE notifications SET ref = 'thread:' || substr(ref, 9) WHERE ref LIKE 'session:%'`,
		`UPDATE notifications SET source = 'thread:' || substr(source, 9) WHERE source LIKE 'session:%'`,
		`UPDATE event_journal SET kind = 'thread.ended' WHERE kind = 'session.ended'`,
		`UPDATE event_journal SET key = 'thread:' || substr(key, 9) WHERE key LIKE 'session:%'`,
		`UPDATE event_journal SET source = 'thread' WHERE source = 'session'`,
		`UPDATE surfaces SET lifetime = replace(lifetime, '"session:', '"thread:'), components = replace(components, '"session:', '"thread:')`,
		`UPDATE kv SET value = 'thread:' || substr(value, 9) WHERE value LIKE 'session:%'`,
		`UPDATE kv SET value = replace(value, '"session:', '"thread:') WHERE value LIKE '%"session:%'`,
		`UPDATE catalog SET spec = replace(replace(spec, '"session:', '"thread:'), 'Chef de session', 'Chef du thread')
			WHERE kind = 'assistant' AND name LIKE 'lead-%'`,
	}
	if hasTable(tx, "session_inbox") {
		steps = append(steps, `DROP TABLE IF EXISTS thread_inbox`,
			`ALTER TABLE session_inbox RENAME TO thread_inbox`, `ALTER TABLE thread_inbox RENAME COLUMN session TO thread`)
	}
	if hasColumn(tx, "skill_runs", "session") {
		steps = append(steps, `ALTER TABLE skill_runs RENAME COLUMN session TO thread`)
	}
	for _, q := range steps {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("sessions to threads: %s: %w", strings.Join(strings.Fields(q)[:3], " "), err)
		}
	}
	for _, q := range []string{threadsSchema, threadInboxSchema} { // the indexes, under their new names
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func hasTable(tx *sql.Tx, name string) bool {
	var n int
	tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0
}

func hasColumn(tx *sql.Tx, table, column string) bool {
	var n int
	tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	return n > 0
}

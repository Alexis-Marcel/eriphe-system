package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// What Eriphe knows lives in the graph's nodes. memory.db holds only the
// intentions (memory_intentions.go), until they move to core.db: its other
// tables, in a file made before, are left as they are and never read.

// MemoryConfig names the intentions' file; it works without one. The
// nightly backup's keys are read in it too (BackupConfig, nodes.go).
type MemoryConfig struct {
	DB string `json:"db,omitempty"` // memory.db next to eriphe-core's database by default
	BackupConfig
}

func (c *Config) memoryConfig() MemoryConfig {
	var m MemoryConfig
	if c.Memory != nil {
		m = *c.Memory
	}
	if m.DB == "" {
		m.DB = filepath.Join(filepath.Dir(c.DB), "memory.db")
	}
	return m
}

// How a brain write is recorded (writeDecision.Status).
const (
	memActive   = "active"
	memProposed = "proposed" // waits for my approval
)

type memStore struct {
	db  *sql.DB
	now func() time.Time
	// The names (names.go): an about written is noted there, and kept
	// under the target it names. nil: as given.
	names aboutBook
	// moved is told after every change is committed: the cards of its
	// proposals follow them (useMemory); nil: no one.
	moved func()
}

// memorySchemaVersion is the schema memory.db is at; meta.schema_version
// says it. A new file is made at it; a file at another is refused, never
// changed.
const memorySchemaVersion = 8

func openMemory(path string) (*memStore, error) {
	if dir := filepath.Dir(path); dir != "" {
		os.MkdirAll(dir, 0o700)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(memory)")
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
	case 0:
	default:
		return fmt.Errorf("memory.db is at schema %d, this eriphe-core reads %d", version, memorySchemaVersion)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{intentionsSchema, `CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`, fmt.Sprint(memorySchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// proposalExpiry is how long a proposed intention waits for the user:
// undecided after it, it is cancelled (expireProposals).
const proposalExpiry = 14 * 24 * time.Hour

// expireProposals cancels the proposed intentions nobody decided within
// proposalExpiry; their cards, if any, then read as rejected. It returns
// how many.
func (m *memStore) expireProposals() (int, error) {
	cut := m.now().Add(-proposalExpiry).Unix()
	rows, err := m.db.Query(`SELECT id FROM intentions WHERE state = 'proposed' AND created_at < ?`, cut)
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
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if _, err := m.changeIntention(id, intentionChange{State: intentCancelled}, "expiry", "undecided for 14 days"); err != nil {
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

// commitIntention commits a change of the intentions, and tells who
// follows the store.
func (m *memStore) commitIntention(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	if m.moved != nil {
		m.moved()
	}
	return nil
}

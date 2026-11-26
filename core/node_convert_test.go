package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A block's snapshot is what core.db kept for it: its sources' last good
// values, the latest read, and the notes of its node's items; nothing for
// a block code kept nothing of, nor in a core.db without the tables.
func TestBlockSnapshots(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := st.db
	oldBlockTables(t, db)
	for _, q := range []string{
		`INSERT INTO node_reads (node, block, name, spec, value, read) VALUES ('n1', 'mails', 'm', '{}', '{"messages":[]}', 100)`,
		`INSERT INTO node_reads (node, block, name, spec, value, read) VALUES ('n1', 'mails', 'k', '{}', '{"x":1}', 200)`,
		`INSERT INTO node_reads (node, block, name, spec, value, read, error) VALUES ('n1', 'mails', 'never', '{}', '', 0, 'down')`,
		`INSERT INTO node_items (node, source, item, note, state, grp, author, tainted, noted) VALUES ('n1', 'm', 'aWQtMQ', 'à lire', 'act', 'À traiter', 'skill:mails', 'mail', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	snap := blockSnapshots(db)
	sn := snap("n1", "mails")
	if sn == nil || len(sn.Values) != 2 || string(sn.Values["m"]) != `{"messages":[]}` || !sn.Read.Equal(time.Unix(200, 0)) {
		t.Fatalf("snapshot: %+v", sn)
	}
	if n := sn.Notes["m"]; len(n) != 1 || n[0].Item != "aWQtMQ" || n[0].Group != "À traiter" || n[0].State != "act" || !n[0].Tainted {
		t.Fatalf("notes: %+v", sn.Notes)
	}
	if sn := snap("n2", "mails"); sn != nil {
		t.Fatalf("another node: %+v", sn)
	}
	bare, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "bare.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	if sn := blockSnapshots(bare)("n1", "mails"); sn != nil {
		t.Fatalf("without the tables: %+v", sn)
	}
}

// oldBlockTables makes in db the tables an older Eriphe kept for the
// blocks.
func oldBlockTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS node_reads (node TEXT NOT NULL, block TEXT NOT NULL, name TEXT NOT NULL, spec TEXT NOT NULL,
			value TEXT NOT NULL DEFAULT '', read INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', PRIMARY KEY (node, block, name))`,
		`CREATE TABLE IF NOT EXISTS node_items (node TEXT NOT NULL, source TEXT NOT NULL, item TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT '', grp TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '',
			noted INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (node, source, item))`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

// tableThere tells a table of db.
func tableThere(db *sql.DB, name string) bool {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n == 1
}

// A start converts the nodes with what core.db kept of their blocks,
// then drops it; while a node does not read (it may be one still to
// convert), core.db keeps it.
func TestBlockTablesDropped(t *testing.T) {
	cfg := &Config{DB: filepath.Join(t.TempDir(), "core.db")}
	st, err := openStore(cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	oldBlockTables(t, st.db)
	dir, _ := cfg.nodesPaths()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "broken-zz9.md")
	if err := os.WriteFile(bad, []byte("not a node"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := openNodes(cfg, st.db)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !tableThere(st.db, "node_reads") || !tableThere(st.db, "node_items") {
		t.Fatal("dropped while a node does not read")
	}
	os.Remove(bad)
	s, err = openNodes(cfg, st.db)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, name := range blockTables {
		if tableThere(st.db, name) {
			t.Errorf("%s kept once every node reads", name)
		}
	}
	// Opened again, nothing to drop: no error.
	s, err = openNodes(cfg, st.db)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

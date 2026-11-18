package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A threads table made before ends and tidied gets both at open: its
// threads end on their goal.
func TestOpenStoreAddsThreadEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, goal TEXT NOT NULL,
	done_when TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'light', about TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
	phase TEXT NOT NULL DEFAULT '', held TEXT NOT NULL DEFAULT '', budget INTEGER NOT NULL DEFAULT 0, amends INTEGER NOT NULL DEFAULT 0,
	summary TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '', lead TEXT NOT NULL DEFAULT '', told TEXT NOT NULL DEFAULT '',
	spent INTEGER NOT NULL DEFAULT 0, since INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL, started INTEGER NOT NULL DEFAULT 0,
	ended INTEGER NOT NULL DEFAULT 0, summarized INTEGER NOT NULL DEFAULT 0, seen INTEGER NOT NULL DEFAULT 0, visited INTEGER NOT NULL DEFAULT 0);
INSERT INTO threads (title, goal, state, created) VALUES ('x', 'y', 'open', 1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	c := &core{store: s}
	th, err := c.thread(1)
	if err != nil || th.Ends != endsGoal || th.tidied != 0 {
		t.Fatalf("%+v %v", th, err)
	}
}

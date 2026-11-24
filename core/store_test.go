package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A skills table made with the off_why column, never written, loses it at
// open, its skills kept; opening again changes nothing.
func TestOpenStoreDropsSkillOffWhy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE skills ADD COLUMN off_why TEXT NOT NULL DEFAULT '';
INSERT INTO skills (name, enabled, updated) VALUES ('brief', 0, 1);`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	for range 2 {
		s, err := openStore(path)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('skills') WHERE name = 'off_why'`).Scan(&n)
		ks, err := s.skills()
		if n != 0 || err != nil || len(ks) != 1 || ks[0].Name != "brief" || ks[0].Enabled {
			t.Fatalf("off_why %d, skills %+v, %v", n, ks, err)
		}
		s.db.Close()
	}
}

// A monitors table made before the hook column gets it at open, and
// opening again changes nothing.
func TestOpenStoreAddsMonitorHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE monitors (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, state TEXT NOT NULL,
  spec TEXT NOT NULL DEFAULT '{}', pending TEXT NOT NULL DEFAULT '',
  look TEXT NOT NULL DEFAULT '{}', next_at INTEGER NOT NULL DEFAULT 0,
  tainted TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL, updated INTEGER NOT NULL);
INSERT INTO monitors (name, state, created, updated) VALUES ('disk', 'on', 1, 1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		s, err := openStore(path)
		if err != nil {
			t.Fatal(err)
		}
		var hook string
		if err := s.db.QueryRow(`SELECT hook FROM monitors WHERE name = 'disk'`).Scan(&hook); err != nil || hook != "" {
			t.Fatalf("hook = %q, %v", hook, err)
		}
		s.db.Close()
	}
}

// A core.db made before a column added by hand (#736) is refused, the
// error naming the table, the column and where its SQL is; once converted
// it opens. A new one has the columns from its schema.
func TestOpenStoreRefusesUnconverted(t *testing.T) {
	for _, c := range convertedColumns {
		path := filepath.Join(t.TempDir(), "core.db")
		s, err := openStore(path) // the tables as they are, then as an older core made this one
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`ALTER TABLE ` + c.table + ` DROP COLUMN ` + c.column); err != nil {
			t.Fatal(err)
		}
		s.db.Close()
		if _, err = openStore(path); err == nil || !strings.Contains(err.Error(), c.table+"."+c.column) || !strings.Contains(err.Error(), c.by) {
			t.Fatalf("%s.%s missing: %v", c.table, c.column, err)
		}
		// Converted by hand, it opens.
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		s, err = openStore(path)
		if err != nil {
			t.Fatalf("converted, refused: %v", err)
		}
		s.db.Close()
	}
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatalf("a new core.db: %v", err)
	}
	s.db.Close()
}

func TestStore(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.registerDevice("pc", hashSecret("secret")); err != nil {
		t.Fatal(err)
	}
	if err := s.setDeviceStatus("pc", true, []byte(`{"cpus":20}`)); err != nil {
		t.Fatal(err)
	}
	if !s.checkDevice("pc", hashSecret("secret")) || s.checkDevice("pc", hashSecret("other")) {
		t.Fatal("checkDevice")
	}
	devices, _ := s.devices()
	if len(devices) != 1 || !devices[0].Online || string(devices[0].Info) != `{"cpus":20}` {
		t.Fatalf("devices = %+v", devices)
	}

	task := &TaskRow{ID: "t-1", Device: "pc", Runtime: "claude-code", Dir: "/w", Prompt: "do", State: stateRunning}
	if err := s.insertTask(task); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.activeCount("pc"); n != 1 {
		t.Fatalf("active = %d", n)
	}
	now := time.Now()
	evs := []EventRow{{Seq: 1, Time: now, Type: "text", Data: map[string]any{"text": "hi"}},
		{Seq: 2, Time: now, Type: "state", Data: map[string]any{"state": "idle"}}}
	if err := s.addEvents("t-1", evs); err != nil {
		t.Fatal(err)
	}
	if err := s.addEvents("t-1", evs[1:]); err != nil { // replays are ignored
		t.Fatal(err)
	}
	got, _ := s.task("t-1")
	if got.LastSeq != 2 {
		t.Fatalf("task = %+v", got)
	}
	last, _ := s.events("t-1", 1)
	if len(last) != 1 || last[0].Seq != 2 {
		t.Fatalf("events = %+v", last)
	}
	s.updateTask("t-1", map[string]any{"state": stateStopped})
	if active, _ := s.tasks(true, 10); len(active) != 0 {
		t.Fatalf("active tasks = %d", len(active))
	}
	if _, err := s.task("nope"); err != errNoTask {
		t.Fatalf("missing task: %v", err)
	}
}

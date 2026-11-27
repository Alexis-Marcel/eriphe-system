package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// A monitor.changed, through the journal, rewrites the line that follows
// it: its number, the unit kept, its date; no version is made. A line of
// another key, and an archived node, are left alone.
func TestLinesFollowTheJournal(t *testing.T) {
	tw, _ := graphWeb(t)
	c := tw.c
	c.subscribeEvents(c.queueHeard)
	page := "Mes placements.\n\n- PEA : [18 385 €](on:monitor.changed/pea) · relu le 25/11 à 9 h 00\n- CTO : [2 100 €](on:monitor.changed/cto)\n"
	id := mustNode(t, c, nodestore.New{Title: "Placements", Body: page})
	gone := mustNode(t, c, nodestore.New{Title: "Ancien", Body: page})
	if err := c.nodes.Archive(gone, byUser); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 26, 10, 5, 0, 0, c.userZone())
	if _, err := c.emit(journalEvent{Kind: evMonitorChanged, Key: "pea", Source: "monitor:pea", At: at,
		Payload: map[string]any{"value": 18412.4, "was": "18385"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-c.listen.events:
		c.heard(e)
	default:
		t.Fatal("the event is not queued for the nodes")
	}
	d, _ := c.nodes.Read(id, 0)
	body, _ := d.Node.Body()
	want := "Mes placements.\n\n- PEA : [18 412 €](on:monitor.changed/pea) · relu le 26/11 à 10 h 05\n- CTO : [2 100 €](on:monitor.changed/cto)\n"
	if body != want {
		t.Errorf("body:\n%s\nwant:\n%s", body, want)
	}
	if vs, _ := c.nodes.Versions(id); len(vs) != 1 {
		t.Errorf("a value made a version: %+v", vs)
	}
	if d, _ := c.nodes.Read(gone, 0); strings.Contains(string(d.Raw), "18 412") {
		t.Error("an archived node was written")
	}
}

// When only values changed, they enter the history in one version of the
// day, author readings, at the night's backup; never twice a day.
func TestNodesNightCommitsTheReadings(t *testing.T) {
	backups := filepath.Join(t.TempDir(), "backups")
	cfg := &Config{DB: filepath.Join(t.TempDir(), "core.db"), Memory: &MemoryConfig{BackupConfig: BackupConfig{ExtractAt: "03:00", BackupDir: backups}}}
	day := time.Date(2026, 11, 17, 3, 1, 0, 0, time.Local)
	dir, index := cfg.nodesPaths()
	s, _, err := nodestore.Open(dir, index, nodestore.Options{Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &core{base: cfg, nodes: s}
	c.snap.Store(cfg)
	d, err := s.Create(nodestore.New{Title: "Placements", Body: "[1](on:monitor.changed/pea)\n", Author: byBrain})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Node.Meta.ID
	value := func(v float64) {
		t.Helper()
		if _, err := s.WriteValues(id, func(n *nodefile.Node) error {
			_, err := n.Hear(nodefile.Heard{Kind: evMonitorChanged, Key: "pea", Payload: map[string]any{"value": v}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	value(2)
	value(3)
	done := c.nodesNight("", day)
	vs, _ := s.Versions(id)
	if done != "2026-11-17" || len(vs) != 2 || vs[1].Author != nodestore.Readings {
		t.Fatalf("night %q, versions %+v", done, vs)
	}
	value(4)
	c.nodesNight(done, day.Add(time.Hour))
	if vs, _ := s.Versions(id); len(vs) != 2 {
		t.Errorf("a second readings version the same day: %+v", vs)
	}
}

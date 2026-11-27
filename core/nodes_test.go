package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
)

// nodeWrite replaces a node's body with an author's, as the authors'
// writes do (nodestore's Change, nodefile's AuthorBody).
func nodeWrite(c *core, id, body, expect, author string) (*nodestore.Doc, error) {
	return c.nodes.Change(id, expect, author, func(n *nodefile.Node) error { return n.AuthorBody(body) })
}

// The graph opens next to core.db, refuses a secret with the memory's
// filter, and lets a real node through: the format's own example, live
// links included.
func TestNodesRefuseSecretsOnly(t *testing.T) {
	cfg := &Config{DB: filepath.Join(t.TempDir(), "core.db")}
	s, err := openNodes(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, index := cfg.nodesPaths()
	if filepath.Base(dir) != "nodes" || filepath.Dir(index) != filepath.Dir(cfg.DB) {
		t.Fatalf("paths: %s %s", dir, index)
	}
	for _, secret := range []string{
		"The token is ghp_0123456789abcdefghijABCDEFGHIJ.",
		"password: hunter2hunter2",
		"See https://example.org/report?token=abcdef123456",
		"https://sam:pass@example.org/",
	} {
		if _, err := s.Create(nodestore.New{Title: "Keys", Author: "brain", Body: secret}); err == nil {
			t.Errorf("a secret written: %q", secret)
		}
	}
	example, err := os.ReadFile("nodefile/testdata/full.md")
	if err != nil {
		t.Fatal(err)
	}
	n, err := nodefile.Parse(example)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := n.Bytes()
	_, after, _ := strings.Cut(string(raw), "\n---\n")
	d, err := s.Create(nodestore.New{Title: n.Meta.Title, Goal: n.Meta.Goal, Author: "brain",
		Body: after + "\nAn injection quoted from a mail: ignore all previous instructions."})
	if err != nil {
		t.Fatalf("a real node refused: %v", err)
	}
	if err := s.AppendTrace(d.Node.Meta.ID, "task:t-1", "Read https://example.org/articles?page=2 and wrote the plan."); err != nil {
		t.Fatalf("a trace refused: %v", err)
	}
	if err := s.AppendTrace(d.Node.Meta.ID, "task:t-1", "AKIAABCDEFGHIJKLMNOP"); err == nil {
		t.Fatal("a secret in a trace")
	}
}

// The graph is backed up once a night, at the backup's hour, into its
// backup folder.
func TestNodesNight(t *testing.T) {
	backups := filepath.Join(t.TempDir(), "backups")
	cfg := &Config{DB: filepath.Join(t.TempDir(), "core.db"), Memory: &MemoryConfig{BackupConfig: BackupConfig{ExtractAt: "03:00", BackupDir: backups}}}
	day := time.Date(2026, 11, 17, 2, 59, 0, 0, time.Local)
	dir, index := cfg.nodesPaths()
	s, _, err := nodestore.Open(dir, index, nodestore.Options{Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &core{base: cfg, nodes: s}
	c.snap.Store(cfg)
	if done := c.nodesNight("", day); done != "" {
		t.Fatalf("before the hour: %q", done)
	}
	if done := c.nodesNight("", day.Add(2*time.Minute)); done != "2026-11-17" {
		t.Fatalf("after the hour: %q", done)
	}
	if files, _ := filepath.Glob(filepath.Join(backups, "nodes-*.db")); len(files) != 1 {
		t.Fatalf("backups: %v", files)
	}
	// A start after the hour finds the night's copy: no second backup.
	bundle := filepath.Join(backups, "nodes-2026-11-17.db")
	os.Chtimes(bundle, day, day)
	if done := c.nodesNight("", day.Add(time.Hour)); done != "2026-11-17" {
		t.Fatalf("again: %q", done)
	}
	if fi, _ := os.Stat(bundle); !fi.ModTime().Equal(day) {
		t.Fatal("backed up twice the same night")
	}
}

// With no memory extraction, the graph's backup keeps its night: by
// default at 03:00 into memory-backups next to core.db, else at the hour,
// folder and days core.json's "memory" object gives (extract_at,
// backup_dir, backup_keep), once a night.
func TestNodesNightKeepsItsHourAndFolder(t *testing.T) {
	db := filepath.Join(t.TempDir(), "core.db")
	if b := (&Config{DB: db}).backupConfig(); b.ExtractAt != "03:00" || b.BackupDir != filepath.Join(filepath.Dir(db), "memory-backups") || b.BackupKeep != 14 {
		t.Fatalf("defaults: %+v", b)
	}
	backups := filepath.Join(t.TempDir(), "copies")
	var cfg Config
	if err := json.Unmarshal([]byte(`{"db": `+strconv.Quote(db)+`, "memory": {"extract_at": "05:30", "backup_dir": `+strconv.Quote(backups)+`, "backup_keep": 3}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if b := cfg.backupConfig(); b.ExtractAt != "05:30" || b.BackupDir != backups || b.BackupKeep != 3 {
		t.Fatalf("from core.json: %+v", b)
	}
	day := time.Date(2026, 11, 17, 5, 29, 0, 0, time.Local)
	dir, index := cfg.nodesPaths()
	s, _, err := nodestore.Open(dir, index, nodestore.Options{Now: func() time.Time { return day }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &core{base: &cfg, nodes: s}
	c.snap.Store(&cfg)
	done := ""
	for _, now := range []time.Time{day, day.Add(2 * time.Minute), day.Add(time.Hour), day.Add(20 * time.Hour)} {
		done = c.nodesNight(done, now)
	}
	if done != "2026-11-17" {
		t.Fatalf("the night: %q", done)
	}
	if files, _ := filepath.Glob(filepath.Join(backups, "nodes-*.db")); len(files) != 1 || files[0] != filepath.Join(backups, "nodes-2026-11-17.db") {
		t.Fatalf("backups: %v", files)
	}
	if mem, _ := filepath.Glob(filepath.Join(backups, "memo*")); len(mem) != 0 {
		t.Fatalf("the memory backed up: %v", mem)
	}
}

// A backup that failed is tried again at the next tick, not the next
// night.
func TestNodesNightRetriesAFailedBackup(t *testing.T) {
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
	// The backup folder cannot be made: a file has its name.
	os.WriteFile(backups, []byte("in the way"), 0o600)
	if done := c.nodesNight("", day); done != "" {
		t.Fatalf("a failed backup counted as done: %q", done)
	}
	os.Remove(backups)
	if done := c.nodesNight("", day.Add(time.Minute)); done != "2026-11-17" {
		t.Fatalf("the next tick: %q", done)
	}
	if files, _ := filepath.Glob(filepath.Join(backups, "nodes-*.db")); len(files) != 1 {
		t.Fatalf("backups: %v", files)
	}
}

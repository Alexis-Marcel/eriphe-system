package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// A node is told to get ready the agents it could run: their runtimes and
// images, not those of an agent needing a label it does not carry.
func TestNodePrepare(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"nas", "pc"} {
		if _, err := s.db.Exec(`INSERT INTO nodes (name, url, secret_hash) VALUES (?, '', 'x')`, n); err != nil {
			t.Fatal(err)
		}
	}
	s.setNodeStatus("pc", true, []byte(`{"labels":["gpu"]}`))
	c := newCore(&Config{}, s)
	m := *c.conf()
	m.Agents = map[string]Agent{
		"claude": {Runtime: "claude-code"},
		"codex":  {Runtime: "codex"},
		"local":  {Runtime: "opencode", Needs: []string{"gpu"}, Image: "ghcr.io/x/cuda-worker:1"},
		"other":  {Runtime: "claude-code", Image: "ghcr.io/x/worker:2"},
	}
	c.snap.Store(&m)
	got := c.nodePrepare("nas")
	if !slices.Equal(got["runtimes"], []string{"claude-code", "codex"}) || !slices.Equal(got["images"], []string{"ghcr.io/x/worker:2"}) {
		t.Errorf("nas: %v", got)
	}
	got = c.nodePrepare("pc")
	if !slices.Equal(got["runtimes"], []string{"claude-code", "codex", "opencode"}) ||
		!slices.Equal(got["images"], []string{"ghcr.io/x/cuda-worker:1", "ghcr.io/x/worker:2"}) {
		t.Errorf("pc: %v", got)
	}
	m.Agents = nil
	if got := c.nodePrepare("pc"); len(got["runtimes"]) != 0 || got["runtimes"] == nil {
		t.Errorf("no agent: %v (an empty list, never null)", got)
	}
}

// Between equal nodes, the one with the task's agent unpacked wins.
func TestScheduleAgentReady(t *testing.T) {
	nodes := []NodeRow{
		{Name: "a", Online: true, Info: []byte(`{"runtimes":["claude-code","codex"],"cpus":4}`)},
		{Name: "b", Online: true, Info: []byte(`{"runtimes":["claude-code","codex"],"cpus":4,"agents_ready":["acp","codex"]}`)},
	}
	active := func(string) (int, error) { return 0, nil }
	if got, _ := schedule(nodes, "", placement{Runtime: "codex"}, active); got != "b" {
		t.Errorf("codex went to %s, want b (its agent is ready)", got)
	}
	if got, _ := schedule(nodes, "", placement{Runtime: "claude-code"}, active); got != "a" {
		t.Errorf("claude-code went to %s, want a (the name breaks the tie)", got)
	}
}

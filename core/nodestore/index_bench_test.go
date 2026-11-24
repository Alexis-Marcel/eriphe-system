package nodestore

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// benchNodes is how many nodes a bench builds: small unless
// ERIPHE_BENCH_NODES is set, to a number or to anything for 10,000.
func benchNodes(small int) int {
	v := os.Getenv("ERIPHE_BENCH_NODES")
	if v == "" {
		return small
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return 10000
}

// writeBenchNode writes node i's file as a person would, outside Eriphe:
// a title, tags and properties, a text that links two other nodes.
func writeBenchNode(t testing.TB, dir string, i, nodes int, extra string) {
	t.Helper()
	tags := []string{"cuisine/recette", "cuisine/recette/dessert", "etude", "maison/travaux", "lecture"}
	id := fmt.Sprintf("b%d", i)
	text := fmt.Sprintf("Text of node %d, about apples %d. See [[Node %d]] and [[Node %d]].%s\n", i, i%50, (i+1)%nodes, (i*7)%nodes, extra)
	n, err := nodefile.New(nodefile.Meta{ID: id, Title: fmt.Sprintf("Node %d", i), Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, text)
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]any{"tags": []any{tags[i%len(tags)]}, "temps": float64(i % 120),
		"fait": fmt.Sprintf("2026-%02d-%02d", 1+i%12, 1+i%28), "statut": []string{"en attente", "fini", "en cours"}[i%3]}
	for k, v := range props {
		if err := n.Meta.SetProp(k, v); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := n.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("node-%d-%s.md", i, id)), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestIndexBench measures the index at start and at work on a graph of
// nodes with tags, properties and links: the first start that commits
// files written outside, a rebuild of a lost index, a start with nothing
// changed and one with a few files changed, a filtered find, and a write.
// It runs small unless ERIPHE_BENCH_NODES is set (a number, or 10,000).
func TestIndexBench(t *testing.T) {
	nodes := benchNodes(200)
	e := newEnv(t)
	e.opts.Now = time.Now
	s, _ := e.open(t)
	s.Close()
	for i := range nodes {
		writeBenchNode(t, e.dir, i, nodes, "")
	}
	timed := func(what string, fn func()) {
		start := time.Now()
		fn()
		t.Logf("%d nodes: %s in %v", nodes, what, time.Since(start).Round(time.Millisecond))
	}
	var rep Report
	timed("first start (files committed)", func() { s, rep = e.open(t) })
	if len(rep.Changed) != nodes {
		t.Fatalf("%d nodes committed at the first start, not %d", len(rep.Changed), nodes)
	}
	s.Close()
	for _, f := range []string{e.index, e.index + "-wal", e.index + "-shm"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	timed("rebuild of a lost index", func() { s, rep = e.open(t) })
	if !rep.Rebuilt || len(rep.Changed) != 0 {
		t.Fatalf("rebuild: %+v", rep)
	}
	if got, err := s.Select(Query{Limit: nodes + 1, Archived: true}); err != nil || len(got) != nodes {
		t.Fatalf("%d nodes after the rebuild, not %d (%v)", len(got), nodes, err)
	}
	s.Close()
	timed("start with nothing changed", func() { s, rep = e.open(t) })
	if len(rep.Changed) != 0 || rep.Behind || rep.Rebuilt {
		t.Fatalf("nothing changed: %+v", rep)
	}
	s.Close()
	for i := range 5 {
		writeBenchNode(t, e.dir, i*(nodes/5), nodes, " Changed.")
	}
	timed("start with 5 files changed", func() { s, rep = e.open(t) })
	if len(rep.Changed) != 5 {
		t.Fatalf("5 changed: %+v", rep.Changed)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	var finds, writes []time.Duration
	for range 100 {
		q := Query{Where: []Cond{{Key: "tags", Value: "cuisine"}, {Key: "temps", Op: "<", Value: float64(10 + rng.IntN(100))}, {Key: "statut", Value: "en attente"}},
			Sort: "temps", Limit: 30}
		start := time.Now()
		got, err := s.Select(q)
		if err != nil || len(got) == 0 {
			t.Fatalf("find %+v: %d, %v", q, len(got), err)
		}
		finds = append(finds, time.Since(start))
	}
	for i := range 20 {
		id := fmt.Sprintf("b%d", rng.IntN(nodes))
		cur, err := s.Read(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if _, err := write(s, id, fmt.Sprintf("Written %d.", i), cur.Version.Hash, "brain"); err != nil {
			t.Fatal(err)
		}
		writes = append(writes, time.Since(start))
	}
	s.Close()
	t.Logf("%d nodes: filtered, sorted find p50 %v p95 %v; write p50 %v p95 %v",
		nodes, pct(finds, 50), pct(finds, 95), pct(writes, 50), pct(writes, 95))
}

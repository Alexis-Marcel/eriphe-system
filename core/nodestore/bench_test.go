package nodestore

import (
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// TestStoreBench writes a graph of 1,000 nodes and 5,000 versions, and
// reports the p95 of a write and of a read of an old version. It runs
// small unless ERIPHE_BENCH_NODES is set: the full run takes minutes.
func TestStoreBench(t *testing.T) {
	nodes, versions := 20, 100
	if os.Getenv("ERIPHE_BENCH_NODES") != "" {
		nodes, versions = 1000, 5000
	}
	e := newEnv(t)
	e.opts.Now = time.Now
	s, _ := e.open(t)
	body := func(i, v int) string {
		return fmt.Sprintf("Version %d of node %d.\n\n```eriphe:block status\n{\"components\": {\"component\": \"Text\", \"text\": \"Step %d\"}, \"data\": {\"n\": %d}}\n```\n", v, i, v, v)
	}
	var writes []time.Duration
	last := map[string]string{}
	var ids []string
	for i := range nodes {
		start := time.Now()
		d, err := s.Create(New{Title: fmt.Sprintf("Node %d", i), Body: body(i, 1), Author: "brain"})
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, time.Since(start))
		last[d.Node.Meta.ID] = d.Version.Hash
		ids = append(ids, d.Node.Meta.ID)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for v := nodes; v < versions; v++ {
		id := ids[rng.IntN(len(ids))]
		start := time.Now()
		d, err := write(s, id, body(v, v), last[id], "task:bench")
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, time.Since(start))
		last[id] = d.Version.Hash
	}
	var reads []time.Duration
	for range 200 {
		id := ids[rng.IntN(len(ids))]
		start := time.Now()
		if _, err := s.Read(id, 1); err != nil {
			t.Fatal(err)
		}
		reads = append(reads, time.Since(start))
	}
	t.Logf("%d nodes, %d versions: write p50 %v p95 %v; read of version 1 p50 %v p95 %v",
		nodes, versions, pct(writes, 50), pct(writes, 95), pct(reads, 50), pct(reads, 95))
	before := dirSize(filepath.Join(e.dir, ".git"))
	start := time.Now()
	if err := s.Backup(filepath.Join(t.TempDir(), "backups"), 1); err != nil {
		t.Fatal(err)
	}
	t.Logf("repository %d MiB, nightly backup in %v", before>>20, time.Since(start).Round(time.Millisecond))
	start = time.Now()
	if err := checkRepo(s.repo); err != nil {
		t.Fatal(err)
	}
	t.Logf("the check at start reads every object in %v", time.Since(start).Round(time.Millisecond))
}

// TestFindBench indexes a graph of nodes with tags and properties, written
// as files and read at start, and reports the p95 of a filtered find (a
// tag, a number compared, a text, a sort, columns) and of one with words
// too. It runs small unless ERIPHE_BENCH_NODES is set: 10,000 nodes.
func TestFindBench(t *testing.T) {
	nodes := 300
	if os.Getenv("ERIPHE_BENCH_NODES") != "" {
		nodes = 10000
	}
	e := newEnv(t)
	s, _ := e.open(t)
	s.Close()
	tags := []string{"cuisine/recette", "cuisine/recette/dessert", "etude", "maison/travaux", "lecture"}
	for i := range nodes {
		id := fmt.Sprintf("b%d", i)
		n, err := nodefile.New(nodefile.Meta{ID: id, Title: fmt.Sprintf("Node %d", i), Lifetime: nodefile.Work, Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			fmt.Sprintf("Text of node %d, about apples %d.\n", i, i%50))
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
		if err := os.WriteFile(filepath.Join(e.dir, fmt.Sprintf("node-%d-%s.md", i, id)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	s, _ = e.open(t)
	t.Logf("%d nodes indexed at start in %v", nodes, time.Since(start).Round(time.Millisecond))
	rng := rand.New(rand.NewPCG(1, 2))
	var filtered, words []time.Duration
	for range 200 {
		q := Query{Where: []Cond{{Key: "tags", Value: "cuisine"}, {Key: "temps", Op: "<", Value: float64(10 + rng.IntN(100))}, {Key: "statut", Value: "en attente"}},
			Sort: "temps", Limit: 30}
		start := time.Now()
		got, err := s.Select(q)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("nothing found for %+v", q)
		}
		ids := make([]string, 0, len(got))
		for _, x := range got {
			ids = append(ids, x.ID)
		}
		if _, err := s.Props(ids, []string{"temps", "fait", "tags"}); err != nil {
			t.Fatal(err)
		}
		filtered = append(filtered, time.Since(start))
		q.Words = fmt.Sprintf("apples %d", rng.IntN(50))
		start = time.Now()
		if _, err := s.Select(q); err != nil {
			t.Fatal(err)
		}
		words = append(words, time.Since(start))
	}
	t.Logf("%d nodes: filtered find with columns p50 %v p95 %v; with words p50 %v p95 %v",
		nodes, pct(filtered, 50), pct(filtered, 95), pct(words, 50), pct(words, 95))
}

func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func pct(xs []time.Duration, p int) time.Duration {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[(len(s)-1)*p/100].Round(10 * time.Microsecond)
}

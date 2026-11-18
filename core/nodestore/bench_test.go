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
		d, err := s.Write(id, body(v, v), last[id], "task:bench")
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
	t.Logf("repository %d MiB, %d MiB after the nightly backup and gc (%v)", before>>20, dirSize(filepath.Join(e.dir, ".git"))>>20, time.Since(start).Round(time.Millisecond))
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

package nodestore

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoBytes is the size of the repository's .git.
func repoBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	err := filepath.WalkDir(filepath.Join(dir, ".git"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err == nil {
			n += fi.Size()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A trace is a plain file outside the repository, appended to: no
// commit, no version, the repository the same size; a secret refused.
func TestTraceAppend(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Research", Body: "Doc."})
	head, _ := s.headHash()
	size := repoBytes(t, e.dir)
	long := strings.Repeat("A long report line.\n", 2000)
	if err := s.AppendTrace("n1", "task:t1", "Started."); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrace("n1", "task:t1", long); err != nil {
		t.Fatal(err)
	}
	tr, _ := s.Trace("n1")
	if !strings.HasPrefix(string(tr), "## ") || !strings.Contains(string(tr), "· task:t1\n\nStarted.\n\n") || !strings.Contains(string(tr), long) {
		t.Fatalf("trace:\n%.300s", tr)
	}
	if _, err := os.Stat(filepath.Join(e.dir+"-trace", "n1.trace.md")); err != nil {
		t.Fatal(err)
	}
	if matches, _ := filepath.Glob(filepath.Join(e.dir, "*.trace.md")); len(matches) > 0 {
		t.Fatalf("a trace in the repository: %v", matches)
	}
	for range 10000 {
		if err := s.AppendTrace("n1", "task:t1", "Une ligne de plus."); err != nil {
			t.Fatal(err)
		}
	}
	if now, _ := s.headHash(); now != head {
		t.Fatal("a trace made a commit")
	}
	if got := repoBytes(t, e.dir); got != size {
		t.Fatalf("the repository grew: %d, was %d", got, size)
	}
	if last, _ := s.Read("n1", 0); last.Version.N != 1 || last.Version.Hash != d.Version.Hash {
		t.Fatalf("a trace made a version: %+v", last.Version)
	}
	if err := s.AppendTrace("n1", "task:t1", "token ghp_abc"); err == nil {
		t.Fatal("a secret in the trace")
	}
	if err := s.AppendTrace("zz", "task:t1", "x"); err == nil {
		t.Fatal("a trace for no node")
	}
}

// Past MaxTraceFile, a trace's file becomes its older part, the one
// before dropped: Trace gives both, the older first.
func TestTraceRotates(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Research"})
	chunk := strings.Repeat("x", MaxTrace-100)
	n := MaxTraceFile/len(chunk) + 1
	for i := range 2*n + 1 {
		text := chunk
		if i == 0 {
			text = "first"
		}
		if i == n+1 {
			text = "middle"
		}
		if err := s.AppendTrace("n1", "brain", text); err != nil {
			t.Fatal(err)
		}
	}
	dir := e.dir + "-trace"
	for _, f := range []string{"n1.trace.md", "n1.trace.1.md"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || fi.Size() > MaxTraceFile {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "n1.trace.2.md")); err == nil {
		t.Fatal("a third part")
	}
	tr, _ := s.Trace("n1")
	if strings.Contains(string(tr), "first") || !strings.Contains(string(tr), "middle") {
		t.Fatalf("the oldest part is kept, or the one before lost (%d bytes)", len(tr))
	}
}

// The night's backup holds the traces, both parts, in a tar beside the
// bundle; the older nights' tars go with their bundles.
func TestBackupHoldsTraces(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Research"})
	mustCreate(t, s, New{Title: "Other"})
	for _, x := range [][2]string{{"n1", "Sam a répondu."}, {"n2", "Rien de neuf."}} {
		if err := s.AppendTrace(x[0], "brain", x[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.dir+"-trace", "n1.trace.1.md"), []byte("## older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(t.TempDir(), "backups")
	if err := s.Backup(backups, 1); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(backups, "nodes-2026-11-17.trace.tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := map[string]string{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
	}
	if len(got) != 3 || !strings.Contains(got["n1.trace.md"], "Sam a répondu.") || !strings.Contains(got["n2.trace.md"], "Rien de neuf.") || got["n1.trace.1.md"] != "## older\n" {
		t.Fatalf("tar: %v", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(backups, "*.tmp")); len(leftovers) > 0 {
		t.Fatalf("left %v", leftovers)
	}
	// Another night: the one before goes, its tar with it.
	s.opts.Now = func() time.Time { return time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC) }
	if err := s.Backup(backups, 1); err != nil {
		t.Fatal(err)
	}
	if tars, _ := filepath.Glob(filepath.Join(backups, "nodes-*.trace.tar")); len(tars) != 1 || !strings.HasSuffix(tars[0], "2026-11-18.trace.tar") {
		t.Fatalf("tars %v", tars)
	}
}

// A repository whose history holds what was written before (a trace
// committed beside its node, a block with its sources' snapshot, a step's
// after) is indexed again whole once nodes.db is deleted: every version
// reads.
func TestOldHistoryRebuilds(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Research", Body: "Doc."})
	old := strings.Replace(string(d.Raw), "lifetime:", "after: [n9]\nlifetime:", 1) +
		"\n```eriphe:block b\n{\"components\":[{\"component\":\"Text\",\"id\":\"root\",\"text\":\"Hi\"}],\"snapshot\":{\"at\":\"2026-11-17T09:00:00Z\"}}\n```\n"
	at := s.opts.Now()
	if _, err := s.commit("change n1", "eriphe", at, []change{{path: d.File, data: []byte(old)}}, []stamp{{"n1", 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.commit("trace n1", "brain", at, []change{{path: "research-n1.trace.md", data: []byte("# Research\n\n## x\n")}}, nil); err != nil {
		t.Fatal(err)
	}
	// By hand: the trace moved out of the repository, in a commit.
	if _, err := s.commit("traces out", "user", at, []change{{path: "research-n1.trace.md"}}, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	os.Remove(e.index)
	s, rep := e.open(t)
	if !rep.Rebuilt || len(rep.Broken) > 0 || len(rep.Changed) > 0 {
		t.Fatalf("report %+v", rep)
	}
	for _, n := range []int{1, 2} {
		if _, err := s.Read("n1", n); err != nil {
			t.Fatalf("v%d: %v", n, err)
		}
	}
}

// An archived node keeps its trace, and one reopened goes on with it.
func TestTraceArchived(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Research"})
	if err := s.AppendTrace("n1", "brain", "Before."); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	if tr, _ := s.Trace("n1"); !strings.Contains(string(tr), "Before.") {
		t.Fatalf("archived: %s", tr)
	}
	if err := s.Reopen("n1", "user"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTrace("n1", "brain", "After."); err != nil {
		t.Fatal(err)
	}
	if tr, _ := s.Trace("n1"); !strings.Contains(string(tr), "Before.") || !strings.Contains(string(tr), "After.") {
		t.Fatalf("reopened: %s", tr)
	}
}

package nodestore

import (
	"database/sql"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Commits of node files, a node's media in its folder, one changed and
// one removed by hand, stage them all in git's index: the worktree is
// clean after them, and the last commit holds each file's bytes.
func TestCommitStagesEveryFile(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Alpha", Body: "First."})
	b := mustCreate(t, s, New{Title: "Beta", Body: "Second."})
	if _, err := write(s, a.Node.Meta.ID, "First, again.", a.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	media, err := s.PutFile(b.Node.Meta.ID, "brain", "png", []byte("\x89PNG\r\n\x1a\nimage"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	// By hand: one file changed, one removed.
	if err := os.WriteFile(filepath.Join(e.dir, a.File), []byte(string(mustReadFile(t, e.dir, a.File))+"\nBy hand.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.dir, b.File)); err != nil {
		t.Fatal(err)
	}
	s, rep := e.open(t)
	defer s.Close()
	if !slices.Equal(rep.Changed, []string{a.Node.Meta.ID}) || !slices.Equal(rep.Gone, []string{b.Node.Meta.ID}) {
		t.Fatalf("report: %+v", rep)
	}
	wt, err := s.repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	st, err := wt.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsClean() {
		t.Fatalf("the worktree is not clean after the commits:\n%s", st)
	}
	head, err := s.repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{a.File, "files/" + b.Node.Meta.ID + "/" + media} {
		got, err := c.File(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if want := blobHash(mustReadFile(t, e.dir, f)); got.Hash.String() != want {
			t.Errorf("%s committed as %s, its bytes are %s", f, got.Hash, want)
		}
	}
	if _, err := c.File(b.File); err == nil {
		t.Errorf("%s removed by hand is still in the last commit", b.File)
	}
}

// A node's words are replaced at each write, never added to; a node
// forgotten takes its words with it, even after the index was rebuilt.
func TestWordsReplaced(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Garden", Body: "Tomatoes and basil."})
	mustCreate(t, s, New{Title: "Kitchen", Body: "Basil pesto."})
	if _, err := write(s, a.Node.Meta.ID, "Courgettes only.", a.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	find := func(q string) []string {
		t.Helper()
		got, err := s.Find(q, true, 0)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, x := range got {
			ids = append(ids, x.ID)
		}
		slices.Sort(ids)
		return ids
	}
	if got := find("tomatoes"); got != nil {
		t.Errorf("an old word still finds the node: %v", got)
	}
	if got := find("courgettes"); !slices.Equal(got, []string{"n1"}) {
		t.Errorf("courgettes: %v", got)
	}
	if got := find("basil"); !slices.Equal(got, []string{"n2"}) {
		t.Errorf("basil: %v", got)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_fts`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("%d rows of words for 2 nodes (%v)", rows, err)
	}
	s.Close()
	// Rebuilt, then one removed by hand: its words go with it.
	for _, f := range []string{e.index, e.index + "-wal", e.index + "-shm"} {
		os.Remove(f)
	}
	s, _ = e.open(t)
	if err := os.Remove(filepath.Join(e.dir, a.File)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, _ = e.open(t)
	defer s.Close()
	if got := find("courgettes"); got != nil {
		t.Errorf("a node removed is still found: %v", got)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_fts`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("%d rows of words for 1 node (%v)", rows, err)
	}
}

func mustReadFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// An index at an older schema is made again from the files and the log:
// every node's file, and the repository, are left as they were; one at a
// newer schema is refused, left as it is.
func TestOldIndexSchemaRebuilt(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Garden", Body: "Tomatoes and basil."})
	if _, err := write(s, a.Node.Meta.ID, "Courgettes.", a.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, New{Title: "Kitchen", Body: "See [[Garden]]."})
	before := snapshot(t, e.dir)
	head, _ := s.headHash()
	s.Close()
	setSchema(t, e.index, schemaVersion-1)
	s, rep := e.open(t)
	if !rep.Rebuilt || rep.OldSchema != schemaVersion-1 || len(rep.Changed) != 0 || rep.Converted != nil {
		t.Fatalf("an old index: %+v", rep)
	}
	if now, _ := s.headHash(); now != head {
		t.Errorf("the repository moved: %s, was %s", now, head)
	}
	if got := mustRead(t, s, a.Node.Meta.ID); got.Last != 2 || got.Version.Hash == "" {
		t.Errorf("versions not read again from the log: %+v", got.Version)
	}
	if got, _ := s.Find("courgettes", false, 0); len(got) != 1 {
		t.Errorf("words not indexed again: %+v", got)
	}
	if sum, _ := s.Get("n2"); !slices.Equal(sum.Links, []string{a.Node.Meta.ID}) {
		t.Errorf("links not resolved again: %+v", sum.Links)
	}
	s.Close()
	if after := snapshot(t, e.dir); !maps.Equal(before, after) {
		t.Errorf("the repository's files changed")
	}
	setSchema(t, e.index, schemaVersion+1)
	if _, _, err := Open(e.dir, e.index, e.opts); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("a newer index opened: %v", err)
	}
	if v := schemaOf(t, e.index); v != schemaVersion+1 {
		t.Errorf("a newer index was changed: schema %d", v)
	}
}

func setSchema(t *testing.T, index string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", index)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE meta SET value = ? WHERE key = 'schema_version'`, fmt.Sprint(v)); err != nil {
		t.Fatal(err)
	}
}

func schemaOf(t *testing.T, index string) int {
	t.Helper()
	db, err := sql.Open("sqlite", index)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// snapshot is every file under dir, .git included: its bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		out[p] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

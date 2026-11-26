package nodestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// testStore opens a store in a test's folder, with a clock that moves a
// second a call and ids n1, n2…
type testEnv struct {
	dir, index string
	opts       Options
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	clock := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	ids := 0
	return &testEnv{
		dir:   filepath.Join(root, "nodes"),
		index: filepath.Join(root, "nodes.db"),
		opts: Options{
			Now:   func() time.Time { clock = clock.Add(time.Second); return clock },
			NewID: func() string { ids++; return fmt.Sprintf("n%d", ids) },
			Refuse: func(text string) error {
				if strings.Contains(text, "ghp_") {
					return errors.New("it looks like a credential")
				}
				return nil
			},
		},
	}
}

func (e *testEnv) open(t *testing.T) (*Store, Report) {
	t.Helper()
	s, rep, err := Open(e.dir, e.index, e.opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, rep
}

func mustCreate(t *testing.T, s *Store, nw New) *Doc {
	t.Helper()
	if nw.Author == "" {
		nw.Author = "brain"
	}
	d, err := s.Create(nw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// write replaces a node's body with an author's, as the authors' writes
// do (Change, AuthorBody).
func write(s *Store, id, body, expect, author string) (*Doc, error) {
	return s.Change(id, expect, author, func(n *nodefile.Node) error { return n.AuthorBody(body) })
}

const statusBlock = "\n> [!info]\n> Waiting for Sam.\n"

func TestCreateWriteAndVersions(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Écrire à Sam : le plan", Goal: "Answer Sam.", Body: "First draft." + statusBlock})
	if d.File != "ecrire-a-sam-le-plan-n1.md" || d.Version.N != 1 || d.Version.Author != "brain" {
		t.Fatalf("created %s v%d by %s", d.File, d.Version.N, d.Version.Author)
	}
	if !strings.HasSuffix(string(d.Raw), "First draft."+statusBlock) {
		t.Fatalf("written:\n%s", d.Raw)
	}
	onDisk, _ := os.ReadFile(filepath.Join(e.dir, d.File))
	if string(onDisk) != string(d.Raw) {
		t.Fatal("the file on disk is not the node written")
	}
	d2, err := write(s, "n1", "Second draft."+statusBlock, d.Version.Hash, "task:t1")
	if err != nil {
		t.Fatal(err)
	}
	if d2.Version.N != 2 || d2.Version.Hash == d.Version.Hash || d2.Version.Author != "task:t1" {
		t.Fatalf("second version: %+v", d2.Version)
	}
	// The same body again makes no version.
	same, err := write(s, "n1", "Second draft."+statusBlock, d2.Version.Hash, "brain")
	if err != nil || same.Version.N != 2 {
		t.Fatalf("a body alike: v%d, %v", same.Version.N, err)
	}
	// A new title would not rename the file: the slug is frozen.
	if sum, _ := s.Get("n1"); sum.File != d.File || sum.Version != 2 {
		t.Fatalf("index: %+v", sum)
	}
	// Each write is one commit, by its author.
	r, _ := git.PlainOpen(e.dir)
	c, err := r.CommitObject(plumbing.NewHash(d2.Version.Hash))
	if err != nil || c.Author.Name != "task:t1" || !strings.Contains(c.Message, "Node: n1 2") {
		t.Fatalf("commit: %v %+v", err, c)
	}
	vs, _ := s.Versions("n1")
	if len(vs) != 2 || vs[0].Hash != d.Version.Hash {
		t.Fatalf("versions: %+v", vs)
	}
	// Its goal is in the index, changed with it.
	if sum, _ := s.Get("n1"); sum.Goal != "Answer Sam." {
		t.Fatalf("goal %q", sum.Goal)
	}
	if _, err := s.Change("n1", d2.Version.Hash, "brain", func(n *nodefile.Node) error { n.Meta.Goal = "Answer Sam today."; return nil }); err != nil {
		t.Fatal(err)
	}
	if sum, _ := s.Get("n1"); sum.Goal != "Answer Sam today." {
		t.Fatalf("goal changed: %q", sum.Goal)
	}
}

func TestReadAnOldVersion(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "Version one."})
	expect := d.Version.Hash
	for i := 2; i <= 4; i++ {
		d, err := write(s, "n1", fmt.Sprintf("Version %d.", i), expect, "brain")
		if err != nil {
			t.Fatal(err)
		}
		expect = d.Version.Hash
	}
	old, err := s.Read("n1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.Node.Text() != "Version one." || old.Version.N != 1 || old.Last != 4 {
		t.Fatalf("v1: %q v%d of %d", old.Node.Text(), old.Version.N, old.Last)
	}
	last, _ := s.Read("n1", 0)
	if last.Node.Text() != "Version 4." || last.Version.N != 4 {
		t.Fatalf("last: %q v%d", last.Node.Text(), last.Version.N)
	}
	if _, err := s.Read("n1", 5); err == nil {
		t.Fatal("version 5 does not exist")
	}
}

func TestStaleExpectRefused(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "One."})
	if _, err := write(s, "n1", "Two.", d.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	_, err := write(s, "n1", "Three, on one.", d.Version.Hash, "task:t2")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale expect: %v", err)
	}
	if _, err := write(s, "n1", "No expect.", "", "brain"); err == nil {
		t.Fatal("a write without expect is refused")
	}
	if last, _ := s.Read("n1", 0); last.Node.Text() != "Two." {
		t.Fatalf("the newer version was written over: %q", last.Node.Text())
	}
}

func TestAuthorCannotWriteCodeFields(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	_, err := s.Create(New{Title: "External", Author: "brain", Body: "> [!figures|external]\n> - PEA : [18 €](on:monitor.changed/pea)\n"})
	if !errors.Is(err, nodefile.ErrCodeField) {
		t.Fatalf("a new callout marked external by its author: %v", err)
	}
}

func TestLimits(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	if _, err := s.Create(New{Title: "Big", Author: "brain", Body: strings.Repeat("words ", MaxBytes/5)}); err == nil {
		t.Fatal("too big a node")
	}
}

func TestArchiveCascade(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Mail from Sam", Body: "Triage."})
	mustCreate(t, s, New{Title: "Reply", Parent: "n1", Body: "Draft."})
	mustCreate(t, s, New{Title: "Research the error", Parent: "n1", Body: "Logs."})
	mustCreate(t, s, New{Title: "Deeper", Parent: "n3", Body: "More logs."})
	mustCreate(t, s, New{Title: "Substack", Body: "Newsletter."})
	if err := s.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		d, err := s.Read(id, 0)
		if err != nil || d.Node.Meta.Archived.IsZero() || d.Version.N != 2 {
			t.Fatalf("%s not archived: %v %+v", id, err, d)
		}
	}
	if d, _ := s.Read("n5", 0); !d.Node.Meta.Archived.IsZero() {
		t.Fatal("a node outside was archived")
	}
	// One commit for the cascade.
	a, _ := s.Read("n1", 0)
	b, _ := s.Read("n4", 0)
	if a.Version.Hash != b.Version.Hash {
		t.Fatal("the cascade is not one commit")
	}
	if _, err := write(s, "n2", "Late.", a.Version.Hash, "brain"); !errors.Is(err, ErrArchived) {
		t.Fatalf("a write to an archived node: %v", err)
	}
	kids, _ := s.Children("n1")
	if len(kids) != 2 || kids[0].ID != "n2" || kids[1].ID != "n3" || kids[0].Archived.IsZero() {
		t.Fatalf("children: %+v", kids)
	}
	// Archived nodes stay findable, when asked.
	if got, _ := s.Find("research error", false, 0); len(got) != 0 {
		t.Fatalf("archived nodes found without asking: %+v", got)
	}
	if got, _ := s.Find("research error", true, 0); len(got) != 1 || got[0].ID != "n3" {
		t.Fatalf("archived on request: %+v", got)
	}
}

func TestFind(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Substack", Body: "The newsletter and its first article."})
	mustCreate(t, s, New{Title: "Style", Parent: "n1", Goal: "Analyse the articles that work.", Body: "Short paragraphs."})
	mustCreate(t, s, New{Title: "Prod errors", Body: "> [!warning]\n> Spike of timeouts at night\n"})
	cases := map[string][]string{
		"newsletter":        {"n1"},
		"ARTICLE":           {"n1", "n2"},
		"analyse":           {"n2"},
		"timeouts":          {"n3"},
		"prod":              {"n3"},
		"article newsl":     {"n1", "n2"}, // n1 holds both words: first
		"prod agenda zz":    {"n3"},       // a word no node holds hides nothing, a short one is left out
		"de":                nil,          // only short words: they are the query
		`"); DROP nodes --`: nil,
		"":                  nil,
	}
	for q, want := range cases {
		got, err := s.Find(q, false, 0)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		var ids []string
		for _, x := range got {
			ids = append(ids, x.ID)
		}
		if strings.Join(ids, ",") != strings.Join(want, ",") && !(len(want) == 2 && len(ids) == 2) {
			t.Errorf("find %q: %v, want %v", q, ids, want)
		}
	}
	if got, _ := s.Find("article newsl", false, 0); len(got) == 0 || got[0].ID != "n1" {
		t.Errorf("the node that holds the most words is not first: %+v", got)
	}
}

func mustRead(t *testing.T, s *Store, id string) *Doc {
	t.Helper()
	d, err := s.Read(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSecretRefused(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	if _, err := s.Create(New{Title: "Keys", Author: "brain", Body: "The key is ghp_0123456789abcdefghij."}); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("a secret in a new node: %v", err)
	}
	d := mustCreate(t, s, New{Title: "Keys", Body: "None here."})
	if _, err := write(s, d.Node.Meta.ID, "ghp_0123456789abcdefghij", d.Version.Hash, "brain"); err == nil {
		t.Fatal("a secret in a write")
	}
	if files, _ := filepath.Glob(filepath.Join(e.dir, "*.md")); len(files) != 1 {
		t.Fatalf("files: %v", files)
	}
	if last := mustRead(t, s, d.Node.Meta.ID); last.Version.N != 1 {
		t.Fatalf("a refused write made a version: %d", last.Version.N)
	}
}

func TestCreateChecksParentAndLinks(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	if _, err := s.Create(New{Title: "Orphan", Parent: "zz", Author: "brain"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown parent: %v", err)
	}
	if _, err := s.Create(New{Title: "Linked", Links: []string{"zz"}, Author: "brain"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown link: %v", err)
	}
	if _, err := s.Create(New{Title: "No author"}); err == nil {
		t.Fatal("a write without author")
	}
}

func TestFileChangedByHandIsReindexed(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "Brain's words."})
	mustCreate(t, s, New{Title: "Other", Body: "Untouched."})
	s.Close()
	edited := strings.Replace(string(d.Raw), "Brain's words.", "Edited by hand: zeppelin.", 1)
	os.WriteFile(filepath.Join(e.dir, d.File), []byte(edited), 0o600)
	os.WriteFile(filepath.Join(e.dir, "broken-n9.md"), []byte("no front matter"), 0o600)

	s2, rep := e.open(t)
	if rep.Rebuilt || strings.Join(rep.Changed, ",") != "n1" || len(rep.Broken) != 1 || !strings.HasPrefix(rep.Broken[0], "broken-n9.md: ") {
		t.Fatalf("report: %+v", rep)
	}
	last := mustRead(t, s2, "n1")
	if last.Version.N != 2 || last.Version.Author != Outside || !strings.Contains(last.Node.Text(), "zeppelin") {
		t.Fatalf("the change by hand: v%d by %s: %q", last.Version.N, last.Version.Author, last.Node.Text())
	}
	if got, _ := s2.Find("zeppelin", false, 0); len(got) != 1 {
		t.Fatalf("the change is not indexed: %+v", got)
	}
	if old, _ := s2.Read("n1", 1); old.Node.Text() != "Brain's words." {
		t.Fatalf("v1 lost: %q", old.Node.Text())
	}
	s2.Close()
	// Started again, nothing more to report.
	_, rep = e.open(t)
	if len(rep.Changed) != 0 || len(rep.Gone) != 0 {
		t.Fatalf("second start: %+v", rep)
	}
}

func TestFileRemovedByHand(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Gone", Body: "x"})
	s.Close()
	os.Remove(filepath.Join(e.dir, d.File))
	s2, rep := e.open(t)
	if strings.Join(rep.Gone, ",") != "n1" {
		t.Fatalf("report: %+v", rep)
	}
	if _, err := s2.Get("n1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a removed node is still indexed: %v", err)
	}
	if head, _ := s2.headFiles(); len(head) != 0 {
		t.Fatalf("the removal is not committed: %v", head)
	}
}

func TestIndexRebuiltFromFiles(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Substack", Body: "Newsletter.\n\n- Readers : [120](on:monitor.changed/subs)\n"})
	mustCreate(t, s, New{Title: "Style", Parent: "n1", Links: []string{"n1"}, Body: "Short."})
	if _, err := write(s, "n1", "Newsletter, v2.", d.Version.Hash, "user"); err != nil {
		t.Fatal(err)
	}
	s.AppendTrace("n2", "task:t1", "Done.")
	if err := s.Archive("n2", "user"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Versions("n1")
	s.Close()
	os.Remove(e.index)
	os.Remove(e.index + "-wal")
	os.Remove(e.index + "-shm")

	s2, rep := e.open(t)
	if !rep.Rebuilt || len(rep.Changed) != 0 || len(rep.Broken) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	after, _ := s2.Versions("n1")
	if len(after) != len(before) || after[1].Hash != before[1].Hash || after[1].Author != "user" {
		t.Fatalf("versions: %+v, were %+v", after, before)
	}
	if old, _ := s2.Read("n1", 1); !strings.HasPrefix(old.Node.Text(), "Newsletter.") {
		t.Fatalf("v1: %q", old.Node.Text())
	}
	sum, _ := s2.Get("n2")
	if sum.Parent != "n1" || sum.Archived.IsZero() || sum.Version != 2 || sum.File != "style-n2.md" {
		t.Fatalf("n2: %+v", sum)
	}
	kids, _ := s2.Children("n1")
	if len(kids) != 1 {
		t.Fatalf("children: %+v", kids)
	}
	var links, listens int
	s2.db.QueryRow(`SELECT COUNT(*) FROM node_links WHERE id = 'n2' AND target = 'n1'`).Scan(&links)
	s2.db.QueryRow(`SELECT COUNT(*) FROM node_listens WHERE id = 'n1'`).Scan(&listens)
	if links != 1 || listens != 0 { // v2 of n1 dropped its line
		t.Fatalf("links %d, listens %d", links, listens)
	}
	if got, _ := s2.Find("style", true, 0); len(got) != 1 {
		t.Fatalf("find after rebuild: %+v", got)
	}
}

func TestBackup(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	backups := filepath.Join(t.TempDir(), "backups")
	if err := s.Backup(backups, 2); err != nil {
		t.Fatalf("an empty graph: %v", err)
	}
	d := mustCreate(t, s, New{Title: "Plan", Body: "One."})
	write(s, "n1", "Two.", d.Version.Hash, "brain")
	if err := s.Backup(backups, 2); err != nil {
		t.Fatal(err)
	}
	day := "2026-11-17"
	if _, err := os.Stat(filepath.Join(backups, "nodes-"+day+".db")); err != nil {
		t.Fatal(err)
	}
	// The bundle clones back into a repository with the same history.
	bundle := filepath.Join(backups, "nodes-"+day+".bundle")
	raw, err := os.ReadFile(bundle)
	if err != nil || !strings.HasPrefix(string(raw), "# v2 git bundle\n") {
		t.Fatalf("bundle: %v %.40q", err, raw)
	}
	got := decodeBundle(t, raw)
	vs, _ := s.Versions("n1")
	for _, v := range vs {
		c, err := object.GetCommit(got, plumbing.NewHash(v.Hash))
		if err != nil {
			t.Fatalf("the bundle lacks v%d: %v", v.N, err)
		}
		if f, err := c.File(d.File); err != nil {
			t.Fatalf("v%d: %v", v.N, err)
		} else if text, _ := f.Contents(); !strings.Contains(text, "title: Plan") {
			t.Fatalf("v%d: %q", v.N, text)
		}
	}
	// The store still reads and writes.
	if old, err := s.Read("n1", 1); err != nil || old.Node.Text() != "One." {
		t.Fatalf("after the backup: %v", err)
	}
	if _, err := write(s, "n1", "Three.", vs[1].Hash, "brain"); err != nil {
		t.Fatalf("a write after the backup: %v", err)
	}
}

func TestIndexKeptOutsideTheRepository(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Open(root, filepath.Join(root, "nodes.db"), Options{}); err == nil {
		t.Fatal("an index inside the repository")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Écrire à Sam":          "ecrire-a-sam",
		"  ":                    "node",
		"5xx — per minute!":     "5xx-per-minute",
		"日本語":                   "node",
		strings.Repeat("a", 60): strings.Repeat("a", 40),
		"Ma journée":            "ma-journee",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// decodeBundle reads a bundle's pack into a storage in memory.
func decodeBundle(t *testing.T, raw []byte) *memory.Storage {
	t.Helper()
	head, pack, ok := strings.Cut(string(raw), "\n\n")
	if !ok || len(strings.Split(head, "\n")) != 3 {
		t.Fatalf("bundle header: %q", head)
	}
	st := memory.NewStorage()
	if err := packfile.UpdateObjectStorage(st, strings.NewReader(pack)); err != nil {
		t.Fatal(err)
	}
	return st
}

// A copy of the index put back (last night's backup) is behind the
// repository: the versions made since are read again from the log, and a
// write made on a version older than the last is still a conflict.
func TestIndexBehindTheRepository(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "One."})
	backups := filepath.Join(t.TempDir(), "backups")
	if err := s.Backup(backups, 1); err != nil {
		t.Fatal(err)
	}
	d2, err := write(s, "n1", "Two.", d.Version.Hash, "brain")
	if err != nil {
		t.Fatal(err)
	}
	d3, err := write(s, "n1", "Three.", d2.Version.Hash, "user")
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, New{Title: "Born since", Body: "New."})
	s.Close()
	copyFile(t, filepath.Join(backups, "nodes-2026-11-17.db"), e.index)
	os.Remove(e.index + "-wal")
	os.Remove(e.index + "-shm")

	s2, rep := e.open(t)
	if !rep.Behind || rep.Rebuilt || len(rep.Changed) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	vs, _ := s2.Versions("n1")
	if len(vs) != 3 || vs[2].Hash != d3.Version.Hash || vs[2].Author != "user" {
		t.Fatalf("versions: %+v", vs)
	}
	if last := mustRead(t, s2, "n1"); last.Version.N != 3 || last.Version.Hash != d3.Version.Hash {
		t.Fatalf("last: %+v", last.Version)
	}
	if _, err := write(s2, "n1", "On one.", d.Version.Hash, "brain"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a write on v1: %v", err)
	}
	if sum, err := s2.Get("n2"); err != nil || sum.Version != 1 {
		t.Fatalf("the node born since: %+v %v", sum, err)
	}
	d4, err := write(s2, "n1", "Four.", d3.Version.Hash, "brain")
	if err != nil || d4.Version.N != 4 {
		t.Fatalf("the next write: %v %+v", err, d4)
	}
}

// A stop between a commit and its indexing: the next start indexes it.
func TestStopBetweenCommitAndIndex(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "One."})
	n, _ := nodefile.Parse(d.Raw)
	n.AuthorBody("Two.")
	raw, _ := n.Bytes()
	hash, err := s.commit("write n1: Plan", "brain", time.Now(), []change{{path: d.File, data: raw}}, []stamp{{"n1", 2}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, rep := e.open(t)
	if !rep.Behind || len(rep.Changed) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if last := mustRead(t, s2, "n1"); last.Version.N != 2 || last.Version.Hash != hash || last.Node.Text() != "Two." {
		t.Fatalf("last: %+v %q", last.Version, last.Node.Text())
	}
}

// A node's file broken by hand is reported and keeps its row, to mend.
func TestNodeFileBrokenByHand(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Plan", Body: "One."})
	mustCreate(t, s, New{Title: "Kid", Parent: "n1", Body: "k"})
	s.Close()
	os.WriteFile(filepath.Join(e.dir, d.File), []byte("---\nid: n1\n"), 0o600)
	s2, rep := e.open(t)
	if len(rep.Broken) != 1 || len(rep.Gone) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if sum, err := s2.Get("n1"); err != nil || sum.Title != "Plan" {
		t.Fatalf("the broken node's row: %+v %v", sum, err)
	}
	if _, err := s2.Read("n1", 0); err == nil {
		t.Fatal("a broken file reads")
	}
	if old, err := s2.Read("n1", 1); err != nil || old.Node.Text() != "One." {
		t.Fatalf("its last good version: %v", err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

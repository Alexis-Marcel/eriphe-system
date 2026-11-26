package nodestore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

const pea = "Mes placements.\n\n- PEA : [18 385 €](on:monitor.changed/pea)\n"

// hearPEA writes the PEA's value as an event would.
func hearPEA(t *testing.T, s *Store, id string, v float64) *Doc {
	t.Helper()
	d, err := s.WriteValues(id, func(n *nodefile.Node) error {
		_, err := n.Hear(nodefile.Heard{Kind: "monitor.changed", Key: "pea", Payload: map[string]any{"value": v}, Stamp: "relu le 26/11 à 10 h 05"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func bodyText(t *testing.T, d *Doc) string {
	t.Helper()
	b, err := d.Node.Body()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWriteValuesMakesNoVersion(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Placements", Body: pea})
	var told []Change
	s.Watch(func(c Change) { told = append(told, c) })
	if ids, err := s.Listening("monitor.changed", "pea"); err != nil || !slices.Equal(ids, []string{d.Node.Meta.ID}) {
		t.Fatalf("Listening = %v, %v", ids, err)
	}
	if ids, _ := s.Listening("monitor.changed", "other"); len(ids) != 0 {
		t.Errorf("another key heard: %v", ids)
	}
	head, _ := s.headHash()
	got := hearPEA(t, s, d.Node.Meta.ID, 18412)
	if got.Version.N != 1 || got.Version.Hash != d.Version.Hash || got.Last != 1 {
		t.Errorf("version %+v, last %d: a value makes none", got.Version, got.Last)
	}
	if h, _ := s.headHash(); h != head {
		t.Error("a value made a commit")
	}
	if !slices.Contains(told, Change{ID: d.Node.Meta.ID, What: ChangedDoc}) {
		t.Errorf("watcher told %v", told)
	}
	last, _ := s.Read(d.Node.Meta.ID, 0)
	if !strings.Contains(bodyText(t, last), "[18 412 €](on:monitor.changed/pea) · relu le 26/11") || last.Version.N != 1 {
		t.Errorf("read: v%d\n%s", last.Version.N, bodyText(t, last))
	}
	if v1, _ := s.Read(d.Node.Meta.ID, 1); !strings.Contains(bodyText(t, v1), "[18 385 €]") {
		t.Errorf("version 1 changed:\n%s", bodyText(t, v1))
	}
	// The search finds the file as it is.
	if found, _ := s.Find("18 412", false, 5); len(found) != 1 {
		t.Errorf("Find = %v", found)
	}
	// Another node's version does not take the pending file.
	mustCreate(t, s, New{Title: "Autre", Body: "Rien.\n"})
	h, _ := s.headHash()
	raw, err := s.fileAt(h, d.File)
	if err != nil || !strings.Contains(string(raw), "[18 385 €]") {
		t.Errorf("another node's commit holds the value: %s %v", raw, err)
	}
	// The same value again writes nothing.
	told = nil
	hearPEA(t, s, d.Node.Meta.ID, 18412.2)
	if len(told) != 0 {
		t.Errorf("an unchanged value told %v", told)
	}
}

func TestNextVersionHoldsTheValues(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Placements", Body: pea})
	id := d.Node.Meta.ID
	hearPEA(t, s, id, 18412)
	// The author read version 1 before the value: their page has the old
	// value, the value code wrote since stays.
	w, err := write(s, id, pea+"- Livret : 1 200 €\n", d.Version.Hash, "brain")
	if err != nil {
		t.Fatal(err)
	}
	b := bodyText(t, w)
	if w.Version.N != 2 || !strings.Contains(b, "[18 412 €](on:monitor.changed/pea) · relu le") || !strings.Contains(b, "Livret") {
		t.Errorf("v%d:\n%s", w.Version.N, b)
	}
	if s.isPending(id) {
		t.Error("still pending after a version")
	}
	if raw, _ := s.fileAt(w.Version.Hash, w.File); !strings.Contains(string(raw), "18 412 €") {
		t.Error("the version does not hold the value")
	}
	// A value the author writes by hand is theirs.
	hearPEA(t, s, id, 18500)
	w, err = write(s, id, strings.Replace(pea, "18 385", "20 000", 1), w.Version.Hash, "user")
	if err != nil {
		t.Fatal(err)
	}
	if b := bodyText(t, w); !strings.Contains(b, "[20 000 €]") {
		t.Errorf("the author's value lost:\n%s", b)
	}
}

func TestCommitReadings(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Placements", Body: pea})
	b := mustCreate(t, s, New{Title: "Autres", Body: pea})
	c := mustCreate(t, s, New{Title: "Calme", Body: pea})
	if ids, err := s.CommitReadings(); err != nil || ids != nil {
		t.Fatalf("nothing pending: %v, %v", ids, err)
	}
	hearPEA(t, s, a.Node.Meta.ID, 1)
	hearPEA(t, s, b.Node.Meta.ID, 2)
	ids, err := s.CommitReadings()
	if err != nil || !slices.Equal(ids, []string{a.Node.Meta.ID, b.Node.Meta.ID}) {
		t.Fatalf("CommitReadings = %v, %v", ids, err)
	}
	hashes := map[string]bool{}
	for _, id := range ids {
		vs, _ := s.Versions(id)
		if len(vs) != 2 || vs[1].Author != Readings {
			t.Fatalf("%s versions %+v", id, vs)
		}
		hashes[vs[1].Hash] = true
	}
	if len(hashes) != 1 {
		t.Errorf("one commit for all: %v", hashes)
	}
	if vs, _ := s.Versions(c.Node.Meta.ID); len(vs) != 1 {
		t.Errorf("a node without values got a version: %+v", vs)
	}
	if ids, _ := s.CommitReadings(); ids != nil {
		t.Errorf("a second time: %v", ids)
	}
}

// An author's write made on the version read before a readings version
// is not stale: the readings are code's, its values kept; and a second
// readings version waits for the next day.
func TestWriteOverReadings(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Placements", Body: pea})
	id := d.Node.Meta.ID
	hearPEA(t, s, id, 18412)
	if ids, _ := s.CommitReadings(); len(ids) != 1 {
		t.Fatalf("CommitReadings = %v", ids)
	}
	w, err := write(s, id, pea+"Une ligne.\n", d.Version.Hash, "brain")
	if err != nil {
		t.Fatalf("a write made before the readings: %v", err)
	}
	if b := bodyText(t, w); w.Version.N != 3 || !strings.Contains(b, "[18 412 €]") || !strings.Contains(b, "Une ligne.") {
		t.Errorf("v%d:\n%s", w.Version.N, b)
	}
	hearPEA(t, s, id, 18500)
	if ids, _ := s.CommitReadings(); ids != nil {
		t.Errorf("a second readings version the same day: %v", ids)
	}
	// An author's own change made on the old version is still stale.
	if _, err := write(s, id, "Autre chose.\n", d.Version.Hash, "brain"); !errors.Is(err, ErrConflict) {
		t.Errorf("a stale write: %v", err)
	}
}

func TestRestartKeepsValuesPending(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Placements", Body: pea})
	id := d.Node.Meta.ID
	hearPEA(t, s, id, 18412)
	s.Close()
	s, rep := e.open(t)
	if len(rep.Changed) != 0 || rep.Behind {
		t.Errorf("report %+v: a value is no change made outside", rep)
	}
	if vs, _ := s.Versions(id); len(vs) != 1 {
		t.Errorf("versions %+v", vs)
	}
	if !s.isPending(id) {
		t.Error("not pending any more")
	}
	if ids, _ := s.CommitReadings(); len(ids) != 1 {
		t.Errorf("CommitReadings = %v", ids)
	}
	// A file changed by hand over the values is changed outside.
	hearPEA(t, s, id, 18500)
	s.Close()
	path := filepath.Join(e.dir, d.File)
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, append(raw, []byte("Ajouté à la main.\n")...), 0o600)
	s, rep = e.open(t)
	if !slices.Equal(rep.Changed, []string{id}) || s.isPending(id) {
		t.Errorf("report %+v, pending %v", rep, s.isPending(id))
	}
}

func TestListeningLeavesArchived(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Tout", Body: "[0](on:task.ended#state)\n"})
	b := mustCreate(t, s, New{Title: "Archivé", Body: "[0](on:task.ended/t1)\n"})
	if ids, _ := s.Listening("task.ended", "t1"); len(ids) != 2 {
		t.Errorf("Listening = %v", ids)
	}
	if err := s.Archive(b.Node.Meta.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.Listening("task.ended", "t1"); !slices.Equal(ids, []string{a.Node.Meta.ID}) {
		t.Errorf("Listening = %v", ids)
	}
	if _, err := s.WriteValues(b.Node.Meta.ID, func(*nodefile.Node) error { return nil }); err == nil {
		t.Error("an archived node written")
	}
}

func looseObjects(t *testing.T, s *Store) int {
	t.Helper()
	n := 0
	s.repo.Storer.(storer.LooseObjectStorer).ForEachObjectHash(func(plumbing.Hash) error { n++; return nil })
	return n
}

func TestPackKeepsTheHistory(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Placements", Body: pea})
	last := d
	for i := range 5 {
		var err error
		if last, err = write(s, d.Node.Meta.ID, pea+strings.Repeat("- ligne\n", i+1), last.Version.Hash, "brain"); err != nil {
			t.Fatal(err)
		}
	}
	if packed, err := s.Pack(); err != nil || packed {
		t.Fatalf("Pack under PackLoose = %v, %v", packed, err)
	}
	before := looseObjects(t, s)
	if err := s.pack(); err != nil {
		t.Fatal(err)
	}
	if after := looseObjects(t, s); after >= before || after != 0 {
		t.Errorf("loose objects %d, then %d", before, after)
	}
	packs, _ := filepath.Glob(filepath.Join(e.dir, ".git", "objects", "pack", "*.pack"))
	if len(packs) != 1 {
		t.Errorf("packs %v", packs)
	}
	// Every version reads, a new one is written, and a start checks it.
	for n := 1; n <= 6; n++ {
		if _, err := s.Read(d.Node.Meta.ID, n); err != nil {
			t.Errorf("v%d: %v", n, err)
		}
	}
	if _, err := write(s, d.Node.Meta.ID, pea, last.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	if err := s.pack(); err != nil { // a second time, over the first pack
		t.Fatal(err)
	}
	if again, _ := filepath.Glob(filepath.Join(e.dir, ".git", "objects", "pack", "*.pack")); len(again) != 1 || again[0] == packs[0] {
		t.Errorf("packs after a second time: %v (before %v)", again, packs)
	}
	// git itself finds the repository whole, when it is installed.
	if _, err := exec.LookPath("git"); err == nil {
		if out, err := exec.Command("git", "-C", e.dir, "fsck", "--full", "--no-dangling").CombinedOutput(); err != nil {
			t.Errorf("git fsck: %v\n%s", err, out)
		}
	}
	s.Close()
	s, _ = e.open(t)
	if vs, _ := s.Versions(d.Node.Meta.ID); len(vs) != 7 {
		t.Errorf("versions %d", len(vs))
	}
	if v, err := s.Read(d.Node.Meta.ID, 1); err != nil || !strings.Contains(bodyText(t, v), "18 385") {
		t.Errorf("v1: %v", err)
	}
}

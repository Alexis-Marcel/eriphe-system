package nodestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// greyToGray is a fake format after today's: the word grey is written gray.
func greyToGray(raw []byte) ([]byte, error) {
	return bytes.ReplaceAll(raw, []byte("grey"), []byte("gray")), nil
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	return h.Hash().String()
}

// Nodes written in today's format come back whole after a change of the
// format: converted at start in one commit, each with a version of its
// own, their old versions kept and read converted.
func TestFormatConversionKeepsEveryNode(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Paint", Body: "The wall is grey."})
	if _, err := write(s, a.Node.Meta.ID, "The wall is grey, the door too.", a.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	b := mustCreate(t, s, New{Title: "Sky", Body: "A grey sky." + statusBlock})
	c := mustCreate(t, s, New{Title: "Plain", Body: "Nothing to change."})
	if err := s.Archive(c.Node.Meta.ID, "user"); err != nil {
		t.Fatal(err)
	}
	ids := []string{a.Node.Meta.ID, b.Node.Meta.ID, c.Node.Meta.ID}
	before := map[string][]Version{}
	for _, id := range ids {
		vs, err := s.Versions(id)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = vs
	}
	s.Close()

	e.opts.conversions = append(nodefile.Conversions(), greyToGray)
	s2, rep := e.open(t)
	if !slices.Equal(rep.Converted, ids) || len(rep.Changed) != 0 || len(rep.Broken) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	var hash string
	for _, id := range ids {
		d := mustRead(t, s2, id)
		was := before[id]
		if d.Version.N != len(was)+1 || d.Version.Author != Convert || d.Node.Meta.Format != 4 {
			t.Fatalf("%s: v%d by %s, format %d", id, d.Version.N, d.Version.Author, d.Node.Meta.Format)
		}
		if !strings.HasPrefix(string(d.Raw), "---\nformat: 4\n") || strings.Contains(string(d.Raw), "grey") {
			t.Fatalf("%s converted:\n%s", id, d.Raw)
		}
		if hash == "" {
			hash = d.Version.Hash
		} else if d.Version.Hash != hash {
			t.Fatal("the conversion is more than one commit")
		}
		// Its history is whole: each old version as it was in git, and
		// read in today's format.
		vs, _ := s2.Versions(id)
		if len(vs) != len(was)+1 {
			t.Fatalf("%s: versions %v", id, vs)
		}
		for i, v := range was {
			if vs[i].Hash != v.Hash || vs[i].Author != v.Author {
				t.Fatalf("%s: version %d was %+v, is %+v", id, v.N, v, vs[i])
			}
			old, err := s2.fileAt(v.Hash, d.File)
			if err != nil || bytes.Contains(old, []byte("format: 4")) {
				t.Fatalf("%s v%d in git: %v\n%s", id, v.N, err, old)
			}
			r, err := s2.Read(id, v.N)
			if err != nil || r.Node.Meta.Format != 4 || strings.Contains(r.Node.Text(), "grey") {
				t.Fatalf("%s v%d read: %v %+v", id, v.N, err, r)
			}
		}
	}
	if mustRead(t, s2, ids[2]).Node.Meta.Archived.IsZero() {
		t.Fatal("the archived node lost its archive")
	}
	// Written again, on the version read before the conversion (only
	// code wrote since), and made after: in the new format.
	pre := before[ids[0]][len(before[ids[0]])-1].Hash
	if w, err := write(s2, ids[0], "The wall is gray.", pre, "brain"); err != nil || w.Node.Meta.Format != 4 {
		t.Fatalf("a write on the version before the conversion: %v", err)
	}
	n := mustCreate(t, s2, New{Title: "New", Body: "x"})
	if !strings.HasPrefix(string(n.Raw), "---\nformat: 4\n") {
		t.Fatalf("a new node:\n%s", n.Raw)
	}
	s2.Close()

	// Started again, and with its index made again from the files:
	// nothing more to convert, every node there.
	_, rep = e.open(t)
	if len(rep.Converted) != 0 || len(rep.Changed) != 0 {
		t.Fatalf("second start: %+v", rep)
	}
	if err := os.Remove(e.index); err != nil {
		t.Fatal(err)
	}
	s3, rep := e.open(t)
	if !rep.Rebuilt || len(rep.Converted) != 0 {
		t.Fatalf("rebuilt: %+v", rep)
	}
	for _, id := range append(ids, n.Node.Meta.ID) {
		if d := mustRead(t, s3, id); d.Node.Meta.Format != 4 {
			t.Fatalf("%s: format %d", id, d.Node.Meta.Format)
		}
	}
	if d := mustRead(t, s3, ids[0]); d.Version.N != len(before[ids[0]])+2 {
		t.Fatalf("versions after the rebuild: v%d", d.Version.N)
	}
}

// A file of a newer format stops the store, and is left as it was.
func TestNewerFormatRefused(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Future", Body: "x"})
	s.Close()
	path := filepath.Join(e.dir, d.File)
	future := []byte(strings.Replace(string(d.Raw), "format: 3\n", "format: 9\n", 1))
	if err := os.WriteFile(path, future, 0o600); err != nil {
		t.Fatal(err)
	}
	head := headOf(t, e.dir)
	_, _, err := Open(e.dir, e.index, e.opts)
	if err == nil || !errors.Is(err, nodefile.ErrNewerFormat) || !strings.Contains(err.Error(), d.File) {
		t.Fatalf("opened: %v", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, future) {
		t.Fatalf("the file was touched:\n%s", got)
	}
	if headOf(t, e.dir) != head {
		t.Fatal("a commit was made")
	}
}

// A file that does not read as a node stays reported and left alone when
// a conversion is there: the other nodes are converted, the store starts.
func TestBrokenFileNotConverted(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	a := mustCreate(t, s, New{Title: "Broken", Body: "grey"})
	b := mustCreate(t, s, New{Title: "Fine", Body: "grey"})
	s.Close()
	path := filepath.Join(e.dir, a.File)
	bad := []byte(strings.Replace(string(a.Raw), "title: Broken\n", "", 1))
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	e.opts.conversions = append(nodefile.Conversions(), greyToGray)
	s2, rep := e.open(t)
	if len(rep.Broken) != 1 || !strings.Contains(rep.Broken[0], a.File) || !slices.Equal(rep.Converted, []string{b.Node.Meta.ID}) {
		t.Fatalf("report: %+v", rep)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, bad) {
		t.Fatal("the broken file was touched")
	}
	if old, err := s2.Read(a.Node.Meta.ID, 1); err != nil || old.Node.Text() != "gray" {
		t.Fatalf("its last good version, read converted: %v %+v", err, old)
	}
}

// A file of an older format changed by hand: its words are committed as
// they were (outside), then converted.
func TestOlderFormatChangedByHand(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Paint", Body: "White."})
	s.Close()
	path := filepath.Join(e.dir, d.File)
	byHand := []byte(strings.Replace(string(d.Raw), "White.", "Now grey.", 1))
	if err := os.WriteFile(path, byHand, 0o600); err != nil {
		t.Fatal(err)
	}
	e.opts.conversions = append(nodefile.Conversions(), greyToGray)
	s2, rep := e.open(t)
	id := d.Node.Meta.ID
	if !slices.Equal(rep.Changed, []string{id}) || !slices.Equal(rep.Converted, []string{id}) {
		t.Fatalf("report: %+v", rep)
	}
	vs, _ := s2.Versions(id)
	if len(vs) != 3 || vs[1].Author != Outside || vs[2].Author != Convert {
		t.Fatalf("versions: %+v", vs)
	}
	if old, _ := s2.fileAt(vs[1].Hash, d.File); !bytes.Equal(old, byHand) {
		t.Fatalf("the words by hand are not in the history:\n%s", old)
	}
	if last := mustRead(t, s2, id); !strings.Contains(last.Node.Text(), "Now gray.") {
		t.Fatalf("converted: %q", last.Node.Text())
	}
}

// A node of format 1 said its lifetime: at start it is converted to
// today's format, the field dropped and every other byte kept; with its
// index kept, or made again (an index at an older schema, as the same
// start finds it).
func TestFormat1LifetimeDropped(t *testing.T) {
	for _, rebuilt := range []bool{false, true} {
		e := newEnv(t)
		s, _ := e.open(t)
		d := mustCreate(t, s, New{Title: "Mails", Goal: "Les mails à traiter.", Body: "Rien de neuf."})
		s.Close()
		old := strings.Replace(string(d.Raw), "format: 3\n", "", 1)
		old = strings.Replace(old, "goal:", "lifetime: standing\ngoal:", 1)
		path := filepath.Join(e.dir, d.File)
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		if rebuilt {
			if err := os.Remove(e.index); err != nil {
				t.Fatal(err)
			}
		}
		s2, rep := e.open(t)
		id := d.Node.Meta.ID
		if !slices.Equal(rep.Converted, []string{id}) || len(rep.Broken) != 0 || rep.Rebuilt != rebuilt {
			t.Fatalf("rebuilt %v: report: %+v", rebuilt, rep)
		}
		if got := mustRead(t, s2, id); !bytes.Equal(got.Raw, d.Raw) || got.Node.Meta.Format != 3 {
			t.Fatalf("rebuilt %v: converted:\n%s\nwant:\n%s", rebuilt, got.Raw, d.Raw)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, d.Raw) {
			t.Fatalf("rebuilt %v: its file:\n%s", rebuilt, got)
		}
		s2.Close()
	}
}

// A format-2 node whose block read a source: converted at start, its
// block written as Markdown with what code had read for it (the store's
// Snapshots); its older version, read from the history, converted
// without (no value is guessed for the past).
func TestFormat2BlockSnapshotAtStart(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	d := mustCreate(t, s, New{Title: "Mails", Body: "Les mails.\n"})
	s.Close()
	block := "```eriphe:block mails\n{\"components\": [{\"id\":\"root\",\"component\":\"List\",\"children\":{\"componentId\":\"r\",\"path\":\"/m/messages\"}},\n" +
		"{\"id\":\"r\",\"component\":\"Item\",\"title\":{\"@path\":\"subject\"}}],\n" +
		" \"sources\": {\"m\": {\"tool\":\"mail/search_messages\",\"items\":{\"path\":\"messages\",\"id\":\"id\"}}}}\n```\n"
	old := strings.Replace(string(d.Raw), "format: 3\n", "format: 2\n", 1) + "\n" + block
	path := filepath.Join(e.dir, d.File)
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	asked := map[string]int{}
	e.opts.Snapshots = func(node, block string) *nodefile.Snapshot {
		mu.Lock()
		asked[node+"/"+block]++
		mu.Unlock()
		return &nodefile.Snapshot{Read: time.Date(2026, 11, 26, 8, 0, 0, 0, time.Local),
			Values: map[string]json.RawMessage{"m": json.RawMessage(`{"messages":[{"id":"aWQtMQ","subject":"Devis"}]}`)},
			Notes:  map[string][]nodefile.ItemNote{"m": {{Item: "aWQtMQ", Note: "à lire", State: "act"}}}}
	}
	s2, rep := e.open(t)
	id := d.Node.Meta.ID
	if !slices.Equal(rep.Changed, []string{id}) || !slices.Equal(rep.Converted, []string{id}) || asked[id+"/mails"] == 0 {
		t.Fatalf("report: %+v, asked %v", rep, asked)
	}
	got := mustRead(t, s2, id)
	text := got.Node.Text()
	if got.Node.Meta.Format != 3 || len(got.Node.Blocks()) != 0 || got.Version.Author != Convert ||
		!strings.Contains(text, "- [ ] [Devis][m1] : à lire") || !strings.Contains(text, "[m1]: mail:aWQtMQ") || !strings.Contains(text, "Écrit le 26/11/2026 à 8 h 00.") {
		t.Fatalf("converted v%d by %s:\n%s", got.Version.N, got.Version.Author, got.Raw)
	}
	// The version by hand, read from the history: converted without the
	// values.
	vs, _ := s2.Versions(id)
	past, err := s2.Read(id, vs[len(vs)-2].N)
	if err != nil || strings.Contains(past.Node.Text(), "Devis") || len(past.Node.Blocks()) != 0 {
		t.Fatalf("its older version: %v\n%s", err, past.Raw)
	}
}

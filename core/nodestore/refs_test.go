package nodestore

import (
	"os"
	"slices"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

func linksOf(t *testing.T, s *Store, id string) []string {
	t.Helper()
	sum, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	ls := slices.Clone(sum.Links)
	slices.Sort(ls)
	return ls
}

func rename(t *testing.T, s *Store, id, title string) {
	t.Helper()
	d := mustRead(t, s, id)
	if _, err := s.Change(id, d.Version.Hash, "brain", func(n *nodefile.Node) error {
		n.Meta.Title = title
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A node's text links are its links: by title, whatever its case and
// accents, never in code, never to itself; one written before its target
// exists links once it does, and says so to whoever watches.
func TestTextLinks(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	var changed []Change
	s.Watch(func(c Change) { changed = append(changed, c) })
	mustCreate(t, s, New{Title: "La recherche"})                                                                                                      // n1
	mustCreate(t, s, New{Title: "Écrire l'article", Body: "Voir [[la RECHERCHE]], `[[Code]]`, [[Écrire l'article]] et [[Choisir l’angle|l'angle]]."}) // n2
	if got := linksOf(t, s, "n2"); !slices.Equal(got, []string{"n1"}) {
		t.Fatalf("links %v", got)
	}
	mustCreate(t, s, New{Title: "Code"}) // n3: named only inside a code span
	changed = nil
	mustCreate(t, s, New{Title: "Choisir l'angle", Parent: "n2"}) // n4
	if got := linksOf(t, s, "n2"); !slices.Equal(got, []string{"n1", "n4"}) {
		t.Fatalf("links once the target exists %v", got)
	}
	if !slices.Contains(changed, Change{ID: "n2", What: ChangedDoc}) {
		t.Fatalf("n2's new link not told: %+v", changed)
	}
	// The front matter's links, code's, are links too.
	mustCreate(t, s, New{Title: "Vue", Links: []string{"n2"}, Body: "Pour [[Écrire l'article]]."}) // n5
	if got := linksOf(t, s, "n5"); !slices.Equal(got, []string{"n2"}) {
		t.Fatalf("front matter and text: %v", got)
	}
	// Removed from the text, the link goes.
	d := mustRead(t, s, "n2")
	if _, err := write(s, "n2", "Plus que [[La recherche]].", d.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	if got := linksOf(t, s, "n2"); !slices.Equal(got, []string{"n1"}) {
		t.Fatalf("after the write %v", got)
	}
}

// A link keeps its node when that node's title changes, written again or
// not; a new node that takes the old title does not take the link.
func TestTextLinkKeptOnRename(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Plan"})                          // n1
	mustCreate(t, s, New{Title: "Sujet", Body: "Voir [[Plan]]."}) // n2
	rename(t, s, "n1", "Plan du premier article")
	mustCreate(t, s, New{Title: "Plan"}) // n3
	if got := linksOf(t, s, "n2"); !slices.Equal(got, []string{"n1"}) {
		t.Fatalf("after the rename %v", got)
	}
	d := mustRead(t, s, "n2")
	if _, err := write(s, "n2", "Voir [[Plan]], encore.", d.Version.Hash, "brain"); err != nil {
		t.Fatal(err)
	}
	if got := linksOf(t, s, "n2"); !slices.Equal(got, []string{"n1"}) {
		t.Fatalf("written again %v", got)
	}
	got, err := s.Resolve("n2", []string{"Plan", "plan du premier ARTICLE", "Nulle part"})
	if err != nil || got["Plan"].ID != "n1" || got["plan du premier ARTICLE"].ID != "n1" || len(got) != 2 {
		t.Fatalf("resolve %+v, %v", got, err)
	}
}

// A name that two nodes have opens the one not archived, else the oldest;
// an archived node is linked when it is the only one.
func TestTextLinkResolution(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Garage"}) // n1
	if err := s.Archive("n1", "user"); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, New{Title: "Ancien", Body: "[[Garage]]"}) // n2
	if got := linksOf(t, s, "n2"); !slices.Equal(got, []string{"n1"}) {
		t.Fatalf("an archived node %v", got)
	}
	mustCreate(t, s, New{Title: "Garage"})                   // n3
	mustCreate(t, s, New{Title: "Garage"})                   // n4
	mustCreate(t, s, New{Title: "Neuf", Body: "[[garage]]"}) // n5
	if got := linksOf(t, s, "n5"); !slices.Equal(got, []string{"n3"}) {
		t.Fatalf("two of that title %v", got)
	}
}

// An index rebuilt from the files resolves the links by the titles of the
// day, whatever the order the files are read in.
func TestTextLinksRebuilt(t *testing.T) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Sujet", Body: "[[Zèbre]] et [[Abeille]]"}) // n1: read before its targets
	mustCreate(t, s, New{Title: "Zebre"})                                   // n2
	mustCreate(t, s, New{Title: "Abeille"})                                 // n3
	s.Close()
	for _, f := range []string{e.index, e.index + "-wal", e.index + "-shm"} {
		os.Remove(f)
	}
	s2, _ := e.open(t)
	if got := linksOf(t, s2, "n1"); !slices.Equal(got, []string{"n2", "n3"}) {
		t.Fatalf("rebuilt %v", got)
	}
}

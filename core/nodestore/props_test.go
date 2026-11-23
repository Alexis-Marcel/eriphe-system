package nodestore

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
)

// setProps sets properties of a node as the brain's node_set does.
func setProps(t *testing.T, s *Store, id string, props map[string]any) {
	t.Helper()
	d := mustRead(t, s, id)
	if _, err := s.Change(id, d.Version.Hash, "brain", func(n *nodefile.Node) error {
		for k, v := range props {
			if err := n.Meta.SetProp(k, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// recipes is a graph of four nodes with tags and properties: n1 to n4.
func recipes(t *testing.T) (*testEnv, *Store) {
	e := newEnv(t)
	s, _ := e.open(t)
	mustCreate(t, s, New{Title: "Tarte aux pommes", Body: "Des pommes et du sucre."})
	mustCreate(t, s, New{Title: "Soupe", Body: "Des légumes."})
	mustCreate(t, s, New{Title: "Gratin", Body: "Des pommes de terre."})
	mustCreate(t, s, New{Title: "Étude sur le sommeil", Body: "Lire."})
	setProps(t, s, "n1", map[string]any{"tags": []any{"cuisine/recette/dessert"}, "temps": 45.0, "fait": "2026-09-12", "chef": "[[Sam]]"})
	setProps(t, s, "n2", map[string]any{"tags": []any{"cuisine/recette"}, "temps": 15.0, "fait": "2026-10-20", "saison": []any{"hiver", "automne"}})
	setProps(t, s, "n3", map[string]any{"tags": []any{"Cuisine/Recette"}, "temps": 9.0})
	setProps(t, s, "n4", map[string]any{"tags": []any{"etude", "cuisine-pro"}, "statut": "en attente"})
	return e, s
}

func ids(xs []Summary) string {
	var out []string
	for _, x := range xs {
		out = append(out, x.ID)
	}
	return strings.Join(out, " ")
}

func TestSelectByProps(t *testing.T) {
	_, s := recipes(t)
	for _, tc := range []struct {
		name string
		q    Query
		want string
	}{
		{"a tag finds its children", Query{Where: []Cond{{Key: "tags", Value: "cuisine"}}, Sort: "title"}, "n3 n2 n1"},
		{"tags without case", Query{Where: []Cond{{Key: "Tags", Value: "#cuisine/recette"}}, Sort: "temps"}, "n3 n2 n1"},
		{"not a tag's prefix", Query{Where: []Cond{{Key: "tags", Value: "cuisine/rec"}}}, ""},
		{"a leaf tag", Query{Where: []Cond{{Key: "tags", Value: "cuisine/recette/dessert"}}}, "n1"},
		{"not a tag", Query{Where: []Cond{{Key: "tags", Op: "!=", Value: "cuisine/recette/dessert"}}, Sort: "title"}, "n4 n3 n2"},
		{"numbers compare as numbers", Query{Where: []Cond{{Key: "temps", Op: "<", Value: 20.0}}, Sort: "temps"}, "n3 n2"},
		{"<=", Query{Where: []Cond{{Key: "temps", Op: "<=", Value: 15}}, Sort: "temps", Desc: true}, "n2 n3"},
		{"dates compare as dates", Query{Where: []Cond{{Key: "fait", Op: "<", Value: "2026-10-01"}}}, "n1"},
		{"after a date", Query{Where: []Cond{{Key: "fait", Op: ">=", Value: "2026-10-01"}}}, "n2"},
		{"text equal without case", Query{Where: []Cond{{Key: "statut", Value: "En attente"}}}, "n4"},
		{"a list holds a value", Query{Where: []Cond{{Key: "saison", Value: "hiver"}}}, "n2"},
		{"a [[link]]", Query{Where: []Cond{{Key: "chef", Value: "[[Sam]]"}}}, "n1"},
		{"exists", Query{Where: []Cond{{Key: "fait", Op: "exists"}}, Sort: "fait"}, "n1 n2"},
		{"missing", Query{Where: []Cond{{Key: "temps", Op: "missing"}}}, "n4"},
		{"sort, nodes without it last", Query{Sort: "temps", Desc: true}, "n1 n2 n3 n4"},
		{"words and filters", Query{Words: "pommes", Where: []Cond{{Key: "temps", Op: ">", Value: 5}}, Sort: "temps"}, "n3 n1"},
		{"all must hold", Query{Where: []Cond{{Key: "tags", Value: "cuisine"}, {Key: "temps", Op: ">", Value: 10}}, Sort: "temps"}, "n2 n1"},
		{"a limit", Query{Where: []Cond{{Key: "tags", Value: "cuisine"}}, Sort: "temps", Limit: 2}, "n3 n2"},
	} {
		got, err := s.Select(tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ids(got) != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, ids(got), tc.want)
		}
	}
	if _, err := s.Select(Query{Where: []Cond{{Key: "temps", Op: "~", Value: 1}}}); err == nil {
		t.Error("an unknown op is not refused")
	}
	if _, err := s.Select(Query{Where: []Cond{{Key: "temps", Op: "<"}}}); err == nil {
		t.Error("a comparison without a value is not refused")
	}
	if err := s.Archive("n2", "user"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Select(Query{Where: []Cond{{Key: "tags", Value: "cuisine"}}, Sort: "temps"}); ids(got) != "n3 n1" {
		t.Errorf("archived: %s", ids(got))
	}
	if got, _ := s.Select(Query{Where: []Cond{{Key: "tags", Value: "cuisine"}}, Sort: "temps", Archived: true}); ids(got) != "n3 n2 n1" {
		t.Errorf("archived too: %s", ids(got))
	}
}

func TestPropsColumns(t *testing.T) {
	_, s := recipes(t)
	got, err := s.Props([]string{"n1", "n2", "n4"}, []string{"Temps", "tags", "saison"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		"n1": {"temps": 45.0, "tags": []any{"cuisine/recette/dessert"}},
		"n2": {"temps": 15.0, "tags": []any{"cuisine/recette"}, "saison": []any{"hiver", "automne"}},
		"n4": {"tags": []any{"etude", "cuisine-pro"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns %#v", got)
	}
}

// The index made again from the files holds the same rows; a property
// changed by hand is indexed.
func TestPropsRebuiltFromFiles(t *testing.T) {
	e, s := recipes(t)
	rows := func(s *Store) string {
		r, err := s.db.Query(`SELECT id, key, pos, kind, value, fold, ifnull(num, '') FROM node_props ORDER BY id, key, pos`)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		var out []string
		for r.Next() {
			var id, key, kind, value, fold, num string
			var pos int
			r.Scan(&id, &key, &pos, &kind, &value, &fold, &num)
			out = append(out, strings.Join([]string{id, key, kind, value, fold, num}, "|"))
		}
		return strings.Join(out, "\n")
	}
	before := rows(s)
	if !strings.Contains(before, "n1|temps|number|45|45|45") || !strings.Contains(before, "n3|tags|text|Cuisine/Recette|cuisine/recette|") {
		t.Fatalf("rows:\n%s", before)
	}
	s.Close()
	os.Remove(e.index)
	os.Remove(e.index + "-wal")
	os.Remove(e.index + "-shm")
	s2, rep := e.open(t)
	if !rep.Rebuilt {
		t.Fatalf("report %+v", rep)
	}
	if after := rows(s2); after != before {
		t.Fatalf("rebuilt:\n%s\nwas:\n%s", after, before)
	}
	d := mustRead(t, s2, "n3")
	s2.Close()
	edited := strings.Replace(string(d.Raw), "temps: 9\n", "temps: 30\n", 1)
	if edited == string(d.Raw) {
		t.Fatalf("file:\n%s", d.Raw)
	}
	os.WriteFile(e.dir+"/"+d.File, []byte(edited), 0o600)
	s3, _ := e.open(t)
	if got, _ := s3.Select(Query{Where: []Cond{{Key: "temps", Op: ">", Value: 20}}, Sort: "temps"}); ids(got) != "n3 n1" {
		t.Fatalf("changed by hand: %s", ids(got))
	}
}

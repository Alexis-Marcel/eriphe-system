package nodefile

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"
)

const recipeBlock = "---\ntags: [cuisine/recette]\nnote: 9\ncout: 7\ntype: plaisir\n---\n\n"

const recipeText = "Écrit le 27/11/2026.\n\nUne tarte aux pommes.\n"

func recipeNode(t *testing.T) *Node {
	t.Helper()
	n, err := New(Meta{ID: "n1", Title: "Tarte", Created: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)}, "")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// propsOf are a node's properties as key=value, in their order.
func propsOf(m Meta) []string {
	var out []string
	for _, p := range m.Props() {
		var vs []string
		for _, v := range p.Values {
			vs = append(vs, v.Text)
		}
		out = append(out, p.Key+"="+strings.Join(vs, ","))
	}
	return out
}

// A page written with its own front matter at its top: its fields become
// the node's properties, as node_set sets them, and the block leaves the
// page: the file has one header.
func TestFrontBlockBecomesProperties(t *testing.T) {
	n := recipeNode(t)
	if err := n.Meta.SetProp("type", "repas"); err != nil {
		t.Fatal(err)
	}
	if err := n.Meta.SetProp("saison", "automne"); err != nil {
		t.Fatal(err)
	}
	if err := n.AuthorBody("\n" + recipeBlock + recipeText); err != nil {
		t.Fatal(err)
	}
	if n.Text() != recipeText {
		t.Fatalf("body %q", n.Text())
	}
	want := []string{"type=plaisir", "saison=automne", "tags=cuisine/recette", "note=9", "cout=7"}
	if got := propsOf(n.Meta); !slices.Equal(got, want) {
		t.Fatalf("props %v", got)
	}
	if p := n.Meta.Props()[3]; p.Values[0].Kind != KindNumber {
		t.Fatalf("note: %+v", p)
	}
	raw, err := n.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte("---\n")) != 2 {
		t.Fatalf("file:\n%s", raw)
	}
	back, err := Parse(raw)
	if err != nil || !slices.Equal(back.Meta.Tags(), []string{"cuisine/recette"}) {
		t.Fatalf("read back: %v %v", err, back)
	}
	if pr := ReadPage(n.Text()).Check(nil); len(pr) != 0 {
		t.Fatalf("problems %v", pr)
	}
}

// Dates, [[links]] and lists keep their kinds; a field without a value is
// skipped, never a removal.
func TestFrontBlockValues(t *testing.T) {
	n := recipeNode(t)
	if err := n.Meta.SetProp("vide", "gardé"); err != nil {
		t.Fatal(err)
	}
	body := "---\nle: 2026-10-01\nde: [[Grand-mère]]\nvu: [2026-11-01, [[Sam]]]\nvide:\n---\nTexte.\n"
	if err := n.AuthorBody(body); err != nil {
		t.Fatal(err)
	}
	want := []string{"vide=gardé", "le=2026-10-01", "de=[[Grand-mère]]", "vu=2026-11-01,[[Sam]]"}
	if got := propsOf(n.Meta); !slices.Equal(got, want) || n.Text() != "Texte.\n" {
		t.Fatalf("props %v, body %q", got, n.Text())
	}
	if p := n.Meta.Props()[1]; p.Values[0].Kind != KindDate {
		t.Fatalf("le: %+v", p)
	}
}

// A block with a field that is not a property stays in the page whole,
// nothing taken from it, and the page check says so; so does a block
// whose value is a mapping.
func TestFrontBlockNotProperties(t *testing.T) {
	for _, block := range []string{
		"---\ntitle: Autre\ntags: [cuisine]\n---\n",
		"---\ntags: [cuisine]\nid: abc\n---\n",
		"---\n_code: 1\n---\n",
		"---\nnote: {a: 1}\n---\n",
		"---\nvu: [a, {b: 1}]\n---\n",
		"---\ntags: [pas un tag!]\n---\n",
	} {
		n := recipeNode(t)
		body := block + recipeText
		if err := n.AuthorBody(body); err != nil {
			t.Fatal(err)
		}
		if n.Text() != body || len(n.Meta.Extra) != 0 {
			t.Errorf("%q: body %q, props %v", block, n.Text(), propsOf(n.Meta))
		}
		pr := ReadPage(n.Text()).Check(nil)
		if len(pr) == 0 || pr[0].Code != ProblemFrontBlock || pr[0].Line != 1 {
			t.Errorf("%q: problems %v", block, pr)
		}
	}
}

// A --- rule at the top of a page, then text that is no YAML mapping, is
// the page's: left alone, and no problem.
func TestFrontBlockRuleLeftAlone(t *testing.T) {
	for _, body := range []string{
		"---\nUne ligne de texte.\n---\nLa suite.\n",
		"---\n\n## Titre\n",
		"---\n- une liste\n---\n",
		"---\n---\nRien.\n",
		"Texte.\n---\ntags: [x]\n---\n",
		"---\n# Titre\ntags: [x]\n---\n",
		"---\ntags: [x] # mes tags\n# note: 3\n---\n",
	} {
		n := recipeNode(t)
		if err := n.AuthorBody(body); err != nil {
			t.Fatal(err)
		}
		if n.Text() != body || len(n.Meta.Extra) != 0 {
			t.Errorf("%q: body %q, props %v", body, n.Text(), propsOf(n.Meta))
		}
		for _, p := range ReadPage(body).Check(nil) {
			if p.Code == ProblemFrontBlock {
				t.Errorf("%q: %v", body, p)
			}
		}
	}
}

// Lines between two --- rules that read as a YAML mapping are a block,
// as Obsidian reads them at the top of a note.
func TestFrontBlockProseAsMapping(t *testing.T) {
	n := recipeNode(t)
	if err := n.AuthorBody("---\nNote: à revoir\n---\nLa suite.\n"); err != nil {
		t.Fatal(err)
	}
	if got := propsOf(n.Meta); !slices.Equal(got, []string{"Note=à revoir"}) || n.Text() != "La suite.\n" {
		t.Fatalf("props %v, body %q", got, n.Text())
	}
}

// A block the page holds already is not written now: an edit elsewhere in
// the page never sets its fields again over what node_set set since.
func TestFrontBlockAlreadyThere(t *testing.T) {
	n := recipeNode(t)
	if err := n.CodeBody(recipeBlock + recipeText); err != nil {
		t.Fatal(err)
	}
	if err := n.Meta.SetProp("note", 10); err != nil {
		t.Fatal(err)
	}
	if err := n.AuthorBody(recipeBlock + recipeText + "Une ligne de plus.\n"); err != nil {
		t.Fatal(err)
	}
	if got := propsOf(n.Meta); !slices.Equal(got, []string{"note=10"}) || !strings.HasPrefix(n.Text(), recipeBlock) {
		t.Fatalf("props %v, body %q", got, n.Text())
	}
}

// Format 3 → 4: a file whose page starts with a block has it as its
// properties, a property its front matter has already keeping its value
// (node_set set it later); a file without one, or with one that does not
// fold, keeps its bytes but its format.
func TestFormat3FoldsFrontBlock(t *testing.T) {
	head := "---\nformat: 3\nid: n1\ntitle: Tarte\ncreated: 2026-11-27T09:00:00Z\nnote: 8\n---\n"
	out, err := Upgrade([]byte(head+recipeBlock+recipeText), Conversions())
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"note=8", "tags=cuisine/recette", "cout=7", "type=plaisir"}
	if got := propsOf(n.Meta); n.Meta.Format != 4 || !slices.Equal(got, want) || n.Text() != recipeText {
		t.Fatalf("format %d, props %v:\n%s", n.Meta.Format, got, out)
	}
	for _, body := range []string{recipeText, "---\ntitle: Autre\n---\n" + recipeText} {
		out, err := Upgrade([]byte(head+body), Conversions())
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(head, "format: 3", "format: 4", 1) + body; string(out) != want {
			t.Errorf("converted:\n%s\nwant:\n%s", out, want)
		}
	}
}

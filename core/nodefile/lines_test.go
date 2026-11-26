package nodefile

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReplaceOnce(t *testing.T) {
	body := "- [ ] Pain\n- [ ] Lait\n- [ ] Lait d'avoine\n"
	got, err := ReplaceOnce(body, "- [ ] Pain", "- [x] Pain")
	if err != nil || got != "- [x] Pain\n- [ ] Lait\n- [ ] Lait d'avoine\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ReplaceOnce(body, "Beurre", "x"); !errors.Is(err, ErrLineGone) {
		t.Errorf("a line gone: %v", err)
	}
	if _, err := ReplaceOnce(body, "Lait", "x"); !errors.Is(err, ErrLineTwice) {
		t.Errorf("a line twice: %v", err)
	}
}

func TestSetTask(t *testing.T) {
	body := "## Courses\n- [ ] Pain\n- [x] Lait\n\n> [!steps]\n> - [/] Cartons 📅 2026-12-05\n>   - Reste les chambres.\n"
	cases := []struct {
		why   string
		ref   LineRef
		state string
		want  string
		err   error
	}{
		{"by its text", LineRef{Text: "- [ ] Pain"}, TaskDone, strings.Replace(body, "- [ ] Pain", "- [x] Pain", 1), nil},
		{"by a piece of it", LineRef{Text: "Lait"}, TaskOpen, strings.Replace(body, "- [x] Lait", "- [ ] Lait", 1), nil},
		{"in a callout", LineRef{Text: "Cartons"}, TaskDone, strings.Replace(body, "> - [/] Cartons", "> - [x] Cartons", 1), nil},
		{"by its number, still there", LineRef{Line: 2, Text: "- [ ] Pain"}, TaskDropped, strings.Replace(body, "- [ ] Pain", "- [-] Pain", 1), nil},
		{"its number moved: found by its text", LineRef{Line: 5, Text: "- [ ] Pain"}, TaskDoing, strings.Replace(body, "- [ ] Pain", "- [/] Pain", 1), nil},
		{"gone", LineRef{Line: 2, Text: "- [ ] Beurre"}, TaskDone, "", ErrLineGone},
	}
	for _, c := range cases {
		got, err := SetTask(body, c.ref, c.state)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%s: %v", c.why, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s:\n%q\n%v", c.why, got, err)
		}
	}
	if _, err := SetTask(body, LineRef{Text: "## Courses"}, TaskDone); err == nil {
		t.Error("a heading is no task")
	}
	if _, err := SetTask(body, LineRef{Text: "Pain"}, "y"); err == nil {
		t.Error("y is no state")
	}
	if got, _ := SetTask("- [ ] a\r\n- [ ] b\r\n", LineRef{Text: "b"}, TaskDone); got != "- [ ] a\r\n- [x] b\r\n" {
		t.Errorf("CRLF: %q", got)
	}
}

func TestMoveItem(t *testing.T) {
	mails, err := os.ReadFile("testdata/pages/mails.md")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		why         string
		body        string
		item, after string
		want        string
	}{
		{
			why:   "under a heading, below its do: line, with its detail",
			body:  "## À traiter\n- [ ] [Train][m1] : à valider\n  - Référence ABC123.\n- [ ] Autre\n\n## À supprimer\n[Jeter ces mails](do:mail/trash)\n- [Newsletter][m5]\n",
			item:  "[Train][m1]",
			after: "## À supprimer",
			want:  "## À traiter\n- [ ] Autre\n\n## À supprimer\n[Jeter ces mails](do:mail/trash)\n- [ ] [Train][m1] : à valider\n  - Référence ABC123.\n- [Newsletter][m5]\n",
		},
		{
			why:   "after another item and its detail",
			body:  "- A\n  - détail de A\n- B\n- C\n",
			item:  "- C",
			after: "- A",
			want:  "- A\n  - détail de A\n- C\n- B\n",
		},
		{
			why:   "into a callout, at its prefix",
			body:  "> [!steps]\n> - [x] Un\n> - [ ] Trois\n\n- [ ] Deux\n\tà faire vite\n",
			item:  "Deux",
			after: "Un",
			want:  "> [!steps]\n> - [x] Un\n> - [ ] Deux\n> \tà faire vite\n> - [ ] Trois\n",
		},
		{
			why:   "out of a nested list",
			body:  "- A\n  - A1\n    - détail\n- B\n",
			item:  "A1",
			after: "- B",
			want:  "- A\n- B\n- A1\n  - détail\n",
		},
		{
			why:   "under a heading with a blank line before its list",
			body:  "## Un\n- x\n\n## Deux\n\n- y\n",
			item:  "- x",
			after: "## Deux",
			want:  "## Un\n\n## Deux\n\n- x\n- y\n",
		},
	}
	for _, c := range cases {
		got, err := MoveItem(c.body, LineRef{Text: c.item}, LineRef{Text: c.after})
		if err != nil || got != c.want {
			t.Errorf("%s:\n%s\n%v", c.why, got, err)
		}
	}
	// On a fixture page: the moved mail's references and detail still read.
	got, err := MoveItem(string(mails), LineRef{Text: "[Sam · Devis terrasse][m2]"}, LineRef{Text: "## À archiver"})
	if err != nil {
		t.Fatal(err)
	}
	p := ReadPage(got)
	if pr := p.Check(nil); len(pr) > 0 {
		t.Errorf("problems after the move: %v", pr)
	}
	var archive Section
	for _, l := range p.Links {
		if l.Target == "mail/move" {
			archive, _ = p.Scope(l)
		}
	}
	if len(archive.Uses) != 3 || archive.Uses[0].Label != "m2" {
		t.Errorf("À archiver holds %v", archive.Uses)
	}
	if !strings.Contains(got, "[Archiver ces mails](do:mail/move)\n- [ ] [Sam · Devis terrasse][m2] : il attend ta réponse avant vendredi 📅 2026-11-28\n  - Brouillon prêt") ||
		!strings.Contains(got, "  - [Envoyer la réponse](card:calls:412)\n- [Facture internet") {
		t.Errorf("its detail did not go with it:\n%s", got)
	}

	for _, c := range []struct {
		why         string
		item, after string
	}{
		{"an item gone", "Beurre", "## À archiver"},
		{"a heading gone", "[m2]", "## Ailleurs"},
		{"not an item", "## À traiter", "## À archiver"},
		{"into itself", "[m2]", "Brouillon prêt"},
		{"after a paragraph", "[m2]", "[Jeter ces mails](do:mail/trash)"},
	} {
		if _, err := MoveItem(string(mails), LineRef{Text: c.item}, LineRef{Text: c.after}); err == nil {
			t.Errorf("%s: moved", c.why)
		}
	}
}

func TestScope(t *testing.T) {
	src, err := os.ReadFile("testdata/pages/mails.md")
	if err != nil {
		t.Fatal(err)
	}
	src = []byte(strings.Replace(string(src), "- [Newsletter Le Tri][m5]", "- [x] [Newsletter Le Tri][m5]", 1))
	p := ReadPage(string(src))
	var got []Section
	for _, l := range p.Links {
		if s, ok := p.Scope(l); ok {
			got = append(got, s)
		}
	}
	if len(got) != 2 {
		t.Fatalf("%d scopes", len(got))
	}
	a, d := got[0], got[1]
	if a.Heading != 11 || a.End != 15 || len(a.Uses) != 2 || a.Uses[0].Label != "m3" || a.Uses[1].Label != "m4" {
		t.Errorf("À archiver: %+v", a)
	}
	if d.Heading != 16 || len(d.Uses) != 2 || d.Uses[0].Task != TaskDone || d.Uses[1].Task != "" {
		t.Errorf("À supprimer: %+v", d)
	}
	// A deeper heading stays in the section; the next one of its level ends it.
	p = ReadPage("## A\n[Jeter](do:mail/trash)\n- [x][m1]\n### A.1\n- [y][m2]\n## B\n- [z][m3]\n\n[m1]: mail:1\n[m2]: mail:2\n[m3]: mail:3\n")
	s, ok := p.Scope(p.Links[0])
	if !ok || s.End != 5 || len(s.Uses) != 2 {
		t.Errorf("%+v %v", s, ok)
	}
	// A blank line between the heading and its do: is fine.
	p = ReadPage("## A\n\n[Jeter](do:mail/trash)\n- [x][m1]\n\n[m1]: mail:1\n")
	if _, ok := p.Scope(p.Links[0]); !ok {
		t.Error("a blank line under the heading")
	}
}

func TestReadPageBits(t *testing.T) {
	p := ReadPage("Texte `[pas][m1]` et \\[pas][m2] et ![image](do:x) et [[Node]] [lien](https://example.org)\n\n[Montre](ask:) [x](on:monitor.changed/pea#value)\n\n[m3]: https://example.org/a b\n[M4]: pr:example/app#128\n")
	if len(p.Uses) != 0 {
		t.Errorf("uses %v", p.Uses)
	}
	if len(p.Links) != 2 || p.Links[1].Kind != LinkOn || p.Links[1].Target != "monitor.changed/pea#value" || !p.Links[1].Alone {
		t.Errorf("links %+v", p.Links)
	}
	if len(p.Refs) != 2 || p.Refs[0].Scheme != "https" || p.Refs[0].Target != "https://example.org/a" ||
		p.Refs[1].Label != "m4" || p.Refs[1].Scheme != "pr" || p.Refs[1].ID != "example/app#128" {
		t.Errorf("refs %+v", p.Refs)
	}
	// A « Écrit le » line after text is text; CRLF lines read as LF.
	p = ReadPage("Bonjour.\r\nÉcrit le 26/11.\r\n")
	if p.Written != nil || p.Blocks[0].Text != "Bonjour.\nÉcrit le 26/11." {
		t.Errorf("%+v", p)
	}
	// A quote and who said it.
	p = ReadPage("> Le bois est commandé.\n> — Sam, 24/11\n")
	if b := p.Blocks[0]; b.Kind != KindQuote || b.Text != "Le bois est commandé." || b.From != "Sam" || b.When != "24/11" {
		t.Errorf("%+v", b)
	}
	// An empty body.
	if p := ReadPage(""); len(p.Blocks) != 0 || len(p.Check(nil)) != 0 {
		t.Errorf("%+v", p)
	}
}

func TestNumber(t *testing.T) {
	for _, s := range []string{"18 385", "1,8", "−2", "-2", "3.5", "17\u202f950", "0"} {
		if !Number(s) {
			t.Errorf("%q is a number", s)
		}
	}
	for _, s := range []string{"beaucoup", "12 €", "1,2,3", "", "--1"} {
		if Number(s) {
			t.Errorf("%q is no number", s)
		}
	}
}

package nodefile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The conversion's fixtures (testdata/convert): each <name>.v2.md is a
// format-2 file of a shape found on a real install (anonymised), its
// <name>.snap.json what code had read for its blocks (by block: read,
// values, notes), and <name>.md the format-3 file it converts to. go test
// -run TestToPageFixtures -update writes the .md again.

// Someone else's words, written by the conversion, read as words: no
// link, reference, date, heading, quote, cell or item of their own.
func TestConvertedWordsStayWords(t *testing.T) {
	for _, s := range []string{
		"[Oui](do:mail/trash)", "[Envoie](ask:)", "[x](card:calls:1)", "[18](on:monitor.changed/x)",
		"[Devis][m1]", "[[Mes tâches]]", "![[Mes tâches]]", "`code` <b>gras</b>", "a | b | c",
		"# Titre", "- un", "+ deux", "1. trois", "> cité", "📅 2026-11-27", "ligne\nautre", `\[échappé\]`,
	} {
		w := esc(s)
		for _, page := range []string{w + "\n", "- " + w + "\n", "> [!note]\n> " + w + "\n"} {
			p := ReadPage(page)
			if len(p.Links) > 0 || len(p.Uses) > 0 || len(p.Blocks) != 1 || strings.Contains(page, "📅") || strings.Count(page, "\n") != strings.Count(strings.TrimSuffix(page, "\n"), "\n")+1 {
				t.Errorf("%q written %q: %+v", s, page, p)
			}
			if b := p.Blocks[0]; b.Kind == KindList && len(b.Items[0].Detail) > 0 || b.Kind == KindHeading || b.Kind == KindTable {
				t.Errorf("%q written %q reads as %s", s, page, b.Kind)
			}
		}
	}
}

type snapFixture struct {
	Read   time.Time                  `json:"read"`
	Values map[string]json.RawMessage `json:"values"`
	Notes  map[string][]ItemNote      `json:"notes"`
}

func TestToPageFixtures(t *testing.T) {
	time.Local = time.FixedZone("CET", 3600)
	t.Cleanup(func() { time.Local = time.UTC })
	olds, _ := filepath.Glob("testdata/convert/*.v2.md")
	if len(olds) < 8 {
		t.Fatalf("%d fixtures", len(olds))
	}
	for _, old := range olds {
		name := strings.TrimSuffix(filepath.Base(old), ".v2.md")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(old)
			if err != nil {
				t.Fatal(err)
			}
			snaps := map[string]snapFixture{}
			if s, err := os.ReadFile(strings.TrimSuffix(old, ".v2.md") + ".snap.json"); err == nil {
				if err := json.Unmarshal(s, &snaps); err != nil {
					t.Fatal(err)
				}
			}
			id := ""
			snap := func(node, block string) *Snapshot {
				id = node
				s, ok := snaps[block]
				if !ok {
					return nil
				}
				return &Snapshot{Read: s.Read, Values: s.Values, Notes: s.Notes}
			}
			out, err := Upgrade(raw, ConversionsWith(snap))
			if err != nil {
				t.Fatal(err)
			}
			n, err := Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if n.Meta.Format != 3 || id != n.Meta.ID {
				t.Fatalf("format %d, node %q", n.Meta.Format, id)
			}
			if bytes.Contains(out, []byte("eriphe:block")) || len(n.Blocks()) > 0 {
				t.Fatal("a block left")
			}
			body, _ := n.Body()
			p := ReadPage(body)
			if ps := p.Check(func(string) bool { return false }); len(ps) > 0 {
				t.Errorf("problems: %+v", ps)
			}
			// Code has no tool: no do: line; and no words of the data
			// read as a link of Eriphe's.
			for _, l := range p.Links {
				if l.Kind == LinkDo || l.Kind == LinkAsk && strings.Contains(l.Label, "Oui") {
					t.Errorf("line %d: a %s link written: %q", l.Line, l.Kind, l.Label)
				}
			}
			if again, err := Upgrade(out, ConversionsWith(snap)); err != nil || !bytes.Equal(again, out) {
				t.Errorf("converted again: %v", err)
			}
			path := strings.TrimSuffix(old, ".v2.md") + ".md"
			if *update {
				if err := os.WriteFile(path, out, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, want) {
				t.Errorf("converted:\n%s\nwant:\n%s", out, want)
			}
		})
	}
}

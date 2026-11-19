package nodefile

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// The cases the workspace's Markdown reader is held to as well
// (web/espace/src/a2ui/wiki.test.tsx): the links Go indexes are the
// links the page draws.
func TestWikiLinksCases(t *testing.T) {
	var cases []struct {
		Why   string     `json:"why"`
		Text  string     `json:"text"`
		Links []WikiLink `json:"links"`
	}
	if err := json.Unmarshal(read(t, "wikilinks.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := WikiLinks(c.Text)
		if !slices.Equal(got, c.Links) && !(len(got) == 0 && len(c.Links) == 0) {
			t.Errorf("%s: %q gives %+v, want %+v", c.Why, c.Text, got, c.Links)
		}
	}
}

// A node's links are its text's, each name once whatever its case or
// accents, never a block's words.
func TestNodeWikiLinks(t *testing.T) {
	n, err := New(Meta{ID: "abc", Title: "Écrire l'article", Lifetime: Work, Created: time.Unix(1700000000, 0)}, "")
	if err != nil {
		t.Fatal(err)
	}
	body := "Voir [[Choisir l'angle]] et [[choisir L’ANGLE|là]].\n\n```eriphe:block b\n{\"components\": [{\"id\": \"root\", \"component\": \"Text\", \"text\": \"[[Bloc]]\"}]}\n```\n\nPuis [[L'article]].\n"
	if err := n.AuthorBody(body); err != nil {
		t.Fatal(err)
	}
	got := n.WikiLinks()
	want := []WikiLink{{Name: "Choisir l'angle"}, {Name: "L'article"}}
	if !slices.Equal(got, want) {
		t.Fatalf("links %+v, want %+v", got, want)
	}
}

func TestLinkKey(t *testing.T) {
	for in, want := range map[string]string{
		"Choisir l'angle":       "choisir l'angle",
		"  ÉCRIRE   l'Article ": "ecrire l'article",
		"Ma journée":            "ma journee",
	} {
		if got := LinkKey(in); got != want {
			t.Errorf("LinkKey(%q) = %q, want %q", in, got, want)
		}
	}
}

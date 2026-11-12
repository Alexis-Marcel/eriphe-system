package a2ui

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// The prompt is written from the catalog and is the same every time: its
// text is kept in testdata/catalog_prompt.md, which a change to the catalog
// or to the writer shows in review.
// ERIPHE_UPDATE_A2UI=1 go test -run TestPromptStable writes it again.
func TestPromptStable(t *testing.T) {
	got := CatalogPrompt()
	for range 5 {
		if again := Eriphe().Prompt(); again != got {
			t.Fatal("the prompt changes from one call to the next")
		}
	}
	const golden = "testdata/catalog_prompt.md"
	if os.Getenv("ERIPHE_UPDATE_A2UI") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("the prompt differs from %s (ERIPHE_UPDATE_A2UI=1 go test -run TestPromptStable writes it):\n%s", golden, got)
	}
}

// The prompt names every component, each of its properties, and every
// function; it stays compact.
func TestPromptCoversTheCatalog(t *testing.T) {
	c := Eriphe()
	p := c.Prompt()
	comps, _ := c.doc["components"].(map[string]any)
	for _, name := range c.component {
		line := lineOf(p, "- **"+name+"**")
		if line == "" {
			t.Errorf("no line for %s", name)
			continue
		}
		def, _ := comps[name].(map[string]any)
		props, _ := def["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		block := p[strings.Index(p, line):]
		if i := strings.Index(block[1:], "\n- **"); i > 0 {
			block = block[:i+1]
		}
		for _, k := range keys {
			if k == "component" {
				continue
			}
			if !strings.Contains(block, k) {
				t.Errorf("%s: property %s is not in the prompt", name, k)
			}
		}
	}
	for _, name := range c.function {
		if lineOf(p, "- **"+name+"**(") == "" {
			t.Errorf("no line for function %s", name)
		}
	}
	for _, want := range []string{"label* texte", "kind* line|bar", "liaison", "`*` : obligatoire", "Exemple : `{\"id\": \"conso\", \"component\": \"Chart\""} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if n := len(p); n > 16000 {
		t.Errorf("the prompt is %d bytes", n)
	}
}

// Any catalog gets a prompt the same way: the generator knows no component.
func TestPromptOfAnotherCatalog(t *testing.T) {
	raw, err := os.ReadFile("testdata/conformance/basic_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Prompt()
	for _, want := range []string{"- **ChoicePicker**", "options* [{label* texte (The text to display", "- **formatCurrency**("} {
		if !strings.Contains(p, want) {
			t.Errorf("the basic catalog's prompt lacks %q:\n%s", want, p)
		}
	}
}

func lineOf(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

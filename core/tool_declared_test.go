package main

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A tool that declares only its annotations, of a server core knows
// nothing of, reads plainly: its call by its title and its items counted,
// marked as throwing away by its annotations (Delete), done in words of
// no tool; an item it reads opens by what a row finds, the whole under
// Détails (#727).
func TestToolDeclaringOnlyAnnotations(t *testing.T) {
	c, _, _ := viewCore(t)
	_, discard := c.catalogTool("monitor/discard")
	if discard == nil || discard.Meta != nil {
		t.Fatalf("the fake tool: %+v", discard)
	}
	if got := toolCategory(discard); got != catDelete {
		t.Errorf("category %s, want delete: a tool that throws away is marked so by its annotations", got)
	}
	a := accessQuestion{Call: "mcp__monitor__discard []", Input: `{"ids":["a","b"]}`}
	if got := c.callText(a, "fr"); got != "Discard · 2 éléments" {
		t.Errorf("fr: %q", got)
	}
	if got := c.callText(a, "en"); got != "Discard · 2 items" {
		t.Errorf("en: %q", got)
	}
	if got := c.doneText(a.Call, 2, "fr"); got != "Fait pour 2 éléments" {
		t.Errorf("done: %q", got)
	}
	if fs := c.callContext(t.Context(), "mcp__monitor__discard", map[string]any{"ids": []any{"a"}}); fs != nil {
		t.Errorf("a card read for a tool that declares no read: %+v", fs)
	}
}

// An item opened by a read that declares only its annotations: its title
// and sender as its row says them, the rest under Détails, no field
// guessed by its name.
func TestItemOpenOfAnnotationsOnly(t *testing.T) {
	tw, csrf, _, id := openWeb(t, `{"tool": "monitor/memo", "id": "id"}`)
	code, out := gesture(t, tw, csrf, "POST", "/api/nodes/"+id+"/open", itemOpenIn{Block: "inbox", Source: "received", Item: "m1"})
	if code != http.StatusOK || out["title"] != "Facture" || out["from"] != "Sam <sam@example.org>" || out["date"] != nil || out["body"] != nil ||
		!strings.Contains(out["raw"].(string), "Le texte de m1") {
		t.Fatalf("open: %d %v", code, out)
	}
}

// A server Eriphe does not write: what its preset declares stands for its
// tools' _meta (presets_meta.go), patterns under exact names, and a key
// the tool declares itself wins.
func TestPresetDeclares(t *testing.T) {
	c, _, _ := viewCore(t)
	p, ok := presetByID("github")
	if !ok || len(p.Meta) == 0 {
		t.Fatal("no preset declares")
	}
	m := *c.conf()
	m.Servers = maps.Clone(m.Servers)
	m.Servers["code"] = Server{URL: p.URL}
	c.snap.Store(&m)
	u := &upstream{name: "code"}
	meta := c.toolMeta(u, &mcp.Tool{Name: "merge_pull_request"})
	if saysOf(meta) == nil || readsOf(meta) == nil {
		t.Fatalf("merge: %v", meta)
	}
	if got := saysOf(meta).phrase(map[string]any{"pullNumber": 12.0}, "", "", "fr", nil); got != "Fusionner la pull request #12" {
		t.Errorf("said: %q", got)
	}
	own := mcp.Meta{saysMetaKey: map[string]any{"text": map[string]any{"fr": "Le sien"}}}
	if s := saysOf(c.toolMeta(u, &mcp.Tool{Name: "merge_pull_request", Meta: own})); s == nil || inLang(s.Text, "fr") != "Le sien" {
		t.Errorf("its own declaration: %+v", s)
	}
	if meta := c.toolMeta(u, &mcp.Tool{Name: "list_issues"}); saysOf(meta) != nil || readsOf(meta) == nil {
		t.Errorf("a pattern: %v", meta)
	}
	if meta := c.toolMeta(&upstream{name: "monitor"}, &mcp.Tool{Name: "merge_pull_request"}); meta != nil {
		t.Errorf("another server: %v", meta)
	}
}

// A card reads what its call names with the read its tool declares, only
// a free read: a write declared as one is never run for a card.
func TestCardReadsOnlyFreeReads(t *testing.T) {
	c, fm, _ := viewCore(t)
	fs := c.callContext(t.Context(), "mcp__monitor__tag", map[string]any{"service": "api"})
	if len(fs) != 1 || fs[0].Name != "Service" || fs[0].Value != "api" {
		t.Errorf("a free read: %+v", fs)
	}
	if fs := c.callContext(t.Context(), "mcp__monitor__flag", map[string]any{"uid": "a1"}); fs != nil {
		t.Errorf("a write read for a card: %+v", fs)
	}
	fm.mu.Lock()
	defer fm.mu.Unlock()
	if len(fm.silenced) != 0 {
		t.Errorf("silence ran for a card: %v", fm.silenced)
	}
}

// Every declaration core holds (its own tools', its presets') says its
// words in French and English.
func TestCoreDeclarationsInBothLanguages(t *testing.T) {
	metas := map[string]mcp.Meta{"catalog_accept": {saysMetaKey: acceptSays}}
	for _, p := range toolPresets {
		for k, m := range p.Meta {
			metas[p.ID+"/"+k] = m
		}
	}
	both := func(m map[string]string) bool { return m["fr"] != "" && m["en"] != "" }
	bothN := func(m map[string][]string) bool { return len(m) == 0 || len(m["fr"]) == 2 && len(m["en"]) == 2 }
	for name, m := range metas {
		if s := saysOf(m); s != nil && (!both(s.Text) || !bothN(s.Items) || !bothN(s.Done) || len(s.Item) > 0 && !both(s.Item)) {
			t.Errorf("%s says in one language: %+v", name, s)
		}
		if r := readsOf(m); r != nil {
			for _, f := range r.Show {
				if !both(f.Name) {
					t.Errorf("%s names %s in one language", name, f.Field)
				}
			}
		}
		if saysOf(m) == nil && readsOf(m) == nil {
			t.Errorf("%s declares nothing that reads: %v", name, m)
		}
	}
}

// A number is said as written, however large: a pull request's #1234567.
func TestSaysANumber(t *testing.T) {
	if got := (&toolSays{}).argWords("n", 1234567.0, "fr", nil); got != "1234567" {
		t.Errorf("%q", got)
	}
	if got := (&toolSays{}).argWords("n", 2.5, "fr", nil); got != "2.5" {
		t.Errorf("%q", got)
	}
}

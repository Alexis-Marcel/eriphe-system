package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Core knows no particular tool (#727): how a call reads, what a card
// reads to say it, how an item opens, are what the tool declares (its
// annotations, its title, its schemas, its _meta: call_says.go,
// card_context.go, item_open.go), or what its preset declares in its place
// (presets_meta.go). No logic file names a tool, its tools or their
// arguments to branch on them.

// toolNameAllowed are the files where a tool may be named, and why. The
// text tables (web_text*) are checked apart (TestNoToolWordsInTables).
var toolNameAllowed = map[string]string{
	"tool_cards.go":       "the cards of Eriphe's own tools: their data",
	"presets.go":          "the presets: data",
	"presets_online.go":   "the gallery's presets: data",
	"presets_meta.go":     "what a preset's tools would declare, for a server Eriphe does not write: data",
	"web_gallery.go":      "the gallery's search words: data",
	"signin_providers.go": "the sign-in providers and the tools they serve: data",
	"agents_kinds.go":     "the model providers: data",
	"github_app.go":       "GitHub's app, the provider of its connections: its permissions",
	"repo_lend.go":        "git through GitHub, lending a repository: its permissions",
	"about_key.go":        "the memory's keys: « mail » or « event » are kinds of items the brain must not take for a key, « agenda » a record; not tools",
}

// commonWords are names of Eriphe's own tools that are also core's own
// everyday words (an upload's files, a skill's files): not guarded.
var commonWords = map[string]bool{"files": true, "fichiers": true}

// namedTools are names of tools, of their arguments and of apps core
// once branched on: GitHub's pull requests, the mail and calendar servers'
// lists of ids and their places, a channel's app.
var namedTools = []string{
	"pull_request_read", "get_pull_request", "create_pull_request", "update_pull_request", "merge_pull_request",
	"pullnumber", "pull_number", "pull_requests", "pulls",
	"search_messages", "read_message", "list_events", "get_event", "respond_event",
	"message_ids", "event_ids", "file_ids", "mailbox", "inbox",
	"homeassistant",
}

// namedApps are apps' names core once branched on, found anywhere in a
// string (a query's list of channels).
var namedApps = []string{"home assistant"}

// toolOfRe finds a tool of a server named in a text: mail/search_messages,
// mail.move.
var toolOfRe = regexp.MustCompile(`\b([a-z]+)[/.]([a-z_]{3,})\b`)

// toolNameHits are the strings of a file that name a tool: words, a
// name (the names of Eriphe's own tools and providers, as their data
// says); a string that is one of them, or a tool of one of them
// (mail/search_messages, mail.move), or one of namedTools.
func toolNameHits(t *testing.T, file string, words map[string]bool) []string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0) // without comments
	if err != nil {
		t.Fatal(err)
	}
	norm := regexp.MustCompile(`[^a-z0-9]+`)
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		if _, ok := n.(*ast.ImportSpec); ok {
			return false
		}
		x, ok := n.(*ast.BasicLit)
		if !ok || x.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			return true
		}
		s = strings.ToLower(strings.TrimSpace(s))
		head, _, _ := strings.Cut(s, "/")
		if h, _, ok := strings.Cut(s, "."); ok && !strings.Contains(h, "/") && len(h) < len(head) {
			head = h
		}
		embedded := slices.ContainsFunc(toolOfRe.FindAllStringSubmatch(s, -1), func(m []string) bool { return words[m[1]] })
		hit := embedded || words[norm.ReplaceAllString(s, "")] || slices.Contains(namedTools, s) ||
			slices.ContainsFunc(namedApps, func(a string) bool { return strings.Contains(s, a) }) ||
			head != s && words[head] && regexp.MustCompile(`^[a-z]+[/.][a-z_]+$`).MatchString(s)
		if hit {
			hits = append(hits, fset.Position(n.Pos()).String()+" "+x.Value)
		}
		return true
	})
	return hits
}

// guardedWords are the names of Eriphe's own tools and providers, from
// their data (toolWords), but core's everyday words.
func guardedWords(t *testing.T) map[string]bool {
	t.Helper()
	words, _ := toolWords()
	for w := range commonWords {
		delete(words, w)
	}
	if !words["mail"] || !words["calendar"] || !words["google"] {
		t.Fatalf("words %v", words)
	}
	return words
}

// No logic file of core names a tool, its tools or their arguments, but
// those allowed and why.
func TestNoToolNameInLogic(t *testing.T) {
	words := guardedWords(t)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if _, ok := toolNameAllowed[f]; ok || strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "web_text") {
			continue
		}
		for _, hit := range toolNameHits(t, f, words) {
			t.Errorf("%s names a tool: %s (read what the tool declares: call_says.go, card_context.go, item_open.go; or its preset's data: presets_meta.go)", f, hit)
		}
	}
	for f, why := range toolNameAllowed {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s is allowed but is not there", f)
		}
		if why == "" {
			t.Errorf("%s is allowed without a reason", f)
		}
		if len(toolNameHits(t, f, words)) == 0 {
			t.Errorf("%s is allowed but names no tool: take it off the list", f)
		}
	}
}

// The guard catches a tool's name, one of its tools and an argument of
// theirs in a string; a comment, an everyday word and a key of a text
// table are let be.
func TestToolNameGuardCatches(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.go")
	os.WriteFile(file, []byte(`package main

// mail/search_messages, for instance.
var a = map[string]string{"mail": "x", "agenda": "y", "via NOT IN ('Home Assistant')": "z"}
var b = []string{"mail/search_messages", "pull_request_read", "pullNumber", " (agenda)"}
var c = []string{"files", "mem.saved", "remote", "card.noun.item.one", "local", "a ref (box.move)"}
var d = "a ref there (mail.move): asked"
`), 0o600)
	hits := toolNameHits(t, file, guardedWords(t))
	if len(hits) != 8 {
		t.Fatalf("hits %v", hits)
	}
}

// genericWords are the keys of the text tables a call's words may use:
// none for a tool's own name (act.verb.trash, access.call.merge_…), nor a
// kind of item by a server's name (card.noun.mail).
var genericWords = map[string]bool{
	"act.verb.other": true, "act.done.one": true, "act.done.many": true, "access.call.other": true,
	"card.noun.item.one": true, "card.noun.item.many": true,
}

// No text table says a call by its tool's name: a tool declares its words.
func TestNoToolWordsInTables(t *testing.T) {
	for lang, words := range webStrings {
		for k := range words {
			for _, p := range []string{"act.verb.", "act.done.", "access.call.", "card.noun."} {
				if strings.HasPrefix(k, p) && !genericWords[k] {
					t.Errorf("%s: %s says a call by a tool's name (declare it: _meta eriphe/says)", lang, k)
				}
			}
		}
	}
}

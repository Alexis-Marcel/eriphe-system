package main

import (
	"errors"
	"strings"
	"testing"
)

// thingReads is a read declared as a tool's _meta carries it: a thing by
// its owner and number, its title then its description.
func thingReads(t *testing.T) *toolReads {
	t.Helper()
	r := readsOf(map[string]any{readsMetaKey: map[string]any{
		"tools": []any{map[string]any{"tool": "thing_read", "args": map[string]any{"method": "get"}}, map[string]any{"tool": "get_thing"}},
		"with":  []any{"owner", "number"},
		"show": []any{
			map[string]any{"field": "title", "name": map[string]any{"fr": "Chose", "en": "Thing"}},
			map[string]any{"field": "body", "name": map[string]any{"fr": "Description", "en": "Description"}},
		},
	}})
	if r == nil {
		t.Fatal("not read")
	}
	return r
}

// A card reads what a call names as its tool declares: the first read the
// server has, with the call's arguments it takes and its own; the fields
// shown, named in the user's language.
func TestReadContext(t *testing.T) {
	r := thingReads(t)
	has := func(name string) bool { return name == "get_thing" }
	var asked map[string]any
	read := func(name string, args map[string]any) (string, error) {
		if name != "get_thing" {
			t.Errorf("read %s", name)
		}
		asked = args
		return `{"title":"docs: write a real README","body":"` + strings.Repeat("x", cardDescriptionMax+10) + `"}`, nil
	}
	got := r.readContext(map[string]any{"owner": "sam", "number": 8.0, "other": "x"}, "fr", has, read)
	if len(got) != 2 || got[0].Name != "Chose" || got[0].Value != "docs: write a real README" || got[1].Name != "Description" || len([]rune(got[1].Value)) > cardDescriptionMax+1 {
		t.Fatalf("%+v", got)
	}
	if len(asked) != 2 || asked["owner"] != "sam" || asked["number"] != 8.0 {
		t.Errorf("asked: %+v", asked)
	}
	// The first read the server has, with its own arguments.
	r.readContext(map[string]any{"owner": "sam", "number": 8.0}, "en", func(string) bool { return true }, func(name string, args map[string]any) (string, error) {
		if name != "thing_read" || args["method"] != "get" {
			t.Errorf("read %s %v", name, args)
		}
		return "{}", nil
	})
}

// A card with no context is still a card.
func TestReadContextNone(t *testing.T) {
	r := thingReads(t)
	has := func(string) bool { return true }
	thing := map[string]any{"owner": "sam", "number": 8.0}
	for name, read := range map[string]func(string, map[string]any) (string, error){
		"error":    func(string, map[string]any) (string, error) { return "", errors.New("no") },
		"not json": func(string, map[string]any) (string, error) { return "a page", nil },
		"no title": func(string, map[string]any) (string, error) { return `{"body":"x"}`, nil },
	} {
		if got := r.readContext(thing, "fr", has, read); got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
	never := func(string, map[string]any) (string, error) { t.Fatal("read"); return "", nil }
	if got := r.readContext(map[string]any{"id": "42"}, "fr", has, never); got != nil {
		t.Errorf("not a thing: %+v", got)
	}
	if got := r.readContext(thing, "fr", func(string) bool { return false }, never); got != nil {
		t.Errorf("no such tool: %+v", got)
	}
	if readsOf(nil) != nil || readsOf(map[string]any{readsMetaKey: map[string]any{"with": []any{"id"}}}) != nil {
		t.Error("a declaration read from nothing")
	}
}

// A card lists what it read, then every parameter of the call; a content
// its diff says is not said twice.
func TestCardFields(t *testing.T) {
	in := map[string]any{"number": 8.0, "repo": "site"}
	got := cardFields([]cardField{{Name: "Chose", Value: "a title"}}, in)
	if len(got) != 3 || got[0].Name != "Chose" {
		t.Errorf("%+v", got)
	}
	if got := cardFields([]cardField{{Name: "diff", Value: "+a"}}, map[string]any{"content": "a", "path": "x"}); len(got) != 2 || got[1].Name != "path" {
		t.Errorf("diff: %+v", got)
	}
}

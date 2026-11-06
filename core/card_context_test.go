package main

import (
	"errors"
	"strings"
	"testing"
)

func TestPullContext(t *testing.T) {
	has := func(name string) bool { return name == "pull_request_read" }
	var asked map[string]any
	read := func(_ string, args map[string]any) (string, error) {
		asked = args
		return `{"title":"docs: write a real README","body":"` + strings.Repeat("x", cardDescriptionMax+10) + `"}`, nil
	}
	got := pullContext(map[string]any{"owner": "sam", "repo": "site", "pullNumber": 8.0}, has, read)
	if len(got) != 2 || got[0].Value != "#8 docs: write a real README" || len([]rune(got[1].Value)) > cardDescriptionMax+1 {
		t.Fatalf("%+v", got)
	}
	if asked["method"] != "get" || asked["pullNumber"] != 8.0 {
		t.Errorf("asked: %+v", asked)
	}
}

// A card with no context is still a card.
func TestPullContextNone(t *testing.T) {
	has := func(string) bool { return true }
	pull := map[string]any{"owner": "sam", "repo": "site", "pullNumber": 8.0}
	for name, read := range map[string]func(string, map[string]any) (string, error){
		"error":    func(string, map[string]any) (string, error) { return "", errors.New("no") },
		"not json": func(string, map[string]any) (string, error) { return "a page", nil },
		"no title": func(string, map[string]any) (string, error) { return `{"body":"x"}`, nil },
	} {
		if got := pullContext(pull, has, read); got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
	never := func(string, map[string]any) (string, error) { t.Fatal("read"); return "", nil }
	if got := pullContext(map[string]any{"id": "42"}, has, never); got != nil {
		t.Errorf("not a pull request: %+v", got)
	}
	if got := pullContext(pull, func(string) bool { return false }, never); got != nil {
		t.Errorf("no such tool: %+v", got)
	}
}

package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// One way to say yes: whatever asks the user's yes is a card, and every
// answer, from a card, a word, a channel or a page, goes through
// decideCardWith, which keeps the card in step. The deciders of each kind
// are called from nowhere else (withdrawProposal is code taking a
// proposal back, never the user's answer).
func TestOneWayToSayYes(t *testing.T) {
	allowed := map[string][]string{
		"decideCardWith":    {"decideCard", "decideCardChosen"},
		"decideProposal":    {"decideCard", "withdrawProposal"},
		"decideCallsChosen": {"decideCardChosen"},
		"decideCalls":       {"decideProposal"},
		"decideRunCalls":    {"decideProposal"},
		"decideAccess":      {"decideProposal", "decideCalls", "decideRunCalls", "decideCallsChosen"},
		"decideSetting":     {"decideProposal"},
		"decideSkill":       {"decideProposal"},
		"decideMonitor":     {"decideProposal"},
		"decideView":        {"decideProposal"},
		"decideTaskStart":   {"decideProposal"},
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	callers := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if _, watched := allowed[sel.Sel.Name]; watched {
						callers[sel.Sel.Name] = append(callers[sel.Sel.Name], fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	for name, by := range callers {
		for _, fn := range by {
			if !slices.Contains(allowed[name], fn) {
				t.Errorf("%s answers a proposal by %s, not through its card (decideCardWith)", fn, name)
			}
		}
	}
	if len(callers["decideCardWith"]) == 0 || len(callers["decideProposal"]) == 0 {
		t.Fatalf("the guard sees nothing: %v", callers)
	}
}

// A page's yes is its card's: the card is decided, by the page, and a
// proposal with no card yet gets one first.
func TestPageYesIsTheCards(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	c := tw.c
	r, err := c.addNotifyRule(notifyRule{Kind: ruleTopic, Match: "banque", Ceiling: 2, State: ruleProposed, By: "brain"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.answerProposal(context.Background(), cardRule, r.ID, true); err != nil {
		t.Fatal(err)
	}
	xs, _ := c.cardsWhere(`ref = ?`, proposalRef(cardRule, r.ID))
	if len(xs) != 1 || xs[0].State != cardApproved || xs[0].By != "page" {
		t.Fatalf("its card: %+v", xs)
	}
	if err := c.answerProposal(context.Background(), cardRule, r.ID, false); err != errCardDecided {
		t.Errorf("answered twice: %v", err)
	}
}

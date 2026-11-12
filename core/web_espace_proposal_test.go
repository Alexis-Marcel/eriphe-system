package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The workspace reads a proposal by its own ref: its card's text and why,
// and the card whose Oui or Non answers it; once answered, its state.
func TestEspaceProposal(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	c := tw.c
	r := proposedRule(t, c, "facture", "")
	brainSays(c, "Je ne te préviens plus des factures avant le brief ?")
	x := cardOf(t, c, cardRule, r.ID)
	ref := proposalRef(cardRule, r.ID)

	read := func() (int, proposalView) {
		code, body, _ := tw.do("GET", "/api/proposals/"+url.PathEscape(ref), nil, nil)
		var v proposalView
		if code == 200 {
			if err := json.Unmarshal([]byte(body), &v); err != nil {
				t.Fatal(err)
			}
		}
		return code, v
	}
	code, v := read()
	if code != 200 || v.Card != x.ID || v.State != cardPending || v.Ref != ref || v.Text == "" || v.Why != "Trop de messages." {
		t.Fatalf("GET: %d %+v", code, v)
	}

	h := map[string]string{"X-CSRF-Token": csrf}
	if code, _, _ := tw.do("POST", fmt.Sprintf("/cards/%d/yes", v.Card), url.Values{}, h); code != 200 {
		t.Fatalf("yes: %d", code)
	}
	if _, v = read(); v.State != cardApproved {
		t.Errorf("after yes: %+v", v)
	}
	// As the page asks (encodeURIComponent): the colon escaped.
	if code, body, _ := tw.do("GET", "/api/proposals/"+strings.ReplaceAll(ref, ":", "%3A"), nil, nil); code != 200 || !strings.Contains(body, `"state":"approved"`) {
		t.Errorf("GET with %%3A: %d %s", code, body)
	}

	for _, bad := range []string{"rule:999999", "nothing:1", "rule:x"} {
		if code, _, _ := tw.do("GET", "/api/proposals/"+url.PathEscape(bad), nil, nil); code != 404 {
			t.Errorf("GET %s: %d, want 404", bad, code)
		}
	}
}

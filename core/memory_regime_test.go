package main

import (
	"strings"
	"testing"
	"time"
)

// A proposal nobody decided is dropped after 14 days: a memory forgotten,
// an intention cancelled; a younger one waits on.
func TestProposalsExpire(t *testing.T) {
	m, clock := testMemory(t)
	old := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	oldIntent := mustIntend(t, m, &Intention{Text: "Payer le garage", State: intentProposed, SourceKind: srcExtraction, Trust: trustExternal})
	clock.add(10 * 24 * time.Hour)
	young := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le garage ferme lundi", Status: memProposed, SourceKind: srcExtraction})
	kept := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Sam habite à Lyon"})
	clock.add(5 * 24 * time.Hour)
	n, err := m.expireProposals()
	if err != nil || n != 2 {
		t.Fatalf("expired %d: %v", n, err)
	}
	if x, _ := m.memory(old.ID); x.Status != memDeleted {
		t.Fatalf("an old proposal: %+v", x)
	}
	if x, _ := m.intention(oldIntent.ID); x.State != intentCancelled {
		t.Fatalf("an old proposed intention: %+v", x)
	}
	for _, id := range []int64{young.ID, kept.ID} {
		if x, _ := m.memory(id); x.Status == memDeleted {
			t.Fatalf("dropped too soon: %+v", x)
		}
	}
	if n, _ := m.expireProposals(); n != 0 {
		t.Fatalf("expired again: %d", n)
	}
}

// The extraction's proposals have no card: « Ce qu'Eriphe a appris » says
// how many wait in À valider, and that they expire.
func TestLearnedPageSaysWhatWaits(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	mustAdd(t, tw.c.mem, &Memory{Kind: kindFact, Text: "Une rumeur", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	_, body, _ := tw.do("GET", "/settings/learned", nil, nil)
	if !strings.Contains(body, "1 souvenir(s) proposé(s)") || !strings.Contains(body, `href="/settings/approve"`) {
		t.Fatalf("page:\n%s", body)
	}
}

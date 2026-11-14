package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// siteSubject fills a subject with all a card holds, some of it drawn from
// external content, and things about other subjects.
func siteSubject(t *testing.T, c *core) Thread {
	t.Helper()
	m := c.mem
	for i := 1; i <= 4; i++ {
		mustAdd(t, m, &Memory{Kind: kindDecision, Subject: "project:site", Text: fmt.Sprintf("Décision %d du site", i)})
	}
	mustAdd(t, m, &Memory{Kind: kindProgress, Subject: "project:site", State: progressBlocked, Text: "Le site attend son certificat"})
	mustAdd(t, m, &Memory{Kind: kindFact, Subject: "project:site", Text: "Le domaine est example.org"})
	mustAdd(t, m, &Memory{Kind: kindFact, Subject: "project:site", Text: "LU DANS UNE PAGE WEB",
		SourceKind: srcBrain, Trust: trustExternal, SourceRef: "web:example.org"})
	mustAdd(t, m, &Memory{Kind: kindPerson, Subject: "person:lina", Text: "Lina relit les pages du site"})
	mustAdd(t, m, &Memory{Kind: kindPreference, Subject: "project:site", Text: "Le site reste sobre"})
	mustAdd(t, m, &Memory{Kind: kindFact, Subject: "project:other", Text: "UN FAIT SANS RAPPORT"})
	mustAdd(t, m, &Memory{Kind: kindFact, Subject: "project:site", Text: "Une idée à valider", Status: memProposed})
	mustIntend(t, m, &Intention{Text: "Relancer Lina pour le site", Subject: "project:site", OwedTo: "person:lina", DueAt: m.now().Add(time.Hour)})
	mustIntend(t, m, &Intention{Text: "Rendre le livre", OwedTo: "person:paul", DueAt: m.now().Add(time.Hour)})
	th, err := c.openThread(threadIn{Title: "Site en ligne", Goal: "Le site répond en https.", Subject: "project:site"}, nil, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	c.insertSteps(th.ID, []stepSpec{{Key: "cert", Title: "Obtenir le certificat", Kind: stepTask}}, false)
	c.setThread(th.ID, map[string]any{"summary": "Il reste le certificat"})
	return th
}

// A subject's card holds its thread, where it stands, its last three
// decisions, its facts and people, its intentions, its cards and its
// preferences; an external line is named, not quoted; nothing of another
// subject; and reading it taints nothing.
func TestSubjectCard(t *testing.T) {
	c, _ := testMemoryCore(t)
	th := siteSubject(t, c)
	card, st := c.subjectCard("project:site", 4000)
	for _, want := range []string{
		"Fiche du sujet project:site", fmt.Sprintf("- fil %d [light, open, sujet project:site", th.ID), "but : Le site répond en https.",
		"cert « Obtenir le certificat »", "résumé : Il reste le certificat",
		"Le site attend son certificat", "Décision 4 du site", "Décision 2 du site",
		"Le domaine est example.org", "fact tiré d'un contenu extérieur", "Lina relit les pages du site",
		"Relancer Lina pour le site", "carte memory:", "Une idée à valider", "Le site reste sobre",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card misses %q:\n%s", want, card)
		}
	}
	for _, not := range []string{"Décision 1 du site", "LU DANS UNE PAGE WEB", "UN FAIT SANS RAPPORT", "Rendre le livre"} {
		if strings.Contains(card, not) {
			t.Errorf("card has %q", not)
		}
	}
	if strings.Index(card, "fil ") > strings.Index(card, "Décision 4") || strings.Index(card, "Décision 4") > strings.Index(card, "Le site reste sobre") {
		t.Errorf("order:\n%s", card)
	}
	if st.Dropped != 0 || st.Chars != runeLen(card) || strings.Count(card, "```") != 2 {
		t.Errorf("stats %+v", st)
	}
	if again, _ := c.subjectCard("project:site", 4000); again != card {
		t.Error("the same records give another card")
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("the card tainted the turn: %v", tainted)
	}
	if card, _ := c.subjectCard("project:nothing", 1000); card != "" {
		t.Errorf("an unknown subject has a card: %s", card)
	}
}

// A card keeps to its budget: what does not fit is cut from the bottom,
// and said.
func TestSubjectCardBudget(t *testing.T) {
	c, _ := testMemoryCore(t)
	siteSubject(t, c)
	card, st := c.subjectCard("project:site", subjectCardChars)
	if st.Chars > subjectCardChars || runeLen(card) != st.Chars {
		t.Fatalf("%d chars over %d:\n%s", st.Chars, subjectCardChars, card)
	}
	if !strings.Contains(card, "- fil ") {
		t.Errorf("the thread first:\n%s", card)
	}
	small, st := c.subjectCard("project:site", 500)
	if st.Dropped == 0 || !strings.Contains(small, "(9 de plus : recall") || runeLen(small) > 500 || strings.Count(small, "```") != 2 {
		t.Errorf("cut %+v:\n%s", st, small)
	}
}

// A tainted thread is named, its plan not quoted.
func TestSubjectCardTaintedThread(t *testing.T) {
	c, _ := testMemoryCore(t)
	th, err := c.openThread(threadIn{Title: "PLAN EXTÉRIEUR", Goal: "Suivre ce que dit le mail.", Subject: "project:mail"}, []string{"external:mail:43"}, byBrain)
	if err != nil {
		t.Fatal(err)
	}
	card, _ := c.subjectCard("project:mail", subjectCardChars)
	if !strings.Contains(card, fmt.Sprintf("- fil %d [light, open] : son plan peut porter", th.ID)) || strings.Contains(card, "PLAN EXTÉRIEUR") {
		t.Fatalf("card:\n%s", card)
	}
}

// read_subject finds a subject by its key or by a name, and returns its
// card; it never taints the turn.
func TestReadSubject(t *testing.T) {
	c, _ := testMemoryCore(t)
	siteSubject(t, c)
	if err := c.addSubjectNames("project:site", "Site web", []string{"Velora"}); err != nil {
		t.Fatal(err)
	}
	want, _ := c.subjectCard("project:site", subjectCardChars)
	for _, s := range []string{"project:site", "Projet : Site", "velora", "le site"} {
		out, isErr := callBrainTool(t, c, "read_subject", map[string]any{"subject": s})
		if isErr || out["subject"] != "project:site" || out["card"] != want {
			t.Errorf("%s: %v", s, out)
		}
	}
	if out, isErr := callBrainTool(t, c, "read_subject", map[string]any{"subject": "rien du tout"}); !isErr {
		t.Errorf("unknown: %v", out)
	}
	if _, _, _, tainted := c.turn.snapshot(); len(tainted) != 0 {
		t.Fatalf("read_subject tainted the turn: %v", tainted)
	}
}

// Réglages → Sujets lists the subjects and shows a card as the model gets it.
func TestSubjectsPage(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	th := siteSubject(t, tw.c)
	code, body, _ := tw.do("GET", "/settings/subjects", nil, nil)
	if code != 200 || !strings.Contains(body, `href="/settings/subjects?s=project%3asite"`) || !strings.Contains(body, `href="/settings/subjects"`) ||
		strings.Contains(body, "Fiche du sujet") {
		t.Fatalf("list %d:\n%s", code, body)
	}
	_, body, _ = tw.do("GET", "/settings/subjects?s=project:site", nil, nil)
	card, _ := tw.c.subjectCard("project:site", subjectCardChars)
	esc := strings.NewReplacer("«", "«", "'", "&#39;", "\"", "&#34;", "<", "&lt;", ">", "&gt;", "&", "&amp;").Replace(card)
	if !strings.Contains(body, esc) || !strings.Contains(body, fmt.Sprintf(`href="/threads/%d"`, th.ID)) {
		t.Fatalf("card:\n%s", body)
	}
	_, body, _ = tw.do("GET", "/settings/subjects?s=project:nope", nil, map[string]string{"Cookie": "lang=en"})
	if !strings.Contains(body, "This subject is not known.") {
		t.Fatalf("unknown:\n%s", body)
	}
}

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// One key per subject: case, accents, spaces, French and plural kind
// names, a topic's plural, unknown kinds and bare words as topics; a
// pull request, a mail's and an event's ids; owner and home alone.
func TestSubjectKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"  ":                      "",
		"project:getout":          "project:getout",
		" Project:Getout ":        "project:getout",
		"projet:Getout":           "project:getout",
		"Projet : Site Web":       "project:site-web",
		"Projet : Getout : Prod":  "project:getout:prod",
		"project:getout:releases": "project:getout:releases",
		"person:Léa Martin":       "person:lea-martin",
		"personne:jules":          "person:jules",
		"Personne: Hélène":        "person:helene",
		"client:Example Org":      "client:example-org",
		"pr:owner/repo#12":        "pr:owner/repo#12",
		" PR:Example/Infra#12 ":   "pr:example/infra#12",
		"pull-request:a/b#3":      "pr:a/b#3",
		"mail:INBOX/42":           "mail:INBOX/42",
		"mail:<ab.C@example.org>": "mail:<ab.C@example.org>",
		"event:ev-1@example.org":  "event:ev-1@example.org",
		"alerte:sauvegarde":       "alert:sauvegarde",
		"alert:7":                 "alert:7",
		"owner":                   "owner",
		"Owner":                   "owner",
		"owner:sam":               "owner",
		"home":                    "home",
		"maison:Salon":            "home:salon",
		"outil:Mail":              "tool:mail",
		"Factures":                "topic:facture",
		"facture":                 "topic:facture",
		"topic:factures":          "topic:facture",
		"sujet:Factures":          "topic:facture",
		"invoices":                "topic:invoice",
		"devis":                   "topic:devis",
		"colis":                   "topic:colis",
		"status":                  "topic:status",
		"notes de frais":          "topic:notes-de-frais",
		"workspace":               "topic:workspace",
		"journal":                 "topic:journal",
		"brand:velora":            "topic:brand-velora",
		"source:mail":             "topic:source-mail",
		"veille:x":                "topic:veille-x",
	} {
		got, err := subjectKey(in)
		if err != nil || got != want {
			t.Errorf("subjectKey(%q) = %q %v, want %q", in, got, err, want)
		}
		if again, err := subjectKey(got); err != nil || again != got {
			t.Errorf("subjectKey not idempotent on %q: %q %v", got, again, err)
		}
	}
	for _, bad := range []string{"Projet X !", "task:t-12", "intention:5", "card:12", "thread:3", "skill:veille", "pr:example",
		"pr:owner/repo", "pr:owner/repo#x", "mail:a b", "topic:", "project:", "a\nb", "project:" + strings.Repeat("x", 200)} {
		if got, err := subjectKey(bad); err == nil {
			t.Errorf("subjectKey(%q) = %q, want an error", bad, got)
		}
	}
	for _, k := range subjectKinds {
		if recordKinds[k] {
			t.Errorf("%s is both a subject's kind and a record's", k)
		}
	}
}

// A filter is a key, or every key of a kind.
func TestSubjectFilter(t *testing.T) {
	for in, want := range map[string]struct {
		key    string
		prefix bool
	}{
		"":              {"", false},
		"person:":       {"person:", true},
		"personne:*":    {"person:", true},
		"Projet:Site":   {"project:site", false},
		"project:site*": {"project:site", true},
	} {
		key, prefix, err := subjectFilter(in)
		if err != nil || key != want.key || prefix != want.prefix {
			t.Errorf("subjectFilter(%q) = %q %v %v, want %+v", in, key, prefix, err, want)
		}
	}
	if _, _, err := subjectFilter("nothing:"); err == nil {
		t.Error("a filter on an unknown kind")
	}
}

// The same subject, written another way by each tool, is one key in each
// store, and finds its thread, memory and rule again.
func TestOneSubjectEverywhere(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	const said, key = "Projet : Site Web", "project:site-web"
	seq := ownerSays(c, "Le site web passe en ligne vendredi, préviens-moi de tout ce qui le concerne")
	if _, err := c.brainRemember(rememberIn{Text: "Le site web passe en ligne vendredi.", Kind: kindFact, Subject: said, SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainIntend(intendIn{Text: "Relire la page d'accueil.", Subject: "projet:site web", OwedTo: "Personne:Sam", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	th, _, err := c.openThreadOnce(threadIn{Title: "Site web", Goal: "Le site est en ligne.", Subject: "PROJECT: site_web"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainRule(ruleIn{Match: "projets:Site Web", Floor: 5, Why: "Demandé.", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	out, err := c.brainNotify(notifyIn{Text: "Le site est en ligne.", Weight: 5, Why: "Déployé.", Ref: "Projet:Site-Web"})
	if err != nil {
		t.Fatal(err)
	}
	if out["subject"] != key {
		t.Errorf("notification under %v", out["subject"])
	}
	if th.Subject != key {
		t.Errorf("thread under %q", th.Subject)
	}
	if found, err := c.findThreads("projet:Site Web", "", true, 5); err != nil || len(found) != 1 || found[0].ID != th.ID {
		t.Errorf("find_thread: %+v %v", found, err)
	}
	xs, err := c.mem.recall(recallQuery{Subject: "Projet:site web"})
	if err != nil || len(xs) != 1 || xs[0].Subject != key {
		t.Errorf("recall by subject: %+v %v", xs, err)
	}
	var subj, owed string
	c.mem.db.QueryRow(`SELECT subject, owed_to FROM intentions`).Scan(&subj, &owed)
	if subj != key || owed != "person:sam" {
		t.Errorf("intention under %q, owed to %q", subj, owed)
	}
	rules, _ := c.notifyRules()
	if len(rules) != 1 || rules[0].Match != key {
		t.Errorf("rules %+v", rules)
	}
}

// A subject has one live thread, whoever opens it: the brain, a skill. An
// ended one is not reopened beside a live one; the index holds it too.
func TestOneLiveThreadPerSubject(t *testing.T) {
	c := stepsCore(t)
	first := lightThread(t, c) // pr:example/infra#12
	if _, err := c.openThread(threadIn{Title: "PR", Goal: "Merged.", Subject: "PR:Example/Infra#12"}, nil, "skill:watch-prs"); err == nil ||
		!strings.Contains(err.Error(), "follows pr:example/infra#12 already") {
		t.Fatalf("a skill opened a second live thread: %v", err)
	}
	if err := c.closeLight(first.ID, "Fusionnée.", byBrain); err != nil {
		t.Fatal(err)
	}
	second, err := c.openThread(threadIn{Title: "PR again", Goal: "Merged.", Subject: "pr:example/infra#12"}, nil, "skill:watch-prs")
	if err != nil {
		t.Fatalf("a subject whose thread ended: %v", err)
	}
	if _, err := c.reopenThread(first.ID, "La PR revient.", byBrain); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("an ended thread reopened beside the live one: %v", err)
	}
	if _, err := c.store.db.Exec(`INSERT INTO threads (title, goal, subject, state, created) VALUES ('x', 'y', ?, 'open', 0)`, second.Subject); err == nil {
		t.Fatal("the index lets two live threads follow one subject")
	}
	if _, err := c.openThread(threadIn{Title: "Sans sujet", Goal: "x"}, nil, "skill:x"); err != nil {
		t.Fatalf("a thread without subject: %v", err)
	}
	if _, err := c.openThread(threadIn{Title: "Sans sujet", Goal: "y"}, nil, "skill:x"); err != nil {
		t.Fatalf("a second thread without subject: %v", err)
	}
}

// One normaliser: no other code makes its own subject key. The old
// functions are gone, and no file but subject.go lowers, trims or
// compares a subject in its own way.
func TestNoOtherSubjectNormaliser(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`\b(canonRef|normalizeSubject|refKinds|refKindAlias|idKinds|isIDRef|ruleRefRe|threadSubjectMax)\b`),
		regexp.MustCompile(`(?i)lower\(\s*subject\s*\)`),
		regexp.MustCompile(`strings\.(ToLower|TrimSpace|EqualFold|Fields|ReplaceAll)\([^()]*\b(in|x|s|r|q|f|th|old)\.(Subject|OwedTo|Owner|Match)\b`),
		regexp.MustCompile(`strings\.(ToLower|TrimSpace)\(\s*(subject|label)\s*\)`),
	}
	// Where the word means something else, or is searched as text.
	allowed := map[string]bool{
		"subject.go": true,
		// decisions.subject: the string the access policy compares; a
		// mail's subject line.
		"policy.go": true, "web_push.go": true,
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, re := range forbidden {
				if re.MatchString(line) {
					t.Errorf("%s:%d makes a subject key of its own (use subjectKey, subject.go): %s", f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

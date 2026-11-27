package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// One key per matter or entity: case, accents, spaces, French and plural
// kind names, a topic's plural, unknown kinds and bare words as topics; a
// pull request, an alert's id; owner and home alone. A mail or an event
// is an item, never a key.
func TestAliasKey(t *testing.T) {
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
		"theme:Factures":          "topic:facture",
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
		got, err := aliasKey(in)
		if err != nil || got != want {
			t.Errorf("aliasKey(%q) = %q %v, want %q", in, got, err, want)
		}
		if again, err := aliasKey(got); err != nil || again != got {
			t.Errorf("aliasKey not idempotent on %q: %q %v", got, again, err)
		}
	}
	for _, bad := range []string{"Projet X !", "task:t-12", "intention:5", "card:12", "node:k3x9m2p7qa", "skill:veille", "pr:example",
		"pr:owner/repo", "pr:owner/repo#x", "mail:INBOX/42", "mail:<ab.C@example.org>", "event:ev-1@example.org", "courriel:x", "topic:", "project:", "a\nb", "project:" + strings.Repeat("x", 200)} {
		if got, err := aliasKey(bad); err == nil {
			t.Errorf("aliasKey(%q) = %q, want an error", bad, got)
		}
	}
	for _, k := range aliasKinds {
		if recordKinds[k] {
			t.Errorf("%s is both a key's kind and a record's", k)
		}
	}
}

// A filter is a key, or every key of a kind.
func TestAboutFilter(t *testing.T) {
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
		key, prefix, err := aboutFilter(in)
		if err != nil || key != want.key || prefix != want.prefix {
			t.Errorf("aboutFilter(%q) = %q %v %v, want %+v", in, key, prefix, err, want)
		}
	}
	if _, _, err := aboutFilter("nothing:"); err == nil {
		t.Error("a filter on an unknown kind")
	}
	if key, prefix, err := aboutFilter("node:k3x9m2p7qa"); err != nil || key != "node:k3x9m2p7qa" || prefix {
		t.Errorf("a node: %q %v %v", key, prefix, err)
	}
}

// The same matter, written another way by each tool, is one about in
// each store: its intention, rule and notification are found again by
// it.
func TestOneAboutEverywhere(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	const said, key = "Projet : Site Web", "project:site-web"
	seq := ownerSays(c, "Le site web passe en ligne vendredi, préviens-moi de tout ce qui le concerne")
	if _, err := c.brainIntend(intendIn{Text: "Relire la page d'accueil.", About: said, OwedTo: "Personne:Sam", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.brainRule(ruleIn{Match: "projets:Site Web", Floor: 5, Why: "Demandé.", SourceRef: ref(seq)}); err != nil {
		t.Fatal(err)
	}
	out, err := c.brainNotify(notifyIn{Text: "Le site est en ligne.", Weight: 5, Why: "Déployé.", About: "PROJECT: site_web"})
	if err != nil {
		t.Fatal(err)
	}
	if out["about"] != key {
		t.Errorf("notification under %v", out["about"])
	}
	var subj, owed string
	c.intents.db.QueryRow(`SELECT about, owed_to FROM intentions`).Scan(&subj, &owed)
	if subj != key || owed != "person:sam" {
		t.Errorf("intention under %q, owed to %q", subj, owed)
	}
	rules, _ := c.notifyRules()
	if len(rules) != 1 || rules[0].Match != key {
		t.Errorf("rules %+v", rules)
	}
	// A node is node:<id>, its id checked.
	if k, err := aboutKey(" node:k3x9m2p7qa "); err != nil || k != "node:k3x9m2p7qa" {
		t.Errorf("node about %q %v", k, err)
	}
	if _, err := aboutKey("node:Not An Id"); err == nil {
		t.Error("a node about that is no node's id")
	}
}

// One normaliser: no other code makes its own key. The old functions are
// gone, and no file but about_key.go lowers, trims or compares an about
// in its own way.
func TestNoOtherSubjectNormaliser(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`\b(canonRef|normalizeSubject|refKinds|refKindAlias|idKinds|isIDRef|ruleRefRe|threadSubjectMax)\b`),
		regexp.MustCompile(`(?i)lower\(\s*(subject|about)\s*\)`),
		regexp.MustCompile(`strings\.(ToLower|TrimSpace|EqualFold|Fields|ReplaceAll)\([^()]*\b(in|x|s|r|q|f|th|old)\.(About|Alias|OwedTo|Owner|Match)\b`),
		regexp.MustCompile(`strings\.(ToLower|TrimSpace)\(\s*(subject|about|alias|label)\s*\)`),
	}
	// Where the word means something else, or is searched as text.
	allowed := map[string]bool{
		"about_key.go": true,
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
					t.Errorf("%s:%d makes a key of its own (use aliasKey, about_key.go): %s", f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Standing approvals (standing_approval.go): proposed on a card, they hold
// only once the user says yes there.

// approveOnCard is the user's yes on a rule's standing approval card.
func approveOnCard(t *testing.T, c *core, ruleID int64) {
	t.Helper()
	g := c.ruleApproval(ruleID)
	if g == nil || g.State != approvalProposed {
		t.Fatalf("no proposed approval for rule %d: %+v", ruleID, g)
	}
	if err := c.decideProposal(context.Background(), cardApproval, g.ID, true); err != nil {
		t.Fatal(err)
	}
}

// A standing approval is a card: not usable before the yes, usable after;
// a no revokes it. Its card names the exact call, and warns when its rule
// fires on facts.
func TestStandingApprovalOnCard(t *testing.T) {
	c, _, _ := pollCore(t)
	seq := ownerSays(c, "Surveille les alertes du déploiement ; si la 2.0.1 casse, tu peux remettre la 2.0.0")
	out, err := c.brainEventRule(ruleEventIn{Name: "Déploiement", Source: "alerte", Prompt: "Si la 2.0.1 casse, remets la 2.0.0", SourceRef: ref(seq), Urgency: urgencyNormal,
		Action: &grantAction{Tool: "monitor.rollback", Args: map[string]any{"version": "2.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	if note, _ := out["approval"].(string); !strings.Contains(note, "only proposed") {
		t.Fatalf("the brain is not told it is proposed: %+v", out)
	}
	id := out["rule"].(eventRuleView).ID
	if c.liveApproval(id) != nil {
		t.Fatal("usable before the yes")
	}
	g := c.ruleApproval(id)
	p := c.proposalOf(cardApproval, g.ID)
	if !p.Pending {
		t.Fatalf("no pending card: %+v", p)
	}
	if text := p.text("en"); !strings.Contains(text, "monitor.rollback") || !strings.Contains(text, "2.0.0") || strings.Contains(text, "a fact can be wrong") {
		t.Fatalf("card: %s", text)
	}
	found := false
	for _, q := range c.pendingProposals() {
		found = found || q.Kind == cardApproval && q.ID == g.ID
	}
	if !found {
		t.Fatal("not listed with the pending cards")
	}
	approveOnCard(t, c, id)
	if c.liveApproval(id) == nil || c.proposalOf(cardApproval, g.ID).Pending {
		t.Fatal("the yes did not make it hold")
	}

	// Another, refused: revoked.
	seq = ownerSays(c, "Tu peux aussi remettre la 1.9.0")
	if _, err := c.brainUpdateRule(updateRuleIn{ID: id, Action: "grant", SourceRef: ref(seq),
		Grant: &grantAction{Tool: "monitor.rollback", Args: map[string]any{"version": "1.9.0"}}}); err != nil {
		t.Fatal(err)
	}
	g2 := c.ruleApproval(id)
	if err := c.decideProposal(context.Background(), cardApproval, g2.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.standingApproval(g2.ID); got.Revoked.IsZero() {
		t.Fatal("a no did not revoke it")
	}
}

// A rule that fires on facts says so on its approval card.
func TestStandingApprovalWarnsOnFacts(t *testing.T) {
	r := &rule{Source: sourceFact, Spec: ruleWhen{Fact: "context.place"}, If: "prod.incidents > 2 and now.time >= 18:00"}
	if got := ruleFactPaths(r); strings.Join(got, ",") != "context.place,prod.incidents" {
		t.Fatalf("paths: %v", got)
	}
	if got := ruleFactPaths(&rule{Source: sourceSchedule, If: "now.day in (weekday)"}); len(got) != 0 {
		t.Fatalf("the time is not a fact: %v", got)
	}
}

// The watches' grants of before are kept, renamed, and still hold.
func TestWatchGrantsMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eriphe.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE standing_approvals`,
		`CREATE TABLE watch_grants (id INTEGER PRIMARY KEY AUTOINCREMENT, watch_id INTEGER NOT NULL, kind TEXT NOT NULL, tool TEXT NOT NULL,
		  input TEXT NOT NULL, input_hash TEXT NOT NULL, max_uses INTEGER NOT NULL DEFAULT 1, uses INTEGER NOT NULL DEFAULT 0,
		  expires INTEGER NOT NULL, source_ref TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL, revoked INTEGER, revoked_why TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX watch_grants_watch ON watch_grants (watch_id)`,
		`INSERT INTO watch_grants (watch_id, kind, tool, input, input_hash, expires, created) VALUES (7, 'tool', 'mcp__monitor__rollback', '{}', 'h', 4000000000, 1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var state string
	if err := s.db.QueryRow(`SELECT state FROM standing_approvals WHERE rule_id = 7`).Scan(&state); err != nil || state != approvalActive {
		t.Fatalf("kept: %q %v", state, err)
	}
}

// The notification rules of before move into rules at start, with new ids,
// and their cards follow them (no card moved twice).
func TestNotifyRulesMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eriphe.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		notifyRulesSchema,
		`ALTER TABLE notify_rules ADD COLUMN channels TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notify_rules ADD COLUMN also TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notify_rules ADD COLUMN never_on TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO rules (id, name, source, prompt, state, by, created, updated) VALUES (1, 'Brief', 'horaire', 'Fais le brief.', 'active', 'owner', 1, 1)`,
		`INSERT INTO notify_rules (id, kind, match, outcome, state, by, created, updated) VALUES (1, 'topic', 'prod', 'now', 'active', 'owner', 1, 1)`,
		`INSERT INTO notify_rules (id, kind, quiet_from, quiet_to, state, by, created, updated) VALUES (2, 'quiet', 1320, 420, 'proposed', 'brain', 1, 1)`,
		`INSERT INTO convo_cards (ref, kind, seq, created) VALUES ('rule:1', 'rule', 11, 1), ('rule:2', 'rule', 22, 1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notify_rules`).Scan(&n); err == nil {
		t.Fatal("the old table is still there")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM rules WHERE source = ?`, sourceNotification).Scan(&n)
	if n != 2 {
		t.Fatalf("moved: %d", n)
	}
	cards := map[int]string{}
	rows, _ := s.db.Query(`SELECT seq, ref FROM convo_cards`)
	for rows.Next() {
		var seq int
		var ref string
		rows.Scan(&seq, &ref)
		cards[seq] = ref
	}
	rows.Close()
	for seq, want := range map[int]string{11: "rule:2", 22: "rule:3"} {
		if cards[seq] != want {
			t.Fatalf("cards: %v", cards)
		}
	}
	var state, act string
	s.db.QueryRow(`SELECT state, act FROM rules WHERE id = 3`).Scan(&state, &act)
	if state != "proposed" || act != "quiet" {
		t.Fatalf("quiet hours: %q %q", state, act)
	}
	// The event rule stays the only one rules() reads.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rules WHERE source != ?`, sourceNotification).Scan(&n); err != nil || n != 1 {
		t.Fatalf("event rules: %d %v", n, err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The key grammar: a kind, or kind:attribute=value with an attribute of
// that kind (or source), in lower case.
func TestParseRuleKey(t *testing.T) {
	for in, want := range map[string]string{
		"mail":                      "mail",
		" Mail:From=Example.ORG ":   "mail:from=example.org",
		"alert:source=backup":       "alert:source=backup",
		"alert:source=skill:backup": "alert:source=skill:backup",
		"card:kind=memory":          "card:kind=memory",
		"session:lead=lead-2":       "session:lead=lead-2",
		"task:agent=sonnet":         "task:agent=sonnet",
		"event:calendar=home":       "event:calendar=home",
		"project:name=getout":       "project:name=getout",
	} {
		k, err := parseRuleKey(in)
		if err != nil || k.String() != want {
			t.Errorf("%q: %v %v, want %s", in, k, err, want)
		}
	}
	for _, bad := range []string{"", "nope", "mail:", "mail:from", "mail:from=", "mail:subject=x", "session:project=x",
		"card:from=x", "mail:from=" + strings.Repeat("a", ruleValueMax+1), "mail:from=a\nb"} {
		if k, err := parseRuleKey(bad); err == nil {
			t.Errorf("%q parsed: %v", bad, k)
		}
	}
}

// A sender matches by its address, its domain and the domains above it;
// a source whole and by its name.
func TestRuleKeyMatching(t *testing.T) {
	got := senderValues("Lou <lou@mail.example.org>")
	if !slices.Equal(got, []string{"lou@mail.example.org", "mail.example.org", "example.org"}) {
		t.Fatalf("sender values %v", got)
	}
	mail := ruleAttrs{Kind: refMail, Attrs: map[string][]string{"from": got, "source": sourceValues("skill:mail")}}
	for key, want := range map[string]bool{
		"mail": true, "mail:from=example.org": true, "mail:from=mail.example.org": true, "mail:from=lou@mail.example.org": true,
		"mail:from=example.net": false, "mail:from=org": false, "mail:source=mail": true, "mail:source=skill:mail": true,
		"mail:source=backup": false, "alert": false, "alert:source=mail": false, "mail:folder=inbox": false,
	} {
		k, err := parseRuleKey(key)
		if err != nil {
			t.Fatal(err)
		}
		if k.matches(mail) != want {
			t.Errorf("%s matches: %v, want %v", key, !want, want)
		}
	}
}

func ruleNamed(t *testing.T, c *core, in showRuleIn) attentionRule {
	t.Helper()
	r, err := c.addRule(in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Rules move the weights of what they match, add up, stay at 5 at most,
// and drop what falls to 0 or below; removing one brings the item back.
func TestRulesMoveWeights(t *testing.T) {
	c := startCore(t)
	c.attend(attentionItem{Ref: "alert:1", Weight: 3, Why: "backup failed", Source: "skill:backup"})
	c.attend(attentionItem{Ref: "alert:2", Weight: 2, Why: "disk full", Source: "skill:disk"})
	c.attend(attentionItem{Ref: "project:getout", Weight: 4, Why: "a release", Source: "brain"})
	weights := func() string {
		var out []string
		for _, it := range c.attentionOpen() {
			out = append(out, fmt.Sprintf("%s/%d", it.Ref, it.Weight))
		}
		return strings.Join(out, ",")
	}
	if got := weights(); got != "project:getout/4,alert:1/3,alert:2/2" {
		t.Fatalf("no rule: %s", got)
	}
	ruleNamed(t, c, showRuleIn{Key: "alert", Delta: 1, Label: "les alertes"})
	ruleNamed(t, c, showRuleIn{Key: "project:name=getout", Delta: 3})
	if got := weights(); got != "project:getout/5,alert:1/4,alert:2/3" {
		t.Fatalf("raised: %s", got)
	}
	never := ruleNamed(t, c, showRuleIn{Key: "alert:source=backup", Delta: ruleNever, Label: "les alertes de sauvegarde"})
	if got := weights(); got != "project:getout/5,alert:2/3" {
		t.Fatalf("never: %s", got)
	}
	// One rule per key: a new one replaces it; a remark alone keeps its delta.
	ruleNamed(t, c, showRuleIn{Key: "Alert", Delta: -1})
	if got := weights(); got != "project:getout/5,alert:2/1" {
		t.Fatalf("replaced: %s", got)
	}
	ruleNamed(t, c, showRuleIn{Key: "alert", Remark: "seulement le matin"})
	rs := c.attentionRules()
	if len(rs) != 3 || rs[0].Key != "alert" || rs[0].Delta != -1 || rs[0].Remark != "seulement le matin" || rs[0].Label != "alert" {
		t.Fatalf("rules %+v", rs)
	}
	if err := c.removeRule(never.ID); err != nil {
		t.Fatal(err)
	}
	if got := weights(); got != "project:getout/5,alert:1/2,alert:2/1" {
		t.Fatalf("removed: %s", got)
	}
	if err := c.removeRule(never.ID); err != errNoRule {
		t.Fatalf("removed twice: %v", err)
	}
	for _, bad := range []showRuleIn{{Key: "nope", Delta: 1}, {Key: "mail", Delta: 6}, {Key: "mail", Delta: -6}, {Key: "mail"}} {
		if _, err := c.addRule(bad); err == nil {
			t.Errorf("%+v kept", bad)
		}
	}
	// The composer reads them, one sentence each; the last one made is a remark.
	ctx := composerRules(c)
	if len(ctx) != 2 || !strings.Contains(ctx[0], "seulement le matin") || !strings.Contains(ctx[0], "moins en avant (-1)") ||
		!strings.Contains(ctx[1], "project:name=getout") {
		t.Fatalf("composer's rules %q", ctx)
	}
	if stamp, words := c.lastRule(); stamp == "" || !strings.Contains(words, "seulement le matin") {
		t.Fatalf("last rule %q %q", stamp, words)
	}
}

// A card waits for the user's answer: a rule lowers it to 1, never hides
// it, and a rule on its kind applies to it.
func TestRulesKeepCards(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "10:00"))
	sid, _ := c.proposeSetting(settingProposal{Name: setCap, Value: "3", Why: "Trop de notifications."})
	c.cardProposed(cardSetting, sid)
	brainSays(c, "Je baisse le plafond ?")
	ref := cardItemRef(cardOf(t, c, cardSetting, sid).ID)
	if it := attentionOf(c, ref); it == nil || it.Weight != weightProposal {
		t.Fatalf("no item: %+v", it)
	}
	ruleNamed(t, c, showRuleIn{Key: "card:kind=setting", Delta: 1})
	if it := attentionOf(c, ref); it == nil || it.Weight != weightProposal+1 {
		t.Fatalf("not raised: %+v", it)
	}
	ruleNamed(t, c, showRuleIn{Key: "card", Delta: ruleNever})
	if it := attentionOf(c, ref); it == nil || it.Weight != 1 {
		t.Fatalf("a card hidden: %+v", it)
	}
}

// Rules are kept in the store: another eriphe-core on the same database
// has them.
func TestRulesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{User: "Sam"}, s)
	ruleNamed(t, c, showRuleIn{Key: "mail:from=example.org", Delta: 2, Label: "les mails de example.org", Remark: "surtout les factures"})
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	rs := newCore(&Config{User: "Sam"}, s).attentionRules()
	if len(rs) != 1 || rs[0].Key != "mail:from=example.org" || rs[0].Delta != 2 || rs[0].Label != "les mails de example.org" ||
		rs[0].Remark != "surtout les factures" || time.Since(rs[0].At) > time.Minute {
		t.Fatalf("after reopening: %+v", rs)
	}
}

// A rule on a mail's sender reads the mail once, from its server; one that
// cannot be read matches by its kind only, and is not asked again at once.
func TestRulesReadObjects(t *testing.T) {
	c, f := objectsCore(t)
	c.attend(attentionItem{Ref: "mail:INBOX/1", Weight: 3, Why: "dinner", Source: "skill:mail"})
	ruleNamed(t, c, showRuleIn{Key: "session", Delta: 1})
	c.attentionOpen()
	if n := len(f.calls("read_message")); n != 0 {
		t.Fatalf("read with no rule on a mail's attribute: %d", n)
	}
	ruleNamed(t, c, showRuleIn{Key: "mail:from=example.org", Delta: -3})
	if it := attentionOf(c, "mail:INBOX/1"); it != nil {
		t.Fatalf("not dropped: %+v", it)
	}
	c.attentionOpen()
	if n := len(f.calls("read_message")); n != 1 {
		t.Fatalf("read %d times, want once", n)
	}
	f.set(true)
	c.attend(attentionItem{Ref: "mail:INBOX/2", Weight: 3, Why: "news", Source: "skill:mail"})
	if it := attentionOf(c, "mail:INBOX/2"); it == nil || it.Weight != 3 {
		t.Fatalf("an unreadable mail: %+v", it)
	}
	c.attentionOpen()
	if n := len(f.calls("read_message")); n != 2 {
		t.Fatalf("a failed read asked again at once: %d", n)
	}
}

// The brain only proposes a rule, on a card: nothing changes before the
// user's yes; no drops it.
func TestWriteRuleProposesACard(t *testing.T) {
	c := startCore(t)
	c.attend(attentionItem{Ref: "alert:1", Weight: 3, Why: "backup failed", Source: "skill:backup"})
	cs := connectTo(t, newMCPServer(c))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "write_rule", Arguments: map[string]any{
		"key": "alert:source=backup", "delta": -5, "label": "les alertes de sauvegarde", "why": "Sam ne veut plus les voir."}})
	if err != nil || res.IsError {
		t.Fatalf("write_rule: %v %+v", err, res)
	}
	if len(c.attentionRules()) != 0 || attentionOf(c, "alert:1") == nil {
		t.Fatal("in force before the user's yes")
	}
	ids := c.proposedRules()
	if len(ids) != 1 {
		t.Fatalf("proposed %v", ids)
	}
	brainSays(c, "Je ne te montre plus les alertes de sauvegarde ?")
	x := cardOf(t, c, cardShowRule, ids[0])
	x, p := c.liveCard(x)
	if x.State != cardPending || p.Text("fr") != "Ne plus montrer : les alertes de sauvegarde" || p.Text("en") != "Never show: les alertes de sauvegarde" ||
		len(p.Fields) != 1 || p.Fields[0].Value != "alert:source=backup" || p.Why != "Sam ne veut plus les voir." {
		t.Fatalf("card %+v %+v", x, p)
	}
	if attentionOf(c, cardItemRef(x.ID)) == nil {
		t.Fatal("the card is not in the attention queue")
	}
	if _, err := c.decideCard(context.Background(), x.ID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if rs := c.attentionRules(); len(rs) != 1 || rs[0].Delta != ruleNever {
		t.Fatalf("not in force: %+v", rs)
	}
	if attentionOf(c, "alert:1") != nil {
		t.Fatal("the alert is still shown")
	}
	// A bad key is refused with the grammar; a no drops the proposal.
	res, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "write_rule", Arguments: map[string]any{"key": "mail:subject=x", "delta": 1, "label": "x"}})
	if res == nil || !res.IsError || !strings.Contains(fmt.Sprint(res.Content[0]), "kind:attribute=value") {
		t.Fatalf("a bad key: %+v", res)
	}
	c.proposeRule(showRuleIn{Key: "mail", Delta: 1, Label: "les mails"}, "", []string{"mail.read_message"})
	id := c.proposedRules()[0]
	if p := c.proposalOf(cardShowRule, id); !p.Pending || p.Tainted == "" {
		t.Fatalf("a tainted proposal: %+v", p)
	}
	if err := c.decideProposal(context.Background(), cardShowRule, id, false); err != nil {
		t.Fatal(err)
	}
	if len(c.proposedRules()) != 0 || len(c.attentionRules()) != 1 {
		t.Fatal("the rejected proposal stays")
	}
}

// /api/rules: the workspace's session, the CSRF token on writes.
func TestRulesAPI(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	anon := newTestWebFor(t, tw.c)
	if code, _, _ := anon.do("GET", "/api/rules", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", code)
	}
	send := func(method, path, body, token string) (int, string) {
		h := map[string]string{"X-CSRF-Token": token, "Content-Type": "application/json"}
		req, _ := http.NewRequest(method, tw.srv.URL+path, strings.NewReader(body))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		resp, err := tw.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, b.String()
	}
	if code, _ := send("POST", "/api/rules", `{"key":"mail","delta":1}`, "wrong"); code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", code)
	}
	if code, body := send("POST", "/api/rules", `{"key":"mail:subject=x","delta":1}`, csrf); code != http.StatusBadRequest || !strings.Contains(body, "kind:attribute=value") {
		t.Fatalf("a bad key: %d %s", code, body)
	}
	if code, _ := send("POST", "/api/rules", `nope`, csrf); code != http.StatusBadRequest {
		t.Fatalf("not JSON: %d", code)
	}
	code, body := send("POST", "/api/rules", `{"key":"mail:from=example.org","delta":-1,"label":"les mails de example.org","remark":"sauf les factures"}`, csrf)
	var r attentionRule
	if code != http.StatusCreated || json.Unmarshal([]byte(body), &r) != nil || r.ID == 0 || r.Key != "mail:from=example.org" || r.Remark != "sauf les factures" {
		t.Fatalf("created: %d %s", code, body)
	}
	code, body, _ = tw.do("GET", "/api/rules", nil, nil)
	var list rulesOut
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list.Rules) != 1 || list.Rules[0].ID != r.ID || list.Rules[0].Label != "les mails de example.org" {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, _ := send("DELETE", fmt.Sprintf("/api/rules/%d", r.ID), "", "wrong"); code != http.StatusForbidden {
		t.Fatalf("delete without CSRF: %d", code)
	}
	if code, _ := send("DELETE", fmt.Sprintf("/api/rules/%d", r.ID), "", csrf); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := send("DELETE", fmt.Sprintf("/api/rules/%d", r.ID), "", csrf); code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", code)
	}
}

// Settings → Règles d'affichage lists the rules and the brain's proposals,
// removes one and answers the others, in French and English.
func TestDisplaySettings(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	r := ruleNamed(t, tw.c, showRuleIn{Key: "alert:source=backup", Delta: ruleNever, Label: "les alertes de sauvegarde", Remark: "trop souvent"})
	tw.c.proposeRule(showRuleIn{Key: "mail:from=example.org", Delta: 1, Label: "les mails de example.org"}, "Demandé.", nil)
	code, body, _ := tw.do("GET", "/settings/display", nil, nil)
	for _, want := range []string{`<a href="/settings/display" aria-current="page">`, "Règles d&#39;affichage", "Ne plus montrer : les alertes de sauvegarde",
		"alert:source=backup", "trop souvent", "Proposées, à approuver", "Montrer plus en avant : les mails de example.org"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("GET /settings/display: %d, lacks %q", code, want)
		}
	}
	tw.post("/lang", url.Values{"lang": {"en"}}, csrf)
	if _, body, _ := tw.do("GET", "/settings/display", nil, nil); !strings.Contains(body, "Display rules") || !strings.Contains(body, "Never show: les alertes de sauvegarde") {
		t.Error("no English page")
	}
	if _, body, _ := tw.post(fmt.Sprintf("/settings/display/%d/remove", r.ID), nil, csrf); strings.Contains(body, "alert:source=backup") {
		t.Error("not removed")
	}
	id := tw.c.proposedRules()[0]
	if _, body, _ := tw.post(fmt.Sprintf("/settings/display/%d/approve", id), nil, csrf); strings.Contains(body, "Proposed, to approve") || !strings.Contains(body, "Show more: les mails de example.org") {
		t.Errorf("not approved: %s", body)
	}
	if rs := tw.c.attentionRules(); len(rs) != 1 || rs[0].Key != "mail:from=example.org" {
		t.Fatalf("rules %+v", rs)
	}
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The display rules of #402 leave no trace: a database made by a release
// that had them loses their table and their cards when it opens.
func TestDisplayRulesGone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE attention_rules (id INTEGER PRIMARY KEY AUTOINCREMENT, key TEXT NOT NULL, delta INTEGER NOT NULL)`,
		`INSERT INTO attention_rules (key, delta) VALUES ('alert:source=backup', -5)`,
		`INSERT INTO convo_cards (id, ref, kind, created) VALUES (7, 'showrule:1', 'showrule', 1)`,
		`INSERT INTO convo_cards (id, ref, kind, created) VALUES (8, 'memory:1', 'memory', 1)`,
		`INSERT INTO attention (ref, weight, time) VALUES ('card:7', 3, 1), ('card:8', 3, 1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s.db.Close()
	if s, err = openStore(path); err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM attention_rules`).Scan(&n); err == nil {
		t.Fatal("attention_rules still there")
	}
	var kinds []string
	rows, _ := s.db.Query(`SELECT kind FROM convo_cards UNION ALL SELECT ref FROM attention`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		kinds = append(kinds, k)
	}
	rows.Close()
	slices.Sort(kinds)
	if strings.Join(kinds, ",") != "card:8,memory" {
		t.Fatalf("left: %v", kinds)
	}
}

// No route, page nor tool of the rules is left; the brain has the
// workspace's two tools instead.
func TestNoRulesLeft(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	for _, path := range []string{"/api/rules", "/settings/display"} {
		if code, _, _ := tw.do("GET", path, nil, nil); code != http.StatusNotFound {
			t.Errorf("GET %s: %d", path, code)
		}
	}
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); strings.Contains(body, "/settings/display") {
		t.Error("the settings still link the display rules")
	}
	if slices.Contains(proposalKinds, "showrule") {
		t.Error("showrule is still a proposal kind")
	}
	cs := connectTo(t, newMCPServer(tw.c))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	if slices.Contains(names, "write_rule") || !slices.Contains(names, "workspace_show") || !slices.Contains(names, "workspace_walkthrough") {
		t.Fatalf("tools %v", names)
	}
}

// The composer's turn lists the user's workspace preferences from memory,
// and the others the surfaces' words recall; a proposed one waits.
func TestComposerReadsWorkspacePreferences(t *testing.T) {
	c, _ := testMemoryCore(t)
	mustAdd(t, c.mem, &Memory{Kind: kindPreference, Subject: workspaceSubject, Text: "Sam veut toujours voir devant une sauvegarde qui échoue."})
	mustAdd(t, c.mem, &Memory{Kind: kindPreference, Subject: "owner", Text: "Sam ne lit jamais les lettres d'information."})
	mustAdd(t, c.mem, &Memory{Kind: kindPreference, Subject: workspaceSubject, Text: "Sam aime les modules ronds.", Status: memProposed})
	mustAdd(t, c.mem, &Memory{Kind: kindFact, Subject: workspaceSubject, Text: "Sam a un grand écran."})
	c.attend(attentionItem{Ref: "mail:7", Weight: 2, Why: "Lettre d'information de news.example.org", Source: "skill:mail"})

	c.syncQueueSurfaces()
	s := c.poolSnapshot(momentChange, "")
	if len(s.Preferences) != 2 || s.Preferences[0] != "Sam veut toujours voir devant une sauvegarde qui échoue." ||
		s.Preferences[1] != "Sam ne lit jamais les lettres d'information." {
		t.Fatalf("preferences %q", s.Preferences)
	}
	turn := composerTurn(s)
	for _, want := range []string{"Ses préférences", "- Sam veut toujours voir devant une sauvegarde qui échoue.", "- Sam ne lit jamais les lettres d'information."} {
		if !strings.Contains(turn, want) {
			t.Errorf("turn lacks %q:\n%s", want, turn)
		}
	}
	if strings.Contains(turn, "ronds") || strings.Contains(turn, "grand écran") {
		t.Errorf("a proposed preference or a fact in the turn:\n%s", turn)
	}

	// The stamp the composer watches moves when one enters memory, with its words.
	stamp, changed, _ := c.workspacePrefStamp()
	time.Sleep(1100 * time.Millisecond) // updated_at counts seconds
	x := mustAdd(t, c.mem, &Memory{Kind: kindPreference, Subject: workspaceSubject, Text: "Sam ne veut plus les lettres de news.example.org."})
	s2, changed2, words := c.workspacePrefStamp()
	if s2 == stamp || changed2 < changed || words != x.Text {
		t.Fatalf("stamp %q → %q, %d → %d, %q", stamp, s2, changed, changed2, words)
	}
	c.mem.forget(x.ID, "owner", "")
	if s3, _, _ := c.workspacePrefStamp(); s3 == s2 {
		t.Fatal("forgetting one did not move the stamp")
	}
}

// A workspace preference is remembered whatever its words: « ne me montre
// plus de notifications de backup » is about the workspace, not a rule.
func TestRememberWorkspacePreference(t *testing.T) {
	c, _ := testMemoryCore(t)
	seq := ownerSays(c, "Sur l'espace, plus de notifications de sauvegarde")
	out, err := c.brainRemember(rememberIn{Kind: kindPreference, Subject: workspaceSubject,
		Text: "Sam ne veut plus voir les notifications de sauvegarde sur son espace.", SourceRef: "msg:" + strconv.Itoa(seq)})
	if err != nil || out["status"] != memActive {
		t.Fatalf("remember: %v %v", out, err)
	}
	if _, err := c.brainRemember(rememberIn{Kind: kindPreference, Text: "Ne me notifie plus la nuit", SourceRef: "msg:" + strconv.Itoa(seq)}); err == nil {
		t.Fatal("a notification preference outside the workspace was remembered")
	}
}

// workspace_show puts objects in front at once: every snapshot holds them,
// queued or not, and the old canvas's composition shows them as modules
// first; the composer is told; none gives the front back.
func TestWorkspaceShow(t *testing.T) {
	c := startCore(t)
	for i, w := range []int{4, 4, 3, 3, 3, 2, 2} {
		c.attend(attentionItem{Ref: "session:" + strconv.Itoa(i+1), Weight: w, Why: "étape"})
	}
	cs := connectTo(t, newMCPServer(c))
	call := func(args map[string]any) *mcp.CallToolResult {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "workspace_show", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(map[string]any{"refs": []string{"nope:1"}}); !res.IsError {
		t.Fatal("a bad ref accepted")
	}
	_, seq := c.shown(time.Now())
	if res := call(map[string]any{"refs": []string{"mail:INBOX/9", "session:7"}, "why": "Le notaire attend"}); res.IsError {
		t.Fatalf("workspace_show: %+v", res)
	}
	show, seq2 := c.shown(time.Now())
	if show == nil || seq2 == seq || !slices.Equal(show.Refs, []string{"mail:INBOX/9", "session:7"}) {
		t.Fatalf("shown %+v", show)
	}
	s := c.compositionSnapshot(momentShow, show.Why, false)
	if it, ok := s.item("mail:INBOX/9"); !ok || it.Weight != weightBlocksATask || it.Why != "Le notaire attend" || it.Source != "brain" {
		t.Fatalf("snapshot %+v", s.Items)
	}
	fb := fallbackComposition(s)
	if len(fb.Items) != compositionItemsMax || fb.Items[0].Ref != "mail:INBOX/9" || fb.Items[1].Ref != "session:7" || fb.Items[1].Weight != weightBlocksATask {
		t.Fatalf("fallback %s", refsOf(fb.Items))
	}
	if turn := composerTurn(c.poolSnapshot(momentShow, show.Why)); !strings.Contains(turn, "on met des objets devant pour lui : « Le notaire attend »") {
		t.Errorf("turn:\n%s", turn)
	}
	// Gone after its time, or when the brain gives the front back.
	if sh, _ := c.shown(time.Now().Add(workspaceShowFor + time.Minute)); sh != nil {
		t.Fatal("still shown past its time")
	}
	c.showOnWorkspace([]string{"session:1"}, "", time.Now())
	call(map[string]any{"refs": []string{}})
	if sh, _ := c.shown(time.Now()); sh != nil {
		t.Fatal("still shown after an empty call")
	}
}

// A message written with a module open carries its ref to the brain and to
// the extraction; what the user does there are gestures the extraction
// reads; a walkthrough the brain starts reaches the page on the stream.
func TestWorkspaceWeb(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()

	if code, _, _ := tw.post("/messages", url.Values{"text": {"montre-m'en moins"}, "about": {"mail:INBOX/7"}}, csrf); code != http.StatusNoContent {
		t.Fatalf("send: %d", code)
	}
	tw.post("/messages", url.Values{"text": {"et ça ?"}, "about": {"nope"}}, csrf)
	var seqs []int
	rows, _ := tw.c.store.db.Query(`SELECT seq FROM messages WHERE role = ? ORDER BY seq`, roleUser)
	for rows.Next() {
		var n int
		rows.Scan(&n)
		seqs = append(seqs, n)
	}
	rows.Close()
	if len(seqs) < 2 || tw.c.messageAbout(seqs[len(seqs)-2]) != "mail:INBOX/7" || tw.c.messageAbout(seqs[len(seqs)-1]) != "" {
		t.Fatalf("about: %v", seqs)
	}

	gesture := func(body string) int {
		req, _ := http.NewRequest("POST", tw.srv.URL+"/api/espace/gesture", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := tw.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	for body, want := range map[string]int{
		`{"what":"opened","ref":"mail:INBOX/7","title":"Les nouveautés"}`: http.StatusNoContent,
		`{"what":"ignored","ref":"alert:3","title":"Sauvegarde"}`:         http.StatusNoContent,
		`{"what":"liked","ref":"alert:3"}`:                                http.StatusBadRequest,
		`{"what":"opened","ref":"nope"}`:                                  http.StatusBadRequest,
	} {
		if code := gesture(body); code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}
	gesture(`{"what":"opened","ref":"mail:INBOX/7","title":"Les nouveautés"}`) // the same again: kept once
	segs, err := tw.c.transcript(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var owner, ws []segment
	for _, s := range segs {
		switch s.Who {
		case "owner":
			owner = append(owner, s)
		case "workspace":
			ws = append(ws, s)
		}
	}
	if len(ws) != 1 || ws[0].Trust != trustExternal || !strings.HasPrefix(ws[0].Ref, "workspace:") ||
		strings.Count(ws[0].Text, "opened mail:INBOX/7 \"Les nouveautés\"") != 1 || !strings.Contains(ws[0].Text, "left unopened") {
		t.Fatalf("gestures %+v", ws)
	}
	if !slices.ContainsFunc(owner, func(s segment) bool {
		return strings.Contains(s.Text, "montre-m'en moins\n[written about mail:INBOX/7, open on the workspace]")
	}) {
		t.Fatalf("owner segments %+v", owner)
	}

	// workspace_walkthrough reaches an open page.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events", nil)
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cs := connectTo(t, newMCPServer(tw.c))
	go func() {
		time.Sleep(300 * time.Millisecond)
		cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "workspace_walkthrough", Arguments: map[string]any{"refs": []string{"mail:INBOX/7", "agenda:2026-11-12"}}})
	}()
	sc := bufio.NewScanner(resp.Body)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok && event == "walkthrough" {
			var w workspaceWalk
			if json.Unmarshal([]byte(data), &w) != nil || !slices.Equal(w.Refs, []string{"mail:INBOX/7", "agenda:2026-11-12"}) {
				t.Fatalf("walkthrough %q", data)
			}
			return
		}
	}
	t.Fatalf("no walkthrough event: %v", sc.Err())
}

// A stream opened just after a walkthrough was asked still gets it; one
// opened later does not.
func TestWalkSeenAt(t *testing.T) {
	c := startCore(t)
	now := time.Now()
	if c.walkSeenAt(now) != 0 {
		t.Fatal("a walk before any")
	}
	w, _ := c.walkWorkspace(nil, now)
	if c.walkSeenAt(now.Add(time.Second)) != w.ID-1 || c.walkSeenAt(now.Add(time.Minute)) != w.ID {
		t.Fatal("replay window")
	}
	if _, err := c.walkWorkspace(manyRefs(workspaceWalkMax+1), now); err == nil {
		t.Fatal("too many refs accepted")
	}
}

func manyRefs(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, "session:"+strconv.Itoa(i))
	}
	return out
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func newMemoryWeb(t *testing.T) (*testWeb, string) {
	t.Helper()
	c, _ := testMemoryCore(t)
	tw := newTestWebFor(t, c)
	return tw, tw.setup()
}

func TestMemoryPageRenders(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	m := tw.c.mem
	mustAdd(t, m, &Memory{Kind: kindProfile, Text: "Sam est développeur"})
	mustAdd(t, m, &Memory{Kind: kindPerson, Subject: "person:hélène", Text: "Hélène, la sœur de Sam <script>alert(1)</script>"})
	mustAdd(t, m, &Memory{Kind: kindEpisode, Subject: "journal", Text: "Journée calme"})
	mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Payer le loyer", DueAt: m.now().Add(-1)})
	seq, _ := tw.c.store.addMessage(roleUser, "Le colis arrive jeudi", true)
	mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, SourceKind: srcExtraction,
		SourceRef: "msg:" + strconv.Itoa(int(seq)), Trust: trustExternal, Flags: "imperative"})
	for path, want := range map[string][]string{
		"/memory":                  {"Mémoire", "Hélène, la sœur de Sam &lt;script&gt;", "Sam est développeur", `aria-current="page"`, "/memory/export.jsonl"},
		"/memory?q=helene":         {"Hélène"},
		"/memory?kind=person":      {"Hélène"},
		"/memory?view=inbox":       {"Le colis arrive jeudi", "Approuver", "ressemble à une consigne", "/#m" + strconv.Itoa(int(seq))},
		"/memory?view=journal":     {"Aujourd&#39;hui", "Journée calme", "Extraire aujourd&#39;hui"},
		"/memory?view=commitments": {"Payer le loyer", "En retard"},
		"/memory?view=profile":     {"Sam est développeur", "/ 2500"},
	} {
		code, body, _ := tw.do("GET", path, nil, nil)
		if code != 200 {
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s lacks %q", path, w)
			}
		}
	}
	code, body, _ := tw.do("GET", "/memory?q=helene", nil, nil)
	if code != 200 || strings.Contains(body, "Payer le loyer") {
		t.Fatal("search shows everything")
	}
	// English.
	tw.hc.Jar.SetCookies(mustURL(tw.srv.URL), []*http.Cookie{{Name: "lang", Value: "en"}})
	if _, body, _ := tw.do("GET", "/memory?view=inbox", nil, nil); !strings.Contains(body, "To approve") || !strings.Contains(body, "Approve") {
		t.Fatal("no English")
	}
	// The export.
	code, body, h := tw.do("GET", "/memory/export.jsonl", nil, nil)
	if code != 200 || !strings.Contains(h.Get("Content-Disposition"), "attachment") || strings.Count(body, "\n") != 5 {
		t.Fatalf("export: %d %q", code, body)
	}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }

func TestMemoryPageNeedsLogin(t *testing.T) {
	tw, _ := newMemoryWeb(t)
	tw.hc.Jar, _ = cookiejar.New(nil)
	code, _, h := tw.do("GET", "/memory", nil, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Fatalf("anonymous: %d %s", code, h.Get("Location"))
	}
}

func TestMemoryActionsCSRF(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	m := tw.c.mem
	x := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Un fait"})
	path := "/memory/" + strconv.FormatInt(x.ID, 10) + "/forget"
	if code, _, _ := tw.post(path, url.Values{}, "wrong"); code != http.StatusForbidden {
		t.Fatalf("bad CSRF: %d", code)
	}
	if code, _, _ := tw.do("POST", path, url.Values{}, map[string]string{"X-CSRF-Token": csrf, "Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("other origin: %d", code)
	}
	if got, _ := m.memory(x.ID); got.Status != memActive {
		t.Fatal("forgotten without CSRF")
	}
	if code, _, h := tw.post(path, url.Values{}, csrf); code != 200 || !strings.Contains(h.Get("HX-Trigger"), `Oubli\u00e9`) {
		t.Fatalf("forget: %d %v", code, h)
	}
	if got, _ := m.memory(x.ID); got.Status != memDeleted {
		t.Fatal("not forgotten")
	}
}

func TestMemoryActions(t *testing.T) {
	tw, csrf := newMemoryWeb(t)
	m := tw.c.mem
	id := func(x *Memory) string { return strconv.FormatInt(x.ID, 10) }
	// Add, as me.
	code, body, _ := tw.post("/memory", url.Values{"text": {"Le NAS est au grenier"}, "kind": {"fact"}, "subject": {"Home"}, "links": {"[[NAS]]\n"}}, csrf)
	if code != 200 || !strings.Contains(body, "Le NAS est au grenier") {
		t.Fatalf("add: %d %s", code, body)
	}
	xs, _ := m.recall(recallQuery{Query: "grenier"})
	if len(xs) != 1 || xs[0].Trust != trustOwner || xs[0].Subject != "home" || xs[0].Links[0] != "[[NAS]]" {
		t.Fatalf("added: %+v", xs)
	}
	x := xs[0]
	// The scanner refuses secrets, even mine.
	if _, _, h := tw.post("/memory", url.Values{"text": {"mot de passe: hunter2hunter2"}, "kind": {"fact"}}, csrf); !strings.Contains(h.Get("HX-Trigger"), "credential") {
		t.Fatalf("secret added: %v", h)
	}
	// Edit in place.
	code, body, _ = tw.post("/memory/"+id(x), url.Values{"text": {"Le NAS est à la cave"}, "kind": {"fact"}, "subject": {"home"}}, csrf)
	if got, _ := m.memory(x.ID); code != 200 || got.Text != "Le NAS est à la cave" || !strings.Contains(body, "à la cave") {
		t.Fatalf("edit: %d %+v", code, got)
	}
	if code, body, _ = tw.post("/memory/"+id(x), url.Values{"text": {""}, "kind": {"fact"}}, csrf); !strings.Contains(body, `class="alert`) {
		t.Fatalf("empty edit: %d %s", code, body)
	}
	if code, body, _ = tw.do("GET", "/memory/"+id(x)+"/edit", nil, nil); code != 200 || !strings.Contains(body, "<textarea") {
		t.Fatalf("edit form: %d", code)
	}
	// Pin, within the budget.
	tw.post("/memory/"+id(x)+"/pin", url.Values{"pin": {"1"}}, csrf)
	if got, _ := m.memory(x.ID); !got.Pinned {
		t.Fatal("not pinned")
	}
	mustAdd(t, m, &Memory{Kind: kindProfile, Text: strings.Repeat("y", 1500)})
	big := mustAdd(t, m, &Memory{Kind: kindFact, Text: strings.Repeat("x", 1000)})
	if _, _, h := tw.post("/memory/"+id(big)+"/pin", url.Values{"pin": {"1"}}, csrf); !strings.Contains(h.Get("HX-Trigger"), "profile is full") {
		t.Fatalf("over budget: %v", h)
	}
	// Obsolete, and back.
	tw.post("/memory/"+id(x)+"/obsolete", url.Values{}, csrf)
	if got, _ := m.memory(x.ID); got.Current(m.now().Add(1)) {
		t.Fatal("still current")
	}
	tw.post("/memory/"+id(x)+"/obsolete", url.Values{}, csrf)
	if got, _ := m.memory(x.ID); !got.Current(m.now()) {
		t.Fatal("not current again")
	}
	// History.
	if code, body, _ = tw.do("GET", "/memory/"+id(x)+"/history", nil, nil); code != 200 || !strings.Contains(body, "Modifié") || !strings.Contains(body, "<del>") {
		t.Fatalf("history: %d %s", code, body)
	}
	// A commitment.
	cm := mustAdd(t, m, &Memory{Kind: kindCommitment, Text: "Appeler le garage"})
	tw.post("/memory/"+id(cm)+"/state", url.Values{"state": {"done"}}, csrf)
	if got, _ := m.memory(cm.ID); got.State != stateDone {
		t.Fatal("not done")
	}
	if code, _, _ = tw.post("/memory/"+id(x)+"/state", url.Values{"state": {"done"}}, csrf); code != http.StatusBadRequest {
		t.Fatalf("state of a fact: %d", code)
	}
	// Approve and reject proposals.
	old := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive mardi"})
	p1 := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Le colis arrive jeudi", Status: memProposed, Supersedes: old.ID, SourceKind: srcExtraction, Trust: trustExternal})
	p2 := mustAdd(t, m, &Memory{Kind: kindFact, Text: "Une rumeur", Status: memProposed, SourceKind: srcExtraction, Trust: trustExternal})
	if got, _ := m.memory(old.ID); got.SupersededBy != 0 {
		t.Fatal("a proposal superseded before approval")
	}
	tw.post("/memory/"+id(p1)+"/approve", url.Values{}, csrf)
	tw.post("/memory/"+id(p2)+"/reject", url.Values{}, csrf)
	if got, _ := m.memory(p1.ID); got.Status != memActive {
		t.Fatal("not approved")
	}
	if got, _ := m.memory(old.ID); got.SupersededBy != p1.ID || got.ValidTo.IsZero() {
		t.Fatalf("approval did not supersede: %+v", got)
	}
	if got, _ := m.memory(p2.ID); got.Status != memDeleted {
		t.Fatal("not rejected")
	}
	if _, _, h := tw.post("/memory/"+id(p1)+"/reject", url.Values{}, csrf); !strings.Contains(h.Get("HX-Trigger"), "error") {
		t.Fatal("rejected an active memory")
	}
	// Restore what was rejected.
	tw.post("/memory/"+id(p2)+"/restore", url.Values{}, csrf)
	if got, _ := m.memory(p2.ID); got.Status != memActive {
		t.Fatal("not restored")
	}
	if code, _, _ = tw.post("/memory/99999/forget", url.Values{}, csrf); code != http.StatusNotFound {
		t.Fatalf("unknown memory: %d", code)
	}
}

func TestHXTriggerIsASCII(t *testing.T) {
	h := hxTrigger("Épinglé « ok » 🎉\x01", false, true)
	for _, r := range h {
		if r >= 0x80 {
			t.Fatalf("non-ASCII in %s", h)
		}
	}
	var v struct {
		Toast struct{ Message, Kind string }
	}
	if err := json.Unmarshal([]byte(h), &v); err != nil || v.Toast.Message != "Épinglé « ok » 🎉\x01" || v.Toast.Kind != "error" {
		t.Fatalf("%s: %+v %v", h, v, err)
	}
}

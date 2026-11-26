package nodefile

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseOn(t *testing.T) {
	for _, c := range []struct {
		target string
		want   On
		ok     bool
	}{
		{"monitor.changed/pea", On{"monitor.changed", "pea", "value"}, true},
		{"monitor.changed/pea#value.total", On{"monitor.changed", "pea", "value.total"}, true},
		{"task.ended", On{"task.ended", "", "value"}, true},
		{"node.gesture/node:a1#gesture", On{"node.gesture", "node:a1", "gesture"}, true},
		{"monitor/pea", On{}, false},          // a kind is two words at least
		{"monitor.changed/pea#", On{}, false}, // a field named, empty
		{"Monitor.Changed/pea", On{}, false},
		{"", On{}, false},
	} {
		got, ok := ParseOn(c.target)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseOn(%q) = %+v, %v; want %+v, %v", c.target, got, ok, c.want, c.ok)
		}
	}
}

func newPage(t *testing.T, body string) *Node {
	t.Helper()
	n, err := New(Meta{ID: "a1", Title: "Placements", Created: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}, body)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func bodyOf(t *testing.T, n *Node) string {
	t.Helper()
	b, err := n.Body()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var stampAt = time.Date(2026, 11, 26, 10, 5, 0, 0, time.UTC)

func TestHearRewritesTheNumberKeepsTheUnit(t *testing.T) {
	n := newPage(t, "# Comptes\n\n- PEA : [18 385 €](on:monitor.changed/pea) · relu le 25/11 à 9 h 00\n- Livret : [1 200,50 €](on:monitor.changed/livret)\n")
	ev := Heard{Kind: "monitor.changed", Key: "pea", Payload: map[string]any{"value": 18412.7}, Stamp: ReadStamp(stampAt, "fr")}
	got, err := n.Hear(ev)
	if err != nil || got != 1 {
		t.Fatalf("Hear = %d, %v", got, err)
	}
	want := "# Comptes\n\n- PEA : [18 413 €](on:monitor.changed/pea) · relu le 26/11 à 10 h 05\n- Livret : [1 200,50 €](on:monitor.changed/livret)\n"
	if b := bodyOf(t, n); b != want {
		t.Errorf("body:\n%s\nwant:\n%s", b, want)
	}
	// Another key: the livret, its decimals kept, a date added.
	ev = Heard{Kind: "monitor.changed", Key: "livret", Payload: map[string]any{"value": "1250.5"}, Stamp: ReadStamp(stampAt, "en")}
	if got, _ := n.Hear(ev); got != 1 {
		t.Fatalf("Hear = %d", got)
	}
	if b := bodyOf(t, n); !strings.Contains(b, "- Livret : [1 250,50 €](on:monitor.changed/livret) · read on Nov 26 at 10:05\n") {
		t.Errorf("body:\n%s", b)
	}
	// An event no line follows, and one without the field: nothing.
	for _, ev := range []Heard{{Kind: "monitor.new", Key: "pea", Payload: map[string]any{"value": 1.0}},
		{Kind: "monitor.changed", Key: "pea", Payload: map[string]any{"was": 1.0}}} {
		if got, _ := n.Hear(ev); got != 0 {
			t.Errorf("%+v rewrote %d lines", ev, got)
		}
	}
}

func TestRelabel(t *testing.T) {
	for _, c := range []struct {
		label string
		v     any
		want  string
		ok    bool
	}{
		{"18 385 €", 18385.4, "18 385 €", true},
		{"18 385 €", 1234567.0, "1 234 567 €", true},
		{"385 €", 18385.0, "18 385 €", true},  // a short number: grouped as a page writes it
		{"18385 €", 20000.0, "20000 €", true}, // written in one piece: kept so
		{"91,5 %", 7.26, "7,3 %", true},       // the author's decimals
		{"3.5 GB", 4.26, "4.3 GB", true},
		{"1 200,50 €", 1250.5, "1 250,50 €", true}, // grouped as the author did
		{"−2 °C", -3.0, "−3 °C", true},
		{"+2 %", -0.4, "0 %", true},
		{"+2 %", 3.0, "+3 %", true},
		{"ok", "en panne", "en panne", true},
		{"ok", "a [link](do:mail/trash) `x` <b>|", "a linkdo:mail/trash x b", true},
		{"ok", "rdv 📅 2026-12-01", "rdv 2026-12-01", true},
		{"ok", strings.Repeat("a", 200), strings.Repeat("a", OnTextMax-1) + "…", true},
		{"ok", "  \n ", "", false},
		{"ok", []any{1.0}, "", false},
		{"ok", map[string]any{"a": 1.0}, "", false},
		{"ok", true, "", false},
		{"—", 12.5, "12,5", true},
	} {
		got, ok := relabel(c.label, c.v, false)
		if ok != c.ok || got != c.want {
			t.Errorf("relabel(%q, %v) = %q, %v; want %q, %v", c.label, c.v, got, ok, c.want, c.ok)
		}
	}
	if _, ok := relabel("ok", "en panne", true); ok {
		t.Error("a text written where only numbers go")
	}
}

func TestHearMarksTheCalloutOfAnExternalEvent(t *testing.T) {
	body := "> [!figures|aside] Prod\n> - Erreurs : [12](on:monitor.changed/prod#value.errors)\n> - État : [ok](on:monitor.changed/prod#value.state)\n\n- Ailleurs : [ok](on:monitor.changed/prod#value.state)\n"
	n := newPage(t, body)
	ev := Heard{Kind: "monitor.changed", Key: "prod", External: true,
		Payload: map[string]any{"value": map[string]any{"errors": 40.0, "state": "dégradé"}}}
	if got, _ := n.Hear(ev); got != 2 {
		t.Fatalf("Hear = %d, want 2 (out of a callout, an external text is not written)", got)
	}
	b := bodyOf(t, n)
	for _, want := range []string{"> [!figures|aside|external] Prod\n", "[40](on:", "[dégradé](on:monitor.changed/prod#value.state)\n\n", "- Ailleurs : [ok](on:"} {
		if !strings.Contains(b, want) {
			t.Errorf("body lacks %q:\n%s", want, b)
		}
	}
	if !n.CalloutExternal() {
		t.Error("CalloutExternal: false")
	}
	// A trusted event writes its text out of the callout, and leaves the
	// mark: words around a number may still be someone else's.
	ev.External = false
	n.Hear(ev)
	if b := bodyOf(t, n); !strings.Contains(b, "> [!figures|aside|external] Prod\n") || !strings.Contains(b, "- Ailleurs : [dégradé]") {
		t.Errorf("body:\n%s", b)
	}
}

// The date after a link is its own: what follows it on the line (a
// table's cells, words) stays, event after event.
func TestHearKeepsWhatFollowsTheDate(t *testing.T) {
	n := newPage(t, "| Compte | Valeur | Part |\n|---|---|---|\n| PEA | [18 385 €](on:monitor.changed/pea) | 3 % |\n\nLe PEA vaut [18 385 €](on:monitor.changed/pea) · relu le 25/11 à 9 h, à peu près.\n")
	for i, v := range []float64{18412, 18500} {
		n.Hear(Heard{Kind: "monitor.changed", Key: "pea", Payload: map[string]any{"value": v}, Stamp: ReadStamp(stampAt.Add(time.Duration(i)*time.Hour), "fr")})
	}
	want := "| Compte | Valeur | Part |\n|---|---|---|\n| PEA | [18 500 €](on:monitor.changed/pea) · relu le 26/11 à 11 h 05 | 3 % |\n\nLe PEA vaut [18 500 €](on:monitor.changed/pea) · relu le 26/11 à 11 h 05, à peu près.\n"
	if b := bodyOf(t, n); b != want {
		t.Errorf("body:\n%s\nwant:\n%s", b, want)
	}
	n = newPage(t, "[1](on:monitor.changed/a) · read on Nov 2 at 9:05 · [ok](on:monitor.changed/b)\n")
	n.Hear(Heard{Kind: "monitor.changed", Key: "a", Payload: map[string]any{"value": 2.0}, Stamp: ReadStamp(stampAt, "en")})
	if b := bodyOf(t, n); b != "[2](on:monitor.changed/a) · read on Nov 26 at 10:05 · [ok](on:monitor.changed/b)\n" {
		t.Errorf("body:\n%s", b)
	}
}

// What the authors wrote leaves out the values and the marks code
// writes: two versions that differ by them only are the same authors'.
func TestAuthorBytesLeaveTheValuesOut(t *testing.T) {
	a := newPage(t, "> [!figures]\n> - PEA : [18 385 €](on:monitor.changed/pea)\n\n```\n[1](on:monitor.changed/pea) · relu le 1/1\n```\n")
	b := newPage(t, "> [!figures|external]\n> - PEA : [18 412 €](on:monitor.changed/pea) · relu le 26/11 à 10 h 05\n\n```\n[1](on:monitor.changed/pea) · relu le 1/1\n```\n")
	ra, _ := a.AuthorBytes()
	rb, _ := b.AuthorBytes()
	if string(ra) != string(rb) {
		t.Errorf("AuthorBytes differ:\n%s\n%s", ra, rb)
	}
	c := newPage(t, "> [!figures]\n> - CTO : [18 385 €](on:monitor.changed/pea)\n\n```\n[1](on:monitor.changed/pea) · relu le 1/1\n```\n")
	if rc, _ := c.AuthorBytes(); string(rc) == string(ra) {
		t.Error("the author's words left out too")
	}
}

func TestAuthorKeepsCodesMarks(t *testing.T) {
	marked := "> [!figures|external]\n> - A : [1](on:monitor.changed/a)\n"
	for _, c := range []struct {
		name, was, now string
		refused        bool
	}{
		{"mark kept", marked, marked + "\nMore.\n", false},
		{"mark taken off", marked, "> [!figures]\n> - A : [1](on:monitor.changed/a)\n", true},
		{"line moved out", marked, "> [!figures|external]\n\n- A : [1](on:monitor.changed/a)\n", true},
		{"callout gone", marked, "Nothing here.\n", false},
		{"a line added in it", marked, "> [!figures|external]\n> - A : [1](on:monitor.changed/a)\n> - B : [1](on:monitor.changed/b)\n", false},
		{"mark made", "> [!figures]\n> - A : [1](on:monitor.changed/a)\n", marked, true},
		{"mark made on nothing", "Text.\n", "> [!note|external]\n> Hi.\n", true},
	} {
		n := newPage(t, c.was)
		err := n.AuthorBody(c.now)
		if refused := err != nil; refused != c.refused {
			t.Errorf("%s: err %v, refused want %v", c.name, err, c.refused)
		}
		if err != nil && !errors.Is(err, ErrCodeField) {
			t.Errorf("%s: %v is not ErrCodeField", c.name, err)
		}
	}
	// Code writes it.
	n := newPage(t, "Text.\n")
	if err := n.CodeBody(marked); err != nil || bodyOf(t, n) != marked {
		t.Errorf("CodeBody: %v", err)
	}
}

func TestKeepHeard(t *testing.T) {
	was := "- PEA : [18 385 €](on:monitor.changed/pea) · relu le 25/11 à 9 h 00\n- Livret : [1 200 €](on:monitor.changed/livret)\n"
	now := "- PEA : [18 413 €](on:monitor.changed/pea) · relu le 26/11 à 10 h 05\n- Livret : [1 250 €](on:monitor.changed/livret) · relu le 26/11 à 10 h 05\n"
	// The author wrote on the version read: the PEA line as it was, the
	// livret changed by hand, a line added.
	written := "Mes comptes.\n\n- PEA : [18 385 €](on:monitor.changed/pea) · relu le 25/11 à 9 h 00\n- Livret : [900 €](on:monitor.changed/livret)\n"
	want := "Mes comptes.\n\n- PEA : [18 413 €](on:monitor.changed/pea) · relu le 26/11 à 10 h 05\n- Livret : [900 €](on:monitor.changed/livret)\n"
	if got := KeepHeard(was, now, written); got != want {
		t.Errorf("KeepHeard:\n%s\nwant:\n%s", got, want)
	}
	if got := KeepHeard(was, was, written); got != written {
		t.Errorf("nothing heard since: %q", got)
	}
}

func TestLineGlance(t *testing.T) {
	n := newPage(t, "> [!figures|glance 3%]\n> - PEA : [18 385 €](on:monitor.changed/pea)\n")
	g, ok := n.LineGlance()
	if !ok || g.Value != 18385 || g.Glance.Label != "PEA" || g.Glance.Threshold != 3 || g.Glance.Unit != GlancePercent || g.External || g.Target != "monitor.changed/pea" {
		t.Errorf("LineGlance = %+v, %v", g, ok)
	}
	n = newPage(t, "> [!figures|external|glance 500 previous]\n> [12 000](on:monitor.changed/conso)\n")
	g, ok = n.LineGlance()
	if !ok || g.Glance.Label != "Placements" || g.Glance.Unit != GlanceAmount || g.Glance.Compare != GlancePrevious || !g.External {
		t.Errorf("LineGlance = %+v, %v", g, ok)
	}
	// The property, on the page's first number that follows an event.
	n = newPage(t, "- État : [ok](on:monitor.changed/a)\n- [ ] Erreurs : [4](on:monitor.changed/b)\n")
	if _, ok := n.LineGlance(); ok {
		t.Error("a glance without its threshold")
	}
	if err := n.Meta.SetProp("glance", "10%"); err != nil {
		t.Fatal(err)
	}
	g, ok = n.LineGlance()
	if !ok || g.Value != 4 || g.Glance.Label != "Erreurs" || g.Glance.Threshold != 10 {
		t.Errorf("LineGlance = %+v, %v", g, ok)
	}
}

func TestCheckOnLinksAndOptions(t *testing.T) {
	p := ReadPage("> [!figures|external|glance 3%]\n> [1](on:monitor.changed/a)\n\n> [!note|glance often]\n> [x](on:monitor)\n")
	var codes []string
	for _, pr := range p.Check(nil) {
		codes = append(codes, pr.Code)
	}
	if strings.Join(codes, " ") != "callout-option on-target" {
		t.Errorf("problems: %v", codes)
	}
}

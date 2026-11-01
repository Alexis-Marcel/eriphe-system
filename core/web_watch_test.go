package main

import (
	"net/url"
	"strings"
	"testing"
)

// Settings: watches are listed with their runs, added, edited, approved,
// paused, resumed and deleted, in French and English.
func TestWatchRoutes(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	c := tw.c
	p, err := c.addWatch(&watch{Name: "Revue du dimanche", Source: sourceSchedule, Spec: watchSpec{Schedule: "sunday 18:00"},
		Prompt: "Fais la revue de la semaine", State: watchProposed, By: "brain", Tainted: "mail.read"})
	if err != nil {
		t.Fatal(err)
	}
	code, body, _ := tw.do("GET", "/settings/watches", nil, nil)
	if code != 200 || !strings.Contains(body, "Revue du dimanche") || !strings.Contains(body, "à approuver") ||
		!strings.Contains(body, "Après un contenu extérieur") {
		t.Fatalf("page %d", code)
	}
	h := map[string]string{"X-CSRF-Token": csrf}
	if code, body, _ := tw.do("POST", "/watches/"+idText(p.ID)+"/approve", url.Values{}, h); code != 200 || !strings.Contains(body, "Active") {
		t.Fatalf("approve %d %s", code, body)
	}
	form := url.Values{"name": {"Point au retour"}, "source": {"retour"}, "after": {"4h"}, "prompt": {"Fais le point"}}
	if code, body, _ := tw.do("POST", "/watches", form, h); code != 200 || !strings.Contains(body, "Point au retour") {
		t.Fatalf("add %d %s", code, body)
	}
	ws, _ := c.watches()
	if len(ws) != 2 || ws[1].returnAfter().Hours() != 4 || ws[1].By != "owner" {
		t.Fatalf("watches %+v", ws)
	}
	form = url.Values{"id": {idText(ws[1].ID)}, "name": {"Point au retour"}, "source": {"retour"}, "after": {"1h30"}, "prompt": {"Le point"}}
	if code, _, _ := tw.do("POST", "/watches", form, h); code != 200 {
		t.Fatalf("edit %d", code)
	}
	if w, _ := c.watch(ws[1].ID); w.Spec.After != 90 || w.Prompt != "Le point" {
		t.Fatalf("edited %+v", w)
	}
	tw.do("POST", "/watches", url.Values{"name": {"x"}, "source": {"horaire"}, "schedule": {"bientôt"}, "prompt": {"y"}}, h)
	if ws, _ := c.watches(); len(ws) != 2 {
		t.Fatal("a bad schedule was kept")
	}
	tw.do("POST", "/watches/"+idText(ws[1].ID)+"/pause", url.Values{}, h)
	if w, _ := c.watch(ws[1].ID); w.State != watchPaused {
		t.Fatalf("pause %s", w.State)
	}
	tw.do("POST", "/watches/"+idText(ws[1].ID)+"/resume", url.Values{}, h)
	if code, _, _ := tw.do("POST", "/watches/"+idText(p.ID)+"/delete", url.Values{}, h); code != 200 {
		t.Fatalf("delete %d", code)
	}
	if ws, _ := c.watches(); len(ws) != 1 || ws[0].State != watchActive {
		t.Fatalf("after delete %+v", ws)
	}
}

// Every word of the watches is in both languages.
func TestWatchStringsBothLanguages(t *testing.T) {
	for k := range watchStrings["fr"] {
		if _, ok := watchStrings["en"][k]; !ok {
			t.Errorf("%s: no English", k)
		}
	}
	for k := range watchStrings["en"] {
		if _, ok := watchStrings["fr"][k]; !ok {
			t.Errorf("%s: no French", k)
		}
	}
}

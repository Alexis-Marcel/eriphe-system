package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// openLayout reads the "layout" events of the workspace's event stream,
// and the order of the events: an a2ui event comes before the layout that
// names its surfaces.
func openLayout(t *testing.T, tw *testWeb) (<-chan workspaceLayout, <-chan string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", tw.srv.URL+"/events?a2ui=1", nil)
	resp, err := tw.hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	layouts, order := make(chan workspaceLayout, 100), make(chan string, 400)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
				if event == "a2ui" || event == "layout" {
					order <- event
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "layout" {
				var l workspaceLayout
				if json.Unmarshal([]byte(data), &l) == nil {
					layouts <- l
				}
			}
		}
		close(layouts)
	}()
	return layouts, order
}

func nextLayout(t *testing.T, ch <-chan workspaceLayout, ok func(workspaceLayout) bool) workspaceLayout {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l, open := <-ch:
			if !open {
				t.Fatal("stream closed")
			}
			if ok(l) {
				return l
			}
		case <-deadline:
			t.Fatal("no such layout")
		}
	}
}

// The workspace's stream sends the layout after the surfaces, and again at
// once when it changes; opening a surface (a tile or a chip) places it
// first and keeps the gesture; back gives the layout back.
func TestWebLayout(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	textSurface(t, tw.c, "first", "Un")
	layouts, order := openLayout(t, tw)
	if first, second := <-order, <-order; first != "a2ui" || second != "layout" {
		t.Fatalf("events %s, %s", first, second)
	}
	l := nextLayout(t, layouts, func(workspaceLayout) bool { return true })
	if len(l.Items) != 1 || l.Items[0].ID != "first" || l.Items[0].Title != "Titre first" || l.Items[0].W == 0 {
		t.Fatalf("layout %+v", l)
	}

	// An urgent item enters the queue: its surface is in front at once.
	tw.c.attend(attentionItem{Ref: "alert:3", Weight: 5, Why: "Panne", Say: "Le serveur ne répond plus."})
	tw.c.syncQueueSurfaces()
	nextLayout(t, layouts, func(l workspaceLayout) bool {
		return len(l.Items) == 2 && l.Items[0].ID == "q-alert-3" && l.Items[0].Size == sizeXL && l.Ambient == "urgent"
	})

	// Opened: first, and a gesture about the surface.
	if code, body := postJSON(tw, "/api/espace/action", actionBody("open", "first"), csrf); code != http.StatusNoContent {
		t.Fatalf("open: %d %s", code, body)
	}
	nextLayout(t, layouts, func(l workspaceLayout) bool { return l.Focus == "first" && l.Items[0].ID == "first" })
	gs, _ := tw.c.gestures(time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if len(gs) != 1 || gs[0].Ref != "surface:first" || gs[0].What != gestureOpened || gs[0].Title != "Titre first" {
		t.Fatalf("gestures %+v", gs)
	}
	if code, _ := postJSON(tw, "/api/espace/action", actionBody("back", "first"), csrf); code != http.StatusNoContent {
		t.Fatalf("back: %d", code)
	}
	nextLayout(t, layouts, func(l workspaceLayout) bool { return l.Focus == "" && l.Items[0].ID == "q-alert-3" })
}

// The workspace's language is the one it was opened in: the queue's
// surfaces and Eriphe's line follow it.
func TestWorkspaceLangFromBoot(t *testing.T) {
	tw := newTestWeb(t)
	tw.setup()
	req, _ := http.NewRequest("GET", tw.srv.URL+"/espace/boot", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})
	resp, err := tw.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := tw.c.workspaceLang(); got != "en" {
		t.Fatalf("lang %q", got)
	}
	if turn := composerTurn(tw.c.poolSnapshot(momentChange, "")); !strings.Contains(turn, "en anglais") {
		t.Fatalf("turn:\n%s", turn)
	}
}

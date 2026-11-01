package main

import (
	"strings"
	"testing"
	"time"
)

func stateLine(t *testing.T, c *core) string {
	t.Helper()
	s, err := c.userState()
	if err != nil {
		t.Fatal(err)
	}
	return s.line("Sam")
}

// The state line: present or away and since when, back from an absence,
// the quiet hours now or next, the "now" left today; busy only with a
// source, none by default.
func TestUserStateLine(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "15:00"))
	if got := stateLine(t, c); !strings.Contains(got, "pas vu actif") {
		t.Errorf("never seen: %s", got)
	}
	c.seenPresence(at(t, "2026-11-04", "08:00"))
	c.seenPresence(at(t, "2026-11-04", "10:20"))
	if got := stateLine(t, c); !strings.HasPrefix(got, "[état de Sam : absent depuis 10:20 (4 h 40)") {
		t.Errorf("away: %s", got)
	}
	c.seenPresence(at(t, "2026-11-04", "14:50"))
	got := stateLine(t, c)
	for _, want := range []string{"présent, de retour à 14:50 après 4 h 30 d'absence (parti à 10:20)",
		"heures calmes à 22:00 (22:00–08:00)", "restantes aujourd'hui : 4 sur 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "occupé") {
		t.Errorf("busy without a calendar: %s", got)
	}
	c.notify(&notification{Text: "x", Why: "y", Source: notifyFromBrain}, notifyItem{Level: levelNow})
	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "16:30") }
	got = stateLine(t, c)
	if !strings.Contains(got, "3 sur 4") {
		t.Errorf("cap: %s", got)
	}

	// At night: in the quiet hours, until when.
	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "23:10") }
	c.seenPresence(at(t, "2026-11-04", "23:05"))
	if got := stateLine(t, c); !strings.Contains(got, "heures calmes en cours jusqu'à 2026-11-05 08:00") || !strings.Contains(got, "3 sur 4") {
		t.Errorf("night: %s", got)
	}
	// A window that starts after midnight is the next one, tomorrow.
	c.addNotifyRule(notifyRule{Kind: ruleQuiet, Days: daysAll, From: 30, To: 10 * 60, State: ruleActive, By: "owner"})
	c.notifyClock = func() time.Time { return at(t, "2026-11-04", "23:50") }
	if got := stateLine(t, c); !strings.Contains(got, "heures calmes à 2026-11-05 00:30 (00:30–10:00)") {
		t.Errorf("after midnight: %s", got)
	}

	// A busy source gives times only.
	busySources = []busySource{func(*core, time.Time) (time.Time, bool) { return at(t, "2026-11-05", "01:00"), true }}
	t.Cleanup(func() { busySources = nil })
	if got := stateLine(t, c); !strings.Contains(got, "occupé jusqu'à 2026-11-05 01:00 (agenda)") {
		t.Errorf("busy: %s", got)
	}
}

// The head of a turn is the time, then the state.
func TestTurnHead(t *testing.T) {
	c, _ := notifyCore(t, at(t, "2026-11-04", "15:00"))
	ownerSays(c, "Salut")
	head := strings.Split(c.turnHead(time.Now()), "\n")
	if len(head) != 2 || !strings.HasPrefix(head[0], "[maintenant ") || !strings.HasPrefix(head[1], "[état de Sam : ") {
		t.Fatalf("%q", head)
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{35 * time.Minute: "35 min", 4*time.Hour + 50*time.Minute: "4 h 50",
		3 * time.Hour: "3 h", 51 * time.Hour: "2 j 3 h"} {
		if got := span(d); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
}

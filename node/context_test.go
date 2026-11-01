package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// An app's category is worked out on the machine: the local table first
// (it corrects the declarations), then the declared category, else other.
func TestCategorize(t *testing.T) {
	for _, x := range []struct{ id, declared, want string }{
		{"com.google.Chrome", "", "web"},
		{"com.apple.Safari", "public.app-category.productivity", "web"},
		{"us.zoom.xos", "public.app-category.business", "meeting"},
		{"com.microsoft.teams2", "", "meeting"},
		{"com.microsoft.VSCode", "public.app-category.developer-tools", "code"},
		{"com.jetbrains.goland", "", "code"},
		{"dev.warp.Warp-Stable", "public.app-category.developer-tools", "code"},
		{"com.example.SomeGame", "public.app-category.games", "game"},
		{"com.example.Puzzle", "public.app-category.puzzle-games", "game"},
		{"com.valvesoftware.steam", "", "game"},
		{"com.example.Notes", "public.app-category.productivity", "other"},
		{"com.example.Unknown", "", "other"},
		{"Code.exe", "", "code"},
		{"msedge.exe", "", "web"},
		{"com.spotify.client", "public.app-category.music", "music"},
		{"com.example.Films", "public.app-category.video", "video"},
	} {
		got := categorize(x.id, x.declared)
		if got != x.want {
			t.Errorf("categorize(%q, %q) = %q, want %q", x.id, x.declared, got, x.want)
		}
		if !slices.Contains(activityCategories, got) {
			t.Errorf("%q is not a category", got)
		}
	}
}

// Level 0 says nothing; level 1 says whether not to disturb and why, never
// the category; level 2 the category too. Never the app's identifier.
func TestSessionAt(t *testing.T) {
	game := frontApp{BundleID: "com.example.SomeGame", Declared: "public.app-category.games"}
	if s := sessionAt(0, game, sessionState{}, true); s != (sessionState{}) {
		t.Fatalf("level 0 = %+v", s)
	}
	if s := sessionAt(1, game, sessionState{}, true); !s.Known || !s.DND || s.Reason != "game" || s.Activity != "" {
		t.Fatalf("level 1 game = %+v", s)
	}
	if s := sessionAt(2, game, sessionState{}, true); s.Activity != "game" {
		t.Fatalf("level 2 game = %+v", s)
	}
	meet := frontApp{BundleID: "us.zoom.xos"}
	if s := sessionAt(1, meet, sessionState{}, true); !s.DND || s.Reason != "meeting" {
		t.Fatalf("meeting = %+v", s)
	}
	code := frontApp{BundleID: "com.microsoft.VSCode"}
	if s := sessionAt(2, code, sessionState{}, true); s.DND || s.Activity != "code" {
		t.Fatalf("code = %+v", s)
	}
	// The system says fullscreen: not to be disturbed, whatever is in front.
	if s := sessionAt(1, code, sessionState{DND: true, Reason: "fullscreen"}, true); !s.DND || s.Reason != "fullscreen" {
		t.Fatalf("fullscreen = %+v", s)
	}
	// A reason off the list is dropped; the flag stays.
	if s := sessionAt(1, frontApp{}, sessionState{DND: true, Reason: "watching Some Film"}, true); !s.DND || s.Reason != "" {
		t.Fatalf("free reason = %+v", s)
	}
	if s := sessionAt(2, game, sessionState{}, false); s.Known {
		t.Fatalf("unknown read as known: %+v", s)
	}
	raw, _ := json.Marshal(sessionAt(2, game, sessionState{}, true))
	if string(raw) != `{"known":true,"dnd":true,"reason":"game","activity":"game"}` {
		t.Fatalf("told %s", raw)
	}
}

func TestRequestLevel(t *testing.T) {
	for q, want := range map[string]int{"": 0, "0": 0, "1": 1, "2": 2, "3": 0, "-1": 0, "x": 0} {
		if got := requestLevel(q); got != want {
			t.Errorf("requestLevel(%q) = %d", q, got)
		}
	}
}

func TestParseAppInfo(t *testing.T) {
	if asn, ok := parseASN([]byte("ASN:0x0-0x19019:\n")); !ok || asn != "ASN:0x0-0x19019:" {
		t.Fatalf("%q %v", asn, ok)
	}
	if _, ok := parseASN([]byte("-only; rm -rf /")); ok {
		t.Fatal("read a front app from garbage")
	}
	app := parseAppInfo([]byte(`"CFBundleIdentifier"="dev.warp.Warp-Stable"
"LSApplicationCategoryType"="public.app-category.developer-tools"`))
	if app.BundleID != "dev.warp.Warp-Stable" || app.Declared != "public.app-category.developer-tools" {
		t.Fatalf("%+v", app)
	}
	if app := parseAppInfo([]byte(`"CFBundleIdentifier"="com.google.Chrome"`)); app.BundleID != "com.google.Chrome" || app.Declared != "" {
		t.Fatalf("%+v", app)
	}
}

// A session helper's file (a Windows helper, say): fresh, it tells the app
// in front (mapped here), do not disturb and the idle time; stale, nothing.
func TestHelperSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	now := time.Now()
	idle := int64(42)
	write := func(h helperFile) {
		raw, _ := json.Marshal(h)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(helperFile{At: now, App: "steam.exe", Idle: &idle})
	app, st, ok := helperSession(path, now)
	if !ok || sessionAt(1, app, st, ok).Reason != "game" {
		t.Fatalf("%+v %+v %v", app, st, ok)
	}
	if s := helperIdle(path, now); !s.Known || s.Seconds != 42 {
		t.Fatalf("idle = %+v", s)
	}
	write(helperFile{At: now, DND: true, Reason: "fullscreen", Locked: true, Idle: &idle})
	if _, st, _ := helperSession(path, now); !st.DND || st.Reason != "fullscreen" {
		t.Fatalf("%+v", st)
	}
	if s := helperIdle(path, now); !s.Locked {
		t.Fatalf("locked not read: %+v", s)
	}
	// The Windows helper's: the category, worked out in the session.
	write(helperFile{At: now, Activity: "game"})
	if s := sessionAt(1, frontApp{}, st, true); s.Activity != "" {
		t.Fatalf("level 1 told the category: %+v", s)
	}
	app, st, ok = helperSession(path, now)
	if s := sessionAt(2, app, st, ok); !s.DND || s.Reason != "game" || s.Activity != "game" {
		t.Fatalf("helper's game = %+v", s)
	}
	write(helperFile{At: now, Activity: "code"})
	app, st, ok = helperSession(path, now)
	if s := sessionAt(2, app, st, ok); s.DND || s.Activity != "code" {
		t.Fatalf("helper's code = %+v", s)
	}
	write(helperFile{At: now, Activity: "Some Game.exe"})
	app, st, ok = helperSession(path, now)
	if s := sessionAt(2, app, st, ok); s.Activity != "" {
		t.Fatalf("a category off the list = %+v", s)
	}
	write(helperFile{At: now.Add(-time.Minute), App: "steam.exe"})
	if _, _, ok := helperSession(path, now); ok {
		t.Fatal("a stale file was read")
	}
	if _, _, ok := helperSession(filepath.Join(t.TempDir(), "none"), now); ok {
		t.Fatal("no file read as a session")
	}
}

// The status tells the session at the level asked, and nothing of the user
// at level 0; an eriphe-core without levels gets the idle time as before.
func TestNodeStatusLevel(t *testing.T) {
	n := newNode(options{docker: filepath.Join(t.TempDir(), "no-docker")})
	get := func(q string) map[string]json.RawMessage {
		rec := httptest.NewRecorder()
		n.handleNode(rec, httptest.NewRequest(http.MethodGet, "/v1/node"+q, nil))
		var out map[string]json.RawMessage
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if got := get("?context=0"); string(got["context"]) != `{"known":false}` || string(got["idle"]) != `{"known":false}` {
		t.Fatalf("level 0 told %s %s", got["context"], got["idle"])
	}
	if got := get(""); string(got["context"]) != `{"known":false}` {
		t.Fatalf("no level told %s", got["context"])
	}
}

// Reading the session stays cheap: well under the status's budget.
func TestMachineSessionCheap(t *testing.T) {
	start := time.Now()
	machineSession(context.Background(), 2)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("reading the session took %v", d)
	}
}

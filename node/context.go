package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/node/usersession"
)

// The user's session, told to eriphe-core with the node's status (GET
// /v1/node "context", beside "idle"), at the level eriphe-core asks for
// (?context=N, the user's choice on the page « Ce qu'Eriphe sait de toi »):
//
//   - 0, or no level (an older eriphe-core): nothing more than before;
//   - 1: whether the machine is not to be disturbed, and why, as a class
//     (game, meeting, fullscreen, mic, focus);
//   - 2: also the category of the app in front (game, meeting, code, web,
//     video, music, chat, other).
//
// Everything is worked out here, on the machine: the app's identifier and
// name never leave it, only a word from those closed lists. eriphe-core
// checks them again and keeps them in memory only, a few minutes.
//
// Each system has its probe (sessionProbe). On a Mac, the app in front
// and its declared category come from lsappinfo, as the user's own
// LaunchAgent: no permission, a few milliseconds. Elsewhere the node cannot
// see the user's session (Windows runs it in WSL; Linux and containers have
// no one answer): a helper running in the session may write what it sees to
// a file the node reads (ERIPHE_SESSION_FILE, helperSession), with the
// same closed values; without one, the session is unknown.

const (
	// sessionFileEnv names the file a session helper writes (helperSession).
	sessionFileEnv = "ERIPHE_SESSION_FILE"
	// helperFresh: a helper's file older than this says nothing.
	helperFresh = 30 * time.Second
)

// sessionState is what the node tells of the session.
type sessionState struct {
	Known    bool   `json:"known"`
	DND      bool   `json:"dnd,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Activity string `json:"activity,omitempty"`
}

// The closed lists, the same as eriphe-core's (core/context.go).
var (
	dndReasons         = usersession.Reasons
	activityCategories = usersession.Activities
)

// frontApp is what a probe reads of the app in front, on the machine only.
type frontApp struct {
	BundleID string // com.example.App, or an executable's name on Windows
	Declared string // its declared category: public.app-category.games
}

// sessionProbe reads the session on one system. A probe for a new system
// (a Windows session helper's, say) gives the app in front, or the state
// straight away when the system says it (fullscreen, a game in exclusive
// mode); sessionAt makes it the state told at a level.
type sessionProbe func(ctx context.Context) (frontApp, sessionState, bool)

// readSession is this system's probe (context_darwin.go, context_other.go).
var _ sessionProbe = readSession

// requestLevel reads the level eriphe-core asks for: 0 when absent or not 0..2.
func requestLevel(q string) int {
	n, err := strconv.Atoi(q)
	if err != nil || n < 0 || n > 2 {
		return 0
	}
	return n
}

// sessionAt is the session told at a level, from what a probe read: the
// app's category, worked out here, decides whether it is not to be
// disturbed; a state the system gives (st) adds to it. The category leaves
// only at level 2.
func sessionAt(level int, app frontApp, st sessionState, ok bool) sessionState {
	if level < 1 || !ok {
		return sessionState{}
	}
	out := sessionState{Known: true, DND: st.DND, Reason: st.Reason}
	cat := ""
	if app.BundleID != "" || app.Declared != "" {
		cat = categorize(app.BundleID, app.Declared)
	} else if slices.Contains(activityCategories, st.Activity) {
		cat = st.Activity // a helper's, worked out in the session
	}
	if !out.DND {
		switch cat {
		case "game", "meeting":
			out.DND, out.Reason = true, cat
		}
	}
	if !slices.Contains(dndReasons, out.Reason) {
		out.Reason = ""
	}
	if level >= 2 {
		out.Activity = cat
	}
	return out
}

// machineSession is the session at a level, from this system's probe, else
// from a session helper's file.
func machineSession(ctx context.Context, level int) sessionState {
	if level < 1 {
		return sessionState{}
	}
	app, st, ok := readSession(ctx)
	if !ok {
		if path := os.Getenv(sessionFileEnv); path != "" {
			app, st, ok = helperSession(path, time.Now())
		}
	}
	return sessionAt(level, app, st, ok)
}

// categorize is an app's category, worked out on the machine (usersession.Category).
func categorize(bundleID, declared string) string {
	return usersession.Category(bundleID, declared)
}

var (
	asnRe      = regexp.MustCompile(`^ASN:0x[0-9a-fA-F]+-0x[0-9a-fA-F]+:$`)
	bundleRe   = regexp.MustCompile(`"CFBundleIdentifier"="([^"\n]{1,200})"`)
	declaredRe = regexp.MustCompile(`"LSApplicationCategoryType"="([^"\n]{1,200})"`)
)

// parseASN reads the app in front from `lsappinfo front`: ASN:0x0-0x19019:.
func parseASN(out []byte) (string, bool) {
	s := strings.TrimSpace(string(out))
	return s, asnRe.MatchString(s)
}

// parseAppInfo reads `lsappinfo info -only bundleid -only
// LSApplicationCategoryType`: the identifier and the declared category.
func parseAppInfo(out []byte) frontApp {
	var app frontApp
	if m := bundleRe.FindSubmatch(out); m != nil {
		app.BundleID = string(m[1])
	}
	if m := declaredRe.FindSubmatch(out); m != nil {
		app.Declared = string(m[1])
	}
	return app
}

// helperFile is what a session helper writes, every few seconds
// (usersession.File): the category of the app in front, or the app itself
// for the node to map, and closed values. The Windows helper
// (cmd/eriphe-session) writes the category, worked out in the session.
type helperFile = usersession.File

// userIdle is the machine's idle time as told: from this system, else from
// a session helper; nothing at level 0. An eriphe-core older than levels
// (no ?context) gets it as before.
func userIdle(ctx context.Context, q url.Values) idleState {
	if q.Has("context") && requestLevel(q.Get("context")) < 1 {
		return idleState{}
	}
	if s := machineIdle(ctx); s.Known {
		return s
	}
	if path := os.Getenv(sessionFileEnv); path != "" {
		return helperIdle(path, time.Now())
	}
	return idleState{}
}

// helperIdle is the idle time a session helper's file tells, if fresh.
func helperIdle(path string, now time.Time) idleState {
	h, ok := readHelper(path, now)
	if !ok || h.Idle == nil || *h.Idle < 0 {
		return idleState{}
	}
	return idleState{Known: true, Seconds: *h.Idle, Locked: h.Locked}
}

// readHelper reads a session helper's file, if it is fresh.
func readHelper(path string, now time.Time) (helperFile, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 4<<10 {
		return helperFile{}, false
	}
	var h helperFile
	if json.Unmarshal(raw, &h) != nil || now.Sub(h.At) > helperFresh || h.At.After(now.Add(helperFresh)) {
		return helperFile{}, false
	}
	return h, true
}

// helperSession reads a session helper's file, if it is fresh.
func helperSession(path string, now time.Time) (frontApp, sessionState, bool) {
	h, ok := readHelper(path, now)
	if !ok {
		return frontApp{}, sessionState{}, false
	}
	st := sessionState{DND: h.DND}
	if slices.Contains(dndReasons, h.Reason) {
		st.Reason = h.Reason
	}
	if slices.Contains(activityCategories, h.Activity) {
		st.Activity = h.Activity
	}
	return frontApp{BundleID: h.App, Declared: h.Declared}, st, true
}

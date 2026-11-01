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
	dndReasons         = []string{"game", "meeting", "fullscreen", "mic", "focus"}
	activityCategories = []string{"game", "meeting", "code", "web", "video", "music", "chat", "other"}
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
		if cat == "" && slices.Contains(activityCategories, st.Activity) {
			out.Activity = st.Activity
		}
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

// The categories of apps that declare none, or declare a misleading one,
// by identifier: browsers (Chrome declares none, Safari "productivity"),
// meeting apps, editors and terminals, game stores. A prefix ends in ".".
var appTable = map[string]string{
	// web
	"com.apple.Safari": "web", "com.google.Chrome": "web", "org.mozilla.firefox": "web", "com.microsoft.edgemac": "web",
	"company.thebrowser.Browser": "web", "com.brave.Browser": "web", "com.operasoftware.Opera": "web", "com.vivaldi.Vivaldi": "web",
	"chrome.exe": "web", "msedge.exe": "web", "firefox.exe": "web", "brave.exe": "web", "opera.exe": "web",
	// meeting
	"us.zoom.xos": "meeting", "com.microsoft.teams": "meeting", "com.microsoft.teams2": "meeting", "com.apple.FaceTime": "meeting",
	"com.cisco.webexmeetingsapp": "meeting", "com.webex.meetingmanager": "meeting",
	"zoom.exe": "meeting", "ms-teams.exe": "meeting", "teams.exe": "meeting",
	// code
	"com.microsoft.VSCode": "code", "com.todesktop.230313mzl4w4u92": "code", "dev.zed.Zed": "code", "com.apple.dt.Xcode": "code",
	"com.jetbrains.": "code", "com.sublimetext.": "code", "com.apple.Terminal": "code", "com.googlecode.iterm2": "code",
	"dev.warp.": "code", "com.mitchellh.ghostty": "code", "net.kovidgoyal.kitty": "code", "org.alacritty": "code",
	"code.exe": "code", "cursor.exe": "code", "windowsterminal.exe": "code", "devenv.exe": "code",
	// game
	"com.valvesoftware.steam": "game", "com.epicgames.EpicGamesLauncher": "game", "steam.exe": "game",
	// video, music, chat
	"com.apple.TV": "video", "org.videolan.vlc": "video", "com.colliderli.iina": "video", "vlc.exe": "video",
	"com.spotify.client": "music", "com.apple.Music": "music", "spotify.exe": "music",
	"com.tinyspeck.slackmacgap": "chat", "com.hnc.Discord": "chat", "ru.keepcoder.Telegram": "chat", "net.whatsapp.WhatsApp": "chat",
	"com.apple.MobileSMS": "chat", "org.whispersystems.signal-desktop": "chat", "slack.exe": "chat", "discord.exe": "chat",
}

// The declared categories (LSApplicationCategoryType) that say something.
var declaredTable = map[string]string{
	"public.app-category.developer-tools":   "code",
	"public.app-category.video":             "video",
	"public.app-category.music":             "music",
	"public.app-category.social-networking": "chat",
	"public.app-category.entertainment":     "video",
}

// categorize is an app's category: the local table first (it corrects
// the declarations), then its declared category, else other. Pure: the
// app's identifier goes in, one word of activityCategories comes out.
func categorize(bundleID, declared string) string {
	id := strings.TrimSpace(bundleID)
	if cat, ok := appTable[id]; ok {
		return cat
	}
	if cat, ok := appTable[strings.ToLower(id)]; ok {
		return cat
	}
	for prefix, cat := range appTable {
		if strings.HasSuffix(prefix, ".") && strings.HasPrefix(id, prefix) {
			return cat
		}
	}
	declared = strings.ToLower(strings.TrimSpace(declared))
	if declared == "public.app-category.games" || strings.HasSuffix(declared, "-games") &&
		strings.HasPrefix(declared, "public.app-category.") {
		return "game"
	}
	if cat, ok := declaredTable[declared]; ok {
		return cat
	}
	return "other"
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

// helperFile is what a session helper writes, every few seconds: the app
// in front stays on the machine (the node maps it), the rest is closed
// values. A Windows helper fills it from GetForegroundWindow and
// SHQueryUserNotificationState (QUNS_BUSY → fullscreen,
// QUNS_RUNNING_D3D_FULL_SCREEN → game, QUNS_PRESENTATION_MODE → focus).
type helperFile struct {
	At       time.Time `json:"at"`
	App      string    `json:"app,omitempty"`      // the executable or bundle identifier in front
	Declared string    `json:"declared,omitempty"` // its declared category, if the system has one
	DND      bool      `json:"dnd,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Idle     *int64    `json:"idle_seconds,omitempty"` // GetLastInputInfo, in the session
	Locked   bool      `json:"locked,omitempty"`       // QUNS_NOT_PRESENT
}

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
	return frontApp{BundleID: h.App, Declared: h.Declared}, st, true
}

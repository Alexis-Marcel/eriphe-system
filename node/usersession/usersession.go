// Package usersession holds what eriphe-node and a session helper share about
// the user's session: the closed lists of what may be told, the category
// of an app (worked out on the machine, from a local table), and the file a
// helper running in the user's session writes for the node to read
// (ERIPHE_SESSION_FILE).
//
// Nothing here leaves the machine but a word of the closed lists: an app's
// identifier, name or path goes in, a category comes out.
package usersession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The closed lists, the same as eriphe-core's (core/context.go).
var (
	// Reasons not to be disturbed, as a class.
	Reasons = []string{"game", "meeting", "fullscreen", "mic", "focus"}
	// Activities: the category of the app in front.
	Activities = []string{"game", "meeting", "code", "web", "video", "music", "chat", "other"}
)

// File is what a session helper writes every few seconds, and the node
// reads (at most 4 KiB, ignored once older than 30 s). The Windows helper
// (cmd/eriphe-session) writes the category of the app in front, worked out
// in the session, never the app itself; App and Declared are for a helper
// that leaves it to the node.
type File struct {
	At       time.Time `json:"at"`
	App      string    `json:"app,omitempty"`      // the executable or bundle identifier in front
	Declared string    `json:"declared,omitempty"` // its declared category, if the system has one
	Activity string    `json:"activity,omitempty"` // the category of the app in front (Activities)
	DND      bool      `json:"dnd,omitempty"`
	Reason   string    `json:"reason,omitempty"`       // Reasons
	Idle     *int64    `json:"idle_seconds,omitempty"` // keyboard and mouse idle, in seconds
	Locked   bool      `json:"locked,omitempty"`       // the screen is locked, or the session is not at the screen
}

// Write writes f to path, whole or not at all: a temporary file next to
// it, then renamed over it (MoveFileEx's replace on Windows).
func Write(path string, f File) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// The categories of apps that declare none, or declare a misleading one,
// by identifier (a Mac's bundle identifier, a Windows executable's name in
// lower case): browsers (Chrome declares none, Safari "productivity"),
// meeting apps, editors and terminals, game stores. A prefix ends in ".".
var appTable = map[string]string{
	// web
	"com.apple.Safari": "web", "com.google.Chrome": "web", "org.mozilla.firefox": "web", "com.microsoft.edgemac": "web",
	"company.thebrowser.Browser": "web", "com.brave.Browser": "web", "com.operasoftware.Opera": "web", "com.vivaldi.Vivaldi": "web",
	"chrome.exe": "web", "msedge.exe": "web", "firefox.exe": "web", "brave.exe": "web", "opera.exe": "web", "vivaldi.exe": "web",
	"arc.exe": "web",
	// meeting
	"us.zoom.xos": "meeting", "com.microsoft.teams": "meeting", "com.microsoft.teams2": "meeting", "com.apple.FaceTime": "meeting",
	"com.cisco.webexmeetingsapp": "meeting", "com.webex.meetingmanager": "meeting",
	"zoom.exe": "meeting", "ms-teams.exe": "meeting", "teams.exe": "meeting", "webex.exe": "meeting", "ciscocollabhost.exe": "meeting",
	// code
	"com.microsoft.VSCode": "code", "com.todesktop.230313mzl4w4u92": "code", "dev.zed.Zed": "code", "com.apple.dt.Xcode": "code",
	"com.jetbrains.": "code", "com.sublimetext.": "code", "com.apple.Terminal": "code", "com.googlecode.iterm2": "code",
	"dev.warp.": "code", "com.mitchellh.ghostty": "code", "net.kovidgoyal.kitty": "code", "org.alacritty": "code",
	"code.exe": "code", "cursor.exe": "code", "windowsterminal.exe": "code", "devenv.exe": "code", "zed.exe": "code",
	"sublime_text.exe": "code", "wezterm-gui.exe": "code", "alacritty.exe": "code", "conhost.exe": "code",
	"powershell.exe": "code", "pwsh.exe": "code", "cmd.exe": "code", "idea64.exe": "code", "goland64.exe": "code",
	"pycharm64.exe": "code", "webstorm64.exe": "code", "rider64.exe": "code", "clion64.exe": "code",
	// game: stores and launchers
	"com.valvesoftware.steam": "game", "com.epicgames.EpicGamesLauncher": "game", "steam.exe": "game",
	"epicgameslauncher.exe": "game", "battle.net.exe": "game", "eadesktop.exe": "game", "galaxyclient.exe": "game",
	"upc.exe": "game", "ubisoftconnect.exe": "game", "riotclientux.exe": "game", "riotclientservices.exe": "game",
	// video, music, chat
	"com.apple.TV": "video", "org.videolan.vlc": "video", "com.colliderli.iina": "video", "vlc.exe": "video",
	"mpv.exe": "video", "mpc-hc64.exe": "video",
	"com.spotify.client": "music", "com.apple.Music": "music", "spotify.exe": "music",
	"com.tinyspeck.slackmacgap": "chat", "com.hnc.Discord": "chat", "ru.keepcoder.Telegram": "chat", "net.whatsapp.WhatsApp": "chat",
	"com.apple.MobileSMS": "chat", "org.whispersystems.signal-desktop": "chat", "slack.exe": "chat", "discord.exe": "chat",
	"telegram.exe": "chat", "whatsapp.exe": "chat", "signal.exe": "chat",
}

// The declared categories (LSApplicationCategoryType) that say something.
var declaredTable = map[string]string{
	"public.app-category.developer-tools":   "code",
	"public.app-category.video":             "video",
	"public.app-category.music":             "music",
	"public.app-category.social-networking": "chat",
	"public.app-category.entertainment":     "video",
}

// Category is an app's category: the local table first (it corrects the
// declarations), then its declared category, else other. Pure: the app's
// identifier goes in, one word of Activities comes out.
func Category(id, declared string) string {
	id = strings.TrimSpace(id)
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

// The folders games are installed in by their stores, in lower case with
// forward slashes: a program under one is a game, whatever its name.
var gameFolders = []string{
	"/steamapps/common/", "/epic games/", "/xboxgames/", "/gog galaxy/games/", "/gog games/",
	"/riot games/", "/ea games/", "/ubisoft game launcher/games/", "/battle.net/",
}

// WindowsCategory is the category of a Windows program from its full path,
// as QueryFullProcessImageName gives it: its executable's name in the
// table, else a game when it is installed where game stores put games,
// else other. Pure; the path never leaves the machine.
func WindowsCategory(path string) string {
	p := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), `\`, "/"))
	if p == "" {
		return ""
	}
	base := p[strings.LastIndex(p, "/")+1:]
	if cat, ok := appTable[base]; ok {
		return cat
	}
	for _, f := range gameFolders {
		if strings.Contains(p, f) {
			return "game"
		}
	}
	return "other"
}

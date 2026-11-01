package usersession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A Windows program's category, from its path: its name in the table, else
// a game where the stores install games, else other.
func TestWindowsCategory(t *testing.T) {
	for _, x := range []struct{ path, want string }{
		{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "web"},
		{`C:\Program Files (x86)\Microsoft\Edge\Application\MSEDGE.EXE`, "web"},
		{`C:\Users\Sam\AppData\Local\Microsoft\WindowsApps\ms-teams.exe`, "meeting"},
		{`C:\Users\Sam\AppData\Roaming\Zoom\bin\Zoom.exe`, "meeting"},
		{`C:\Users\Sam\AppData\Local\Programs\Microsoft VS Code\Code.exe`, "code"},
		{`C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.21\WindowsTerminal.exe`, "code"},
		{`C:\Program Files (x86)\Steam\steam.exe`, "game"},
		{`D:\SteamLibrary\steamapps\common\Some Game\bin\win64\game.exe`, "game"},
		{`C:\Program Files\Epic Games\Some Game\Binaries\Win64\Game-Win64-Shipping.exe`, "game"},
		{`C:\XboxGames\Some Game\Content\game.exe`, "game"},
		{`C:\Program Files\VideoLAN\VLC\vlc.exe`, "video"},
		{`C:\Users\Sam\AppData\Roaming\Spotify\Spotify.exe`, "music"},
		{`C:\Users\Sam\AppData\Local\Discord\app-1.0\Discord.exe`, "chat"},
		{`C:\Windows\explorer.exe`, "other"},
		{`C:\Program Files\Some Vendor\Tool.exe`, "other"},
		{"", ""},
	} {
		got := WindowsCategory(x.path)
		if got != x.want {
			t.Errorf("WindowsCategory(%q) = %q, want %q", x.path, got, x.want)
		}
		if got != "" && !slices.Contains(Activities, got) {
			t.Errorf("%q is not a category", got)
		}
	}
}

// Every category in the tables is one of the closed list.
func TestTablesClosed(t *testing.T) {
	for id, cat := range appTable {
		if !slices.Contains(Activities, cat) {
			t.Errorf("%s → %q", id, cat)
		}
	}
	for id, cat := range declaredTable {
		if !slices.Contains(Activities, cat) {
			t.Errorf("%s → %q", id, cat)
		}
	}
}

// Write replaces the file whole and leaves nothing beside it.
func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	at := time.Date(2026, 11, 1, 20, 15, 0, 0, time.UTC)
	for _, act := range []string{"web", "game"} {
		if err := Write(path, File{At: at, Activity: act}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil || f.Activity != "game" || !f.At.Equal(at) {
		t.Fatalf("read %s (%v)", raw, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("left %v", ents)
	}
	if err := Write(filepath.Join(dir, "none", "session.json"), File{At: at}); err == nil {
		t.Fatal("wrote into a missing folder")
	}
}

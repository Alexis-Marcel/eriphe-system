package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/node/usersession"
)

// The answers fileOf ignores.
const (
	qunsAcceptsNotifs = 5
	qunsQuietTime     = 6
)

var now = time.Date(2026, 11, 1, 20, 15, 0, 0, time.UTC)

// What Windows says becomes closed values; the program in front, a
// category; never its name or path.
func TestFileOf(t *testing.T) {
	steamGame := `C:\Program Files (x86)\Steam\steamapps\common\Some Game\game.exe`
	for _, x := range []struct {
		name     string
		p        probe
		dnd      bool
		reason   string
		activity string
		locked   bool
	}{
		{"exclusive fullscreen", probe{QUNS: qunsRunningD3DFull, Image: steamGame}, true, "game", "game", false},
		{"fullscreen", probe{QUNS: qunsBusy, Image: `C:\Program Files\VideoLAN\VLC\vlc.exe`}, true, "fullscreen", "video", false},
		{"presentation", probe{QUNS: qunsPresentationMode, Image: `C:\Windows\System32\cmd.exe`}, true, "fullscreen", "code", false},
		{"accepts", probe{QUNS: qunsAcceptsNotifs, Image: `C:\Program Files\Microsoft VS Code\Code.exe`}, false, "", "code", false},
		{"quiet time is not the user's", probe{QUNS: qunsQuietTime, Image: `C:\Apps\Tool.exe`}, false, "", "other", false},
		{"meeting app", probe{QUNS: qunsAcceptsNotifs, Image: `C:\Users\Sam\AppData\Roaming\Zoom\bin\Zoom.exe`}, false, "", "meeting", false},
		{"lock screen", probe{QUNS: qunsNotPresent, Image: `C:\Windows\System32\LockApp.exe`}, false, "", "", true},
		{"locked by the session", probe{Locked: true, Image: steamGame}, false, "", "", true},
		{"nothing in front", probe{QUNS: qunsAcceptsNotifs}, false, "", "", false},
		{"unknown state", probe{}, false, "", "", false},
	} {
		f := fileOf(x.p, now)
		if f.DND != x.dnd || f.Reason != x.reason || f.Activity != x.activity || f.Locked != x.locked {
			t.Errorf("%s: %+v", x.name, f)
		}
		if f.App != "" || f.Declared != "" {
			t.Errorf("%s: the program left the session: %+v", x.name, f)
		}
		raw, _ := json.Marshal(f)
		if strings.Contains(strings.ToLower(string(raw)), ".exe") {
			t.Errorf("%s: %s", x.name, raw)
		}
	}
}

func TestFileOfIdle(t *testing.T) {
	if f := fileOf(probe{IdleOK: true, Idle: 42900 * time.Millisecond}, now); f.Idle == nil || *f.Idle != 42 {
		t.Fatalf("idle = %v", f.Idle)
	}
	if f := fileOf(probe{IdleOK: true}, now); f.Idle == nil || *f.Idle != 0 {
		t.Fatalf("idle 0 = %v", f.Idle)
	}
	if f := fileOf(probe{}, now); f.Idle != nil {
		t.Fatalf("unknown idle = %v", *f.Idle)
	}
}

// It writes its file at once, then every tick, whole each time.
func TestRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	read := func() (probe, error) {
		reads++
		if reads == 2 {
			cancel()
		}
		return probe{IdleOK: true, Idle: time.Second * time.Duration(reads), QUNS: qunsBusy}, nil
	}
	run(ctx, path, time.Millisecond, read, func() time.Time { return now })
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f usersession.File
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if !f.At.Equal(now) || f.Idle == nil || *f.Idle != 2 || !f.DND || f.Reason != "fullscreen" {
		t.Fatalf("wrote %s", raw)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*"))
	if len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

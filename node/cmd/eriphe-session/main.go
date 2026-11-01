// eriphe-session tells eriphe-node what it cannot see from where it runs:
// whether the user is at the PC, and whether they are not to be disturbed.
// On Windows the node runs in WSL, started at boot outside the user's
// session, where keyboard, mouse, screen and windows are out of sight. This
// small program runs in the session instead, as the user, started at logon
// by the scheduled task "Eriphe session" (install/install.ps1), with no
// window, and every few seconds writes a file the node reads
// (ERIPHE_SESSION_FILE, usersession.File):
//
//   - idle_seconds: since the last key or mouse move (GetLastInputInfo);
//   - locked: the screen is locked, or the session is not at the screen
//     (WTSQuerySessionInformation; QUNS_NOT_PRESENT);
//   - dnd and reason: what Windows says of notifications
//     (SHQueryUserNotificationState): a Direct3D game in exclusive
//     fullscreen → game, an app in fullscreen or presentation mode →
//     fullscreen;
//   - activity: the category of the program in front, from its path, by a
//     table on the machine (usersession.WindowsCategory): game, meeting,
//     code, web, video, music, chat or other. Never its name or path, nor a
//     window's title.
//
// It writes only its own file, opens no port and sends nothing anywhere.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/node/usersession"
)

// probe is what the session gives, read on Windows (probe_windows.go).
type probe struct {
	IdleOK bool
	Idle   time.Duration // since the last input, when IdleOK
	Locked bool          // locked, or the session is not at the screen
	QUNS   int           // SHQueryUserNotificationState's answer; 0 when unknown
	Image  string        // the full path of the program in front; "" when unknown
}

// SHQueryUserNotificationState's answers (QUERY_USER_NOTIFICATION_STATE).
// QUNS_ACCEPTS_NOTIFICATIONS (5) and QUNS_QUIET_TIME (6, the first hour
// after an account's first logon, not the user's choice) say nothing.
const (
	qunsNotPresent       = 1 // a screen saver, the lock screen, fast user switching
	qunsBusy             = 2 // an app in fullscreen
	qunsRunningD3DFull   = 3 // a Direct3D app in exclusive fullscreen: a game
	qunsPresentationMode = 4 // presentation mode, set by the user
	qunsApp              = 7 // a Store app in fullscreen (Windows 8)
)

const (
	defaultEvery = 5 * time.Second
	// The file, under %ProgramData%: the folder the installer lets the user write.
	defaultPath = "eriphe-node/session/session.json"
)

// fileOf is what the helper writes of a probe: closed values only. Pure.
func fileOf(p probe, now time.Time) usersession.File {
	f := usersession.File{At: now.UTC().Truncate(time.Second), Locked: p.Locked || p.QUNS == qunsNotPresent}
	if p.IdleOK && p.Idle >= 0 {
		s := int64(p.Idle / time.Second)
		f.Idle = &s
	}
	switch p.QUNS {
	case qunsRunningD3DFull:
		f.DND, f.Reason = true, "game"
	case qunsBusy, qunsPresentationMode, qunsApp:
		f.DND, f.Reason = true, "fullscreen"
	}
	// What is in front of a locked screen is not what the user does.
	if !f.Locked {
		f.Activity = usersession.WindowsCategory(p.Image)
	}
	return f
}

// defaultFile is where the installer has the node read it.
func defaultFile() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, filepath.FromSlash(defaultPath))
}

func main() {
	file := flag.String("file", defaultFile(), "the file to write, which eriphe-node reads")
	every := flag.Duration("every", defaultEvery, "how often to write it")
	once := flag.Bool("print", false, "print what it would write, once, and exit (eriphe-session -print | more)")
	flag.Parse()
	if *every < time.Second {
		*every = time.Second
	}
	p, err := read()
	if err != nil {
		fmt.Fprintln(os.Stderr, "eriphe-session:", err)
		os.Exit(2)
	}
	if *once {
		raw, _ := json.Marshal(fileOf(p, time.Now()))
		fmt.Println(string(raw))
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run(ctx, *file, *every, read, time.Now)
}

// run writes the file every tick until ctx ends. A failed read or write is
// tried again at the next tick: the node takes a file that is not renewed
// for an unknown session.
func run(ctx context.Context, file string, every time.Duration, read func() (probe, error), now func() time.Time) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if p, err := read(); err == nil {
			_ = usersession.Write(file, fileOf(p, now()))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

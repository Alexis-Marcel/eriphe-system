package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Updates. The node cannot update itself: it has no right to pull an image
// or to recreate its own container. The installers put an updater next to
// it on the host side (install/README.md, Updates), which holds those
// rights: a systemd unit on Linux and in the macOS VM, a loop in the WSL
// distribution on Windows. They talk through three files in the node's root:
//
//	updater           written by the installer: an updater is there, and the version it is pinned to
//	update-requested  written by the node: update (to the latest version, or the pinned one) or roll back
//	update-status     written by the updater: how the last request went
//
// eriphe-core asks for an update in the node's assignments. It never names
// an image nor a command: only "update" or "rollback", the version it runs
// itself (the node is never to be newer than eriphe-core), and whether the
// request is automatic (an automatic one skips a version that failed here
// before). The node writes the request only once no task is running: the
// updater recreates it, and a running agent would lose its turn.

const (
	updaterFile = "updater"
	requestFile = "update-requested"
	statusFile  = "update-status"
)

// updateRequest is what eriphe-core asks for (in the assignments).
type updateRequest struct {
	ID     string `json:"id"`
	Action string `json:"action"` // update or rollback
	Core   string `json:"core"`   // eriphe-core's version: the most this node may get
	Auto   bool   `json:"auto"`   // from the night's automatic updates
}

var (
	updateIDRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	// A release (0.2.0), a build of main (0.2.0-dev+abc1234), a build from
	// before the releases (sha-<commit>), a local one (dev), or none.
	updateVersionRe = regexp.MustCompile(`^(sha-[0-9a-f]{7,40}|dev|(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(-[0-9A-Za-z.-]{1,40})?(\+[0-9A-Za-z.-]{1,40})?)?$`)
)

func (u updateRequest) valid() bool {
	return updateIDRe.MatchString(u.ID) && (u.Action == "update" || u.Action == "rollback") &&
		updateVersionRe.MatchString(u.Core)
}

// updates is the node's side of the updates: the request it holds until no
// task runs, and the last one it handed to the updater.
type updates struct {
	root string

	mu      sync.Mutex
	pending *updateRequest // waiting for the tasks to end
	written string         // the id of the request handed to the updater
}

// keyValues reads a file of key=value lines; nil if it is missing.
func keyValues(path string) map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// request takes eriphe-core's request, and tells whether it is a new one;
// nil drops the one waiting (eriphe-core took it back).
func (u *updates) request(r *updateRequest) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if r == nil {
		u.pending = nil
		return false
	}
	if !r.valid() {
		log.Printf("update: ignoring a malformed request from eriphe-core")
		return false
	}
	if r.ID == u.written || (u.pending != nil && u.pending.ID == r.ID) {
		return false
	}
	if st := keyValues(filepath.Join(u.root, statusFile)); st["id"] == r.ID {
		return false // done already, before a restart
	}
	u.pending = r
	log.Printf("update: eriphe-core asks to %s, once no task is running", r.Action)
	return true
}

// handOver writes the waiting request for the updater once nothing runs.
// It returns whether it wrote one.
func (u *updates) handOver(running int) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pending == nil || running > 0 {
		return false
	}
	if keyValues(filepath.Join(u.root, updaterFile)) == nil {
		return false // no updater: eriphe-core is told, through the info
	}
	r := u.pending
	auto := "0"
	if r.Auto {
		auto = "1"
	}
	body := fmt.Sprintf("id=%s\naction=%s\ncore=%s\nauto=%s\n", r.ID, r.Action, r.Core, auto)
	path := filepath.Join(u.root, requestFile)
	if err := os.WriteFile(path+".new", []byte(body), 0o600); err != nil {
		log.Printf("update: %v", err)
		return false
	}
	if err := os.Rename(path+".new", path); err != nil {
		log.Printf("update: %v", err)
		return false
	}
	u.pending, u.written = nil, r.ID
	log.Printf("update: request %s handed to the updater (%s)", r.ID, r.Action)
	return true
}

// info is what the node reports about its updates (GET /v1/node).
func (u *updates) info() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	up := keyValues(filepath.Join(u.root, updaterFile))
	out := map[string]any{"updater": up != nil}
	if up != nil && up["pin"] != "" {
		out["pin"] = up["pin"]
	}
	if up != nil && up["channel"] == "edge" {
		out["channel"] = "edge"
	}
	if u.pending != nil {
		out["waiting"] = u.pending.ID
	}
	if _, err := os.Stat(filepath.Join(u.root, requestFile)); err == nil && u.written != "" {
		out["requested"] = u.written
	}
	if st := keyValues(filepath.Join(u.root, statusFile)); st != nil {
		s := map[string]any{}
		for _, k := range []string{"id", "action", "outcome", "message", "from", "to"} {
			if st[k] != "" {
				s[k] = st[k]
			}
		}
		var at int64
		fmt.Sscan(st["at"], &at)
		if at > 0 {
			s["at"] = time.Unix(at, 0).UTC()
		}
		out["status"] = s
	}
	return out
}

// running counts the tasks an update would interrupt: an agent at work or
// waiting for a permission. An idle one resumes its session after the
// restart.
func (n *node) running() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, t := range n.tasks {
		if s := t.state(); s == stateRunning || s == stateWaitingPermission {
			c++
		}
	}
	return c
}

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Machines update themselves, each with its own updater, which the
// installers put on the host side (install/README.md, Updates): eriphe-core
// only says when, and never names an image nor a command. Its requests are
// "update" (to the latest official version, or the one the machine is
// pinned to) and "rollback" (to the version before). The order is the
// Kubernetes one: eriphe-core's machine first, the nodes once eriphe-core
// runs the latest version, as a node is never to be newer than eriphe-core;
// and a node only once none of its tasks is at work.
//
// eriphe-core's own machine reads the request from eriphe-core's data folder
// (update-requested), and writes how it went next to it (update-status); a
// node gets its request in its assignments and hands it to its updater the
// same way, in its own root (node/update.go). In both places, the file
// "updater" says there is one, and the version it is pinned to.
//
// When is the user's choice, for all machines and for each one: tell me
// (the workspace says a new version is out, the default), automatically at
// night, or never.

const (
	updateNotify = "notify"
	updateNight  = "night"
	updateNever  = "never"

	coreMachine = "core" // eriphe-core's machine; a node is "node:<name>"

	nightStart = 3 // automatic updates run from 3:00 to 5:00, the user's time
	nightEnd   = 5

	requestTTL = 24 * time.Hour // a request not done by then is dropped
)

var updatePolicies = []string{updateNotify, updateNight, updateNever}

var (
	errNoUpdater = errors.New("update.noupdater")
	errCoreFirst = errors.New("update.corefirst")
	errPinned    = errors.New("update.pinnedno")
)

// updaterInfo is what a machine's updater says, through the node's info
// (node/update.go) or eriphe-core's data folder.
type updaterInfo struct {
	Updater   bool          `json:"updater"`
	Pin       string        `json:"pin,omitempty"`       // the version it is pinned to
	Waiting   string        `json:"waiting,omitempty"`   // a request waits for the tasks to end
	Requested string        `json:"requested,omitempty"` // a request is with the updater
	Status    *updateStatus `json:"status,omitempty"`    // how the last one went
}

// updateStatus is the outcome of a request: running, ok, current (nothing
// newer), pinned, refused (it would be newer than eriphe-core), skipped
// (failed here before), failed (nothing changed) or rolled_back.
type updateStatus struct {
	ID      string    `json:"id"`
	Action  string    `json:"action,omitempty"`
	Outcome string    `json:"outcome"`
	Message string    `json:"message,omitempty"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	At      time.Time `json:"at,omitzero"`
}

// machineUpdate is a request not done yet.
type machineUpdate struct {
	ID     string    `json:"id"`
	Action string    `json:"action"`
	Auto   bool      `json:"auto,omitempty"`
	At     time.Time `json:"at"`
}

// nodeUpdate is the request as a node reads it in its assignments.
type nodeUpdate struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Core   string `json:"core"` // eriphe-core's version: the most the node may get
	Auto   bool   `json:"auto,omitempty"`
}

func validPolicy(p string) bool {
	for _, q := range updatePolicies {
		if p == q {
			return true
		}
	}
	return false
}

// updatePolicy is a machine's own choice (empty: as all machines) and the
// one that applies.
func (c *core) updatePolicy(machine string) (own, eff string) {
	own = c.store.get("update_policy:" + machine)
	eff = own
	if eff == "" {
		eff = c.store.get("update_policy")
	}
	if !validPolicy(eff) {
		eff = updateNotify
	}
	return own, eff
}

// setUpdatePolicy sets the choice for all machines (machine empty) or one;
// for one, an empty policy goes back to the choice for all.
func (c *core) setUpdatePolicy(machine, p string) error {
	if p != "" && !validPolicy(p) || p == "" && machine == "" {
		return fmt.Errorf("unknown update policy %q", p)
	}
	key := "update_policy"
	if machine != "" {
		key += ":" + machine
	}
	if p == "" {
		_, err := c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, key)
		return err
	}
	return c.store.set(key, p)
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

// coreUpdater is what the updater of eriphe-core's machine says, in
// eriphe-core's data folder.
func (c *core) coreUpdater() updaterInfo {
	dir := c.store.dir
	up := keyValues(filepath.Join(dir, "updater"))
	info := updaterInfo{Updater: up != nil, Pin: up["pin"]}
	if _, err := os.Stat(filepath.Join(dir, "update-requested")); err == nil {
		var r machineUpdate
		if json.Unmarshal([]byte(c.store.get("update_request:"+coreMachine)), &r) == nil {
			info.Requested = r.ID
		}
	}
	if st := keyValues(filepath.Join(dir, "update-status")); st != nil {
		s := &updateStatus{ID: st["id"], Action: st["action"], Outcome: st["outcome"], Message: st["message"],
			From: st["from"], To: st["to"]}
		var at int64
		fmt.Sscan(st["at"], &at)
		if at > 0 {
			s.At = time.Unix(at, 0)
		}
		info.Status = s
	}
	return info
}

// machineUpdater is a machine's updater, as last reported.
func (c *core) machineUpdater(machine string) updaterInfo {
	if machine == coreMachine {
		return c.coreUpdater()
	}
	nodes, _ := c.store.nodes()
	for _, n := range nodes {
		if "node:"+n.Name == machine {
			return parseInfo(n).Update
		}
	}
	return updaterInfo{}
}

func (c *core) pendingUpdate(machine string) *machineUpdate {
	raw := c.store.get("update_request:" + machine)
	if raw == "" {
		return nil
	}
	var r machineUpdate
	if json.Unmarshal([]byte(raw), &r) != nil {
		return nil
	}
	return &r
}

func newUpdateID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return "u-" + hex.EncodeToString(b)
}

// sameVersion tells whether a node runs eriphe-core's own build. Only
// published builds (sha-<commit>) compare: a local one (dev) never asks
// for an update.
func sameVersion(node, core string) bool {
	return !strings.HasPrefix(node, "sha-") || !strings.HasPrefix(core, "sha-") || node == core
}

// requestUpdate asks a machine's updater to update (to the latest version,
// or its pinned one) or to roll back. A node is updated only once
// eriphe-core runs the latest version.
func (c *core) requestUpdate(machine, action string, auto bool) (*machineUpdate, error) {
	if action != "update" && action != "rollback" {
		return nil, fmt.Errorf("unknown update action %q", action)
	}
	up := c.machineUpdater(machine)
	switch {
	case !up.Updater:
		return nil, errNoUpdater
	case action == "update" && auto && up.Pin != "":
		return nil, errPinned
	case action == "update" && machine != coreMachine && c.updates.behind("eriphe-core", version):
		return nil, errCoreFirst
	}
	if machine != coreMachine && !strings.HasPrefix(machine, "node:") {
		return nil, fmt.Errorf("unknown machine %q", machine)
	}
	r := &machineUpdate{ID: newUpdateID(), Action: action, Auto: auto, At: time.Now()}
	raw, _ := json.Marshal(r)
	if machine == coreMachine {
		a := "0"
		if auto {
			a = "1"
		}
		body := fmt.Sprintf("id=%s\naction=%s\ncore=%s\nauto=%s\n", r.ID, r.Action, version, a)
		path := filepath.Join(c.store.dir, "update-requested")
		if err := os.WriteFile(path+".new", []byte(body), 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(path+".new", path); err != nil {
			return nil, err
		}
	}
	if err := c.store.set("update_request:"+machine, string(raw)); err != nil {
		return nil, err
	}
	log.Printf("update: %s asked of %s (%s)", action, machine, map[bool]string{true: "automatic", false: "by hand"}[auto])
	if machine != coreMachine {
		c.bump() // the node reads it in its assignments
	}
	return r, nil
}

// nodeUpdateRequest is the request a node finds in its assignments.
func (c *core) nodeUpdateRequest(name string) *nodeUpdate {
	r := c.pendingUpdate("node:" + name)
	if r == nil {
		return nil
	}
	return &nodeUpdate{ID: r.ID, Action: r.Action, Core: version, Auto: r.Auto}
}

// resolveUpdates drops the requests that are done (the updater wrote their
// outcome) or too old. It runs with the nodes' check, every 15 seconds.
func (c *core) resolveUpdates(nodes []NodeRow) {
	machines := []string{coreMachine}
	infos := map[string]updaterInfo{coreMachine: c.coreUpdater()}
	for _, n := range nodes {
		m := "node:" + n.Name
		machines = append(machines, m)
		infos[m] = parseInfo(n).Update
	}
	rows, _ := c.store.db.Query(`SELECT key FROM kv WHERE key LIKE 'update_request:%'`)
	var keys []string
	for rows != nil && rows.Next() {
		var k string
		rows.Scan(&k)
		keys = append(keys, k)
	}
	if rows != nil {
		rows.Close()
	}
	for _, k := range keys {
		m := strings.TrimPrefix(k, "update_request:")
		r := c.pendingUpdate(m)
		info, known := infos[m]
		var drop string
		switch {
		case r == nil || !known:
			drop = "forgotten"
		case info.Status != nil && info.Status.ID == r.ID && info.Status.Outcome != "running":
			drop = info.Status.Outcome
		case time.Since(r.At) > requestTTL:
			drop = "not done within a day"
		}
		if drop == "" {
			continue
		}
		c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, k)
		if r != nil {
			log.Printf("update: %s of %s: %s", r.Action, m, drop)
		}
		if m == coreMachine {
			os.Remove(filepath.Join(c.store.dir, "update-requested"))
		} else {
			c.bump()
		}
	}
}

// updating tells whether a node has a request not done yet: no new task
// goes there meanwhile, like a cordoned Kubernetes node.
func (c *core) updating(name string) bool {
	return c.pendingUpdate("node:"+name) != nil
}

// schedulable marks the nodes being updated as offline for the scheduler:
// a task then waits for them rather than starting there.
func (c *core) schedulable(nodes []NodeRow) []NodeRow {
	var out []NodeRow
	for _, n := range nodes {
		if n.Online && c.updating(n.Name) {
			n.Online = false
		}
		out = append(out, n)
	}
	return out
}

// autoUpdates runs at night for the machines set to update automatically:
// eriphe-core's machine first; a node once eriphe-core runs the latest
// version and the node runs another, and none of its tasks is at work.
// Each machine is tried once a night.
func (c *core) autoUpdates(now time.Time) {
	loc := c.conf().location
	if loc == nil {
		loc = time.Local
	}
	local := now.In(loc)
	if local.Hour() < nightStart || local.Hour() >= nightEnd {
		return
	}
	night := local.Format("2006-01-02")
	try := func(machine string) {
		if _, eff := c.updatePolicy(machine); eff != updateNight || c.pendingUpdate(machine) != nil ||
			c.store.get("update_auto:"+machine) == night {
			return
		}
		c.store.set("update_auto:"+machine, night)
		if _, err := c.requestUpdate(machine, "update", true); err != nil {
			log.Printf("update: no automatic update of %s: %v", machine, err)
		}
	}
	if c.updates.behind("eriphe-core", version) {
		if up := c.coreUpdater(); up.Updater && up.Pin == "" {
			try(coreMachine)
		}
		return // the nodes wait for eriphe-core
	}
	nodes, err := c.store.nodes()
	if err != nil {
		return
	}
	for _, n := range nodes {
		info := parseInfo(n)
		if !n.Online || !info.Update.Updater || info.Update.Pin != "" || sameVersion(info.Version, version) {
			continue
		}
		if active, err := c.store.activeCount(n.Name); err != nil || active > 0 {
			continue
		}
		try("node:" + n.Name)
	}
}

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
// and a node only once none of its tasks is at work. A node asked by hand
// while eriphe-core is behind waits for it: eriphe-core is asked first, and
// the node follows once it runs the latest version (followUpdates).
//
// eriphe-core's own machine reads the request from eriphe-core's data folder
// (update-requested), and writes how it went next to it (update-status); a
// node gets its request in its assignments and hands it to its updater the
// same way, in its own root (node/update.go). In both places, the file
// "updater" says there is one, and the version it is pinned to.
//
// An update also brings a machine to the install's layout of its new
// version (what an install puts there besides the images: services,
// LaunchAgents, Syncthing): the updater runs that version's installer by
// itself. What it cannot do alone (a step that needs the Mac's password or
// an administrator's PowerShell, or any step under an updater from before
// the layouts) the node reports as its needs, and the machine's card says
// « Cette mise à jour a besoin de toi », with the command to run; the brain
// is told once (tellNeeds).
//
// The workspace always says when a new version is out; one switch, for all
// machines, lets them update by themselves at night (off by default).
// Going back to the version before, or staying on one, is the install
// command's (--rollback, --version): the request "rollback" stays for the
// API and the tests.

const (
	updateNightKey = "update_night" // "1": the machines update at night
	// updateAllKey holds when "Update all" was asked while eriphe-core was
	// behind: the nodes follow once it runs the latest version.
	updateAllKey = "update_all"
	// afterCoreKey+machine holds when a node was asked by hand while
	// eriphe-core was behind: it follows once eriphe-core runs the latest.
	afterCoreKey = "update_after_core:"

	coreMachine = "core" // eriphe-core's machine; a node is "node:<name>"

	nightStart = 3 // automatic updates run from 3:00 to 5:00, the user's time
	nightEnd   = 5

	requestTTL = 24 * time.Hour // a request not done by then is dropped
	// A request with the updater (handed to it, or under way) that has not
	// ended by then stopped: its updater died or was stopped (a reinstall
	// restarts it). It is dropped, and the machine's line says so, rather
	// than keeping a node unschedulable for a day.
	stalledAfter = 30 * time.Minute
	// stalledKey+machine is the request dropped as stalled, for its line.
	stalledKey = "update_stalled:"
	// needsSeenKey+machine is when its needs were first seen ("why time"),
	// needsToldKey+machine what the brain was told of them (tellNeeds).
	needsSeenKey = "update_needs_seen:"
	needsToldKey = "update_needs_told:"
	// needsQuiet: needs seen for that long, with no request under way, are
	// told to the brain (an update clears its own in between).
	needsQuiet = 10 * time.Minute
)

var (
	errNoUpdater = errors.New("update.noupdater")
	errCoreFirst = errors.New("update.corefirst")
	errPinned    = errors.New("update.pinnedno")
)

// updaterInfo is what a machine's updater says, through the node's info
// (node/update.go) or eriphe-core's data folder.
type updaterInfo struct {
	Updater   bool          `json:"updater"`
	Pin       string        `json:"pin,omitempty"`       // the release it is pinned to
	Channel   string        `json:"channel,omitempty"`   // edge: it follows main, not the releases
	Waiting   string        `json:"waiting,omitempty"`   // a request waits for the tasks to end
	Requested string        `json:"requested,omitempty"` // a request is with the updater
	Status    *updateStatus `json:"status,omitempty"`    // how the last one went
	Layout    int           `json:"layout,omitempty"`    // the install's layout (node/update.go)
	Needs     *updateNeeds  `json:"needs,omitempty"`     // what the install waits for from its user
}

// updateNeeds is what a machine's install waits for from its user: its
// install command run once more (layout), in the Mac's Terminal for a step
// that needs its password (password), or in an administrator's PowerShell
// (admin). The node says only which: the command is eriphe-core's, for how
// the machine was installed (manualUpdate), never one a node names.
type updateNeeds struct {
	Why string `json:"why"`
}

// Reason is Why, one of the three; anything else is layout.
func (n *updateNeeds) Reason() string {
	switch n.Why {
	case "password", "admin":
		return n.Why
	}
	return "layout"
}

// needsBrain is what the brain is told of each reason.
var needsBrain = map[string]string{
	"layout":   "relancer une fois sa commande d'installation : elle ajoute ce que sa nouvelle version demande, et ses mises à jour le feront ensuite seules",
	"password": "relancer sa commande d'installation dans le Terminal du Mac : une étape demande son mot de passe, le reste est déjà fait",
	"admin":    "relancer sa commande d'installation dans un PowerShell administrateur : une étape demande les droits d'administrateur de Windows, le reste est déjà fait",
}

// tellNeeds tells the brain, once, what a machine's update needs of the
// user, once it has been so for needsQuiet with no request under way; it
// forgets once the machine needs nothing (the user ran the command).
func (c *core) tellNeeds(nodes []NodeRow) {
	now := time.Now()
	for _, n := range nodes {
		m := "node:" + n.Name
		why := ""
		if nd := parseInfo(n).Update.Needs; nd != nil {
			why = nd.Reason()
		}
		if why == "" {
			c.store.db.Exec(`DELETE FROM kv WHERE key IN (?, ?)`, needsSeenKey+m, needsToldKey+m)
			continue
		}
		if c.store.get(needsToldKey+m) == why || c.pendingUpdate(m) != nil {
			continue
		}
		seenWhy, at, _ := strings.Cut(c.store.get(needsSeenKey+m), " ")
		since, err := time.Parse(time.RFC3339, at)
		if seenWhy != why || err != nil {
			c.store.set(needsSeenKey+m, why+" "+now.UTC().Format(time.RFC3339))
			continue
		}
		if now.Sub(since) < needsQuiet {
			continue
		}
		c.store.set(needsToldKey+m, why)
		log.Printf("update: %s needs its user (%s)", m, why)
		if c.conf().brain() == nil {
			continue
		}
		text := fmt.Sprintf("La mise à jour de l'appareil %s a besoin de %s : %s. Réglages › Appareils lui donne la commande à copier (« Cette mise à jour a besoin de toi »). Dis-le-lui une fois, au bon moment, sans insister.",
			n.Name, c.conf().User, needsBrain[why])
		if _, err := c.store.addMessage(roleEvent, text, false); err == nil {
			c.ping()
		}
	}
}

// updateStatus is the outcome of a request: running, ok, current (nothing
// newer), pinned, refused (it would be newer than eriphe-core), skipped
// (failed here before), failed (nothing changed), rolled_back,
// interrupted (the updater stopped before its end, said by its next run),
// or stalled (eriphe-core gave up waiting for it: resolveUpdates).
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

// nightUpdates tells whether the machines update by themselves at night.
func (c *core) nightUpdates() bool { return c.store.get(updateNightKey) == "1" }

func (c *core) setNightUpdates(on bool) error {
	if !on {
		_, err := c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, updateNightKey)
		return err
	}
	return c.store.set(updateNightKey, "1")
}

// manualUpdate is the one command that updates a machine by hand, for how
// it was installed: its install command again (which also gives it an
// updater), or the compose project's pull for one made by hand.
func manualUpdate(install string) updateCommand {
	return updateCommands(install)[0]
}

// updateAll asks every machine that is behind to update, eriphe-core's
// first: while it is behind, only it is asked, and the nodes follow once it
// runs the latest version (followUpdates). It says how many were asked.
func (c *core) updateAll() (int, error) {
	if c.updates.behind("eriphe-core", version) {
		if c.pendingUpdate(coreMachine) == nil {
			if _, err := c.requestUpdate(coreMachine, "update", false); err != nil {
				return 0, err
			}
		}
		return 1, c.store.set(updateAllKey, time.Now().UTC().Format(time.RFC3339))
	}
	return c.updateNodes(), nil
}

// updateNodes asks the nodes that run another version than eriphe-core's,
// and have an updater, to update. A node at work waits for its tasks.
func (c *core) updateNodes() int {
	nodes, err := c.store.nodes()
	if err != nil {
		return 0
	}
	n := 0
	for _, row := range nodes {
		info := parseInfo(row)
		m := "node:" + row.Name
		if !info.Update.Updater || c.pendingUpdate(m) != nil ||
			!c.updates.behind("eriphe-node", info.Version) && !nodeBehindCore(info.Version, version) {
			continue
		}
		if _, err := c.requestUpdate(m, "update", false); err != nil {
			log.Printf("update: %s not asked: %v", m, err)
			continue
		}
		n++
	}
	return n
}

// updateNow asks a machine to update, by hand. A node while eriphe-core is
// behind waits for it: eriphe-core is asked first (unless it already is),
// and the node follows once it runs the latest version (followUpdates).
// afterCore tells so; a node whose eriphe-core has no updater gets
// errCoreFirst, as nothing here can update eriphe-core.
func (c *core) updateNow(machine string) (afterCore bool, err error) {
	if machine == coreMachine || !c.updates.behind("eriphe-core", version) {
		_, err := c.requestUpdate(machine, "update", false)
		return false, err
	}
	if !c.machineUpdater(machine).Updater {
		return false, errNoUpdater
	}
	if !c.coreUpdater().Updater {
		return false, errCoreFirst
	}
	if c.pendingUpdate(coreMachine) == nil {
		if _, err := c.requestUpdate(coreMachine, "update", false); err != nil {
			return false, err
		}
	}
	log.Printf("update: %s follows eriphe-core's update", machine)
	return true, c.store.set(afterCoreKey+machine, time.Now().UTC().Format(time.RFC3339))
}

// afterCore tells whether a machine waits for eriphe-core's update.
func (c *core) afterCore(machine string) bool {
	return c.store.get(afterCoreKey+machine) != ""
}

// followUpdates asks the nodes waiting for eriphe-core ("Update all", or
// one asked by hand) once eriphe-core runs the latest version. Once
// eriphe-core's update is over without reaching it (failed, rolled back,
// pinned), they are not asked: they would take eriphe-core's build, the one
// they run. A wait gives up after a day.
func (c *core) followUpdates() {
	rows, err := c.store.db.Query(`SELECT key, value FROM kv WHERE key = ? OR key LIKE ?`, updateAllKey, afterCoreKey+"%")
	if err != nil {
		return
	}
	waits := map[string]string{}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		waits[k] = v
	}
	rows.Close()
	// Just restarted, eriphe-core has not read the latest versions yet.
	if len(waits) == 0 || c.updates.checked().IsZero() || c.pendingUpdate(coreMachine) != nil {
		return
	}
	behind := c.updates.behind("eriphe-core", version)
	for k, at := range waits {
		t, err := time.Parse(time.RFC3339, at)
		machine := strings.TrimPrefix(k, afterCoreKey)
		switch {
		case err != nil || time.Since(t) > requestTTL:
			log.Printf("update: %s not asked: eriphe-core did not update within a day", k)
		case behind:
			outcome := "no outcome"
			if st := c.coreUpdater().Status; st != nil {
				outcome = st.Outcome
			}
			log.Printf("update: %s not asked: eriphe-core did not reach the latest version (%s)", k, outcome)
		case k == updateAllKey:
			log.Printf("update: eriphe-core is up to date, the nodes follow")
			c.updateNodes()
		case c.pendingUpdate(machine) == nil:
			log.Printf("update: eriphe-core is up to date, %s follows", machine)
			if _, err := c.requestUpdate(machine, "update", false); err != nil {
				log.Printf("update: %s not asked: %v", machine, err)
			}
		}
		c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, k)
	}
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
	info := updaterInfo{Updater: up != nil, Pin: up["pin"], Channel: up["channel"]}
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
// published builds compare (a release, a build of main, or one from before
// the releases): a local one (dev) never asks for an update.
func sameVersion(node, core string) bool {
	return node == core || !publishedVersion(node) || !publishedVersion(core)
}

// nodeBehindCore tells whether a node runs an older build than
// eriphe-core's, which it then goes to: never a newer one (a node updated
// before eriphe-core), which would be a downgrade. Two builds of main of the
// same version cannot be ordered: the node follows eriphe-core's.
func nodeBehindCore(node, core string) bool {
	if sameVersion(node, core) {
		return false
	}
	if legacyVersion(node) {
		return true
	}
	if legacyVersion(core) {
		return false
	}
	n, ok := parseVersion(node)
	c, ok2 := parseVersion(core)
	if !ok || !ok2 {
		return false
	}
	return n.compare(c) <= 0
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
	c.store.db.Exec(`DELETE FROM kv WHERE key = ?`, stalledKey+machine)
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
	if r.Action == "update" {
		nodes, _ := c.store.nodes()
		for _, n := range nodes {
			if n.Name == name && c.atCoreVersion(parseInfo(n)) {
				return nil // reinstalled meanwhile: resolveUpdates drops it
			}
		}
	}
	u := &nodeUpdate{ID: r.ID, Action: r.Action, Core: version, Auto: r.Auto}
	// A node from before the releases (sha-<commit>) drops a request with a
	// version it cannot read: without one, its updater takes latest.
	if !legacyVersion(version) {
		nodes, _ := c.store.nodes()
		for _, n := range nodes {
			if n.Name == name && legacyVersion(parseInfo(n).Version) {
				u.Core = ""
			}
		}
	}
	return u
}

// atCoreVersion tells whether a node already runs eriphe-core's own
// release, the most an update can give it (its install command was run
// again, say), and no updater is at work on it: an update request has
// nothing left to do there.
func (c *core) atCoreVersion(info nodeInfo) bool {
	if info.Version != version || !publishedVersion(version) || c.updates.behind("eriphe-node", info.Version) {
		return false
	}
	return info.Update.Status == nil || info.Update.Status.Outcome != "running"
}

// stalled tells whether a request is with the updater (handed to it, or
// under way) and has not ended within stalledAfter.
func stalled(r *machineUpdate, info updaterInfo, now time.Time) bool {
	since := time.Time{}
	switch st := info.Status; {
	case st != nil && st.ID == r.ID && st.Outcome == "running":
		since = st.At
		if since.IsZero() {
			since = r.At
		}
	case info.Requested == r.ID:
		since = r.At
	default:
		return false
	}
	return now.Sub(since) > stalledAfter
}

// stalledStatus is the line of a machine whose request was dropped as
// stalled, while its updater says nothing newer about it.
func (c *core) stalledStatus(machine string, st *updateStatus) *updateStatus {
	var r machineUpdate
	if json.Unmarshal([]byte(c.store.get(stalledKey+machine)), &r) != nil {
		return st
	}
	if st != nil && (st.ID != r.ID || st.Outcome != "running") {
		return st
	}
	out := &updateStatus{ID: r.ID, Action: r.Action, Outcome: "stalled", At: r.At}
	if st != nil {
		out.From, out.At = st.From, st.At
	}
	return out
}

// resolveUpdates drops the requests that are done (the updater wrote their
// outcome), stalled, too old, or left with nothing to do (the node runs
// eriphe-core's version already). It runs with the nodes' check, every 15
// seconds.
func (c *core) resolveUpdates(nodes []NodeRow) {
	machines := []string{coreMachine}
	infos := map[string]updaterInfo{coreMachine: c.coreUpdater()}
	atCore := map[string]bool{}
	for _, n := range nodes {
		m := "node:" + n.Name
		machines = append(machines, m)
		info := parseInfo(n)
		infos[m] = info.Update
		atCore[m] = c.atCoreVersion(info)
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
		case r.Action == "update" && atCore[m]:
			drop = "it runs eriphe-core's version already"
		case stalled(r, info, time.Now()):
			drop = "stalled: the updater did not end it"
			raw, _ := json.Marshal(r)
			c.store.set(stalledKey+m, string(raw))
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
	c.tellNeeds(nodes)
}

// updating tells whether a node has a request not done yet: no new task
// goes there meanwhile, like a cordoned Kubernetes node.
func (c *core) updating(name string) bool {
	return c.pendingUpdate("node:"+name) != nil
}

// schedulable marks the nodes being updated as offline for the scheduler:
// a task then waits for them rather than starting there. A node whose
// access is cut is left out.
func (c *core) schedulable(nodes []NodeRow) []NodeRow {
	var out []NodeRow
	for _, n := range nodes {
		if n.Suspended {
			continue // its access is cut: no work goes there (node_access.go)
		}
		if n.Online && c.updating(n.Name) {
			n.Online = false
		}
		out = append(out, n)
	}
	return out
}

// autoUpdates runs at night once the machines update automatically:
// eriphe-core's machine first; a node once eriphe-core runs the latest
// version and the node runs another, and none of its tasks is at work.
// Each machine is tried once a night.
func (c *core) autoUpdates(now time.Time) {
	if !c.nightUpdates() {
		return
	}
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
		if c.pendingUpdate(machine) != nil || c.store.get("update_auto:"+machine) == night {
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
		if !n.Online || !info.Update.Updater || info.Update.Pin != "" || !nodeBehindCore(info.Version, version) {
			continue
		}
		if active, err := c.store.activeCount(n.Name); err != nil || active > 0 {
			continue
		}
		try("node:" + n.Name)
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Copies of a shared folder on the user's other machines, kept by Syncthing
// (node/syncfolders.go), through eriphe-core's tunnels (sync_relay.go).
//
// Only a folder its machine allows copies of (copyable, chosen when it was
// shared there, and never turned back on from here) is copied, to the
// machines the user ticks on Settings → Folders. eriphe-core keeps which
// machine holds a copy of which folder (folder_copies), and hands each node
// its plan (PUT /v1/sync): the peers, with their Syncthing device IDs, the
// folders it sends and the copies it keeps, each at the folder's level now.
// The nodes decide the rest: the folder's machine sends only what is shared
// and copyable there, at most at its level there; a copy lives only in the
// copy machine's data. Turning copies off, lowering a folder to "not
// copyable", unsharing it or forgetting a machine removes the copies from
// the plan: the copy machines then stop the tasks working on them and
// delete them.
//
// It does not matter which machine owns a folder: a task lent folders may
// run on any machine that holds each of them up to date (folderHosts), the
// folder's own machine or one keeping a copy in sync now, with nothing
// pending either way; while a folder's machine is away, a copy that was
// complete when that machine was last seen will do (syncGrace). The
// scheduler picks the best of them, as for any task (scheduler.go). With
// drop or manage the task writes on the copy (for manage, after a snapshot
// of the copy, on the machine running it), and Syncthing brings that back
// to the folder's machine, at once or when it returns. The task's level is
// never above the folder's on its machine.

// syncGrace is how close to its folder machine's last moment a copy must
// have been complete to stand in for it.
const syncGrace = 2 * time.Minute

// syncState is a synced folder's state on a machine (node/syncfolders.go).
type syncState struct {
	ID        string    `json:"id"`
	Share     string    `json:"share"`
	Source    string    `json:"source"`
	Role      string    `json:"role"`
	State     string    `json:"state"`
	Done      float64   `json:"done"`
	NeedBytes int64     `json:"need_bytes"`
	Bytes     int64     `json:"bytes"`
	Error     string    `json:"error,omitempty"`
	ICloud    bool      `json:"icloud,omitempty"`
	SyncedAt  time.Time `json:"synced_at,omitzero"`
}

// nodeSyncthing is what a node reports of its Syncthing.
type nodeSyncthing struct {
	DeviceID string      `json:"device_id"`
	Folders  []syncState `json:"folders"`
}

// syncFolderID is a folder's id in Syncthing, the same on every machine.
func syncFolderID(node, share string) string { return "e-" + node + "-" + share }

// nodePlan is what a node is asked to sync (node/syncfolders.go syncPlan).
type nodePlan struct {
	Peers   []planPeer   `json:"peers"`
	Folders []planFolder `json:"folders"`
}

type planPeer struct {
	Name     string `json:"name"`
	DeviceID string `json:"device_id"`
}

type planFolder struct {
	ID      string   `json:"id"`
	Share   string   `json:"share"`
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Role    string   `json:"role"` // source or copy
	Access  string   `json:"access"`
	Results string   `json:"results,omitempty"`
	Peers   []string `json:"peers"`
	// Paused: kept as it is, not synced, while a machine of the pair is
	// suspended or has no Syncthing for now (an older version back).
	Paused bool `json:"paused,omitempty"`
}

// syncView is what the plans are made of, read once.
type syncView struct {
	nodes   map[string]NodeRow
	folders map[string]*Folder // by node/id
	copies  []folderCopy
}

func (c *core) syncView() (*syncView, error) {
	rows, err := c.store.nodes()
	if err != nil {
		return nil, err
	}
	fs, err := c.store.folders()
	if err != nil {
		return nil, err
	}
	copies, err := c.store.copies()
	if err != nil {
		return nil, err
	}
	v := &syncView{nodes: map[string]NodeRow{}, folders: map[string]*Folder{}, copies: copies}
	for _, n := range rows {
		v.nodes[n.Name] = n
	}
	for _, f := range fs {
		v.folders[f.Node+"/"+f.ID] = f
	}
	return v, nil
}

// deviceID is a machine's Syncthing device ID, as it last reported it.
func (v *syncView) deviceID(node string) string {
	n, ok := v.nodes[node]
	if !ok || n.Suspended {
		return ""
	}
	if st := parseInfo(n).Syncthing; st != nil {
		return st.DeviceID
	}
	return ""
}

// live is whether a copy is synced now: its folder still shared and
// copyable, both machines there with Syncthing. A copy whose folder is
// still copyable but a machine suspended or without Syncthing for now is
// planned paused: kept, not deleted.
func (v *syncView) live(fc folderCopy) (*Folder, bool) {
	f := v.folders[fc.Node+"/"+fc.Folder]
	return f, f != nil && f.Copyable && v.deviceID(fc.Node) != "" && v.deviceID(fc.Copy) != ""
}

// plan is what node is asked to sync.
func (c *core) plan(v *syncView, node string) nodePlan {
	p := nodePlan{Peers: []planPeer{}, Folders: []planFolder{}}
	peers := map[string]bool{}
	sources := map[string]int{} // folder id → its entry
	for _, fc := range v.copies {
		if fc.Node != node && fc.Copy != node {
			continue
		}
		f, ok := v.live(fc)
		if f == nil || !f.Copyable {
			continue
		}
		pf := planFolder{ID: syncFolderID(f.Node, f.ID), Share: f.ID, Name: f.Name, Source: f.Node, Access: f.Access}
		if f.Access == folderDrop {
			pf.Results = c.resultsName()
		}
		if !ok {
			// Paused, with no peer: kept as it is until both are back.
			if fc.Copy == node && !slices.ContainsFunc(p.Folders, func(g planFolder) bool { return g.ID == pf.ID }) {
				pf.Role, pf.Peers, pf.Paused = "copy", []string{}, true
				p.Folders = append(p.Folders, pf)
			}
			continue
		}
		peer := fc.Node
		if fc.Node == node {
			peer = fc.Copy
			if i, ok := sources[pf.ID]; ok {
				p.Folders[i].Peers = append(p.Folders[i].Peers, peer)
			} else {
				pf.Role, pf.Peers = "source", []string{peer}
				sources[pf.ID] = len(p.Folders)
				p.Folders = append(p.Folders, pf)
			}
		} else {
			pf.Role, pf.Peers = "copy", []string{peer}
			p.Folders = append(p.Folders, pf)
		}
		if !peers[peer] {
			peers[peer] = true
			p.Peers = append(p.Peers, planPeer{Name: peer, DeviceID: v.deviceID(peer)})
		}
	}
	return p
}

// syncAll forgets the copies that may no longer be (their folder unshared,
// no longer copyable, a machine forgotten), hands every node with Syncthing
// its plan when it changed, and cuts the connections of the machines that
// no longer share a copy.
func (c *core) syncAll(ctx context.Context) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	v, err := c.syncView()
	if err != nil {
		log.Printf("copies: %v", err)
		return
	}
	for _, fc := range v.copies {
		f := v.folders[fc.Node+"/"+fc.Folder]
		_, copyKnown := v.nodes[fc.Copy]
		if f == nil || !f.Copyable || !copyKnown {
			log.Printf("copies: the copy of %s/%s on %s goes: the folder may no longer be copied there", fc.Node, fc.Folder, fc.Copy)
			c.store.removeCopy(fc.Node, fc.Folder, fc.Copy)
		}
	}
	if v, err = c.syncView(); err != nil {
		return
	}
	for name, n := range v.nodes {
		if v.deviceID(name) == "" || !n.Online {
			continue
		}
		c.pushPlan(ctx, name, c.plan(v, name))
	}
	c.cutUnpaired()
}

// pushPlan hands a node its plan, unless it has it already.
func (c *core) pushPlan(ctx context.Context, node string, p nodePlan) {
	raw, _ := json.Marshal(p)
	if last, ok := c.syncPushed.Load(node); ok && last.(string) == string(raw) {
		return
	}
	n := c.node(node)
	if !n.connected() {
		return
	}
	var out struct {
		Problems map[string]string `json:"problems"`
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := n.call(cctx, http.MethodPut, "/v1/sync", p, &out); err != nil {
		log.Printf("copies: %s did not take its plan: %v", node, err)
		return
	}
	c.syncPushed.Store(node, string(raw))
	for id, why := range out.Problems {
		log.Printf("copies: %s leaves %s out: %s", node, id, why)
	}
}

// setCopies makes the machines that keep a copy of a folder exactly those
// named: the copies added are made, those left out deleted on their machine.
func (c *core) setCopies(ctx context.Context, name string, machines []string) error {
	f, err := c.store.folder(name)
	if err != nil {
		return err
	}
	if len(machines) > 0 && !f.Copyable {
		return &folderError{Code: "not_copyable", Msg: fmt.Sprintf("folder %q may not be copied: allow it when sharing it on %s", f.Name, f.Node)}
	}
	v, err := c.syncView()
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, m := range machines {
		m = strings.TrimSpace(m)
		if _, ok := v.nodes[m]; !ok || m == f.Node {
			return &folderError{Code: "invalid", Msg: fmt.Sprintf("%q: another of your machines", m)}
		}
		if v.deviceID(m) == "" {
			return &folderError{Code: "no_syncthing", Msg: fmt.Sprintf("%s keeps no copy yet: update it (Syncthing comes with the installation)", m)}
		}
		want[m] = true
	}
	if v.deviceID(f.Node) == "" && len(want) > 0 {
		return &folderError{Code: "no_syncthing", Msg: fmt.Sprintf("%s cannot send copies yet: update it (Syncthing comes with the installation)", f.Node)}
	}
	for _, fc := range v.copies {
		if fc.Node == f.Node && fc.Folder == f.ID && !want[fc.Copy] {
			if err := c.store.removeCopy(fc.Node, fc.Folder, fc.Copy); err != nil {
				return err
			}
			c.logNode(fc.Copy, NodeLogEntry{Kind: logCopy, Folder: f.Name, Detail: "copy deleted"})
		}
	}
	for m := range want {
		if !slices.ContainsFunc(v.copies, func(fc folderCopy) bool { return fc.Node == f.Node && fc.Folder == f.ID && fc.Copy == m }) {
			if err := c.store.addCopy(f.Node, f.ID, m); err != nil {
				return err
			}
			c.logNode(m, NodeLogEntry{Kind: logCopy, Folder: f.Name, Detail: "copy from " + f.Node})
		}
	}
	c.syncAll(ctx)
	return nil
}

// dropCopies forgets every copy of a folder (unshared, or no longer
// copyable): their machines delete them.
func (c *core) dropCopies(ctx context.Context, f *Folder) {
	copies, err := c.store.copies()
	if err != nil {
		return
	}
	for _, fc := range copies {
		if fc.Node == f.Node && fc.Folder == f.ID {
			c.store.removeCopy(fc.Node, fc.Folder, fc.Copy)
		}
	}
	c.syncAll(ctx)
}

// forgetMachineCopies forgets the copies a machine kept or sent.
func (s *Store) forgetMachineCopies(node string) {
	s.db.Exec(`DELETE FROM folder_copies WHERE node = ? OR copy = ?`, node, node)
}

// copyView is one copy of a folder, as Settings → Folders shows it.
type copyView struct {
	Node   string  `json:"node"`
	Online bool    `json:"online"`
	Status string  `json:"status"` // ok (up to date), syncing, behind, error, waiting
	Done   float64 `json:"done"`   // %, while syncing
	Bytes  int64   `json:"bytes"`  // what it takes on that machine
	Error  string  `json:"error,omitempty"`
}

// copyChoice is a machine the user may tick to keep a copy.
type copyChoice struct {
	Node    string
	Checked bool
	Able    bool // it runs Syncthing
}

// copyStatus is a copy's state: up to date, syncing, behind, an error, or
// waiting (not synced there yet).
func (v *syncView) copyStatus(f *Folder, node string) copyView {
	cv := copyView{Node: node, Online: v.nodes[node].Online, Status: "waiting"}
	st := parseInfo(v.nodes[node]).Syncthing
	if st == nil {
		return cv
	}
	id := syncFolderID(f.Node, f.ID)
	i := slices.IndexFunc(st.Folders, func(s syncState) bool { return s.ID == id && s.Role == "copy" })
	if i < 0 {
		return cv
	}
	s := st.Folders[i]
	cv.Done, cv.Bytes = s.Done, s.Bytes
	src := v.nodes[f.Node]
	switch {
	case s.State == "missing":
	case s.Error != "":
		cv.Status, cv.Error = "error", s.Error
	case src.Online && s.NeedBytes == 0 && s.State == "idle":
		cv.Status = "ok"
	case src.Online:
		cv.Status = "syncing"
	case !s.SyncedAt.IsZero() && !s.SyncedAt.Before(src.LastSeen.Add(-syncGrace)):
		cv.Status = "ok" // complete when its folder's machine was last seen
	default:
		cv.Status = "behind"
	}
	return cv
}

// copiesOf are a folder's copies, and the machines that may keep one.
func (v *syncView) copiesOf(f *Folder) ([]copyView, []copyChoice) {
	var out []copyView
	have := map[string]bool{}
	for _, fc := range v.copies {
		if fc.Node == f.Node && fc.Folder == f.ID {
			out = append(out, v.copyStatus(f, fc.Copy))
			have[fc.Copy] = true
		}
	}
	var choices []copyChoice
	for name, n := range v.nodes {
		if name == f.Node || n.Suspended {
			continue
		}
		choices = append(choices, copyChoice{Node: name, Checked: have[name], Able: v.deviceID(name) != ""})
	}
	slices.SortFunc(choices, func(a, b copyChoice) int { return strings.Compare(a.Node, b.Node) })
	slices.SortFunc(out, func(a, b copyView) int { return strings.Compare(a.Node, b.Node) })
	return out, choices
}

// iCloudHint: the folder's machine says Syncthing cannot read files that
// iCloud Drive keeps in the cloud only.
func (v *syncView) iCloudHint(f *Folder) bool {
	st := parseInfo(v.nodes[f.Node]).Syncthing
	if st == nil {
		return false
	}
	id := syncFolderID(f.Node, f.ID)
	return slices.ContainsFunc(st.Folders, func(s syncState) bool { return s.ID == id && s.ICloud })
}

// folderHosts are the machines a task lent fs may run on now, by name:
// for each folder, its own machine, sharing it as it last reported, or one
// that keeps a copy of it in sync (copyUsable). Which machine owns a
// folder does not matter: the scheduler picks the best of them
// (scheduler.go).
func (c *core) folderHosts(fs []*Folder) []string {
	v, err := c.syncView()
	if err != nil || len(fs) == 0 {
		return nil
	}
	var out []string
	for name := range v.nodes {
		if !slices.ContainsFunc(fs, func(f *Folder) bool { return !v.holdsNow(f, name) }) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// holdsNow tells whether node may lend f to a task now: its own machine
// sharing it, or its copy in sync.
func (v *syncView) holdsNow(f *Folder, node string) bool {
	if node == f.Node {
		return slices.ContainsFunc(parseInfo(v.nodes[node]).Folders, func(sf sharedFolder) bool { return sf.ID == f.ID })
	}
	return v.copyUsable(f, node)
}

// copyUsable tells whether node's copy of f may stand in for it now: kept
// and synced there, that machine online, and the copy in sync. With the
// folder's machine online, nothing may be pending either way: the copy
// idle with nothing to receive, the folder's machine idle with nothing to
// receive from its copies. With it away, the copy must have been complete
// when it was last seen (syncGrace).
func (v *syncView) copyUsable(f *Folder, node string) bool {
	n := v.nodes[node]
	if !n.Online || n.Suspended || !v.hasCopy(f, node) {
		return false
	}
	if _, live := v.live(folderCopy{Node: f.Node, Folder: f.ID, Copy: node}); !live || v.copyStatus(f, node).Status != "ok" {
		return false
	}
	return !v.nodes[f.Node].Online || v.sourceSettled(f)
}

// sourceSettled tells whether f's machine reports its side of the copies
// idle, with nothing to receive from them and no error.
func (v *syncView) sourceSettled(f *Folder) bool {
	st := parseInfo(v.nodes[f.Node]).Syncthing
	if st == nil {
		return false
	}
	id := syncFolderID(f.Node, f.ID)
	i := slices.IndexFunc(st.Folders, func(s syncState) bool { return s.ID == id && s.Role == "source" })
	return i >= 0 && st.Folders[i].State == "idle" && st.Folders[i].NeedBytes == 0 && st.Folders[i].Error == ""
}

// folderHolder is a machine holding a folder, for the brain (list_folders).
type folderHolder struct {
	Node     string `json:"node"`
	Role     string `json:"role"` // its machine, or a copy
	Online   bool   `json:"online"`
	UpToDate bool   `json:"up_to_date"` // a task may be lent it there now
}

// holdersOf are the machines holding f: its own, then those keeping a copy.
func (v *syncView) holdersOf(f *Folder) []folderHolder {
	out := []folderHolder{{Node: f.Node, Role: "machine", Online: v.nodes[f.Node].Online, UpToDate: v.holdsNow(f, f.Node)}}
	for _, fc := range v.copies {
		if fc.Node == f.Node && fc.Folder == f.ID {
			out = append(out, folderHolder{Node: fc.Copy, Role: "copy", Online: v.nodes[fc.Copy].Online, UpToDate: v.copyUsable(f, fc.Copy)})
		}
	}
	return out
}

// folderHolders are the machines that hold each of fs, up to date or not:
// its own machine, or one keeping a copy of it. A task lent fs is only
// ever placed on one of them, and waits for one while none is up to date.
func (c *core) folderHolders(fs []*Folder) []string {
	v, err := c.syncView()
	if err != nil || len(fs) == 0 {
		return nil
	}
	var out []string
	for name := range v.nodes {
		if !slices.ContainsFunc(fs, func(f *Folder) bool { return name != f.Node && !v.hasCopy(f, name) }) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func (v *syncView) hasCopy(f *Folder, node string) bool {
	return slices.ContainsFunc(v.copies, func(fc folderCopy) bool { return fc.Node == f.Node && fc.Folder == f.ID && fc.Copy == node })
}

// handleFolderCopies sets the machines that keep a copy of a folder (PUT
// /v1/folders/{name}/copies, {"nodes": [...]}).
func (c *core) handleFolderCopies(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Nodes []string `json:"nodes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		folderHTTPError(w, &folderError{Code: "invalid", Msg: `want {"nodes": ["machine", …]}`})
		return
	}
	if err := c.setCopies(r.Context(), r.PathValue("name"), body.Nodes); err != nil {
		folderHTTPError(w, err)
		return
	}
	views, err := c.folderViews()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, fv := range views {
		if fv.Source == sourceMachine && strings.EqualFold(fv.Name, r.PathValue("name")) {
			writeJSON(w, http.StatusOK, fv)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// Settings → Machines → Updates (machine_updates.go): each machine's
// version, a button to update the ones that are behind (or the command to
// run, for a machine without an updater), "Update all", and one switch to
// let them update by themselves at night.

type updateRow struct {
	Machine string // core, or node:<name>
	Name    string
	Core    bool
	Online  bool
	Version string
	Latest  string // the latest version, when this one is behind
	updaterInfo
	Pending   *machineUpdate
	CoreFirst bool          // a node, while eriphe-core is behind: eriphe-core goes first
	Manual    updateCommand // the command that updates it by hand, without an updater
	// Done: the request the page follows has just ended (handleUpdateState),
	// and its outcome shows once.
	Done bool
}

type updatesData struct {
	Night  bool // the machines update by themselves at night
	Rows   []updateRow
	Behind int // machines behind that an updater can update now
}

func (w *web) updatesData(nodes []NodeRow) updatesData {
	c := w.c
	d := updatesData{Night: c.nightUpdates()}
	coreBehind := c.updates.behind("eriphe-core", version)
	row := updateRow{Machine: coreMachine, Name: "eriphe-core", Core: true, Online: true, Version: version,
		updaterInfo: c.coreUpdater(), Pending: c.pendingUpdate(coreMachine), Manual: manualUpdate(coreInstall)}
	if coreBehind {
		row.Latest = c.updates.version("eriphe-core")
	}
	d.add(row)
	for _, n := range nodes {
		info := parseInfo(n)
		m := "node:" + n.Name
		row := updateRow{Machine: m, Name: n.Name, Online: n.Online, Version: info.Version,
			updaterInfo: info.Update, Pending: c.pendingUpdate(m), CoreFirst: coreBehind,
			Manual: manualUpdate(nodeInstall(n, info))}
		if c.updates.behind("eriphe-node", info.Version) {
			row.Latest = c.updates.version("eriphe-node")
		} else if !coreBehind && c.updates.version("eriphe-core") != "" && !sameVersion(info.Version, version) {
			row.Latest = version // eriphe-core's, the latest: where the node goes
		}
		d.add(row)
	}
	if core := d.Rows[0]; core.Latest != "" && !core.Updater {
		d.Behind = 0 // eriphe-core goes first, by its command: no button can do anything before
	}
	return d
}

func (d *updatesData) add(r updateRow) {
	// The updater wrote how the request went: it is done, even before
	// resolveUpdates drops it.
	if p, st := r.Pending, r.Status; p != nil && st != nil && st.ID == p.ID && st.Outcome != "running" {
		r.Pending = nil
	}
	if r.Latest != "" && r.Updater && r.Pending == nil {
		d.Behind++
	}
	d.Rows = append(d.Rows, r)
}

// nodeInstall is how a node was installed: as it reports it, or as it
// last did while it is offline.
func nodeInstall(n NodeRow, info nodeInfo) string {
	if info.Install != "" || n.Install == "" {
		return info.Install
	}
	return n.Install
}

// machineParam is the machine a form names, if it exists.
func (w *web) machineParam(m string) bool {
	if m == coreMachine {
		return true
	}
	name, ok := strings.CutPrefix(m, "node:")
	return ok && w.c.store.hasNode(name)
}

// handleNightUpdates turns the automatic updates at night on or off, for
// all machines.
func (w *web) handleNightUpdates(rw http.ResponseWriter, r *http.Request, s *session) {
	on := r.PostFormValue("night") != ""
	if err := w.c.setNightUpdates(on); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: automatic updates at night: %t", on)
	msg := "updates.nightoff"
	if on {
		msg = "updates.nighton"
	}
	w.toast(rw, tr(w.lang(r), msg), true)
}

// handleUpdateNow asks a machine's updater to update now.
func (w *web) handleUpdateNow(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	m := r.PostFormValue("machine")
	if !w.machineParam(m) {
		w.toast(rw, tr(lang, "machines.nonode"), false)
		return
	}
	if p := w.c.pendingUpdate(m); p != nil {
		w.toast(rw, tr(lang, "update.already"), false)
		return
	}
	if _, err := w.c.requestUpdate(m, "update", false); err != nil {
		w.updateError(rw, lang, err)
		return
	}
	w.updateState(rw, r, s, m, "")
}

// handleUpdateState is where a machine's update stands, for its card (or
// eriphe-core's row) to follow it: the page asks every few seconds while a
// request is pending, and the answer stops asking once it is done. id is
// the request the page follows, whose outcome then shows once.
func (w *web) handleUpdateState(rw http.ResponseWriter, r *http.Request, s *session) {
	m := coreMachine
	if name := r.PathValue("name"); name != "" {
		m = "node:" + name
	}
	w.updateState(rw, r, s, m, r.URL.Query().Get("id"))
}

func (w *web) updateState(rw http.ResponseWriter, r *http.Request, s *session, m, follow string) {
	nodes, err := w.c.store.nodes()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	d := w.updatesData(nodes)
	for i := range d.Rows {
		if row := &d.Rows[i]; follow != "" && row.Pending == nil && row.Status != nil && row.Status.ID == follow {
			row.Done = true
		}
	}
	if m == coreMachine {
		w.render(rw, r, s, "settings", "core-update-fragment", page{Data: d.Rows[0]})
		return
	}
	views, err := w.deviceViews(nodes, d)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, v := range views {
		if "node:"+v.Name == m {
			w.render(rw, r, s, "settings", "device-foot-fragment", page{Data: v})
			return
		}
	}
	http.NotFound(rw, r)
}

// handleUpdateAll asks every machine behind to update, eriphe-core first.
func (w *web) handleUpdateAll(rw http.ResponseWriter, r *http.Request, s *session) {
	n, err := w.c.updateAll()
	if err != nil {
		w.updateError(rw, w.lang(r), err)
		return
	}
	if n == 0 {
		w.toast(rw, tr(w.lang(r), "updates.nothing"), false)
		return
	}
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

func (w *web) updateError(rw http.ResponseWriter, lang string, err error) {
	if errors.Is(err, errNoUpdater) || errors.Is(err, errCoreFirst) || errors.Is(err, errPinned) {
		w.toast(rw, tr(lang, err.Error()), false)
		return
	}
	w.toast(rw, fmt.Sprint(err), false)
}

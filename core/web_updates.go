package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

// Settings → Machines → Updates (machine_updates.go): when each machine
// updates, a button to update it now, and one to go back to the version
// before.

type updateRow struct {
	Machine  string // core, or node:<name>
	Name     string
	Core     bool
	Online   bool
	Version  string
	Latest   string // the latest version, when this one is behind
	Own, Eff string // its own choice (empty: as all), and the one that applies
	updaterInfo
	Pending   *machineUpdate
	CoreFirst bool // a node, while eriphe-core is behind: eriphe-core goes first
}

type updatesData struct {
	Policy string // for all machines
	Rows   []updateRow
}

func (w *web) updatesData(nodes []NodeRow) updatesData {
	c := w.c
	_, all := c.updatePolicy("")
	d := updatesData{Policy: all}
	own, eff := c.updatePolicy(coreMachine)
	row := updateRow{Machine: coreMachine, Name: "eriphe-core", Core: true, Online: true, Version: version,
		Own: own, Eff: eff, updaterInfo: c.coreUpdater(), Pending: c.pendingUpdate(coreMachine)}
	if c.updates.behind("eriphe-core", version) {
		row.Latest = c.updates.version("eriphe-core")
	}
	d.Rows = append(d.Rows, row)
	coreBehind := c.updates.behind("eriphe-core", version)
	for _, n := range nodes {
		info := parseInfo(n)
		m := "node:" + n.Name
		own, eff := c.updatePolicy(m)
		row := updateRow{Machine: m, Name: n.Name, Online: n.Online, Version: info.Version, Own: own, Eff: eff,
			updaterInfo: info.Update, Pending: c.pendingUpdate(m), CoreFirst: coreBehind}
		if c.updates.behind("eriphe-node", info.Version) {
			row.Latest = c.updates.version("eriphe-node")
		} else if !sameVersion(info.Version, version) {
			row.Latest = version // eriphe-core's: where the node goes
		}
		d.Rows = append(d.Rows, row)
	}
	return d
}

// machineParam is the machine a form names, if it exists.
func (w *web) machineParam(m string) bool {
	if m == coreMachine {
		return true
	}
	name, ok := strings.CutPrefix(m, "node:")
	return ok && w.c.store.hasNode(name)
}

// handleUpdatePolicy sets when all machines (machine empty) or one update.
func (w *web) handleUpdatePolicy(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	m, p := r.PostFormValue("machine"), r.PostFormValue("policy")
	if m != "" && !w.machineParam(m) {
		w.toast(rw, tr(lang, "machines.nonode"), false)
		return
	}
	if err := w.c.setUpdatePolicy(m, p); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: updates of %s: %s", orAll(m), orAll(p))
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

func orAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// handleUpdateNow asks a machine's updater to update now, or to roll back.
func (w *web) handleUpdateNow(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	m, action := r.PostFormValue("machine"), r.PostFormValue("action")
	if !w.machineParam(m) {
		w.toast(rw, tr(lang, "machines.nonode"), false)
		return
	}
	if p := w.c.pendingUpdate(m); p != nil {
		w.toast(rw, tr(lang, "update.already"), false)
		return
	}
	if _, err := w.c.requestUpdate(m, action, false); err != nil {
		if errors.Is(err, errNoUpdater) || errors.Is(err, errCoreFirst) || errors.Is(err, errPinned) {
			w.toast(rw, tr(lang, err.Error()), false)
		} else {
			w.toast(rw, err.Error(), false)
		}
		return
	}
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

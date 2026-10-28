package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
)

// Settings → Devices: the devices connected to Eriphe (web_pages.go has
// their cards), each one's page with its access log, and "Cut access"
// (node_access.go). The addresses under /settings/machines, from before
// devices, still work.

func (w *web) deviceRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/devices", w.auth(w.handleMachines))
	mux.Handle("POST /settings/devices/join", w.auth(w.handleJoinCommand))
	mux.Handle("GET /settings/devices/{name}", w.auth(w.handleDevice))
	mux.Handle("GET /settings/machines/{name}", w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
		http.Redirect(rw, r, "/settings/devices/"+r.PathValue("name"), http.StatusSeeOther)
	}))
	mux.Handle("POST /settings/devices/{name}/forget", w.auth(w.handleForget))
	mux.Handle("POST /settings/devices/{name}/rejoin", w.auth(w.handleRejoinCommand))
	mux.Handle("POST /settings/devices/{name}/suspend", w.auth(w.handleDeviceAccess(true)))
	mux.Handle("POST /settings/devices/{name}/resume", w.auth(w.handleDeviceAccess(false)))
}

// deviceLogPage is how many entries of the access log a page shows.
const deviceLogPage = 50

type deviceData struct {
	Device machineView
	Log    []NodeLogEntry
	Next   int64 // the entry to show more before, if there are more
}

// handleDevice shows one device: what Eriphe sees and can do there, and
// its access log. With before, only the next rows of the log (htmx).
func (w *web) handleDevice(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	nodes, err := w.c.store.nodes()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := w.deviceViews(nodes, w.updatesData(nodes))
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	var d deviceData
	found := false
	for _, v := range views {
		if v.Name == name {
			d.Device, found = v, true
		}
	}
	if !found {
		http.Redirect(rw, r, "/settings/devices", http.StatusSeeOther)
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	entries, err := w.c.store.nodeLog(name, deviceLogPage+1, before)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(entries) > deviceLogPage {
		entries = entries[:deviceLogPage]
		d.Next = entries[len(entries)-1].ID
	}
	d.Log = entries
	if before > 0 {
		w.render(rw, r, s, "settings", "device-log-rows", page{Data: d})
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.machines", Nav: "settings", Tab: "devices", View: "device", Data: d})
}

// handleDeviceAccess cuts a device's access, or restores it.
func (w *web) handleDeviceAccess(suspend bool) func(http.ResponseWriter, *http.Request, *session) {
	return func(rw http.ResponseWriter, r *http.Request, s *session) {
		name := r.PathValue("name")
		do, what := w.c.resumeNode, "restored"
		if suspend {
			do, what = w.c.suspendNode, "cut"
		}
		if err := do(name); err != nil {
			if errors.Is(err, errNoNode) {
				w.toast(rw, tr(w.lang(r), "machines.nonode"), false)
				return
			}
			w.toast(rw, err.Error(), false)
			return
		}
		log.Printf("web: access to %s %s", name, what)
		w.notify()
		rw.Header().Set("HX-Refresh", "true")
		rw.WriteHeader(http.StatusOK)
	}
}

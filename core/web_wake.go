package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Settings → Devices → a device: "Eriphe may wake this device", "Wake now"
// and whether it came back (wake.go).

func (w *web) wakeRoutes(mux *http.ServeMux) {
	mux.Handle("POST /settings/devices/{name}/wake/allow", w.auth(w.handleWakeAllow))
	mux.Handle("POST /settings/devices/{name}/wake", w.auth(w.handleWakeNow))
	mux.Handle("GET /settings/devices/{name}/wake", w.auth(w.handleWakeState))
}

// wakeView is a device's waking, on its card and its page.
type wakeView struct {
	Can     bool     // it is not the machine that stays on
	Allowed bool     // the user lets Eriphe wake it
	MACs    []string // its network card, as it reported it
}

func (w *web) wakeView(n NodeRow, info nodeInfo) wakeView {
	return wakeView{Can: !info.AlwaysOn, Allowed: w.c.store.wakeAllowed(n.Name), MACs: nodeWakeInfo(n).MACs}
}

// handleWakeAllow turns "Eriphe may wake this device" on or off.
func (w *web) handleWakeAllow(rw http.ResponseWriter, r *http.Request, s *session) {
	name, lang := r.PathValue("name"), w.lang(r)
	on := r.PostFormValue("allow") != ""
	if err := w.c.store.setWakeAllowed(name, on); err != nil {
		if errors.Is(err, errNoNode) {
			w.toast(rw, tr(lang, "machines.nonode"), false)
			return
		}
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: waking %s allowed: %t", name, on)
	// The page shows the guide and "Wake now" with it.
	rw.Header().Set("HX-Refresh", "true")
	rw.WriteHeader(http.StatusOK)
}

// wakeState is where a "Wake now" stands: waiting for the device to
// connect, back, or not back after wakeWait.
type wakeState struct {
	Name  string
	Since int64 // when the signal was sent, Unix seconds
	State string
	Via   []string
}

// handleWakeNow sends the signal, then shows the device being waited for.
func (w *web) handleWakeNow(rw http.ResponseWriter, r *http.Request, s *session) {
	name, lang := r.PathValue("name"), w.lang(r)
	now := time.Now()
	via, err := w.c.wake(r.Context(), name)
	if err != nil {
		key := ""
		switch {
		case errors.Is(err, errNoNode):
			key = "machines.nonode"
		case errors.Is(err, errWakeNotAllowed):
			key = "wake.err.notallowed"
		case errors.Is(err, errWakeNoMAC):
			key = "wake.err.nomac"
		case errors.Is(err, errWakeOnline):
			key = "wake.err.online"
		case errors.Is(err, errNodeSuspended):
			key = "folders.err.suspended"
		}
		msg := err.Error()
		if key != "" {
			msg = tr(lang, key)
		}
		w.toast(rw, msg, false)
		return
	}
	w.render(rw, r, s, "settings", "wake-state-fragment", page{Data: wakeState{Name: name, Since: now.Unix(), State: "waiting", Via: via}})
}

// handleWakeState tells whether the device woken at since is back.
func (w *web) handleWakeState(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if !nodeName.MatchString(name) || err != nil {
		http.NotFound(rw, r)
		return
	}
	nodes, err := w.c.store.nodes()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	v := wakeState{Name: name, Since: since, State: "waiting"}
	for _, n := range nodes {
		if n.Name == name && n.Online && n.LastSeen.Unix() >= since {
			v.State = "back"
		}
	}
	if v.State == "waiting" && time.Since(time.Unix(since, 0)) > wakeWait {
		v.State = "late"
	}
	w.render(rw, r, s, "settings", "wake-state-fragment", page{Data: v})
}

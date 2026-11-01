package main

import (
	"net/http"
	"strconv"
)

// Settings → Veilles (watch.go): the routines (at a time of day, on the
// user's return) and the watches (a tool polled, an alert's webhook), each
// with its last runs. The brain writes them when asked (watch_tools.go):
// the page shows them, pauses, resumes and deletes them; what it proposes
// waits in « À valider » (web_activity.go). An alerte watch's webhook
// address (and its signing secret) is made here and shown once; an
// authorised action is shown, and revoked, here. The webhook itself
// (watch_alert.go) is served beside, without a session.

const watchPageRuns = 5

func (w *web) watchRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/watches", w.auth(w.handleWatches))
	mux.Handle("POST /watches/{id}/{action}", w.auth(w.handleWatchAction))
	mux.HandleFunc("POST "+hookPath+"{token}", w.handleWatchHook)
}

type watchCard struct {
	*watch
	Runs  []*watchRun
	Grant *watchGrant // its live authorised action
}

// freshHook is a webhook address just made, shown this once.
type freshHook struct {
	ID          int64
	URL, Secret string
}

type watchesPage struct {
	Routines, Watches []watchCard // at a time or on return; on a tool or an alert
	Proposed          int         // waiting in « À valider »
	Zone              string
	Fresh             *freshHook
}

func (w *web) watchesPage() watchesPage {
	v := watchesPage{Zone: w.c.notifyZone().String()}
	ws, _ := w.c.watches()
	for _, x := range ws {
		if x.State == watchProposed {
			v.Proposed++
			continue
		}
		card := watchCard{watch: x, Runs: w.c.recentRuns(x.ID, watchPageRuns), Grant: w.c.liveGrant(x.ID)}
		if x.Source == sourceSchedule || x.Source == sourceReturn {
			v.Routines = append(v.Routines, card)
		} else {
			v.Watches = append(v.Watches, card)
		}
	}
	return v
}

func (w *web) handleWatches(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.watches", Nav: "settings", Tab: "watches", Data: w.watchesPage()})
}

func (w *web) renderWatches(rw http.ResponseWriter, r *http.Request, s *session, msg string) {
	w.renderWatchesWith(rw, r, s, msg, nil)
}

func (w *web) renderWatchesWith(rw http.ResponseWriter, r *http.Request, s *session, msg string, fresh *freshHook) {
	rw.Header().Set("HX-Trigger", hxTrigger(msg, true, true))
	v := w.watchesPage()
	v.Fresh = fresh
	w.render(rw, r, s, "settings", "watches-frag", page{Data: v})
}

func (w *web) handleWatchAction(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	action := r.PathValue("action")
	switch action {
	case "approve", "resume":
		_, err = w.c.setWatchState(id, watchActive)
	case "pause":
		_, err = w.c.setWatchState(id, watchPaused)
	case "reject", "delete":
		err = w.c.deleteWatch(id)
	case "revoke":
		w.c.revokeGrants(id, "revoked in Settings")
	case "hook":
		token, secret, err := w.c.newWatchHook(id)
		if err != nil {
			w.toast(rw, err.Error(), false)
			return
		}
		w.renderWatchesWith(rw, r, s, tr(lang, "watch.did.hook"), &freshHook{ID: id, URL: w.publicURL(r) + hookPath + token, Secret: secret})
		return
	default:
		http.Error(rw, "approve, reject, pause, resume, delete, revoke or hook", http.StatusBadRequest)
		return
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	if action == "approve" || action == "reject" {
		w.c.proposalDecided(cardWatch, id, action == "approve") // its card says so (convo_cards.go)
	}
	w.renderWatches(rw, r, s, tr(lang, "watch.did."+action))
}

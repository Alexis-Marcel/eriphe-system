package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Settings → Watches (watch.go): every watch with its last runs, the
// proposed ones to approve, and a form to add or edit one. What the user
// writes here is theirs: active at once. An alerte watch's webhook address
// (and its signing secret) is made here and shown once; an authorised
// action is shown, and revoked, here. The webhook itself (watch_alert.go)
// is served beside, without a session.

const watchPageRuns = 5

func (w *web) watchRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/watches", w.auth(w.handleWatches))
	mux.Handle("POST /watches", w.auth(w.handleWatchSave))
	mux.Handle("POST /watches/{id}/{action}", w.auth(w.handleWatchAction))
	mux.HandleFunc("POST "+hookPath+"{token}", w.handleWatchHook)
}

type watchCard struct {
	*watch
	Runs  []*watchRun
	After string      // retour: its absence, as the form shows it
	Grant *watchGrant // its live authorised action
	Args  string      // outil: its arguments, as JSON
}

// freshHook is a webhook address just made, shown this once.
type freshHook struct {
	ID          int64
	URL, Secret string
}

type watchesPage struct {
	Proposed, Watches []watchCard
	Stub              watchCard // the form of a new watch
	Zone              string
	Fresh             *freshHook
}

func (w *web) watchesPage() watchesPage {
	v := watchesPage{Zone: w.c.notifyZone().String(),
		Stub: watchCard{watch: &watch{Source: sourceSchedule, Spec: watchSpec{Schedule: "daily 07:00", Every: pollEveryDefault}}, After: "3h"}}
	ws, _ := w.c.watches()
	for _, x := range ws {
		card := watchCard{watch: x, Runs: w.c.recentRuns(x.ID, watchPageRuns), After: afterText(x.returnAfter()),
			Grant: w.c.liveGrant(x.ID)}
		if x.Spec.Args != nil {
			card.Args = canonicalArgs(x.Spec.Args)
		}
		if x.State == watchProposed {
			v.Proposed = append(v.Proposed, card)
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

// watchFromForm reads a watch from the form.
func watchFromForm(r *http.Request) (*watch, error) {
	after, err := parseAfter(r.PostFormValue("after"))
	if err != nil {
		return nil, errors.New("watch.err.after")
	}
	x := &watch{Name: r.PostFormValue("name"), Source: r.PostFormValue("source"), Prompt: r.PostFormValue("prompt"),
		Brief: r.PostFormValue("brief") == "1", State: watchActive, By: "owner",
		Spec: watchSpec{Schedule: r.PostFormValue("schedule"), After: after, Server: r.PostFormValue("server"),
			Tool: r.PostFormValue("tool"), Signed: r.PostFormValue("signed") == "1"}}
	if x.Source == sourcePoll {
		x.Spec.Every, _ = strconv.Atoi(r.PostFormValue("every"))
		x.Spec.Cond = &watchCond{Kind: r.PostFormValue("cond_kind"), Path: r.PostFormValue("cond_path"),
			Op: r.PostFormValue("cond_op"), Value: r.PostFormValue("cond_value")}
		if raw := strings.TrimSpace(r.PostFormValue("args")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &x.Spec.Args); err != nil {
				return nil, errors.New("watch.err.args")
			}
		}
	}
	return x, nil
}

// handleWatchSave adds a watch, or changes one (id): the user's own.
func (w *web) handleWatchSave(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	x, err := watchFromForm(r)
	if err != nil {
		w.toast(rw, tr(lang, err.Error()), false)
		return
	}
	if scan := scanMemoryText(x.Name, x.Prompt); scan.Reject != "" {
		w.toast(rw, scan.Reject, false)
		return
	}
	if err := x.check(); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	if id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64); err == nil && id > 0 {
		old, err := w.c.watch(id)
		if err != nil {
			w.toast(rw, err.Error(), false)
			return
		}
		x.ID, x.State, x.Why = old.ID, old.State, old.Why
		if old.State == watchProposed {
			x.State = watchActive // written by the user: theirs now
		}
		err = w.c.saveWatch(x)
		if err != nil {
			w.toast(rw, err.Error(), false)
			return
		}
		w.c.revokeGrants(x.ID, "the watch changed")
	} else if _, err := w.c.addWatch(x); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderWatches(rw, r, s, tr(lang, "watch.saved"))
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
	w.renderWatches(rw, r, s, tr(lang, "watch.did."+action))
}

// afterText is an absence as parseAfter reads it back: 3h, 1h30, 45m.
func afterText(d time.Duration) string {
	m := int(d.Minutes())
	switch {
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m%60 == 0:
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dh%02d", m/60, m%60)
}

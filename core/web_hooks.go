package main

import (
	"net/http"
	"strconv"
)

// The incoming alerts, on Settings → Skills (alert_hooks.go): add one,
// shown its address and secret once; make a new token; remove one.
func (w *web) alertRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+hookPath+"{token}", w.handleAlertHook)
	mux.HandleFunc("POST "+hookPathOld+"{token}", w.handleAlertHook) // its former path
	mux.Handle("POST /settings/alerts", w.auth(w.handleNewAlert))
	mux.Handle("POST /settings/alerts/{id}/delete", w.auth(w.handleDeleteAlert))
}

func (w *web) handleNewAlert(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.FormValue("name")
	token, secret, err := w.c.newAlertHook(name, r.FormValue("signed") == "on", r.FormValue("header"))
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	w.renderSkills(rw, r, s, &freshHook{Name: name, URL: w.publicURL(r) + hookPath + token, Secret: secret})
}

func (w *web) handleDeleteAlert(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := w.c.deleteAlertHook(id); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(rw, r, "/settings/skills#alerts", http.StatusSeeOther)
}

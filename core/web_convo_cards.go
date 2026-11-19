package main

import (
	"errors"
	"net/http"
	"strconv"
)

// A card's answer in the workspace (convo_cards.go): the graph's proposal
// blocks say yes or no to the card that carries them (web/espace,
// answerCard), and read what it became from the event stream
// ("proposals").

func (w *web) cardRoutes(mux *webMux) {
	mux.Handle("POST /cards/{id}/{action}", w.auth(w.handleCard))
}

// handleCard is a card's Oui or Non. A card decided already, here, on a
// channel or in words, stays as it was decided.
func (w *web) handleCard(rw http.ResponseWriter, r *http.Request, s *session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	action := r.PathValue("action")
	if action != "yes" && action != "no" {
		http.Error(rw, "yes or no", http.StatusBadRequest)
		return
	}
	_, err = w.c.decideCard(r.Context(), id, action == "yes", "web")
	switch {
	case errors.Is(err, errNoCard):
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	case err != nil && !errors.Is(err, errCardDecided):
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.notify()
	rw.WriteHeader(http.StatusOK)
}

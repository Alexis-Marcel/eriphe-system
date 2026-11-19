package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// A card's answer in the workspace (convo_cards.go): the graph's proposal
// blocks say yes or no to the card that carries them (web/espace,
// answerCard), and read what it became from the event stream
// ("proposals").

func (w *web) cardRoutes(mux *webMux) {
	mux.Handle("POST /cards/{id}/{action}", w.auth(w.handleCard))
}

// formIDs reads ids sent in a form, each value one id or several
// separated by commas.
func formIDs(values []string) ([]int64, bool) {
	var out []int64
	for _, v := range values {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil || id <= 0 {
				return nil, false
			}
			out = append(out, id)
		}
	}
	return out, true
}

// handleCard is a card's Oui or Non. A card decided already, here, on a
// channel or in words, stays as it was decided. Oui on a calls card may
// carry the user's choice: the calls ticked (only) and those unticked
// (left), by their ids.
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
	r.ParseForm()
	if _, chosen := r.PostForm["only"]; chosen && action == "yes" {
		// Oui on a calls card with the user's choice (call_fold.go).
		only, ok1 := formIDs(r.PostForm["only"])
		left, ok2 := formIDs(r.PostForm["left"])
		if !ok1 || !ok2 {
			http.Error(rw, "bad only or left", http.StatusBadRequest)
			return
		}
		_, err = w.c.decideCardChosen(r.Context(), id, only, left, "web")
	} else {
		_, err = w.c.decideCard(r.Context(), id, action == "yes", "web")
	}
	switch {
	case errors.Is(err, errNotOnCard):
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
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

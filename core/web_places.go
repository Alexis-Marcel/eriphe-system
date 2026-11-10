package main

import (
	"context"
	"net/http"
	"time"
)

// A tool's places, on its page (Settings → Tools → <tool> → Places,
// places.go): the ones given to Eriphe, each read or read and add results,
// and the tool's own browsing to pick more, by account.

type placesView struct {
	Tool     string
	Status   string // the tool's: ready, connecting…
	Accounts []placeAccount
	Places   []Place
	Error    string
}

// Many tells whether the places come from several accounts.
func (v placesView) Many() bool { return len(v.Accounts) > 1 }

// CanWrite tells whether an account may get places to write in.
func (v placesView) CanWrite(account string) bool {
	for _, a := range v.Accounts {
		if a.Name == account {
			return a.CanWrite
		}
	}
	return false
}

type placesBrowse struct {
	Tool         string
	Account      string   // the account browsed
	Accounts     []string // the accounts, when there are several
	Parent, Name string
	Items        []placeItem
	Next         string
	CanWrite     bool
	Given        map[string]string // place id → its access, for the ones already given
	Error        string
}

func (w *web) placeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/tools/{name}/places", w.auth(w.handlePlaces))
	mux.Handle("GET /settings/tools/{name}/places/browse", w.auth(w.handlePlacesBrowse))
	mux.Handle("POST /settings/tools/{name}/places", w.auth(w.handlePlaceGive))
	mux.Handle("POST /settings/tools/{name}/places/remove", w.auth(w.handlePlaceRemove))
}

// placesTool is the tool of the page, if it has places.
func (w *web) placesTool(rw http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if _, _, ok := keeperOf(w.c.conf().Servers[name]); !ok {
		http.NotFound(rw, r)
		return "", false
	}
	return name, true
}

func (w *web) placesView(tool string) placesView {
	v := placesView{Tool: tool, Accounts: w.c.placeAccounts(tool), Places: placesIn(tool, w.c.conf().Servers[tool])}
	if u := w.c.pool.get(tool); u != nil {
		v.Status, _, _ = u.snapshot()
	}
	return v
}

func (w *web) handlePlaces(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.render(rw, r, s, "settings", "", page{Title: "places.title", Nav: "settings", Tab: "tools", View: "places", Data: w.placesView(tool)})
	}
}

func (w *web) handlePlacesBrowse(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	v := placesBrowse{Tool: tool, Parent: q.Get("parent"), Name: q.Get("name"), Given: map[string]string{}}
	accounts := w.c.placeAccounts(tool)
	for _, a := range accounts {
		if a.Name == q.Get("account") || v.Account == "" {
			v.Account, v.CanWrite = a.Name, a.CanWrite
		}
	}
	if len(accounts) > 1 {
		for _, a := range accounts {
			v.Accounts = append(v.Accounts, a.Name)
		}
	}
	for _, p := range placesIn(tool, w.c.conf().Servers[tool]) {
		v.Given[p.ID] = p.Access
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	if v.Items, v.Next, err = w.c.browsePlaces(ctx, tool, v.Account, v.Parent, q.Get("page")); err != nil {
		v.Error = w.driveError(r, err)
	}
	w.render(rw, r, s, "settings", "places-browse", page{Data: v})
}

func (w *web) handlePlaceGive(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.placesAgain(rw, r, s, tool, w.c.givePlace(tool, r.PostFormValue("account"), r.PostFormValue("id"), r.PostFormValue("name"), r.PostFormValue("access")))
	}
}

func (w *web) handlePlaceRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.placesAgain(rw, r, s, tool, w.c.removePlace(tool, r.PostFormValue("id")))
	}
}

// placesAgain answers a change with the list of places again.
func (w *web) placesAgain(rw http.ResponseWriter, r *http.Request, s *session, tool string, err error) {
	v := w.placesView(tool)
	if err != nil {
		v.Error = w.driveError(r, err)
	} else {
		w.notify()
	}
	w.render(rw, r, s, "settings", "places-list-frag", page{Data: v})
}

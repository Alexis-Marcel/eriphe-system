package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// A tool's places, on the page of one of its accounts (Settings → Tools →
// <account> → Folders, places.go): one list, the folders given first, each
// with its cases (Lire, Modifier, Supprimer: its account's, or its own),
// then the account's own folders to give more, browsed through the tool.

// toolFolders is the page's list: the folders of an account, the given
// ones first.
type toolFolders struct {
	Tool, Base string
	Account    string
	Status     string // the tool's: ready, connecting…
	CanWrite   bool   // the account's credential writes: else its cases above Lire are greyed
	Parent     string // the folder browsed, "" for the top
	Name       string // … and its name
	Items      []folderItem
	Next       string
	Error      string
}

// folderItem is one line: a folder given, with its cases, or one to give.
type folderItem struct {
	placeItem
	Given bool // its Name is then the given folder's, as tasks name it
	Cases []folderCase
}

// folderCase is one case of a given folder: its own value, or "" to
// follow its account's (Account).
type folderCase struct {
	Category, Own, Account string
	Greyed                 bool // above its account's ceiling
}

// placeCount is "no folder", "1 folder", "3 folders".
func placeCount(lang string, n int) string {
	switch n {
	case 0:
		return tr(lang, "places.count0")
	case 1:
		return tr(lang, "places.count1")
	}
	return fmt.Sprintf(tr(lang, "places.count"), n)
}

func (w *web) placeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/tools/{name}/places", w.auth(w.handlePlaces))
	mux.Handle("POST /settings/tools/{name}/places", w.auth(w.handlePlaceGive))
	mux.Handle("POST /settings/tools/{name}/places/case", w.auth(w.handlePlaceCase))
	mux.Handle("POST /settings/tools/{name}/places/remove", w.auth(w.handlePlaceRemove))
	mux.Handle("GET /settings/connections/{id}/places", w.auth(w.handleConnPlaces))
	mux.Handle("GET /settings/connections/{id}/places/browse", w.auth(w.handleConnPlacesBrowse))
	mux.Handle("POST /settings/connections/{id}/places", w.auth(w.handleConnPlaceGive))
	mux.Handle("POST /settings/connections/{id}/places/remove", w.auth(w.handleConnPlaceRemove))
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

// toolFolders is the list of one account's folders: the given ones, then,
// browsed through the tool, the account's folders under parent (the given
// ones of them first).
func (w *web) toolFolders(ctx context.Context, tool, account, parent, name, page string) toolFolders {
	srv := w.c.conf().Servers[tool]
	k, _, _ := keeperOf(srv)
	v := toolFolders{Tool: tool, Base: "/settings/tools/" + tool + "/places", Parent: parent, Name: name}
	accounts := w.c.placeAccounts(tool)
	for _, a := range accounts {
		if strings.EqualFold(a.Name, account) || v.Account == "" {
			v.Account, v.CanWrite = a.Name, a.CanWrite
		}
	}
	given := map[string]folderItem{}
	var order []string
	for _, p := range placesOfAccount(tool, srv, v.Account, len(accounts)) {
		it := folderItem{placeItem: placeItem{ID: p.ID, Name: p.Name}, Given: true}
		account := srv.placeAccess(Place{Account: p.Account}) // its account's, without its own cases
		own := srv.PlaceGrids[p.ID]
		for _, c := range k.Card.cases() {
			it.Cases = append(it.Cases, folderCase{Category: c, Own: own.raw(c), Account: account.grid.value(c),
				Greyed: !v.CanWrite && c != catRead})
		}
		given[p.ID] = it
		order = append(order, p.ID)
	}
	all := func() []folderItem {
		var out []folderItem
		for _, id := range order {
			out = append(out, given[id])
		}
		return out
	}
	if parent == "" {
		v.Items = all()
	}
	if u := w.c.pool.get(tool); u != nil {
		v.Status, _, _ = u.snapshot()
	}
	if v.Status != statusReady || v.Account == "" {
		v.Items = all() // what is given stays in view, wherever the browsing was
		return v
	}
	items, next, err := w.c.browsePlaces(ctx, tool, v.Account, parent, page)
	if err != nil {
		v.Error, v.Items = err.Error(), all()
		return v
	}
	var mine, others []folderItem
	for _, it := range items {
		g, ok := given[it.ID]
		switch {
		case ok && parent != "":
			g.placeItem.Link = it.Link
			mine = append(mine, g)
		case !ok:
			others = append(others, folderItem{placeItem: it})
		}
	}
	v.Items, v.Next = append(append(v.Items, mine...), others...), next
	return v
}

// raw is a grid's own value of a category, "" when it says nothing.
func (g accessGrid) raw(category string) string {
	switch category {
	case catRead:
		return g.Read
	case catChange:
		return g.Change
	case catDelete:
		return g.Delete
	}
	return g.Send
}

func (w *web) handlePlaces(rw http.ResponseWriter, r *http.Request, s *session) {
	tool, ok := w.placesTool(rw, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v := w.toolFolders(ctx, tool, q.Get("account"), q.Get("parent"), q.Get("name"), q.Get("page"))
	if v.Error != "" {
		v.Error = w.connError(r, fmt.Errorf("%s", v.Error))
	}
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-Target") == "tool-folders" {
		w.render(rw, r, s, "settings", "tool-folders-frag", page{Data: v})
		return
	}
	w.render(rw, r, s, "settings", "", page{Title: "places.title", Nav: "settings", Tab: "tools", View: "tool-folders", Data: v})
}

// foldersAgain answers a change with the list again, where it was.
func (w *web) foldersAgain(rw http.ResponseWriter, r *http.Request, s *session, tool string, err error) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v := w.toolFolders(ctx, tool, r.PostFormValue("account"), r.PostFormValue("parent"), r.PostFormValue("parent_name"), "")
	if v.Error != "" {
		v.Error = w.connError(r, fmt.Errorf("%s", v.Error))
	}
	if err != nil {
		v.Error = w.connError(r, err)
	} else {
		w.notify()
	}
	w.render(rw, r, s, "settings", "tool-folders-frag", page{Data: v})
}

func (w *web) handlePlaceGive(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.foldersAgain(rw, r, s, tool, w.c.givePlace(tool, r.PostFormValue("account"), r.PostFormValue("id"), r.PostFormValue("name")))
	}
}

func (w *web) handlePlaceCase(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.foldersAgain(rw, r, s, tool, w.c.setPlaceCase(tool, r.PostFormValue("id"), r.PostFormValue("category"), r.PostFormValue("value")))
	}
}

func (w *web) handlePlaceRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if tool, ok := w.placesTool(rw, r); ok {
		w.foldersAgain(rw, r, s, tool, w.c.removePlace(tool, r.PostFormValue("id")))
	}
}

// The repositories of a connection whose provider mints tokens
// (connection_places.go), each read or read and write.

type placesView struct {
	Tool     string // its name, in the heading
	Base     string // the page's address, which its forms post to
	Write    string // the access that writes
	Lead     string // the text that says what a place gives
	Status   string
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
	Base, Write  string
	Flat         bool
	Account      string
	Accounts     []string
	Parent, Name string
	Items        []placeItem
	Next         string
	CanWrite     bool
	Given        map[string]string // place id → its access, for the ones already given
	Error        string
}

// placesConn is the connection of the page, if it has places.
func (w *web) placesConn(rw http.ResponseWriter, r *http.Request) (connection, bool) {
	cn, ok := w.c.connectionByID(r.PathValue("id"))
	if !ok || w.c.minters[cn.Provider] == nil {
		http.NotFound(rw, r)
		return connection{}, false
	}
	return cn, true
}

func (w *web) connPlacesView(cn connection) placesView {
	return placesView{Tool: providerLabel(cn.Provider) + " · " + cn.Identity, Base: "/settings/connections/" + cn.ID + "/places",
		Write: accessWrite, Lead: "places.lead.repos", Status: statusReady,
		Accounts: []placeAccount{{Name: cn.Identity, CanWrite: true}}, Places: w.c.connectionPlaces(cn.ID)}
}

func (w *web) handleConnPlaces(rw http.ResponseWriter, r *http.Request, s *session) {
	if cn, ok := w.placesConn(rw, r); ok {
		w.render(rw, r, s, "settings", "", page{Title: "repos.title", Nav: "settings", Tab: "tools", View: "places", Data: w.connPlacesView(cn)})
	}
}

func (w *web) handleConnPlacesBrowse(rw http.ResponseWriter, r *http.Request, s *session) {
	cn, ok := w.placesConn(rw, r)
	if !ok {
		return
	}
	v := placesBrowse{Tool: cn.Identity, Base: "/settings/connections/" + cn.ID + "/places", Write: accessWrite, Flat: true,
		Account: cn.Identity, CanWrite: true, Given: map[string]string{}}
	for _, p := range w.c.connectionPlaces(cn.ID) {
		v.Given[p.ID] = p.Access
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	if v.Items, v.Next, err = w.c.browseConnection(ctx, cn.ID, r.URL.Query().Get("page")); err != nil {
		v.Error = w.connError(r, err)
	}
	w.render(rw, r, s, "settings", "places-browse", page{Data: v})
}

func (w *web) handleConnPlaceGive(rw http.ResponseWriter, r *http.Request, s *session) {
	if cn, ok := w.placesConn(rw, r); ok {
		w.connPlacesAgain(rw, r, s, cn, w.c.giveConnectionPlace(cn.ID, r.PostFormValue("id"), r.PostFormValue("access")))
	}
}

func (w *web) handleConnPlaceRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	if cn, ok := w.placesConn(rw, r); ok {
		w.connPlacesAgain(rw, r, s, cn, w.c.removeConnectionPlace(cn.ID, r.PostFormValue("id")))
	}
}

func (w *web) connPlacesAgain(rw http.ResponseWriter, r *http.Request, s *session, cn connection, err error) {
	v := w.connPlacesView(cn)
	if err != nil {
		v.Error = w.connError(r, err)
	} else {
		w.notify()
	}
	w.render(rw, r, s, "settings", "places-list-frag", page{Data: v})
}

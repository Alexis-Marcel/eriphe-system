package main

import (
	"net/http"
	"strconv"
	"time"
)

// Settings → Access: every way into Eriphe, one row each, as the tools are
// listed. Two are always there: the web (the remote access, web_remote.go)
// and the mobile app (installing the workspace, its notifications and the
// devices told, web_push.go). The others are added from a catalog: the
// channels eriphe-core talks to (channels.go: a chat app's bot, each with
// its own page) and the API accesses (channels_added.go), any app or
// script holding a token (Home Assistant's integration is one). Each row says who
// speaks through it and what it may carry, and opens its own page.

const (
	entryWeb = "web"
	entryApp = "app"
	// An API access (channels.kind): any app holding a token.
	apiGeneric = "api"
)

// entryKind is an entry of the catalog.
type entryKind struct {
	ID      string // a channel's (channels.go), or an API access's kind
	Name    string // an API access's name to start with ("" for the generic one)
	Carries string // and what it may say
}

// entryCatalog is what the user may add, in the catalog's order: the
// channels with a page of their own, then an API access, which starts not
// trusted: the user says otherwise.
func entryCatalog() []entryKind {
	var out []entryKind
	for _, k := range channelKinds {
		if k.Page != "" {
			out = append(out, entryKind{ID: k.ID})
		}
	}
	return append(out, entryKind{ID: apiGeneric})
}

func entryKindOf(id string) (entryKind, bool) {
	for _, k := range entryCatalog() {
		if k.ID == id {
			return k, true
		}
	}
	return entryKind{}, false
}

// entryRow is a row of the list.
type entryRow struct {
	Kind    string // web, app, a channel's id or a kind of API access: its mark
	Name    string // an API access's own; the others are named by the page's words
	URL     string // its page
	Speaker string // inbound, who speaks: chan.speaker.*
	Carries string // outbound, what it carries: chan.carries.*
	State   string // on, wait, bad or off: the chip's colour
	Label   string // the chip's words (a key), with Count if it has a %d
	Count   int
	Detail  string // shown as is: an address, a bot
}

// Chip is the class of its state's chip, as the tools have.
func (r entryRow) Chip() string {
	switch r.State {
	case "on":
		return "srv-ready"
	case "wait":
		return "srv-needs_auth"
	case "bad":
		return "srv-error"
	}
	return "srv-pending"
}

// entryTile is an entry of the catalog, as shown.
type entryTile struct {
	entryKind
	URL   string
	Added bool // a channel with its own page, already set up
}

type entryPage struct {
	Rows    []entryRow
	Catalog []entryTile
}

func (w *web) entryPage() entryPage {
	var v entryPage
	c := w.c
	// The web: the remote access.
	web := entryRow{Kind: entryWeb, URL: "/settings/access/web", Speaker: speakerUser, State: "off", Label: "remote.phase.off"}
	if c.remote != nil {
		rv := c.remote.current()
		web.Label = "remote.phase." + rv.Phase
		switch rv.Phase {
		case phaseOff:
		case phaseReady:
			web.State, web.Detail = "on", rv.URL()
		case phaseError:
			web.State = "bad"
		default:
			web.State = "wait"
		}
	}
	v.Rows = append(v.Rows, web)
	// The mobile app: the devices told.
	app := entryRow{Kind: entryApp, URL: "/settings/access/app", Speaker: speakerUser, Carries: carryText, State: "off", Label: "acc.app.none"}
	if c.push != nil {
		if subs, _ := c.push.subscriptions(); len(subs) > 0 {
			app.State, app.Count, app.Label = "on", len(subs), "acc.app.devices"
			if len(subs) == 1 {
				app.Label = "acc.app.device"
			}
		}
	}
	v.Rows = append(v.Rows, app)
	// The channels eriphe-core talks to, once set up.
	set := map[string]bool{}
	for _, k := range channelKinds {
		if k.entry == nil {
			continue
		}
		if row, ok := k.entry(w); ok {
			set[k.ID] = true
			v.Rows = append(v.Rows, row)
		}
	}
	// The API accesses.
	now := time.Now()
	for _, ch := range c.addedChannels() {
		row := entryRow{Kind: apiGeneric, Name: ch.Name, URL: "/settings/access/api/" + strconv.FormatInt(ch.ID, 10),
			Speaker: "anyone", Carries: "none", State: "on", Label: "chan.ready"}
		if ch.Trusted {
			row.Speaker = "trusted"
		}
		if ch.Carries == carrySentence {
			row.Carries = carrySentence
			if c.out.listening(ch.ID, now) {
				row.Label = "chan.listening"
			} else {
				row.State, row.Label = "off", "chan.deaf"
			}
		}
		v.Rows = append(v.Rows, row)
	}
	for _, k := range entryCatalog() {
		v.Catalog = append(v.Catalog, entryTile{entryKind: k, URL: "/settings/access/new/" + k.ID, Added: set[k.ID]})
	}
	return v
}

func (w *web) accessRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/access", w.auth(w.handleAccess))
	mux.Handle("GET /settings/access/new", w.auth(w.handleAccessCatalog))
	mux.Handle("GET /settings/access/new/{kind}", w.auth(w.handleAccessNew))
	mux.Handle("GET /settings/access/web", w.auth(w.handleAccessWeb))
	mux.Handle("GET /settings/access/app", w.auth(w.handleAccessApp))
	mux.Handle("GET /settings/access/api/{id}", w.auth(w.handleAccessAPI))
	mux.Handle("POST /settings/access/api", w.auth(w.handleAPIAdd))
	mux.Handle("POST /settings/access/api/{id}", w.auth(w.handleAPISet))
	mux.Handle("POST /settings/access/api/{id}/token", w.auth(w.handleAPIToken))
	mux.Handle("POST /settings/access/api/{id}/remove", w.auth(w.handleAPIRemove))
	// Where these pages were before, in links already shared.
	for from, to := range map[string]string{"/settings/remote": "/settings/access", "/settings/channels": "/settings/access",
		"/settings/home-assistant": "/settings/tools"} {
		mux.HandleFunc("GET "+from, func(rw http.ResponseWriter, r *http.Request) {
			http.Redirect(rw, r, to, http.StatusMovedPermanently)
		})
	}
}

func (w *web) accessRender(rw http.ResponseWriter, r *http.Request, s *session, view string, data any) {
	w.render(rw, r, s, "settings", "", page{Title: "settings.access", Nav: "settings", Tab: "access", View: view, Data: data})
}

func (w *web) handleAccess(rw http.ResponseWriter, r *http.Request, s *session) {
	w.accessRender(rw, r, s, "", w.entryPage())
}

func (w *web) handleAccessCatalog(rw http.ResponseWriter, r *http.Request, s *session) {
	w.accessRender(rw, r, s, "access-new", w.entryPage())
}

// webAccessPage is the web's page: the remote access, then the workspace's
// address (web_address.go) and how one signs in to it (web_signin.go).
type webAccessPage struct {
	remotePage
	Address addressView
	Signin  signinView
}

func (w *web) handleAccessWeb(rw http.ResponseWriter, r *http.Request, s *session) {
	w.accessRender(rw, r, s, "access-web", webAccessPage{remotePage: w.remotePage(r), Address: w.addressView(r), Signin: w.signinView(r)})
}

func (w *web) handleAccessApp(rw http.ResponseWriter, r *http.Request, s *session) {
	w.accessRender(rw, r, s, "access-app", w.notificationsPage(w.lang(r)))
}

// apiAccessView is an API access's page, or the form adding one.
type apiAccessView struct {
	addedChannel
	New   bool
	Error string
	// A token just made, shown once, with the address the app talks to.
	Token string
	Core  string
}

// handleAccessNew is the page adding what the catalog offers: a channel's
// own page, or an API access's form.
func (w *web) handleAccessNew(rw http.ResponseWriter, r *http.Request, s *session) {
	k, ok := entryKindOf(r.PathValue("kind"))
	if !ok {
		http.NotFound(rw, r)
		return
	}
	if ch, ok := channelKindOf(k.ID); ok && ch.Page != "" {
		http.Redirect(rw, r, ch.Page, http.StatusSeeOther)
		return
	}
	if len(w.c.addedChannels()) >= maxChannels {
		w.accessRender(rw, r, s, "access-api", apiAccessView{New: true, Error: tr(w.lang(r), "acc.api.max")})
		return
	}
	w.accessRender(rw, r, s, "access-api", apiAccessView{New: true, addedChannel: addedChannel{Kind: k.ID, Name: k.Name, Carries: k.Carries}})
}

func (w *web) handleAccessAPI(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ch, err := w.c.addedChannel(id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	w.accessRender(rw, r, s, "access-api", apiAccessView{addedChannel: ch})
}

// apiAnswer answers a form of an API access's page with the page's body.
func (w *web) apiAnswer(rw http.ResponseWriter, r *http.Request, s *session, v apiAccessView) {
	if v.Token != "" {
		v.Core = w.coreURL(r)
	}
	w.render(rw, r, s, "settings", "access-api-frag", page{Data: v})
}

func (w *web) handleAPIAdd(rw http.ResponseWriter, r *http.Request, s *session) {
	kind := apiGeneric
	ch, tok, err := w.c.addChannel(r.PostFormValue("name"), r.PostFormValue("trusted") == "1", r.PostFormValue("carries"))
	if err != nil {
		w.apiAnswer(rw, r, s, apiAccessView{New: true, Error: err.Error(), addedChannel: addedChannel{Kind: kind,
			Name: r.PostFormValue("name"), Trusted: r.PostFormValue("trusted") == "1", Carries: r.PostFormValue("carries")}})
		return
	}
	// The page is now the access's own: a reload shows it without the token.
	rw.Header().Set("HX-Push-Url", "/settings/access/api/"+strconv.FormatInt(ch.ID, 10))
	w.apiAnswer(rw, r, s, apiAccessView{addedChannel: ch, Token: tok})
}

func (w *web) handleAPISet(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	v := apiAccessView{}
	if err := w.c.setChannel(id, r.PostFormValue("name"), r.PostFormValue("trusted") == "1", r.PostFormValue("carries")); err != nil {
		v.Error = err.Error()
	}
	ch, err := w.c.addedChannel(id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	v.addedChannel = ch
	w.apiAnswer(rw, r, s, v)
}

func (w *web) handleAPIToken(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ch, err := w.c.addedChannel(id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	v := apiAccessView{addedChannel: ch}
	if v.Token, err = w.c.replaceChannelToken(id); err != nil {
		v.Error = err.Error()
	}
	w.apiAnswer(rw, r, s, v)
}

func (w *web) handleAPIRemove(rw http.ResponseWriter, r *http.Request, s *session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := w.c.removeChannel(id); err != nil {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("HX-Redirect", "/settings/access")
	rw.WriteHeader(http.StatusNoContent)
}

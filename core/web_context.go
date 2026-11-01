package main

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Settings → Ce qu'Eriphe sait de toi (context.go): one page for the
// context. What Eriphe knows right now, live, as the router and the brain
// read it; the level; a switch per source; the sources with a token (made
// here, shown once, revoked here); the user's rooms; and plainly, what is
// kept and where. The brain changes the level and the switches too, through
// setting (settings_brain.go).

func (w *web) contextRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/context", w.auth(w.handleContextPage))
	mux.Handle("GET /context/live", w.auth(w.handleContextLive))
	mux.Handle("POST /context/level", w.auth(w.handleContextLevel))
	mux.Handle("POST /context/source", w.auth(w.handleContextSource))
	mux.Handle("POST /context/sources", w.auth(w.handleNewContextSource))
	mux.Handle("POST /context/sources/{id}/revoke", w.auth(w.handleRevokeContextSource))
	mux.Handle("POST /context/rooms", w.auth(w.handleContextRooms))
	mux.Handle("POST /context/ha/{id}/remove", w.auth(w.handleRemoveMapping))
	mux.Handle("POST /context/ha/proposals/{id}/{action}", w.auth(w.handleMappingProposal))
	mux.Handle("POST /context/ha/voice-urgent", w.auth(w.handleVoiceUrgent))
}

// haPageView is Home Assistant on the page: whether it is connected and
// followed, and the entities mapped to a signal, removable.
type haPageView struct {
	Linked      bool // the catalog has a Home Assistant
	Following   bool
	Err         string
	On          bool
	Seen        time.Time
	Mappings    []haMappingView
	Proposed    int
	VoiceUrgent bool
}

// haMappingView is a mapping as the page shows it, with the entity's name
// in Home Assistant when discovery saw it.
type haMappingView struct {
	haMapping
	Name string
}

func (w *web) haPage(p ctxPrefs) haPageView {
	c := w.c
	_, err := c.haLink()
	v := haPageView{Linked: err == nil, On: !slices.Contains(p.Off, srcHA), Proposed: len(c.haMappings(ruleProposed)), VoiceUrgent: c.voiceUrgent()}
	v.Following, v.Err = c.haStatus()
	v.Seen, _ = c.known.lastSeen(srcHA)
	house := c.ha.cached()
	for _, m := range c.haMappings(ruleActive) {
		mv := haMappingView{haMapping: m}
		if e, ok := house.entity(m.Entity); ok {
			mv.Name = e.Name
		}
		v.Mappings = append(v.Mappings, mv)
	}
	return v
}

// ctxSourceView is a source with its switch.
type ctxSourceView struct {
	Key  string // machine:<node> or src:<id>
	ID   int64  // a token source's
	Name string
	OS   string // a machine's
	On   bool
	Seen time.Time // its last word since eriphe-core started
}

// ctxDeviceView is a device in the live view.
type ctxDeviceView struct {
	Name string
	deviceCtx
	IdleMin int
}

// ctxLive is what Eriphe knows now.
type ctxLive struct {
	Level   int
	Devices []ctxDeviceView
	Place   string
	Room    string
	Home    string // the house's activity
	Focus   string
	Busy    time.Time
	Line    string // the header's line, word for word
	HasHome bool   // a source that can tell the house: a token source
}

// freshCtxSource is a token just made, shown this once.
type freshCtxSource struct {
	Name, Token, URL string
}

type contextPage struct {
	Live     ctxLive
	Level    int
	Machines []ctxSourceView
	Sources  []ctxSourceView
	Rooms    string
	Fresh    *freshCtxSource
	Endpoint string
	HA       haPageView
}

func (w *web) contextLive(now time.Time) ctxLive {
	c := w.c
	u := c.userContext(now)
	v := ctxLive{Level: u.Level, Place: u.Place, Room: u.Room, Home: u.HomeDoes, Focus: u.Focus, Busy: u.Busy,
		Line: u.line(c.sourceName), HasHome: len(c.contextSources()) > 0 || len(c.haMappings(ruleActive)) > 0}
	for _, d := range u.Devices {
		v.Devices = append(v.Devices, ctxDeviceView{Name: c.sourceName(d.Source), deviceCtx: d, IdleMin: int(d.Idle / time.Minute)})
	}
	return v
}

func (w *web) contextPage(r *http.Request) contextPage {
	c := w.c
	p := c.contextPrefs()
	v := contextPage{Live: w.contextLive(time.Now()), Level: p.Level, Rooms: strings.Join(p.Rooms, ", "),
		Endpoint: w.coreURL(r) + "/v1/context", HA: w.haPage(p)}
	if nodes, err := c.store.nodes(); err == nil {
		for _, n := range nodes {
			key := srcMachine + n.Name
			sv := ctxSourceView{Key: key, Name: n.Name, OS: n.OS, On: !slices.Contains(p.Off, key)}
			sv.Seen, _ = c.known.lastSeen(key)
			v.Machines = append(v.Machines, sv)
		}
	}
	for _, s := range c.contextSources() {
		sv := ctxSourceView{Key: s.key(), ID: s.ID, Name: s.Name, On: !slices.Contains(p.Off, s.key())}
		sv.Seen, _ = c.known.lastSeen(s.key())
		v.Sources = append(v.Sources, sv)
	}
	return v
}

func (w *web) handleContextPage(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "", page{Title: "ctx.title", Nav: "settings", Tab: "context", Data: w.contextPage(r)})
}

// handleContextLive is the live view alone, polled by the page.
func (w *web) handleContextLive(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "context-live", page{Data: w.contextLive(time.Now())})
}

func (w *web) renderContext(rw http.ResponseWriter, r *http.Request, s *session, msg string, fresh *freshCtxSource) {
	rw.Header().Set("HX-Trigger", hxTrigger(msg, true, true))
	v := w.contextPage(r)
	v.Fresh = fresh
	w.render(rw, r, s, "settings", "context-frag", page{Data: v})
}

func (w *web) handleContextLevel(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	n, err := strconv.Atoi(r.PostFormValue("level"))
	if err == nil {
		err = w.c.setContextLevel(n)
	}
	if err != nil {
		w.toast(rw, tr(lang, "ctx.err.level"), false)
		return
	}
	w.renderContext(rw, r, s, tr(lang, "ctx.saved"), nil)
}

// handleContextSource turns a source on or off: a machine, or a token
// source; anything else is refused.
func (w *web) handleContextSource(rw http.ResponseWriter, r *http.Request, s *session) {
	key := r.PostFormValue("source")
	known := key == srcHA || slices.ContainsFunc(w.contextPage(r).Machines, func(v ctxSourceView) bool { return v.Key == key }) ||
		slices.ContainsFunc(w.c.contextSources(), func(v ctxSource) bool { return v.key() == key })
	if !known {
		http.Error(rw, "unknown source", http.StatusBadRequest)
		return
	}
	w.c.setSourceOn(key, r.PostFormValue("on") == "1")
	w.renderContext(rw, r, s, tr(w.lang(r), "ctx.saved"), nil)
}

func (w *web) handleNewContextSource(rw http.ResponseWriter, r *http.Request, s *session) {
	src, tok, err := w.c.newContextSource(r.PostFormValue("name"))
	if err != nil {
		w.toast(rw, tr(w.lang(r), "ctx.err.source"), false)
		return
	}
	fresh := &freshCtxSource{Name: src.Name, Token: tok, URL: w.coreURL(r) + "/v1/context"}
	w.renderContext(rw, r, s, tr(w.lang(r), "ctx.did.source"), fresh)
}

func (w *web) handleRevokeContextSource(rw http.ResponseWriter, r *http.Request, s *session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = w.c.revokeContextSource(id)
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderContext(rw, r, s, tr(w.lang(r), "ctx.did.revoke"), nil)
}

// handleContextRooms keeps the user's rooms: the only values a room takes.
// A room taken out fades with its signals.
func (w *web) handleContextRooms(rw http.ResponseWriter, r *http.Request, s *session) {
	rooms := parseRooms(r.PostFormValue("rooms"))
	w.c.store.set(ctxRoomsKey, strings.Join(rooms, ", "))
	for _, sig := range w.c.known.fresh(time.Now()) {
		if sig.Kind == sigRoom && !slices.Contains(rooms, sig.Value) {
			w.c.known.forget(sig.Source, sigRoom)
		}
	}
	w.renderContext(rw, r, s, tr(w.lang(r), "ctx.saved"), nil)
}

// handleRemoveMapping drops an entity's mapping: its signal fades at once.
func (w *web) handleRemoveMapping(rw http.ResponseWriter, r *http.Request, s *session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = w.c.removeMapping(id)
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderContext(rw, r, s, tr(w.lang(r), "ctx.ha.did.remove"), nil)
}

// handleMappingProposal approves or rejects a mapping the brain proposed
// (À valider).
func (w *web) handleMappingProposal(rw http.ResponseWriter, r *http.Request, s *session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	action := r.PathValue("action")
	if action != "approve" && action != "reject" {
		http.Error(rw, "approve or reject", http.StatusBadRequest)
		return
	}
	if err := w.c.decideMapping(r.Context(), id, action == "approve"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.c.proposalDecided(cardPresence, id, action == "approve") // its card says so (convo_cards.go)
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "ctx.ha.did."+action), true, true))
	rw.Header().Set("HX-Reswap", "none")
	rw.WriteHeader(http.StatusOK)
}

// handleVoiceUrgent turns on or off the voice for an urgent watch's
// fixed message (voice_for_urgent).
func (w *web) handleVoiceUrgent(rw http.ResponseWriter, r *http.Request, s *session) {
	v := "off"
	if r.PostFormValue("on") == "1" {
		v = "on"
	}
	w.c.store.set(voiceUrgentKey, v)
	w.renderContext(rw, r, s, tr(w.lang(r), "ctx.saved"), nil)
}

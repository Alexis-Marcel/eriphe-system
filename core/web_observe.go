package main

import (
	"net/http"
	"slices"
	"strconv"
	"time"
)

// Settings → Ce qu'Eriphe observe (observe.go, ha_follow.go): what Eriphe
// observes now, live: each machine (locked, idle, do not disturb) and each
// Home Assistant entity the user follows (its state and area), the room
// the voice line would use, the entities to follow or stop following, and
// whether an urgent skill's fixed message may be said aloud. Nothing here
// is kept in the database but the list of followed entities.

func (w *web) observeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/observe", w.auth(w.handleObservePage))
	mux.Handle("GET /settings/observe/live", w.auth(w.handleObserveLive))
	mux.Handle("POST /settings/observe/follow", w.auth(w.handleFollow))
	mux.Handle("POST /settings/observe/unfollow", w.auth(w.handleUnfollow))
	mux.Handle("POST /settings/observe/proposals/{id}/{action}", w.auth(w.handleFollowProposal))
	mux.Handle("POST /settings/observe/voice-urgent", w.auth(w.handleVoiceUrgent))
	mux.Handle("GET /settings/context", w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
		http.Redirect(rw, r, "/settings/observe", http.StatusSeeOther) // its old address
	}))
}

// observedMachine is a machine as the page shows it.
type observedMachine struct {
	Name     string
	OS       string
	Presence string // locked, idle, active, or "" (unknown)
	IdleMin  int
	DND      bool
	Reason   string
}

// followedView is a followed entity as the page shows it.
type followedView struct {
	Entity  string
	Name    string
	Area    string
	State   string
	Changed time.Time
}

type observeLive struct {
	Machines []observedMachine
	Followed []followedView
	Room     string // the room the voice line would use now
	HALinked bool
	HAOn     bool // connected and following
	HAErr    string
}

type observePage struct {
	Live        observeLive
	Candidates  []haEntity // the house's entities not followed yet, when discovery saw them
	Proposed    int
	VoiceUrgent bool
}

func (w *web) observeLive(now time.Time) observeLive {
	c := w.c
	var v observeLive
	if nodes, err := c.store.nodes(); err == nil {
		for _, n := range nodes {
			mv := observedMachine{Name: n.Name, OS: n.OS}
			if m, ok := c.obs.machine(n.Name, now); ok {
				mv.Presence, mv.IdleMin, mv.DND, mv.Reason = m.presence(), int(m.Idle/time.Minute), m.DND, m.Reason
			}
			v.Machines = append(v.Machines, mv)
		}
	}
	_, err := c.haLink()
	v.HALinked = err == nil
	v.HAOn, v.HAErr = c.haStatus()
	house, states := c.ha.cached(), c.ha.snapshot()
	for _, id := range c.followedEntities() {
		fv := followedView{Entity: id}
		if e, ok := house.entity(id); ok {
			fv.Name, fv.Area = haShown(e.Name), e.Area
		}
		if st, ok := states[id]; ok {
			fv.State, fv.Changed = haStateShown(st.State), st.Changed
		}
		v.Followed = append(v.Followed, fv)
	}
	v.Room, _ = c.haUserRoom(now)
	return v
}

func (w *web) observePage() observePage {
	c := w.c
	v := observePage{Live: w.observeLive(time.Now()), Proposed: len(c.haFollows(ruleProposed)), VoiceUrgent: c.voiceUrgent()}
	followed := c.followedEntities()
	for _, e := range c.ha.cached().Entities {
		if !slices.Contains(followed, e.ID) {
			e.Name = haShown(e.Name)
			v.Candidates = append(v.Candidates, e)
		}
	}
	return v
}

func (w *web) handleObservePage(rw http.ResponseWriter, r *http.Request, s *session) {
	if v := w.c.ha.cached(); v.At.IsZero() || time.Since(v.At) > haHouseFresh {
		w.c.haHouse(r.Context()) // the candidates, for the form; none when Home Assistant is out of reach
	}
	w.render(rw, r, s, "settings", "", page{Title: "obs.title", Nav: "settings", Tab: "observe", Data: w.observePage()})
}

// handleObserveLive is the live view alone, polled by the page.
func (w *web) handleObserveLive(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "observe-live", page{Data: w.observeLive(time.Now())})
}

func (w *web) renderObserve(rw http.ResponseWriter, r *http.Request, s *session, msg string) {
	rw.Header().Set("HX-Trigger", hxTrigger(msg, true, true))
	w.render(rw, r, s, "settings", "observe-frag", page{Data: w.observePage()})
}

// handleFollow follows an entity the user picked: their own say.
func (w *web) handleFollow(rw http.ResponseWriter, r *http.Request, s *session) {
	house, err := w.c.haHouse(r.Context())
	var e haEntity
	if err == nil {
		e, err = checkFollow(r.PostFormValue("entity"), house)
	}
	if err == nil {
		_, err = w.c.saveFollow(haFollow{Entity: e.ID, State: ruleActive, Why: tr("fr", "obs.byuser")})
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderObserve(rw, r, s, tr(w.lang(r), "obs.did.follow"))
}

func (w *web) handleUnfollow(rw http.ResponseWriter, r *http.Request, s *session) {
	if err := w.c.unfollow(r.PostFormValue("entity")); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderObserve(rw, r, s, tr(w.lang(r), "obs.did.unfollow"))
}

// handleFollowProposal approves or rejects an entity the brain proposed
// to follow (À valider).
func (w *web) handleFollowProposal(rw http.ResponseWriter, r *http.Request, s *session) {
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
	if err := w.c.decideFollow(r.Context(), id, action == "approve"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.c.proposalDecided(cardPresence, id, action == "approve") // its card says so (convo_cards.go)
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "obs.did."+action), true, true))
	rw.Header().Set("HX-Reswap", "none")
	rw.WriteHeader(http.StatusOK)
}

// handleVoiceUrgent turns on or off the voice for an urgent skill's fixed
// message (voice_for_urgent).
func (w *web) handleVoiceUrgent(rw http.ResponseWriter, r *http.Request, s *session) {
	v := "off"
	if r.PostFormValue("on") == "1" {
		v = "on"
	}
	w.c.store.set(voiceUrgentKey, v)
	w.renderObserve(rw, r, s, tr(w.lang(r), "obs.saved"))
}

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The work sessions in the workspace (sessions.go): each one's page, its
// plan drawn as stages (the steps that may run together side by side),
// each step with how far its evidence goes (claimed, verified, accepted),
// what waits for the user at the top, the log, and a line to the lead.
// The page follows the session live, through its own event stream. The
// sessions running also sit on top of the tasks panel.

func (w *web) sessionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /sessions", w.auth(w.handleSessions))
	mux.Handle("GET /sessions/{id}", w.auth(w.handleSession))
	mux.Handle("GET /sessions/{id}/events", w.auth(w.handleSessionEvents))
	mux.Handle("POST /sessions/{id}/{action}", w.auth(w.handleSessionAction))
}

// stepView is a step as the page shows it.
type stepView struct {
	Step
	Proof    string // none, claimed, verified, failed, accepted, rejected: how far its evidence goes
	TaskLink string // its task, in the drawer
	Retries  int    // left
}

// proofOf says how far a step's evidence goes: a report is only a claim.
func proofOf(st Step) string {
	switch {
	case st.Status == stepAccepted:
		return "accepted"
	case st.Status == stepRejected || st.Status == stepBlocked:
		return "rejected"
	case st.Failed:
		return "failed"
	case st.Status == stepVerified:
		return "verified"
	case st.Status == stepReported:
		return "claimed"
	}
	return "none"
}

// sessionPage is a session's page.
type sessionPage struct {
	sessionView
	Stages   []stageView // by the steps they come after: the first can start at once
	Asks     []cardView  // its cards that wait for the user
	Choices  []stepView  // its choices that wait for the user
	LeadName string      // its lead's name, when it has one
	LeadBusy bool        // its lead works on a turn
	Minutes  int         // the time it ran
	Percent  int         // of its budget
	Counts   map[string]int
	Total    int
}

// Active tells whether it may still move.
func (p sessionPage) Active() bool { return p.Session.active() }

// stageView is a stage of the plan: the steps that may run together.
type stageView struct {
	N     int
	Final bool // the final objective check
	Steps []stepView
}

// stages groups steps by depth: a step comes one stage after the deepest
// step it needs. The final check comes last.
func stages(steps []Step) []stageView {
	depth := map[string]int{}
	var deep func(st Step, seen map[string]bool) int
	byKey := map[string]Step{}
	for _, st := range steps {
		byKey[st.Key] = st
	}
	deep = func(st Step, seen map[string]bool) int {
		if d, ok := depth[st.Key]; ok {
			return d
		}
		if seen[st.Key] {
			return 0
		}
		seen[st.Key] = true
		d := 0
		for _, k := range st.After {
			if p, ok := byKey[k]; ok {
				d = max(d, deep(p, seen)+1)
			}
		}
		depth[st.Key] = d
		return d
	}
	last := 0
	for _, st := range steps {
		if st.Key != finalStep {
			last = max(last, deep(st, map[string]bool{}))
		}
	}
	out := make([][]stepView, last+1)
	for _, st := range steps {
		d := depth[st.Key]
		if st.Key == finalStep {
			out = append(out, nil)
			d = len(out) - 1
		}
		v := stepView{Step: st, Proof: proofOf(st), Retries: max(0, stepRetries-st.Tries)}
		if st.Task != "" {
			v.TaskLink = "/tasks/" + st.Task
		}
		out[d] = append(out[d], v)
	}
	var views []stageView
	for i, xs := range out {
		if len(xs) > 0 {
			views = append(views, stageView{N: len(views) + 1, Final: xs[0].Key == finalStep && i == len(out)-1, Steps: xs})
		}
	}
	return views
}

func (w *web) sessionPage(id int64, lang string) (sessionPage, error) {
	v, err := w.c.sessionView(id, 40)
	if err != nil {
		return sessionPage{}, err
	}
	now := time.Now()
	p := sessionPage{sessionView: v, Stages: stages(v.Steps), Counts: map[string]int{}}
	p.Minutes = int(v.Session.elapsed(now) / time.Minute)
	if v.Budget > 0 {
		p.Percent = min(100, int(v.Session.elapsed(now)*100/(time.Duration(v.Budget)*time.Minute)))
	}
	for _, st := range v.Steps {
		if st.Status == stepDropped {
			continue
		}
		p.Total++
		p.Counts[st.Status]++
		if st.Kind == stepChoice && st.Status == stepRunning && v.Session.active() {
			p.Choices = append(p.Choices, stepView{Step: st})
		}
	}
	if w.c.hasLead(v.Session) {
		a := w.c.conf().Assistants[v.Lead]
		p.LeadName = a.displayName()
		if t, err := w.c.store.task(w.c.assistantStatus(v.Lead).Task); err == nil {
			p.LeadBusy = t.State == stateRunning
		}
	}
	for _, aid := range w.c.sessionAsksOf(id) {
		xs, _ := w.c.cardsWhere(`ref = ?`, proposalRef(cardSession, aid))
		for _, x := range xs {
			if cv := w.cardView(x, lang); cv.State == cardPending {
				p.Asks = append(p.Asks, cv)
			}
		}
	}
	return p, nil
}

// sessionAsksOf are the asks of a session that wait.
func (c *core) sessionAsksOf(id int64) []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM session_asks WHERE session = ? AND state = ? ORDER BY id`, id, askPending)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}

// sessionRow is a session in a list: the tasks panel, the sessions page.
type sessionRow struct {
	Session
	Steps    []Step
	Waits    bool // something waits for the user
	Done     int
	Total    int
	Updated  time.Time
	Progress []string // each step's status, in order, for its strip
}

func (w *web) sessionRows(activeOnly bool, n int) []sessionRow {
	ss, err := w.c.sessions(activeOnly, n)
	if err != nil {
		return nil
	}
	out := make([]sessionRow, 0, len(ss))
	for _, s := range ss {
		steps, _ := w.c.steps(s.ID)
		x := sessionRow{Session: s, Steps: steps, Updated: s.Created}
		for _, st := range steps {
			if st.Status == stepDropped {
				continue
			}
			x.Total++
			x.Progress = append(x.Progress, st.Status)
			if st.Status == stepAccepted {
				x.Done++
			}
			if st.Kind == stepChoice && st.Status == stepRunning {
				x.Waits = true
			}
		}
		if s.active() && len(w.c.sessionAsksOf(s.ID)) > 0 {
			x.Waits = true
		}
		if l := w.c.sessionLog(s.ID, 1); len(l) > 0 {
			x.Updated = l[0].At
		}
		out = append(out, x)
	}
	return out
}

func (w *web) handleSessions(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "sessions", "", page{Title: "sessions.title", Nav: "chat", View: "sessions", Data: w.sessionRows(false, 60)})
}

func sessionID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (w *web) handleSession(rw http.ResponseWriter, r *http.Request, s *session) {
	id, ok := sessionID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	p, err := w.sessionPage(id, w.lang(r))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if r.URL.Query().Get("part") == "live" {
		w.render(rw, r, s, "sessions", "session-live", page{Data: p})
		return
	}
	w.render(rw, r, s, "sessions", "", page{Title: "sessions.title", Nav: "chat", View: "session", Data: p})
}

// handleSessionEvents streams the live part of a session's page when it
// changes, as remote.js does for the remote access.
func (w *web) handleSessionEvents(rw http.ResponseWriter, r *http.Request, s *session) {
	id, ok := sessionID(r)
	flusher, canFlush := rw.(http.Flusher)
	if !ok || !canFlush {
		http.NotFound(rw, r)
		return
	}
	lang := w.lang(r)
	h := rw.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "retry: 3000\n\n")
	flusher.Flush()
	var last string
	quiet := time.Now()
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	for {
		if p, err := w.sessionPage(id, lang); err == nil {
			if html, err := w.fragment(lang, "sessions", "session-live", page{Lang: lang, Data: p}); err == nil && html != last {
				writeEvent(rw, "session", 0, html)
				last, quiet = html, time.Now()
				flusher.Flush()
			}
		}
		if time.Since(quiet) > 25*time.Second {
			fmt.Fprint(rw, ": keep-alive\n\n")
			flusher.Flush()
			quiet = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		case <-w.waker():
		}
	}
}

// handleSessionAction is the user's: pause, resume (more time, if the
// budget paused it), stop, a choice, a word to the lead.
func (w *web) handleSessionAction(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	id, ok := sessionID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	var err error
	msg := ""
	switch r.PathValue("action") {
	case "pause":
		err, msg = w.c.pauseSession(id, "user"), "sessions.did.pause"
	case "resume":
		err, msg = w.c.resumeSession(r.Context(), id, byUser), "sessions.did.resume"
	case "stop":
		err, msg = w.c.stopSession(r.Context(), id, "user"), "sessions.did.stop"
	case "choose":
		_, err = w.c.chooseStep(r.Context(), id, r.PostFormValue("step"), r.PostFormValue("option"), "user")
		msg = "sessions.did.choose"
	case "lead":
		err, msg = w.c.tellLeadFromUser(id, r.PostFormValue("text")), "sessions.did.lead"
	default:
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.notify()
	rw.Header().Set("HX-Trigger", hxTrigger(tr(lang, msg), true, true))
	p, err := w.sessionPage(id, lang)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	w.render(rw, r, s, "sessions", "session-live", page{Data: p})
}

// tellLeadFromUser passes the lead the user's own words, from the
// session's page.
func (c *core) tellLeadFromUser(id int64, text string) error {
	s, err := c.session(id)
	if err != nil {
		return err
	}
	if !s.active() || !c.hasLead(s) {
		return errNoLead
	}
	if text = strings.TrimSpace(text); text == "" {
		return errEmptyText
	}
	text = truncate(text, 2000)
	c.logSession(id, "user", "Au chef : "+text)
	c.queueLead(s, "De la part de "+c.conf().User+" (son message, depuis la page de la session) : "+text, nil)
	return nil
}

var (
	errNoLead    = fmt.Errorf("this session has no lead to write to")
	errEmptyText = fmt.Errorf("write something first")
)

// The tasks panel: the sessions running, above the tasks.

// sessionOfTask is the session a task is a step of: its id and title.
func (c *core) sessionOfTask(id string) (int64, string) {
	st, ok := c.stepOfTask(id)
	if !ok {
		return 0, ""
	}
	s, err := c.session(st.Session)
	if err != nil {
		return 0, ""
	}
	return s.ID, s.Title
}

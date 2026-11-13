package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The work threads in the workspace (threads.go): each one's page, its
// plan drawn as stages (the steps that may run together side by side),
// each step with how far its evidence goes (claimed, verified, accepted),
// what waits for the user at the top, the log, and a line to the lead.
// The page follows the thread live, through its own event stream. The
// threads running also sit on top of the tasks panel.

func (w *web) threadRoutes(mux *webMux) {
	mux.Handle("GET /threads", w.auth(w.handleThreads))
	mux.Handle("GET /threads/{id}", w.auth(w.handleThread))
	mux.Handle("GET /threads/{id}/events", w.auth(w.handleThreadEvents))
	mux.Handle("POST /threads/{id}/{action}", w.auth(w.handleThreadAction))
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

// threadPage is a thread's page.
type threadPage struct {
	threadView
	Stages   []stageView // by the steps they come after: the first can start at once
	Asks     []cardView  // its cards that wait for the user
	Choices  []stepView  // its choices that wait for the user
	Users    []stepView  // what only the user does: « C'est fait »
	LeadName string      // its lead's name, when it has one
	LeadBusy bool        // its lead works on a turn
	Minutes  int         // the time it ran
	Percent  int         // of its budget
	Counts   map[string]int
	Total    int
}

// Active tells whether it may still move.
func (p threadPage) Active() bool { return p.Thread.active() }

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

func (w *web) threadPage(id int64, lang string) (threadPage, error) {
	v, err := w.c.threadView(id, 40)
	if err != nil {
		return threadPage{}, err
	}
	now := time.Now()
	p := threadPage{threadView: v, Stages: stages(v.Steps), Counts: map[string]int{}}
	p.Minutes = int(v.Thread.elapsed(now) / time.Minute)
	if v.Budget > 0 {
		p.Percent = min(100, int(v.Thread.elapsed(now)*100/(time.Duration(v.Budget)*time.Minute)))
	}
	for _, st := range v.Steps {
		if st.Status == stepDropped {
			continue
		}
		p.Total++
		p.Counts[st.Status]++
		if st.Kind == stepChoice && st.Status == stepRunning && v.Thread.active() {
			p.Choices = append(p.Choices, stepView{Step: st})
		}
		if st.Kind == stepUser && st.Status == stepRunning && v.Thread.active() {
			p.Users = append(p.Users, stepView{Step: st})
		}
		if st.Card != "" && st.Status == stepRunning {
			xs, _ := w.c.cardsWhere(`ref = ?`, st.Card)
			for _, x := range xs {
				if cv := w.cardView(x, lang); cv.State == cardPending {
					p.Asks = append(p.Asks, cv) // a call step's yes
				}
			}
		}
	}
	if w.c.hasLead(v.Thread) {
		a := w.c.conf().Assistants[v.Lead]
		p.LeadName = a.displayName()
		if t, err := w.c.store.task(w.c.assistantStatus(v.Lead).Task); err == nil {
			p.LeadBusy = t.State == stateRunning
		}
	}
	for _, aid := range w.c.threadAsksOf(id) {
		xs, _ := w.c.cardsWhere(`ref = ?`, proposalRef(cardThread, aid))
		for _, x := range xs {
			if cv := w.cardView(x, lang); cv.State == cardPending {
				p.Asks = append(p.Asks, cv)
			}
		}
	}
	return p, nil
}

// threadAsksOf are the asks of a thread that wait.
func (c *core) threadAsksOf(id int64) []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM thread_asks WHERE thread = ? AND state = ? ORDER BY id`, id, askPending)
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

// threadRow is a thread in a list: the tasks panel, the threads page.
type threadRow struct {
	Thread
	Steps    []Step
	Waits    bool // something waits for the user
	Done     int
	Total    int
	Updated  time.Time
	Progress []string // each step's status, in order, for its strip
}

func (w *web) threadRows(activeOnly bool, n int) []threadRow {
	ss, err := w.c.threads(activeOnly, n)
	if err != nil {
		return nil
	}
	out := make([]threadRow, 0, len(ss))
	for _, s := range ss {
		steps, _ := w.c.steps(s.ID)
		x := threadRow{Thread: s, Steps: steps, Updated: s.Created}
		for _, st := range steps {
			if st.Status == stepDropped {
				continue
			}
			x.Total++
			x.Progress = append(x.Progress, st.Status)
			if st.Status == stepAccepted {
				x.Done++
			}
			if (st.Kind == stepChoice || st.Kind == stepUser || st.Card != "") && st.Status == stepRunning {
				x.Waits = true
			}
		}
		if s.active() && len(w.c.threadAsksOf(s.ID)) > 0 {
			x.Waits = true
		}
		if l := w.c.threadLog(s.ID, 1); len(l) > 0 {
			x.Updated = l[0].At
		}
		out = append(out, x)
	}
	return out
}

func (w *web) handleThreads(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "threads", "", page{Title: "threads.title", Nav: "chat", View: "threads", Data: w.threadRows(false, 60)})
}

func threadID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (w *web) handleThread(rw http.ResponseWriter, r *http.Request, s *session) {
	id, ok := threadID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	p, err := w.threadPage(id, w.lang(r))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if r.URL.Query().Get("part") == "live" {
		w.render(rw, r, s, "threads", "thread-live", page{Data: p})
		return
	}
	w.render(rw, r, s, "threads", "", page{Title: "threads.title", Nav: "chat", View: "thread", Data: p})
}

// handleThreadEvents streams the live part of a thread's page when it
// changes, as remote.js does for the remote access.
func (w *web) handleThreadEvents(rw http.ResponseWriter, r *http.Request, s *session) {
	id, ok := threadID(r)
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
		if p, err := w.threadPage(id, lang); err == nil {
			if html, err := w.fragment(lang, "threads", "thread-live", page{Lang: lang, Data: p}); err == nil && html != last {
				writeEvent(rw, "thread", 0, html)
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

// handleThreadAction is the user's: pause, resume (more time, if the
// budget paused it), stop, a choice, « C'est fait » on a user step, a
// word to the lead.
func (w *web) handleThreadAction(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	id, ok := threadID(r)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	var err error
	msg := ""
	switch r.PathValue("action") {
	case "pause":
		err, msg = w.c.pauseThread(id, "user"), "threads.did.pause"
	case "resume":
		err, msg = w.c.resumeThread(r.Context(), id, byUser), "threads.did.resume"
	case "stop":
		err, msg = w.c.stopThread(r.Context(), id, "user"), "threads.did.stop"
	case "choose":
		_, err = w.c.chooseStep(r.Context(), id, r.PostFormValue("step"), r.PostFormValue("option"), "user")
		msg = "threads.did.choose"
	case "done": // « C'est fait » on a user step (thread_steps.go)
		_, err = w.c.userStepDone(r.Context(), id, r.PostFormValue("step"), "user")
		msg = "threads.did.done"
	case "lead":
		err, msg = w.c.tellLeadFromUser(id, r.PostFormValue("text")), "threads.did.lead"
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
	p, err := w.threadPage(id, lang)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	w.render(rw, r, s, "threads", "thread-live", page{Data: p})
}

// tellLeadFromUser passes the lead the user's own words, from the
// thread's page.
func (c *core) tellLeadFromUser(id int64, text string) error {
	s, err := c.thread(id)
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
	c.logThread(id, "user", "Au chef : "+text)
	c.queueLead(s, "De la part de "+c.conf().User+" (son message, depuis la page du thread) : "+text, nil)
	return nil
}

var (
	errNoLead    = fmt.Errorf("this thread has no lead to write to")
	errEmptyText = fmt.Errorf("write something first")
)

// The tasks panel: the threads running, above the tasks.

// threadOfTask is the thread a task is a step of: its id and title.
func (c *core) threadOfTask(id string) (int64, string) {
	st, ok := c.stepOfTask(id)
	if !ok {
		return 0, ""
	}
	s, err := c.thread(st.Thread)
	if err != nil {
		return 0, ""
	}
	return s.ID, s.Title
}

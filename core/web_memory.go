package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// The workspace's Mémoire page: what Eriphe remembers, which I can search,
// correct, pin to the profile, mark obsolete, forget, and the proposals
// waiting for my approval. What I write here is mine (trust owner).

func (w *web) memoryRoutes(mux *http.ServeMux) {
	mux.Handle("GET /memory", w.auth(w.handleMemory))
	mux.Handle("GET /memory/export.jsonl", w.auth(w.handleMemoryExport))
	mux.Handle("POST /memory", w.auth(w.handleMemoryAdd))
	mux.Handle("POST /memory/extract", w.auth(w.handleMemoryExtract))
	mux.Handle("POST /memory/interview", w.auth(w.handleMemoryInterview))
	mux.Handle("GET /memory/{id}/card", w.auth(w.handleMemoryCard))
	mux.Handle("GET /memory/{id}/edit", w.auth(w.handleMemoryEditForm))
	mux.Handle("GET /memory/{id}/history", w.auth(w.handleMemoryHistory))
	mux.Handle("POST /memory/{id}", w.auth(w.handleMemoryEdit))
	mux.Handle("POST /memory/{id}/pin", w.auth(w.handleMemoryPin))
	mux.Handle("POST /memory/{id}/obsolete", w.auth(w.handleMemoryObsolete))
	mux.Handle("POST /memory/{id}/state", w.auth(w.handleMemoryState))
	mux.Handle("POST /memory/{id}/forget", w.auth(w.handleMemoryForget))
	mux.Handle("POST /memory/{id}/restore", w.auth(w.handleMemoryRestore))
	mux.Handle("POST /memory/{id}/approve", w.auth(w.handleMemoryApprove))
	mux.Handle("POST /memory/{id}/reject", w.auth(w.handleMemoryReject))
}

var memoryViews = []string{"all", "inbox", "journal", "commitments", "profile", "forgotten"}

// memCard is a memory as the page shows it.
type memCard struct {
	*Memory
	Now        time.Time
	Edit       bool
	Kinds      []string
	SourceWho  string // owner, brain, event, task
	SourceText string
	SourceLink string
	Overdue    bool
	Past       bool
	Error      string
	Size       int      // characters, for the profile's budget
	FlagList   []string // what the scanner noticed
}

type memoryPage struct {
	Off         bool
	View        string
	Q           string
	Kind        string
	Subject     string
	Past        bool
	Items       []memCard
	Done        []memCard
	Days        []memDay
	Subjects    []string
	Kinds       []string
	KindLinks   []kindLink
	AllLink     string
	Active      int
	Proposed    int
	Open        int
	Overdue     int
	Forgotten   int
	ProfileUsed int
	Budget      int
	Pct         int
	Runs        []extractionRun
	Agent       string
	Now         time.Time
	About       []memCard // what the assistant knows about me, pinned first
	Interview   bool      // a brain to do the interview again
}

type kindLink struct {
	Kind, Href string
	Current    bool
}

type memDay struct {
	Day   time.Time
	Label string
	Items []memCard
}

var (
	frDays   = []string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
	frMonths = []string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
)

// dayLabel names a day: today, yesterday, or its date in words.
func dayLabel(lang string, d, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch {
	case d.Equal(today):
		return map[string]string{"fr": "Aujourd'hui", "en": "Today"}[lang]
	case d.Equal(today.AddDate(0, 0, -1)):
		return map[string]string{"fr": "Hier", "en": "Yesterday"}[lang]
	}
	if lang == "en" {
		if d.Year() == now.Year() {
			return d.Format("Monday, January 2")
		}
		return d.Format("Monday, January 2, 2006")
	}
	s := fmt.Sprintf("%s %d %s", frDays[d.Weekday()], d.Day(), frMonths[d.Month()-1])
	if d.Year() != now.Year() {
		s += fmt.Sprintf(" %d", d.Year())
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (w *web) memCard(x *Memory, withSource bool) memCard {
	now := w.c.mem.now()
	c := memCard{Memory: x, Now: now, Kinds: memoryKinds, Past: !x.Current(now), Size: utf8.RuneCountInString(x.Text)}
	for _, f := range strings.Split(x.Flags, ",") {
		if f != "" {
			c.FlagList = append(c.FlagList, f)
		}
	}
	c.Overdue = x.Kind == kindCommitment && x.State == stateOpen && !x.DueAt.IsZero() && x.DueAt.Before(now)
	if withSource {
		c.SourceWho, c.SourceText, c.SourceLink = w.memorySource(x.SourceRef)
	}
	return c
}

// memorySource shows where a proposal comes from: the message or the task.
func (w *web) memorySource(ref string) (who, text, link string) {
	first, _, _ := strings.Cut(ref, ",")
	if seq, ok := parseMsgRef(first); ok {
		msgs, err := w.c.store.messagesAfter(seq-1, 1)
		if err == nil && len(msgs) == 1 && msgs[0].Seq == seq {
			who = map[string]string{roleUser: "owner", roleBrain: "brain"}[msgs[0].Role]
			if who == "" {
				who = "event"
			}
			return who, truncate(msgs[0].Text, 400), "/#m" + strconv.Itoa(seq)
		}
	}
	if id, ok := strings.CutPrefix(first, "task:"); ok {
		if st, err := w.c.status(id, 0); err == nil {
			return "task", truncate(oneLine(st.Task.Prompt), 200) + " → " + truncate(st.LastText, 300), ""
		}
	}
	return "", "", ""
}

func (w *web) memoryCounts(p *memoryPage) {
	m := w.c.mem
	now := m.now().Unix()
	p.Active = m.count(`status = 'active' AND (valid_to IS NULL OR valid_to > ?)`, now)
	p.Proposed = m.count(`status = 'proposed'`)
	p.Open = m.count(`kind = 'commitment' AND status = 'active' AND state = 'open' AND (valid_to IS NULL OR valid_to > ?)`, now)
	p.Overdue = m.count(`kind = 'commitment' AND status = 'active' AND state = 'open' AND due_at IS NOT NULL AND due_at < ?
		AND (valid_to IS NULL OR valid_to > ?)`, now, now)
	p.Forgotten = m.count(`status = 'deleted'`)
	p.ProfileUsed, p.Budget = m.profileChars(m.db, 0), profileBudget
	p.Pct = min(100, p.ProfileUsed*100/profileBudget)
}

func (w *web) handleMemory(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	p := memoryPage{View: q.Get("view"), Q: strings.TrimSpace(q.Get("q")), Kind: q.Get("kind"), Subject: strings.TrimSpace(q.Get("subject")),
		Past: q.Get("past") == "1", Kinds: memoryKinds, Budget: profileBudget}
	if !containsStr(memoryViews, p.View) {
		p.View = "all"
	}
	if !containsStr(memoryKinds, p.Kind) {
		p.Kind = ""
	}
	if w.c.mem == nil {
		p.Off = true
		w.render(rw, r, s, "memory", "", page{Title: "nav.memory", Nav: "memory", Data: p})
		return
	}
	m := w.c.mem
	p.Now = m.now()
	w.memoryCounts(&p)
	var err error
	var xs []*Memory
	shown := map[int64]bool{} // in the section above: not again in the list
	switch p.View {
	case "all":
		if about, err := m.aboutOwner(); err == nil {
			for _, x := range about {
				p.About = append(p.About, w.memCard(x, false))
				if p.Q == "" && p.Kind == "" && p.Subject == "" && !p.Past {
					shown[x.ID] = true
				}
			}
		}
		b := w.c.conf().brain()
		p.Interview = b != nil && b.Agent != ""
		rq := recallQuery{Query: p.Q, Subject: p.Subject, IncludePast: p.Past, Limit: 100}
		if p.Kind != "" {
			rq.Kinds = []string{p.Kind}
		}
		xs, err = m.recall(rq)
		if err == nil {
			p.Subjects = w.subjects()
		}
		p.AllLink = memoryQuery(p, "")
		for _, k := range memoryKinds {
			p.KindLinks = append(p.KindLinks, kindLink{Kind: k, Href: memoryQuery(p, k), Current: p.Kind == k})
		}
	case "inbox":
		xs, err = m.list(`status = 'proposed'`, nil, `recorded_at DESC, id DESC`, 200)
	case "journal":
		xs, err = m.list(`kind = 'episode' AND status = 'active'`, nil, `occurred_at DESC, id DESC`, 300)
		p.Runs, _ = m.runs(5)
		p.Agent = w.c.extractionAgent()
		if p.Agent == "" {
			p.Agent = w.c.conf().DefaultAgent
		}
	case "commitments":
		xs, err = m.commitments(time.Time{})
		if err == nil {
			done, _ := m.list(`kind = 'commitment' AND status = 'active' AND state != 'open'`, nil, `updated_at DESC`, 20)
			for _, x := range done {
				p.Done = append(p.Done, w.memCard(x, false))
			}
		}
	case "profile":
		xs, err = m.profile()
	case "forgotten":
		xs, err = m.list(`status = 'deleted'`, nil, `deleted_at DESC`, 200)
	}
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, x := range xs {
		if shown[x.ID] {
			continue
		}
		card := w.memCard(x, p.View == "inbox")
		if p.View == "journal" {
			day := time.Date(x.OccurredAt.Year(), x.OccurredAt.Month(), x.OccurredAt.Day(), 0, 0, 0, 0, time.Local)
			if n := len(p.Days); n == 0 || !p.Days[n-1].Day.Equal(day) {
				p.Days = append(p.Days, memDay{Day: day, Label: dayLabel(w.lang(r), day, p.Now)})
			}
			p.Days[len(p.Days)-1].Items = append(p.Days[len(p.Days)-1].Items, card)
			continue
		}
		p.Items = append(p.Items, card)
	}
	w.render(rw, r, s, "memory", "", page{Title: "nav.memory", Nav: "memory", Data: p})
}

// handleMemoryInterview asks the assistant for the first-run interview
// again (first_run.go), and opens the conversation where it begins.
func (w *web) handleMemoryInterview(rw http.ResponseWriter, r *http.Request, s *session) {
	if b := w.c.conf().brain(); b == nil || b.Agent == "" {
		http.Error(rw, tr(w.lang(r), "chat.nobrain"), http.StatusConflict)
		return
	}
	if err := w.c.offerInterview(); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(rw, r, "/")
}

func (w *web) subjects() []string {
	rows, err := w.c.mem.db.Query(`SELECT subject FROM memories WHERE status = 'active' AND subject != ''
		GROUP BY subject ORDER BY COUNT(*) DESC, subject LIMIT 60`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// memoryFor reads the memory of a request's {id}, or answers the error.
func (w *web) memoryFor(rw http.ResponseWriter, r *http.Request) *Memory {
	if w.c.mem == nil {
		http.Error(rw, errMemoryOff.Error(), http.StatusServiceUnavailable)
		return nil
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return nil
	}
	x, err := w.c.mem.memory(id)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return nil
	}
	return x
}

func (w *web) renderCard(rw http.ResponseWriter, r *http.Request, s *session, card memCard) {
	w.render(rw, r, s, "memory", "mem-card-frag", page{Data: card})
}

// cardAfter answers an action with the memory's card again, and a toast.
func (w *web) cardAfter(rw http.ResponseWriter, r *http.Request, s *session, id int64, msg string) {
	x, err := w.c.mem.memory(id)
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	if msg != "" {
		rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), msg), true, false))
	}
	w.renderCard(rw, r, s, w.memCard(x, x.Status == memProposed))
}

func (w *web) handleMemoryCard(rw http.ResponseWriter, r *http.Request, s *session) {
	if x := w.memoryFor(rw, r); x != nil {
		w.renderCard(rw, r, s, w.memCard(x, x.Status == memProposed))
	}
}

func (w *web) handleMemoryEditForm(rw http.ResponseWriter, r *http.Request, s *session) {
	if x := w.memoryFor(rw, r); x != nil {
		card := w.memCard(x, false)
		card.Edit = true
		w.renderCard(rw, r, s, card)
	}
}

// ownerScan checks what I type: the same scanner, but only its refusals
// count (my imperative lessons are mine).
func ownerScan(parts ...string) error {
	if res := scanMemoryText(parts...); res.Reject != "" {
		return fmt.Errorf("%s", res.Reject)
	}
	return nil
}

func formLinks(v string) []string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (w *web) handleMemoryEdit(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	text, subject, kind := r.PostFormValue("text"), r.PostFormValue("subject"), r.PostFormValue("kind")
	links := formLinks(r.PostFormValue("links"))
	fail := func(err error) {
		card := w.memCard(x, false)
		card.Edit, card.Error = true, err.Error()
		card.Text, card.Subject, card.Links = text, subject, links
		if containsStr(memoryKinds, kind) {
			card.Kind = kind
		}
		w.renderCard(rw, r, s, card)
	}
	if err := ownerScan(text, subject, strings.Join(links, "\n")); err != nil {
		fail(err)
		return
	}
	p := memoryPatch{Text: &text, Subject: &subject, Links: &links, Kind: &kind}
	if due := r.PostFormValue("due"); x.Kind == kindCommitment || kind == kindCommitment {
		t, err := parseWhen(due)
		if err != nil {
			fail(err)
			return
		}
		p.DueAt = &t
	}
	if _, err := w.c.mem.update(x.ID, p, "owner", ""); err != nil {
		fail(err)
		return
	}
	w.cardAfter(rw, r, s, x.ID, "mem.saved")
}

func (w *web) handleMemoryAdd(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.mem == nil {
		w.toast(rw, errMemoryOff.Error(), false)
		return
	}
	x := &Memory{Kind: r.PostFormValue("kind"), Subject: r.PostFormValue("subject"), Text: r.PostFormValue("text"),
		Links: formLinks(r.PostFormValue("links")), SourceKind: srcOwner, SourceRef: "workspace", Trust: trustOwner, Status: memActive,
		Pinned: r.PostFormValue("pin") == "1"}
	if due, err := parseWhen(r.PostFormValue("due")); err == nil {
		x.DueAt = due
	}
	if err := ownerScan(x.Text, x.Subject, strings.Join(x.Links, "\n")); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	if err := w.c.mem.add(x, "owner"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "mem.added"), true, false))
	w.renderCard(rw, r, s, w.memCard(x, false))
}

func (w *web) handleMemoryPin(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	pin := r.PostFormValue("pin") == "1"
	if x.Status != memActive {
		w.toast(rw, tr(w.lang(r), "mem.notactive"), false)
		return
	}
	if _, err := w.c.mem.update(x.ID, memoryPatch{Pinned: &pin}, "owner", ""); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	msg := "mem.unpinned"
	if pin {
		msg = "mem.pinned"
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), msg), true, true))
	if r.PostFormValue("from") == "profile" && !pin {
		rw.WriteHeader(http.StatusOK) // the card leaves the profile
		return
	}
	nx, _ := w.c.mem.memory(x.ID)
	w.renderCard(rw, r, s, w.memCard(nx, false))
}

func (w *web) handleMemoryObsolete(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	now := w.c.mem.now()
	if !x.Current(now) {
		zero := time.Time{}
		if _, err := w.c.mem.update(x.ID, memoryPatch{ValidTo: &zero}, "owner", "true again"); err != nil {
			w.toast(rw, err.Error(), false)
			return
		}
		w.cardAfter(rw, r, s, x.ID, "mem.current")
		return
	}
	if _, err := w.c.mem.update(x.ID, memoryPatch{ValidTo: &now}, "owner", "no longer true"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.cardAfter(rw, r, s, x.ID, "mem.obsoleted")
}

func (w *web) handleMemoryState(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	state := r.PostFormValue("state")
	if x.Kind != kindCommitment || !containsStr([]string{stateOpen, stateDone, stateDropped}, state) {
		http.Error(rw, "open, done or dropped, for a commitment", http.StatusBadRequest)
		return
	}
	if _, err := w.c.mem.update(x.ID, memoryPatch{State: &state}, "owner", ""); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.cardAfter(rw, r, s, x.ID, "mem.state."+state)
}

// gone answers an action after which the card leaves the list.
func (w *web) gone(rw http.ResponseWriter, r *http.Request, msg string) {
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), msg), true, true))
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleMemoryForget(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	if err := w.c.mem.forget(x.ID, "owner", strings.TrimSpace(r.PostFormValue("reason"))); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.gone(rw, r, "mem.forgotten")
}

func (w *web) handleMemoryRestore(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	if err := w.c.mem.restore(x.ID, "owner"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.gone(rw, r, "mem.restored")
}

func (w *web) handleMemoryApprove(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	if _, err := w.c.mem.approve(x.ID, "owner"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.gone(rw, r, "mem.approved")
}

func (w *web) handleMemoryReject(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	if x.Status != memProposed {
		w.toast(rw, tr(w.lang(r), "mem.notproposed"), false)
		return
	}
	if err := w.c.mem.forget(x.ID, "owner", "rejected"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.gone(rw, r, "mem.rejected")
}

func (w *web) handleMemoryHistory(rw http.ResponseWriter, r *http.Request, s *session) {
	x := w.memoryFor(rw, r)
	if x == nil {
		return
	}
	h, err := w.c.mem.history(x.ID)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.render(rw, r, s, "memory", "mem-history", page{Data: h})
}

func (w *web) handleMemoryExport(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.mem == nil {
		http.Error(rw, errMemoryOff.Error(), http.StatusServiceUnavailable)
		return
	}
	rw.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	rw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="eriphe-memory-%s.jsonl"`, w.c.mem.now().Format("2006-01-02")))
	rw.Header().Set("Cache-Control", "no-store")
	if err := w.c.mem.exportJSONL(rw); err != nil {
		log.Printf("web: memory export: %v", err)
	}
}

// handleMemoryExtract runs the extraction of today now, as the night would.
func (w *web) handleMemoryExtract(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	if w.c.mem == nil {
		w.toast(rw, errMemoryOff.Error(), false)
		return
	}
	day := r.PostFormValue("day")
	if day == "" {
		day = w.c.mem.now().Format("2006-01-02")
	}
	run, err := w.c.startExtraction(r.Context(), day, w.c.mem.now())
	switch {
	case err != nil:
		w.toast(rw, err.Error(), false)
	case run == nil:
		w.toast(rw, tr(lang, "mem.nothing"), true)
	default:
		w.toast(rw, fmt.Sprintf(tr(lang, "mem.extracting"), run.TaskID), true)
	}
}

// memoryQuery keeps the page's filters in its links.
func memoryQuery(p memoryPage, kind string) string {
	v := url.Values{}
	if p.Q != "" {
		v.Set("q", p.Q)
	}
	if kind != "" {
		v.Set("kind", kind)
	}
	if p.Subject != "" {
		v.Set("subject", p.Subject)
	}
	if p.Past {
		v.Set("past", "1")
	}
	if len(v) == 0 {
		return "/memory"
	}
	return "/memory?" + v.Encode()
}

// hxTrigger is the HX-Trigger header of a toast: JSON with every non-ASCII
// character escaped, since browsers read headers as Latin-1.
func hxTrigger(msg string, ok, changed bool) string {
	kind := "ok"
	if !ok {
		kind = "error"
	}
	v := map[string]any{"toast": map[string]string{"message": msg, "kind": kind}}
	if changed {
		v["memory-changed"] = true
	}
	return hxEvents(v)
}

// hxEvents is an HX-Trigger header of events and their details, every
// non-ASCII character escaped.
func hxEvents(v map[string]any) string {
	raw, _ := json.Marshal(v)
	var b strings.Builder
	for _, r := range string(raw) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

func init() {
	for lang, words := range memoryStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var memoryStrings = map[string]map[string]string{
	"fr": {
		"nav.memory":           "Mémoire",
		"mem.lead":             "Ce que {{name}} retient de toi et de tes journées. Tout est ici : corrige, épingle, oublie.",
		"mem.about":            "Ce que {{name}} sait de toi",
		"mem.aboutlead":        "Ce qui est épinglé lui sert dans chaque conversation ; le reste, il le retrouve quand il en a besoin.",
		"mem.aboutnone":        "Rien encore. Une courte interview, cinq minutes, et {{name}} saura l'essentiel.",
		"mem.aboutnobrain":     "Rien encore : {{name}} pourra te poser quelques questions dès qu'il sera configuré.",
		"mem.interview":        "Faire l'interview",
		"mem.off":              "La mémoire de {{name}} n'a pas pu s'ouvrir : voir le journal d'eriphe-core.",
		"mem.export":           "Exporter",
		"mem.view.all":         "Souvenirs",
		"mem.view.inbox":       "À approuver",
		"mem.view.journal":     "Journal",
		"mem.view.commitments": "Engagements",
		"mem.view.profile":     "Profil",
		"mem.view.forgotten":   "Oubliés",
		"mem.stat.active":      "souvenirs",
		"mem.stat.proposed":    "à approuver",
		"mem.stat.open":        "engagements",
		"mem.stat.overdue":     "%d en retard",
		"mem.stat.profile":     "profil",
		"mem.budget":           "%d / %d caractères",
		"mem.budgetlead":       "Le profil est donné à {{name}} au début de chaque conversation, et à nouveau quand il change. Garde-le court : %d caractères au plus.",
		"mem.search":           "Chercher dans la mémoire",
		"mem.searchph":         "anniversaire, Paul, NAS…",
		"mem.searchhint":       "Les accents ne comptent pas ; n'importe quel mot suffit.",
		"mem.subject":          "Sujet",
		"mem.anysubject":       "Tous les sujets",
		"mem.past":             "Inclure ce qui n'est plus vrai",
		"mem.go":               "Chercher",
		"mem.allkinds":         "Tout",
		"mem.none":             "Rien ici pour l'instant. {{name}} retient ce que tu lui demandes, et ce qu'il tire de tes journées chaque nuit.",
		"mem.nomatch":          "Aucun souvenir ne correspond.",
		"mem.inboxlead":        "Ce que {{name}} a lu dans un mail, une page ou une tâche n'entre pas dans sa mémoire sans ton accord.",
		"mem.inboxnone":        "Rien à approuver.",
		"mem.journallead":      "Une entrée par conversation et par tâche terminée, écrite chaque nuit par l'extraction.",
		"mem.journalnone":      "Le journal est vide. Il se remplit chaque nuit à partir de tes conversations.",
		"mem.commitmentslead":  "Ce que {{name}} ou toi avez promis de faire.",
		"mem.commitmentsnone":  "Aucun engagement en cours.",
		"mem.donerecent":       "Terminés récemment",
		"mem.profilenone":      "Le profil est vide. Épingle les souvenirs que {{name}} doit toujours avoir en tête.",
		"mem.forgottenlead":    "Oubliés, ils sont effacés pour de bon 30 jours après. Jusque-là, tu peux les restaurer.",
		"mem.forgottennone":    "Rien d'oublié.",
		"mem.add":              "Ajouter un souvenir",
		"mem.text":             "Texte",
		"mem.textph":           "Ce que {{name}} doit retenir, en une phrase.",
		"mem.kind":             "Type",
		"mem.subjectph":        "owner, person:paul, project:eriphe…",
		"mem.links":            "Liens (un par ligne)",
		"mem.linksph":          "[[Note du coffre]]",
		"mem.due":              "Échéance",
		"mem.pinit":            "Épingler au profil",
		"mem.save":             "Enregistrer",
		"mem.cancel":           "Annuler",
		"mem.edit":             "Modifier",
		"mem.pin":              "Épingler",
		"mem.unpin":            "Détacher",
		"mem.obsolete":         "Plus vrai",
		"mem.current":          "De nouveau vrai",
		"mem.history":          "Historique",
		"mem.forget":           "Oublier",
		"mem.forgetconfirm":    "Oublier ce souvenir ? Il disparaît tout de suite et sera effacé dans 30 jours.",
		"mem.restore":          "Restaurer",
		"mem.approve":          "Approuver",
		"mem.reject":           "Rejeter",
		"mem.done":             "Fait",
		"mem.drop":             "Abandonner",
		"mem.st.done":          "Tenu",
		"mem.st.dropped":       "Abandonné",
		"mem.reopen":           "Rouvrir",
		"mem.pinned":           "Épinglé au profil.",
		"mem.unpinned":         "Retiré du profil.",
		"mem.saved":            "Enregistré.",
		"mem.added":            "Souvenir ajouté.",
		"mem.obsoleted":        "Marqué comme plus vrai.",
		"mem.forgotten":        "Oublié.",
		"mem.restored":         "Restauré.",
		"mem.approved":         "Approuvé : {{name}} s'en souviendra.",
		"mem.rejected":         "Rejeté.",
		"mem.notactive":        "Seul un souvenir actif peut aller au profil.",
		"mem.notproposed":      "Ce souvenir n'attend plus d'approbation.",
		"mem.state.done":       "Engagement tenu.",
		"mem.state.dropped":    "Engagement abandonné.",
		"mem.state.open":       "Engagement rouvert.",
		"mem.nothing":          "Rien de nouveau à extraire aujourd'hui.",
		"mem.extracting":       "Extraction lancée (tâche %s).",
		"mem.extract":          "Extraire aujourd'hui",
		"mem.lastrun":          "Dernières extractions",
		"mem.agent":            "Agent d'extraction : %s",
		"mem.run.running":      "en cours",
		"mem.run.applying":     "en cours",
		"mem.run.done":         "faite",
		"mem.run.failed":       "échouée",
		"mem.run.applied":      "%d écrit(s)",
		"mem.from":             "Source",
		"mem.why":              "Pourquoi à approuver",
		"mem.recorded":         "Noté %s",
		"mem.validfrom":        "vrai depuis le %s",
		"mem.validto":          "plus vrai depuis le %s",
		"mem.dueon":            "pour le %s",
		"mem.overdue":          "En retard",
		"mem.supersededby":     "remplacé par n°%d",
		"mem.used":             "rappelé %d fois",
		"mem.deletedon":        "oublié %s",
		"mem.flag.imperative":  "ressemble à une consigne",
		"mem.flag.url":         "lien inhabituel",
		"mem.flag.url-scheme":  "lien inhabituel",
		"mem.noh":              "Pas d'historique.",
		"kind.profile":         "Profil",
		"kind.preference":      "Préférence",
		"kind.fact":            "Fait",
		"kind.person":          "Personne",
		"kind.episode":         "Épisode",
		"kind.commitment":      "Engagement",
		"kind.lesson":          "Leçon",
		"trust.owner":          "De toi",
		"trust.first_party":    "D'Eriphe",
		"trust.external":       "De l'extérieur",
		"src.owner":            "toi",
		"src.brain":            "{{name}}",
		"src.extraction":       "extraction",
		"src.task":             "tâche",
		"src.import":           "import",
		"who.owner":            "Toi",
		"who.brain":            "{{name}}",
		"who.event":            "Événement",
		"who.task":             "Tâche",
		"op.add":               "Ajouté",
		"op.propose":           "Proposé",
		"op.update":            "Modifié",
		"op.supersede":         "Remplacé",
		"op.approve":           "Approuvé",
		"op.forget":            "Oublié",
		"op.restore":           "Restauré",
		"op.purge":             "Effacé",
	},
	"en": {
		"nav.memory":           "Memory",
		"mem.lead":             "What {{name}} remembers about you and your days. It is all here: correct, pin, forget.",
		"mem.about":            "What {{name}} knows about you",
		"mem.aboutlead":        "What is pinned helps in every conversation; the rest, {{name}} looks up when needed.",
		"mem.aboutnone":        "Nothing yet. A short interview, five minutes, and {{name}} will know the essentials.",
		"mem.aboutnobrain":     "Nothing yet: {{name}} can ask you a few questions once it is set up.",
		"mem.interview":        "Do the interview",
		"mem.off":              "{{name}}'s memory could not open: see eriphe-core's log.",
		"mem.export":           "Export",
		"mem.view.all":         "Memories",
		"mem.view.inbox":       "To approve",
		"mem.view.journal":     "Journal",
		"mem.view.commitments": "Commitments",
		"mem.view.profile":     "Profile",
		"mem.view.forgotten":   "Forgotten",
		"mem.stat.active":      "memories",
		"mem.stat.proposed":    "to approve",
		"mem.stat.open":        "commitments",
		"mem.stat.overdue":     "%d overdue",
		"mem.stat.profile":     "profile",
		"mem.budget":           "%d / %d characters",
		"mem.budgetlead":       "The profile is given to {{name}} at the start of every conversation, and again when it changes. Keep it short: %d characters at most.",
		"mem.search":           "Search the memory",
		"mem.searchph":         "birthday, Paul, NAS…",
		"mem.searchhint":       "Accents do not matter; any word is enough.",
		"mem.subject":          "Subject",
		"mem.anysubject":       "All subjects",
		"mem.past":             "Include what is no longer true",
		"mem.go":               "Search",
		"mem.allkinds":         "All",
		"mem.none":             "Nothing here yet. {{name}} remembers what you ask it to, and what it draws from your days every night.",
		"mem.nomatch":          "No memory matches.",
		"mem.inboxlead":        "What {{name}} read in a mail, a page or a task does not enter its memory without your approval.",
		"mem.inboxnone":        "Nothing to approve.",
		"mem.journallead":      "One entry per conversation and per finished task, written every night by the extraction.",
		"mem.journalnone":      "The journal is empty. It fills up every night from your conversations.",
		"mem.commitmentslead":  "What {{name}} or you promised to do.",
		"mem.commitmentsnone":  "No open commitment.",
		"mem.donerecent":       "Recently closed",
		"mem.profilenone":      "The profile is empty. Pin the memories {{name}} should always keep in mind.",
		"mem.forgottenlead":    "Forgotten memories are erased for good 30 days later. Until then, you can restore them.",
		"mem.forgottennone":    "Nothing forgotten.",
		"mem.add":              "Add a memory",
		"mem.text":             "Text",
		"mem.textph":           "What {{name}} should remember, in one sentence.",
		"mem.kind":             "Kind",
		"mem.subjectph":        "owner, person:paul, project:eriphe…",
		"mem.links":            "Links (one per line)",
		"mem.linksph":          "[[Vault note]]",
		"mem.due":              "Due",
		"mem.pinit":            "Pin to the profile",
		"mem.save":             "Save",
		"mem.cancel":           "Cancel",
		"mem.edit":             "Edit",
		"mem.pin":              "Pin",
		"mem.unpin":            "Unpin",
		"mem.obsolete":         "No longer true",
		"mem.current":          "True again",
		"mem.history":          "History",
		"mem.forget":           "Forget",
		"mem.forgetconfirm":    "Forget this memory? It disappears at once and is erased in 30 days.",
		"mem.restore":          "Restore",
		"mem.approve":          "Approve",
		"mem.reject":           "Reject",
		"mem.done":             "Done",
		"mem.drop":             "Drop",
		"mem.st.done":          "Kept",
		"mem.st.dropped":       "Dropped",
		"mem.reopen":           "Reopen",
		"mem.pinned":           "Pinned to the profile.",
		"mem.unpinned":         "Removed from the profile.",
		"mem.saved":            "Saved.",
		"mem.added":            "Memory added.",
		"mem.obsoleted":        "Marked as no longer true.",
		"mem.forgotten":        "Forgotten.",
		"mem.restored":         "Restored.",
		"mem.approved":         "Approved: {{name}} will remember it.",
		"mem.rejected":         "Rejected.",
		"mem.notactive":        "Only an active memory can go to the profile.",
		"mem.notproposed":      "This memory no longer waits for approval.",
		"mem.state.done":       "Commitment kept.",
		"mem.state.dropped":    "Commitment dropped.",
		"mem.state.open":       "Commitment reopened.",
		"mem.nothing":          "Nothing new to extract today.",
		"mem.extracting":       "Extraction started (task %s).",
		"mem.extract":          "Extract today",
		"mem.lastrun":          "Recent extractions",
		"mem.agent":            "Extraction agent: %s",
		"mem.run.running":      "running",
		"mem.run.applying":     "running",
		"mem.run.done":         "done",
		"mem.run.failed":       "failed",
		"mem.run.applied":      "%d written",
		"mem.from":             "Source",
		"mem.why":              "Why it waits",
		"mem.recorded":         "Noted %s",
		"mem.validfrom":        "true since %s",
		"mem.validto":          "no longer true since %s",
		"mem.dueon":            "due %s",
		"mem.overdue":          "Overdue",
		"mem.supersededby":     "replaced by #%d",
		"mem.used":             "recalled %d times",
		"mem.deletedon":        "forgotten %s",
		"mem.flag.imperative":  "reads like an instruction",
		"mem.flag.url":         "unusual link",
		"mem.flag.url-scheme":  "unusual link",
		"mem.noh":              "No history.",
		"kind.profile":         "Profile",
		"kind.preference":      "Preference",
		"kind.fact":            "Fact",
		"kind.person":          "Person",
		"kind.episode":         "Episode",
		"kind.commitment":      "Commitment",
		"kind.lesson":          "Lesson",
		"trust.owner":          "From you",
		"trust.first_party":    "From Eriphe",
		"trust.external":       "External",
		"src.owner":            "you",
		"src.brain":            "{{name}}",
		"src.extraction":       "extraction",
		"src.task":             "task",
		"src.import":           "import",
		"who.owner":            "You",
		"who.brain":            "{{name}}",
		"who.event":            "Event",
		"who.task":             "Task",
		"op.add":               "Added",
		"op.propose":           "Proposed",
		"op.update":            "Edited",
		"op.supersede":         "Replaced",
		"op.approve":           "Approved",
		"op.forget":            "Forgotten",
		"op.restore":           "Restored",
		"op.purge":             "Erased",
	},
}

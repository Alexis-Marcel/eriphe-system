package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Activité: what one consults, beside the conversation and its tasks, out
// of the settings. Two views: the feed the brain reads with since
// (since.go), by kind, the tool accesses among them (the decisions'
// journal); and « À valider », the one place where everything the brain
// proposed waits for the user: permissions, memories, intentions,
// notification rules, watches, and the quiet hours the activity suggests.
// The pages these used to have lead here.

const happenedPageMax = 200

func (w *web) activityRoutes(mux *http.ServeMux) {
	mux.Handle("GET /activity", w.auth(w.handleActivity))
	mux.Handle("GET /activity/approve", w.auth(w.handleApprovals))
	mux.Handle("POST /settings/proposals/{id}/{action}", w.auth(w.handleSettingProposal))
	// The old addresses, in notifications already sent and in the brain's
	// messages: they keep working.
	moved := func(to string, keep bool) http.Handler {
		return w.auth(func(rw http.ResponseWriter, r *http.Request, s *session) {
			u := to
			if keep && r.URL.RawQuery != "" {
				u += "?" + r.URL.RawQuery
			}
			http.Redirect(rw, r, u, http.StatusSeeOther)
		})
	}
	mux.Handle("GET /settings/happened", moved("/activity", true))
	mux.Handle("GET /settings/journal", moved("/activity?kind="+feedAccess, false))
	mux.Handle("GET /settings/channels", moved("/settings/notifications#channels", false))
	mux.Handle("GET /settings/where", moved("/settings/notifications#where", false))
}

// feedAccess is the tool accesses' filter: the decisions' journal, paged,
// rather than items of the feed.
const feedAccess = "access"

type happenedItem struct {
	At       time.Time
	Kind     string
	Summary  string
	External bool
	Link     string
}

type happenedPage struct {
	Items    []happenedItem
	Kind     string // "" for all
	Range    string // last, day or week
	From     time.Time
	FromLast bool // since the user left
	NoLast   bool // they were not away: the last 24 hours instead
	More     bool
	Max      int
	Journal  journalView // the tool accesses, for that kind
}

var (
	happenedKinds  = []string{feedTask, feedNotification, feedIntention, feedMessage, feedWatch, feedAccess}
	happenedRanges = []string{"last", "day", "week"}
)

// activityPage is what both views get: the counts of their tabs.
type activityPage struct {
	View    string // feed or approve
	Waiting int    // what waits in « À valider »
	Feed    happenedPage
	Approve approvals
}

func (w *web) handleActivity(rw http.ResponseWriter, r *http.Request, s *session) {
	q := r.URL.Query()
	v := happenedPage{Kind: q.Get("kind"), Range: q.Get("range"), Max: happenedPageMax}
	if !containsStr(happenedKinds, v.Kind) {
		v.Kind = ""
	}
	if !containsStr(happenedRanges, v.Range) {
		v.Range = "last"
	}
	if v.Kind == feedAccess {
		offset, _ := strconv.Atoi(q.Get("offset"))
		offset = max(offset, 0)
		rows, err := w.c.store.decisions(offset, journalPage+1)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		v.Journal = journalView{Rows: rows}
		if len(rows) > journalPage {
			v.Journal.Rows, v.Journal.Next = rows[:journalPage], offset+journalPage
		}
		if offset > 0 { // "more": the next rows only
			w.render(rw, r, s, "activity", "journal-rows", page{Data: v.Journal})
			return
		}
	} else if err := w.fillFeed(&v, w.lang(r)); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	w.render(rw, r, s, "activity", "", page{Title: "nav.activity", Nav: "activity", View: "feed",
		Data: activityPage{View: "feed", Waiting: w.approvalCount(), Feed: v}})
}

// fillFeed reads the feed of the page's period and kind.
func (w *web) fillFeed(v *happenedPage, lang string) error {
	now := w.c.notifyTime()
	switch v.Range {
	case "week":
		v.From = now.AddDate(0, 0, -7)
	case "day":
		v.From = now.Add(-24 * time.Hour)
	default:
		if a, ok := w.c.lastAbsence(now); ok {
			v.From, v.FromLast = a.Left, true
		} else {
			v.From, v.NoLast = now.Add(-24*time.Hour), true
		}
	}
	var kinds []string
	if v.Kind != "" {
		kinds = []string{v.Kind}
	}
	items, more, err := w.c.feed(v.From, kinds, happenedPageMax)
	if err != nil {
		return err
	}
	v.More = more
	for _, h := range items {
		v.Items = append(v.Items, happenedItem{At: h.At, Kind: h.Kind, Summary: h.summary(lang, true),
			External: h.Trust == feedExternal, Link: happenedLink(h.Ref)})
	}
	return nil
}

// happenedLink is where an item is seen in full in the workspace.
func happenedLink(ref string) string {
	kind, id, _ := strings.Cut(ref, ":")
	switch kind {
	case "task":
		return "/tasks/" + id
	case "notification":
		return "/settings/notifications?n=" + id + "#notif-" + id
	case "intention":
		return "/memory?view=commitments"
	case "watch":
		return "/settings/watches#watch-" + id
	}
	return ""
}

// approvals is everything the brain proposed that waits for the user,
// and the quiet hours the activity suggests (not counted: a suggestion
// asks nothing).
type approvals struct {
	Perms    []permView
	Memories []memCard
	Intents  []intentCard
	Rules    []notifyRule
	Watches  []watchCard
	Settings []settingProposal
	Presence []haMapping // Home Assistant entities the brain proposes to follow (ha_presence.go)
	Quiet    []quietSuggestion
}

// Count is all that waits; Remember, the memories and intentions.
func (a approvals) Count() int {
	return len(a.Perms) + a.Remember() + len(a.Rules) + len(a.Watches) + len(a.Settings) + len(a.Presence)
}

func (a approvals) Remember() int { return len(a.Memories) + len(a.Intents) }

func (w *web) approvals() approvals {
	var a approvals
	if _, perms, err := w.tasksAndPerms(); err == nil {
		for _, p := range perms {
			p.Where = "a-"
			a.Perms = append(a.Perms, p)
		}
	}
	if m := w.c.mem; m != nil {
		if xs, err := m.list(`status = 'proposed'`, nil, `recorded_at DESC, id DESC`, 200); err == nil {
			for _, x := range xs {
				a.Memories = append(a.Memories, w.memCard(x, true))
			}
		}
		if xs, err := m.intentions(intentProposed); err == nil {
			for _, x := range xs {
				a.Intents = append(a.Intents, w.intentCard(x))
			}
		}
	}
	a.Rules, _ = w.c.notifyRules(ruleProposed)
	if ws, err := w.c.watches(watchProposed); err == nil {
		for _, x := range ws {
			a.Watches = append(a.Watches, watchCard{watch: x})
		}
	}
	a.Settings, _ = w.c.settingProposals()
	a.Presence = w.c.haMappings(ruleProposed)
	a.Quiet = w.c.suggestQuiet()
	return a
}

// approvalCount is the number on the Activité button: the proposals, read
// with a count each. The permissions are not in it: Tâches already shows
// them, live.
func (w *web) approvalCount() int {
	n := 0
	if m := w.c.mem; m != nil {
		n += m.count(`status = 'proposed'`)
		var k int
		m.db.QueryRow(`SELECT COUNT(*) FROM intentions WHERE state = 'proposed'`).Scan(&k)
		n += k
	}
	if rules, err := w.c.notifyRules(ruleProposed); err == nil {
		n += len(rules)
	}
	if ws, err := w.c.watches(watchProposed); err == nil {
		n += len(ws)
	}
	if ps, err := w.c.settingProposals(); err == nil {
		n += len(ps)
	}
	n += len(w.c.haMappings(ruleProposed))
	return n
}

// handleSettingProposal applies or drops a setting the brain proposed.
func (w *web) handleSettingProposal(rw http.ResponseWriter, r *http.Request, s *session) {
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
	if err := w.c.decideSetting(id, action == "approve"); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "setting.did."+action), true, true))
	rw.Header().Set("HX-Reswap", "none")
	rw.WriteHeader(http.StatusOK)
}

func (w *web) handleApprovals(rw http.ResponseWriter, r *http.Request, s *session) {
	a := w.approvals()
	w.render(rw, r, s, "activity", "", page{Title: "activity.approve", Nav: "activity", View: "approve",
		Data: activityPage{View: "approve", Waiting: a.Count(), Approve: a}})
}

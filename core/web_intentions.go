package main

import (
	"net/http"
	"strconv"
	"time"
)

// The intentions' page (/memory, until they move to core.db): what is
// owed, by whom, by when, with done, snooze and cancel; approve or reject
// what was drawn from outside, here or in « À valider ».

type intentCard struct {
	*Intention
	Now     time.Time
	Overdue bool
}

func (w *web) intentCard(x *Intention) intentCard {
	now := w.c.mem.now()
	return intentCard{Intention: x, Now: now, Overdue: x.Overdue(now)}
}

func (w *web) intentionRoutes(mux *webMux) {
	mux.Handle("GET /memory", w.auth(w.handleIntentions))
	mux.Handle("POST /memory/intention/{id}/{action}", w.auth(w.handleIntention))
}

type intentionsPage struct {
	Off         bool
	Intents     []intentCard // the live ones
	IntentsDone []intentCard // recently closed
}

// handleIntentions shows the live intentions, the due first, then the
// recently closed.
func (w *web) handleIntentions(rw http.ResponseWriter, r *http.Request, s *session) {
	p := intentionsPage{Off: w.c.mem == nil}
	if !p.Off {
		xs, err := w.c.mem.intentions(intentDue, intentOpen, intentSnoozed, intentProposed)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, x := range xs {
			p.Intents = append(p.Intents, w.intentCard(x))
		}
		closed, err := w.c.mem.intentions(intentDone, intentCancelled)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		for i := len(closed) - 1; i >= 0 && len(p.IntentsDone) < 20; i-- {
			p.IntentsDone = append(p.IntentsDone, w.intentCard(closed[i]))
		}
	}
	w.render(rw, r, s, "memory", "", page{Title: "intent.title", Nav: "settings", Tab: "memory", Data: p})
}

func (w *web) handleIntention(rw http.ResponseWriter, r *http.Request, s *session) {
	if w.c.mem == nil {
		http.Error(rw, errIntentionsOff.Error(), http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	x, err := w.c.mem.intention(id)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}
	now := w.c.mem.now()
	action := r.PathValue("action")
	ch := intentionChange{}
	switch action {
	case "approve":
		if x.State != intentProposed {
			w.toast(rw, tr(w.lang(r), "intent.notproposed"), false)
			return
		}
		ch.State = intentOpen
	case "reject", "cancel":
		ch.State = intentCancelled
	case "done":
		ch.State = intentDone
	case "reopen":
		ch.State = intentOpen
	case "snooze":
		ch.State = intentSnoozed
		if r.PostFormValue("for") == "hour" {
			ch.Until = now.Add(time.Hour)
		} else {
			tomorrow := now.AddDate(0, 0, 1)
			ch.Until = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, time.Local)
		}
	default:
		http.Error(rw, "approve, reject, done, cancel, snooze or reopen", http.StatusBadRequest)
		return
	}
	if x.State == intentProposed && action != "approve" && action != "reject" {
		w.toast(rw, tr(w.lang(r), "intent.approvefirst"), false)
		return
	}
	var got *Intention
	if x.State == intentProposed { // its card's answer (convo_cards.go)
		if err = w.c.answerProposal(r.Context(), cardIntention, id, action == "approve"); err == nil {
			got, err = w.c.mem.intention(id)
		}
	} else {
		got, err = w.c.mem.changeIntention(id, ch, "owner", action)
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "intent.did."+action), true, true))
	w.render(rw, r, s, "memory", "intent-card-frag", page{Data: w.intentCard(got)})
}

func init() {
	for lang, words := range intentionStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var intentionStrings = map[string]map[string]string{
	"fr": {
		"intent.kind.promise":     "Promesse",
		"intent.kind.commitment":  "Engagement de {{name}}",
		"intent.kind.waiting_for": "En attente",
		"intent.kind.reminder":    "Rappel",
		"intent.st.proposed":      "À approuver",
		"intent.st.open":          "Prévu",
		"intent.st.due":           "À faire",
		"intent.st.snoozed":       "Reporté",
		"intent.st.done":          "Fait",
		"intent.st.cancelled":     "Annulé",
		"intent.owes":             "%s doit",
		"intent.owesto":           "%s doit à %s",
		"intent.owner":            "toi",
		"intent.assistant":        "{{name}}",
		"intent.due":              "pour le %s",
		"intent.remind":           "rappel le %s",
		"intent.snoozeduntil":     "reporté au %s",
		"intent.cue":              "quand tu parles de %s",
		"intent.external":         "tiré d'un contenu extérieur",
		"intent.done":             "Fait",
		"intent.snoozehour":       "Dans 1 h",
		"intent.snoozeday":        "Demain",
		"intent.cancel":           "Annuler",
		"intent.reopen":           "Rouvrir",
		"intent.approve":          "Approuver",
		"intent.reject":           "Refuser",
		"intent.approvefirst":     "Approuve-la d'abord, ou refuse-la.",
		"intent.did.approve":      "Intention approuvée.",
		"intent.did.reject":       "Intention refusée.",
		"intent.did.done":         "C'est fait.",
		"intent.did.cancel":       "Intention annulée.",
		"intent.did.snooze":       "Reporté.",
		"intent.did.reopen":       "Intention rouverte.",
		"intent.proposedlead":     "Des intentions attendent ton accord : elles viennent d'un contenu extérieur (un mail, une page) ou ont été déduites.",
		"intent.title":            "Intentions",
		"intent.lead":             "Ce qui est dû, par qui, pour quand : tes promesses, celles de {{name}}, ce que tu attends des autres, tes rappels. {{name}} te le rappelle au bon moment.",
		"intent.off":              "Les intentions de {{name}} n'ont pas pu s'ouvrir : voir le journal d'eriphe-core.",
		"intent.none":             "Rien en cours.",
		"intent.donerecent":       "Terminés récemment",
		"intent.overdue":          "En retard",
		"intent.notproposed":      "Cette intention n'attend plus d'approbation.",
		"intent.inboxlead":        "Ce que {{name}} a lu dans un mail, une page ou une tâche ne devient pas une intention sans ton accord.",
		"trust.owner":             "De toi",
		"trust.first_party":       "D'Eriphe",
		"trust.external":          "De l'extérieur",
		"src.owner":               "toi",
		"src.brain":               "{{name}}",
		"src.extraction":          "extraction",
		"src.task":                "tâche",
		"src.import":              "import",
	},
	"en": {
		"intent.kind.promise":     "Promise",
		"intent.kind.commitment":  "{{name}}'s commitment",
		"intent.kind.waiting_for": "Waiting for",
		"intent.kind.reminder":    "Reminder",
		"intent.st.proposed":      "To approve",
		"intent.st.open":          "Planned",
		"intent.st.due":           "To do",
		"intent.st.snoozed":       "Snoozed",
		"intent.st.done":          "Done",
		"intent.st.cancelled":     "Cancelled",
		"intent.owes":             "%s owes",
		"intent.owesto":           "%s owes %s",
		"intent.owner":            "you",
		"intent.assistant":        "{{name}}",
		"intent.due":              "due %s",
		"intent.remind":           "reminder %s",
		"intent.snoozeduntil":     "snoozed until %s",
		"intent.cue":              "when you talk about %s",
		"intent.external":         "drawn from external content",
		"intent.done":             "Done",
		"intent.snoozehour":       "In 1 h",
		"intent.snoozeday":        "Tomorrow",
		"intent.cancel":           "Cancel",
		"intent.reopen":           "Reopen",
		"intent.approve":          "Approve",
		"intent.reject":           "Reject",
		"intent.approvefirst":     "Approve it first, or reject it.",
		"intent.did.approve":      "Intention approved.",
		"intent.did.reject":       "Intention rejected.",
		"intent.did.done":         "Done.",
		"intent.did.cancel":       "Intention cancelled.",
		"intent.did.snooze":       "Snoozed.",
		"intent.did.reopen":       "Intention reopened.",
		"intent.proposedlead":     "Intentions wait for your approval: they come from external content (a mail, a page) or were inferred.",
		"intent.title":            "Intentions",
		"intent.lead":             "What is owed, by whom, by when: your promises, {{name}}'s, what you wait for from others, your reminders. {{name}} reminds you at the right time.",
		"intent.off":              "{{name}}'s intentions could not be opened: see eriphe-core's log.",
		"intent.none":             "Nothing under way.",
		"intent.donerecent":       "Recently closed",
		"intent.overdue":          "Overdue",
		"intent.notproposed":      "This intention no longer waits for approval.",
		"intent.inboxlead":        "What {{name}} read in a mail, a page or a task does not become an intention without your approval.",
		"trust.owner":             "From you",
		"trust.first_party":       "From Eriphe",
		"trust.external":          "External",
		"src.owner":               "you",
		"src.brain":               "{{name}}",
		"src.extraction":          "extraction",
		"src.task":                "task",
		"src.import":              "import",
	},
}

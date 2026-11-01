package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Settings → App & notifications, the brain's part (notify.go): what it told
// the user and why, with 👍 / 👎 / « pas maintenant »; what waits for the
// brief; the rules, the proposed ones to approve; the zone and the cap.

func (w *web) notifyRoutes(mux *http.ServeMux) {
	mux.Handle("POST /notify/{id}/feedback", w.auth(w.handleNotifyFeedback))
	mux.Handle("POST /notify/rules", w.auth(w.handleRuleSave))
	mux.Handle("POST /notify/rules/{id}/{action}", w.auth(w.handleRuleAction))
	mux.Handle("POST /notify/settings", w.auth(w.handleNotifySettings))
	// A push's buttons: the service worker has no session, the push carried
	// the notification's own token.
	mux.Handle("POST /notify/react", http.MaxBytesHandler(http.HandlerFunc(w.handleNotifyReact), 1<<10))
}

type notifCard struct {
	*notification
	Open bool // the one a push opened
}

type notifyPage struct {
	Recent, Held    []notifCard
	Rules, Proposed []notifyRule
	Weekdays        notifyRule // the quiet windows in force
	Weekend         notifyRule
	Zone            string
	Cap, SentToday  int
	Suggested       []quietSuggestion
	DefaultZone     string
	Stub            notifyRule // the form of a new rule
}

func (w *web) notifyPage(open int64) notifyPage {
	c := w.c
	v := notifyPage{Zone: c.store.get("notify_zone"), DefaultZone: time.Local.String(), Cap: c.notifyCap(),
		Stub: notifyRule{Kind: ruleTopic, Outcome: levelBrief, Days: daysAll, From: seedQuietFrom, To: seedQuietTo}}
	card := func(n *notification) notifCard { return notifCard{notification: n, Open: n.ID == open} }
	if recent, err := c.recentNotifications(30); err == nil {
		for _, n := range recent {
			v.Recent = append(v.Recent, card(n))
		}
	}
	if held, err := c.heldNotifications(); err == nil {
		for _, n := range held {
			v.Held = append(v.Held, card(n))
		}
	}
	if rules, err := c.notifyRules(); err == nil {
		for _, r := range rules {
			if r.State == ruleProposed {
				v.Proposed = append(v.Proposed, r)
			} else {
				v.Rules = append(v.Rules, r)
			}
		}
	}
	if cx, err := c.notifyContext(); err == nil {
		v.Weekdays, v.Weekend = quietFor(cx.Rules, sampleDay(daysWeekdays)), quietFor(cx.Rules, sampleDay(daysWeekend))
		v.SentToday = cx.SentNow
	}
	v.Suggested = c.suggestQuiet()
	return v
}

func (w *web) handleNotifyFeedback(rw http.ResponseWriter, r *http.Request, s *session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	n, err := w.c.notificationFeedback(id, r.PostFormValue("r"))
	if err != nil {
		w.toast(rw, tr(w.lang(r), "notif.fb.failed"), false)
		return
	}
	rw.Header().Set("HX-Trigger", hxTrigger(tr(w.lang(r), "notif.fb.thanks"), true, false))
	w.render(rw, r, s, "settings", "notify-card-frag", page{Data: notifCard{notification: n}})
}

// handleNotifyReact is a push button's answer, with the notification's token.
func (w *web) handleNotifyReact(rw http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || r.Method != http.MethodPost || !w.sameOrigin(r) {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}
	if err := w.c.reactWithToken(id, r.URL.Query().Get("k"), r.PostFormValue("r")); err != nil {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// handleRuleSave adds a rule, or replaces one (id): the user's own, active at once.
func (w *web) handleRuleSave(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	rule := notifyRule{Kind: r.PostFormValue("kind"), Match: r.PostFormValue("match"), Outcome: r.PostFormValue("outcome"),
		Night: r.PostFormValue("night") == "1", Channels: r.PostFormValue("channels"), Days: r.PostFormValue("days"), Why: r.PostFormValue("why"), State: ruleActive, By: "owner"}
	if rule.Kind == ruleQuiet {
		var err1, err2 error
		rule.From, err1 = parseClock(r.PostFormValue("from"))
		rule.To, err2 = parseClock(r.PostFormValue("to"))
		if err1 != nil || err2 != nil {
			w.toast(rw, tr(lang, "rule.err.clock"), false)
			return
		}
	}
	if scan := scanMemoryText(rule.Match, rule.Why); scan.Reject != "" {
		w.toast(rw, scan.Reject, false)
		return
	}
	if err := rule.check(); err != nil {
		key := "rule.err.topic"
		if rule.Kind == ruleQuiet {
			key = "rule.err.quiet"
		}
		w.toast(rw, tr(lang, key), false)
		return
	}
	if id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64); err == nil && id > 0 {
		if err := w.c.deleteNotifyRule(id); err != nil {
			w.toast(rw, err.Error(), false)
			return
		}
	}
	if _, err := w.c.addNotifyRule(rule); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderRules(rw, r, s, tr(lang, "rule.saved"))
}

func (w *web) handleRuleAction(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "bad id", http.StatusBadRequest)
		return
	}
	switch action := r.PathValue("action"); action {
	case "approve":
		_, err = w.c.approveNotifyRule(id)
	case "reject", "delete":
		err = w.c.deleteNotifyRule(id)
	default:
		http.Error(rw, "approve, reject or delete", http.StatusBadRequest)
		return
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	w.renderRules(rw, r, s, tr(lang, "rule.did."+r.PathValue("action")))
}

func (w *web) renderRules(rw http.ResponseWriter, r *http.Request, s *session, msg string) {
	rw.Header().Set("HX-Trigger", hxTrigger(msg, true, true))
	w.render(rw, r, s, "settings", "notify-rules-frag", page{Data: w.notifyPage(0)})
}

// handleNotifySettings keeps the user's zone and the daily cap.
func (w *web) handleNotifySettings(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	zone := strings.TrimSpace(r.PostFormValue("zone"))
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil || len(zone) > 64 {
			w.toast(rw, tr(lang, "notify.err.zone"), false)
			return
		}
	}
	n, err := strconv.Atoi(r.PostFormValue("cap"))
	if err != nil || n < 0 || n > maxNotifyCap {
		w.toast(rw, tr(lang, "notify.err.cap"), false)
		return
	}
	w.c.store.set("notify_zone", zone)
	w.c.store.set("notify_cap", strconv.Itoa(n))
	w.renderRules(rw, r, s, tr(lang, "notify.saved"))
}

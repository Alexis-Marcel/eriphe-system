package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Settings → Surveillance (monitors.go, monitors_user.go): what Eriphe
// watches, each monitor's state and events, the skills that react to
// them; adding or changing one (the user's own: it looks at once), trying
// its tool first, pausing, deleting, and the brain's proposals answered
// here as on their card.

func (w *web) monitorRoutes(mux *http.ServeMux) {
	mux.Handle("GET /settings/monitors", w.auth(w.handleMonitors))
	mux.Handle("POST /settings/monitors", w.auth(w.handleSaveMonitor))
	mux.Handle("POST /settings/monitors/try", w.auth(w.handleTryMonitor))
	mux.Handle("POST /settings/monitors/address", w.auth(w.handleNewAddress))
	mux.Handle("POST /settings/monitors/follow", w.auth(w.handleFollow))
	mux.Handle("GET /settings/monitors/{name}", w.auth(w.handleMonitor))
	mux.Handle("POST /settings/monitors/{name}/{action}", w.auth(w.handleMonitorAction))
}

const monitorEventsShown = 20

// monitorView is a monitor as the page shows it.
type monitorView struct {
	Name     string
	State    string // on, paused, proposed
	Does     string
	LastLook string
	Waiting  string
	Error    string
	Failing  bool
	Events   int // over 30 days
	Skills   []string
	Change   string // a change the brain proposed, in words
	Tainted  string
	Listen   bool // an incoming address
	Follow   bool // a server's events
}

type monitorEventView struct {
	When, Kind, Words string
	External          bool
}

// monitorForm is the add or edit form, as typed.
type monitorForm struct {
	Name, Tool, Args, Every, Cond, Path, ID, Above, Below string
	Edit                                                  bool
}

type monitorsPage struct {
	Monitors []monitorView
	Tools    []string
	Form     monitorForm
	Fresh    *freshHook // an address just made: its URL and secret, shown once
	Streams  []string   // the servers that publish events and no monitor follows
}

type freshHook struct{ Name, URL, Secret string }

type monitorPage struct {
	Monitor monitorView
	Events  []monitorEventView
	Tools   []string
	Form    monitorForm
}

func (w *web) monitorView(m *monitor, lang string, now time.Time) monitorView {
	v := monitorView{Name: m.Name, State: m.State, Does: m.Spec.Text(lang), Waiting: m.Look.Waiting, Error: m.Look.Error,
		Failing: m.Look.Failed, Events: w.c.monitorEventCount(m.Name, now.Add(-30*24*time.Hour)), Skills: w.c.skillsFollowing(m.Name),
		Tainted: m.Tainted, Listen: m.Spec.Way == wayListen, Follow: m.Spec.Way == wayFollow}
	if m.Look.Checked > 0 {
		v.LastLook = time.Unix(m.Look.Checked, 0).In(now.Location()).Format("2006-01-02 15:04")
	}
	if m.Pending != nil {
		v.Change = m.Pending.Text(lang)
	}
	return v
}

func formOf(name string, s monitorSpec) monitorForm {
	f := monitorForm{Name: name, Tool: s.Server + "." + s.Tool, Cond: s.Cond, Path: s.Path, ID: s.ID, Edit: true}
	if s.Every%(24*60) == 0 {
		f.Every = strconv.Itoa(s.Every/(24*60)) + "d"
	} else if s.Every%60 == 0 {
		f.Every = strconv.Itoa(s.Every/60) + "h"
	} else {
		f.Every = strconv.Itoa(s.Every) + "m"
	}
	if s.Args != nil {
		raw, _ := json.Marshal(s.Args)
		f.Args = string(raw)
	}
	if s.Above != nil {
		f.Above = strconv.FormatFloat(*s.Above, 'f', -1, 64)
	}
	if s.Below != nil {
		f.Below = strconv.FormatFloat(*s.Below, 'f', -1, 64)
	}
	return f
}

func (w *web) handleMonitors(rw http.ResponseWriter, r *http.Request, s *session) {
	w.renderMonitors(rw, r, s, nil)
}

func (w *web) renderMonitors(rw http.ResponseWriter, r *http.Request, s *session, fresh *freshHook) {
	lang, now := w.lang(r), w.c.notifyTime()
	ms, err := w.c.monitors()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	d := monitorsPage{Tools: w.c.monitorTools(), Form: monitorForm{Every: "5m", Cond: condChanged}, Fresh: fresh, Streams: w.c.unfollowedStreams()}
	for _, m := range ms {
		d.Monitors = append(d.Monitors, w.monitorView(m, lang, now))
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.monitors", Nav: "settings", Tab: "monitors", Data: d})
}

func (w *web) handleMonitor(rw http.ResponseWriter, r *http.Request, s *session) {
	lang, now := w.lang(r), w.c.notifyTime()
	m, err := w.c.monitorNamed(r.PathValue("name"))
	if err != nil {
		http.Redirect(rw, r, "/settings/monitors", http.StatusSeeOther)
		return
	}
	d := monitorPage{Monitor: w.monitorView(m, lang, now), Tools: w.c.monitorTools(), Form: formOf(m.Name, m.Spec)}
	for _, e := range w.c.monitorEvents(m.Name, monitorEventsShown) {
		d.Events = append(d.Events, monitorEventView{When: e.At.In(now.Location()).Format("2006-01-02 15:04"), Kind: e.Kind,
			Words: monitorEventWords(e), External: e.External})
	}
	w.render(rw, r, s, "settings", "", page{Title: "settings.monitors", Nav: "settings", Tab: "monitors", View: "monitor", Data: d})
}

// monitorEventWords is a few words of what an event says.
func monitorEventWords(e journalEvent) string {
	var parts []string
	for _, k := range []string{"status", "value", "was", "holds", "id", "error", "detail"} {
		if v, ok := e.Payload[k]; ok {
			parts = append(parts, k+" "+truncate(valueText(v), 120))
		}
	}
	return strings.Join(parts, " · ")
}

// specOfForm reads the form into a spec.
func specOfForm(r *http.Request) (string, monitorSpec, error) {
	server, tool, ok := strings.Cut(strings.TrimSpace(r.PostFormValue("tool")), ".")
	s := monitorSpec{Way: wayPoll, Server: server, Tool: tool, Cond: r.PostFormValue("cond"),
		Path: r.PostFormValue("path"), ID: r.PostFormValue("id")}
	if !ok {
		return "", s, fmt.Errorf("pick a tool")
	}
	if a := strings.TrimSpace(r.PostFormValue("args")); a != "" {
		if err := json.Unmarshal([]byte(a), &s.Args); err != nil {
			return "", s, fmt.Errorf("the arguments are a JSON object: {\"service\": \"web\"}")
		}
	}
	if e := strings.TrimSpace(r.PostFormValue("every")); e != "" {
		d, err := parseMonitorEvery(e)
		if err != nil {
			return "", s, err
		}
		s.Every = int(d / time.Minute)
	}
	for _, f := range []struct {
		field string
		to    **float64
	}{{"above", &s.Above}, {"below", &s.Below}} {
		if v := strings.TrimSpace(r.PostFormValue(f.field)); v != "" {
			n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
			if err != nil {
				return "", s, fmt.Errorf("%s is a number", f.field)
			}
			*f.to = &n
		}
	}
	return r.PostFormValue("name"), s, nil
}

func (w *web) handleSaveMonitor(rw http.ResponseWriter, r *http.Request, s *session) {
	name, spec, err := specOfForm(r)
	if err == nil {
		err = w.c.saveMonitor(name, spec)
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: monitor %s saved", name)
	rw.Header().Set("HX-Redirect", "/settings/monitors/"+url.PathEscape(strings.ToLower(strings.TrimSpace(name))))
	rw.WriteHeader(http.StatusOK)
}

// handleNewAddress makes a monitor that listens, or a new token for one,
// and shows its address and secret this once.
func (w *web) handleNewAddress(rw http.ResponseWriter, r *http.Request, s *session) {
	name := strings.ToLower(strings.TrimSpace(r.PostFormValue("name")))
	token, secret, err := w.c.newListenMonitor(name, r.PostFormValue("signed") == "on", r.PostFormValue("header"))
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: address %s made", name)
	w.renderMonitors(rw, r, s, &freshHook{Name: name, URL: w.publicURL(r) + hookPath + token, Secret: secret})
}

// handleFollow makes a monitor that follows a server's events.
func (w *web) handleFollow(rw http.ResponseWriter, r *http.Request, s *session) {
	name := strings.ToLower(strings.TrimSpace(r.PostFormValue("name")))
	if err := w.c.saveMonitor(name, monitorSpec{Way: wayFollow, Server: r.PostFormValue("server")}); err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: monitor %s follows %s", name, r.PostFormValue("server"))
	rw.Header().Set("HX-Redirect", "/settings/monitors/"+url.PathEscape(name))
	rw.WriteHeader(http.StatusOK)
}

// handleTryMonitor calls the tool once and shows what the path extracts.
func (w *web) handleTryMonitor(rw http.ResponseWriter, r *http.Request, s *session) {
	_, spec, err := specOfForm(r)
	got := ""
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
		got, err = w.c.tryMonitor(ctx, spec)
		cancel()
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		fmt.Fprintf(rw, `<p class="notice notice-err" role="status">%s</p>`, html.EscapeString(err.Error()))
		return
	}
	fmt.Fprintf(rw, `<p class="small">%s</p><pre class="mono small">%s</pre>`, html.EscapeString(tr(w.lang(r), "monitors.tried")), html.EscapeString(got))
}

func (w *web) handleMonitorAction(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	m, err := w.c.monitorNamed(name)
	if err != nil {
		rw.Header().Set("HX-Redirect", "/settings/monitors")
		rw.WriteHeader(http.StatusOK)
		return
	}
	to := "/settings/monitors/" + url.PathEscape(name)
	switch r.PathValue("action") {
	case "pause", "resume":
		err = w.c.pauseMonitor(name, r.PathValue("action") == "pause")
	case "delete":
		err, to = w.c.deleteMonitor(name), "/settings/monitors"
	case "approve", "reject":
		err = w.c.decideProposal(r.Context(), cardMonitor, m.ID, r.PathValue("action") == "approve")
		if _, gone := w.c.monitorNamed(name); gone != nil {
			to = "/settings/monitors"
		}
	default:
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		w.toast(rw, err.Error(), false)
		return
	}
	log.Printf("web: monitor %s: %s", name, r.PathValue("action"))
	rw.Header().Set("HX-Redirect", to)
	rw.WriteHeader(http.StatusOK)
}

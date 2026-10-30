package main

import (
	"fmt"
	"net/http"
	"net/url"
)

// "Add a tool" in two steps, in the tools page's dialog: the search
// (web_gallery.go), then the chosen tool's form loaded in its place with
// htmx (?dialog=1), with a way back to the search. Once saved, the dialog
// follows the tool until it is ready or says why not, instead of saying at
// once that it can be used. An edit opens the same dialog on its form.
// Without JavaScript, each step is its own page, as before (web_tools.go).
//
// Google Drive needs a long guide (an app of one's own in the Google Cloud
// console, then the folders to give): its step only says what it takes and
// leads to its page. Home Assistant and GitHub have their form in the
// dialog, with a link to their own page for the rest (the voice, the app).

// inDialog tells a request for a step of the dialog rather than a page.
func inDialog(r *http.Request) bool {
	return r.URL.Query().Get("dialog") != "" && r.Header.Get("HX-Request") != ""
}

// StepURL is where a tile of the dialog leads: the preset's form, even for
// the presets whose tile leads to a page of their own without JavaScript.
func (p toolPreset) StepURL() string {
	if p.ID == "remote" {
		return p.Page()
	}
	return "/settings/tools/new?preset=" + p.ID
}

// StepHash is the address of a step reached from the form or its list of
// accounts, after the # of the tools page: add/mail/gmail, edit/mail/choose.
func (f toolForm) StepHash(provider string) string {
	if f.Edit {
		return "edit/" + f.Name + "/" + provider
	}
	return "add/" + f.Preset.ID + "/" + provider
}

// Hash is the address of the form's own step.
func (f toolForm) Hash() string {
	p := f.Mail.Provider
	if f.Preset.Form == "calendar" {
		p = f.Calendar.Provider
	}
	switch {
	case f.Edit && f.Choose:
		return "edit/" + f.Name + "/choose"
	case f.Edit:
		return "edit/" + f.Name
	case p != "" && !f.Choose:
		return f.StepHash(p)
	}
	return "add/" + f.Preset.ID
}

// BackURL is where the form's back link leads; BackHash is that step in
// the dialog ("add" is the search). An edit's form has none in the dialog.
func (f toolForm) BackURL() string {
	switch {
	case f.Choose && f.Edit:
		return "/settings/tools/" + f.Name + "/edit"
	case f.Edit:
		return "/settings/tools"
	case (f.Mail.Provider != "" || f.Calendar.Provider != "") && !f.Choose:
		return "/settings/tools/new?preset=" + f.Preset.ID
	}
	return "/settings/tools/add"
}

func (f toolForm) BackHash() string {
	switch {
	case f.Choose && f.Edit:
		return "edit/" + f.Name
	case f.Edit:
		return ""
	case (f.Mail.Provider != "" || f.Calendar.Provider != "") && !f.Choose:
		return "add/" + f.Preset.ID
	}
	return "add"
}

// driveStep is Google Drive's step: what it takes, and its page.
type driveStep struct{ Added bool }

// leavingStep is the step shown while the browser goes to an OAuth
// service to authorize Eriphe; it comes back to the tools page, whose
// dialog then follows the tool (pending.go, web_catalog.go).
type leavingStep struct {
	Preset  toolPreset
	Service string
	Name    string
	AuthURL string
}

func (w *web) leavingStep(rw http.ResponseWriter, r *http.Request, s *session, f toolForm, authURL string) {
	v := leavingStep{Preset: f.Preset, Service: serviceTitle(w.lang(r), f.Preset.ID, f.URL), Name: f.Name, AuthURL: authURL}
	if v.Service == "" {
		v.Service = f.Name
	}
	rw.Header().Set("HX-Retarget", "#tool-step")
	rw.Header().Set("HX-Reswap", "innerHTML")
	w.render(rw, r, s, "settings", "tool-leaving", page{Data: v})
}

// addedStep is the step after a save: the tool, and a line that follows
// it until it is up.
type addedStep struct {
	Name   string
	Preset toolPreset
	Edit   bool
	Status toolStatus
}

func (w *web) addedStep(rw http.ResponseWriter, r *http.Request, s *session, name string, edit bool) {
	rw.Header().Set("HX-Retarget", "#tool-step")
	rw.Header().Set("HX-Reswap", "innerHTML")
	w.render(rw, r, s, "settings", "tool-added", page{Data: w.added(w.lang(r), name, edit)})
}

func (w *web) added(lang, name string, edit bool) addedStep {
	v := addedStep{Name: name, Edit: edit, Status: w.toolStatus(lang, name)}
	v.Preset, _ = presetOf(lang, w.c.conf().Servers[name])
	return v
}

// handleToolAdded is the step after a save on its own (GET
// /settings/tools/{name}/added): where the browser comes back to after an
// OAuth service (#added/<name>). Without the dialog, the tools page says it.
func (w *web) handleToolAdded(rw http.ResponseWriter, r *http.Request, s *session) {
	name := r.PathValue("name")
	if !inDialog(r) || w.c.source(kindServer, name) == "" {
		http.Redirect(rw, r, "/settings/tools?saved="+url.QueryEscape(name), http.StatusSeeOther)
		return
	}
	w.render(rw, r, s, "settings", "tool-added", page{Data: w.added(w.lang(r), name, false)})
}

// toolStatus is the line that follows a tool just saved: waiting for a
// device, starting, ready, or why not and what to do.
type toolStatus struct {
	Name   string
	State  string // wait, start, ok (ready) or err
	Text   string
	Hint   string // what to do, when it is not ready
	Detail string // the raw error, folded
	Edit   bool   // offer to change the tool
	Poll   string // the delay before asking again; "" once it settled
}

func (w *web) toolStatus(lang, name string) toolStatus {
	for _, sv := range w.servers() {
		if sv.Name == name {
			return statusOf(lang, sv)
		}
	}
	return toolStatus{Name: name, State: "err", Text: fmt.Sprintf(tr(lang, "added.gone"), name)}
}

// statusOf words where a tool is: the line of the step after a save.
func statusOf(lang string, sv serverView) toolStatus {
	v := toolStatus{Name: sv.Name, State: "err"}
	v.Edit = sv.Source == sourceWorkspace
	pending, placed := startState(sv)
	switch {
	case pending && sv.Service == stateFailed:
		v.Text, v.Hint, v.Detail, v.Poll = serviceProblem(lang, sv), tr(lang, "added.failedhint"), sv.Error, "5s"
	case pending && !placed:
		v.State, v.Text, v.Hint, v.Edit, v.Poll = "wait", tr(lang, "server.pending"), tr(lang, "tools.pendinghint"), false, "3s"
	case pending:
		v.State, v.Text, v.Edit, v.Poll = "start", fmt.Sprintf(tr(lang, "added.startingon"), sv.Node), false, "1500ms"
	case sv.Status == statusReady:
		v.State, v.Text, v.Edit = "ok", tr(lang, "added.ready"), false
		v.Hint = tr(lang, "tools.count1")
		if sv.ToolCount != 1 {
			v.Hint = fmt.Sprintf(tr(lang, "tools.count"), sv.ToolCount)
		}
	case sv.Status == statusConnecting:
		v.State, v.Text, v.Edit, v.Poll = "start", tr(lang, "added.connecting"), false, "1500ms"
	case sv.Status == statusNeedsAuth:
		v.Text, v.Hint = tr(lang, "added.needsauth"), tr(lang, "added.needsauthhint")
	default:
		// it may still come up: a service that answers late, a restart
		v.Text, v.Hint, v.Detail, v.Poll = tr(lang, "added.error"), tr(lang, "added.errorhint"), sv.Error, "5s"
	}
	return v
}

// handleToolStatus is the line alone, asked again until the tool settles.
func (w *web) handleToolStatus(rw http.ResponseWriter, r *http.Request, s *session) {
	w.render(rw, r, s, "settings", "tool-status", page{Data: w.toolStatus(w.lang(r), r.PathValue("name"))})
}

package main

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// step asks for a step of the tools page's dialog, as app.js does.
func (tw *testWeb) step(path string) (int, string, http.Header) {
	tw.t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return tw.do("GET", path+sep+"dialog=1", nil, map[string]string{"HX-Request": "true"})
}

// A step is a fragment: no page around it, a title, and its own address.
func wantStep(t *testing.T, what, body, hash string) {
	t.Helper()
	if strings.Contains(body, "<html") || strings.Contains(body, `class="page-head`) {
		t.Errorf("%s: a whole page, not a step", what)
	}
	if !strings.Contains(body, `<div class="step" data-hash="`+hash+`"`) || !strings.Contains(body, `id="step-title"`) {
		t.Errorf("%s: not the step %s", what, hash)
	}
}

// The tools page's dialog has room for the second step; each tile loads
// its step there, and "Modifier" opens the same dialog on the form.
func TestAddDialogTiles(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"todoist"}, "name": {"taches"}, "use": {"tasks"}, "kind": {"remote"},
		"url": {"https://ai.todoist.net/mcp"}, "auth": {"bearer"}, "token": {"todo-token"}}
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %d", code)
	}
	waitStatus(t, c.pool.get("taches"), statusReady)
	_, body, _ := tw.do("GET", "/settings/tools", nil, nil)
	list, dlg := toolsList(t, body)
	if !strings.Contains(dlg, `<div class="add-pick" data-step-pick>`) || !strings.Contains(dlg, `<div class="add-step" id="tool-step" hidden></div>`) {
		t.Error("no room for the second step")
	}
	for _, p := range toolPresets {
		if !strings.Contains(dlg, `href="`+p.Page()+`" data-step="`+p.StepURL()+`" data-hash="add/`+p.ID+`"`) {
			t.Errorf("tile %s: no step", p.ID)
		}
	}
	for id, want := range map[string]string{"home-assistant": "/settings/tools/new?preset=home-assistant",
		"gdrive": "/settings/tools/new?preset=gdrive", "remote": "/settings/tools/add/other", "mail": "/settings/tools/new?preset=mail"} {
		if p, _ := presetByID(id); p.StepURL() != want {
			t.Errorf("%s: step %s", id, p.StepURL())
		}
	}
	if !strings.Contains(list, `<div id="tools-body">`) {
		t.Error("the list has no part to refresh")
	}
	if !strings.Contains(list, `href="/settings/tools/taches/edit" data-step-open="add-dlg" data-hash="edit/taches"`) {
		t.Error("Modifier does not open the dialog")
	}
}

// Each kind of preset has its step: the form (or the kinds of account)
// with a way back, Google Drive what it takes and its page, "Autre service"
// its address. Without the dialog, each is its own page as before.
func TestAddDialogSteps(t *testing.T) {
	tw, _, _ := catalogWeb(t, nil)
	for _, tc := range []struct {
		path, hash, back, backHash string
		wants                      []string
	}{
		{"/settings/tools/new?preset=mail", "add/mail", "/settings/tools/add", "add",
			[]string{`class="panel picker"`, `href="/settings/tools/new?preset=mail&amp;provider=gmail" data-step data-hash="add/mail/gmail"`}},
		{"/settings/tools/new?preset=mail&provider=gmail", "add/mail/gmail", "/settings/tools/new?preset=mail", "add/mail",
			[]string{`name="mail_provider" value="gmail"`, `name="dialog" value="1"`, `data-hash="add/mail/choose"`, `href="/settings/tools" data-step-close`}},
		{"/settings/tools/new?preset=calendar&provider=icloud", "add/calendar/icloud", "/settings/tools/new?preset=calendar", "add/calendar",
			[]string{`name="cal_provider" value="icloud"`, `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=linear", "add/linear", "/settings/tools/add", "add",
			[]string{"Se connecter à Linear", `name="connect" value="1"`, "Tu seras envoyé chez Linear", `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=todoist", "add/todoist", "/settings/tools/add", "add",
			[]string{`name="token"`, `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=home-assistant", "add/home-assistant", "/settings/tools/add", "add",
			[]string{`name="url"`, `name="token"`, `<a href="/settings/home-assistant">Page Home Assistant</a>`}},
		{"/settings/tools/new?preset=github", "add/github", "/settings/tools/add", "add",
			[]string{`name="token"`, `<a href="/settings/github">`}},
		{"/settings/tools/new?preset=local", "add/local", "/settings/tools/add", "add",
			[]string{`name="image"`, `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=gdrive", "add/gdrive", "/settings/tools/add", "add",
			[]string{`<a class="btn btn-primary" href="/settings/drive">Ouvrir le guide</a>`, "app Google à toi"}},
		{"/settings/tools/add/other", "add/remote", "/settings/tools/add", "add",
			[]string{`hx-post="/settings/tools/add/other"`, `data-step data-hash="add/remote/form"`, `<div id="discover-result"`}},
	} {
		code, body, _ := tw.step(tc.path)
		if code != 200 {
			t.Errorf("%s: %d", tc.path, code)
			continue
		}
		wantStep(t, tc.path, body, tc.hash)
		if !strings.Contains(body, `<a class="step-back" href="`+html.EscapeString(tc.back)+`" data-step-back data-hash="`+tc.backHash+`">`) {
			t.Errorf("%s: no way back to %s", tc.path, tc.back)
		}
		for _, w := range tc.wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %s", tc.path, w)
			}
		}
		if !strings.Contains(body, `<form method="dialog">`) {
			t.Errorf("%s: no close button", tc.path)
		}
		// Without JavaScript (no htmx, or no dialog asked): the page.
		for _, h := range []map[string]string{nil, {"HX-Request": "true"}} {
			plain := tc.path
			if h == nil {
				plain += map[bool]string{true: "&", false: "?"}[strings.Contains(plain, "?")] + "dialog=1"
			}
			code, page, hdr := tw.do("GET", plain, nil, h)
			if strings.Contains(tc.path, "gdrive") {
				if code != http.StatusSeeOther || hdr.Get("Location") != "/settings/drive" {
					t.Errorf("drive without the dialog: %d %s", code, hdr.Get("Location"))
				}
				continue
			}
			if code != 200 || !strings.Contains(page, "<html") || !strings.Contains(page, `class="back"`) || strings.Contains(page, `name="dialog"`) {
				t.Errorf("%s without the dialog (%v): %d, not its page", tc.path, h, code)
			}
		}
	}
	// "Autre service": what is found leads to the form, in the dialog too.
	d := discoverPage{URL: "https://mcp.example.com/mcp", Name: "example"}
	if got := d.FormURL("oauth"); !strings.HasPrefix(got, "/settings/tools/new?") {
		t.Errorf("form url %s", got)
	}
}

// "Modifier" opens the tool's form in the dialog directly: no way back to
// the search, and the save stays in the dialog.
func TestEditInDialog(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"mail"}, "name": {"mails"}, "use": {"mails"}, "access": {"read"}, "mail_provider": {"gmail"},
		"address": {"sam@gmail.com"}, "password": {"abcd efgh ijkl mnop"}}
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=mails" {
		t.Fatalf("save: %d %v", code, h)
	}
	code, body, _ := tw.step("/settings/tools/mails/edit")
	if code != 200 {
		t.Fatalf("edit: %d", code)
	}
	wantStep(t, "edit", body, "edit/mails")
	if strings.Contains(body, "data-step-back") {
		t.Error("an edit has a way back to the search")
	}
	if !strings.Contains(body, `hx-post="/settings/tools/mails"`) || !strings.Contains(body, `name="dialog" value="1"`) ||
		!strings.Contains(body, `href="/settings/tools/mails/edit?provider=choose" data-step data-hash="edit/mails/choose"`) {
		t.Errorf("edit form: %s", mainPart(t, body))
	}
	// Another kind of account, from the edit: back to the edit.
	_, body, _ = tw.step("/settings/tools/mails/edit?provider=choose")
	wantStep(t, "edit picker", body, "edit/mails/choose")
	if !strings.Contains(body, `<a class="step-back" href="/settings/tools/mails/edit" data-step-back data-hash="edit/mails">`) {
		t.Error("the kinds of account of an edit do not lead back to it")
	}
	// Saved from the dialog: the step that follows it, not a jump to the list.
	form.Set("use", "my mails")
	form.Set("dialog", "1")
	code, body, h := tw.post("/settings/tools/mails", form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "" || h.Get("HX-Retarget") != "#tool-step" || h.Get("HX-Reswap") != "innerHTML" {
		t.Fatalf("save in the dialog: %d %v", code, h)
	}
	if !strings.Contains(body, `data-hash="added/mails" data-tool-saved`) || !strings.Contains(html.UnescapeString(body), "« mails » enregistré") {
		t.Errorf("saved step: %s", body)
	}
	if c.conf().Servers["mails"].Use != "my mails" {
		t.Error("not saved")
	}
	// A refusal stays in the form, in the dialog.
	form.Set("use", "")
	_, body, h = tw.post("/settings/tools/mails", form, csrf)
	if h.Get("HX-Retarget") != "" || !strings.Contains(body, `id="tool-form"`) || !strings.Contains(body, `name="dialog" value="1"`) || !strings.Contains(body, `class="alert"`) {
		t.Errorf("refused in the dialog: %v %s", h, body)
	}
}

// Once saved, the dialog follows the tool until it is ready or says why
// not; the line alone is asked again until then.
func TestAddedFollowsTheTool(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"mail"}, "name": {"mails"}, "use": {"mails"}, "access": {"read"}, "mail_provider": {"gmail"},
		"address": {"sam@gmail.com"}, "password": {"abcd efgh ijkl mnop"}, "dialog": {"1"}}
	code, body, h := tw.post("/settings/tools", form, csrf)
	if code != 200 || h.Get("HX-Retarget") != "#tool-step" {
		t.Fatalf("save: %d %v", code, h)
	}
	// (the test's core connects any tool at once: the states before are in TestStatusOf)
	if b := html.UnescapeString(body); !strings.Contains(b, "« mails » ajouté") || !strings.Contains(body, `id="tool-status"`) ||
		!strings.Contains(body, `data-hash="added/mails" data-tool-saved`) || !strings.Contains(body, "Terminé") {
		t.Fatalf("saved step: %s", body)
	}
	// A remote tool: ready, said once, no more asking.
	waitStatus(t, c.pool.get("docs"), statusReady)
	_, s, _ := tw.do("GET", "/settings/tools/docs/status", nil, map[string]string{"HX-Request": "true"})
	if s = html.UnescapeString(s); !strings.Contains(s, "test-ok") || !strings.Contains(s, "Prêt — Eriphe peut l'utiliser.") || strings.Contains(s, "hx-get") {
		t.Errorf("ready: %s", s)
	}
	_, s, _ = tw.do("GET", "/settings/tools/nope/status", nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(html.UnescapeString(s), "« nope » n'existe plus") || strings.Contains(s, "hx-get") {
		t.Errorf("gone: %s", s)
	}
	// The step on its own, where an OAuth service brings the browser back;
	// without the dialog, the tools page.
	code, body, _ = tw.step("/settings/tools/docs/added")
	if code != 200 {
		t.Fatalf("added: %d", code)
	}
	wantStep(t, "added", body, "added/docs")
	if code, _, h := tw.do("GET", "/settings/tools/docs/added", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/settings/tools?saved=docs" {
		t.Errorf("added without the dialog: %d %s", code, h.Get("Location"))
	}
	// The list's flash no longer says it can be used at once.
	_, page, _ := tw.do("GET", "/settings/tools?saved=mails", nil, nil)
	if p := html.UnescapeString(page); !strings.Contains(p, "Outil « mails » enregistré. Son état est sur sa carte") || strings.Contains(p, "dès maintenant") {
		t.Error("the saved flash")
	}
}

// Every state of the line: what it says, whether it asks again, and
// whether it offers to change the tool.
func TestStatusOf(t *testing.T) {
	local := func(node, service, status, err string) serverView {
		return serverView{serverInfo: serverInfo{Name: "t", Kind: "local", Source: sourceWorkspace, Status: status, Error: err}, Node: node, Service: service}
	}
	remote := func(status, err string) serverView {
		return serverView{serverInfo: serverInfo{Name: "t", Kind: "remote", Source: sourceWorkspace, Status: status, Error: err, ToolCount: 3}}
	}
	for _, tc := range []struct {
		name  string
		sv    serverView
		state string
		text  string
		poll  bool
		edit  bool
	}{
		{"no device", local("", statePending, statusError, "service t is pending"), "wait", "En attente d'un appareil", true, false},
		{"starting", local("pc", "starting", statusError, "service t is pending on pc"), "start", "Démarrage sur pc…", true, false},
		{"failed", local("pc", stateFailed, statusError, "unauthorized"), "err", "n'a pas pu télécharger", true, true},
		{"running, connecting", local("pc", stateRunning, statusConnecting, ""), "start", "Connexion…", true, false},
		{"ready", remote(statusReady, ""), "ok", "Prêt", false, false},
		{"connecting", remote(statusConnecting, ""), "start", "Connexion…", true, false},
		{"needs auth", remote(statusNeedsAuth, ""), "err", "Il reste à autoriser", false, true},
		{"error", remote(statusError, "calling \"initialize\": Unauthorized"), "err", "n'arrive pas à se connecter", true, true},
	} {
		v := statusOf("fr", tc.sv)
		if v.State != tc.state || !strings.Contains(v.Text, tc.text) || (v.Poll != "") != tc.poll || v.Edit != tc.edit {
			t.Errorf("%s: %+v", tc.name, v)
		}
		if tc.state != "err" && v.Detail != "" {
			t.Errorf("%s: a raw error while it is not one: %q", tc.name, v.Detail)
		}
	}
	if v := statusOf("fr", remote(statusReady, "")); v.Hint != "3 outils" && !strings.Contains(v.Hint, "3") {
		t.Errorf("ready hint %q", v.Hint)
	}
}

// An OAuth tool from the dialog: a step on the way to the service, which
// brings the browser back to the dialog following the tool.
func TestOAuthFromDialog(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	form := oauthToolForm(f)
	form.Set("dialog", "1")
	code, body, h := tw.post("/settings/tools", form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "" || h.Get("HX-Retarget") != "#tool-step" {
		t.Fatalf("connect in the dialog: %d %v %s", code, h, body)
	}
	i := strings.Index(body, `data-go>`)
	j := strings.LastIndex(body[:max(i, 0)], `href="`)
	if i < 0 || j < 0 {
		t.Fatalf("no link to the service: %s", body)
	}
	authURL := html.UnescapeString(body[j+len(`href="`) : i-2])
	if !strings.Contains(authURL, "/authorize") || !strings.Contains(html.UnescapeString(body), "Direction linear") {
		t.Fatalf("leaving step: %s", body)
	}
	code2, state := f.authorize(authURL)
	_, back := callback(t, tw, "code="+code2+"&state="+url.QueryEscape(state))
	if !strings.Contains(back, `url=/settings/tools?connected=linear#added/linear`) {
		t.Fatalf("back: %s", back)
	}
	waitStatus(t, c.pool.get("linear"), statusReady)
}

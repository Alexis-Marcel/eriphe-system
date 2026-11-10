package main

import (
	"html"
	"net/http"
	"net/http/httptest"
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
	if !strings.Contains(list, `href="/settings/tools/taches/access"`) {
		t.Error("Modifier does not open the tool's page")
	}
}

// Each kind of preset has its step: the form (or the kinds of account)
// with a way back, "Autre service" its address. Without the dialog, each is its own page as before.
func TestAddDialogSteps(t *testing.T) {
	tw, _, _ := catalogWeb(t, nil)
	for _, tc := range []struct {
		path, hash, back, backHash string
		wants                      []string
	}{
		{"/settings/tools/new?preset=mail", "add/mail", "/settings/tools/add", "add",
			[]string{`class="panel picker"`, `href="/settings/tools/new?preset=mail&amp;provider=gmail" data-step data-hash="add/mail/gmail"`}},
		{"/settings/tools/new?preset=mail&provider=gmail", "add/mail/gmail", "/settings/tools/new?preset=mail", "add/mail",
			[]string{`name="mail_provider" value="gmail"`, `name="dialog" value="1"`, `href="/settings/tools" data-step-close`}},
		{"/settings/tools/new?preset=calendar&provider=icloud", "add/calendar/icloud", "/settings/tools/new?preset=calendar", "add/calendar",
			[]string{`name="cal_provider" value="icloud"`, `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=linear", "add/linear", "/settings/tools/add", "add",
			[]string{"Se connecter à Linear", `name="connect" value="1"`, "Tu seras envoyé chez Linear", `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=todoist", "add/todoist", "/settings/tools/add", "add",
			[]string{`name="token"`, `name="dialog" value="1"`}},
		{"/settings/tools/new?preset=home-assistant", "add/home-assistant", "/settings/tools/add", "add",
			[]string{`name="url"`, `name="token"`}},
		{"/settings/tools/new?preset=github", "add/github", "/settings/tools/add", "add",
			[]string{`<a class="btn btn-primary" href="/settings/github">`, `data-step data-hash="add/github/token"`}},
		{"/settings/tools/new?preset=github&provider=token", "add/github/token", "/settings/tools/new?preset=github", "add/github",
			[]string{`name="token"`, `<a href="/settings/github">`}},
		{"/settings/tools/new?preset=local", "add/local", "/settings/tools/add", "add",
			[]string{`name="image"`, `name="dialog" value="1"`}},
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
		// The kind of account picked goes back to the list by the step's
		// back link only: no "Changer" beside it.
		if strings.Contains(body, "/choose") {
			t.Errorf("%s: a second way back to the kinds of account", tc.path)
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
			code, page, _ := tw.do("GET", plain, nil, h)
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
// the search, its access in it, and the save closes the dialog on the list.
// A mailbox is an account of the mail: its form is the account's.
func TestEditInDialog(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	key := rowKey("mail", "sam@gmail.com")
	form := url.Values{"preset": {"mail"}, "access": {"read"}, "mail_provider": {"gmail"},
		"address": {"sam@gmail.com"}, "password": {"abcd efgh ijkl mnop"}}
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=mail&tool="+key+"&new=1" {
		t.Fatalf("save: %d %v", code, h)
	}
	edit := "/settings/tools/mail/edit?account=sam@gmail.com"
	code, body, _ := tw.step(edit)
	if code != 200 {
		t.Fatalf("edit: %d", code)
	}
	wantStep(t, "edit", body, "edit/mail~sam@gmail.com")
	if strings.Contains(body, "data-step-back") {
		t.Error("an edit has a way back to the search")
	}
	if !strings.Contains(body, `hx-post="/settings/tools/mail"`) || !strings.Contains(body, `name="dialog" value="1"`) ||
		!strings.Contains(body, `name="account" value="sam@gmail.com"`) || strings.Contains(body, `name="name"`) ||
		!strings.Contains(body, `href="/settings/tools/mail/edit?account=sam%40gmail.com&amp;provider=choose" data-step data-hash="edit/mail~sam@gmail.com/choose"`) {
		t.Errorf("edit form: %s", mainPart(t, body))
	}
	// Another kind of account, from the edit: back to the edit.
	_, body, _ = tw.step(edit + "&provider=choose")
	wantStep(t, "edit picker", body, "edit/mail~sam@gmail.com/choose")
	if !strings.Contains(body, `<a class="step-back" href="/settings/tools/mail/edit?account=sam%40gmail.com" data-step-back data-hash="edit/mail~sam@gmail.com">`) {
		t.Error("the kinds of account of an edit do not lead back to it")
	}
	// Its access is on the tool's page; the credential and the allowed actions show here too.
	_, body, _ = tw.step(edit)
	if strings.Contains(body, `id="form-access"`) || !strings.Contains(body, `id="allowed-`+key+`"`) ||
		!strings.Contains(body, "token-note") {
		t.Errorf("no access in the edit: %s", body)
	}
	// Saved from the dialog: no step after it, the dialog closes on the list.
	form.Set("access", "free")
	form.Set("account", "sam@gmail.com")
	form.Del("password")
	form.Set("dialog", "1")
	code, body, h := tw.post("/settings/tools/mail", form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "" || h.Get("HX-Retarget") != "" || h.Get("HX-Reswap") != "none" || body != "" {
		t.Fatalf("save in the dialog: %d %v %s", code, h, body)
	}
	if got := h.Get("HX-Trigger"); got != `{"tool-saved":{"message":"\u00ab sam@gmail.com \u00bb modifi\u00e9","name":"`+key+`","new":false}}` {
		t.Errorf("saved event: %s", got)
	}
	if c.conf().Servers["mail"].AccountAccess["sam@gmail.com"] != accessFree {
		t.Error("not saved")
	}
	// A refusal stays in the form, in the dialog.
	form.Set("address", "not an address")
	_, body, h = tw.post("/settings/tools/mail", form, csrf)
	if h.Get("HX-Retarget") != "" || !strings.Contains(body, `id="tool-form"`) || !strings.Contains(body, `name="dialog" value="1"`) || !strings.Contains(body, `class="alert"`) {
		t.Errorf("refused in the dialog: %v %s", h, body)
	}
}

// Once saved, the dialog closes on the list, where the tool's row is lit
// (a new one at the top) and follows it until it is ready or says why not:
// the row alone is asked again until then.
func TestSavedRowFollowsTheTool(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	key := rowKey("mail", "sam@gmail.com")
	form := url.Values{"preset": {"mail"}, "access": {"read"}, "mail_provider": {"gmail"},
		"address": {"sam@gmail.com"}, "password": {"abcd efgh ijkl mnop"}, "dialog": {"1"}}
	code, body, h := tw.post("/settings/tools", form, csrf)
	if code != 200 || h.Get("HX-Reswap") != "none" || body != "" ||
		h.Get("HX-Trigger") != `{"tool-saved":{"message":"\u00ab sam@gmail.com \u00bb ajout\u00e9","name":"`+key+`","new":true}}` {
		t.Fatalf("save: %d %v %s", code, h, body)
	}
	if strings.Contains(body, "data-tool-saved") || strings.Contains(body, "Terminé") {
		t.Fatal("a step after the save")
	}
	// The list: the new account first, its row following it.
	waitStatus(t, c.pool.get("docs"), statusReady)
	_, list, _ := tw.do("GET", "/settings/tools?tool="+key+"&new=1", nil, nil)
	first := strings.Index(list, `<li class="tool" id="tool-`)
	if first < 0 || !strings.HasPrefix(list[first:], `<li class="tool" id="tool-`+key+`">`) {
		t.Fatalf("the new account is not first: %s", mainPart(t, list))
	}
	row := list[first:]
	row = row[:strings.Index(row, "</li>")]
	if !strings.Contains(row, "tool-status") {
		t.Errorf("the row does not follow the account: %s", row)
	}
	if strings.Contains(list[strings.Index(list, `id="tool-docs"`):], "tool-status") {
		t.Error("another row follows too")
	}
	// The account's row alone.
	if _, s, _ := tw.do("GET", "/settings/tools/mail/row?account=sam@gmail.com", nil, map[string]string{"HX-Request": "true"}); !strings.Contains(s, "<h3>Mail</h3>") {
		t.Errorf("account row: %s", s)
	}
	// Ready: said once, no more asking.
	_, s, _ := tw.do("GET", "/settings/tools/docs/row", nil, map[string]string{"HX-Request": "true"})
	if s = html.UnescapeString(s); !strings.Contains(s, "test-ok") || !strings.Contains(s, "Prêt — Eriphe peut l'utiliser.") || strings.Contains(s, "hx-get") ||
		!strings.Contains(s, "<h3 class=\"mono\">docs</h3>") {
		t.Errorf("ready: %s", s)
	}
	// Still coming up: the row asks for itself again, in place.
	sv := serverView{serverInfo: serverInfo{Name: "mails", Kind: "local", Source: sourceWorkspace, Status: statusError}, Node: "pc", Service: "starting"}
	st := statusOf("fr", sv)
	rec := httptest.NewRecorder()
	tw.w.render(rec, httptest.NewRequest("GET", "/", nil), nil, "settings", "tool-row-frag", page{Data: toolView{serverView: sv, Follow: &st}})
	if b := rec.Body.String(); !strings.Contains(b, `hx-get="/settings/tools/mails/row" hx-trigger="load delay:1500ms" hx-target="closest li" hx-swap="innerHTML"`) {
		t.Errorf("starting: %s", b)
	}
	// … an account's row asks for its own.
	st.Row = toolView{serverView: sv, Account: "sam@gmail.com"}.RowURL()
	rec = httptest.NewRecorder()
	tw.w.render(rec, httptest.NewRequest("GET", "/", nil), nil, "settings", "tool-row-frag", page{Data: toolView{serverView: sv, Account: "sam@gmail.com", Follow: &st}})
	if b := rec.Body.String(); !strings.Contains(b, `hx-get="/settings/tools/mails/row?account=sam%40gmail.com"`) {
		t.Errorf("an account starting: %s", b)
	}
	if _, _, h := tw.do("GET", "/settings/tools/nope/row", nil, map[string]string{"HX-Request": "true"}); h.Get("HX-Refresh") != "true" {
		t.Errorf("gone: %v", h)
	}
	// The older way back (#added/<name>, its step's address): the list.
	if code, _, h := tw.do("GET", "/settings/tools/docs/added", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/settings/tools?tool=docs" {
		t.Errorf("added: %d %s", code, h.Get("Location"))
	}
	// The list's flash no longer says it can be used at once.
	_, page, _ := tw.do("GET", "/settings/tools?saved=mail", nil, nil)
	if p := html.UnescapeString(page); !strings.Contains(p, "Outil « mail » enregistré. Son état est sur sa carte") || strings.Contains(p, "dès maintenant") {
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
// brings the browser back to the list, the new tool's row first and lit.
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
	if !strings.Contains(html.UnescapeString(back), `url=/settings/tools?connected=linear&tool=linear&new=1"`) {
		t.Fatalf("back: %s", back)
	}
	waitStatus(t, c.pool.get("linear"), statusReady)
	// the flash, a toast with the script; the row followed
	_, page, _ := tw.do("GET", "/settings/tools?connected=linear&tool=linear&new=1", nil, nil)
	if !strings.Contains(page, `role="status" data-toast>`) || !strings.Contains(page, `id="tool-linear">`) || !strings.Contains(page, "tool-status") {
		t.Errorf("list after the service: %s", mainPart(t, page))
	}
}

// A row signs in to an OAuth service only when it is needed: Connect when
// it never was, Reconnect when the authorization expired or was revoked;
// never a standing Reconnect.
func TestRowSignIn(t *testing.T) {
	row := func(auth, status string, token bool) toolView {
		return toolView{serverView: serverView{serverInfo: serverInfo{Name: "t", Auth: auth, Status: status}}, Token: token}
	}
	for _, tc := range []struct {
		v    toolView
		want string
	}{
		{row("oauth", statusReady, true), ""},
		{row("oauth", statusConnecting, true), ""},
		{row("oauth", statusNeedsAuth, false), "connect"},
		{row("oauth", statusNeedsAuth, true), "reconnect"},
		{row("bearer", statusNeedsAuth, false), ""},
	} {
		if got := tc.v.SignIn(); got != tc.want {
			t.Errorf("%s %s token=%v: %q, want %q", tc.v.Auth, tc.v.Status, tc.v.Token, got, tc.want)
		}
	}
}

// A refused form comes back without the secrets typed (never sent back),
// but their fields ask the browser to keep what was typed in them.
func TestRefusedFormKeepsTypedSecrets(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"calendar"}, "access": {"read"}, "cal_provider": {"icloud"},
		"cal_user": {"sam@icloud.com"}, "password": {"abcd-efgh-ijkl-mnop"}, "timezone": {"Europe/Paris"}, "dialog": {"1"}}
	if code, _, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Trigger") == "" {
		t.Fatalf("first save: %d %v", code, h)
	}
	_, body, _ := tw.post("/settings/tools", form, csrf)
	if !strings.Contains(body, "déjà connecté") || !strings.Contains(body, `id="secret-password" hx-preserve`) || strings.Contains(body, "abcd-efgh") {
		t.Fatalf("refused: %s", body)
	}
	// A second calendar is an account of the same one: no name to give.
	if _, body, _ = tw.step("/settings/tools/new?preset=calendar&provider=icloud"); strings.Contains(body, `name="name"`) {
		t.Fatalf("second calendar's name: %s", body)
	}
	// A form shown for the first time keeps nothing.
	if _, body, _ = tw.step("/settings/tools/new?preset=calendar&provider=icloud"); !strings.Contains(body, `id="secret-password"`) || strings.Contains(body, "hx-preserve") {
		t.Fatalf("new form: %s", body)
	}
}

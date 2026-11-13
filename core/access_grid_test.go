package main

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolCategory(t *testing.T) {
	no, yes := false, true
	cases := []struct {
		name string
		a    *mcp.ToolAnnotations
		want string
	}{
		{"read", &mcp.ToolAnnotations{ReadOnlyHint: true}, catRead},
		{"change", &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}, catChange},
		{"delete", &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &yes}, catDelete},
		{"delete by default", &mcp.ToolAnnotations{OpenWorldHint: &no}, catDelete},
		{"send", &mcp.ToolAnnotations{OpenWorldHint: &yes, DestructiveHint: &no}, catSend},
		{"send by default", &mcp.ToolAnnotations{DestructiveHint: &no}, catSend},
		{"unannotated", nil, catSend},
		{"annotated, empty", &mcp.ToolAnnotations{}, catSend},
	}
	if toolCategory(nil) != catSend {
		t.Error("no tool is not Send")
	}
	for _, tc := range cases {
		if got := toolCategory(&mcp.Tool{Name: "x", Annotations: tc.a}); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Each level reads as the issue's table says, and comes back from its grid.
func TestLevelsReadAsGrids(t *testing.T) {
	for level, want := range map[string]accessGrid{
		accessReadOnly: {gridFree, gridNever, gridNever, gridNever},
		accessReads:    {gridFree, gridApproval, gridApproval, gridApproval},
		accessFree:     {gridFree, gridFree, gridFree, gridFree},
		accessAsk:      {gridApproval, gridApproval, gridApproval, gridApproval},
	} {
		if got := levelGrid(level); got != want {
			t.Errorf("%s = %+v", level, got)
		}
		if got := gridLevel(want); got != level {
			t.Errorf("%s comes back as %s", level, got)
		}
	}
	// What no level says is the closest with writes, so a credential keeps them.
	if got := gridLevel(accessGrid{Delete: gridNever, Send: gridFree}); got != accessReads {
		t.Errorf("a grid of its own = %s", got)
	}
	// Ask everything: a read is still a read, on approval.
	s := Server{Access: accessAsk}
	if f := s.toolFacts(&mcp.Tool{Name: "x", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, nil); f.category != catRead || f.value != gridApproval {
		t.Errorf("ask = %+v", f)
	}
}

func TestGridChecked(t *testing.T) {
	bad := []Server{
		{Grid: &accessGrid{Send: "sometimes"}},
		{Exceptions: map[string]string{"x": "yes"}},
		{AccountGrids: map[string]accountGrid{"a": {Grid: accessGrid{Read: "maybe"}}}},
	}
	for i, s := range bad {
		if s.checkAccess() == nil {
			t.Errorf("%d: accepted", i)
		}
	}
	raw := `{"grid": {"send": "never"}, "exceptions": {"draft": "free"}, "trust_annotations": true}`
	var s Server
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s.checkAccess() != nil {
		t.Fatalf("the new form: %v", err)
	}
	if s.Grid.value(catRead) != gridFree || s.Grid.value(catChange) != gridApproval {
		t.Errorf("a row left out = %+v", s.Grid)
	}
}

// A row may be anything: nothing ties one to another, and an exception
// replaces its category's value, Never included.
func TestGridRowsAndExceptions(t *testing.T) {
	no := false
	move := &mcp.Tool{Name: "move", Annotations: &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}}
	send := &mcp.Tool{Name: "send"}
	s := Server{Grid: &accessGrid{Change: gridNever, Send: gridFree}}
	if lends(s, move) || !lends(s, send) {
		t.Errorf("lent: move %v, send %v", lends(s, move), lends(s, send))
	}
	s.Exceptions = map[string]string{"move": gridFree, "send": gridNever}
	if !lends(s, move) || lends(s, send) {
		t.Errorf("with exceptions: move %v, send %v", lends(s, move), lends(s, send))
	}
}

// An account's value decides the call that names it; one that names none
// is refused when their values differ.
func TestAccountGrids(t *testing.T) {
	no := false
	move := &mcp.Tool{Name: "move", Annotations: &mcp.ToolAnnotations{OpenWorldHint: &no, DestructiveHint: &no}}
	read := &mcp.Tool{Name: "search", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
	s := Server{AccountGrids: map[string]accountGrid{
		"pro@example.org":   {Grid: accessGrid{Change: gridNever}},
		"perso@example.org": {Grid: accessGrid{Change: gridFree}, Exceptions: map[string]string{"search": gridNever}},
	}}
	if f := s.toolFacts(move, map[string]any{"account": "PERSO@example.org"}); f.value != gridFree {
		t.Errorf("named = %+v", f)
	}
	if f := s.toolFacts(move, nil); !f.noAccount {
		t.Errorf("no account = %+v", f)
	}
	if f := s.toolFacts(read, nil); !f.noAccount {
		t.Errorf("a read no account may refuse = %+v", f)
	}
	if !lends(s, move) {
		t.Error("a tool one account allows is not lent")
	}
	if got := s.withLevel(accessReads); got.Grid != nil || got.AccountGrids != nil || len(got.AccountAccess) != 2 {
		t.Errorf("a level over grids = %+v", got)
	}
}

// The brain: Free is free on a clean turn; on a tainted one every Free
// above Read is the user's, and Read stays free. Never and On approval do
// not move. The turn the user starts is clean again.
func TestBrainFreeWriteFollowsTheTurn(t *testing.T) {
	c := newMailCore(t, accessFree)
	read, write := "mcp__mail__search_messages", "mcp__mail__move"
	for tool, want := range map[string]string{read: decideAllow, write: decideAllow} {
		if got, why := c.decideBrainCall(tool, nil, nil); got != want {
			t.Errorf("clean: %s = %q (%s)", tool, got, why)
		}
	}
	c.turn.taint("mail.search_messages")
	if got, _ := c.decideBrainCall(read, nil, nil); got != decideAllow {
		t.Errorf("tainted: a read = %q", got)
	}
	if got, why := c.decideBrainCall(write, nil, nil); got != decideAsk || why != uncleanReason {
		t.Errorf("tainted: a write = %q (%s)", got, why)
	}
	// "Always allow" is a Free exception: it follows the same rule.
	c = newMailCore(t, accessReads)
	if err := c.store.allowAction("mail", write, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decideBrainCall(write, nil, nil); got != decideAllow {
		t.Errorf("remembered, clean: %q", got)
	}
	c.turn.taint("mail.search_messages")
	if got, _ := c.decideBrainCall(write, nil, nil); got != decideAsk {
		t.Errorf("remembered, tainted: %q", got)
	}
	c.turn.start(1, 1, 0)
	if got, _ := c.decideBrainCall(write, nil, nil); got != decideAllow {
		t.Errorf("the user's next turn: %q", got)
	}
}

// The gap this closes: a Free write is never allowed in advance nor called
// without a ticket, so it cannot skip the turn's taint.
func TestBrainFreeWriteNeedsThePolicy(t *testing.T) {
	c := newMailCore(t, accessFree)
	if got := strings.Join(c.brainFreeReads(), ","); got != "mcp__mail__search_messages" {
		t.Errorf("allowed in advance: %s", got)
	}
	if c.freeCall("mcp__mail__move", nil) {
		t.Error("a Free write is a free call")
	}
	if !c.freeCall("mcp__mail__search_messages", nil) {
		t.Error("a Free read is not a free call")
	}
}

// A Free read of the open web (openWorldHint not false) is not allowed in
// advance: on a tainted turn it is the user's, as WebFetch is, and the
// proxy calls it only on a ticket.
func TestBrainOpenWorldReadFollowsTheTurn(t *testing.T) {
	c := newMailCore(t, accessFree)
	fetch := "mcp__mail__fetch_page"
	if slices.Contains(c.brainFreeReads(), fetch) {
		t.Error("an open-web read is allowed in advance")
	}
	if c.freeAhead(fetch, nil) {
		t.Error("an open-web read skips the policy")
	}
	if got, why := c.decideBrainCall(fetch, nil, nil); got != decideAllow {
		t.Errorf("clean: %q (%s)", got, why)
	}
	c.turn.taint("mail.search_messages")
	if got, why := c.decideBrainCall(fetch, nil, nil); got != decideAsk || why != uncleanReason {
		t.Errorf("tainted: %q (%s)", got, why)
	}
	if got, _ := c.decideBrainCall("mcp__mail__search_messages", nil, nil); got != decideAllow {
		t.Errorf("tainted: a closed read = %q", got)
	}
	f := c.pool.facts(fetch, nil)
	f.unclean, f.granted = true, true
	if got, _ := decideWith(fetch, nil, f, nil); got != decideAllow {
		t.Errorf("granted: %q", got)
	}
}

// Eriphe-core never reads the open web by itself, for a monitor or a
// surface: their arguments may come from a tainted turn.
func TestOpenWorldIsNoSource(t *testing.T) {
	c, _, _ := pollCore(t)
	if why := c.sourceWhy(monitorSpec{Server: "monitor", Tool: "status"}); why != "" {
		t.Fatalf("a closed read: %s", why)
	}
	if why := c.sourceWhy(monitorSpec{Server: "monitor", Tool: "page"}); !strings.Contains(why, "open web") {
		t.Errorf("an open-web read: %q", why)
	}
}

// A server the user trusts keeps its reads of the open web free: allowed
// in advance, on a tainted turn, and as a monitor's or surface's source.
func TestTrustedOpenWorldReadIsFree(t *testing.T) {
	c := newMailCore(t, accessFree)
	fetch := "mcp__mail__fetch_page"
	if err := c.setServerTrust("mail", true, trustByUser); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.brainFreeReads(), fetch) || !c.freeAhead(fetch, nil) {
		t.Error("a trusted server's open-web read is not free")
	}
	c.turn.taint("web.fetch")
	if got, why := c.decideBrainCall(fetch, nil, nil); got != decideAllow {
		t.Errorf("tainted: %q (%s)", got, why)
	}
	m, _, _ := pollCore(t)
	if err := m.setServerTrust("monitor", true, trustByUser); err != nil {
		t.Fatal(err)
	}
	if why := m.sourceWhy(monitorSpec{Server: "monitor", Tool: "page"}); why != "" {
		t.Errorf("a trusted source: %s", why)
	}
}

// A task: the rule is read at launch, and holds whatever it reads since.
func TestTaskFreeWriteFollowsItsStart(t *testing.T) {
	c := newMailCore(t, accessFree)
	if got := ask(t, c, "t-1", "mail", "search_messages", "move"); got["search_messages"] != "allowed" || got["move"] != "allowed" {
		t.Errorf("a clean start: %v", got)
	}
	if got := ask(t, c, "t-alone", "mail", "search_messages", "move"); got["search_messages"] != "allowed" || got["move"] != "user" {
		t.Errorf("no clean start: %v", got)
	}
}

// "Always allow" lifts On approval, not Never: a server gone read only stays so.
func TestRememberedDoesNotLiftNever(t *testing.T) {
	c := newMailCore(t, accessReadOnly)
	write := "mcp__mail__move"
	if err := c.store.allowAction("mail", write, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.decide(write, nil); got != decideDeny {
		t.Errorf("remembered on Never = %q", got)
	}
}

// Never: refused at the proxy, a ticket or not.
func TestNeverAtTheProxy(t *testing.T) {
	c := newMailCore(t, accessReadOnly)
	c.store.addTicket("t-1", "mcp__mail__move", nil)
	out := c.proxyCall(context.Background(), "t-1", []string{"mail"}, c.pool.get("mail"), "move", nil)
	if !out.IsError || !strings.Contains(resultText(out), "not allowed") {
		t.Errorf("a Never with a ticket: %s", resultText(out))
	}
}

func TestListToolsSaysCategoryAndValue(t *testing.T) {
	c := newMailCore(t, "")
	tools := map[string]toolInfo{}
	for _, ti := range c.listServers("mail", true)[0].Tools {
		tools[ti.Name] = ti
	}
	for name, want := range map[string][2]string{
		"search_messages": {catRead, gridFree}, "move": {catChange, gridApproval},
		"send": {catSend, gridApproval}, "unannotated": {catSend, gridApproval},
	} {
		if got := tools[name]; got.Category != want[0] || got.Value != want[1] {
			t.Errorf("%s = %+v", name, got)
		}
	}
}

// The tool's page saves a grid: its rows, its shortcuts, its exceptions.
func TestToolPageSavesTheGrid(t *testing.T) {
	tw, c, f, csrf := pendingWeb(t)
	authURL := startConnectForm(t, tw, csrf, "/settings/tools", oauthToolForm(f))
	code, state := f.authorize(authURL)
	callback(t, tw, "code="+code+"&state="+url.QueryEscape(state))
	waitStatus(t, c.pool.get("linear"), statusReady)
	form := url.Values{"grid-read": {gridFree}, "grid-change": {gridFree}, "grid-delete": {gridApproval}, "grid-send": {gridNever}}
	if code, _, h := tw.post("/settings/tools/linear/access", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %d %v", code, h)
	}
	s := c.conf().Servers["linear"]
	if s.Grid == nil || *s.Grid != (accessGrid{gridFree, gridFree, gridApproval, gridNever}) {
		t.Fatalf("saved %+v", s)
	}
	waitStatus(t, c.pool.get("linear"), statusReady) // saved, it reconnects
	_, body, _ := tw.do("GET", "/settings/tools/linear/access", nil, nil)
	if !strings.Contains(body, `name="grid-send" value="never" aria-label`) || !strings.Contains(html.UnescapeString(body), "Comme sa catégorie") {
		t.Fatalf("page: %s", body)
	}
	// A shortcut fills the grid in the page: it saves nothing, nor is it the
	// button Enter presses.
	if !strings.Contains(body, `type="button" data-fill="never"`) || !strings.Contains(body, `type="button" data-fill="free"`) ||
		!strings.Contains(body, `type="button" data-fill="approval"`) || strings.Contains(body, `name="trust"`) ||
		!strings.Contains(body, `class="alert free-warn"`) {
		t.Fatalf("shortcuts: %s", body)
	}
	form.Set("grid-send", "sometimes")
	status, body, h := tw.post("/settings/tools/linear/access", form, csrf)
	if h.Get("HX-Redirect") != "" {
		t.Fatal("a bad value saved")
	}
	// Refused, the page keeps what the user chose.
	if status != 200 || !strings.Contains(body, `name="grid-delete" value="approval" aria-label="Supprimer : Sur accord" checked`) {
		t.Fatalf("refused: %d %s", status, body)
	}
	// Every row is needed: a form without one resets nothing.
	if _, _, h := tw.post("/settings/tools/linear/access", url.Values{"grid-send": {gridFree}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("a partial form saved")
	}
}

// A tool's exceptions come from its panel; the configuration's page changes
// nothing, and shows no way to.
func TestToolPageExceptionsAndLocked(t *testing.T) {
	c := newCatalogCore(t)
	c.conf().User = "Sam"
	tw := newTestWebFor(t, c)
	csrf := tw.setup()
	_, body, _ := tw.do("GET", "/settings/tools/github/access", nil, nil)
	if !strings.Contains(body, `name="exception-create_issue"`) || !strings.Contains(body, "<fieldset class=\"access-grid\" disabled") ||
		strings.Contains(body, `name="shortcut"`) {
		t.Fatalf("a tool of the file: %s", body)
	}
	// Its exceptions are a grid like the one above, with a fourth choice:
	// a radio per value, its category's checked, nothing to change on a tool of the file.
	for _, value := range []string{"", gridFree, gridApproval, gridNever} {
		if !strings.Contains(body, `<input type="radio" name="exception-create_issue" value="`+value+`"`) {
			t.Errorf("no exception radio for %q", value)
		}
	}
	if !strings.Contains(body, `value="" aria-label="create_issue : Comme sa catégorie" checked disabled>`) || strings.Contains(body, `<select name="exception-`) {
		t.Errorf("exceptions: %s", body)
	}
	before := c.conf().Servers["github"]
	if _, _, h := tw.post("/settings/tools/github/access", url.Values{"grid-send": {gridFree}}, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("a tool of the file was changed")
	}
	if got := c.conf().Servers["github"]; got.Grid != before.Grid {
		t.Fatalf("changed: %+v", got)
	}
	post := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	own := map[string]string{"get_issue": gridNever}
	v := toolAccess{Tools: []toolException{{Name: "create_issue"}, {Name: "get_issue"}}, Own: own}
	r := post("exception-create_issue=free&exception-get_issue=")
	if ex := formExceptions(r, accessGrid{}, v); len(ex) != 1 || ex["create_issue"] != gridFree {
		t.Fatalf("exceptions = %v", ex)
	}
	// A page drawn while the server reconnected showed no panel: nothing goes.
	if ex := formExceptions(post("grid-read=free"), accessGrid{}, v); len(ex) != 1 || ex["get_issue"] != gridNever {
		t.Fatalf("no panel = %v", ex)
	}
	// On an account, what its server says is not copied; following its
	// category takes a value of its own.
	v = toolAccess{Tools: []toolException{{Name: "send", Category: catSend}, {Name: "move", Category: catDelete}},
		Inherited: map[string]string{"send": gridFree, "move": gridNever}}
	ex := formExceptions(post("exception-send=free&exception-move="), accessGrid{Delete: gridApproval}, v)
	if len(ex) != 1 || ex["move"] != gridApproval {
		t.Fatalf("inherited = %v", ex)
	}
	// A server that is down lists nothing: its exceptions stay.
	if ex := formExceptions(r, accessGrid{}, toolAccess{Own: own}); ex["get_issue"] != gridNever {
		t.Fatalf("exceptions of a server down = %v", ex)
	}
}

// An account's page saves the account's grid: it reaches its server's
// read_only, its switch holds for it alone, the other account keeps its own.
// The server has no grid of its own to save.
func TestAccountPageSavesItsGrid(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	for _, addr := range []string{"pro@example.org", "perso@example.org"} {
		form := url.Values{"preset": {"mail"}, "access": {"reads"}, "mail_provider": {"ovh"}, "address": {addr}, "password": {addr + "-pw"}}
		if code, body, _ := tw.post("/settings/tools", form, csrf); code != 200 {
			t.Fatalf("add %s: %d %s", addr, code, body)
		}
	}
	form := url.Values{"grid-read": {gridFree}, "grid-change": {gridNever}, "grid-delete": {gridNever}, "grid-send": {gridNever}}
	if _, _, h := tw.post("/settings/tools/mail/access", form, csrf); h.Get("HX-Redirect") != "" {
		t.Fatal("saved on the server of several accounts")
	}
	if c.conf().Servers["mail"].Grid != nil {
		t.Fatal("a grid no call would read")
	}
	if code, body, h := tw.post("/settings/tools/mail/access?account=pro%40example.org", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %d %s", code, body)
	}
	pro, _ := serviceAccount(t, c.core, mailService, "pro@example.org")
	perso, _ := serviceAccount(t, c.core, mailService, "perso@example.org")
	if !pro.ReadOnly || perso.ReadOnly {
		t.Fatalf("pro %+v perso %+v", pro, perso)
	}
	srv := c.conf().Servers["mail"]
	if a, ok := srv.accountAccess(map[string]any{"account": "pro@example.org"}); !ok || a.grid.Send != gridNever {
		t.Fatalf("pro: %+v", a)
	}
	if a, ok := srv.accountAccess(map[string]any{"account": "perso@example.org"}); !ok || a.level() != accessReads {
		t.Fatalf("perso: %+v", a)
	}
}

// Once one account has its own access, the server's no longer speaks for the
// others: they are written down as they were.
func TestKeepAccounts(t *testing.T) {
	grid := accessGrid{gridFree, gridFree, gridApproval, gridNever}
	srv := Server{Grid: &grid, AccountAccess: map[string]string{"pro@example.org": accessFree}}
	kept := keepAccounts(srv, svcConfig{Accounts: []svcAccount{{Name: "pro@example.org"}, {Name: "perso@example.org"}}})
	a, ok := kept.accountAccess(map[string]any{"account": "perso@example.org"})
	if !ok || a.grid != grid {
		t.Fatalf("perso: %+v", a)
	}
	if _, ok := kept.AccountGrids["pro@example.org"]; ok || srv.AccountGrids != nil {
		t.Fatalf("kept %+v, server %+v", kept.AccountGrids, srv.AccountGrids)
	}
}

// An exception that does more than read needs a credential that can.
func TestExceptionKeepsTheCredential(t *testing.T) {
	never := accessGrid{gridFree, gridNever, gridNever, gridNever}
	s := Server{Grid: &never}
	if s.level() != accessReadOnly {
		t.Fatalf("level = %s", s.level())
	}
	s.Exceptions = map[string]string{"draft": gridFree}
	if s.level() != accessReads {
		t.Errorf("with an exception = %s", s.level())
	}
	s.Exceptions = map[string]string{"draft": gridNever}
	if s.level() != accessReadOnly {
		t.Errorf("with a Never = %s", s.level())
	}
}

// Every row leads to its page, and each connection has its way.
func TestConnectionPages(t *testing.T) {
	for _, tc := range []struct {
		p            toolPreset
		auth, source string
		want         string
	}{
		{toolPreset{}, "oauth", sourceWorkspace, "/settings/tools/x/edit"},
		{toolPreset{ID: "home-assistant"}, "bearer", sourceWorkspace, "/settings/tools/x/edit"}, // a tool like any other
		{toolPreset{ID: "github", AppPage: "/settings/github"}, "connection", sourceWorkspace, "/settings/github"},
		{toolPreset{Form: "connection", Provider: "google"}, "none", sourceWorkspace, "/settings/tools"}, // its account's card
		{toolPreset{}, "bearer", sourceConfig, ""},
	} {
		if got := connectionPage("x", "", tc.p, tc.auth, tc.source); got != tc.want {
			t.Errorf("%+v = %q, want %q", tc, got, tc.want)
		}
	}
}

// The NAS's servers saved when a checkbox said whether to believe their
// annotations: one not believed counted every tool as Send, and keeps
// doing so, once; one believed, or an account believed, is left as it was.
func TestLegacyTrust(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(name, spec string) {
		if _, err := store.db.Exec(`INSERT INTO catalog (kind, name, spec, updated) VALUES (?, ?, ?, 0)`, kindServer, name, spec); err != nil {
			t.Fatal(err)
		}
	}
	put("doubted", `{"url":"https://a.example/mcp","use":"a","grid":{"read":"free","change":"free","send":"never"},"exceptions":{"x":"free"}}`)
	put("believed", `{"url":"https://b.example/mcp","use":"b","grid":{"read":"free","send":"never"},"trust_annotations":true}`)
	put("mail", `{"url":"https://m.example/mcp","use":"m","trust_annotations":true,"account_grids":{`+
		`"pro":{"grid":{"read":"free","send":"approval"},"trust_annotations":false},"perso":{"grid":{"read":"free","send":"never"}}}}`)
	legacyTrust(store)
	got := map[string]Server{}
	rows, _ := store.catalogRows()
	for _, r := range rows {
		var s Server
		json.Unmarshal(r.Spec, &s)
		got[r.Name] = s
	}
	never := accessGrid{gridNever, gridNever, gridNever, gridNever}
	if s := got["doubted"]; *s.Grid != never || s.Exceptions["x"] != gridFree {
		t.Errorf("doubted %+v", s)
	}
	if s := got["believed"]; *s.Grid != (accessGrid{Read: gridFree, Send: gridNever}) {
		t.Errorf("believed %+v", s.Grid)
	}
	approval := accessGrid{gridApproval, gridApproval, gridApproval, gridApproval}
	if s := got["mail"]; s.AccountGrids["pro"].Grid != approval || s.AccountGrids["perso"].Grid != (accessGrid{Read: gridFree, Send: gridNever}) {
		t.Errorf("mail %+v", s.AccountGrids)
	}
	// Once: a server saved since, without the field, is not read as doubted.
	put("later", `{"url":"https://c.example/mcp","use":"c","grid":{"read":"free","send":"never"}}`)
	legacyTrust(store)
	rows, _ = store.catalogRows()
	for _, r := range rows {
		var s Server
		if json.Unmarshal(r.Spec, &s); r.Name == "later" && s.Grid.Read != gridFree {
			t.Errorf("migrated twice: %s", r.Spec)
		}
	}
}

// The accounts of a tool with places become the root of their tree (the
// NAS, once): each folder kept takes its account's grid under its own
// cases, so nothing it could do changes; the account goes to Never and
// keeps its exceptions.
func TestLegacyPlaceRoot(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := `{"accounts":[{"name":"sam@example.org","provider":"google","auth":{"token_env":"A"},"scopes":["` + scopeDrive + `"]},` +
		`{"name":"bob@example.org","provider":"google","auth":{"token_env":"B"},"scopes":["` + scopeDrive + `"]}],` +
		`"places":[{"name":"Compta","account":"sam@example.org","id":"c"},{"name":"Photos","account":"sam@example.org","id":"p"},{"name":"Bob","account":"bob@example.org","id":"b"}]}`
	run, _ := json.Marshal(RunSpec{Image: filesService.Image + ":1", Transport: transportStdio, Env: map[string]EnvValue{toolConfigEnv: {Value: cfg}}})
	srv := Server{Run: run, Use: "files", AccountAccess: map[string]string{"bob@example.org": accessReads},
		AccountGrids: map[string]accountGrid{"sam@example.org": {Grid: accessGrid{gridFree, gridApproval, gridApproval, gridNever}, Exceptions: map[string]string{"x": gridFree}}},
		PlaceGrids:   map[string]accessGrid{"p": {Delete: gridFree}}}
	if err := store.putCatalog(kindServer, filesService.Base, srv); err != nil {
		t.Fatal(err)
	}
	legacyPlaceRoot(store)
	rows, _ := store.catalogRows()
	var s Server
	json.Unmarshal(rows[0].Spec, &s)
	if s.PlaceGrids["c"] != (accessGrid{gridFree, gridApproval, gridApproval, gridNever}) || s.PlaceGrids["p"] != (accessGrid{gridFree, gridApproval, gridFree, gridNever}) ||
		s.PlaceGrids["b"] != levelGrid(accessReads) {
		t.Errorf("folders: %+v", s.PlaceGrids)
	}
	if s.AccountAccess != nil || s.AccountGrids["sam@example.org"].Grid != neverGrid || s.AccountGrids["sam@example.org"].Exceptions["x"] != gridFree ||
		s.AccountGrids["bob@example.org"].Grid != neverGrid {
		t.Errorf("accounts: %+v %+v", s.AccountAccess, s.AccountGrids)
	}
	for _, p := range placesIn(filesService.Base, s) { // what each could do before
		if a := s.placeAccess(p); a.grid.value(catRead) != gridFree || a.grid.value(catChange) != gridApproval {
			t.Errorf("%s: %+v", p.Name, a.grid)
		}
	}
	// Once: a folder set back since is not given the account's grid again.
	s.PlaceGrids = nil
	store.putCatalog(kindServer, filesService.Base, s)
	legacyPlaceRoot(store)
	rows, _ = store.catalogRows()
	if json.Unmarshal(rows[0].Spec, &s); s.PlaceGrids != nil {
		t.Errorf("twice: %+v", s.PlaceGrids)
	}
}

// A catalog call that has not run reads nothing: the brain's write waiting
// for its decision, or one a card asked and that was refused at once, does
// not taint its own turn (it did before: its tool_use was counted as a
// read, and a Free write after the user's "oui" got a card). One that ran
// does.
func TestUnrunCallsDoNotTaint(t *testing.T) {
	c := newMailCore(t, accessFree)
	c.store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Runtime: "claude-code", State: stateWaitingPermission, Prompt: "brain"})
	write := "mcp__mail__move"
	in := map[string]any{"ids": []any{"m1"}, "to": "archive"}
	other := map[string]any{"ids": []any{"m2"}, "to": "archive"}
	c.store.addEvents("t-brain", []EventRow{
		// Asked earlier in the turn, refused at once for a card.
		{Seq: 1, Type: "tool_use", Data: map[string]any{"name": write, "input": other}},
		{Seq: 2, Type: "permission", Data: map[string]any{"id": "p2", "tool_name": write, "input": other}},
		{Seq: 3, Type: "permission_answer", Data: map[string]any{"id": "p2", "allow": false}},
		// The call being decided.
		{Seq: 4, Type: "tool_use", Data: map[string]any{"name": write, "input": in}},
		{Seq: 5, Type: "permission", Data: map[string]any{"id": "p5", "tool_name": write, "input": in}},
	})
	waiting := []map[string]any{{"id": "p5", "tool_name": write, "input": in}}
	if got, _ := c.eventsTaint("t-brain", 0, nil, pendingFetches(waiting)); len(got) != 0 {
		t.Fatalf("calls that never ran taint the turn: %v", got)
	}
	// Without the call waiting counted, it is a read as before.
	if got, _ := c.eventsTaint("t-brain", 0, nil, nil); !slices.Contains(got, write) {
		t.Fatalf("a call not known to wait: %v", got)
	}
	// A read that ran taints.
	c.store.addEvents("t-brain", []EventRow{{Seq: 6, Type: "tool_use", Data: map[string]any{"name": "mcp__mail__search_messages", "input": map[string]any{}}}})
	if got, _ := c.eventsTaint("t-brain", 0, nil, pendingFetches(waiting)); !slices.Contains(got, "mcp__mail__search_messages") || slices.Contains(got, write) {
		t.Fatalf("a read that ran: %v", got)
	}
}

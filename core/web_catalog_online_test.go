package main

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every online tool is complete: a name the catalog takes, its address,
// its words in French and English, a category, a logo or a letter mark
// with its colour, and a scope table that splits.
func TestOnlineCatalog(t *testing.T) {
	css, err := os.ReadFile("web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	ids, urls := map[string]bool{}, map[string]bool{}
	for _, p := range toolPresets {
		if ids[p.ID] {
			t.Errorf("preset %s twice", p.ID)
		}
		ids[p.ID] = true
		if p.ID != "remote" && p.ID != "local" && !slices.Contains(toolCategories, p.Category) {
			t.Errorf("preset %s: category %q", p.ID, p.Category)
		}
		if !strings.Contains(string(css), ".mark-"+p.ID+" ") && !strings.Contains(string(css), ".mark-"+p.ID+",") && p.ID != "remote" && p.ID != "local" &&
			p.Form != "mail" && p.Form != "calendar" {
			t.Errorf("preset %s has no colour in app.css", p.ID)
		}
		if p.FixedURL() {
			if urls[p.URL] {
				t.Errorf("address %s twice", p.URL)
			}
			urls[p.URL] = true
		}
	}
	for _, s := range onlineServices {
		p, _ := presetByID(s.id)
		if !serverName.MatchString(p.Name) || !strings.HasPrefix(p.URL, "https://") || !p.FixedURL() || !p.Trust {
			t.Errorf("preset %s: %+v", s.id, p)
		}
		if (p.Auth == "oauth") != !p.Open() || (p.Auth != "oauth" && p.Auth != "none") {
			t.Errorf("preset %s: auth %s", s.id, p.Auth)
		}
		for _, lang := range []string{"fr", "en"} {
			for _, k := range []string{"title", "desc", "use"} {
				if key := "preset." + s.id + "." + k; webStrings[lang][key] == "" {
					t.Errorf("%s: no %s", lang, key)
				}
			}
		}
		if presetLogo(s.id) == "" {
			t.Errorf("preset %s has no logo nor letter", s.id)
		}
		if webStrings["fr"]["preset."+s.id+".help"] == "" && p.Vendor() {
			t.Errorf("preset %s: no help", s.id)
		}
	}
	for id, set := range tokenScopes {
		if _, ok := presetByID(id); !ok {
			t.Errorf("scopes of %s, which is no preset", id)
		}
		if !set.byName && (len(set.read) == 0 || len(set.write) == 0) {
			t.Errorf("scopes of %s: %+v", id, set)
		}
	}
	// What the servers advertised on 2026-10-30, read as the levels ask.
	for _, tc := range []struct {
		preset string
		found  []string
		read   string
	}{
		{"dropbox", []string{"account_info.read", "file_requests.read", "file_requests.write", "files.content.read", "files.content.write",
			"files.metadata.read", "files.metadata.write", "sharing.read", "sharing.write"},
			"[account_info.read file_requests.read files.content.read files.metadata.read sharing.read]"},
		{"miro", []string{"boards:read", "boards:write", "openid", "email"}, "[boards:read openid email]"},
		{"calendly", []string{"mcp:scheduling:write", "mcp:scheduling:read"}, "[mcp:scheduling:read]"},
		{"evernote", []string{"read", "create", "write", "delete"}, "[read]"},
		{"mercury", []string{"read", "offline_access", "transactions:request", "transfers:request", "categories:create",
			"categories:edit", "transactions:update", "budgets:manage"}, "[read offline_access]"},
		{"todoist", []string{"data:read_write"}, "[data:read]"},
		{"buffer", []string{"posts:read", "posts:write", "ideas:read", "ideas:write", "account:read", "account:write", "insights:read",
			"engagements:read", "engagements:write", "snippets:read", "snippets:write", "channels:write"},
			"[account:read posts:read ideas:read insights:read engagements:read]"},
		{"metricool", []string{"mcp:read", "mcp:write"}, "[mcp:read]"},
		{"beehiiv", []string{"read", "write", "openid", "email"}, "[read openid email]"},
		{"huggingface", []string{"openid", "profile", "read-mcp", "read-repos", "jobs", "contribute-repos", "inference-api"},
			"[openid profile read-mcp read-repos]"},
		// Advertised on 2026-10-31.
		{"akiflow", []string{"mcp:read", "mcp:write"}, "[mcp:read]"},
		{"sunsama", []string{"read", "execute", "offline_access"}, "[read offline_access]"},
		{"slite", []string{"openid", "email", "mcp:read", "mcp:write", "offline_access"}, "[openid email mcp:read offline_access]"},
		{"close", []string{"mcp.read", "mcp.write_destructive", "mcp.write_safe", "offline_access"}, "[mcp.read offline_access]"},
		{"pandadoc", []string{"read", "write", "documents:read", "documents:edit", "documents:send"}, "[read documents:read]"},
		{"jotform", []string{"readOnly", "full"}, "[readOnly]"},
		{"mural", []string{"murals:read", "murals:write", "rooms:read", "workspaces:read"}, "[murals:read rooms:read workspaces:read]"},
		{"neon", []string{"read", "write"}, "[read]"},
		{"qonto", []string{"offline_access", "client.read", "client.write", "client_invoices.read", "client_invoice.write",
			"request_transfers.write", "card.read"}, "[offline_access client.read client_invoices.read card.read]"},
		{"amplitude", []string{"all:read", "all:write"}, "[all:read]"},
		{"grafana", []string{"grafana:read", "grafana:query", "grafana:write"}, "[grafana:read grafana:query]"},
		{"honeycomb", []string{"mcp:read", "mcp:write"}, "[mcp:read]"},
		{"wave", []string{"mcp:read", "mcp:write"}, "[mcp:read]"},
		{"circle", []string{"read", "write"}, "[read]"},
		{"leadpages", []string{"pages:read", "pages:write", "sites:read", "sites:write", "blogs:read", "blogs:write", "ads:read", "ads:write"},
			"[pages:read sites:read blogs:read ads:read]"},
		{"lovable", []string{"offline", "projects:read", "projects:write", "workspaces:read", "workspaces:write"},
			"[offline projects:read workspaces:read]"},
		{"mintlify", []string{"docs:read", "docs:write", "nav:write", "config:write", "session:write", "deployment:read", "deployment:write",
			"members:read", "members:write", "billing:read", "integrations:write", "analytics:read", "offline_access"},
			"[docs:read deployment:read members:read billing:read analytics:read offline_access]"},
		// Explicit: by their names, publish and full_access would read as reads.
		{"kajabi", []string{"read", "write:content", "write:commerce", "write:contacts", "write:community", "write:emails",
			"write:automations", "publish", "write:theme", "admin:users"}, "[read]"},
		{"typesense", []string{"clusters:read", "clusters:restart", "data:full_access", "data:search"},
			"[clusters:read metrics:read backups:read search_insights:read data:view data:search]"},
	} {
		got, granted := scopesFor(tc.preset, accessReadOnly, tc.found, false)
		if fmtList(got) != tc.read || granted != grantedRead {
			t.Errorf("%s read only: %v %s, want %s", tc.preset, got, granted, tc.read)
		}
		if _, granted := scopesFor(tc.preset, accessReads, tc.found, false); granted != grantedWrite {
			t.Errorf("%s reads: %s", tc.preset, granted)
		}
	}
	// A single scope: not scoped, Eriphe holds to the level.
	for _, tc := range []struct {
		preset string
		found  []string
	}{{"stripe", []string{"mcp"}}, {"granola", []string{"mcp"}}, {"fastmail", []string{"https://www.fastmail.com/dev/mcp", "offline_access"}},
		{"typefully", []string{"full_access"}}, {"hootsuite", []string{"offline"}}, {"kit", []string{"public"}}, {"make", nil},
		{"basecamp", []string{"mcp", "full"}}, {"coda", []string{"mcp:all"}}, {"readai", []string{"mcp:execute"}}, {"square", nil},
		{"railway", []string{"workspace:member"}}, {"ifttt", []string{"mcp"}}, {"baserow", []string{"mcp"}}, {"guru", []string{"default"}},
		{"padlet", []string{"mcp"}}, {"apify", []string{"full_api_access"}}, {"similarweb", []string{"read"}}, {"sanity", []string{"global"}},
		{"helpscout", []string{"helpscout"}}, {"plain", []string{"openid", "offline_access"}},
		{"cloudinary", []string{"openid", "profile", "email", "offline_access", "asset_management", "upload", "media_generation"}},
		{"datadog", nil}, {"paddle", nil}, {"planetscale", nil}, {"axiom", nil}, {"hex", nil},
		{"postman", nil}, {"contentful", nil}, {"klaviyo", nil}, {"mailerlite", nil}, {"grain", nil}, {"bitly", nil}} {
		if _, granted := scopesFor(tc.preset, accessReadOnly, tc.found, false); granted != grantedNone {
			t.Errorf("%s: %s", tc.preset, granted)
		}
	}
}

func fmtList(s []string) string { return "[" + strings.Join(s, " ") + "]" }

// The gallery: a search, tags, and the tools already added marked.
func TestGalleryCatalog(t *testing.T) {
	tw, _, _ := catalogWeb(t, func(c *Config) {
		c.Servers["taches"] = Server{URL: "https://ai.todoist.net/mcp", Auth: &ServerAuth{OAuth: &OAuthConfig{}}, Use: "tâches"}
	})
	_, body, _ := tw.do("GET", "/settings/tools/add", nil, nil)
	if !strings.Contains(body, `data-finder-bar hidden`) || !strings.Contains(body, `type="search"`) {
		t.Error("no search field, hidden until the script shows it")
	}
	tile := func(id string) string {
		i := strings.Index(body, `class="ftile preset-`+id+`"`)
		if i < 0 {
			t.Fatalf("no tile %s", id)
		}
		j := strings.Index(body[i:], "</li>")
		return html.UnescapeString(body[i : i+j])
	}
	for id, want := range map[string][]string{
		"todoist":   {"ajouté", "lecture seule possible"},
		"evernote":  {"payant", "lecture seule possible"},
		"dropbox":   {"bêta"},
		"exa":       {"sans compte"},
		"strava":    {"ne fait que lire"},
		"stripe":    {"Paiements, clients et factures Stripe."},
		"buffer":    {"lecture seule possible"},
		"kit":       {"payant"},
		"hootsuite": {"payant"},
	} {
		for _, w := range want {
			if !strings.Contains(tile(id), w) {
				t.Errorf("tile %s lacks %q: %s", id, w, tile(id))
			}
		}
	}
	for _, id := range []string{"stripe", "notion"} {
		if strings.Contains(tile(id), "lecture seule possible") || strings.Contains(tile(id), "ajouté") {
			t.Errorf("tile %s: %s", id, tile(id))
		}
	}
	if !strings.Contains(body, `data-search="todoist todoist tâches et projets dans todoist. tâches &amp; notes todo to-do taches tasks"`) {
		t.Error("the search words of a tile")
	}
	// A social tool is found by the networks it reaches.
	if !strings.Contains(body, `data-search="buffer buffer posts, programmation, stats et commentaires, par buffer. réseaux &amp; newsletters linkedin instagram`) {
		t.Error("the search words of a social tool")
	}
	for _, not := range []string{"Figma, Slack, Asana", "ClickUp", "Articles Substack, Medium, Reddit"} {
		if !strings.Contains(body, not) {
			t.Errorf("the services not possible yet lack %s", not)
		}
		if strings.Contains(body, `preset-figma"`) || strings.Contains(body, `preset-slack"`) {
			t.Error("a service that needs an app is in the catalog")
		}
	}
	// The tools page has them all in its dialog, the added one marked.
	_, body, _ = tw.do("GET", "/settings/tools", nil, nil)
	if _, dlg := toolsList(t, body); !strings.Contains(dlg, "preset-stripe") || !strings.Contains(dlg, `<span class="ftile-added" title="ajouté">`) {
		t.Error("the tools page's dialog")
	}
	// A vendor's page says whether its token follows the level.
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=dropbox", nil, nil)
	if !strings.Contains(html.UnescapeString(body), "Le jeton suit ce choix") || !strings.Contains(body, "Se connecter à Dropbox") ||
		!strings.Contains(body, "bêta") {
		t.Error("dropbox's page")
	}
	// Buffer's page names the networks it reaches.
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=buffer", nil, nil)
	if b := html.UnescapeString(body); !strings.Contains(b, "atteint Instagram, Facebook, LinkedIn, X, Threads, TikTok, YouTube, Pinterest, Bluesky, Mastodon et les Notes Substack") ||
		!strings.Contains(b, "Le jeton suit ce choix") || !strings.Contains(b, "Se connecter à Buffer") {
		t.Error("buffer's page")
	}
	_, body, _ = tw.do("GET", "/settings/tools/new?preset=stripe", nil, nil)
	if !strings.Contains(html.UnescapeString(body), "ne propose pas de jeton en lecture seule") {
		t.Error("stripe's page does not say Eriphe holds to the level")
	}
}

// openMCP is an MCP server open without an account.
func openMCP(t *testing.T) *httptest.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "open"}, nil)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscover(t *testing.T) {
	ctx := context.Background()
	hc := http.DefaultClient
	f := newFakeAS(t)
	f.scopes = []string{"notes:read", "notes:write"}
	d, err := discover(ctx, hc, f.srv.URL+"/mcp")
	if err != nil || d.Reach != reachOAuth || !d.DCR || d.Secret || !d.ReadOnly() || fmtList(d.Read) != "[notes:read]" || d.Issuer != f.srv.URL {
		t.Fatalf("oauth: %+v %v", d, err)
	}
	f2 := newFakeAS(t)
	f2.secretOnly, f2.methods = true, []string{"client_secret_basic"}
	f2.scopes = []string{"mcp"}
	if d, _ := discover(ctx, hc, f2.srv.URL+"/mcp"); d.Reach != reachOAuth || !d.Secret || d.ReadOnly() {
		t.Fatalf("secret only: %+v", d)
	}
	f3 := newFakeAS(t)
	f3.methods = []string{"private_key_jwt"}
	if d, _ := discover(ctx, hc, f3.srv.URL+"/mcp"); d.Reach != reachApp {
		t.Fatalf("no method Eriphe has: %+v", d)
	}
	if d, _ := discover(ctx, hc, openMCP(t).URL); d.Reach != reachOpen {
		t.Fatalf("open: %+v", d)
	}
	// OAuth without registration: a client is to be made at the vendor.
	noDCR := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			writeJSON(w, 200, map[string]any{"issuer": "http://" + r.Host, "authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint": "http://" + r.Host + "/token", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}})
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer noDCR.Close()
	if d, _ := discover(ctx, hc, noDCR.URL+"/mcp"); d.Reach != reachApp || d.DCR {
		t.Fatalf("no DCR: %+v", d)
	}
	// A 401 without any metadata wants a key; 403 refuses; 404 is no MCP.
	for code, want := range map[int]string{401: reachToken, 403: reachRefused, 404: reachNotMCP} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", code) }))
		if d, _ := discover(ctx, hc, srv.URL+"/mcp"); d.Reach != want {
			t.Errorf("%d: %+v", code, d)
		}
		srv.Close()
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if d, _ := discover(ctx, hc, gone.URL+"/mcp"); d.Reach != reachUnreachable {
		t.Errorf("unreachable: %+v", d)
	}
	for _, bad := range []string{"", "mcp.example.com", "ftp://x/y", "https://user:pw@x.example/mcp"} {
		if _, err := discover(ctx, hc, bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
	for host, want := range map[string]string{"mcp.example.com": "example", "api.foo-bar.io": "foo-bar", "huggingface.co": "huggingface",
		"netlify-mcp.netlify.app": "netlify-mcp-netlify", "127.0.0.1": "127-0-0"} {
		if got := guessName(host); got != want {
			t.Errorf("guessName(%s) = %s, want %s", host, got, want)
		}
	}
}

// "Autre service" in the workspace: the address, what was found, and the
// form filled with it.
func TestWebDiscover(t *testing.T) {
	tw, _, csrf := catalogWeb(t, nil)
	f := newFakeAS(t)
	_, body, _ := tw.do("GET", "/settings/tools/add", nil, nil)
	if !strings.Contains(body, `href="/settings/tools/add/other"`) {
		t.Fatal("the gallery does not lead to Autre service")
	}
	_, body, _ = tw.do("GET", "/settings/tools/add/other", nil, nil)
	if !strings.Contains(body, `action="/settings/tools/add/other"`) || !strings.Contains(body, `name="csrf"`) {
		t.Fatal("no address form")
	}
	if code, _, _ := tw.post("/settings/tools/add/other", url.Values{"url": {f.srv.URL + "/mcp"}}, "forged"); code != http.StatusForbidden {
		t.Fatalf("forged CSRF: %d", code)
	}
	code, body, _ := tw.post("/settings/tools/add/other", url.Values{"url": {f.srv.URL + "/mcp"}}, csrf)
	body = html.UnescapeString(body)
	if code != 200 || !strings.Contains(body, "Connexion en un clic") || strings.Contains(body, "<html") {
		t.Fatalf("discover: %d %s", code, body)
	}
	i := strings.Index(body, `href="/settings/tools/new?`)
	if i < 0 {
		t.Fatalf("no link to the form: %s", body)
	}
	link := body[i+6 : i+6+strings.Index(body[i+6:], `"`)]
	if !strings.Contains(link, "auth=oauth") || !strings.Contains(link, "preset=remote") {
		t.Fatalf("link = %s", link)
	}
	_, form, _ := tw.do("GET", link, nil, nil)
	if !strings.Contains(form, `value="`+f.srv.URL+`/mcp"`) || !strings.Contains(form, `name="auth" value="oauth" checked`) ||
		!strings.Contains(form, `name="name" value="127-0-0"`) {
		t.Fatalf("form not filled: %s", form)
	}
	// Without htmx, the whole page comes back with the result.
	code, body, _ = tw.do("POST", "/settings/tools/add/other", url.Values{"url": {"nope"}, "csrf": {csrf}}, nil)
	if code != 200 || !strings.Contains(body, "<html") || !strings.Contains(body, "Adresse invalide") {
		t.Fatalf("plain post: %d", code)
	}
	// A preset's address points to its tile.
	withPresetURL(t, "linear", f.srv.URL+"/mcp")
	_, body, _ = tw.post("/settings/tools/add/other", url.Values{"url": {f.srv.URL + "/mcp"}}, csrf)
	if !strings.Contains(html.UnescapeString(body), "C'est Linear, qui est déjà dans la liste.") || !strings.Contains(body, `href="/settings/tools/new?preset=linear"`) {
		t.Fatalf("a preset's address: %s", body)
	}
}

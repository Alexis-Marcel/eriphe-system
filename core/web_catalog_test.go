package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// catalogWeb is the workspace over a dynamic core with an open vault.
func catalogWeb(t *testing.T, mutate func(*Config)) (*testWeb, *catalogCore, string) {
	t.Helper()
	cc := newDynamicCore(t, mutate)
	tw := newTestWebFor(t, cc.core)
	return tw, cc, tw.setup()
}

func TestWebAddToolWithToken(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"home-assistant"}, "name": {"maison"}, "use": {"the house"}, "kind": {"remote"},
		"url": {"http://ha.local:8123/api/mcp"}, "auth": {"bearer"}, "token": {"ha-token-value-42"}, "trust": {"1"}}
	// Without the token: the form comes back with why, nothing saved.
	noTok := url.Values{}
	for k, v := range form {
		noTok[k] = v
	}
	noTok.Del("token")
	if code, body, _ := tw.post("/settings/tools", noTok, csrf); code != 200 || !strings.Contains(body, "Colle le jeton") || !strings.Contains(body, `value="http://ha.local:8123/api/mcp"`) {
		t.Fatalf("no token: %d %s", code, body)
	}
	code, body, h := tw.post("/settings/tools", form, csrf)
	if code != 200 || h.Get("HX-Redirect") != "/settings/tools?saved=maison&tool=maison&new=1" {
		t.Fatalf("save: %d %v %s", code, h, body)
	}
	srv := c.conf().Servers["maison"]
	if srv.Auth == nil || srv.Auth.BearerFile != "vault:server.maison.token" || !srv.TrustAnnotations {
		t.Fatalf("server = %+v", srv)
	}
	waitStatus(t, c.pool.get("maison"), statusReady)
	for _, path := range []string{"/settings/tools?saved=maison", "/settings/tools/maison/edit"} {
		_, body, _ := tw.do("GET", path, nil, nil)
		if strings.Contains(body, "ha-token-value-42") {
			t.Fatalf("%s shows the token", path)
		}
		if path == "/settings/tools/maison/edit" && (!strings.Contains(body, "défini — remplacer") || !strings.Contains(body, "the house")) {
			t.Fatalf("edit form: %s", body)
		}
		if path != "/settings/tools/maison/edit" && (!strings.Contains(body, "enregistré") || !strings.Contains(body, "Outil « maison »")) {
			t.Fatalf("tools page: %s", body)
		}
	}
	// Edit without retyping the token keeps it; the name cannot change.
	form.Del("token")
	form.Set("use", "lights and heating")
	form.Set("name", "renamed")
	if code, _, h := tw.post("/settings/tools/maison", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("edit: %d", code)
	}
	if got, _ := readSecret("vault:server.maison.token"); got != "ha-token-value-42" || c.conf().Servers["maison"].Use != "lights and heating" {
		t.Fatal("edit lost the token or the change")
	}
	if _, ok := c.conf().Servers["renamed"]; ok {
		t.Fatal("renamed")
	}
	// A configuration server cannot be edited or removed here.
	if _, body, _ := tw.post("/settings/tools/docs", form, csrf); !strings.Contains(body, "défini dans la configuration") {
		t.Fatalf("edit config server: %s", body)
	}
	if _, _, h := tw.post("/settings/tools/docs/delete", nil, csrf); !strings.Contains(h.Get("HX-Trigger"), "error") {
		t.Fatal("removed a config server")
	}
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "Défini dans la configuration") ||
		strings.Contains(body, "/settings/tools/docs/edit") {
		t.Fatal("config server not shown read-only")
	}
	// Remove.
	if code, _, h := tw.post("/settings/tools/maison/delete", nil, csrf); code != 200 || !strings.Contains(h.Get("HX-Redirect"), "removed=maison") {
		t.Fatalf("delete: %d %v", code, h)
	}
	if c.pool.get("maison") != nil {
		t.Fatal("still in the pool")
	}
	if _, err := readSecret("vault:server.maison.token"); err == nil {
		t.Fatal("token kept after removal")
	}
}

// Every preset, filled as a user would, gives an entry the catalog takes.
func TestWebPresetsAreValid(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	fills := map[string]url.Values{
		"home-assistant": {"token": {"t"}, "url": {"http://192.168.0.10:8123/api/mcp"}},
		"github":         {"token": {"ghp_x"}},
		"linear":         {},
		"notion":         {},
		"mail":           {"address": {"someone@gmail.com"}, "password": {"app-pw"}},
		"calendar":       {"cal_provider": {"icloud"}, "cal_user": {"someone@icloud.com"}, "password": {"app-pw"}, "timezone": {"Europe/Paris"}},
		"remote":         {"url": {"https://mcp.example.com/mcp"}, "use": {"x"}},
		"local":          {"image": {"mcp/sequentialthinking"}, "use": {"think"}, "transport": {"stdio"}, "egress": {"1"}},
	}
	for _, p := range toolPresets {
		if p.Form == "drive" {
			continue // its own page (web_drive_test.go)
		}
		f := newToolForm("fr", p)
		form := url.Values{"preset": {p.ID}, "name": {"p-" + p.ID}, "use": {f.Use}, "kind": {f.Kind}, "url": {f.URL}, "auth": {f.Auth},
			"transport": {transportStdio}, "egress": {"1"}, "timezone": {"Europe/Paris"}}
		form.Set("access", f.Access)
		for k, v := range fills[p.ID] {
			form[k] = v
		}
		code, body, h := tw.post("/settings/tools", form, csrf)
		if code != 200 || h.Get("HX-Redirect") == "" {
			t.Errorf("preset %s: %d %s", p.ID, code, body)
			continue
		}
		srv := c.conf().Servers["p-"+p.ID]
		if srv.Use == "" || srv.TrustAnnotations != p.Trust || srv.level() != p.level() {
			t.Errorf("preset %s: %+v", p.ID, srv)
		}
		if p.Kind == "remote" && (srv.URL == "" || srv.authKind() != p.Auth) {
			t.Errorf("preset %s: %+v", p.ID, srv)
		}
	}
	mail := c.conf().runSpecs["p-mail"]
	if mail.Image != mailImage || mail.Env[mailSecretEnv].File != "vault:service.p-mail.mail_password" ||
		!strings.Contains(mail.Env["MAIL_CONFIG"].Value, `"address":"someone@gmail.com"`) || !strings.Contains(mail.Env["MAIL_CONFIG"].Value, `"password_env":"MAIL_PASSWORD"`) {
		t.Fatalf("mail = %+v", mail)
	}
	cal := c.conf().runSpecs["p-calendar"]
	if !strings.Contains(cal.Env["CALENDAR_CONFIG"].Value, `"provider":"icloud"`) || cal.Env[calSecretEnv].File == "" {
		t.Fatalf("calendar = %+v", cal)
	}
	if _, err := c.store.service("p-mail"); err != nil {
		t.Fatal("mail service not registered")
	}
	// The mail tool edits back in its own form, without its password.
	_, body, _ := tw.do("GET", "/settings/tools/p-mail/edit", nil, nil)
	if !strings.Contains(body, `value="someone@gmail.com"`) || strings.Contains(body, "app-pw") || !strings.Contains(body, "défini — remplacer") {
		t.Fatalf("mail edit: %s", body)
	}
	// Bad mail inputs are refused in words.
	if _, body, _ := tw.post("/settings/tools", url.Values{"preset": {"mail"}, "name": {"m2"}, "use": {"x"}, "address": {"nobody@unknown.example"}, "password": {"x"}}, csrf); !strings.Contains(body, "Messagerie non reconnue") {
		t.Fatalf("unknown provider: %s", body)
	}
	if _, body, _ := tw.post("/settings/tools", url.Values{"preset": {"mail"}, "name": {"m2"}, "use": {"x"}, "address": {"someone@gmail.com"}}, csrf); !strings.Contains(body, "mot de passe d&#39;application") {
		t.Fatalf("no password: %s", body)
	}
}

func TestWebLocalToolEnvSecrets(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	form := url.Values{"preset": {"local"}, "name": {"notes"}, "use": {"my notes"}, "kind": {"local"}, "image": {"ghcr.io/x/notes"},
		"transport": {"http"}, "port": {"8080"}, "egress": {"1"}, "volume": {"1"}, "env_count": {"3"},
		"env_name_0": {"LOG_LEVEL"}, "env_value_0": {"info"},
		"env_name_1": {"API_KEY"}, "env_value_1": {"k-123"}, "env_secret_1": {"1"}}
	if code, body, h := tw.post("/settings/tools", form, csrf); code != 200 || h.Get("HX-Redirect") == "" {
		t.Fatalf("save: %d %s", code, body)
	}
	spec := c.conf().runSpecs["notes"]
	if spec.Port != 8080 || spec.Path != "/mcp" || !spec.Volume || spec.Env["LOG_LEVEL"].Value != "info" || spec.Env["API_KEY"].File != "vault:service.notes.api_key" {
		t.Fatalf("spec = %+v", spec)
	}
	_, body, _ := tw.do("GET", "/settings/tools/notes/edit", nil, nil)
	if strings.Contains(body, "k-123") || !strings.Contains(body, `value="API_KEY"`) || !strings.Contains(body, `value="info"`) {
		t.Fatalf("edit: %s", body)
	}
	// Kept when not retyped.
	form.Del("env_value_1")
	if _, _, h := tw.post("/settings/tools/notes", form, csrf); h.Get("HX-Redirect") == "" {
		t.Fatal("edit refused")
	}
	if got, _ := readSecret("vault:service.notes.api_key"); got != "k-123" {
		t.Fatal("env secret lost")
	}
	// The saved keys (Mon espace) say what each belongs to, never its value.
	_, body, _ = tw.do("GET", "/settings/account", nil, nil)
	if !strings.Contains(body, "Outil « notes »") || !strings.Contains(body, "API_KEY") || strings.Contains(body, "k-123") {
		t.Fatalf("secrets: %s", body)
	}
	// Replace it there.
	if _, body, _ := tw.post("/settings/secrets", url.Values{"name": {"service.notes.api_key"}, "value": {"k-456"}}, csrf); !strings.Contains(body, "remplacé") || strings.Contains(body, "k-456") {
		t.Fatalf("replace: %s", body)
	}
	if got, _ := readSecret("vault:service.notes.api_key"); got != "k-456" {
		t.Fatal("not replaced")
	}
	// No new named secret from there.
	tw.post("/settings/secrets", url.Values{"name": {"made-up"}, "value": {"x"}}, csrf)
	if _, err := readSecret("vault:made-up"); err == nil {
		t.Fatal("a named secret was created")
	}
}

func TestWebProvidersAndAgents(t *testing.T) {
	tw, c, csrf := catalogWeb(t, func(cfg *Config) { cfg.Providers, cfg.Agents, cfg.DefaultAgent = nil, nil, "" })
	if _, body, _ := tw.post("/settings/providers", url.Values{"name": {"or"}, "kind": {"openrouter-api"}}, csrf); !strings.Contains(body, "Colle la clé") {
		t.Fatalf("no key: %s", body)
	}
	if _, _, h := tw.post("/settings/providers", url.Values{"name": {"or"}, "kind": {"openrouter-api"}, "key": {"sk-or-secret"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatal("provider refused")
	}
	if _, body, _ := tw.post("/settings/agents", url.Values{"name": {"kimi"}, "provider": {"or"}, "model": {"other"}, "use": {"x"}}, csrf); !strings.Contains(body, "nom du modèle") {
		t.Fatalf("no model: %s", body)
	}
	if _, body, h := tw.post("/settings/agents", url.Values{"name": {"kimi"}, "runtime": {"opencode"}, "provider": {"or"},
		"model": {"openrouter/moonshotai/kimi-k2"}, "use": {"second opinion"}, "prefer_cpu": {"1"}, "prefers": {"fast"}}, csrf); h.Get("HX-Redirect") == "" {
		t.Fatalf("agent refused: %s", body)
	}
	a := c.conf().Agents["kimi"]
	if strings.Join(a.Prefers, ",") != "cpu,fast" || c.conf().DefaultAgent != "kimi" {
		t.Fatalf("agent = %+v, default %q", a, c.conf().DefaultAgent)
	}
	_, body, _ := tw.do("GET", "/settings/agents", nil, nil)
	for _, want := range []string{"kimi", "second opinion", "OpenRouter", "Utilisé par kimi", `name="agent"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agents page lacks %q", want)
		}
	}
	if strings.Contains(body, "sk-or-secret") {
		t.Fatal("key shown")
	}
	if _, _, h := tw.post("/settings/providers/or/delete", nil, csrf); !strings.Contains(h.Get("HX-Trigger"), "kimi") {
		t.Fatalf("removed a provider in use: %v", h)
	}
	if _, _, h := tw.post("/settings/agents/kimi/delete", nil, csrf); h.Get("HX-Redirect") == "" {
		t.Fatal("agent not removed")
	}
	if _, _, h := tw.post("/settings/providers/or/delete", nil, csrf); h.Get("HX-Redirect") == "" {
		t.Fatal("provider not removed")
	}
}

// Every form of the catalog refuses a request without the session's token.
func TestWebCatalogCSRF(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	c.putServer(putCreate, "x", Server{URL: "https://x.example/mcp", Auth: &ServerAuth{None: true}, Use: "x"}, nil)
	paths := []string{"/settings/tools", "/settings/tools/x", "/settings/tools/x/delete", "/settings/agents", "/settings/agents/sonnet",
		"/settings/agents/sonnet/delete", "/settings/default-agent", "/settings/providers", "/settings/providers/anthropic",
		"/settings/providers/anthropic/delete", "/settings/secrets", "/settings/secrets/import", "/settings/secrets/x/delete",
		"/settings/servers/x/connect", "/settings/servers/x/callback"}
	for _, p := range paths {
		if code, _, _ := tw.post(p, url.Values{"name": {"y"}, "use": {"y"}}, "forged"); code != http.StatusForbidden {
			t.Errorf("POST %s without the token: %d", p, code)
		}
		if code, _, _ := tw.do("POST", p, url.Values{"name": {"y"}}, map[string]string{"X-CSRF-Token": csrf, "Origin": "https://evil.example"}); code != http.StatusForbidden {
			t.Errorf("POST %s from another origin: %d", p, code)
		}
	}
	if _, ok := c.conf().Servers["y"]; ok || c.conf().Servers["x"].URL == "" {
		t.Fatal("a refused POST changed the catalog")
	}
}

func TestWebSkippedEntryShown(t *testing.T) {
	cc := newDynamicCore(t, nil)
	cc.store.putCatalog(kindServer, "broken", Server{URL: "ftp://x", Auth: &ServerAuth{None: true}, Use: "x"})
	c2 := startDynamicCore(t, cc.base, cc.store)
	tw := newTestWebFor(t, c2.core)
	tw.setup()
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, "« broken » n&#39;a pas pu être chargé") {
		t.Fatalf("skipped not shown: %s", body)
	}
}

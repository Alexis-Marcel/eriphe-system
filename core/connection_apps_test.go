package main

import (
	"html"
	"net/url"
	"strings"
	"testing"
)

// The Google app kept before connection apps is read where it was, and
// saving the app again moves it to app.google.client.
func TestAppClientLegacy(t *testing.T) {
	tw, _, csrf := filesCore(t, nil)
	v := currentVault()
	v.set("google-drive-client", `{"client_id":"`+testClientID+`","client_secret":"`+testClientSecret+`"}`)
	if app, err := googleClient(); err != nil || app.ID != testClientID {
		t.Fatalf("legacy: %+v %v", app, err)
	}
	_, body, _ := tw.do("GET", "/settings/apps/google", nil, nil)
	if !strings.Contains(body, testClientID) || strings.Contains(body, testClientSecret) {
		t.Fatalf("page: %s", body)
	}
	code, body, _ := tw.post("/settings/apps/google", url.Values{"client_id": {"456-def" + providerGoogle.ClientIDSuffix},
		"client_secret": {"GOCSPX-new"}, "back": {"/settings/connections/google"}}, csrf)
	if code != 200 || !strings.Contains(body, "Ton app est enregistrée") || !strings.Contains(body, `href="/settings/connections/google"`) || strings.Contains(body, "GOCSPX-new") {
		t.Fatalf("save: %d %s", code, body)
	}
	if _, err := v.get("google-drive-client"); err == nil {
		t.Fatal("the legacy secret stays")
	}
	if app, err := appClient(providerGoogle); err != nil || app.ID != "456-def"+providerGoogle.ClientIDSuffix {
		t.Fatalf("saved: %+v %v", app, err)
	}
}

// The page is the provider's data: its steps, the scopes and the redirect
// URI to copy, a JSON file only if its console gives one, its own client
// id rule, and its redirect rule.
func TestAppPage(t *testing.T) {
	tw, _, csrf := filesCore(t, nil)
	_, body, _ := tw.do("GET", "/settings/apps/google", nil, nil)
	body = html.UnescapeString(body)
	for _, want := range []string{"Ton app Google", "Crée ton app", "console.cloud.google.com › New project", scopeMail + "," + scopeCalendar,
		tw.srv.URL + "/oauth/callback", `name="client_json"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("want %q in %s", want, body)
		}
	}
	if code, _, _ := tw.do("GET", "/settings/apps/nowhere", nil, nil); code != 404 {
		t.Fatalf("unknown provider: %d", code)
	}

	p := &signInProvider{Name: "example", Label: "Example", ClientIDSuffix: ".example.org", Redirect: redirectPublicHTTPS,
		App: appGuide{Steps: []appStep{{Text: "app.google.project", Copy: appCopyCallback}}}}
	signInProviders[p.Name] = p
	t.Cleanup(func() { delete(signInProviders, p.Name) })
	_, body, _ = tw.do("GET", "/settings/apps/example", nil, nil)
	if !strings.Contains(body, "Ton app Example") || strings.Contains(body, `name="client_json"`) || !strings.Contains(body, tw.srv.URL+"/oauth/callback") {
		t.Fatalf("example: %s", body)
	}
	if _, body, _ := tw.post("/settings/apps/example", url.Values{"client_id": {"abc" + providerGoogle.ClientIDSuffix}, "client_secret": {"s"}}, csrf); strings.Contains(body, "Ton app est enregistrée") {
		t.Fatalf("another provider's client id: %s", body)
	}
	if _, body, _ := tw.post("/settings/apps/example", url.Values{"client_id": {"abc.example.org"}, "client_secret": {"s"}}, csrf); !strings.Contains(body, "Ton app est enregistrée") {
		t.Fatalf("save: %s", body)
	}
	if app, err := appClient(p); err != nil || app.ID != "abc.example.org" {
		t.Fatalf("%+v %v", app, err)
	}
	if _, err := currentVault().get("app.example.client"); err != nil {
		t.Fatal("not in the vault under its name")
	}
}

// The app's test, without the provider's consent: the address, the client,
// then each account connected, which it marks.
func TestAppTest(t *testing.T) {
	g := newFakeGoogle(t)
	tw, c, csrf := filesCore(t, nil)
	g.clientID, g.secret, g.redirect = testClientID, testClientSecret, tw.srv.URL+"/oauth/callback"
	test := func() string {
		t.Helper()
		code, body, _ := tw.post("/settings/apps/google/test", nil, csrf)
		if code != 200 {
			t.Fatalf("test: %d %s", code, body)
		}
		return html.UnescapeString(body)
	}
	if body := test(); !strings.Contains(body, "Google accepte l'adresse de ton espace") || !strings.Contains(body, "Pas encore d'app") ||
		!strings.Contains(body, "Aucun compte connecté") {
		t.Fatalf("no app: %s", body)
	}
	storeGoogleClient(oauthApp{ID: testClientID, Secret: testClientSecret})
	g.scope = scopeMail
	googleSignIn(t, tw, g, csrf, url.Values{"service": {"mail"}})
	if body := test(); !strings.Contains(body, "Google reconnaît ton app ("+testClientID+")") || !strings.Contains(body, tw.srv.URL+"/oauth/callback") ||
		!strings.Contains(body, "Le compte sam@example.org est connecté") || strings.Contains(body, testClientSecret) {
		t.Fatalf("working: %s", body)
	}
	g.clientID = "999-zzz" + providerGoogle.ClientIDSuffix // the app was deleted at Google
	body := test()
	if !strings.Contains(body, "est refusé") || !strings.Contains(body, "L'autorisation du compte sam@example.org a été retirée") {
		t.Fatalf("refused: %s", body)
	}
	if cn, _ := c.googleAccount("sam@example.org"); cn.Lapsed == "" {
		t.Fatal("the test did not mark the account")
	}
	if _, body, _ := tw.do("GET", "/settings/apps/google", nil, nil); !strings.Contains(body, `hx-post="/settings/apps/google/test"`) {
		t.Fatal("no test on the app page")
	}
}

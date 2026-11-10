package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A refresh grant lent by eriphe-core: the access token is fetched once and
// reused while it lasts; a refused refresh token says to reconnect.
func TestRefreshGrant(t *testing.T) {
	var calls atomic.Int32
	revoked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if revoked || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-1" || r.Form.Get("client_id") != "cid" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer srv.Close()
	grant := func(rt string) string {
		raw, _ := json.Marshal(refreshGrant{ClientID: "cid", ClientSecret: "cs", RefreshToken: rt, TokenURL: srv.URL})
		return string(raw)
	}
	t.Setenv("MAIL_GOOGLE_TOKEN", grant("rt-1"))
	a := AccountConfig{Name: "perso", Auth: AuthConfig{OAuthTokenEnv: "MAIL_GOOGLE_TOKEN"}}
	for i := 0; i < 3; i++ {
		if s, err := a.secret(); err != nil || s != "fresh" {
			t.Fatalf("secret: %q %v", s, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d refreshes, want 1", calls.Load())
	}
	revoked = true
	t.Setenv("MAIL_GOOGLE_TOKEN", grant("rt-2"))
	_, err := a.secret()
	if !errors.Is(err, errRevoked) || !strings.Contains(err.Error(), "reconnect") || strings.Contains(err.Error(), "rt-2") {
		t.Fatalf("revoked: %v", err)
	}
	// A refresh grant without token_url is refused, saying so.
	t.Setenv("MAIL_GOOGLE_TOKEN", `{"client_id":"cid","client_secret":"cs","refresh_token":"rt-3"}`)
	if _, err := a.secret(); err == nil || !strings.Contains(err.Error(), "token_url") {
		t.Fatalf("no token_url: %v", err)
	}
	// An access token response is still read.
	t.Setenv("MAIL_GOOGLE_TOKEN", `{"access_token":"at"}`)
	if s, err := a.secret(); err != nil || s != "at" {
		t.Fatalf("access token: %q %v", s, err)
	}
}

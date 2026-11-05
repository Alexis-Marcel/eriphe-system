package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Google's reason for a 403 reaches the user, with what to do: the CalDAV
// API turned off, or Google Calendar not granted. Another refusal, or one
// from another server, stays as it was.
func TestGoogleRefusal(t *testing.T) {
	const disabled = `{"error":{"code":403,"message":"CalDAV API has not been used in project 123 before or it is disabled.",` +
		`"errors":[{"reason":"accessNotConfigured"}],"status":"PERMISSION_DENIED","details":[{"reason":"SERVICE_DISABLED"}]}}`
	const scope = `{"error":{"code":403,"message":"Request had insufficient authentication scopes.",` +
		`"status":"PERMISSION_DENIED","details":[{"reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`
	for _, c := range []struct {
		provider, body, want, not string
	}{
		{"google", disabled, "the CalDAV API is turned off", "403"},
		{"google", scope, "connected without Google Calendar", "403"},
		{"google", `{"error":{"code":403,"message":"Rate limit"}}`, "403", "CalDAV API"},
		{"google", "<html>no</html>", "403", "CalDAV API"},
		{"icloud", disabled, "403", "CalDAV API"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(c.body))
		}))
		t.Setenv("CAL_TOKEN", "s3cret-value")
		a := &AccountConfig{Name: "sam@example.org", Provider: c.provider, URL: srv.URL + "/caldav/v2/", Username: "sam@example.org",
			Auth: AuthConfig{OAuthTokenEnv: "CAL_TOKEN"}}
		if c.provider != "google" {
			a.Auth = AuthConfig{PasswordEnv: "CAL_TOKEN"}
		}
		d, err := newCalDAV(a, srv.Client(), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Calendars(context.Background())
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), c.not) {
			t.Errorf("%s %.40s: %v", c.provider, c.body, err)
		}
		if err != nil && strings.Contains(err.Error(), "s3cret-value") {
			t.Errorf("the secret in the error: %v", err)
		}
	}
}

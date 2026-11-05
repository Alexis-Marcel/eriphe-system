package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Google says in the body of a 403 why it refuses a CalDAV request, and
// go-webdav drops it: "403 Forbidden" alone does not tell the user what to
// do. googleRefusal reads the two refusals that have a fix of the user's:
// the CalDAV API is turned off in the Google Cloud project of their Google
// app (it is not the "Google Calendar API": both exist), or the account
// was connected without Google Calendar.
type googleRefusal struct {
	disabled bool // the CalDAV API is off; else a right not granted
}

func (e *googleRefusal) Error() string {
	if e.disabled {
		return "Google refuses: the CalDAV API is turned off in the Google Cloud project of Eriphe's Google app " +
			"(it is not the Google Calendar API). Turn it on at " +
			"https://console.cloud.google.com/apis/library/caldav.googleapis.com, wait a minute, then try again"
	}
	return "Google refuses: this account was connected without Google Calendar. " +
		"Connect it again in Settings → Tools → Google, with Google Calendar ticked on Google's page"
}

// readGoogleRefusal reads a refused response: the refusal if it is one of
// those, else the response as it came (its body read back).
func readGoogleRefusal(resp *http.Response) (*http.Response, error) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if why := googleWhy(body); why != nil {
		return nil, why
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// googleWhy reads Google's error body ({"error": {"status", "message",
// "errors": [{"reason"}], "details": [{"reason"}]}}).
func googleWhy(body []byte) *googleRefusal {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return nil
	}
	var reasons []string
	for _, r := range e.Error.Errors {
		reasons = append(reasons, r.Reason)
	}
	for _, r := range e.Error.Details {
		reasons = append(reasons, r.Reason)
	}
	has := func(names ...string) bool {
		for _, r := range reasons {
			for _, n := range names {
				if strings.EqualFold(r, n) {
					return true
				}
			}
		}
		return false
	}
	msg := strings.ToLower(e.Error.Message)
	switch {
	case has("SERVICE_DISABLED", "accessNotConfigured") || strings.Contains(msg, "has not been used in project") || strings.Contains(msg, "it is disabled"):
		return &googleRefusal{disabled: true}
	case has("ACCESS_TOKEN_SCOPE_INSUFFICIENT", "insufficientPermissions") || strings.Contains(msg, "insufficient authentication scopes"):
		return &googleRefusal{}
	}
	return nil
}

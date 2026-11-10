package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// "Test the connection" on the Google Drive page, without a consent on
// Google's side: a line per check, each ✓ or ✗.
//
//  1. The workspace's address: one Google takes as a redirect (signInProvider.refuses).
//  2. The client id and secret: a code exchange with a made-up code. Google
//     checks the client first: invalid_client (or unauthorized_client) is a
//     client it does not know or a wrong secret, invalid_grant is a client
//     it knows that sent a bad code, as expected here.
//  3. The redirect URI: not asked (Google says it only on its sign-in
//     pages); the line says what to check in the console.
//  4. Once connected: the stored refresh token for an access token, then
//     Drive's about.get, for the account's address.
//
// The secret and the tokens are never shown nor logged.

// driveAPI is Drive's API; tests replace it.
var driveAPI = "https://www.googleapis.com/drive/v3"

// driveTestCode is the made-up code of the client check.
const driveTestCode = "eriphe-connection-test"

type driveTest struct {
	Lines     []testView
	Reconnect bool // the stored authorization is gone: offer to connect again
	Write     bool // … asking for the scope it had
}

func (w *web) handleDriveTest(rw http.ResponseWriter, r *http.Request, s *session) {
	lang := w.lang(r)
	var v driveTest
	if !w.testLimit.allow(s.ID, w.now()) {
		v.Lines = []testView{{State: "err", Text: tr(lang, "test.ratelimit")}}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 3*testConnTimeout)
		defer cancel()
		v = w.c.testDrive(ctx, lang, w.origin(r))
		states := make([]string, len(v.Lines))
		for i, l := range v.Lines {
			states[i] = l.State
		}
		log.Printf("web: Google Drive test: %s", strings.Join(states, " ")) // never the client nor a token
	}
	w.render(rw, r, s, "settings", "drive-test", page{Data: v})
}

// driveChecker writes the lines of a test.
type driveChecker struct {
	lang string
	hc   *http.Client
	out  driveTest
}

func (d *driveChecker) add(state, key string, args ...any) *testView {
	text := tr(d.lang, key)
	if len(args) > 0 {
		text = fmt.Sprintf(text, args...)
	}
	d.out.Lines = append(d.out.Lines, testView{State: state, Text: text})
	return &d.out.Lines[len(d.out.Lines)-1]
}

// netFail is Google out of reach: what failed, never the request's URL.
func (d *driveChecker) netFail(err error) {
	key := netErrorKey(err)
	if key == "" {
		key = "test.unreachable"
	}
	d.add("err", "drive.test.google").Detail = tr(d.lang, key)
}

// testDrive runs the checks for the workspace at origin.
func (c *core) testDrive(ctx context.Context, lang, origin string) driveTest {
	d := &driveChecker{lang: lang, hc: &http.Client{Transport: testTransport(), Timeout: testConnTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	add, hc := d.add, d.hc

	why := providerGoogle.refuses(origin)
	if why != "" {
		add("err", "drive.origin."+why, origin)
	} else {
		add("ok", "drive.test.origin", origin)
	}
	redirect := origin + "/oauth/callback"

	app, err := googleClient()
	switch {
	case errors.Is(err, errNoGoogleClient):
		add("info", "drive.test.noclient")
	case err != nil:
		add("err", "drive.test.clientunreadable")
	default:
		hide := func(s string) string { return strings.ReplaceAll(s, app.Secret, "••••") }
		form := url.Values{"grant_type": {"authorization_code"}, "code": {driveTestCode}, "redirect_uri": {redirect},
			"client_id": {app.ID}, "client_secret": {app.Secret}}
		code, desc, _, err := providerGoogle.tokenCall(ctx, hc, form)
		switch {
		case err != nil:
			d.netFail(err)
		case code == "invalid_grant":
			add("ok", "drive.test.clientok", app.ID)
		case code == "invalid_client" || code == "unauthorized_client":
			l := add("err", "drive.test.clientbad")
			l.Detail = truncate(hide(desc), 200)
		default:
			add("err", "drive.test.clientother", truncate(hide(strings.TrimSpace(code+" "+desc)), 200))
		}
		if why == "" && code != "invalid_client" && code != "unauthorized_client" {
			// Whether the client lists the redirect URI is not asked: the
			// line says what to check in the console.
			add("info", "drive.test.redirectcheck", redirect)
		}
	}

	st := c.driveState()
	switch {
	case !st.Exists:
		add("info", "drive.test.notconnected")
	case st.Mine:
		d.out.Write = st.CanWrite
		c.testDriveToken(ctx, d)
	}
	return d.out
}

// testDriveToken checks the stored authorization: a fresh access token,
// then the account it is for.
func (c *core) testDriveToken(ctx context.Context, d *driveChecker) {
	add, hc, out, lang := d.add, d.hc, &d.out, d.lang
	srv := c.conf().Servers[filesServer]
	spec, _ := decodeRun(srv.Run)
	raw, err := "", errors.New("no token")
	// The first account's (a Drive of several accounts tests that one;
	// Settings → Tools → Google shows the state of each).
	if fc, ok := filesConfigOf(srv); ok && len(fc.Accounts) > 0 {
		if f := spec.Env[fc.Accounts[0].Auth.TokenEnv].File; f != "" {
			raw, err = readSecret(f)
		}
	}
	var tok struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &tok) != nil || tok.RefreshToken == "" {
		add("err", "drive.test.notoken")
		out.Reconnect = true
		return
	}
	hide := strings.NewReplacer(tok.ClientSecret, "••••", tok.RefreshToken, "••••").Replace
	code, desc, access, err := providerGoogle.tokenCall(ctx, hc, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken},
		"client_id": {tok.ClientID}, "client_secret": {tok.ClientSecret}})
	switch {
	case err != nil:
		d.netFail(err)
		return
	case code == "invalid_grant":
		add("err", "drive.test.expired")
		out.Reconnect = true
		return
	case code == "invalid_client" || code == "unauthorized_client":
		l := add("err", "drive.test.tokenclient")
		l.Detail = truncate(hide(desc), 200)
		out.Reconnect = true
		return
	case code != "" || access == "":
		add("err", "drive.test.clientother", truncate(hide(strings.TrimSpace(code+" "+desc)), 200))
		return
	}
	hideAll := func(s string) string { return strings.ReplaceAll(hide(s), access, "••••") }
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, driveAPI+"/about?fields="+url.QueryEscape("user(emailAddress,displayName),storageQuota"), nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := hc.Do(req)
	if err != nil {
		d.netFail(err)
		return
	}
	defer resp.Body.Close()
	var about struct {
		User struct {
			Email string `json:"emailAddress"`
			Name  string `json:"displayName"`
		} `json:"user"`
		Quota struct {
			Limit string `json:"limit"`
			Usage string `json:"usage"`
		} `json:"storageQuota"`
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
	json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&about)
	if resp.StatusCode != http.StatusOK || about.User.Email == "" {
		disabled := false
		for _, e := range about.Error.Errors {
			disabled = disabled || e.Reason == "accessNotConfigured"
		}
		for _, e := range about.Error.Details {
			disabled = disabled || e.Reason == "SERVICE_DISABLED"
		}
		if disabled {
			add("err", "drive.test.apioff")
			return
		}
		l := add("err", "drive.test.apierr", resp.StatusCode)
		l.Detail = truncate(hideAll(about.Error.Message), 200)
		return
	}
	who := about.User.Email
	if about.User.Name != "" {
		who += " (" + about.User.Name + ")"
	}
	l := add("ok", "drive.test.connected", who)
	usage, uerr := strconv.ParseInt(about.Quota.Usage, 10, 64)
	limit, lerr := strconv.ParseInt(about.Quota.Limit, 10, 64)
	switch {
	case uerr == nil && lerr == nil && limit > 0:
		l.Text += " " + fmt.Sprintf(tr(lang, "drive.test.quota"), byteSize(lang, usage), byteSize(lang, limit))
	case uerr == nil:
		l.Text += " " + fmt.Sprintf(tr(lang, "drive.test.usage"), byteSize(lang, usage))
	}
}

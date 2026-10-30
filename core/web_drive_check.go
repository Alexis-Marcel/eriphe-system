package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

// "Test the connection" on the Google Drive page, without a consent on
// Google's side: a line per check, each ✓ or ✗.
//
//  1. The workspace's address: one Google takes as a redirect (googleOrigin).
//  2. The client id and secret: a code exchange with a made-up code. Google
//     checks the client first: invalid_client (or unauthorized_client) is a
//     client it does not know or a wrong secret, invalid_grant is a client
//     it knows that sent a bad code, as expected here.
//  3. The redirect URI: the authorization URL a Connect would open, asked
//     without following it. Google answers a request it refuses with a
//     redirect to /signin/oauth/error?authError=<base64 protobuf whose first
//     field is the error code>: redirect_uri_mismatch is a redirect URI the
//     client does not list. A request it takes goes on to its sign-in pages,
//     which are never followed. Any other answer is not guessed at: the line
//     says to check the URI in the console.
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

	why := googleOrigin(origin)
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
		code, desc, _, err := googleToken(ctx, hc, form)
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
		if why == "" {
			switch state, gcode := googleAuthorize(ctx, hc, app.ID, redirect); state {
			case "ok":
				add("ok", "drive.test.redirectok", redirect)
			case "mismatch":
				add("err", "drive.test.redirectbad", redirect)
			case "client": // the line above says it
			case "net":
				add("info", "drive.test.redirectcheck", redirect)
			default:
				l := add("info", "drive.test.redirectcheck", redirect)
				l.Detail = truncate(hide(gcode), 100)
			}
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
	spec, _ := decodeRun(c.conf().Servers[filesServer].Run)
	raw, err := "", errors.New("no token")
	if f := spec.Env[driveTokenEnv].File; f != "" {
		raw, err = readSecret(f)
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
	code, desc, access, err := googleToken(ctx, hc, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken},
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

// googleToken posts to Google's token endpoint: the error code and its
// description when Google refuses, else the access token.
func googleToken(ctx context.Context, hc *http.Client, form url.Values) (code, desc, access string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleOAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
		Access      string `json:"access_token"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body) != nil && resp.StatusCode != http.StatusOK {
		return "http " + strconv.Itoa(resp.StatusCode), "", "", nil
	}
	if body.Error == "" && resp.StatusCode != http.StatusOK {
		body.Error = "http " + strconv.Itoa(resp.StatusCode)
	}
	return body.Error, body.Description, body.Access, nil
}

// googleAuthorize asks for the authorization URL a Connect would open, and
// reads where Google sends the browser, never following it: "ok" for its
// sign-in, "mismatch" for a redirect URI the client does not list,
// "client" for a client it does not know, "net" when Google could not be
// asked, else "other" with Google's error code (or the HTTP status).
func googleAuthorize(ctx context.Context, hc *http.Client, clientID, redirect string) (state, code string) {
	cfg := &oauth2.Config{ClientID: clientID, Endpoint: googleOAuth, RedirectURL: redirect, Scopes: []string{scopeDriveReadonly}}
	u := cfg.AuthCodeURL(randomToken(18), oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(oauth2.GenerateVerifier()),
		oauth2.SetAuthURLParam("prompt", "consent"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "net", ""
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "net", ""
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	loc, _ := resp.Location()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400 && loc != nil:
		if e := loc.Query().Get("authError"); e != "" || strings.HasSuffix(loc.Path, "/error") {
			code = authErrorCode(e)
			break
		}
		// On to its sign-in, or straight back to the redirect URI
		// with an error of another kind: the URI is registered.
		return "ok", ""
	case resp.StatusCode == http.StatusBadRequest && strings.Contains(string(raw), "redirect_uri_mismatch"):
		code = "redirect_uri_mismatch"
	default:
		return "other", "http " + strconv.Itoa(resp.StatusCode)
	}
	switch code {
	case "redirect_uri_mismatch":
		return "mismatch", code
	case "invalid_client", "deleted_client", "unauthorized_client":
		return "client", code
	case "":
		return "other", "error"
	}
	return "other", code
}

// authErrorCode reads the error code in Google's authError: base64 of a
// protobuf message whose field 1 is that code.
func authErrorCode(s string) string {
	var raw []byte
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			raw = b
			break
		}
	}
	if len(raw) < 2 || raw[0] != 0x0a { // field 1, length-delimited
		return ""
	}
	n, i := 0, 1
	for shift := 0; i < len(raw) && shift < 28; shift += 7 {
		b := raw[i]
		i++
		n |= int(b&0x7f) << shift
		if b < 0x80 {
			break
		}
	}
	if n <= 0 || n > 64 || i+n > len(raw) {
		return ""
	}
	code := string(raw[i : i+n])
	for _, r := range code {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return code
}

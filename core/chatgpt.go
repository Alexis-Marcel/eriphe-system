package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A ChatGPT subscription (Plus, Pro) is used through Codex, OpenAI's coding
// agent, signed in with ChatGPT. eriphe-core owns that sign-in:
//
//   - It signs in with the device code flow Codex itself uses (`codex login
//     --device-auth`): the workspace shows a code and a link, the user types
//     the code at OpenAI, eriphe-core polls, then exchanges the code for
//     tokens (an ID token, an access token and a refresh token).
//   - The tokens are kept in the vault, as the provider's credential, and
//     refreshed here only, one refresh at a time: a refresh token is single
//     use, so two holders refreshing it would sign the other out.
//   - Each task gets, when its node fetches it, a Codex auth.json with a
//     fresh access token and no refresh token, marked as just refreshed so
//     that Codex does not try to refresh it.
//
// Tokens never appear in logs, task specs as stored, or pages.

// chatgptAuth is OpenAI's sign-in, replaced in tests.
var chatgptAuth = struct {
	Issuer   string
	ClientID string // Codex's public client
	Client   *http.Client
}{"https://auth.openai.com", "app_EMoamEEZ73f0CkXaXp7hrann", &http.Client{Timeout: 20 * time.Second}}

// ERIPHE_CHATGPT_ISSUER points the sign-in at a fake OpenAI, for tests.
func init() {
	if v := os.Getenv("ERIPHE_CHATGPT_ISSUER"); v != "" {
		chatgptAuth.Issuer = v
	}
}

// How long a lent access token must still be valid, and how old a sign-in
// may get before it is refreshed anyway (Codex itself refreshes after 8 days).
const (
	chatgptMinLeft = 2 * time.Hour
	chatgptMaxAge  = 7 * 24 * time.Hour
)

// codexAuthEnv carries the auth.json of a task; the codex runtime writes it
// where Codex reads it and takes it out of the environment.
const codexAuthEnv = "ERIPHE_CODEX_AUTH"

var errSignedOut = errors.New("the ChatGPT sign-in is no longer valid: sign in again in Settings → Agents")

type chatgptTokens struct {
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id,omitempty"`
	LastRefresh  time.Time `json:"last_refresh"`
}

// jwtClaims reads a JWT's claims, unverified: they come from OpenAI over
// TLS, and are only shown or used to time a refresh.
func jwtClaims(token string, v any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

type chatgptIdentity struct {
	Email   string
	Plan    string
	Account string
}

func (t chatgptTokens) identity() chatgptIdentity {
	var c struct {
		Email string `json:"email"`
		Auth  struct {
			Plan    string `json:"chatgpt_plan_type"`
			Account string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	jwtClaims(t.IDToken, &c)
	id := chatgptIdentity{Email: c.Email, Plan: c.Auth.Plan, Account: c.Auth.Account}
	if id.Email == "" {
		id.Email = c.Profile.Email
	}
	if id.Account == "" {
		id.Account = t.AccountID
	}
	return id
}

// expiry is when the access token ends, or zero if it does not say.
func (t chatgptTokens) expiry() time.Time {
	var c struct {
		Exp int64 `json:"exp"`
	}
	if jwtClaims(t.AccessToken, &c) != nil || c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}

// planName is a plan as people know it.
func planName(p string) string {
	switch p {
	case "":
		return ""
	case "plus", "pro", "team", "free", "business", "enterprise", "edu":
		return strings.ToUpper(p[:1]) + p[1:]
	}
	return p
}

// tokenPost posts to OpenAI's sign-in and decodes its JSON answer.
func tokenPost(ctx context.Context, path string, body []byte, form bool, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(chatgptAuth.Issuer, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if form {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := chatgptAuth.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s: HTTP %d %s", path, resp.StatusCode, oauthErrorCode(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// oauthErrorCode is the error code of an OAuth error answer, never more:
// an answer could echo what was sent.
func oauthErrorCode(raw []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	switch v := e.Error.(type) {
	case string:
		return truncate(v, 60)
	case map[string]any:
		if c, ok := v["code"].(string); ok {
			return truncate(c, 60)
		}
	}
	return ""
}

// refreshTokens trades a refresh token for new tokens. The answer's
// refresh token replaces the old one, which is now spent.
func refreshTokens(ctx context.Context, t chatgptTokens) (chatgptTokens, error) {
	body, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "client_id": chatgptAuth.ClientID, "refresh_token": t.RefreshToken})
	var out struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	status, err := tokenPost(ctx, "/oauth/token", body, false, &out)
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		return t, fmt.Errorf("%w (%v)", errSignedOut, err)
	}
	if err != nil {
		return t, err
	}
	if out.AccessToken == "" {
		return t, errors.New("refresh: no access token")
	}
	n := t
	n.AccessToken, n.LastRefresh = out.AccessToken, time.Now().UTC()
	if out.IDToken != "" {
		n.IDToken = out.IDToken
	}
	if out.RefreshToken != "" {
		n.RefreshToken = out.RefreshToken
	}
	return n, nil
}

// chatgptKeeper refreshes the stored sign-ins, one at a time.
type chatgptKeeper struct {
	mu  sync.Mutex
	now func() time.Time
}

var chatgptKeep = &chatgptKeeper{now: time.Now}

// fresh returns the sign-in stored at ref (vault:<name>), refreshed first
// if its access token ends within minLeft or it is old. A rotated refresh
// token is written to the vault before the new access token is used. When
// a refresh fails for a passing reason, a token still valid is returned.
func (k *chatgptKeeper) fresh(ctx context.Context, ref string, minLeft time.Duration) (chatgptTokens, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	name, ok := strings.CutPrefix(ref, vaultPrefix)
	v := currentVault()
	if !ok || v == nil {
		return chatgptTokens{}, errors.New("a ChatGPT sign-in is kept in the vault")
	}
	raw, err := v.get(name)
	if err != nil {
		return chatgptTokens{}, err
	}
	var t chatgptTokens
	if err := json.Unmarshal([]byte(raw), &t); err != nil || t.AccessToken == "" {
		return chatgptTokens{}, errors.New("the stored ChatGPT sign-in is unreadable: sign in again")
	}
	now := k.now()
	exp := t.expiry()
	if (exp.IsZero() || exp.Sub(now) > minLeft) && now.Sub(t.LastRefresh) < chatgptMaxAge {
		return t, nil
	}
	if t.RefreshToken == "" {
		return t, errSignedOut
	}
	n, err := refreshTokens(ctx, t)
	if err != nil {
		if !errors.Is(err, errSignedOut) && (exp.IsZero() || exp.After(now.Add(10*time.Minute))) {
			log.Printf("chatgpt: refresh of %s failed, its token is still valid: %v", name, err)
			return t, nil
		}
		log.Printf("chatgpt: refresh of %s failed: %v", name, err)
		return t, err
	}
	stored, _ := json.Marshal(n)
	if err := v.put(name, string(stored)); err != nil {
		return t, fmt.Errorf("keeping the refreshed sign-in: %w", err)
	}
	log.Printf("chatgpt: sign-in %s refreshed", name)
	return n, nil
}

// codexAuthFile is the auth.json lent to one task: the access token, no
// refresh token, and a last refresh of now, so Codex does not refresh.
func codexAuthFile(t chatgptTokens, now time.Time) string {
	account := t.AccountID
	if account == "" {
		account = t.identity().Account
	}
	f := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{"id_token": t.IDToken, "access_token": t.AccessToken, "refresh_token": "",
			"account_id": account},
		"last_refresh": now.UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(f)
	return string(raw)
}

// lendChatGPT is the environment of a task whose agent uses a ChatGPT
// subscription.
func lendChatGPT(ref string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t, err := chatgptKeep.fresh(ctx, ref, chatgptMinLeft)
	if err != nil {
		return nil, err
	}
	return map[string]string{codexAuthEnv: codexAuthFile(t, chatgptKeep.now())}, nil
}

// The device code sign-in, started from the workspace.

const (
	loginPending  = "pending"
	loginDone     = "done"
	loginExpired  = "expired"
	loginDisabled = "disabled"
	loginDenied   = "denied"
	loginFailed   = "failed"
	loginUnknown  = "unknown"
	loginBusy     = "busy"

	loginTTL = 15 * time.Minute // OpenAI's code lasts as long
)

// loginPollUnit is the unit of OpenAI's polling interval, shorter in tests.
var loginPollUnit = time.Second

type chatgptLogin struct {
	id, session string
	code, url   string
	deviceID    string
	interval    time.Duration
	expires     time.Time

	mu     sync.Mutex
	state  string
	detail string
	tokens *chatgptTokens
	cancel context.CancelFunc
	ended  chan struct{} // closed when its poll returns
}

// loginView is what the workspace shows of a sign-in.
type loginView struct {
	ID, State string
	URL, Code string
	Minutes   int
	Who, Plan string
	Detail    string
}

type loginManager struct {
	mu     sync.Mutex
	logins map[string]*chatgptLogin
	max    int
}

var chatgptLogins = &loginManager{logins: map[string]*chatgptLogin{}, max: 16}

// start asks OpenAI for a code and polls for the sign-in in the background.
func (m *loginManager) start(session string) *loginView {
	// One sign-in per session: a new one replaces the session's last.
	m.mu.Lock()
	now := time.Now()
	active := 0
	var gone []*chatgptLogin
	for id, l := range m.logins {
		if l.session == session || now.After(l.expires.Add(time.Minute)) {
			gone = append(gone, l)
			delete(m.logins, id)
			continue
		}
		active++
	}
	m.mu.Unlock()
	for _, l := range gone {
		l.cancel()
		<-l.ended
	}
	if active >= m.max {
		return &loginView{State: loginBusy}
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTTL)
	body, _ := json.Marshal(map[string]string{"client_id": chatgptAuth.ClientID})
	var uc struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		UserCodeAlt  string          `json:"usercode"`
		Interval     json.RawMessage `json:"interval"`
	}
	status, err := tokenPost(ctx, "/api/accounts/deviceauth/usercode", body, false, &uc)
	if err != nil {
		cancel()
		if status == http.StatusNotFound {
			return &loginView{State: loginDisabled}
		}
		log.Printf("chatgpt: sign-in not started: %v", err)
		return &loginView{State: loginFailed, Detail: truncate(err.Error(), 120)}
	}
	if uc.UserCode == "" {
		uc.UserCode = uc.UserCodeAlt
	}
	interval := 5 * time.Second
	if s, err := strconv.Unquote(string(uc.Interval)); err == nil {
		uc.Interval = json.RawMessage(s)
	}
	if n, err := strconv.Atoi(string(uc.Interval)); err == nil && n > 0 {
		interval = time.Duration(n) * loginPollUnit
	}
	b := make([]byte, 12)
	rand.Read(b)
	l := &chatgptLogin{id: hex.EncodeToString(b), session: session, code: uc.UserCode, deviceID: uc.DeviceAuthID,
		url: strings.TrimRight(chatgptAuth.Issuer, "/") + "/codex/device", interval: interval, expires: now.Add(loginTTL),
		state: loginPending, cancel: cancel, ended: make(chan struct{})}
	m.mu.Lock()
	m.logins[l.id] = l
	m.mu.Unlock()
	go l.poll(ctx)
	log.Printf("chatgpt: sign-in %s started", l.id[:6])
	return l.view()
}

func (l *chatgptLogin) finish(state, detail string, t *chatgptTokens) {
	l.mu.Lock()
	if l.state == loginPending {
		l.state, l.detail, l.tokens = state, detail, t
	}
	l.mu.Unlock()
	log.Printf("chatgpt: sign-in %s: %s", l.id[:6], state)
}

// poll waits for the user to type the code, then exchanges it.
func (l *chatgptLogin) poll(ctx context.Context) {
	defer close(l.ended)
	body, _ := json.Marshal(map[string]string{"device_auth_id": l.deviceID, "user_code": l.code})
	for {
		select {
		case <-ctx.Done():
			l.finish(loginExpired, "", nil)
			return
		case <-time.After(l.interval):
		}
		var got struct {
			AuthorizationCode string `json:"authorization_code"`
			CodeVerifier      string `json:"code_verifier"`
		}
		status, err := tokenPost(ctx, "/api/accounts/deviceauth/token", body, false, &got)
		if status == http.StatusForbidden || status == http.StatusNotFound {
			continue // not yet
		}
		if err != nil {
			if ctx.Err() != nil {
				l.finish(loginExpired, "", nil)
				return
			}
			if status == 0 {
				continue // a network hiccup: try again
			}
			l.finish(loginDenied, truncate(err.Error(), 120), nil)
			return
		}
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {chatgptAuth.ClientID}, "code": {got.AuthorizationCode},
			"redirect_uri": {strings.TrimRight(chatgptAuth.Issuer, "/") + "/deviceauth/callback"}, "code_verifier": {got.CodeVerifier}}
		var tok chatgptTokens
		if _, err := tokenPost(ctx, "/oauth/token", []byte(form.Encode()), true, &tok); err != nil || tok.AccessToken == "" || tok.RefreshToken == "" {
			l.finish(loginFailed, "", nil)
			return
		}
		tok.LastRefresh = time.Now().UTC()
		tok.AccountID = tok.identity().Account
		l.finish(loginDone, "", &tok)
		return
	}
}

func (l *chatgptLogin) view() *loginView {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := &loginView{ID: l.id, State: l.state, URL: l.url, Code: l.code, Detail: l.detail,
		Minutes: max(1, int(time.Until(l.expires).Minutes()+.5))}
	if l.tokens != nil {
		id := l.tokens.identity()
		v.Who, v.Plan = id.Email, planName(id.Plan)
	}
	return v
}

func (m *loginManager) get(id, session string) *chatgptLogin {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.logins[id]
	if l == nil || l.session != session {
		return nil
	}
	return l
}

// status is a sign-in as its session sees it.
func (m *loginManager) status(id, session string) *loginView {
	if id == "" {
		return nil
	}
	l := m.get(id, session)
	if l == nil {
		return &loginView{State: loginUnknown}
	}
	return l.view()
}

// tokens are a finished sign-in's, as the vault keeps them.
func (m *loginManager) tokens(id, session string) (string, bool) {
	l := m.get(id, session)
	if l == nil {
		return "", false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tokens == nil {
		return "", false
	}
	raw, _ := json.Marshal(l.tokens)
	return string(raw), true
}

// drop forgets a sign-in, once its tokens are kept or it is abandoned.
func (m *loginManager) drop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.logins[id]
	delete(m.logins, id)
	m.mu.Unlock()
	if l != nil {
		l.cancel()
		<-l.ended
	}
	m.mu.Lock()
}

// dropAll forgets every sign-in, and waits for their polls to end.
func (m *loginManager) dropAll() {
	m.mu.Lock()
	all := m.logins
	m.logins = map[string]*chatgptLogin{}
	m.mu.Unlock()
	for _, l := range all {
		l.cancel()
		<-l.ended
	}
}

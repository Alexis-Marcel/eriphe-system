package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeJWT makes an unsigned JWT with these claims.
func fakeJWT(claims map[string]any) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	raw, _ := json.Marshal(claims)
	return h + "." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
}

// fakeOpenAI is OpenAI's sign-in as Codex uses it: device codes, the code
// exchange and refresh tokens that rotate and are single use.
type fakeOpenAI struct {
	*httptest.Server
	mu        sync.Mutex
	approved  bool          // the user typed the code
	disabled  bool          // device code sign-in is off
	refresh   string        // the one valid refresh token
	refreshes int           // how many refreshes
	gen       int           // tokens issued
	lifetime  time.Duration // of an access token
	seen      []string      // every request body, to check what is sent
}

func newFakeOpenAI(t *testing.T) *fakeOpenAI {
	f := &fakeOpenAI{lifetime: time.Hour}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	old, unit := chatgptAuth, loginPollUnit
	chatgptAuth.Issuer, chatgptAuth.Client, loginPollUnit = f.URL, f.Client(), 10*time.Millisecond
	t.Cleanup(func() {
		chatgptLogins.dropAll()
		chatgptAuth, loginPollUnit = old, unit
	})
	return f
}

func (f *fakeOpenAI) tokens() map[string]any {
	f.gen++
	f.refresh = fmt.Sprintf("rt-%d", f.gen)
	return map[string]any{
		"id_token": fakeJWT(map[string]any{"email": "sam@example.com",
			"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "plus", "chatgpt_account_id": "acct-1"}}),
		"access_token":  fakeJWT(map[string]any{"exp": time.Now().Add(f.lifetime).Unix(), "n": f.gen}),
		"refresh_token": f.refresh,
	}
}

func (f *fakeOpenAI) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, string(raw))
	switch r.URL.Path {
	case "/api/accounts/deviceauth/usercode":
		if f.disabled {
			http.NotFound(w, r)
			return
		}
		var in map[string]string
		json.Unmarshal(raw, &in)
		if in["client_id"] != chatgptAuth.ClientID {
			http.Error(w, "bad client", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_auth_id": "dev-1", "user_code": "ABCD-1234", "interval": "1"})
	case "/api/accounts/deviceauth/token":
		if !f.approved {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"authorization_code": "code-1", "code_challenge": "ch", "code_verifier": "ver"})
	case "/oauth/token":
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			q, _ := url.ParseQuery(string(raw))
			if q.Get("grant_type") != "authorization_code" || q.Get("code") != "code-1" || q.Get("code_verifier") != "ver" ||
				q.Get("redirect_uri") != f.URL+"/deviceauth/callback" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			writeJSON(w, http.StatusOK, f.tokens())
			return
		}
		var in map[string]string
		json.Unmarshal(raw, &in)
		if in["grant_type"] != "refresh_token" || in["refresh_token"] != f.refresh {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"code":"refresh_token_reused"}}`))
			return
		}
		f.refreshes++
		writeJSON(w, http.StatusOK, f.tokens())
	default:
		http.NotFound(w, r)
	}
}

func waitLogin(t *testing.T, id, session, want string) *loginView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := chatgptLogins.status(id, session)
		if v.State == want {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("sign-in %s, want %s", v.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestChatGPTLoginAndLend(t *testing.T) {
	f := newFakeOpenAI(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	cc := newDynamicCore(t, nil)

	v := chatgptLogins.start("s1")
	if v.State != loginPending || v.Code != "ABCD-1234" || v.URL != f.URL+"/codex/device" {
		t.Fatalf("start = %+v", v)
	}
	if chatgptLogins.status(v.ID, "another session").State != loginUnknown {
		t.Fatal("another session sees the sign-in")
	}
	f.mu.Lock()
	f.approved = true
	f.mu.Unlock()
	done := waitLogin(t, v.ID, "s1", loginDone)
	if done.Who != "sam@example.com" || done.Plan != "Plus" {
		t.Fatalf("done = %+v", done)
	}
	tok, ok := chatgptLogins.tokens(v.ID, "s1")
	if !ok {
		t.Fatal("no tokens")
	}
	k, _ := kindOf("chatgpt-subscription")
	agents := map[string]Agent{"gpt": presetAgent("fr", k, "chatgpt", k.Presets[0])}
	if err := cc.putProviderAgents(putCreate, "chatgpt", Provider{Kind: "chatgpt-subscription"}, tok, agents, ""); err != nil {
		t.Fatal(err)
	}
	chatgptLogins.drop(v.ID)

	// Lent: a fresh access token, no refresh token, the stored one intact.
	env, err := cc.conf().lendAgent(cc.conf().Agents["gpt"])
	if err != nil {
		t.Fatal(err)
	}
	var auth struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
		LastRefresh time.Time `json:"last_refresh"`
	}
	if err := json.Unmarshal([]byte(env[codexAuthEnv]), &auth); err != nil {
		t.Fatal(err)
	}
	if auth.AuthMode != "chatgpt" || auth.Tokens.AccessToken == "" || auth.Tokens.RefreshToken != "" || time.Since(auth.LastRefresh) > time.Minute {
		t.Fatalf("auth.json = %+v", auth)
	}
	if !strings.Contains(env[codexAuthEnv], `"account_id":"acct-1"`) {
		t.Fatalf("no account: %s", env[codexAuthEnv])
	}
	// A token lasting an hour is refreshed before each lend (less than 2 h
	// left); in parallel, one refresh at a time, never with a spent token.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cc.conf().lendAgent(cc.conf().Agents["gpt"]); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	f.mu.Lock()
	refreshes := f.refreshes
	f.lifetime = 24 * time.Hour
	f.mu.Unlock()
	if refreshes < 1 {
		t.Fatal("never refreshed")
	}
	// A long-lived token is lent as is.
	cc.conf().lendAgent(cc.conf().Agents["gpt"])
	f.mu.Lock()
	before := f.refreshes
	f.mu.Unlock()
	cc.conf().lendAgent(cc.conf().Agents["gpt"])
	f.mu.Lock()
	if f.refreshes != before {
		t.Fatal("refreshed a token with a day left")
	}
	f.mu.Unlock()

	// The stored sign-in is the last one issued.
	stored, _ := currentVault().get("provider.chatgpt.key")
	var st chatgptTokens
	json.Unmarshal([]byte(stored), &st)
	f.mu.Lock()
	if st.RefreshToken != f.refresh {
		t.Fatalf("stored %q, valid %q", st.RefreshToken, f.refresh)
	}
	f.mu.Unlock()

	// Signed out elsewhere: the refresh token is refused.
	f.mu.Lock()
	f.refresh, f.lifetime = "revoked", time.Hour
	f.mu.Unlock()
	expired := st
	expired.AccessToken = fakeJWT(map[string]any{"exp": time.Now().Add(-time.Minute).Unix()})
	raw, _ := json.Marshal(expired)
	currentVault().put("provider.chatgpt.key", string(raw))
	if _, err := cc.conf().lendAgent(cc.conf().Agents["gpt"]); err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Fatalf("lend after sign-out: %v", err)
	}

	for _, secret := range []string{"rt-", "ABCD-1234", "dev-1", "code-1", st.AccessToken[len(st.AccessToken)-20:]} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log shows %q: %s", secret, logs.String())
		}
	}
}

func TestChatGPTLoginStates(t *testing.T) {
	f := newFakeOpenAI(t)
	f.disabled = true
	if v := chatgptLogins.start("s"); v.State != loginDisabled {
		t.Fatalf("disabled: %+v", v)
	}
	f.disabled = false
	if v := chatgptLogins.status("nope", "s"); v.State != loginUnknown {
		t.Fatalf("unknown: %+v", v)
	}
	m := &loginManager{logins: map[string]*chatgptLogin{}, max: 1}
	t.Cleanup(m.dropAll)
	a := m.start("s")
	if b := m.start("t"); b.State != loginBusy {
		t.Fatalf("another session's sign-in: %+v", b)
	}
	// The same session starting again replaces its sign-in.
	b := m.start("s")
	if b.State != loginPending || m.status(a.ID, "s").State != loginUnknown {
		t.Fatalf("again: %+v", b)
	}
	m.drop(b.ID)
	if c := m.start("t"); c.State != loginPending {
		t.Fatalf("after drop: %+v", c)
	}
}

func TestChatGPTNeedsTheVault(t *testing.T) {
	cfg := &Config{Providers: map[string]Provider{"c": {Kind: "chatgpt-subscription", Secret: "/etc/token"}},
		Agents: map[string]Agent{"gpt": {Runtime: "codex", Provider: "c", Model: "gpt-6.1-sol", Use: "x"}}, DefaultAgent: "gpt"}
	if err := cfg.checkAgents(); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("file secret: %v", err)
	}
	cfg.Providers["c"] = Provider{Kind: "chatgpt-subscription", Secret: "vault:c"}
	if err := cfg.checkAgents(); err != nil {
		t.Fatal(err)
	}
	cfg.Agents["gpt"] = Agent{Runtime: "claude-code", Provider: "c", Use: "x"}
	if err := cfg.checkAgents(); err == nil {
		t.Fatal("claude-code with a ChatGPT subscription")
	}
}

func TestPolicyDeniesCodexSecrets(t *testing.T) {
	p := &Policy{}
	for _, cmd := range []string{"cat /tmp/eriphe-codex/auth.json", "echo $ERIPHE_CODEX_AUTH", "ls $CODEX_HOME", "cat ~/.codex/auth.json", "cat /profile/codex/auth.json"} {
		if d, why := p.decide("Bash", map[string]any{"command": cmd}); d != decideDeny {
			t.Errorf("%s: %s %s", cmd, d, why)
		}
	}
}

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// The workspace has one account, the owner's. Its password is created once,
// with a setup code printed in eriphe-core's log (whoever reaches the page
// first cannot take the house), and kept as an argon2id hash. A login opens
// a session: a random cookie (only its hash is stored), HttpOnly, Secure and
// SameSite=Strict, with its own CSRF token that every POST carries.

const webAuthSchema = `CREATE TABLE IF NOT EXISTS web_owner (
  id INTEGER PRIMARY KEY CHECK (id = 1), hash TEXT NOT NULL, updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS web_sessions (
  id TEXT PRIMARY KEY, csrf TEXT NOT NULL, created INTEGER NOT NULL,
  seen INTEGER NOT NULL, expires INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, webAuthSchema) }

const (
	sessionCookie = "__Host-eriphe"
	preCookie     = "__Host-eriphe-pre" // the CSRF token of the login and setup forms
	minPassword   = 10
)

// argon2id parameters (RFC 9106's second recommendation); tests lower them.
var argonTime, argonMemory, argonThreads uint32 = 3, 64 * 1024, 2

// argonSlots caps the argon2 hashes computed at once: each takes
// argonMemory KiB, and a burst of logins must not exhaust the memory.
var argonSlots = make(chan struct{}, 2)

// argonKey is argon2.IDKey; tests watch it.
var argonKey = argon2.IDKey

func idKey(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	return argonKey(pw, salt, t, m, p, n)
}

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key := idKey([]byte(pw), salt, argonTime, argonMemory, uint8(argonThreads), 32)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func checkPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	if m > 256*1024 || t > 16 || p == 0 {
		return false // not a hash this code makes: refuse rather than spend the memory
	}
	got := idKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// setupCode is short enough to type from a log: 8 letters and digits
// without the ambiguous ones.
func setupCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

func normalizeCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}

func (w *web) hasOwner() bool {
	var n int
	w.c.store.db.QueryRow(`SELECT COUNT(*) FROM web_owner`).Scan(&n)
	return n == 1
}

func (w *web) ownerHash() string {
	var h string
	w.c.store.db.QueryRow(`SELECT hash FROM web_owner WHERE id = 1`).Scan(&h)
	return h
}

func (w *web) setPassword(pw string) error {
	_, err := w.c.store.db.Exec(`INSERT INTO web_owner (id, hash, updated) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET hash = excluded.hash, updated = excluded.updated`, hashPassword(pw), w.now().Unix())
	return err
}

type session struct {
	ID   string // the hash of the cookie
	CSRF string
}

func (w *web) newSession(rw http.ResponseWriter) (*session, error) {
	token := randomToken(32)
	s := &session{ID: hashToken(token), CSRF: randomToken(24)}
	now := w.now()
	expires := now.Add(w.sessions)
	w.c.store.db.Exec(`DELETE FROM web_sessions WHERE expires < ? OR seen < ?`, now.Unix(), now.Add(-w.idle).Unix())
	if _, err := w.c.store.db.Exec(`INSERT INTO web_sessions (id, csrf, created, seen, expires) VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.CSRF, now.Unix(), now.Unix(), expires.Unix()); err != nil {
		return nil, err
	}
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	return s, nil
}

// session returns the request's valid session, or nil. A session ends when
// it expires, or after a week without use.
func (w *web) session(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	s := &session{ID: hashToken(c.Value)}
	var seen, expires int64
	err = w.c.store.db.QueryRow(`SELECT csrf, seen, expires FROM web_sessions WHERE id = ?`, s.ID).Scan(&s.CSRF, &seen, &expires)
	if err != nil {
		return nil
	}
	now := w.now()
	if now.Unix() >= expires || now.Sub(time.Unix(seen, 0)) > w.idle {
		w.c.store.db.Exec(`DELETE FROM web_sessions WHERE id = ?`, s.ID)
		return nil
	}
	if now.Unix()-seen > 60 {
		w.c.store.db.Exec(`UPDATE web_sessions SET seen = ? WHERE id = ?`, now.Unix(), s.ID)
	}
	return s
}

func (w *web) endSession(rw http.ResponseWriter, s *session) {
	w.c.store.db.Exec(`DELETE FROM web_sessions WHERE id = ?`, s.ID)
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// redirect sends the browser elsewhere, also from an htmx request.
func redirect(rw http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") != "" {
		rw.Header().Set("HX-Redirect", to)
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(rw, r, to, http.StatusSeeOther)
}

// sameOrigin refuses a request a browser sent from another site.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

type authed func(http.ResponseWriter, *http.Request, *session)

// auth lets only the owner's session in, and checks the CSRF token of
// everything but GET.
func (w *web) auth(h authed) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !w.hasOwner() {
			redirect(rw, r, "/setup")
			return
		}
		s := w.session(r)
		if s == nil {
			if r.URL.Path == "/events" {
				http.Error(rw, "unauthorized", http.StatusUnauthorized)
				return
			}
			redirect(rw, r, "/login")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get("X-CSRF-Token")
			if got == "" {
				got = r.PostFormValue("csrf")
			}
			if !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(got), []byte(s.CSRF)) != 1 {
				http.Error(rw, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		h(rw, r, s)
	})
}

// preToken gives the login and setup forms a CSRF token, as a cookie the
// form repeats (double submit).
func (w *web) preToken(rw http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(preCookie); err == nil && len(c.Value) >= 16 {
		return c.Value
	}
	v := randomToken(24)
	http.SetCookie(rw, &http.Cookie{Name: preCookie, Value: v, Path: "/", MaxAge: 3600,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	return v
}

func checkPreToken(r *http.Request) bool {
	c, err := r.Cookie(preCookie)
	got := r.PostFormValue("csrf")
	return err == nil && got != "" && sameOrigin(r) && subtle.ConstantTimeCompare([]byte(c.Value), []byte(got)) == 1
}

type authForm struct {
	Error string
	Setup bool
}

func (w *web) handleSetupPage(rw http.ResponseWriter, r *http.Request) {
	if w.hasOwner() {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	}
	w.render(rw, r, &session{CSRF: w.preToken(rw, r)}, "login", "", page{Title: "setup.title", Data: authForm{Setup: true}})
}

func (w *web) handleSetup(rw http.ResponseWriter, r *http.Request) {
	if w.hasOwner() {
		http.Redirect(rw, r, "/login", http.StatusSeeOther)
		return
	}
	csrf := w.preToken(rw, r)
	fail := func(status int, msg string) {
		rw.WriteHeader(status)
		w.render(rw, r, &session{CSRF: csrf}, "login", "", page{Title: "setup.title", Data: authForm{Setup: true, Error: msg}})
	}
	if !checkPreToken(r) {
		fail(http.StatusForbidden, "err.csrf")
		return
	}
	code, pw := r.PostFormValue("code"), r.PostFormValue("password")
	switch ok, limited := w.guarded(r, func() bool {
		return subtle.ConstantTimeCompare([]byte(normalizeCode(code)), []byte(normalizeCode(w.setup))) == 1
	}); {
	case limited:
		fail(http.StatusTooManyRequests, "err.toomany")
		return
	case !ok:
		fail(http.StatusUnauthorized, "err.setupcode")
		return
	}
	if len([]rune(pw)) < minPassword {
		fail(http.StatusBadRequest, "err.short")
		return
	}
	if pw != r.PostFormValue("confirm") {
		fail(http.StatusBadRequest, "err.mismatch")
		return
	}
	if err := w.setPassword(pw); err != nil {
		fail(http.StatusInternalServerError, "err.internal")
		return
	}
	w.setup = ""
	if _, err := w.newSession(rw); err != nil {
		fail(http.StatusInternalServerError, "err.internal")
		return
	}
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (w *web) handleLoginPage(rw http.ResponseWriter, r *http.Request) {
	if !w.hasOwner() {
		http.Redirect(rw, r, "/setup", http.StatusSeeOther)
		return
	}
	if w.session(r) != nil {
		http.Redirect(rw, r, "/", http.StatusSeeOther)
		return
	}
	w.render(rw, r, &session{CSRF: w.preToken(rw, r)}, "login", "", page{Title: "login.title", Data: authForm{}})
}

func (w *web) handleLogin(rw http.ResponseWriter, r *http.Request) {
	if !w.hasOwner() {
		http.Redirect(rw, r, "/setup", http.StatusSeeOther)
		return
	}
	csrf := w.preToken(rw, r)
	fail := func(status int, msg string) {
		rw.WriteHeader(status)
		w.render(rw, r, &session{CSRF: csrf}, "login", "", page{Title: "login.title", Data: authForm{Error: msg}})
	}
	if !checkPreToken(r) {
		fail(http.StatusForbidden, "err.csrf")
		return
	}
	switch ok, limited := w.guarded(r, func() bool { return checkPassword(w.ownerHash(), r.PostFormValue("password")) }); {
	case limited:
		fail(http.StatusTooManyRequests, "err.toomany")
		return
	case !ok:
		fail(http.StatusUnauthorized, "err.password")
		return
	}
	if _, err := w.newSession(rw); err != nil {
		fail(http.StatusInternalServerError, "err.internal")
		return
	}
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (w *web) handleLogout(rw http.ResponseWriter, r *http.Request, s *session) {
	w.endSession(rw, s)
	if r.Header.Get("HX-Request") != "" {
		rw.Header().Set("HX-Redirect", "/login")
		return
	}
	http.Redirect(rw, r, "/login", http.StatusSeeOther)
}

// handlePassword changes the password, and ends every other session.
func (w *web) handlePassword(rw http.ResponseWriter, r *http.Request, s *session) {
	msg := func(key string, ok bool) {
		w.render(rw, r, s, "settings", "password-result", page{Data: map[string]any{"Msg": key, "OK": ok}})
	}
	switch ok, limited := w.guarded(r, func() bool { return checkPassword(w.ownerHash(), r.PostFormValue("current")) }); {
	case limited:
		msg("err.toomany", false)
		return
	case !ok:
		msg("err.password", false)
		return
	}
	pw := r.PostFormValue("password")
	switch {
	case len([]rune(pw)) < minPassword:
		msg("err.short", false)
		return
	case pw != r.PostFormValue("confirm"):
		msg("err.mismatch", false)
		return
	}
	if err := w.setPassword(pw); err != nil {
		msg("err.internal", false)
		return
	}
	w.c.store.db.Exec(`DELETE FROM web_sessions WHERE id != ?`, s.ID)
	msg("account.changed", true)
}

// guarded runs a password or setup code check under the login limiter:
// limited when the address is over its limit, slowed down when many
// attempts have failed lately, whatever their address.
func (w *web) guarded(r *http.Request, check func() bool) (ok, limited bool) {
	ip := clientIP(r)
	if !w.limiter.begin(ip, w.now()) {
		return false, true
	}
	if w.limiter.wait(r.Context(), w.now()) == nil {
		ok = check()
	}
	w.limiter.end(ip, w.now(), ok)
	return ok, false
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLimiter slows down password guessing without ever locking the owner
// out. Per address (an IPv6 /64 counts as one): 5 failures in 15 minutes
// refuse that address for the rest of the window; attempts in flight count,
// so parallel requests cannot go past the limit. Globally: past 20 failures
// in the window, from all addresses, every attempt waits longer (up to a few
// seconds), but is still checked: an owner on another address gets in.
type loginLimiter struct {
	mu       sync.Mutex
	byIP     map[string][]time.Time
	inflight map[string]int
	all      []time.Time
	window   time.Duration
	perIP    int
	slowFrom int           // global failures in the window before attempts slow down
	step     time.Duration // added wait per failure past slowFrom
	maxWait  time.Duration
	maxAll   int // failures remembered globally (the wait is capped anyway)
	maxIPs   int // addresses remembered before pruning
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{byIP: map[string][]time.Time{}, inflight: map[string]int{}, window: 15 * time.Minute,
		perIP: 5, slowFrom: 20, step: 250 * time.Millisecond, maxWait: 5 * time.Second, maxAll: 10000, maxIPs: 10000}
}

// limitKey is the address an attempt counts against: the IP, or its /64.
func limitKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Unmap().Is4() {
		return ip
	}
	p, _ := a.Prefix(64)
	return p.String()
}

func (l *loginLimiter) recent(ts []time.Time, now time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < l.window {
			out = append(out, t)
		}
	}
	return out
}

// begin starts an attempt from ip, or refuses it if that address is over
// its limit. Every begin is followed by end.
func (l *loginLimiter) begin(ip string, now time.Time) bool {
	key := limitKey(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byIP[key] = l.recent(l.byIP[key], now)
	if len(l.byIP[key]) == 0 {
		delete(l.byIP, key)
	}
	if len(l.byIP[key])+l.inflight[key] >= l.perIP {
		return false
	}
	l.inflight[key]++
	return true
}

// end ends an attempt: a success forgets the address's failures.
func (l *loginLimiter) end(ip string, now time.Time, ok bool) {
	key := limitKey(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[key]--; l.inflight[key] <= 0 {
		delete(l.inflight, key)
	}
	if ok {
		delete(l.byIP, key)
		return
	}
	if len(l.byIP) >= l.maxIPs {
		for k, ts := range l.byIP {
			if l.byIP[k] = l.recent(ts, now); len(l.byIP[k]) == 0 {
				delete(l.byIP, k)
			}
		}
	}
	l.byIP[key] = append(l.byIP[key], now)
	l.all = append(l.recent(l.all, now), now)
	if len(l.all) > l.maxAll {
		l.all = l.all[len(l.all)-l.maxAll:]
	}
}

// delay is how long an attempt waits first: nothing until the global
// failures pass slowFrom, then longer with each one, up to maxWait.
func (l *loginLimiter) delay(now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = l.recent(l.all, now)
	over := len(l.all) - l.slowFrom
	if over < 0 {
		return 0
	}
	return min(time.Duration(over+1)*l.step, l.maxWait)
}

// wait waits for the global slowdown, or until the request is gone.
func (l *loginLimiter) wait(ctx context.Context, now time.Time) error {
	d := l.delay(now)
	if d == 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

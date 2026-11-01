package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVAPIDKeysKept(t *testing.T) {
	file := filepath.Join(t.TempDir(), pushKeyFile)
	priv, pub, err := vapidKeys(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi.Mode(), err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(pub)
	if len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("public key: %d bytes", len(raw))
	}
	priv2, pub2, err := vapidKeys(file)
	if err != nil || priv2 != priv || pub2 != pub {
		t.Fatalf("not the same keys at the second start: %v", err)
	}
	os.WriteFile(file, []byte("not a key"), 0o600)
	if _, _, err := vapidKeys(file); err == nil {
		t.Fatal("a broken key file was accepted")
	}
}

// browser is a push subscriber: its keys, as a browser makes them, and
// what its push service received, decrypted.
type browser struct {
	t      *testing.T
	key    *ecdh.PrivateKey
	auth   []byte
	srv    *httptest.Server
	status int // what the push service answers

	mu    sync.Mutex
	got   []pushPayload
	heads []http.Header
}

func newBrowser(t *testing.T, vapidPub string) *browser {
	b := &browser{t: t, auth: make([]byte, 16), status: http.StatusCreated}
	b.key, _ = ecdh.P256().GenerateKey(rand.Reader)
	rand.Read(b.auth)
	b.srv = httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		defer b.mu.Unlock()
		b.heads = append(b.heads, r.Header.Clone())
		checkVAPID(t, r, vapidPub, "https://"+r.Host)
		var p pushPayload
		if err := json.Unmarshal(decryptPush(t, b.key, b.auth, body), &p); err != nil {
			t.Errorf("payload: %v", err)
		}
		b.got = append(b.got, p)
		rw.WriteHeader(b.status)
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *browser) sub(path string) pushSub {
	return pushSub{Endpoint: b.srv.URL + path, P256dh: base64.RawURLEncoding.EncodeToString(b.key.PublicKey().Bytes()),
		Auth: base64.RawURLEncoding.EncodeToString(b.auth), Lang: "fr", Label: "Android · Chrome"}
}

func (b *browser) received() []pushPayload {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]pushPayload(nil), b.got...)
}

// decryptPush opens an aes128gcm push message (RFC 8188, RFC 8291) as the
// browser does.
func decryptPush(t *testing.T, key *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("push body of %d bytes", len(body))
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	asPub, sealed := body[21:21+idlen], body[21+idlen:]
	if rs < 18 || len(sealed) > int(rs) {
		t.Fatalf("record size %d for %d bytes", rs, len(sealed))
	}
	server, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := key.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), key.PublicKey().Bytes()...), asPub...)
	ikm, _ := hkdf.Key(sha256.New, secret, auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		t.Fatalf("cannot decrypt: %v", err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		t.Fatal("no last-record delimiter")
	}
	return plain[:len(plain)-1]
}

// checkVAPID verifies the request's VAPID signature (RFC 8292) with our public key.
func checkVAPID(t *testing.T, r *http.Request, pub, aud string) {
	h := r.Header.Get("Authorization")
	tok, k, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	if !strings.HasPrefix(h, "vapid t=") || !ok || k != pub {
		t.Errorf("Authorization %q", h)
		return
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Errorf("JWT %q", tok)
		return
	}
	var claims struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(raw, &claims)
	if claims.Aud != aud || claims.Sub != pushContact || claims.Exp < time.Now().Unix() {
		t.Errorf("claims %+v", claims)
	}
	point, _ := base64.RawURLEncoding.DecodeString(pub)
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		t.Error(err)
		return
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the VAPID signature does not verify")
	}
}

// pushCore is a core with a pusher whose HTTP client trusts the fake push service.
func pushCore(t *testing.T) (*core, *browser) {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{User: "Sam"}, s)
	if c.push, err = newPusher(s); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, c.push.pub)
	c.push.client = b.srv.Client()
	return c, b
}

func TestPushEncryptedAndSigned(t *testing.T) {
	c, b := pushCore(t)
	if _, err := c.push.subscribe(b.sub("/push/1")); err != nil {
		t.Fatal(err)
	}
	c.tell(pushNote{URL: "/x", Tag: "t", Topic: "task-t-1234", Urgent: true, Body: map[string]string{"fr": "Bonjour", "en": "Hello"}})
	c.push.pending.Wait()
	got := b.received()
	if len(got) != 1 || !reflect.DeepEqual(got[0], pushPayload{Title: assistantName(), Body: "Bonjour", URL: "/x", Tag: "t"}) {
		t.Fatalf("received %+v", got)
	}
	h := b.heads[0]
	if h.Get("Content-Encoding") != "aes128gcm" || h.Get("Urgency") != "high" || h.Get("Topic") != "task-t-1234" || h.Get("TTL") == "" {
		t.Fatalf("headers %v", h)
	}
}

func TestPushSkippedWhileInFront(t *testing.T) {
	c, b := pushCore(t)
	c.push.subscribe(b.sub("/push/1"))
	now := time.Now()
	c.push.now = func() time.Time { return now }
	note := pushNote{Body: map[string]string{"fr": "x"}}

	c.push.seen("tab-1", true)
	c.tell(note)
	c.push.pending.Wait()
	if n := len(b.received()); n != 0 {
		t.Fatalf("%d pushes with a tab in front", n)
	}
	// A test the user asks for goes anyway.
	c.tell(pushNote{Anyway: true, Body: map[string]string{"fr": "essai"}})
	c.push.pending.Wait()
	if n := len(b.received()); n != 1 {
		t.Fatalf("the test: %d pushes", n)
	}
	// The tab went to the background.
	c.push.seen("tab-1", false)
	c.tell(note)
	c.push.pending.Wait()
	if n := len(b.received()); n != 2 {
		t.Fatalf("%d pushes once the tab left", n)
	}
	// A tab in front that stops saying so is no longer in front.
	c.push.seen("tab-2", true)
	now = now.Add(frontFor + time.Second)
	c.tell(note)
	c.push.pending.Wait()
	if n := len(b.received()); n != 3 {
		t.Fatalf("%d pushes after the tab went quiet", n)
	}
}

func TestPushGoneSubscriptionRemoved(t *testing.T) {
	c, b := pushCore(t)
	c.push.subscribe(b.sub("/push/1"))
	b.status = http.StatusGone
	c.tell(pushNote{Body: map[string]string{"fr": "x"}})
	c.push.pending.Wait()
	if c.push.any() {
		t.Fatal("a subscription the push service no longer knows is kept")
	}
	// Another answer keeps it.
	c.push.subscribe(b.sub("/push/2"))
	b.status = http.StatusTooManyRequests
	c.tell(pushNote{Body: map[string]string{"fr": "x"}})
	c.push.pending.Wait()
	if !c.push.any() {
		t.Fatal("a subscription was removed on a 429")
	}
}

func TestPushSubscriptionChecked(t *testing.T) {
	c, b := pushCore(t)
	good := b.sub("/push/1")
	for name, s := range map[string]pushSub{
		"http":     {Endpoint: "http://push.example/1", P256dh: good.P256dh, Auth: good.Auth},
		"no key":   {Endpoint: good.Endpoint, P256dh: "AAAA", Auth: good.Auth},
		"bad auth": {Endpoint: good.Endpoint, P256dh: good.P256dh, Auth: "AAAA"},
		"user":     {Endpoint: "https://u:p@push.example/1", P256dh: good.P256dh, Auth: good.Auth},
	} {
		if _, err := c.push.subscribe(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	id, err := c.push.subscribe(good)
	if err != nil || id != pushID(good.Endpoint) {
		t.Fatalf("subscribe: %q %v", id, err)
	}
	// The same device again replaces its row.
	if _, err := c.push.subscribe(good); err != nil {
		t.Fatal(err)
	}
	if subs, _ := c.push.subscriptions(); len(subs) != 1 {
		t.Fatalf("%d subscriptions", len(subs))
	}
}

func TestPushNotes(t *testing.T) {
	task := &TaskRow{ID: "t-1", Prompt: "Publish the package\nwith the tests"}
	done := taskEndedNote(task, stateIdle)
	if done.Body["fr"] != "Tâche finie : Publish the package" || done.Body["en"] != "Task done: Publish the package" || done.URL != "/?task=t-1" {
		t.Fatalf("done: %+v", done)
	}
	failed := taskEndedNote(task, stateFailed)
	if failed.Body["fr"] != "La tâche « Publish the package » a échoué" || failed.Body["en"] != "The task “Publish the package” failed" {
		t.Fatalf("failed: %+v", failed)
	}
	setAssistantName("Iris")
	t.Cleanup(func() { setAssistantName("") })
	w := waitingNote("Bash npm publish", "/#perm-t-1-p-1", "perm")
	if w.Body["fr"] != "Iris attend ta réponse : Bash npm publish" || w.Body["en"] != "Iris is waiting for your answer: Bash npm publish" || !w.Urgent {
		t.Fatalf("waiting: %+v", w)
	}
	if p := w.payload("en"); p.Title != "Iris" || p.URL != "/#perm-t-1-p-1" {
		t.Fatalf("payload: %+v", p)
	}
	for text, want := range map[string]string{
		"Done.\n\nShall I open the PR?":       "Shall I open the PR?",
		"**Tu veux que je continue ?**":       "Tu veux que je continue ?",
		"Done: https://x.test/?a=1\nPR open.": "",
		"":                                    "",
	} {
		if q, ok := asksUser(text); q != want || ok != (want != "") {
			t.Errorf("asksUser(%q) = %q %v", text, q, ok)
		}
	}
}

// A worker task's end reaches the devices; another task's does not.
func TestPushTaskEnded(t *testing.T) {
	c, b := pushCore(t)
	c.push.subscribe(b.sub("/push/1"))
	for _, task := range []*TaskRow{
		{ID: "t-w", Node: "box", Runtime: "claude-code", Prompt: "Fix the form", State: stateIdle},
		{ID: "t-f", Node: "box", Runtime: "claude-code", Prompt: "Sort the photos", State: stateFailed},
		{ID: "t-b", Node: "box", Runtime: "claude-code", Prompt: "brain", State: stateIdle, Kind: "brain"},
	} {
		if err := c.store.insertTask(task); err != nil {
			t.Fatal(err)
		}
	}
	c.notifyBrain("t-w", stateIdle)
	c.notifyBrain("t-w", stateRunning)
	c.notifyBrain("t-f", stateFailed)
	c.notifyBrain("t-b", stateIdle)
	c.push.pending.Wait()
	got := b.received()
	if len(got) != 2 {
		t.Fatalf("received %+v", got)
	}
	bodies := got[0].Body + " | " + got[1].Body
	if !strings.Contains(bodies, "Tâche finie : Fix the form") || !strings.Contains(bodies, "La tâche « Sort the photos » a échoué") {
		t.Fatalf("received %+v", got)
	}
}

// What waits for the user is told once: a permission that is theirs, and
// a question the assistant ends its message with.
func TestPushWaiting(t *testing.T) {
	tw := newTestWeb(t)
	c := tw.c
	b := newBrowser(t, c.push.pub)
	c.push.client = b.srv.Client()
	c.push.subscribe(b.sub("/push/1"))
	fixtures(t, c)
	tw.w.pushWaiting() // the first look starts from the messages already there
	c.push.pending.Wait()
	got := b.received()
	if len(got) != 1 || got[0].URL != "/#perm-t-perm-p-1" || !strings.Contains(got[0].Body, "attend ta réponse : Bash npm publish") {
		t.Fatalf("received %+v", got)
	}
	c.store.addMessage(roleBrain, "C'est prêt.\n\nJe publie la version 1.4 ?", true)
	c.store.addMessage(roleBrain, "Je m'en occupe.", true)
	tw.w.pushWaiting()
	tw.w.pushWaiting()
	c.push.pending.Wait()
	got = b.received()
	if len(got) != 2 || !strings.HasSuffix(got[1].Body, "attend ta réponse : Je publie la version 1.4 ?") || !strings.HasPrefix(got[1].URL, "/#m") {
		t.Fatalf("received %+v", got)
	}
}

func TestPushRoutes(t *testing.T) {
	tw := newTestWeb(t)
	// Before a session, nothing.
	if code, _, _ := tw.do("GET", "/push/key", nil, nil); code != http.StatusSeeOther {
		t.Fatalf("key without a session: %d", code)
	}
	csrf := tw.setup()
	code, body, _ := tw.do("GET", "/push/key", nil, nil)
	if code != 200 || !strings.Contains(body, tw.c.push.pub) {
		t.Fatalf("key: %d %s", code, body)
	}
	if code, body, _ := tw.do("GET", "/settings/notifications", nil, nil); code != 200 || !strings.Contains(body, `data-push-key="`+tw.c.push.pub+`"`) {
		t.Fatalf("page: %d", code)
	}
	b := newBrowser(t, tw.c.push.pub)
	s := b.sub("/push/1")
	sub, _ := json.Marshal(map[string]any{"endpoint": s.Endpoint, "keys": map[string]string{"p256dh": s.P256dh, "auth": s.Auth}, "app": true})
	post := func(token string, raw []byte) (int, string) {
		req, _ := http.NewRequest("POST", tw.srv.URL+"/push/subscribe", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/130.0 Mobile Safari/537.36")
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		resp, err := tw.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}
	if code, _ := post("", sub); code != http.StatusForbidden {
		t.Fatalf("subscribe without CSRF: %d", code)
	}
	if code, _ := post(csrf, []byte(`{"endpoint":"https://push.example/1","keys":{}}`)); code != http.StatusBadRequest {
		t.Fatalf("a bad subscription: %d", code)
	}
	code, body = post(csrf, sub)
	id := pushID(s.Endpoint)
	if code != 200 || !strings.Contains(body, `data-push-id="`+id+`"`) || !strings.Contains(body, "Android · Chrome · appli") {
		t.Fatalf("subscribe: %d %s", code, body)
	}
	// The test goes to that device.
	tw.c.push.client = b.srv.Client()
	if code, _, h := tw.post("/push/test", url.Values{"id": {id}}, csrf); code != 200 || !strings.Contains(h.Get("HX-Trigger"), "ok") {
		t.Fatalf("test: %d %v", code, h)
	}
	tw.c.push.pending.Wait()
	if got := b.received(); len(got) != 1 || got[0].URL != "/settings/notifications" {
		t.Fatalf("test push: %+v", got)
	}
	// Presence needs the CSRF token too.
	if code, _, _ := tw.do("POST", "/presence", url.Values{"tab": {"a"}, "front": {"1"}}, nil); code != http.StatusForbidden {
		t.Fatalf("presence without CSRF: %d", code)
	}
	if code, _, _ := tw.post("/presence", url.Values{"tab": {"a"}, "front": {"1"}}, csrf); code != http.StatusNoContent || !tw.c.push.inFront() {
		t.Fatalf("presence: %d", code)
	}
	if code, _, _ := tw.do("POST", "/push/unsubscribe", url.Values{"id": {id}}, nil); code != http.StatusForbidden {
		t.Fatalf("unsubscribe without CSRF: %d", code)
	}
	if code, body, _ := tw.post("/push/unsubscribe", url.Values{"id": {id}}, csrf); code != 200 || strings.Contains(body, id) || tw.c.push.any() {
		t.Fatalf("unsubscribe: %d %s", code, body)
	}
}

func TestDeviceLabel(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1": "iPhone · Safari",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36":                   "Mac · Chrome",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0":           "Windows · Edge",
		"curl/8": "Appareil",
	} {
		if got := deviceLabel(ua, "fr"); got != want {
			t.Errorf("%s: %q", ua, got)
		}
	}
}

func TestServiceWorkerVersioned(t *testing.T) {
	tw := newTestWeb(t)
	code, body, h := tw.do("GET", "/sw.js", nil, nil)
	if code != 200 || strings.Contains(body, "__VERSION__") || !strings.Contains(body, assetVersion()) || h.Get("Cache-Control") != "no-cache" {
		t.Fatalf("sw.js: %d %v", code, h)
	}
	code, body, _ = tw.do("GET", "/manifest.webmanifest", nil, nil)
	var m map[string]any
	if code != 200 || json.Unmarshal([]byte(body), &m) != nil || m["id"] != "/" || m["display"] != "standalone" || m["name"] != assistantName() {
		t.Fatalf("manifest: %d %s", code, body)
	}
}

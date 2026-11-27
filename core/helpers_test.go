package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Helpers shared by the package's tests.

// fakeClock is a settable now.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time      { return f.t }
func (f *fakeClock) add(d time.Duration) { f.t = f.t.Add(d) }
func newClock() *fakeClock               { return &fakeClock{t: time.Date(2026, 10, 26, 14, 0, 0, 0, time.Local)} }

// testIntents is an intentions store of its own, in a core.db of its
// own, its abouts kept as given.
func testIntents(t *testing.T) (*intentStore, *fakeClock) {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	clock := newClock()
	return &intentStore{db: s.db, now: clock.now}, clock
}

// testIntentsCore is a core with a store, its intentions on a fake clock,
// a brain task and an agent whose credential exists, so that tasks can be
// started (they wait for a device).
func testIntentsCore(t *testing.T) (*core, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	s, err := openStore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "claude")
	os.WriteFile(secret, []byte("tok"), 0o600)
	cfg := &Config{User: "Sam", Listen: "127.0.0.1:8800", DB: filepath.Join(dir, "core.db"), DefaultAgent: "small",
		Providers: map[string]Provider{"p": {Kind: "claude-subscription", Secret: secret}},
		Agents: map[string]Agent{"small": {Runtime: "claude-code", Provider: "p", Use: "quick things"},
			"haiku": {Runtime: "claude-code", Provider: "p", Use: "cheap"}},
		Brain: &BrainConfig{}}
	c := newCore(cfg, s)
	c.setServerTrust("vault", true, trustByUser)
	// A device that has gone offline: tasks wait for it.
	s.registerDevice("nas", hashSecret("a"))
	s.setDeviceStatus("nas", true, []byte(`{"runtimes":["claude-code"]}`))
	s.setDeviceStatus("nas", false, nil)
	clock := newClock()
	c.intents.now = clock.now
	s.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Runtime: "claude-code", State: stateIdle, Prompt: "brain"})
	setBrainTask(c, "t-brain")
	return c, clock
}

// ownerSays adds my message and starts the brain's turn with it.
func ownerSays(c *core, text string) int {
	seq, _ := c.store.addMessage(roleUser, text, false)
	t, _ := c.store.task("t-brain")
	c.turn.start(int(seq), int(seq), t.LastSeq)
	return int(seq)
}

func ref(seq int) string { return "msg:" + strconv.Itoa(seq) }

func mustIntend(t *testing.T, m *intentStore, x *Intention) *Intention {
	t.Helper()
	if x.SourceKind == "" {
		x.SourceKind, x.Trust = srcOwner, trustOwner
	}
	got, err := m.addIntention(x, "test")
	if err != nil {
		t.Fatalf("intend %q: %v", x.Text, err)
	}
	return got
}

func newIntentsWeb(t *testing.T) (*testWeb, string) {
	t.Helper()
	c, _ := testIntentsCore(t)
	tw := newTestWebFor(t, c)
	return tw, tw.setup()
}

func ptr[T any](v T) *T { return &v }

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

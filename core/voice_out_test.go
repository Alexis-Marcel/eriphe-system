package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func announceGet(t *testing.T, c *core, token, query string) (int, []voiceAnnouncement) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/voice/announce"+query, nil)
	req.RemoteAddr = "127.0.0.1:5000" // the LAN: clear HTTP from the internet is refused
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	c.handleVoiceAnnounce(rec, req)
	var out struct {
		Announcements []voiceAnnouncement `json:"announcements"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Announcements
}

// Only the voice's token fetches what to say; what waits comes at once,
// once, and an empty answer is a list.
func TestVoiceAnnounceEndpoint(t *testing.T) {
	c, tok := voiceCore(t)
	for _, bad := range []string{"", "evt_nope", c.token} {
		if code, _ := announceGet(t, c, bad, ""); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", bad, code)
		}
	}
	c.announce(pushNote{}, routeTarget{Step: stepVoice, Device: voiceDevice + "assist_satellite.salon"})
	code, got := announceGet(t, c, tok, "?wait=1")
	if code != 200 || len(got) != 1 || got[0].Entity != "assist_satellite.salon" || got[0].Text == "" || got[0].ID == 0 {
		t.Fatalf("%d %+v", code, got)
	}
	start := time.Now()
	if _, got := announceGet(t, c, tok, "?wait=1"); got == nil || len(got) != 0 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("said twice, or did not wait: %+v", got)
	}
}

// A long poll answers as soon as something is to be said; the integration
// listening opens the voice line, and stops opening it a while after.
func TestVoiceAnnounceWaits(t *testing.T) {
	var v voiceOut
	if v.listening(time.Now()) {
		t.Fatal("listening before any request")
	}
	got := make(chan []voiceAnnouncement)
	go func() { got <- v.take(context.Background(), 5*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	if !v.listening(time.Now()) {
		t.Fatal("a request open is not listening")
	}
	v.add("media_player.tv", "Sam, Eriphe a quelque chose pour toi.", time.Now())
	select {
	case a := <-got:
		if len(a) != 1 || a[0].Entity != "media_player.tv" {
			t.Fatalf("%+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request did not answer when something came")
	}
	if !v.listening(time.Now().Add(voiceListening - time.Second)) {
		t.Fatal("not listening just after a request")
	}
	if v.listening(time.Now().Add(voiceListening + time.Second)) {
		t.Fatal("still listening long after the last request")
	}
	// Late is dropped: a room may be empty by then.
	v.add("media_player.tv", "old", time.Now().Add(-voiceAnnounceTTL))
	if a := v.pop(time.Now()); len(a) != 0 {
		t.Fatalf("a late sentence: %+v", a)
	}
	for range voiceQueueMax + 3 {
		v.add("media_player.tv", "x", time.Now())
	}
	if a := v.pop(time.Now()); len(a) != voiceQueueMax {
		t.Fatalf("queue of %d", len(a))
	}
}

// Without the integration listening, the router has no voice line.
func TestVoiceSpotNeedsListener(t *testing.T) {
	c, _ := voiceCore(t)
	if _, ok := c.voiceSpot(time.Now()); ok {
		t.Fatal("a voice spot without the integration")
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func outGet(t *testing.T, c *core, token, query string) (int, []channelMessage) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/channel/out"+query, nil)
	req.RemoteAddr = "127.0.0.1:5000" // the LAN: clear HTTP from the internet is refused
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	c.handleChannelOut(rec, req)
	var out struct {
		Messages []channelMessage `json:"messages"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Messages
}

// Only a channel's token fetches what goes out through it; what waits
// comes at once, once, and an empty answer is a list. Another channel's
// queue is its own.
func TestChannelOutEndpoint(t *testing.T) {
	c, tok := voiceCore(t)
	for _, bad := range []string{"", "ech_nope", c.token} {
		if code, _ := outGet(t, c, bad, ""); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", bad, code)
		}
	}
	ha := c.addedChannels()[0]
	_, otherTok, _ := c.addChannel("Écran", false, carrySentence)
	c.announce(pushNote{}, routeTarget{Step: stepVoice, Device: fmt.Sprintf("channel:%d", ha.ID), Label: "Salon"})
	if _, got := outGet(t, c, otherTok, ""); len(got) != 0 {
		t.Fatalf("another channel got it: %+v", got)
	}
	code, got := outGet(t, c, tok, "?wait=1")
	if code != 200 || len(got) != 1 || got[0].Place != "Salon" || got[0].Text == "" || got[0].ID == 0 {
		t.Fatalf("%d %+v", code, got)
	}
	start := time.Now()
	if _, got := outGet(t, c, tok, "?wait=1"); got == nil || len(got) != 0 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("said twice, or did not wait: %+v", got)
	}
}

// A long poll answers as soon as something comes; the app listening, and
// the places it reaches, open the voice line; a while after, it is closed.
func TestChannelOutWaits(t *testing.T) {
	var o channelOut
	if o.listening(1, time.Now()) || o.reaches(1, "Salon", time.Now()) {
		t.Fatal("listening before any request")
	}
	got := make(chan []channelMessage)
	go func() { got <- o.take(context.Background(), 1, []string{"Salon", "Bureau"}, 5*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	if !o.reaches(1, "salon", time.Now()) || o.reaches(1, "Cuisine", time.Now()) || o.reaches(2, "Salon", time.Now()) {
		t.Fatal("the places it reaches")
	}
	o.add(1, "Sam, Eriphe a quelque chose pour toi.", "Salon", time.Now())
	select {
	case m := <-got:
		if len(m) != 1 || m[0].Place != "Salon" {
			t.Fatalf("%+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request did not answer when something came")
	}
	if !o.listening(1, time.Now().Add(channelListening-time.Second)) {
		t.Fatal("not listening just after a request")
	}
	if o.listening(1, time.Now().Add(channelListening+time.Second)) {
		t.Fatal("still listening long after the last request")
	}
	// No places said: anywhere.
	o.take(context.Background(), 1, []string{}, 0)
	if !o.reaches(1, "Cuisine", time.Now()) {
		t.Fatal("an app that says no places reaches nowhere")
	}
	// Late is dropped: a room may be empty by then.
	o.add(1, "old", "", time.Now().Add(-channelOutTTL))
	if m := o.pop(1, time.Now()); len(m) != 0 {
		t.Fatalf("a late sentence: %+v", m)
	}
	for range channelQueueMax + 3 {
		o.add(1, "x", "", time.Now())
	}
	if m := o.pop(1, time.Now()); len(m) != channelQueueMax {
		t.Fatalf("queue of %d", len(m))
	}
}

// Without a channel that says a sentence and listens, the router has no
// voice line.
func TestVoiceSpotNeedsListener(t *testing.T) {
	c, _ := voiceCore(t)
	if _, ok := c.voiceSpot(time.Now()); ok {
		t.Fatal("a voice spot without a listening channel")
	}
}

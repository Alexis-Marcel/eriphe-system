package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The voice's outbound side: what Eriphe says on a speaker of the house.
// The voice is a channel (channels.go) with one connection and one secret,
// both ways: Home Assistant's integration (ha/) holds the voice's token,
// sends what is said through POST /v1/voice, and keeps a request open here
// for what to say, which it says itself (assist_satellite.announce, or
// tts.speak on a media player). eriphe-core never calls Home Assistant to
// speak, and never with the token lent to the house's tool.
//
//	GET /v1/voice/announce?wait=50  {"announcements": [{"id", "entity", "text"}]}
//
// The request answers at once when something waits, else after wait
// seconds (at most voicePollMax) with an empty list. What waits longer than
// voiceAnnounceTTL is dropped: late, it would be said to an empty room.
// The router's voice line is used only while the integration listens.

const (
	voicePollMax     = 55 * time.Second
	voiceAnnounceTTL = time.Minute
	voiceListening   = 2 * time.Minute // since its last request, the integration still listens
	voiceQueueMax    = 10
)

// voiceAnnouncement is one sentence for one speaker.
type voiceAnnouncement struct {
	ID     int64     `json:"id"`
	Entity string    `json:"entity"` // assist_satellite.… or media_player.…
	Text   string    `json:"text"`   // written by the code (voiceText)
	at     time.Time // queued
}

// voiceOut is the queue of what to say, and whether the integration
// listens. Its zero value is ready.
type voiceOut struct {
	mu       sync.Mutex
	queue    []voiceAnnouncement
	next     int64
	open     int       // requests waiting now
	lastPoll time.Time // the end of the last one
	wake     chan struct{}
}

func (v *voiceOut) wakeChan() chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.wake == nil {
		v.wake = make(chan struct{}, 1)
	}
	return v.wake
}

// listening: the integration has a request open, or had one lately.
func (v *voiceOut) listening(now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.open > 0 || !v.lastPoll.IsZero() && now.Sub(v.lastPoll) < voiceListening
}

// add queues a sentence for a speaker; the oldest goes when it is full.
func (v *voiceOut) add(entity, text string, now time.Time) {
	v.mu.Lock()
	v.next++
	v.queue = append(v.queue, voiceAnnouncement{ID: v.next, Entity: entity, Text: text, at: now})
	if len(v.queue) > voiceQueueMax {
		v.queue = v.queue[len(v.queue)-voiceQueueMax:]
	}
	v.mu.Unlock()
	select {
	case v.wakeChan() <- struct{}{}:
	default:
	}
}

// pop takes what waits, without what is too old to be said.
func (v *voiceOut) pop(now time.Time) []voiceAnnouncement {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := []voiceAnnouncement{}
	for _, a := range v.queue {
		if now.Sub(a.at) < voiceAnnounceTTL {
			out = append(out, a)
		}
	}
	v.queue = nil
	return out
}

// take answers one request of the integration: what waits, or what comes
// within wait.
func (v *voiceOut) take(ctx context.Context, wait time.Duration) []voiceAnnouncement {
	v.mu.Lock()
	v.open++
	v.mu.Unlock()
	defer func() {
		v.mu.Lock()
		v.open--
		v.lastPoll = time.Now()
		v.mu.Unlock()
	}()
	deadline := time.Now().Add(wait)
	for {
		if out := v.pop(time.Now()); len(out) > 0 {
			return out
		}
		left := time.Until(deadline)
		if left <= 0 {
			return []voiceAnnouncement{}
		}
		select {
		case <-ctx.Done():
			return []voiceAnnouncement{}
		case <-v.wakeChan():
		case <-time.After(left):
		}
	}
}

// handleVoiceAnnounce is the integration's open request.
func (c *core) handleVoiceAnnounce(w http.ResponseWriter, r *http.Request) {
	if clearFromInternet(r) {
		http.Error(w, errClearFromInternet, http.StatusForbidden)
		return
	}
	if !c.voiceTokenOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wait := time.Duration(0)
	if s, err := strconv.Atoi(r.URL.Query().Get("wait")); err == nil && s > 0 {
		wait = min(time.Duration(s)*time.Second, voicePollMax)
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcements": c.voice.take(r.Context(), wait)})
}

// voiceSpot is where the voice line reaches the user now: a speaker in the
// room they are in (ha_presence.go), while the integration listens.
func (c *core) voiceSpot(now time.Time) (voiceSpot, bool) {
	if !c.voice.listening(now) {
		return voiceSpot{}, false
	}
	return c.haVoiceSpot(now)
}

// announce queues the code's sentence for the target's speaker.
func (c *core) announce(n pushNote, t routeTarget) {
	entity, ok := strings.CutPrefix(t.Device, voiceDevice)
	if !ok || !speakerEntity(entity) {
		return
	}
	c.voice.add(entity, c.voiceText(n), time.Now())
}

// speakerEntity: what the voice may speak on.
func speakerEntity(entity string) bool {
	return strings.HasPrefix(entity, "assist_satellite.") || strings.HasPrefix(entity, "media_player.")
}

const voiceDevice = "voice:"

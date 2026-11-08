package main

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What goes out through a channel the user added (channels_added.go): the
// app holding its token keeps a request open here, and shows or says what
// comes. Eriphe never calls the app.
//
//	GET /v1/channel/out?wait=50&places=Salon,Bureau  {"messages": [{"id", "text", "place"}]}
//
// The request answers at once when something waits, else after wait
// seconds (at most channelPollMax) with an empty list. What waits longer
// than channelOutTTL is dropped: late, it would be said to an empty room.
//
// A channel that carries a sentence is the router's "voice" line (route.go):
// a sentence the code writes, never a notification's text, said in the
// room the user is in now. The app tells the places it reaches (places;
// none: anywhere), and maps a place to its own speaker; the line is used
// only while the app listens and reaches that room.

const (
	channelPollMax   = 55 * time.Second
	channelOutTTL    = time.Minute
	channelListening = 2 * time.Minute // since its last request, the app still listens
	channelQueueMax  = 10
	channelPlacesMax = 50
)

// channelMessage is one thing to say or show.
type channelMessage struct {
	ID    int64     `json:"id"`
	Text  string    `json:"text"`  // written by the code (voiceText)
	Place string    `json:"place"` // the room it is for; "" anywhere
	at    time.Time // queued
}

// channelListener is one channel's queue, and whether its app listens.
type channelListener struct {
	queue    []channelMessage
	open     int       // requests waiting now
	lastPoll time.Time // the end of the last one
	places   []string  // what the app said it reaches; none: anywhere
	wake     chan struct{}
}

// channelOut is every channel's. Its zero value is ready.
type channelOut struct {
	mu   sync.Mutex
	next int64
	by   map[int64]*channelListener
}

func (o *channelOut) get(id int64) *channelListener {
	if o.by == nil {
		o.by = map[int64]*channelListener{}
	}
	l := o.by[id]
	if l == nil {
		l = &channelListener{wake: make(chan struct{}, 1)}
		o.by[id] = l
	}
	return l
}

func (o *channelOut) forget(id int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.by, id)
}

// reaches: the app listens, and reaches that place.
func (o *channelOut) reaches(id int64, place string, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	l := o.by[id]
	if l == nil || l.open == 0 && (l.lastPoll.IsZero() || now.Sub(l.lastPoll) >= channelListening) {
		return false
	}
	return len(l.places) == 0 || slices.ContainsFunc(l.places, func(p string) bool { return strings.EqualFold(p, place) })
}

// listening: the app has a request open, or had one lately.
func (o *channelOut) listening(id int64, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	l := o.by[id]
	return l != nil && (l.open > 0 || !l.lastPoll.IsZero() && now.Sub(l.lastPoll) < channelListening)
}

// add queues a message; the oldest goes when it is full.
func (o *channelOut) add(id int64, text, place string, now time.Time) {
	o.mu.Lock()
	l := o.get(id)
	o.next++
	l.queue = append(l.queue, channelMessage{ID: o.next, Text: text, Place: place, at: now})
	if len(l.queue) > channelQueueMax {
		l.queue = l.queue[len(l.queue)-channelQueueMax:]
	}
	wake := l.wake
	o.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

// pop takes what waits for a channel, without what is too old.
func (o *channelOut) pop(id int64, now time.Time) []channelMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	l := o.get(id)
	out := []channelMessage{}
	for _, m := range l.queue {
		if now.Sub(m.at) < channelOutTTL {
			out = append(out, m)
		}
	}
	l.queue = nil
	return out
}

// take answers one request of a channel's app: what waits, or what comes
// within wait.
func (o *channelOut) take(ctx context.Context, id int64, places []string, wait time.Duration) []channelMessage {
	o.mu.Lock()
	l := o.get(id)
	l.open++
	if places != nil {
		l.places = places
	}
	wake := l.wake
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		l.open--
		l.lastPoll = time.Now()
		o.mu.Unlock()
	}()
	deadline := time.Now().Add(wait)
	for {
		if out := o.pop(id, time.Now()); len(out) > 0 {
			return out
		}
		left := time.Until(deadline)
		if left <= 0 {
			return []channelMessage{}
		}
		select {
		case <-ctx.Done():
			return []channelMessage{}
		case <-wake:
		case <-time.After(left):
		}
	}
}

// handleChannelOut is the app's open request.
func (c *core) handleChannelOut(w http.ResponseWriter, r *http.Request) {
	ch, ok := c.channelAuth(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	wait := time.Duration(0)
	if s, err := strconv.Atoi(q.Get("wait")); err == nil && s > 0 {
		wait = min(time.Duration(s)*time.Second, channelPollMax)
	}
	places := []string{} // the request says none: anywhere
	for _, p := range strings.Split(q.Get("places"), ",") {
		if p = cleanPlace(p); p != "" && len(places) < channelPlacesMax {
			places = append(places, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": c.out.take(r.Context(), ch.ID, places, wait)})
}

// voiceSpot is where the voice line reaches the user now: a channel that
// carries a sentence, listening, reaching the room they are in.
type voiceSpot struct {
	Channel int64
	Name    string
	Room    string
}

func (c *core) voiceSpot(now time.Time) (voiceSpot, bool) {
	room, ok := c.haUserRoom(now)
	if !ok {
		return voiceSpot{}, false
	}
	for _, ch := range c.addedChannels() {
		if ch.Carries == carrySentence && c.out.reaches(ch.ID, room, now) {
			return voiceSpot{Channel: ch.ID, Name: ch.Name, Room: room}, true
		}
	}
	return voiceSpot{}, false
}

// voiceReady: a channel carries a sentence, so the voice line can be used.
func (c *core) voiceReady() bool {
	return slices.ContainsFunc(c.addedChannels(), func(ch addedChannel) bool { return ch.Carries == carrySentence })
}

// announce queues the code's sentence on the target's channel, for its room.
func (c *core) announce(n pushNote, t routeTarget) {
	id, ok := voiceChannelID(t.Device)
	if !ok {
		return
	}
	c.out.add(id, c.voiceText(n), t.Label, time.Now())
}

// The voice line's target is a channel: "channel:<id>".
const voiceDevice = "channel:"

func voiceChannelID(device string) (int64, bool) {
	s, ok := strings.CutPrefix(device, voiceDevice)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}

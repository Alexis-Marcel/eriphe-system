package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// What comes in through a channel the user added (channels_added.go): text,
// from any app holding the channel's token. It joins the conversation like
// any of mine, marked with the channel and, if the app says it, the place
// it was written or said in; the brain answers it in the same thread, with
// the same memory and tools, and the answer streams back.
//
//	POST /v1/channel  {"text", "language"?, "place"?, "wait"?}
//	GET  /v1/channel  {"name", "channel"}: the assistant's name, the channel's
//
// The answer is NDJSON: {"delta": "…"} lines, then {"done": true, "text",
// "continue"}. "wait" is how long to wait for a first word, in seconds
// (10 by default, at most 80: an automation that sends the answer on).
//
// A channel the user did not trust may carry anyone's words: the message
// taints its turn, so the brain reads and answers freely but a write goes
// on a card, which the user confirms on a trusted channel, never through
// this one (resolveByText). The answer then says so, and a notice in the
// code's words goes to the user (channelCardNote).
//
// POST and GET /v1/voice are the same, for Home Assistant's integration
// before it moved to /v1/channel: its "area" is the place.

const (
	deliveredLastKey = "brain_delivered_last" // the last message at the brain's last delivery
	channelHolding   = "Je m'en occupe, je te préviens."
	channelWaitMax   = 80 * time.Second
)

var (
	// A channel waits this long for a first word by default: the brain may
	// be busy with another turn, or doing something long.
	channelHold = 10 * time.Second
	// Nor longer than this, after the first word, for the whole answer.
	channelMax = 60 * time.Second
)

// A place's name is quoted into the brain's turn: letters, digits, spaces
// and a few marks, short.
var notPlace = regexp.MustCompile(`[^\p{L}\p{N} '’-]+`)

func cleanPlace(place string) string {
	place = strings.Join(strings.Fields(notPlace.ReplaceAllString(place, " ")), " ")
	if r := []rune(place); len(r) > 40 {
		place = string(r[:40])
	}
	return place
}

// channelVia is where a message says it came from: the channel, and the
// place if the app said it ("Home Assistant, Salon").
func channelVia(ch addedChannel, place string) string {
	if place = cleanPlace(place); place != "" {
		return ch.Name + ", " + place
	}
	return ch.Name
}

// channelAuth answers a request that is not a channel's.
func (c *core) channelAuth(w http.ResponseWriter, r *http.Request) (addedChannel, bool) {
	if clearFromInternet(r) {
		http.Error(w, errClearFromInternet, http.StatusForbidden)
		return addedChannel{}, false
	}
	ch, ok := c.channelOf(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
	return ch, ok
}

// handleChannelInfo tells the app the assistant's name and its channel's.
func (c *core) handleChannelInfo(w http.ResponseWriter, r *http.Request) {
	ch, ok := c.channelAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": c.conf().assistantName(), "channel": ch.Name})
}

func (c *core) handleChannelIn(w http.ResponseWriter, r *http.Request) {
	ch, ok := c.channelAuth(w, r)
	if !ok {
		return
	}
	var in struct {
		Text     string          `json:"text"`
		Language string          `json:"language"`
		Place    string          `json:"place"`
		Area     string          `json:"area"` // /v1/voice's place
		Wait     json.RawMessage `json:"wait"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if b := c.conf().brain(); b == nil || b.Agent == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no brain on this server"})
		return
	}
	if in.Place == "" {
		in.Place = in.Area
	}
	hold := channelHold
	if s, err := strconv.Atoi(strings.Trim(string(in.Wait), `"`)); err == nil && s > 0 {
		hold = min(time.Duration(s)*time.Second, channelWaitMax)
	}
	seq, err := c.store.addMessageVia(roleUser, strings.TrimSpace(in.Text), channelVia(ch, in.Place), false)
	if err == nil {
		if ch.Trusted {
			c.resolveByText(r.Context(), int(seq)) // "oui" to the question just asked (convo_cards.go)
		} else {
			err = c.store.set(taintKey(int(seq)), channelTaint(ch.Name))
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.ping()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	emit := func(v any) {
		enc.Encode(v)
		if flusher != nil {
			flusher.Flush()
		}
	}
	text := c.channelReply(r.Context(), int(seq), hold, max(hold, channelMax), func(delta string) { emit(map[string]string{"delta": delta}) })
	// A card the answer left is confirmed on a trusted channel, never
	// through this one: the answer says so, and the card's notice goes there.
	if !ch.Trusted {
		lang := "fr"
		if strings.HasPrefix(strings.ToLower(in.Language), "en") {
			lang = "en"
		}
		if x, say := c.channelCardConfirm(int(seq), lang); say != "" {
			if text != channelHolding {
				emit(map[string]string{"delta": " " + say})
				text = strings.TrimSpace(text + " " + say)
			}
			if c.store.get(channelCardKey(x.ID)) == "" {
				c.store.set(channelCardKey(x.ID), "1")
				c.tell(channelCardNote(x))
			}
		}
	}
	emit(map[string]any{"done": true, "text": text, "continue": strings.HasSuffix(text, "?")})
}

// channelCardKey marks a card whose notice went to the user.
func channelCardKey(id int64) string { return fmt.Sprintf("channel_card:%d", id) }

// channelReply follows the brain's answer to message seq: its whole messages
// as they are stored, and the draft it is writing, from the moment it has
// read seq (what it says before belongs to another turn). It sends what is
// new as deltas and returns the whole answer, once the brain's turn is
// over. Without a word within channelHold, it answers channelHolding: the
// brain's answer stays in the conversation.
func (c *core) channelReply(ctx context.Context, seq int, hold, max time.Duration, delta func(string)) string {
	start := time.Now()
	sent, lastSeen, read := "", seq, false
	var said []string
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		draft, after, changed := c.draft.get()
		if !read && c.delivered(seq) {
			read = true
			// What the brain said before it read seq is another turn's.
			fmt.Sscan(c.store.get(deliveredLastKey), &lastSeen)
		}
		if msgs, err := c.store.messagesAfter(lastSeen, 50); err == nil {
			for _, m := range msgs {
				lastSeen = m.Seq
				if read && m.Role == roleBrain && strings.TrimSpace(m.Text) != "" {
					said = append(said, spoken(m.Text))
				}
			}
		}
		now := strings.Join(said, " ")
		if read && after >= lastSeen && strings.TrimSpace(draft) != "" {
			now = strings.TrimSpace(now + " " + spoken(draft))
		}
		if len(now) > len(sent) && strings.HasPrefix(now, sent) {
			delta(now[len(sent):])
			sent = now
		}
		if len(said) > 0 && strings.TrimSpace(draft) == "" && c.brainIdle() {
			return strings.Join(said, " ")
		}
		switch elapsed := time.Since(start); {
		case sent == "" && elapsed > hold:
			delta(channelHolding)
			return channelHolding
		case elapsed > max:
			return sent
		}
		select {
		case <-ctx.Done():
			return sent
		case <-changed:
		case <-tick.C:
		}
	}
}

func (c *core) delivered(seq int) bool {
	var delivered bool
	return c.store.db.QueryRow(`SELECT delivered FROM messages WHERE seq = ?`, seq).Scan(&delivered) == nil && delivered
}

func (c *core) brainIdle() bool {
	t, err := c.store.task(c.brainTaskID())
	return err == nil && t.State == stateIdle
}

// spoken is a message as it is read aloud: no markdown marks.
var markdownMarks = strings.NewReplacer("**", "", "__", "", "`", "", "# ", "")

func spoken(text string) string {
	return strings.Join(strings.Fields(markdownMarks.Replace(text)), " ")
}

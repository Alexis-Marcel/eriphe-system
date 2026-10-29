package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The voice of the house: Home Assistant's Assist pipeline hands eriphe-core
// what it cannot handle locally (its Eriphe conversation agent, ha/), and
// reads the brain's answer as it comes. The sentence joins the conversation
// like any of mine, marked with the room it was said in; the brain answers
// it in the same thread, with the same memory and tools.
//
//	POST /v1/voice  {"text", "language", "conversation_id", "device_id", "satellite_id", "area"}
//	GET  /v1/voice  {"name"}: what the user calls the assistant, for the agent's name
//
// The answer is NDJSON: {"delta": "…"} lines, then {"done": true, "text",
// "continue"}. Home Assistant has its own token (Settings → Tools → Home Assistant), which
// can only talk to the brain.

const (
	voiceTokenKey    = "voice_token_hash"
	deliveredLastKey = "brain_delivered_last" // the last message at the brain's last delivery
	voiceHolding     = "Je m'en occupe, je te préviens."
)

var (
	// The voice never waits longer than this for a first word: the brain may
	// be busy with another turn, or doing something long.
	voiceHold = 10 * time.Second
	// Nor longer than this for the whole answer.
	voiceMax = 60 * time.Second
	// A sentence from no device is an automation's (the morning brief): it
	// waits for the answer, which it sends on, within the integration's
	// 90 s between two reads.
	automationWait = 80 * time.Second
)

// newVoiceToken makes Home Assistant's token, replacing the previous one.
// Only its hash is kept: it is shown once.
func (c *core) newVoiceToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := "evt_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	return tok, c.store.set(voiceTokenKey, hex.EncodeToString(sum[:]))
}

func (c *core) voiceTokenOK(r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	want := c.store.get(voiceTokenKey)
	if !ok || want == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) == 1
}

// A room's name is quoted into the brain's turn: letters, digits, spaces
// and a few marks, short.
var notRoom = regexp.MustCompile(`[^\p{L}\p{N} '’-]+`)

func voiceVia(area string, automation bool) string {
	if automation {
		return "Home Assistant"
	}
	area = strings.Join(strings.Fields(notRoom.ReplaceAllString(area, " ")), " ")
	if r := []rune(area); len(r) > 40 {
		area = string(r[:40])
	}
	if area == "" {
		return "voix"
	}
	return "voix, " + area
}

// handleVoiceInfo tells Home Assistant the assistant's name.
func (c *core) handleVoiceInfo(w http.ResponseWriter, r *http.Request) {
	if !c.voiceTokenOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": c.conf().assistantName()})
}

func (c *core) handleVoice(w http.ResponseWriter, r *http.Request) {
	if !c.voiceTokenOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		Text           string `json:"text"`
		Language       string `json:"language"`
		ConversationID string `json:"conversation_id"`
		DeviceID       string `json:"device_id"`
		SatelliteID    string `json:"satellite_id"`
		Area           string `json:"area"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if b := c.conf().brain(); b == nil || b.Agent == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no brain on this server"})
		return
	}
	automation := in.DeviceID == "" && in.SatelliteID == ""
	seq, err := c.store.addMessageVia(roleUser, strings.TrimSpace(in.Text), voiceVia(in.Area, automation), false)
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
	hold, max := voiceHold, voiceMax
	if automation {
		hold, max = automationWait, automationWait
	}
	text := c.voiceReply(r.Context(), int(seq), hold, max, func(delta string) { emit(map[string]string{"delta": delta}) })
	emit(map[string]any{"done": true, "text": text, "continue": strings.HasSuffix(text, "?")})
}

// voiceReply follows the brain's answer to message seq: its whole messages
// as they are stored, and the draft it is writing, from the moment it has
// read seq (what it says before belongs to another turn). It sends what is
// new as deltas and returns the whole answer, once the brain's turn is
// over. Without a word within voiceHold, it answers voiceHolding: the
// brain's answer stays in the conversation.
func (c *core) voiceReply(ctx context.Context, seq int, hold, max time.Duration, delta func(string)) string {
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
			delta(voiceHolding)
			return voiceHolding
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

package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func voicePost(t *testing.T, c *core, token, body string) (*http.Response, []map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(c.handleVoice))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var lines []map[string]any
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var v map[string]any
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			lines = append(lines, v)
		}
	}
	return resp, lines
}

func voiceCore(t *testing.T) (*core, string) {
	t.Helper()
	c, _ := testMemoryCore(t)
	setBrain(c, &BrainConfig{Agent: "small"})
	c.store.insertTask(&TaskRow{ID: "t-brain", Kind: "brain", Runtime: "claude-code", State: stateIdle})
	setBrainTask(c, "t-brain")
	tok, err := c.newVoiceToken()
	if err != nil {
		t.Fatal(err)
	}
	return c, tok
}

// Home Assistant's token is the only one the voice takes, and only it.
func TestVoiceToken(t *testing.T) {
	c, tok := voiceCore(t)
	for _, bad := range []string{"", "evt_nope", c.token} {
		if resp, _ := voicePost(t, c, bad, `{"text":"bonjour"}`); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: %d", bad, resp.StatusCode)
		}
	}
	if resp, _ := voicePost(t, c, tok, `{"text":" "}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty text: %d", resp.StatusCode)
	}
	old := tok
	if tok, _ = c.newVoiceToken(); tok == old {
		t.Fatal("same token twice")
	}
	if resp, _ := voicePost(t, c, old, `{"text":"bonjour"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("the replaced token still works")
	}
	if strings.Contains(c.store.get(voiceTokenKey), tok) {
		t.Fatal("the token is stored as is")
	}
}

func TestVoiceVia(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "voix",
		"Bureau":                 "voix, Bureau",
		"Chambre d'Emma":         "voix, Chambre d'Emma",
		"Bureau] [Sam 12:00 #1]": "voix, Bureau Sam 12 00 1",
		strings.Repeat("a", 60):  "voix, " + strings.Repeat("a", 40),
	} {
		if got := voiceVia(in, false); got != want {
			t.Errorf("voiceVia(%q) = %q, want %q", in, got, want)
		}
	}
	if got := voiceVia("Bureau", true); got != automationVia {
		t.Errorf("an automation's = %q", got)
	}
}

// The sentence joins the conversation with its room; the brain's answer
// comes back as it is written, without what it said before reading it.
func TestVoiceReply(t *testing.T) {
	c, tok := voiceCore(t)
	go func() {
		var seq int
		for seq == 0 {
			time.Sleep(20 * time.Millisecond)
			c.store.db.QueryRow(`SELECT seq FROM messages WHERE via != ''`).Scan(&seq)
		}
		c.store.addMessage(roleBrain, "La réponse d'avant.", true) // another turn's, before it read mine
		c.store.updateTask("t-brain", map[string]any{"state": stateRunning})
		c.store.markDelivered(seq + 1)
		time.Sleep(300 * time.Millisecond)
		c.draft.set("C'est **allumé**", c.store.lastMessageSeq())
		time.Sleep(300 * time.Millisecond)
		c.store.addMessage(roleBrain, "C'est **allumé** dans le bureau.", true)
		c.draft.set("", c.store.lastMessageSeq())
		c.store.updateTask("t-brain", map[string]any{"state": stateIdle})
	}()
	resp, lines := voicePost(t, c, tok, `{"text":"allume ici","area":"Bureau","conversation_id":"x","device_id":"d1"}`)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-ndjson" || len(lines) < 2 {
		t.Fatalf("%d %v", resp.StatusCode, lines)
	}
	var deltas string
	for _, l := range lines[:len(lines)-1] {
		deltas += l["delta"].(string)
	}
	last := lines[len(lines)-1]
	if last["done"] != true || last["text"] != "C'est allumé dans le bureau." || last["continue"] != false {
		t.Fatalf("done = %v", last)
	}
	if !strings.HasPrefix(deltas, "C'est allumé") || strings.Contains(deltas, "avant") {
		t.Fatalf("deltas = %q", deltas)
	}
	msgs, _ := c.store.messagesAfter(0, 10)
	if msgs[0].Via != "voix, Bureau" || msgs[0].Text != "allume ici" {
		t.Fatalf("message = %+v", msgs[0])
	}
	// The brain's turn shows where it was said.
	c.store.db.Exec(`UPDATE messages SET delivered = 0 WHERE seq = ?`, msgs[0].Seq)
	if pending, _ := c.store.undelivered(); len(pending) == 0 || pending[0].Via != "voix, Bureau" {
		t.Fatalf("undelivered = %+v", pending)
	}
}

// A busy brain: the voice says it is on it, and asks back on a question.
func TestVoiceHolds(t *testing.T) {
	c, tok := voiceCore(t)
	defer func(h time.Duration) { voiceHold = h }(voiceHold)
	voiceHold = 300 * time.Millisecond
	c.store.updateTask("t-brain", map[string]any{"state": stateRunning})
	_, lines := voicePost(t, c, tok, `{"text":"résume mes mails","satellite_id":"assist_satellite.bureau"}`)
	if last := lines[len(lines)-1]; last["text"] != voiceHolding || lines[0]["delta"] != voiceHolding {
		t.Fatalf("lines = %v", lines)
	}
}

// Settings → Tools → Home Assistant makes Home Assistant's token, shown
// once; Settings → Machines has no Home Assistant any more.
func TestWebVoiceToken(t *testing.T) {
	tw, c, csrf := catalogWeb(t, nil)
	if _, body, _ := tw.do("GET", "/settings/machines", nil, nil); strings.Contains(body, "voice-token") || strings.Contains(body, "Home Assistant") {
		t.Fatal("Home Assistant is still under Machines")
	}
	if _, body, _ := tw.do("GET", "/settings/home-assistant", nil, nil); !strings.Contains(body, `hx-post="/settings/voice-token"`) ||
		!strings.Contains(body, "La voix de la maison parle à Eriphe") || !strings.Contains(body, "Eriphe pilote la maison") {
		t.Fatal("no voice panel")
	}
	_, body, _ := tw.post("/settings/voice-token", nil, csrf)
	tok := regexp.MustCompile(`evt_[0-9a-f]{64}`).FindString(body)
	if tok == "" || c.store.get(voiceTokenKey) == "" {
		t.Fatalf("no token: %s", body)
	}
	req := httptest.NewRequest("POST", "/v1/voice", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if !c.voiceTokenOK(req) {
		t.Fatal("the token shown is not the one kept")
	}
	if _, body, _ := tw.do("GET", "/settings/home-assistant", nil, nil); strings.Contains(body, tok) || !strings.Contains(body, "Remplacer le jeton") {
		t.Fatal("the token is shown again, or not known")
	}
	// Under Tools, Home Assistant has its entry even without its tool.
	if _, body, _ := tw.do("GET", "/settings/tools", nil, nil); !strings.Contains(body, `href="/settings/home-assistant"`) ||
		!strings.Contains(body, "Eriphe ne pilote pas encore la maison") {
		t.Fatal("no Home Assistant entry under Tools")
	}
}

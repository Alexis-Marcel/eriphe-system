package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// « Laisse Eriphe parler »: what goes to a channel is never cut at
// Eriphe's own limits; longer than the service takes, it goes in parts.

// sameWords tells whether the parts carry all of s, in order: only the
// spaces and line breaks a cut fell on may go.
func sameWords(s string, parts []string) bool {
	return strings.Join(strings.Fields(s), "") == strings.Join(strings.Fields(strings.Join(parts, "")), "")
}

func longText(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "Phrase %d de ce que dit Eriphe, sans rien perdre. ", i)
		if i%7 == 6 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func TestSplitText(t *testing.T) {
	if got := splitText("court", 100, 20); len(got) != 1 || got[0] != "court" {
		t.Fatalf("short: %q", got)
	}
	if s := longText(50000); len(splitText(s, 0, 200)) != 1 {
		t.Fatal("no limit, yet split")
	}
	emoji := strings.Repeat("🙂", 3000) // 2 UTF-16 units each
	for name, s := range map[string]string{
		"prose":    longText(20000),
		"one line": strings.Repeat("mot ", 3000),
		"no space": strings.Repeat("x", 9000),
		"emoji":    emoji,
		"mixed":    longText(3000) + emoji + "é" + longText(3000),
	} {
		for _, max := range []int{2000, 4096} {
			for _, room := range []int{0, botDoneRoom} {
				parts := splitText(s, max, room)
				if len(parts) < 2 {
					t.Fatalf("%s/%d: one part", name, max)
				}
				for i, p := range parts {
					limit := max
					if i == len(parts)-1 {
						limit = max - room
					}
					if p == "" || utf16Len(p) > limit {
						t.Fatalf("%s/%d: part %d of %d units (limit %d)", name, max, i, utf16Len(p), limit)
					}
				}
				if !sameWords(s, parts) {
					t.Fatalf("%s/%d/%d: something was lost", name, max, room)
				}
			}
		}
	}
	// It fits whole but not with the room: its end goes on.
	if got := splitText(strings.Repeat("a ", 990), 2000, 200); len(got) != 2 || utf16Len(got[1]) > 1800 {
		t.Fatalf("room: %d parts", len(got))
	}
}

// Telegram: a long reply goes in parts of at most 4096, the buttons on the
// last; a tap whose « → » would not fit the message drops its buttons and
// says it below, in full.
func TestTelegramSendsInParts(t *testing.T) {
	c, _, f := telegramCore(t)
	linked(c)
	tg := tgOf(c)
	s := longText(10000)
	buttons := [][]botButton{{{Text: "✓ Oui", Data: "pc:1:y"}, {Text: "✕ Non", Data: "pc:1:n"}}}
	tg.post(botMessage{Text: s, Buttons: buttons})
	tg.pending.Wait()
	sent := f.sentMessages()
	var parts []string
	for i, m := range sent {
		text := fmt.Sprint(m["text"])
		if utf16Len(text) > tgTextMax {
			t.Fatalf("part %d: %d units", i, utf16Len(text))
		}
		if (m["reply_markup"] != nil) != (i == len(sent)-1) {
			t.Fatalf("buttons on part %d of %d", i, len(sent))
		}
		parts = append(parts, text)
	}
	if len(sent) < 3 || !sameWords(s, parts) {
		t.Fatalf("%d parts, words lost: %v", len(sent), !sameWords(s, parts))
	}

	full := strings.Repeat("x", tgTextMax-2)
	ev := botEvent{Ref: &tgCallback{ID: "cb", Message: &tgMessage{MessageID: 9, Chat: tgChat{ID: userTG}, Text: full}}}
	if err := tg.tr.answer(context.Background(), fakeBotToken, ev, "", "Fait"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	edited := f.edited
	f.mu.Unlock()
	if len(edited) != 1 || edited[0]["method"] != "editMessageReplyMarkup" || edited[0]["text"] != nil {
		t.Fatalf("edited %v", edited)
	}
	if got := lastText(f); got != "→ Fait" {
		t.Fatalf("after %q", got)
	}
	ev.Ref.(*tgCallback).Message.Text = "Court"
	tg.tr.answer(context.Background(), fakeBotToken, ev, "", "Fait")
	f.mu.Lock()
	last := f.edited[len(f.edited)-1]
	f.mu.Unlock()
	if last["method"] != "editMessageText" || last["text"] != "Court\n\n→ Fait" {
		t.Fatalf("edited %v", last)
	}
}

// Discord: plain content in parts of at most 2000, the buttons on the
// last; a tap whose « → » would not fit keeps the message as it is,
// without its buttons, and says it below.
func TestDiscordSendsInParts(t *testing.T) {
	c, b, f := discordCore(t)
	linkDiscord(t, c, b)
	before := len(f.sentNow())
	s := longText(7000) + strings.Repeat("🙂", 1500)
	b.post(botMessage{Text: s, Buttons: [][]botButton{{{Text: "✓ Oui", Data: "pc:1:y"}}}})
	b.pending.Wait()
	sent := f.sentNow()[before:]
	var parts []string
	for i, m := range sent {
		text := fmt.Sprint(m["content"])
		if utf16Len(text) > dcTextMax || m["embeds"] != nil {
			t.Fatalf("part %d: %d units", i, utf16Len(text))
		}
		if (m["components"] != nil) != (i == len(sent)-1) {
			t.Fatalf("buttons on part %d of %d", i, len(sent))
		}
		parts = append(parts, text)
	}
	if len(sent) < 5 || !sameWords(s, parts) {
		t.Fatalf("%d parts", len(sent))
	}

	x := &dcInteraction{ID: "i-1", Token: "itoken", ChannelID: "dm-" + dcUserID,
		Message: &dcMessage{Content: strings.Repeat("x", dcTextMax-2)}}
	if err := b.tr.answer(context.Background(), fakeDiscordToken, botEvent{Ref: x}, "", "Fait"); err != nil {
		t.Fatal(err)
	}
	cb := f.callbacksNow()
	data, _ := cb[len(cb)-1]["data"].(map[string]any)
	if _, ok := data["content"]; ok || fmt.Sprint(data["components"]) != "[]" {
		t.Fatalf("callback %v", cb[len(cb)-1])
	}
	if all := f.sentNow(); fmt.Sprint(all[len(all)-1]["content"]) != "→ Fait" {
		t.Fatalf("after %v", all[len(all)-1])
	}
}

// Signal takes a long text whole (signal-cli sends it as Signal's
// long-text attachment): one message, its legend after it.
func TestSignalSendsWhole(t *testing.T) {
	b, f := signalLinked(t)
	before := len(f.sentNow())
	s := longText(12000)
	b.post(botMessage{Text: s, Buttons: [][]botButton{{{Text: "✓ Oui", Data: "pc:1:y"}}}})
	b.pending.Wait()
	sent := f.sentNow()[before:]
	if len(sent) != 1 || fmt.Sprint(sent[0]["text"]) != s+"\n\n👍 Oui" {
		t.Fatalf("%d messages", len(sent))
	}
}

// A card goes to a channel whole: a monitor's arguments far over 200
// characters, a value of several lines with its lines.
func TestChannelCardWhole(t *testing.T) {
	c, _, _ := pollCore(t)
	query := strings.Repeat("is:unresolved project:site ", 20)
	spec := monitorSpec{Server: "monitor", Tool: "status", Args: map[string]any{"service": query}, Every: 5}
	if _, err := c.writeMonitor("etat", spec, nil); err != nil {
		t.Fatal(err)
	}
	m, _ := c.monitorNamed("etat")
	p := c.proposalOf(cardMonitor, m.ID)
	text := cardText(convoCard{Kind: cardMonitor}, p, "fr")
	if !strings.Contains(text, query) || strings.Contains(text, "…") {
		t.Fatalf("cut: %s", text)
	}
	p.fields, p.Fields = nil, []cardField{{Name: "Consignes", Value: "Ligne un\nLigne deux"}}
	if text := cardText(convoCard{Kind: cardMonitor}, p, "fr"); !strings.Contains(text, "Consignes :\nLigne un\nLigne deux") {
		t.Fatalf("lines: %s", text)
	}
}

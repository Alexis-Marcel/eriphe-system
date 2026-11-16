package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Suggested replies (#613). The brain may end an answer with up to three
// replies the user might give, in its own words, in a block that closes
// its text:
//
//	… la PR est prête.
//	<suggestions>
//	Fusionne-la
//	Montre-moi le diff
//	</suggestions>
//
// The workspace shows them as chips under its latest answer; a click sends
// the sentence as the user's message, in the same thread, typed by them
// (handleSend). A trailing block parsed by code, rather than a tool: its
// answer is the text after its last tool call (answerText), so a tool
// called after the answer would turn it into a working note, and one called
// before would suggest replies to an answer not written yet. The block is
// cut where the text is stored (copyBrainOutput) and where the draft is
// kept (brainWrote), so no reader of the conversation (the workspace, a
// channel, the voice, push, the memory) ever sees it.
//
//   - From the marker on, nothing is the brain's text, even unclosed or
//     followed by more: it fails closed.
//   - Kept beside the message (suggestKey), only from a clean turn: a turn
//     that read external content offers none, whatever it wrote
//     (suggestReplies), since its words may be someone else's.
//   - At most suggestMax, each a line of suggestRunes at most; a longer one
//     is dropped, never cut: a chip says the brain's sentence or nothing.

const (
	suggestOpen  = "<suggestions>"
	suggestClose = "</suggestions>"
	suggestMax   = 3
	suggestRunes = 80
)

// viaSuggestion is POST /messages' from for a suggested reply the user
// chose: their message, typed in the workspace, that never decides a card
// (no click does).
const viaSuggestion = "suggestion"

func suggestKey(seq int) string { return fmt.Sprintf("suggest_msg:%d", seq) }

// splitSuggestions parts what the brain wrote into its text and the
// replies it suggests.
func splitSuggestions(said string) (text string, replies []string) {
	i := strings.Index(said, suggestOpen)
	if i < 0 {
		return said, nil
	}
	block := said[i+len(suggestOpen):]
	if j := strings.Index(block, suggestClose); j >= 0 {
		block = block[:j]
	}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•"))
		n := len([]rune(line))
		if n == 0 || n > suggestRunes || containsStr(replies, line) || strings.ContainsAny(line, "<>") {
			continue
		}
		if replies = append(replies, line); len(replies) == suggestMax {
			break
		}
	}
	return textBefore(said[:i]), replies
}

// textBefore is the text before the block: without the space, nor a code
// fence opened around the block.
func textBefore(s string) string {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	return strings.TrimRightFunc(strings.TrimSuffix(s, "```"), unicode.IsSpace)
}

// draftText is a draft without the block of replies, nor the start of its
// marker at its end: what is shown of it never takes back a word.
func draftText(draft string) string {
	if i := strings.Index(draft, suggestOpen); i >= 0 {
		return textBefore(draft[:i])
	}
	for n := len(suggestOpen) - 1; n > 0; n-- {
		if strings.HasSuffix(draft, suggestOpen[:n]) {
			return textBefore(draft[:len(draft)-n])
		}
	}
	return draft
}

// suggestReplies keeps the replies of a message of the brain's, unless its
// turn read external content.
func (c *core) suggestReplies(seq int, replies []string) {
	if len(replies) == 0 || len(c.turnTaint()) > 0 {
		return
	}
	if raw, err := json.Marshal(replies); err == nil {
		c.store.set(suggestKey(seq), string(raw))
	}
}

// suggestions are the replies kept for a message of the brain's.
func (s *Store) suggestions(seq int) []string {
	var out []string
	json.Unmarshal([]byte(s.get(suggestKey(seq))), &out)
	return out
}

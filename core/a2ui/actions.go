package a2ui

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The one action of Eriphe's workspace (#425, #633, #704): a button asks
// Eriphe, and nothing else. Every surface is a block of a node's page.
//
//   - ask: a request to the brain, in the author's own words. Its event's
//     context is exactly {"text": "<literal>"}. The page names only the
//     block and the button; code reads the words again from the stored
//     block and sends them as the user's message to its node. It acts on
//     nothing: what Eriphe does next, if it acts, asks the user's yes on a
//     card, which code draws from its records, never a block.
//
// Any other name, and any action that is not an event (a function call),
// is refused. No action calls a tool.
const (
	ActionAsk = "ask"

	// AskTextMax is the longest ask, in characters.
	AskTextMax = 1000
)

// CheckActions checks the actions of a block's components against the
// grammar, as an author writes them: an ask event with a literal text
// only, never a function call. Each error's path points at the action in
// the components list ("/components/2/action/event").
func CheckActions(surfaceID string, components json.RawMessage) []ValidationError {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return nil
	}
	var errs []ValidationError
	for i, comp := range list {
		fail := func(path, msg string) {
			errs = append(errs, ValidationError{Code: CodeValidationFailed, SurfaceID: surfaceID, Path: path, Message: msg})
		}
		if comp["component"] == "Asks" {
			for n, ask := range AskItems(comp) {
				if ask.Text != "" && BareYes(ask.Text) { // "": hidden by code (setup_buttons.go)
					fail("/components/"+strconv.Itoa(i)+"/items/"+strconv.Itoa(n), strconv.Quote(ask.Text)+" asks nothing: an ask says what it asks, its verb first (« Envoie la réponse », « Jette les 102 »); a yes is a card's")
				}
			}
		}
		a, ok := comp["action"].(map[string]any)
		if !ok {
			continue
		}
		at := "/components/" + strconv.Itoa(i) + "/action"
		ev, ok := a["event"].(map[string]any)
		if !ok {
			fail(at, `an action is an ask event: {"event": {"name": "ask", "context": {"text": "…"}}}`)
			continue
		}
		at += "/event"
		name, _ := ev["name"].(string)
		ctx, _ := ev["context"].(map[string]any)
		if bindsData(ev["userMessage"]) {
			fail(at+"/userMessage", "an action's userMessage is literal text, never a value bound to the data")
			continue
		}
		if name != ActionAsk {
			fail(at+"/name", "no action "+strconv.Quote(name)+": a button only asks Eriphe (ask); a yes is a card, which code draws")
			continue
		}
		text, ok := ctx["text"].(string)
		switch {
		case bindsData(ctx):
			fail(at, "an ask carries your own words only: literal text, never a value bound to the data")
		case !ok || strings.TrimSpace(text) == "" || len(ctx) != 1:
			fail(at, `an ask's context is {"text": "<your request, in words>"} and nothing else`)
		case len([]rune(text)) > AskTextMax:
			fail(at+"/context/text", "an ask is at most "+strconv.Itoa(AskTextMax)+" characters")
		}
	}
	return errs
}

// Ask is one ask of an Asks: its words, and what it sets up (its until:
// code hides it once that holds), if anything.
type Ask struct {
	Text  string
	Until map[string]any
}

// AskItems are the asks of an Asks component, in its order: an item
// written as text, or as {text, until}; an item that is neither keeps
// its place with no text.
func AskItems(comp map[string]any) []Ask {
	items, _ := comp["items"].([]any)
	out := make([]Ask, 0, len(items))
	for _, it := range items {
		var a Ask
		switch x := it.(type) {
		case string:
			a.Text = x
		case map[string]any:
			a.Text, _ = x["text"].(string)
			a.Until, _ = x["until"].(map[string]any)
		}
		a.Text = strings.TrimSpace(a.Text)
		out = append(out, a)
	}
	return out
}

// bareYes are the words that answer without saying what: a button that
// says only one of them asks nothing (an ask says its verb).
var bareYes = map[string]bool{"oui": true, "non": true, "ok": true, "okay": true, "d'accord": true, "ouais": true, "vas-y": true, "go": true, "yes": true, "no": true, "sure": true}

// BareYes tells whether a button's words are only a yes or a no.
func BareYes(text string) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(text), " .!?…✓"))
	return t == "" || bareYes[strings.ReplaceAll(t, "’", "'")]
}

// AskText is the text of the ask written on a component of the list, as
// its author wrote it; ok false when the component has none. An ask of
// an Asks is named by the component's id and its place, "<id>#<n>"
// (from 0).
func AskText(components json.RawMessage, id string) (string, bool) {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return "", false
	}
	id, at, many := strings.Cut(id, "#")
	for _, comp := range list {
		if cid, _ := comp["id"].(string); cid != id {
			continue
		}
		if comp["component"] == "Asks" {
			n, err := strconv.Atoi(at)
			asks := AskItems(comp)
			if !many || err != nil || n < 0 || n >= len(asks) || asks[n].Text == "" {
				return "", false
			}
			return asks[n].Text, true
		}
		if many {
			return "", false
		}
		a, _ := comp["action"].(map[string]any)
		ev, _ := a["event"].(map[string]any)
		ctx, _ := ev["context"].(map[string]any)
		text, ok := ctx["text"].(string)
		if name, _ := ev["name"].(string); name != ActionAsk || !ok || strings.TrimSpace(text) == "" {
			return "", false
		}
		return strings.TrimSpace(text), true
	}
	return "", false
}

// bindsData tells whether a value holds a binding or a function call.
func bindsData(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["@path"]; ok {
			return true
		}
		if _, ok := x["@call"]; ok {
			return true
		}
		for _, next := range x {
			if bindsData(next) {
				return true
			}
		}
	case []any:
		for _, next := range x {
			if bindsData(next) {
				return true
			}
		}
	}
	return false
}

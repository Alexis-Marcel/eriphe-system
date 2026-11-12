package a2ui

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The actions of Eriphe's workspace are a small closed grammar (#425): a
// click does only what is safe, and code checks it both when an author
// writes it and when the page sends it.
//
//   - ask: a request to the brain, in the author's own words. Its event's
//     context is exactly {"text": "<literal>"}; code adds the surface and
//     the component.
//   - answer: the user's yes or no to one proposal, {"proposal": "<ref>",
//     "answer": "yes"|"no"}. Only the Proposal component sends it, for the
//     proposal it shows: an author never writes one.
//   - local: open, back, expand, tab. The page's own gestures; core records
//     them ("open", the user opened a tile or a chip, also places the
//     surface first and reads its sources again; "back" gives the layout
//     back).
//
// Any other name, and any action that is not an event (a function call),
// is refused. No action calls a tool that writes.
const (
	ActionAsk    = "ask"
	ActionAnswer = "answer"
	ActionOpen   = "open"
	ActionBack   = "back"
	ActionExpand = "expand"
	ActionTab    = "tab"

	// The kinds of action.
	KindAsk    = "ask"
	KindAnswer = "answer"
	KindLocal  = "local"

	// AskTextMax is the longest ask, in characters.
	AskTextMax = 1000
)

// LocalActions are the names done in the page.
var LocalActions = []string{ActionOpen, ActionBack, ActionExpand, ActionTab}

// ActionKind is the kind of an action name: KindAsk, KindAnswer, KindLocal,
// or "" for a name outside the grammar.
func ActionKind(name string) string {
	switch name {
	case ActionAsk:
		return KindAsk
	case ActionAnswer:
		return KindAnswer
	case ActionOpen, ActionBack, ActionExpand, ActionTab:
		return KindLocal
	}
	return ""
}

// CheckActions checks the actions of a surface's components against the
// grammar, as an author writes them: an event, ask with a literal text
// only, or a local name with no context; never answer, never a function
// call. Each error's path points at the action in the components list
// ("/components/2/action/event").
func CheckActions(surfaceID string, components json.RawMessage) []ValidationError {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return nil
	}
	var errs []ValidationError
	for i, comp := range list {
		a, ok := comp["action"].(map[string]any)
		if !ok {
			continue
		}
		at := "/components/" + strconv.Itoa(i) + "/action"
		fail := func(path, msg string) {
			errs = append(errs, ValidationError{Code: CodeValidationFailed, SurfaceID: surfaceID, Path: path, Message: msg})
		}
		ev, ok := a["event"].(map[string]any)
		if !ok {
			fail(at, "an action is an event: ask, or a local name ("+strings.Join(LocalActions, ", ")+")")
			continue
		}
		at += "/event"
		name, _ := ev["name"].(string)
		ctx, _ := ev["context"].(map[string]any)
		if bindsData(ev["userMessage"]) {
			fail(at+"/userMessage", "an action's userMessage is literal text, never a value bound to the data")
			continue
		}
		switch ActionKind(name) {
		case KindAsk:
			text, ok := ctx["text"].(string)
			switch {
			case bindsData(ctx):
				fail(at, "an ask carries your own words only: literal text, never a value bound to the data")
			case !ok || strings.TrimSpace(text) == "" || len(ctx) != 1:
				fail(at, `an ask's context is {"text": "<your request, in words>"} and nothing else`)
			case len([]rune(text)) > AskTextMax:
				fail(at+"/context/text", "an ask is at most "+strconv.Itoa(AskTextMax)+" characters")
			}
		case KindLocal:
			if len(ctx) > 0 {
				fail(at+"/context", "a local action ("+name+") carries no context")
			}
		case KindAnswer:
			fail(at+"/name", "answer is sent by a Proposal component only, for the proposal it shows: write a Proposal")
		default:
			fail(at+"/name", "no action "+strconv.Quote(name)+": a button asks Eriphe (ask) or acts in the page ("+strings.Join(LocalActions, ", ")+")")
		}
	}
	return errs
}

// AskText is the text of the ask written on a component of the list, as
// its author wrote it; ok false when the component has none.
func AskText(components json.RawMessage, id string) (string, bool) {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return "", false
	}
	for _, comp := range list {
		if cid, _ := comp["id"].(string); cid != id {
			continue
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

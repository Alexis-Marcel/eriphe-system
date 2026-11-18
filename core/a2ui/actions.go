package a2ui

import (
	"encoding/json"
	"regexp"
	"slices"
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
//     "answer": "yes"|"no"}, and "at": its place in the data when it is bound
//     inside a list. Only the Proposal component sends it, for the proposal
//     it shows: an author never writes one.
//   - local: open, back, expand, tab. The page's own gestures; core records
//     them ("open", the user opened a tile or a chip, also places the
//     surface first and reads its sources again; "back" gives the layout
//     back). An author's carries no context. Code alone places an open
//     that names another surface, {"surface": "<id>"}, on a thread's own
//     surface: its child's row, a child's crumb back to its parent. The
//     page opens that surface as it opens a tile; nothing reaches the
//     brain.
//   - tidy: « Ranger », the user puts an ended thread away, {"thread":
//     <id>}. Only code places it, on the thread's own surface.
//   - control: the user stops a thread, {"thread": <id>, "do": "stop"}.
//     Only code places it, on the thread's own surface.
//   - offer: the user takes an action offered on an item of a thread, or
//     on a group of them, {"thread": <id>, "source": "<name>", "item":
//     "<id>" or "state": "<state>", "offer": "<key>"}: a card is made for
//     it, nothing runs. Only the Offers component sends it, one button per
//     offer code put in the thread's data (/thread/items): an author never
//     writes one, and places an Offers on a thread's own surface only.
//   - mark: the user keeps, drops or marks done something a thread shows,
//     {"list": "<id>", "item": "<id>", "mark": "keep"|"drop"|"done"|""}
//     ("" clears it): ids only, never text; the surface names the thread.
//     Only the Mark component sends it, one of the marks it offers: an
//     author places a Mark on a thread's own surface only, list and item
//     each a literal id or bound to the data (a row of a list). Code
//     records it in the thread's data, never in the composed part, and
//     starts no turn.
//
// Any other name, and any action that is not an event (a function call),
// is refused. No action calls a tool that writes.
const (
	ActionAsk     = "ask"
	ActionAnswer  = "answer"
	ActionTidy    = "tidy"
	ActionControl = "control"
	ActionOffer   = "offer"
	ActionMark    = "mark"
	ActionOpen    = "open"
	ActionBack    = "back"
	ActionExpand  = "expand"
	ActionTab     = "tab"

	// The kinds of action.
	KindAsk     = "ask"
	KindAnswer  = "answer"
	KindTidy    = "tidy"
	KindControl = "control"
	KindOffer   = "offer"
	KindMark    = "mark"
	KindLocal   = "local"

	// AskTextMax is the longest ask, in characters.
	AskTextMax = 1000

	// The marks a Mark offers; a mark action sets one, or "" to clear it.
	MarkKeep = "keep"
	MarkDrop = "drop"
	MarkDone = "done"
)

// markIDRe is a mark's list or item: an id, never text, and a safe token
// of a JSON pointer (no / nor ~).
var markIDRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:@=+-]{0,127}$`)

// MarkIDOK tells whether a mark's list or item is an id it may carry.
func MarkIDOK(s string) bool { return markIDRe.MatchString(s) }

// LocalActions are the names done in the page.
var LocalActions = []string{ActionOpen, ActionBack, ActionExpand, ActionTab}

// ActionKind is the kind of an action name: KindAsk, KindAnswer, KindTidy,
// KindLocal, or "" for a name outside the grammar.
func ActionKind(name string) string {
	switch name {
	case ActionAsk:
		return KindAsk
	case ActionAnswer:
		return KindAnswer
	case ActionTidy:
		return KindTidy
	case ActionControl:
		return KindControl
	case ActionOffer:
		return KindOffer
	case ActionMark:
		return KindMark
	case ActionOpen, ActionBack, ActionExpand, ActionTab:
		return KindLocal
	}
	return ""
}

// ThreadOnly are the components that send an action of a thread's: on a
// thread's own surface only.
var ThreadOnly = []string{"Mark", "Offers"}

// CheckActions checks the actions of a surface's components against the
// grammar, as an author writes them: an event, ask with a literal text
// only, a local name with no context; never answer, mark or offer (their
// components send them), never a function call. A Mark or an Offers is on
// a thread's surface only (thread; 0 for a surface that is no thread's).
// Each error's path points at the action in the components list
// ("/components/2/action/event"), or at the component.
func CheckActions(surfaceID string, thread int64, components json.RawMessage) []ValidationError {
	var list []map[string]any
	if json.Unmarshal(components, &list) != nil {
		return nil
	}
	var errs []ValidationError
	for i, comp := range list {
		fail := func(path, msg string) {
			errs = append(errs, ValidationError{Code: CodeValidationFailed, SurfaceID: surfaceID, Path: path, Message: msg})
		}
		if name, _ := comp["component"].(string); slices.Contains(ThreadOnly, name) && thread == 0 {
			fail("/components/"+strconv.Itoa(i), name+" is placed on a thread's surface only")
		}
		a, ok := comp["action"].(map[string]any)
		if !ok {
			continue
		}
		at := "/components/" + strconv.Itoa(i) + "/action"
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
		case KindMark:
			fail(at+"/name", "mark is sent by a Mark component only, on a thread's surface: write a Mark")
		case KindOffer:
			fail(at+"/name", "offer is sent by an Offers component only, bound to the offers code puts in a thread's data: write an Offers")
		case KindAnswer:
			fail(at+"/name", "answer is sent by a Proposal component only, for the proposal it shows: write a Proposal")
		case KindTidy:
			fail(at+"/name", "tidy is placed by code only, on an ended thread's own surface")
		case KindControl:
			fail(at+"/name", name+" is placed by code only, on a thread's own surface")
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

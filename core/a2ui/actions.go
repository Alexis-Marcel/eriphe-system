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
//   - stepDone: the user did a step of a thread that is theirs ("C'est
//     fait"), {"thread": <id>, "step": "<key>"}. Only code places it, on
//     the thread's own surface: an author never writes one.
//   - tidy: « Ranger », the user puts an ended thread away, {"thread":
//     <id>}. Only code places it, on the thread's own surface.
//   - choose: the user picks an option of a thread's choice step,
//     {"thread": <id>, "step": "<key>", "option": "<option>"}. Only code
//     places it, one button per option, on the thread's own surface.
//   - control: the user pauses, resumes or stops a thread that runs,
//     {"thread": <id>, "do": "pause"|"resume"|"stop"}. Only code places
//     it, on the thread's own surface.
//   - offer: the user takes an action offered on an item of a thread, or
//     on a group of them, {"thread": <id>, "source": "<name>", "item":
//     "<id>" or "state": "<state>", "offer": "<key>"}: a card is made for
//     it, nothing runs. Only code places it, on the thread's own surface.
//   - mark: the user keeps, drops or marks done something a thread shows,
//     {"thread": <id>, "list": "<id>", "item": "<id>", "mark":
//     "keep"|"drop"|"done"|""} ("" clears it): ids only, never text. An
//     author places it on its own thread's surface only (thread: that
//     thread, a literal), mark a literal, list and item each a literal id
//     or bound to the data (a row of a list). Code records it in the
//     thread's data, never in the composed part, and starts no turn.
//
// Any other name, and any action that is not an event (a function call),
// is refused. No action calls a tool that writes.
const (
	ActionAsk      = "ask"
	ActionAnswer   = "answer"
	ActionStepDone = "stepDone"
	ActionTidy     = "tidy"
	ActionChoose   = "choose"
	ActionControl  = "control"
	ActionOffer    = "offer"
	ActionMark     = "mark"
	ActionOpen     = "open"
	ActionBack     = "back"
	ActionExpand   = "expand"
	ActionTab      = "tab"

	// The kinds of action.
	KindAsk     = "ask"
	KindAnswer  = "answer"
	KindStep    = "step"
	KindTidy    = "tidy"
	KindChoose  = "choose"
	KindControl = "control"
	KindOffer   = "offer"
	KindMark    = "mark"
	KindLocal   = "local"

	// AskTextMax is the longest ask, in characters.
	AskTextMax = 1000

	// The marks.
	MarkKeep = "keep"
	MarkDrop = "drop"
	MarkDone = "done"
)

// Marks are the marks a mark action sets; "" clears one.
var Marks = []string{MarkKeep, MarkDrop, MarkDone}

// markIDRe is a mark's list or item: an id, never text, and a safe token
// of a JSON pointer (no / nor ~).
var markIDRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:@=+-]{0,127}$`)

// MarkIDOK tells whether a mark's list or item is an id it may carry.
func MarkIDOK(s string) bool { return markIDRe.MatchString(s) }

// MarkOK tells whether a mark is one of Marks, or "" to clear it.
func MarkOK(s string) bool { return s == "" || slices.Contains(Marks, s) }

// LocalActions are the names done in the page.
var LocalActions = []string{ActionOpen, ActionBack, ActionExpand, ActionTab}

// ActionKind is the kind of an action name: KindAsk, KindAnswer, KindStep, KindTidy,
// KindLocal, or "" for a name outside the grammar.
func ActionKind(name string) string {
	switch name {
	case ActionAsk:
		return KindAsk
	case ActionAnswer:
		return KindAnswer
	case ActionStepDone:
		return KindStep
	case ActionTidy:
		return KindTidy
	case ActionChoose:
		return KindChoose
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

// CheckActions checks the actions of a surface's components against the
// grammar, as an author writes them: an event, ask with a literal text
// only, a local name with no context, or mark on the thread whose surface
// it is (thread; 0 for a surface that is no thread's); never answer,
// never a function call. Each error's path points at the action in the
// components list ("/components/2/action/event").
func CheckActions(surfaceID string, thread int64, components json.RawMessage) []ValidationError {
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
		case KindMark:
			if msg := checkMark(ctx, thread); msg != "" {
				fail(at+"/context", msg)
			}
		case KindAnswer:
			fail(at+"/name", "answer is sent by a Proposal component only, for the proposal it shows: write a Proposal")
		case KindStep:
			fail(at+"/name", "stepDone is placed by code only, on a thread's own surface, for a step the user does")
		case KindTidy:
			fail(at+"/name", "tidy is placed by code only, on an ended thread's own surface")
		case KindChoose, KindControl, KindOffer:
			fail(at+"/name", name+" is placed by code only, on a thread's own surface")
		default:
			fail(at+"/name", "no action "+strconv.Quote(name)+": a button asks Eriphe (ask), marks something its thread shows (mark) or acts in the page ("+strings.Join(LocalActions, ", ")+")")
		}
	}
	return errs
}

// markShape is a mark's context, as its errors say it.
const markShape = `a mark's context is {"thread": <this thread's id>, "list": "<id>", "item": "<id>", "mark": "keep"|"drop"|"done"|""}`

// checkMark checks a mark's context as its author writes it: on the
// thread whose surface it is, the mark a literal, list and item each a
// literal id or a binding, never a function call; "" when it holds.
func checkMark(ctx map[string]any, thread int64) string {
	n, isNum := ctx["thread"].(float64)
	mark, isStr := ctx["mark"].(string)
	switch {
	case thread == 0:
		return "mark is placed on a thread's surface only"
	case len(ctx) != 4 || !isNum || !isStr:
		return markShape
	case n != float64(thread):
		return "a mark is placed on its own thread only: thread " + strconv.FormatInt(thread, 10)
	case !MarkOK(mark):
		return `mark is keep, drop, done, or "" to clear it`
	}
	for _, k := range []string{"list", "item"} {
		switch v := ctx[k].(type) {
		case string:
			if !MarkIDOK(v) {
				return k + " is an id (letters, digits and _.:@=+-, 128 at most), never text"
			}
		case map[string]any:
			if p, ok := v["@path"].(string); !ok || p == "" || len(v) != 1 {
				return k + ` is a literal id or a binding {"@path": …}, never a function call`
			}
		default:
			return markShape
		}
	}
	return ""
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

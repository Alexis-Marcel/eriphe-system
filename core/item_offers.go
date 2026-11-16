package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// Actions offered on an item (#575): whoever notes an item (note_item)
// may attach the actions the user could take on it: a tool of the
// catalog and its input, "$item" where the item's id goes. Code fills it
// in, never the model; the item's row then offers each as a button, and
// a state group whose items offer the same action offers it on them all
// (« Archiver · 24 »).
//
//   - Checked when noted: the tool is in the catalog, not one Eriphe
//     provides, the author may use it (a task: lent it), its input names
//     the item ("$item") and fits the tool's input schema, and the access
//     policy does not refuse it (never). Checked again when shown and when
//     clicked: a never tool is never offered.
//   - Its offers are code's data, on its row and its group in
//     /thread/items/<source>; an Offers component shows them, one button
//     each: code's default composition places one, and so may the
//     holder's block that shows those rows (bound under /thread/items
//     only, offersBound). The page sends one of them as is (action
//     offer); nobody writes one.
//   - A click never runs a tool: code reads the note again from the store, never
//     the page, and makes one card bound to that item (kind calls,
//     item_cards.go) with the exact call; a group's click one card with
//     its N exact calls, one per item, on the items shown in that group.
//     The user's Oui on that card runs them as kept; the same call on the
//     same item already waiting is not asked twice.
//   - One action an item at most: the one its author recommends, only
//     when useful; Eriphe is not a menu of buttons. A folded group (none,
//     done) shows no row's button, only the action its rows share.
//   - Its words are never a run's: a skill declares its actions once, in
//     its SKILL.md (actions:, each a label, a tool and the arguments it
//     must have), approved by the user with it; an action of its run's
//     note that matches one says that label, tainted note or not (the
//     user approved those words, the run only chose the item). Any other
//     action of a task says the tool's own words (toolWords: its title in
//     the catalog, else its name). The brain's and a lead's say their
//     label, but for a note written after reading external content: the
//     tool's words then. Its card is code's words, and keeps the note's
//     taint (« extérieur », buttons only).

const (
	offerItemArg  = "$item" // where the item's id goes in an action's input
	offersMax     = 1       // actions on one note: the one recommended
	offerLabelMax = 40      // runes of an action's label
	offerArgsMax  = 2000    // bytes of an action's input
)

// noteAction is an action offered on a noted item.
type noteAction struct {
	Label string         `json:"label" jsonschema:"what the button says, a verb in the user's language, a few words: « Archiver »"`
	Tool  string         `json:"tool" jsonschema:"the tool, server/tool: one you may use"`
	Args  map[string]any `json:"args" jsonschema:"its exact input, with the string \"$item\" as the value where the item's id goes"`
}

// key is an action's identity: its tool and its input.
func (a noteAction) key() string {
	raw, _ := json.Marshal(a.Args) // map keys sorted
	sum := sha256.Sum256([]byte(a.Tool + "\x00" + string(raw)))
	return hex.EncodeToString(sum[:6])
}

// bound is its input on one item: "$item" made the item's id.
func (a noteAction) bound(item string) map[string]any {
	out, _ := bindItem(a.Args, item).(map[string]any)
	return out
}

func bindItem(v any, item string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = bindItem(e, item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = bindItem(e, item)
		}
		return out
	case string:
		if x == offerItemArg {
			return item
		}
	}
	return v
}

// namesItem tells whether an input has "$item" as a value somewhere.
func namesItem(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			if namesItem(e) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(x, namesItem)
	case string:
		return x == offerItemArg
	}
	return false
}

// toolName is a tool as the catalog names it (mcp__server__tool), from
// server/tool or that name.
func toolName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if _, _, ok := splitToolName(name); ok {
		return name, true
	}
	server, tool, ok := strings.Cut(name, "/")
	if !ok || server == "" || tool == "" || strings.ContainsAny(tool, "/ ") {
		return "", false
	}
	return claudeToolName(server, tool), true
}

// offerAllowed tells whether an action on an item may be offered now: its
// tool is there and the policy does not refuse it.
func (c *core) offerAllowed(tool string, input map[string]any) bool {
	server, name, ok := splitToolName(tool)
	if !ok || providedServer(server) || c.pool == nil {
		return false
	}
	u := c.pool.get(server)
	if u == nil || u.toolByClaudeName(name) == nil {
		return false
	}
	f := c.pool.facts(tool, input)
	return !f.noAccount && f.value != gridNever
}

// checkActions checks the actions offered on an item when noted; may
// tells whether the author may use a tool (nil: any of the catalog).
func (c *core) checkActions(list []noteAction, item string, may func(tool string) error) ([]noteAction, error) {
	if len(list) > offersMax {
		return nil, fmt.Errorf("%d action on an item at most: the one you recommend, only when useful", offersMax)
	}
	var out []noteAction
	seen := map[string]bool{}
	for i, a := range list {
		at := fmt.Sprintf("actions[%d]: ", i)
		a.Label = oneLine(a.Label)
		name, ok := toolName(a.Tool)
		switch {
		case a.Label == "" || utf8.RuneCountInString(a.Label) > offerLabelMax:
			return nil, fmt.Errorf("%sa label of %d characters at most", at, offerLabelMax)
		case !ok:
			return nil, fmt.Errorf("%stool is server/tool", at)
		case !namesItem(map[string]any(a.Args)):
			return nil, fmt.Errorf("%sits args give the item as the value %q: code puts its id there", at, offerItemArg)
		}
		if raw, _ := json.Marshal(a.Args); len(raw) > offerArgsMax {
			return nil, fmt.Errorf("%sargs of %d bytes at most", at, offerArgsMax)
		}
		a.Tool = name
		if may != nil {
			if err := may(name); err != nil {
				return nil, fmt.Errorf("%s%v", at, err)
			}
		}
		input := a.bound(item)
		if !c.offerAllowed(name, input) {
			return nil, fmt.Errorf("%s%s is not a tool you may offer: not in the catalog, Eriphe's own, or refused by the user's access policy", at, a.Tool)
		}
		if err := fitsSchema(c.inputSchema(name), input); err != nil {
			return nil, fmt.Errorf("%s%v", at, err)
		}
		if seen[a.key()] {
			continue
		}
		seen[a.key()] = true
		out = append(out, a)
	}
	return out, nil
}

// fitsSchema checks an input against the top of a tool's input schema:
// its required properties, and no other when it allows none.
func fitsSchema(schema, input map[string]any) error {
	if schema == nil {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if name, _ := r.(string); name != "" {
				if _, ok := input[name]; !ok {
					return fmt.Errorf("the tool needs %q", name)
				}
			}
		}
	}
	if extra, ok := schema["additionalProperties"].(bool); ok && !extra && props != nil {
		for name := range input {
			if _, ok := props[name]; !ok {
				return fmt.Errorf("the tool takes no %q", name)
			}
		}
	}
	return nil
}

// skillAction is an action a skill declares in its SKILL.md (actions:):
// « Archiver: mail/move_message folder=Archive », its label, its tool,
// and the arguments an action must have to be it.
type skillAction struct {
	Label string
	Tool  string            // mcp__server__tool
	Args  map[string]string // each argument it must have, as text
}

const skillActionsMax = 8

// parseSkillAction reads « label: server/tool key=value … ».
func parseSkillAction(line string) (skillAction, bool) {
	line = strings.Trim(strings.TrimSpace(line), `"'`)
	i := strings.LastIndex(line, ": ")
	if i < 0 {
		return skillAction{}, false
	}
	a := skillAction{Label: oneLine(line[:i]), Args: map[string]string{}}
	fields := strings.Fields(line[i+2:])
	if len(fields) == 0 || a.Label == "" || utf8.RuneCountInString(a.Label) > offerLabelMax {
		return skillAction{}, false
	}
	name, ok := toolName(fields[0])
	if !ok {
		return skillAction{}, false
	}
	a.Tool = name
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return skillAction{}, false
		}
		a.Args[k] = v
	}
	return a, true
}

// matches tells whether an action noted is this declared one: its tool,
// and each argument it must have.
func (s skillAction) matches(a noteAction) bool {
	if s.Tool != a.Tool {
		return false
	}
	for k, v := range s.Args {
		got, ok := a.Args[k]
		if !ok || fmt.Sprint(got) != v {
			return false
		}
	}
	return true
}

// skillActionsOf are the actions a skill declares, in its approved
// version; none for a skill off or unknown.
func (c *core) skillActionsOf(name string) []skillAction {
	ks, err := c.store.skills()
	if err != nil {
		return nil
	}
	for _, k := range ks {
		if k.Name == name && k.Active() {
			if m, ok := c.skillManifestOf(k.Approved); ok {
				return m.Actions
			}
		}
	}
	return nil
}

// toolWords is a tool as a button says it: its title in the catalog (its
// server's words for it), else its name (callVerb).
func (c *core) toolWords(tool, lang string) string {
	if server, name, ok := splitToolName(tool); ok && c.pool != nil {
		if u := c.pool.get(server); u != nil {
			if t := u.toolByClaudeName(name); t != nil {
				title := strings.TrimSpace(t.Title)
				if title == "" && t.Annotations != nil {
					title = strings.TrimSpace(t.Annotations.Title)
				}
				if title != "" {
					return truncate(oneLine(title), offerLabelMax)
				}
			}
		}
	}
	return callVerb(tool+" {}", lang)
}

// offerWords says what an action's button says (offerLabel), the
// actions skills declare read once.
type offerWords struct {
	c     *core
	lang  string
	skill map[string][]skillAction
}

func (c *core) offerWords(lang string) *offerWords {
	return &offerWords{c: c, lang: lang, skill: map[string][]skillAction{}}
}

// label is what an action's button says: a skill's run's, the label its
// skill declares for it; another task's, the tool's words; the brain's or
// a lead's, its label, but the tool's words for a note written after
// reading external content.
func (w *offerWords) label(a noteAction, n itemNote) string {
	if name, ok := strings.CutPrefix(n.Author, "skill:"); ok {
		decl, seen := w.skill[name]
		if !seen {
			decl = w.c.skillActionsOf(name)
			w.skill[name] = decl
		}
		for _, d := range decl {
			if d.matches(a) {
				return d.Label
			}
		}
		return w.c.toolWords(a.Tool, w.lang)
	}
	if byTask(n.Author) || n.Tainted != "" || a.Label == "" {
		return w.c.toolWords(a.Tool, w.lang)
	}
	return a.Label
}

// rowOffers are the actions a row offers, as its buttons bind them: those
// of its note the policy still allows.
func (c *core) rowOffers(thread int64, n itemNote, words *offerWords) []any {
	var out []any
	for _, a := range n.Actions {
		if !c.offerAllowed(a.Tool, a.bound(n.Item)) {
			continue
		}
		out = append(out, map[string]any{"label": words.label(a, n), "thread": thread, "source": n.Source, "item": n.Item, "offer": a.key(), "tool": a.Tool})
	}
	return out
}

// groupOffers are the actions a state group offers on its items: each
// that at least least of its rows offer, with how many.
func groupOffers(thread int64, source, state string, rows []any, least int, words *offerWords) []any {
	type group struct {
		label string
		n     int
	}
	var keys []string
	by := map[string]*group{}
	for _, r := range rows {
		offers, _ := r.(map[string]any)["offers"].([]any)
		for _, o := range offers {
			o := o.(map[string]any)
			k := o["offer"].(string)
			g, ok := by[k]
			if !ok {
				g = &group{label: o["label"].(string)}
				by[k] = g
				keys = append(keys, k)
			}
			if g.label != o["label"] {
				g.label = words.c.toolWords(o["tool"].(string), words.lang) // labels that differ: the tool's own words
			}
			g.n++
		}
	}
	var out []any
	for _, k := range keys {
		if g := by[k]; g.n >= least {
			out = append(out, map[string]any{"label": fmt.Sprintf(tr(words.lang, "thread.offer.group"), g.label, g.n), "thread": thread, "source": source, "state": state, "offer": k})
		}
	}
	return out
}

// offerKeys are the keys of an offer the page sends back, as code wrote
// them in its row's or group's data: the rest (its label, its tool) is
// for the button only.
var offerKeys = []string{"thread", "source", "item", "state", "offer"}

// offersBound tells whether an Offers' binding, at each place it is
// rendered (paths), reads code's offers: under /thread/items, which no
// author writes.
func offersBound(paths []string) bool {
	return len(paths) > 0 && !slices.ContainsFunc(paths, func(p string) bool {
		return !strings.HasPrefix(p, "/"+threadDataKey+"/items/")
	})
}

// offersGuard keeps the Offers of a block written on a thread's surface
// to code's offers: each bound under /thread/items, wherever it is
// rendered (offersBound).
func offersGuard(sid string, comps json.RawMessage) []a2ui.ValidationError {
	var errs []a2ui.ValidationError
	for _, b := range a2ui.Eriphe().BoundPaths(comps) {
		if b.Type == "Offers" && b.Prop == "offers" && !offersBound([]string{b.Path}) {
			errs = append(errs, a2ui.ValidationError{Code: a2ui.CodeValidationFailed, SurfaceID: sid, Path: "/components" + b.At,
				Message: "an Offers binds the offers code puts on a row or a group of /thread/items/<source> (" + b.Path + " is not one)"})
		}
	}
	return errs
}

// offerOf is what an offer click asks: the thread, the source, the item
// (a row's) or the state (a group's), and the action. The clicked
// component is an Offers of the thread's own surface, bound to code's
// offers (offersBound), and the page sent one of those its binding takes
// in the data, as code wrote it; the rest is read again from the store
// (offerCalls).
func offerOf(s surface, a a2ui.Action) (thread int64, source, item, state, key string, refused *errActionRefused) {
	th, ok := threadOfSurface(s.ID)
	if !ok || s.Author != threadAuthor(th) {
		return 0, "", "", "", "", &errActionRefused{Message: "an offer is a thread's own: shown by an Offers on its surface"}
	}
	comp, at, ok := componentOf(s, a.SourceComponentID)
	if !ok || comp["component"] != "Offers" {
		return 0, "", "", "", "", &errActionRefused{Path: at, Message: "an offer comes from an Offers component"}
	}
	paths, values := boundValues(s, at, "offers")
	if !offersBound(paths) {
		return 0, "", "", "", "", &errActionRefused{Path: at, Message: "an Offers shows the offers code puts under /thread/items, and no other"}
	}
	if !slices.ContainsFunc(values, func(v any) bool { return sentOffer(v, a.Context) }) {
		return 0, "", "", "", "", &errActionRefused{Path: at, Message: "this Offers shows no such offer"}
	}
	str := func(k string) string {
		var v string
		json.Unmarshal(a.Context[k], &v)
		return v
	}
	var got int64
	_, hasItem := a.Context["item"]
	_, hasState := a.Context["state"]
	if json.Unmarshal(a.Context["thread"], &got) != nil || got != th || len(a.Context) != 4 || hasItem == hasState || hasItem && str("item") == "" || str("source") == "" || !offerKey(str("offer")) {
		return 0, "", "", "", "", &errActionRefused{Path: at, Message: `an offer's context is {"thread", "source", "item" or "state", "offer"}, for this thread`}
	}
	return th, str("source"), str("item"), str("state"), str("offer"), nil
}

// sentOffer tells whether the page sent one of a list of offers as code
// wrote it: its offerKeys, each the same, and nothing else.
func sentOffer(list any, sent map[string]json.RawMessage) bool {
	offers, _ := list.([]any)
	return slices.ContainsFunc(offers, func(o any) bool {
		m, _ := o.(map[string]any)
		n := 0
		for _, k := range offerKeys {
			want, has := m[k]
			raw, sends := sent[k]
			if has != sends {
				return false
			}
			if !has {
				continue
			}
			var got any
			if json.Unmarshal(raw, &got) != nil || fmt.Sprint(got) != fmt.Sprint(want) {
				return false
			}
			n++
		}
		return n > 0 && n == len(sent)
	})
}

// errOfferGone is an offer the store no longer has as clicked.
var errOfferGone = errors.New("no such action on that item any more")

// offerCalls are the exact calls an offer click asks for: on the item, the
// action its note offers; on a state group, that action on each item of
// the group shown that offers it. The note, its state and the item's
// being listed are read again from the store.
func (c *core) offerCalls(thread int64, source, item, state, key string) ([]offerCall, error) {
	s, err := c.thread(thread)
	if err != nil || !s.active() {
		return nil, errOfferGone
	}
	var src *storedSource
	for _, x := range c.threadSources(thread) {
		if x.Name == source && x.Items != nil && x.kind() != srcMonitor {
			src = &x
			break
		}
	}
	if src == nil {
		return nil, errOfferGone
	}
	shown := map[string]bool{}
	for _, it := range threadSourceItems(*src, threadItemsMax) {
		shown[it["id"].(string)] = true
	}
	var out []offerCall
	for _, n := range c.itemNotes(thread) {
		if n.Source != source || n.Gone != 0 || !shown[n.Item] {
			continue
		}
		if item != "" && n.Item != item || item == "" && n.State != state {
			continue
		}
		for _, a := range n.Actions {
			if a.key() == key && c.offerAllowed(a.Tool, a.bound(n.Item)) {
				out = append(out, offerCall{note: n, action: a})
			}
		}
	}
	if len(out) == 0 {
		return nil, errOfferGone
	}
	return out, nil
}

// offerCall is one call an offer asks for, on one item.
type offerCall struct {
	note   itemNote
	action noteAction
}

// offer makes the card an offer click asks for: one card, its calls kept
// exactly, bound to their items; nothing runs. A call already waiting on
// its item is not asked again; with nothing left to ask, the card already
// there answers.
func (c *core) offer(ctx context.Context, thread int64, calls []offerCall) (string, error) {
	var batch int64
	for _, oc := range calls {
		input := oc.action.bound(oc.note.Item)
		raw, _ := json.Marshal(input)
		if id := c.itemAskWaiting(thread, oc.note.Source, oc.note.Item, oc.action.Tool, string(raw)); id != 0 {
			continue
		}
		// What the call acts on, read for one item only: a group's click
		// does not wait on N reads.
		var known []cardField
		if len(calls) == 1 {
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			known = c.callContext(rctx, oc.action.Tool, input)
			cancel()
		}
		fields, _ := json.Marshal(cardFields(known, input))
		n := c.callNeed(oc.action.Tool, input, c.pool.facts(oc.action.Tool, input))
		res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, server, category, account, call, input, tainted, created, thread, source, item, batch)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, n.Server, n.Category, n.Account, oc.action.Tool+" "+string(fields), string(raw),
			oc.note.Tainted, time.Now().Unix(), thread, oc.note.Source, oc.note.Item, batch)
		id, err := rowsID(res, err)
		if err != nil {
			return "", err
		}
		if batch == 0 {
			batch = id
			c.store.db.Exec(`UPDATE access_asks SET batch = ? WHERE id = ?`, batch, id)
		}
	}
	if batch == 0 {
		return "", errOfferAsked
	}
	ref := proposalRef(cardCalls, batch)
	p := c.proposalOf(cardCalls, batch)
	c.store.db.Exec(`INSERT OR IGNORE INTO convo_cards (ref, kind, text, created, about) VALUES (?, ?, ?, ?, ?)`,
		ref, cardCalls, p.Text(c.userLang()), time.Now().Unix(), c.store.noteAbout(c.sourceAbout(threadRef(thread))))
	c.linkCard(thread, ref) // on its row, where the user clicked (item_cards.go)
	c.proposalMoved(ref)
	c.logThread(thread, "user", fmt.Sprintf("card %s: %d action(s) offered on its items", ref, len(calls)))
	return ref, nil
}

// errOfferAsked is an offer whose calls all wait already, on their cards.
var errOfferAsked = errors.New("already asked: its card waits on the row")

// offerKey reads an offer's key as a short hex string.
func offerKey(s string) bool {
	if len(s) != 12 {
		return false
	}
	_, err := strconv.ParseUint(s, 16, 64)
	return err == nil
}

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The attention signal: what an assistant (a skill's run, a thread's lead)
// says deserves the user's attention, as a structured block rather than a
// prefix in its prose. A skill writes it in its receipt (receipt.go), a
// lead in its report; code checks its form and puts each of its refs in
// the attention queue (attention.go), with who raised it. Nothing in a
// signal is trusted beyond its form: a ref of a kind not known, or a
// weight outside 1 to 5, is dropped and logged, never fatal.

// attentionSignal is one block: the objects it is about, how much they
// matter, why, and what Eriphe could say of them.
type attentionSignal struct {
	Refs   []string `json:"refs" jsonschema:"the objects it is about: mail:<id>, agenda:<YYYY-MM-DD>, event:<id>, thread:<id>, card:<id>, task:<id>, surface:<id> (a surface of the workspace), or a subject kind:slug (project:<name>, pr:<owner>/<repo>#<n>, alert:<id>…)"`
	Weight int      `json:"weight" jsonschema:"5 interrupts the user, 4 needs the user now, 3 matters today, 2 a glance, 1 background"`
	Why    string   `json:"why" jsonschema:"why it deserves attention, in the user's language, at most 80 characters"`
	Say    string   `json:"say,omitempty" jsonschema:"what Eriphe could tell the user of it, in the user's language, one sentence; optional"`
}

const (
	signalWhyMax  = 80 // runes
	signalSayMax  = 300
	signalsMax    = 20 // signals kept of one block
	signalRefsMax = 10 // refs kept of one signal
	signalRefMax  = 200
)

// attentionInstruction tells an assistant how to write its signal, after
// the receipt's own instruction (receiptInstruction).
const attentionInstruction = "When something deserves the user's attention, add to the receipt an \"attention\" list, one entry per thing: " +
	`"attention": [{"refs": ["mail:<id>"], "weight": 3, "why": "at most 80 characters, in the user's language", "say": "optional: what Eriphe could tell the user, one sentence in the user's language"}]` + ". " +
	"refs name the objects it is about, each in one of these forms: mail:<id>, agenda:<YYYY-MM-DD>, event:<id>, thread:<id>, card:<id>, task:<id>, surface:<id> (a surface of the workspace), or a subject kind:slug (project:<name>, pr:<owner>/<repo>#<n>, alert:<id>, person:<slug>…). " +
	"weight: 5 interrupts the user, 4 needs the user now, 3 matters today, 2 is a glance, 1 is background. " +
	"Leave refs out when the entry is about your final message itself. " +
	"Leave the list out when nothing deserves it: the user is then told nothing. A ref of another form, or a weight outside 1 to 5, is dropped."

// The kinds of records an attention item may be about: kind:id. It may be
// about a subject too (subject.go): mail:<message id>, event:<event id>,
// project:<name>, pr:<owner>/<repo>#<n>, alert:<id>…, the id as written.
const (
	refAgenda  = "agenda"  // agenda:<YYYY-MM-DD>, the user's day
	refThread  = "thread"  // thread:<id>
	refCard    = "card"    // card:<id>, a card of the conversation
	refTask    = "task"    // task:<id>
	refSurface = "surface" // surface:<id>, a surface of the workspace (surfaces.go)
)

// itemRef is a ref, parsed.
type itemRef struct {
	Kind string
	ID   string
}

func (r itemRef) String() string { return r.Kind + ":" + r.ID }

// errBadRef is a ref that does not parse.
type errBadRef struct{ why string }

func (e errBadRef) Error() string { return "bad ref: " + e.why }

// parseRef reads a ref: a record's or a subject's kind (subject.go), a
// colon, an id. An agenda's id is a date, a thread's and a card's a
// number, a surface's its id; a subject's id is kept as written.
func parseRef(s string) (itemRef, error) {
	kind, id, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || id == "" {
		return itemRef{}, errBadRef{"a ref is kind:id"}
	}
	r := itemRef{Kind: kind, ID: id}
	switch kind {
	case refAgenda:
		if _, err := time.Parse(time.DateOnly, id); err != nil {
			return r, errBadRef{"an agenda is a date, YYYY-MM-DD"}
		}
	case refThread, refCard:
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
			return r, errBadRef{"a " + kind + " is a number"}
		}
	case refSurface:
		if !surfaceIDRe.MatchString(id) {
			return r, errBadRef{"a surface is its id"}
		}
	case refTask:
	default:
		if isSubjectKind(kind) {
			break
		}
		return r, errBadRef{"no kind " + strconv.Quote(kind)}
	}
	if len(id) > 1000 {
		return r, errBadRef{"too long"}
	}
	return r, nil
}

// validAttentionRef tells a ref of a known form (parseRef), short and on
// one line.
func validAttentionRef(ref string) bool {
	_, err := parseRef(ref)
	return err == nil && ref == strings.TrimSpace(ref) && len(ref) <= signalRefMax && !strings.ContainsAny(ref, "\r\n")
}

// decodeSignals reads a receipt's "attention": a list of blocks, or one
// block alone; refs given as one string are one ref. An entry without refs
// is about the message the receipt ends (a skill's run: its notification,
// skill_runs.go): kept with none. What does not read is dropped, with why.
func decodeSignals(raw json.RawMessage) ([]attentionSignal, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		items = []json.RawMessage{raw}
	}
	var out []attentionSignal
	var dropped []string
	for _, it := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(it, &m) != nil {
			dropped = append(dropped, "an attention entry that is not an object")
			continue
		}
		var s attentionSignal
		if refs := m["refs"]; len(refs) == 0 || string(refs) == "null" {
			// none: about the message itself
		} else if json.Unmarshal(refs, &s.Refs) != nil {
			var one string
			json.Unmarshal(m["refs"], &one)
			s.Refs = []string{one}
		}
		var w float64
		if json.Unmarshal(m["weight"], &w) != nil || w != float64(int(w)) {
			w = 0 // not a whole number: out of range
		}
		s.Weight = int(w)
		json.Unmarshal(m["why"], &s.Why)
		json.Unmarshal(m["say"], &s.Say)
		out = append(out, s)
	}
	kept, bad := cleanSignals(out, true)
	return kept, append(dropped, bad...)
}

// cleanSignals keeps the signals of a good form, trimmed: refs of a known
// form, a weight from 1 to 5, a why cut to its length. A signal with no
// good ref left is dropped; one that named none is kept with none when
// own (it is about the message that carries it), else dropped too.
func cleanSignals(in []attentionSignal, own bool) ([]attentionSignal, []string) {
	var out []attentionSignal
	var dropped []string
	for _, s := range in {
		if s.Weight < 1 || s.Weight > 5 {
			dropped = append(dropped, fmt.Sprintf("weight %d out of 1-5 (%s)", s.Weight, strings.Join(s.Refs, ", ")))
			continue
		}
		var refs []string
		for _, r := range s.Refs {
			r = strings.TrimSpace(r)
			switch {
			case !validAttentionRef(r):
				dropped = append(dropped, fmt.Sprintf("unknown ref %q", truncate(r, 60)))
			case !slices.Contains(refs, r) && len(refs) < signalRefsMax:
				refs = append(refs, r)
			}
		}
		if len(refs) == 0 && (len(s.Refs) > 0 || !own) {
			if len(s.Refs) == 0 {
				dropped = append(dropped, "a signal without refs")
			}
			continue
		}
		if len(out) == signalsMax {
			dropped = append(dropped, fmt.Sprintf("more than %d signals", signalsMax))
			break
		}
		out = append(out, attentionSignal{Refs: refs, Weight: s.Weight,
			Why: truncate(oneLine(s.Why), signalWhyMax), Say: truncate(oneLine(s.Say), signalSayMax)})
	}
	return out, dropped
}

// attendSignals puts each ref of the signals in the attention queue, from
// source (skill:<name>, assistant:<name>); tainted when who wrote them
// read external content. The dropped are logged. A signal without refs
// makes no item here: its caller knows what it is about.
func (c *core) attendSignals(signals []attentionSignal, dropped []string, source string, tainted bool) int {
	for _, d := range dropped {
		log.Printf("attention: %s: dropped %s", source, d)
	}
	n := 0
	for _, s := range signals {
		for _, ref := range s.Refs {
			c.attend(attentionItem{Ref: ref, Weight: s.Weight, Why: s.Why, Say: s.Say, Source: source, tainted: tainted})
			n++
		}
	}
	return n
}

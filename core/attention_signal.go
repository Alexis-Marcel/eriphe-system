package main

import (
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
)

// The attention signal: what an assistant (a skill's run, a session lead)
// says deserves the user's attention, as a structured block rather than a
// prefix in its prose. A skill writes it in its receipt (receipt.go), a
// lead in its report; code checks its form and puts each of its refs in
// the attention queue (attention.go), with who raised it. Nothing in a
// signal is trusted beyond its form: a ref of a kind not known, or a
// weight outside 1 to 5, is dropped and logged, never fatal.

// attentionSignal is one block: the objects it is about, how much they
// matter, why, and what Eriphe could say of them.
type attentionSignal struct {
	Refs   []string `json:"refs" jsonschema:"the objects it is about: mail:<id>, agenda:<YYYY-MM-DD>, event:<id>, session:<id>, card:<id>, task:<id>, project:<name> or alert:<id>"`
	Weight int      `json:"weight" jsonschema:"5 interrupts the user, 4 needs the user now, 3 matters today, 2 a glance, 1 background"`
	Why    string   `json:"why" jsonschema:"why it deserves attention, in French, at most 80 characters"`
	Say    string   `json:"say,omitempty" jsonschema:"what Eriphe could tell the user of it, in French, one sentence; optional"`
}

// attentionRefKinds are the forms a ref may take: <kind>:<id>.
var attentionRefKinds = []string{"mail", "agenda", "event", "session", "card", "task", "project", "alert"}

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
	`"attention": [{"refs": ["mail:<id>"], "weight": 3, "why": "at most 80 characters, in French", "say": "optional: what Eriphe could tell the user, one sentence in French"}]` + ". " +
	"refs name the objects it is about, each in one of these forms: mail:<id>, agenda:<YYYY-MM-DD>, event:<id>, session:<id>, card:<id>, task:<id>, project:<name>, alert:<id>. " +
	"weight: 5 interrupts the user, 4 needs the user now, 3 matters today, 2 is a glance, 1 is background. " +
	"Leave it out when nothing does. A ref of another form, or a weight outside 1 to 5, is dropped."

// validAttentionRef tells a ref of a known form: a known kind, then an id.
func validAttentionRef(ref string) bool {
	kind, id, ok := strings.Cut(ref, ":")
	return ok && slices.Contains(attentionRefKinds, kind) && strings.TrimSpace(id) == id && id != "" &&
		len(ref) <= signalRefMax && !strings.ContainsAny(ref, " \t\r\n")
}

// decodeSignals reads a receipt's "attention": a list of blocks, or one
// block alone; refs given as one string are one ref. What does not read
// is dropped, with why.
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
		if json.Unmarshal(m["refs"], &s.Refs) != nil {
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
	kept, bad := cleanSignals(out)
	return kept, append(dropped, bad...)
}

// cleanSignals keeps the signals of a good form, trimmed: refs of a known
// form, a weight from 1 to 5, a why cut to its length. A signal with no
// good ref left is dropped.
func cleanSignals(in []attentionSignal) ([]attentionSignal, []string) {
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
		if len(refs) == 0 {
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
// read external content. The dropped are logged.
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

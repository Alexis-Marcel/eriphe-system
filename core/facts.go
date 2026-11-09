package main

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Facts: what Eriphe knows now, in one typed store (#244 step 3). A fact
// is an owner's field: its value from a closed type (a choice, a number, a
// date, a text, rows of such), its time, its validity (expired, it reads
// unknown), its provenance (who wrote it) and its taint (why its writer
// was tainted when it wrote it; a closed value never taints).
//
// Two kinds, by who writes them:
//
//   - kept facts, in eriphe-core's database with a few days of history:
//     an assistant's, written by itself through set_state and checked
//     against its schema (assistant_state.go). Their owner is the
//     assistant's name;
//   - ephemeral facts, in memory only, never written to disk, with no
//     history: the user's context (context.go), owned by "context" and
//     written by code alone from its sources (a machine's node, a token
//     source, Home Assistant's follower). A source's own words never
//     reach a fact: its values are from closed lists.
//
// What reads them: the brain, in each turn (the [contexte] and
// [assistants] lines, closed values only), and through its facts tool and
// the assistants' views; the router (route.go), which reads the context's
// facts to choose a device. Facts orient, never authorise: the grid, the
// cards and the approvals never read them.

const ownerContext = "context"

// reservedOwners are owners no assistant may be named after.
var reservedOwners = []string{ownerContext, "eriphe", "brain", "user"}

// fact is one fact, as read now.
type fact struct {
	Owner   string
	Field   string
	Value   any // nil: unknown
	Set     time.Time
	Expires time.Time
	Writer  string // provenance: an assistant, machine:<node>, src:<id>, ha
	Tainted string // why its writer was tainted when it wrote it; "" when closed
	Closed  bool   // a value from a closed set: it carries no words
	Kept    bool   // in the database, with history; otherwise ephemeral
	// Closed extras of an ephemeral fact (a do-not-disturb reason, an idle
	// time); never words from a source.
	Detail map[string]any
}

func (f fact) known(now time.Time) bool {
	return f.Value != nil && !now.Before(f.Set) && now.Before(f.Expires)
}

// id is how a fact is named: owner.field.
func (f fact) id() string { return f.Owner + "." + f.Field }

// factMemory keeps ephemeral facts, in memory only. A field may have one
// fact per writer (two machines both say presence); readers fold them.
// Its zero value is empty and ready.
type factMemory struct {
	mu    sync.Mutex
	facts map[string]fact // by owner, field and writer
}

func memKey(owner, field, writer string) string { return owner + "|" + field + "|" + writer }

func (m *factMemory) put(f fact) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.facts == nil {
		m.facts = map[string]fact{}
	}
	f.Kept = false
	m.facts[memKey(f.Owner, f.Field, f.Writer)] = f
}

// forget drops an owner's facts from a writer, or one field of them ("" for
// all its fields).
func (m *factMemory) forget(owner, writer, field string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, f := range m.facts {
		if f.Owner == owner && f.Writer == writer && (field == "" || f.Field == field) {
			delete(m.facts, k)
		}
	}
}

// fresh are an owner's facts known at now; the expired ones are dropped.
func (m *factMemory) fresh(owner string, now time.Time) []fact {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []fact
	for k, f := range m.facts {
		if !now.Before(f.Expires) {
			delete(m.facts, k) // expired: unknown from now on
			continue
		}
		if f.Owner == owner && f.known(now) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Writer != out[j].Writer {
			return out[i].Writer < out[j].Writer
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// The context's facts, as the user, the house and each device: the
// signals folded (context.go), named as rules and the facts tool read them.

// contextFacts are the context's facts now: the user's (place, room,
// focus, busy), the house's (home_activity) and each device's
// (<device>.presence, .dnd, .activity), written by code.
func (c *core) contextFacts(now time.Time) []fact {
	sigs := c.liveSignals(now)
	if len(sigs) == 0 {
		return nil
	}
	u := buildContext(c.contextPrefs().Level, sigs)
	newest := map[string]ctxSignal{}
	for _, s := range sigs {
		if !ctxKinds[s.Kind].Device && !s.At.Before(newest[s.Kind].At) {
			newest[s.Kind] = s
		}
	}
	var out []fact
	add := func(field string, v any, s ctxSignal) {
		out = append(out, fact{Owner: ownerContext, Field: field, Value: v, Set: s.At, Expires: s.Until, Writer: s.Source, Closed: true})
	}
	for _, kind := range []string{sigPlace, sigRoom, sigHomeActivity, sigFocus} {
		if s, ok := newest[kind]; ok {
			add(kind, s.Value, s)
		}
	}
	if s, ok := newest[sigBusy]; ok && !u.Busy.IsZero() {
		add("busy_until", u.Busy.In(now.Location()).Format("2006-01-02T15:04"), s)
	}
	for _, d := range u.Devices {
		name := strings.TrimPrefix(strings.TrimPrefix(d.Source, srcMachine), srcToken)
		for _, s := range sigs {
			if s.Source != d.Source || !ctxKinds[s.Kind].Device {
				continue
			}
			f := fact{Owner: ownerContext, Field: name + "." + s.Kind, Value: s.Value, Set: s.At, Expires: s.Until, Writer: s.Source, Closed: true}
			if s.Reason != "" {
				f.Detail = map[string]any{"reason": s.Reason}
			}
			if s.Idle > 0 {
				f.Detail = map[string]any{"idle_minutes": int(s.Idle.Minutes())}
			}
			out = append(out, f)
		}
	}
	return out
}

// factOwnerReserved tells whether a name is an owner no assistant may take.
func factOwnerReserved(name string) bool {
	return slices.Contains(reservedOwners, strings.ToLower(name))
}

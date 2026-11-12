package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A composition is what the workspace shows, by ref: which objects of the
// attention queue are modules, how big (their weight), why, which wait as
// chips, and what Eriphe says over them. The composer (composer.go) writes
// it from a snapshot of the queue; code checks every ref against that
// snapshot, clamps the weights and caps the counts, and composes by weight
// itself when the composer is off, slow or wrong. A composition carries
// refs, never copies: the workspace reads the objects (objects.go).

const (
	compositionItemsMax = 6   // modules the screen holds (a 12×8 grid)
	compositionChipsMax = 8   // chips under them
	compositionSayMax   = 140 // Eriphe's line, in runes
	compositionWhyMax   = 60  // an item's why, in runes

	composedBy = "composer"
	fallbackBy = "fallback"

	modeCenter = "center" // Eriphe in the middle: nothing, or little, to show
	modeSide   = "side"   // Eriphe on the side, the modules in front
)

// The moments a composition is made for.
const (
	momentChange  = "change"  // the queue changed while the user is here
	momentArrival = "arrival" // the user is away: what to show on their return
	momentRemark  = "remark"  // the user made a rule (attention_rules.go)
	momentStart   = "start"   // eriphe-core started
)

// composition is the stable contract the workspace reads (GET
// /api/composition, the event stream's "composition").
type composition struct {
	Mode    string            `json:"mode"`              // center or side
	Ambient string            `json:"ambient,omitempty"` // calm, urgent or decision: from the weights
	Eriphe  string            `json:"eriphe"`            // Eriphe's line, at most 140 characters
	Items   []compositionItem `json:"items"`
	Chips   []string          `json:"chips"`
	Moment  string            `json:"moment,omitempty"`
	At      time.Time         `json:"at"`
	By      string            `json:"by"` // composer or fallback
}

// compositionItem is a module: its object, its size, its why. Say is the
// queue's own (attention.go), never the composer's.
type compositionItem struct {
	Ref    string `json:"ref"`
	Weight int    `json:"weight"`
	Why    string `json:"why"`
	Say    string `json:"say,omitempty"`
}

// snapshotItem is one item of the queue as the composer reads it.
type snapshotItem struct {
	attentionItem
	Kind  string // the ref's kind
	Title string // its object's title, when it could be read in time
}

// compositionSnapshot is what a composition is made from.
type compositionSnapshot struct {
	Items  []snapshotItem
	Moment string
	Remark string        // the user's words, for a remark
	Away   time.Duration // how long the user has been away, for an arrival
	Rules  []string      // the user's rules about the workspace, in words (composerRules)
	Now    time.Time
}

// composerRules are the user's rules the composer reads with each
// snapshot, one sentence each (attention_rules.go). Code applies them to
// the queue before the composer; the composer reads them to place what is
// left.
var composerRules = func(c *core) []string { return c.rulesContext() }

func (s compositionSnapshot) item(ref string) (snapshotItem, bool) {
	i := slices.IndexFunc(s.Items, func(it snapshotItem) bool { return it.Ref == ref })
	if i < 0 {
		return snapshotItem{}, false
	}
	return s.Items[i], true
}

// snapshotTitles is how long the titles of a snapshot's objects may take:
// the composition does without those that come later.
var snapshotTitles = 2 * time.Second

// compositionSnapshot reads the queue and, if asked, the titles of its
// objects.
func (c *core) compositionSnapshot(moment, remark string, titles bool) compositionSnapshot {
	now := time.Now()
	s := compositionSnapshot{Moment: moment, Remark: remark, Now: now, Rules: composerRules(c)}
	if moment == momentArrival {
		_, s.Away = c.awaySince(c.notifyTime())
	}
	open := c.attentionOpen()
	refs := make([]string, 0, len(open))
	for _, it := range open {
		kind, _, _ := strings.Cut(it.Ref, ":")
		s.Items = append(s.Items, snapshotItem{attentionItem: it, Kind: kind})
		refs = append(refs, it.Ref)
	}
	if !titles {
		return s
	}
	if len(refs) > objectsMax {
		refs = refs[:objectsMax]
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTitles)
	defer cancel()
	for i, o := range c.objects(ctx, refs) {
		if o.Object != nil {
			s.Items[i].Title = o.Object.Title
		}
	}
	return s
}

// fallbackComposition composes by weight: the heaviest first as modules,
// the rest as chips. It is what the workspace shows when the composer is
// off, slow or wrong.
func fallbackComposition(s compositionSnapshot) composition {
	items := slices.Clone(s.Items)
	slices.SortStableFunc(items, func(a, b snapshotItem) int {
		return cmp.Or(cmp.Compare(b.Weight, a.Weight), b.Time.Compare(a.Time))
	})
	comp := composition{Moment: s.Moment, At: s.Now, By: fallbackBy, Items: []compositionItem{}, Chips: []string{}}
	for _, it := range items {
		if len(comp.Items) < compositionItemsMax {
			comp.Items = append(comp.Items, compositionItem{Ref: it.Ref, Weight: it.Weight, Why: truncate(oneLine(it.Why), compositionWhyMax), Say: it.Say})
		} else if len(comp.Chips) < compositionChipsMax {
			comp.Chips = append(comp.Chips, it.Ref)
		}
	}
	return settle(comp, s)
}

// composerOutput is what the composer answers.
type composerOutput struct {
	Mode   string `json:"mode"`
	Eriphe string `json:"eriphe"`
	Items  []struct {
		Ref    string `json:"ref"`
		Weight int    `json:"weight"`
		Why    string `json:"why"`
	} `json:"items"`
	Chips []string `json:"chips"`
}

var errNoComposition = errors.New("no usable composition")

// checkComposition reads what the composer answered against the snapshot:
// a ref it does not hold is dropped, a weight clamped to 1–5, the texts cut
// to their length, the items and chips capped. An answer that is not JSON,
// or that keeps nothing of a queue that has items, is an error: the
// fallback composes instead. dropped counts the refs left out.
func checkComposition(raw string, s compositionSnapshot) (comp composition, dropped int, err error) {
	var out composerOutput
	if err := json.Unmarshal([]byte(jsonObject(raw)), &out); err != nil {
		return composition{}, 0, fmt.Errorf("not a composition: %w", err)
	}
	comp = composition{Mode: out.Mode, Eriphe: truncate(oneLine(out.Eriphe), compositionSayMax), Moment: s.Moment, At: s.Now, By: composedBy,
		Items: []compositionItem{}, Chips: []string{}}
	seen := map[string]bool{}
	for _, x := range out.Items {
		it, ok := s.item(strings.TrimSpace(x.Ref))
		if !ok || seen[it.Ref] || len(comp.Items) == compositionItemsMax {
			dropped++
			continue
		}
		seen[it.Ref] = true
		comp.Items = append(comp.Items, compositionItem{Ref: it.Ref, Weight: min(max(x.Weight, 1), 5),
			Why: truncate(oneLine(x.Why), compositionWhyMax), Say: it.Say})
	}
	for _, ref := range out.Chips {
		it, ok := s.item(strings.TrimSpace(ref))
		if !ok || seen[it.Ref] || len(comp.Chips) == compositionChipsMax {
			dropped++
			continue
		}
		seen[it.Ref] = true
		comp.Chips = append(comp.Chips, it.Ref)
	}
	if len(s.Items) > 0 && len(comp.Items)+len(comp.Chips) == 0 {
		return composition{}, dropped, errNoComposition
	}
	// Nothing of the queue goes unseen: what the composer left out waits as
	// a chip, the heaviest first, while there is room.
	rest := slices.Clone(s.Items)
	slices.SortStableFunc(rest, func(a, b snapshotItem) int { return cmp.Compare(b.Weight, a.Weight) })
	for _, it := range rest {
		if !seen[it.Ref] && len(comp.Chips) < compositionChipsMax {
			comp.Chips = append(comp.Chips, it.Ref)
		}
	}
	return settle(comp, s), dropped, nil
}

// settle gives a composition its mode and ambient: Eriphe in the middle
// when there is nothing to show, the light by the heaviest module.
func settle(comp composition, s compositionSnapshot) composition {
	if len(comp.Items) == 0 {
		comp.Mode = modeCenter
	} else if comp.Mode != modeCenter && comp.Mode != modeSide {
		comp.Mode = modeSide
	}
	comp.Ambient = "calm"
	for _, it := range comp.Items {
		switch {
		case it.Weight == 5:
			comp.Ambient = "urgent"
		case it.Weight == 4 && comp.Ambient == "calm":
			comp.Ambient = "decision"
		}
	}
	return comp
}

// jsonObject is the JSON object in a model's answer: the answer itself,
// or what lies between its first { and its last }, out of a code fence.
func jsonObject(s string) string {
	s = strings.TrimSpace(s)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}

// debouncer says when a burst of changes is over: wait after the last
// change, but never more than maxWait after the first.
type debouncer struct {
	wait, maxWait time.Duration
	first, last   time.Time
}

// changed records a change at now.
func (d *debouncer) changed(now time.Time) {
	if d.first.IsZero() {
		d.first = now
	}
	d.last = now
}

// due tells whether the changes recorded are settled at now, and forgets
// them when they are.
func (d *debouncer) due(now time.Time) bool {
	if d.first.IsZero() {
		return false
	}
	if now.Sub(d.last) >= d.wait || now.Sub(d.first) >= d.maxWait {
		d.first, d.last = time.Time{}, time.Time{}
		return true
	}
	return false
}

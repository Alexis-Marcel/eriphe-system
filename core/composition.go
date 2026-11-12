package main

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"
)

// A composition is what the old workspace canvas shows, by ref: which
// objects of the attention queue are modules, how big (their weight), why,
// and which wait as chips. Since #422 code alone makes it, by weight
// (fallbackComposition), with what the brain put in front (workspace_show):
// the composer chooses among surfaces instead (workspace_pool.go). It goes
// with the per-kind canvas in #424. A composition carries refs, never
// copies: the canvas reads the objects (objects.go).

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
	momentRemark  = "remark"  // a preference about the workspace entered memory (workspace.go)
	momentShow    = "show"    // the brain put objects in front (workspace_show)
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
	Items       []snapshotItem
	Moment      string
	Remark      string        // the preference that just entered memory, for a remark; why, for a show
	Away        time.Duration // how long the user has been away, for an arrival
	Preferences []string      // the user's preferences about the workspace, from memory (workspacePreferences)
	Required    []string      // refs every composition shows as modules (workspace_show)
	Now         time.Time
}

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
	s := compositionSnapshot{Moment: moment, Remark: remark, Now: now}
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
	// What the brain put in front is there, queued or not.
	if show, _ := c.shown(now); show != nil {
		s.Required = show.Refs
		for _, ref := range show.Refs {
			if i := slices.Index(refs, ref); i >= 0 {
				s.Items[i].Weight = max(s.Items[i].Weight, weightBlocksATask)
				continue
			}
			kind, _, _ := strings.Cut(ref, ":")
			s.Items = append(s.Items, snapshotItem{attentionItem: attentionItem{Ref: ref, Weight: weightBlocksATask, Why: show.Why, Source: "brain", Time: show.At}, Kind: kind})
			refs = append(refs, ref)
		}
	}
	if !titles {
		s.Preferences = c.workspacePreferences(snapshotWords(s.Items))
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
	s.Preferences = c.workspacePreferences(snapshotWords(s.Items))
	return s
}

// snapshotWords are the titles and whys of a snapshot's items.
func snapshotWords(items []snapshotItem) []string {
	var words []string
	for _, it := range items {
		words = append(words, it.Title, it.Why)
	}
	return words
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

// settle gives a composition what the brain put in front, as modules
// first, then its mode and ambient: Eriphe in the middle when there is
// nothing to show, the light by the heaviest module.
func settle(comp composition, s compositionSnapshot) composition {
	comp = requireFront(comp, s)
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

// requireFront makes the snapshot's required refs modules, in their order
// and ahead of the others; the modules they push out of the screen wait
// as chips, first.
func requireFront(comp composition, s compositionSnapshot) composition {
	if len(s.Required) == 0 {
		return comp
	}
	var front, rest []compositionItem
	for _, ref := range s.Required {
		it, ok := s.item(ref)
		if !ok {
			continue
		}
		mod := compositionItem{Ref: ref, Weight: max(it.Weight, weightBlocksATask), Why: truncate(oneLine(it.Why), compositionWhyMax), Say: it.Say}
		if i := slices.IndexFunc(comp.Items, func(x compositionItem) bool { return x.Ref == ref }); i >= 0 {
			mod.Weight = max(comp.Items[i].Weight, weightBlocksATask)
			mod.Why = cmp.Or(comp.Items[i].Why, mod.Why)
		}
		front = append(front, mod)
	}
	for _, it := range comp.Items {
		if !slices.Contains(s.Required, it.Ref) {
			rest = append(rest, it)
		}
	}
	chips := slices.DeleteFunc(slices.Clone(comp.Chips), func(r string) bool { return slices.Contains(s.Required, r) })
	items := append(front, rest...)
	if len(items) > compositionItemsMax {
		var out []string
		for _, it := range items[compositionItemsMax:] {
			out = append(out, it.Ref)
		}
		chips = append(out, chips...)
		items = items[:compositionItemsMax]
	}
	if len(chips) > compositionChipsMax {
		chips = chips[:compositionChipsMax]
	}
	comp.Items, comp.Chips = items, chips
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

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The composer's bench (testdata/composer): invented moments of a
// fictional user, Sam, each with what a good choice must do. This test
// runs in CI with no model: every case is valid, its reference answer
// passes checkChoice and, laid out by code, meets its expectations,
// invented ids are cleaned, and the layout by weight is measured against
// "in front". composer_live_test.go runs the same cases on a real model.
//
// Since #422 the composer chooses among surfaces: an item of a case is an
// open item of the queue and the surface showing it (queueSurfaceID), its
// title the object's; or, with an author, a surface that author wrote
// (ref surface:<id>, weight 1 to 100), outside the queue. The composer can
// no longer hide a surface: what it leaves out waits folded, so "hidden"
// means "not chosen", and code folds it (a chip, never a tile).

const composerBenchZone = "Europe/Paris"

type composerCase struct {
	ID          string `json:"id"`
	What        string `json:"what"`
	Now         string `json:"now"` // 2006-01-02 15:04, in composerBenchZone
	Moment      string `json:"moment"`
	AwayMinutes int    `json:"away_minutes"`
	Remark      string `json:"remark"`
	// Preferences are the user's preference memories about the workspace,
	// as sentences, the way the composer reads them (workspacePreferences).
	Preferences []string       `json:"preferences"`
	Items       []composerItem `json:"items"`
	Expect      composerExpect `json:"expect"`
	// Answer is a choice that meets Expect, as the composer would write it.
	Answer json.RawMessage `json:"answer"`

	file string
}

type composerItem struct {
	Ref string `json:"ref"`
	// Author is who wrote the surface (brain, skill:<name>, lead:<id>,
	// task:<id>), for a surface outside the queue: its ref is
	// surface:<id>, its weight 1 to 100. Empty for an item of the queue
	// (weight 1 to 5).
	Author     string `json:"author"`
	Weight     int    `json:"weight"`
	AgeMinutes int    `json:"age_minutes"`
	Title      string `json:"title"`
	Why        string `json:"why"`
	Say        string `json:"say"`
}

// composerExpect is what a good choice of a case does, by the items' refs.
type composerExpect struct {
	Front  []string `json:"front"`  // must be tiles
	Back   []string `json:"back"`   // must not be tiles (chips are fine)
	Hidden []string `json:"hidden"` // must not be chosen nor tiles: the user's preferences exclude them
	// FallbackFront says whether the layout by weight puts every Front ref
	// among its tiles: false records a case where only the composer can (a
	// failure the user wants in front, with a low weight).
	FallbackFront bool `json:"fallback_front"`
}

func loadComposerCases(t *testing.T) []composerCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "composer", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no composer cases: %v", err)
	}
	var cases []composerCase
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var x composerCase
		if err := json.Unmarshal(raw, &x); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		x.file = filepath.Base(f)
		cases = append(cases, x)
	}
	return cases
}

// benchSurface is a case's item as a surface of the pool.
func benchSurface(it composerItem, now time.Time) surface {
	at := now.Add(-time.Duration(it.AgeMinutes) * time.Minute)
	if it.Author != "" {
		return surface{ID: queueSurfaceID(it.Ref), Title: it.Title, Author: it.Author, Weight: it.Weight, Why: it.Why, Created: at, Updated: at}
	}
	return surface{ID: queueSurfaceID(it.Ref), Title: it.Title, Author: surfaceAuthorCore, Weight: queueSurfaceWeight(it.Weight),
		Why: it.Why, Created: at, Updated: at}
}

// pool is the case's pool, as layoutOf takes it.
func (x composerCase) pool(now time.Time) []surface {
	var out []surface
	for _, it := range x.Items {
		out = append(out, benchSurface(it, now))
	}
	return out
}

// snapshot is the case as production builds it (poolSnapshot): the pool,
// the queue with the surface of each item, the moment and the user's
// preferences from memory.
func (x composerCase) snapshot(t *testing.T) poolSnapshot {
	t.Helper()
	now, err := time.ParseInLocation("2006-01-02 15:04", x.Now, mustZone(t, composerBenchZone))
	if err != nil {
		t.Fatalf("%s: now: %v", x.file, err)
	}
	s := poolSnapshot{Moment: x.Moment, Remark: x.Remark, Away: time.Duration(x.AwayMinutes) * time.Minute, Lang: "fr",
		Preferences: slices.Clone(x.Preferences), Now: now}
	for _, it := range x.Items {
		sf := benchSurface(it, now)
		s.Pool = append(s.Pool, entryOf(sf))
		if it.Author != "" {
			continue
		}
		s.Queue = append(s.Queue, poolQueued{attentionItem: attentionItem{Ref: it.Ref, Weight: it.Weight, Why: it.Why, Say: it.Say,
			Source: "bench", Time: sf.Created}, Surface: sf.ID})
	}
	slices.SortStableFunc(s.Pool, func(a, b poolEntry) int { return b.Weight - a.Weight })
	return s
}

// surfaceIDsOf are the surfaces of refs.
func surfaceIDsOf(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, queueSurfaceID(r))
	}
	return out
}

// check says what is wrong with the case itself, "" when it is valid.
func (x composerCase) check() string {
	switch {
	case x.ID == "" || x.What == "":
		return "no id or what"
	case !slices.Contains([]string{momentChange, momentArrival, momentRemark, momentStart}, x.Moment):
		return "unknown moment " + x.Moment
	case x.Moment == momentArrival && x.AwayMinutes <= 0:
		return "an arrival without away_minutes"
	case x.Moment == momentRemark && x.Remark == "":
		return "a remark without its words"
	case len(x.Answer) == 0:
		return "no answer"
	}
	refs := map[string]bool{}
	for _, it := range x.Items {
		if _, id, ok := strings.Cut(it.Ref, ":"); !ok || id == "" {
			return "a ref that does not parse: " + it.Ref
		}
		if refs[it.Ref] {
			return "a ref twice: " + it.Ref
		}
		top := 5
		if it.Author != "" {
			id, ok := strings.CutPrefix(it.Ref, "surface:")
			if !ok || !surfaceIDRe.MatchString(id) {
				return "an author's surface needs a ref surface:<id>: " + it.Ref
			}
			top = 100
		}
		if it.Weight < 1 || it.Weight > top || it.AgeMinutes < 0 {
			return "weight or age out of range: " + it.Ref
		}
		refs[it.Ref] = true
	}
	seen := map[string]string{}
	for list, rs := range map[string][]string{"front": x.Expect.Front, "back": x.Expect.Back, "hidden": x.Expect.Hidden} {
		for _, r := range rs {
			if !refs[r] {
				return fmt.Sprintf("%s names %s, not in the queue", list, r)
			}
			if other, ok := seen[r]; ok {
				return fmt.Sprintf("%s in both %s and %s", r, other, list)
			}
			seen[r] = list
		}
	}
	if len(x.Expect.Front) > choiceItemsMax {
		return "more in front than the screen holds"
	}
	return ""
}

// composerScore is how a choice does against its case.
type composerScore struct {
	Invented    []string // ids the answer names that the pool does not hold
	MissFront   []string // front surfaces that are not tiles
	FrontBack   []string // back or hidden surfaces shown as tiles
	ShownHidden []string // hidden surfaces the answer chose
}

func (s composerScore) agrees() bool {
	return len(s.MissFront)+len(s.FrontBack)+len(s.ShownHidden) == 0
}

func (s composerScore) String() string {
	if s.agrees() && len(s.Invented) == 0 {
		return "agrees"
	}
	var b []string
	for _, p := range []struct {
		what string
		ids  []string
	}{{"invented", s.Invented}, {"not in front", s.MissFront}, {"in front, should wait", s.FrontBack}, {"chosen, should be left out", s.ShownHidden}} {
		if len(p.ids) > 0 {
			b = append(b, p.what+" "+strings.Join(p.ids, ","))
		}
	}
	return strings.Join(b, "; ")
}

// scoreChoice scores the composer's raw answer and the layout code made of
// its choice.
func scoreChoice(x composerCase, s poolSnapshot, raw string, l workspaceLayout) composerScore {
	var sc composerScore
	var out composerOutput
	json.Unmarshal([]byte(jsonObject(raw)), &out)
	hidden := surfaceIDsOf(x.Expect.Hidden)
	for _, it := range out.Items {
		id := strings.TrimSpace(it.ID)
		if _, ok := s.entry(id); !ok {
			sc.Invented = append(sc.Invented, id)
		}
		if slices.Contains(hidden, id) && !slices.Contains(sc.ShownHidden, id) {
			sc.ShownHidden = append(sc.ShownHidden, id)
		}
	}
	for _, id := range out.Stale {
		if _, ok := s.entry(strings.TrimSpace(id)); !ok {
			sc.Invented = append(sc.Invented, id)
		}
	}
	tiles := tileIDs(l)
	for _, id := range surfaceIDsOf(x.Expect.Front) {
		if !slices.Contains(tiles, id) {
			sc.MissFront = append(sc.MissFront, id)
		}
	}
	for _, id := range append(surfaceIDsOf(x.Expect.Back), hidden...) {
		if slices.Contains(tiles, id) {
			sc.FrontBack = append(sc.FrontBack, id)
		}
	}
	return sc
}

func tileIDs(l workspaceLayout) []string {
	out := make([]string, 0, len(l.Items))
	for _, t := range l.Items {
		out = append(out, t.ID)
	}
	return out
}

func TestComposerBenchCases(t *testing.T) {
	cases := loadComposerCases(t)
	if len(cases) < 5 {
		t.Fatalf("%d cases, want 5 at least", len(cases))
	}
	seenIDs := map[string]bool{}
	moments := map[string]bool{}
	for _, x := range cases {
		t.Run(x.ID, func(t *testing.T) {
			if bad := x.check(); bad != "" {
				t.Fatalf("%s: %s", x.file, bad)
			}
			if seenIDs[x.ID] {
				t.Fatalf("%s: id %s twice", x.file, x.ID)
			}
			seenIDs[x.ID], moments[x.Moment] = true, true
			s := x.snapshot(t)
			pool := x.pool(s.Now)

			// The turn the composer reads names every id, and only those,
			// and the user's preferences.
			turn := composerTurn(s)
			allowed := make([]string, 0, len(s.Pool))
			for _, e := range s.Pool {
				allowed = append(allowed, e.ID)
			}
			if !strings.Contains(turn, "Ids permis : "+strings.Join(allowed, ", ")+"\n") {
				t.Errorf("turn without its ids:\n%s", turn)
			}
			for _, p := range s.Preferences {
				if !strings.Contains(turn, "- "+packQuote(p)) {
					t.Errorf("turn without the preference %q", p)
				}
			}
			if len(x.Expect.Hidden) > 0 && len(x.Preferences) == 0 {
				t.Errorf("%s: hidden refs with no preference to hide them", x.file)
			}

			// The reference answer passes as it is and, laid out, meets the case.
			raw := string(x.Answer)
			ch, sig, dropped, err := checkChoice(raw, s)
			if err != nil || dropped != 0 || ch.By != composedBy {
				t.Fatalf("answer: %v, %d dropped, by %s", err, dropped, ch.By)
			}
			l := layoutOf(pool, ch, "")
			if sc := scoreChoice(x, s, raw, l); !sc.agrees() || len(sc.Invented) > 0 {
				t.Errorf("answer: %s", sc)
			}
			// What the user's preferences hide is folded by code: a chip.
			for _, id := range surfaceIDsOf(x.Expect.Hidden) {
				if !slices.ContainsFunc(l.Chips, func(c layoutChip) bool { return c.ID == id }) {
					t.Errorf("hidden %s not folded: chips %v", id, l.Chips)
				}
			}
			// Nothing of the pool goes unseen: a tile or a chip.
			if n := len(l.Items) + len(l.Chips); n != min(len(pool), len(l.Items)+layoutChipsMax) {
				t.Errorf("layout shows %d of %d", n, len(pool))
			}

			// Invented ids are cleaned: the same choice, and the score
			// counts them.
			var out map[string]any
			json.Unmarshal(x.Answer, &out)
			items, _ := out["items"].([]any)
			out["items"] = append([]any{map[string]any{"id": "s-invented-1", "size": "xl", "why": "inventé"}}, items...)
			stale, _ := out["stale"].([]any)
			out["stale"] = append(stale, "s-invented-2")
			bad, _ := json.Marshal(out)
			cleaned, badSig, dropped, err := checkChoice(string(bad), s)
			if len(ch.Items) == 0 {
				// Choosing nothing is a choice; only invented ids are not.
				if err == nil {
					t.Error("an answer of invented ids accepted")
				}
			} else if err != nil || dropped != 2 || !slices.Equal(cleaned.Items, ch.Items) || !slices.Equal(badSig.Stale, sig.Stale) {
				t.Errorf("invented ids: %v, %d dropped, %v vs %v", err, dropped, cleaned.Items, ch.Items)
			}
			if sc := scoreChoice(x, s, string(bad), l); len(sc.Invented) != 2 {
				t.Errorf("invented counted %v", sc.Invented)
			}
			// Only invented ids: invalid when the pool holds something.
			if _, _, _, err := checkChoice(`{"eriphe":"","items":[{"id":"s-invented-1","size":"m","why":"x"}]}`, s); err == nil {
				t.Error("an answer of invented ids accepted")
			}

			// The layout by weight puts the front surfaces in front exactly
			// where the case says it can.
			fb := layoutOf(pool, fallbackChoice(x.Moment, s.Now), "")
			miss := scoreChoice(x, s, "", fb).MissFront
			if (len(miss) == 0) != x.Expect.FallbackFront {
				t.Errorf("fallback %v: front missed %v, fallback_front %v", tileIDs(fb), miss, x.Expect.FallbackFront)
			}
		})
	}
	for _, m := range []string{momentArrival, momentChange, momentRemark} {
		if !moments[m] {
			t.Errorf("no case for the moment %s", m)
		}
	}
}

// composerSystemPrompt is the composer's instructions as its task gets
// them (startAssistant): the user's name, then its role's.
func composerSystemPrompt(user string) string {
	r := assistantRoles[assistantRoleComposer]
	return r.instructions(nil, Assistant{Name: composerAssistant, Role: assistantRoleComposer},
		standingInstructions(r.Prompt, user, composerAssistant))
}

func TestComposerSystemPrompt(t *testing.T) {
	p := composerSystemPrompt("Sam")
	if strings.Contains(p, "{{") || !strings.Contains(p, "compositeur") || !strings.Contains(p, "Sam") {
		t.Fatalf("prompt %q", p)
	}
}

// jaccard is |a ∩ b| / |a ∪ b|, 1 for two empty sets.
func jaccard(a, b []string) float64 {
	in := map[string]int{} // 1: in a, 2: in b, 3: in both
	for _, r := range a {
		in[r] |= 1
	}
	for _, r := range b {
		in[r] |= 2
	}
	if len(in) == 0 {
		return 1
	}
	both := 0
	for _, v := range in {
		if v == 3 {
			both++
		}
	}
	return float64(both) / float64(len(in))
}

func TestJaccard(t *testing.T) {
	for _, c := range []struct {
		a, b []string
		want float64
	}{{nil, nil, 1}, {[]string{"a"}, nil, 0}, {[]string{"a", "b"}, []string{"b", "a"}, 1}, {[]string{"a", "b"}, []string{"b", "c"}, 1.0 / 3}, {[]string{"a"}, []string{"a", "a"}, 1}} {
		if got := jaccard(c.a, c.b); got != c.want {
			t.Errorf("jaccard(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

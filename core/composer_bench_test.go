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
// fictional user, Sam, each with what a good composition must do. This
// test runs in CI with no model: every case is valid, its reference
// answer passes checkComposition and meets its expectations, invented
// refs are cleaned, and the fallback's order by weight is measured
// against "in front". composer_live_test.go runs the same cases on a real
// model.

const composerBenchZone = "Europe/Paris"

type composerCase struct {
	ID          string         `json:"id"`
	What        string         `json:"what"`
	Now         string         `json:"now"` // 2006-01-02 15:04, in composerBenchZone
	Moment      string         `json:"moment"`
	AwayMinutes int            `json:"away_minutes"`
	Remark      string         `json:"remark"`
	// Preferences are the user's preference memories about the workspace,
	// as sentences, the way the composer reads them (workspacePreferences).
	Preferences []string       `json:"preferences"`
	Items       []composerItem `json:"items"`
	Expect      composerExpect `json:"expect"`
	// Answer is a composition that meets Expect, as the composer would
	// write it.
	Answer json.RawMessage `json:"answer"`

	file string
}

type composerItem struct {
	Ref        string `json:"ref"`
	Weight     int    `json:"weight"`
	AgeMinutes int    `json:"age_minutes"`
	Title      string `json:"title"`
	Why        string `json:"why"`
	Say        string `json:"say"`
}

// composerExpect is what a good composition of a case does.
type composerExpect struct {
	Front  []string `json:"front"`  // must be modules
	Back   []string `json:"back"`   // must not be modules (chips are fine)
	Hidden []string `json:"hidden"` // must be neither module nor chip: the user's preferences exclude them
	Mode   string   `json:"mode"`   // side or center, "" for either
	// FallbackFront says whether the fallback's order by weight puts every
	// Front ref among its modules: false records a case where only the
	// composer can (a failure the user wants in front, with a low weight).
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

// snapshot is the case as production builds it (compositionSnapshot):
// the queue's items with their kind and title, the moment and the user's
// preferences from memory.
func (x composerCase) snapshot(t *testing.T) compositionSnapshot {
	t.Helper()
	now, err := time.ParseInLocation("2006-01-02 15:04", x.Now, mustZone(t, composerBenchZone))
	if err != nil {
		t.Fatalf("%s: now: %v", x.file, err)
	}
	s := compositionSnapshot{Moment: x.Moment, Remark: x.Remark, Away: time.Duration(x.AwayMinutes) * time.Minute,
		Preferences: slices.Clone(x.Preferences), Now: now}
	for _, it := range x.Items {
		kind, _, _ := strings.Cut(it.Ref, ":")
		s.Items = append(s.Items, snapshotItem{
			attentionItem: attentionItem{Ref: it.Ref, Weight: it.Weight, Why: it.Why, Say: it.Say, Source: "bench",
				Time: now.Add(-time.Duration(it.AgeMinutes) * time.Minute)},
			Kind: kind, Title: it.Title,
		})
	}
	return s
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
	case x.Expect.Mode != "" && x.Expect.Mode != modeSide && x.Expect.Mode != modeCenter:
		return "unknown mode " + x.Expect.Mode
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
		if it.Weight < 1 || it.Weight > 5 || it.AgeMinutes < 0 {
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
	if len(x.Expect.Front) > compositionItemsMax {
		return "more in front than the screen holds"
	}
	return ""
}

// composerScore is how a composition does against its case.
type composerScore struct {
	Invented    []string // refs the answer names that the queue does not hold
	MissFront   []string // front refs that are not modules
	FrontBack   []string // back refs shown as modules
	ShownHidden []string // hidden refs the answer chose (as module or chip)
	BadMode     bool
}

func (s composerScore) agrees() bool {
	return len(s.MissFront)+len(s.FrontBack)+len(s.ShownHidden) == 0 && !s.BadMode
}

func (s composerScore) String() string {
	if s.agrees() && len(s.Invented) == 0 {
		return "agrees"
	}
	var b []string
	for _, p := range []struct {
		what string
		refs []string
	}{{"invented", s.Invented}, {"not in front", s.MissFront}, {"in front, should wait", s.FrontBack}, {"shown, should be hidden", s.ShownHidden}} {
		if len(p.refs) > 0 {
			b = append(b, p.what+" "+strings.Join(p.refs, ","))
		}
	}
	if s.BadMode {
		b = append(b, "wrong mode")
	}
	return strings.Join(b, "; ")
}

// scoreComposition scores the composer's raw answer and the composition
// code made of it. Hidden is judged on what the answer chose and on what
// code shows (modules and chips): the composer hides what a preference
// excludes (its "hidden"), or code would show it as a chip.
func scoreComposition(x composerCase, s compositionSnapshot, raw string, comp composition) composerScore {
	var sc composerScore
	var out composerOutput
	json.Unmarshal([]byte(jsonObject(raw)), &out)
	chosen := slices.Clone(out.Chips)
	for _, it := range out.Items {
		chosen = append(chosen, it.Ref)
	}
	for _, r := range chosen {
		r = strings.TrimSpace(r)
		if _, ok := s.item(r); !ok {
			sc.Invented = append(sc.Invented, r)
		}
		if slices.Contains(x.Expect.Hidden, r) && !slices.Contains(sc.ShownHidden, r) {
			sc.ShownHidden = append(sc.ShownHidden, r)
		}
	}
	for _, r := range append(itemRefs(comp.Items), comp.Chips...) {
		if slices.Contains(x.Expect.Hidden, r) && !slices.Contains(sc.ShownHidden, r) {
			sc.ShownHidden = append(sc.ShownHidden, r)
		}
	}
	modules := itemRefs(comp.Items)
	for _, r := range x.Expect.Front {
		if !slices.Contains(modules, r) {
			sc.MissFront = append(sc.MissFront, r)
		}
	}
	for _, r := range append(slices.Clone(x.Expect.Back), x.Expect.Hidden...) {
		if slices.Contains(modules, r) {
			sc.FrontBack = append(sc.FrontBack, r)
		}
	}
	sc.BadMode = x.Expect.Mode != "" && comp.Mode != x.Expect.Mode
	return sc
}

func itemRefs(items []compositionItem) []string {
	refs := make([]string, 0, len(items))
	for _, it := range items {
		refs = append(refs, it.Ref)
	}
	return refs
}

func TestComposerBenchCases(t *testing.T) {
	cases := loadComposerCases(t)
	if len(cases) < 5 {
		t.Fatalf("%d cases, want 5 at least", len(cases))
	}
	ids := map[string]bool{}
	moments := map[string]bool{}
	for _, x := range cases {
		t.Run(x.ID, func(t *testing.T) {
			if bad := x.check(); bad != "" {
				t.Fatalf("%s: %s", x.file, bad)
			}
			if ids[x.ID] {
				t.Fatalf("%s: id %s twice", x.file, x.ID)
			}
			ids[x.ID], moments[x.Moment] = true, true
			s := x.snapshot(t)

			// The turn the composer reads names every ref, and only those,
			// and the user's preferences.
			turn := composerTurn(s)
			allowed := make([]string, 0, len(s.Items))
			for _, it := range s.Items {
				allowed = append(allowed, it.Ref)
			}
			if !strings.Contains(turn, "Refs permises : "+strings.Join(allowed, ", ")+"\n") {
				t.Errorf("turn without its refs:\n%s", turn)
			}
			for _, p := range s.Preferences {
				if !strings.Contains(turn, "- "+packQuote(p)) {
					t.Errorf("turn without the preference %q", p)
				}
			}
			if len(x.Expect.Hidden) > 0 && len(x.Preferences) == 0 {
				t.Errorf("%s: hidden refs with no preference to hide them", x.file)
			}

			// The reference answer passes as it is and meets the case.
			raw := string(x.Answer)
			comp, dropped, err := checkComposition(raw, s)
			if err != nil || dropped != 0 || comp.By != composedBy {
				t.Fatalf("answer: %v, %d dropped, by %s", err, dropped, comp.By)
			}
			if sc := scoreComposition(x, s, raw, comp); !sc.agrees() || len(sc.Invented) > 0 {
				t.Errorf("answer: %s", sc)
			}
			// Nothing of the queue goes unseen but what a preference hides.
			if n := len(comp.Items) + len(comp.Chips); n != min(len(s.Items)-len(x.Expect.Hidden), compositionItemsMax+compositionChipsMax) {
				t.Errorf("answer shows %d of %d", n, len(s.Items))
			}

			// Invented refs are cleaned: the same composition, and the
			// score counts them.
			var out map[string]any
			json.Unmarshal(x.Answer, &out)
			items, _ := out["items"].([]any)
			out["items"] = append([]any{map[string]any{"ref": "mail:invented-1", "weight": 5, "why": "inventé"}}, items...)
			chips, _ := out["chips"].([]any)
			out["chips"] = append(chips, "card:c-999")
			bad, _ := json.Marshal(out)
			cleaned, dropped, err := checkComposition(string(bad), s)
			if err != nil || dropped != 2 || refsOf(cleaned.Items) != refsOf(comp.Items) || !slices.Equal(cleaned.Chips, comp.Chips) {
				t.Errorf("invented refs: %v, %d dropped, %s vs %s", err, dropped, refsOf(cleaned.Items), refsOf(comp.Items))
			}
			if sc := scoreComposition(x, s, string(bad), cleaned); len(sc.Invented) != 2 {
				t.Errorf("invented counted %v", sc.Invented)
			}
			// Only invented refs: invalid when the queue holds something.
			if _, _, err := checkComposition(`{"mode":"side","eriphe":"","items":[{"ref":"mail:invented-1","weight":3,"why":"x"}],"chips":["card:c-999"]}`, s); err == nil {
				t.Error("an answer of invented refs accepted")
			}

			// The fallback, by weight, puts the front refs in front exactly
			// where the case says it can.
			fb := fallbackComposition(s)
			miss := scoreComposition(x, s, "", fb).MissFront
			if (len(miss) == 0) != x.Expect.FallbackFront {
				t.Errorf("fallback %s: front missed %v, fallback_front %v", refsOf(fb.Items), miss, x.Expect.FallbackFront)
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

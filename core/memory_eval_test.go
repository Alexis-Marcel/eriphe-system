package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The memory's evaluation, on a fictional freelancer (testdata/memoryeval).
// What needs no model runs here, in CI, with a fake clock: retrieval by
// family (recall@k of the memories that answer each question), the
// prospective checkpoints (Set-F1 of what should be due when), and the
// interrupt-or-not labels. What needs a model (extraction, answers) is
// memory_eval_model_test.go, behind the memoryeval build tag.
//
// The scores are logged (go test -run MemoryEval -v) and kept above a
// floor: a change that makes the memory worse fails here.

type evalCorpus struct {
	User       string                          `json:"user"`
	Now        string                          `json:"now"`
	Transcript []evalMessage                   `json:"transcript"`
	Tasks      []evalTask                      `json:"tasks"`
	Memories   []evalMemory                    `json:"memories"`
	Questions  []evalQuestion                  `json:"questions"`
	Prospect   evalProspective                 `json:"prospective"`
	Interrupt  struct{ Cases []evalInterrupt } `json:"interrupt"`
}

type evalMessage struct {
	At   string `json:"at"`
	From string `json:"from"`
	Text string `json:"text"`
}

type evalTask struct {
	ID, At, Prompt, Text string
}

type evalMemory struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Subject    string `json:"subject"`
	At         string `json:"at"`
	Text       string `json:"text"`
	Supersedes string `json:"supersedes"`
	ValidTo    string `json:"valid_to"`
	Trust      string `json:"trust"`
	Status     string `json:"status"`
	Pinned     bool   `json:"pinned"`
	// What a good extraction gives besides the text.
	Importance int    `json:"importance"`
	Keywords   string `json:"keywords"`
	// The kind a memory gets once the memory has it (decision, progress),
	// and a progress card's state; until then it is a fact.
	AsKind string `json:"as_kind"`
	State  string `json:"state"`
}

type evalQuestion struct {
	ID      string     `json:"id"`
	Family  string     `json:"family"`
	Q       string     `json:"q"`
	Expect  []string   `json:"expect"`
	Forbid  []string   `json:"forbid"`
	Keys    [][]string `json:"keys"`
	K       int        `json:"k"`
	Abstain bool       `json:"abstain"`
	// What a good brain passes to recall besides the words.
	Filters struct {
		AsOf        string   `json:"as_of"`
		Since       string   `json:"since"`
		Until       string   `json:"until"`
		IncludePast bool     `json:"include_past"`
		Kinds       []string `json:"kinds"`
	} `json:"filters"`
}

type evalProspective struct {
	Intentions []evalIntention `json:"intentions"`
	Events     []struct {
		At, Key, Op, Due, Remind, Until string
	} `json:"events"`
	Checks []struct {
		At  string   `json:"at"`
		Due []string `json:"due"`
	} `json:"checks"`
}

type evalIntention struct {
	Key, Kind, Owner, To, Text, Created, Due, Remind string
}

type evalInterrupt struct {
	ID, What, Source, Urgency string
	Importance                int
	Busy                      bool
	QuietHours                bool `json:"quiet_hours"`
	Label                     string
}

func loadEvalCorpus(t testing.TB) *evalCorpus {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "memoryeval", "camille.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c evalCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("corpus: %v", err)
	}
	return &c
}

// evalTime reads a local time of the corpus: 2026-09-01T09:05 or 2026-09-01.
func evalTime(t testing.TB, s string) time.Time {
	t.Helper()
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02"} {
		if x, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return x
		}
	}
	t.Fatalf("corpus time %q", s)
	return time.Time{}
}

// seedEval writes the corpus's memories as a good extraction would have,
// each at its own time; it returns their ids by key.
func seedEval(t testing.TB, m *memStore, clock *fakeClock, c *evalCorpus) map[string]int64 {
	t.Helper()
	xs := append([]evalMemory(nil), c.Memories...)
	sort.SliceStable(xs, func(i, j int) bool { return evalTime(t, xs[i].At).Before(evalTime(t, xs[j].At)) })
	idOf := map[string]int64{}
	for _, e := range xs {
		clock.t = evalTime(t, e.At)
		x := &Memory{Kind: e.Kind, Subject: e.Subject, Text: e.Text, Pinned: e.Pinned, SourceKind: srcExtraction,
			Trust: trustOwner, Status: memActive, SourceRef: "msg:1"}
		evalExtras(x, e)
		if e.Kind == kindEpisode {
			x.OccurredAt = clock.t
		}
		if e.Trust != "" {
			x.Trust = e.Trust
		}
		if e.Status != "" {
			x.Status = e.Status
		}
		if e.ValidTo != "" {
			x.ValidTo = evalTime(t, e.ValidTo)
		}
		if e.Supersedes != "" {
			x.Supersedes = idOf[e.Supersedes]
		}
		if err := m.add(x, "eval"); err != nil {
			t.Fatalf("seed %s: %v", e.Key, err)
		}
		idOf[e.Key] = x.ID
	}
	clock.t = evalTime(t, c.Now)
	m.owner = func() string { return c.User }
	return idOf
}

// evalNoExtras seeds the text only, as an extraction that gives nothing
// else would; the eval reports both.
var evalNoExtras bool

// evalExtras gives a seeded memory what the extraction adds besides its
// text: its keywords and importance.
func evalExtras(x *Memory, e evalMemory) {
	if !evalNoExtras {
		x.Keywords, x.Importance = e.Keywords, e.Importance
	}
}

// evalRecall is the recall of a question: its words only (raw), or with
// the filters a good brain would add (tool).
func evalRecall(t testing.TB, m *memStore, q evalQuestion, tool bool, k int) []*Memory {
	t.Helper()
	rq := recallQuery{Query: q.Q, Limit: k}
	if tool {
		f := q.Filters
		rq.AsOf, rq.IncludePast, rq.Kinds = evalOptTime(t, f.AsOf), f.IncludePast, f.Kinds
		rq.Since = evalOptTime(t, f.Since)
		if f.Until != "" {
			rq.Until = evalTime(t, f.Until).Add(24*time.Hour - time.Second)
		}
	}
	xs, err := m.recall(rq)
	if err != nil {
		t.Fatalf("%s: %v", q.ID, err)
	}
	return xs
}

func evalOptTime(t testing.TB, s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	return evalTime(t, s)
}

// retrievalScore is a family's score: the mean recall@k of the memories
// that answer its questions, and how many questions got a forbidden one
// (a fact no longer true, an external proposal).
type retrievalScore struct {
	N      int
	Recall float64
	Stale  int
	Missed []string
}

func (s retrievalScore) String() string {
	if s.N == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%5.1f%% (%d q, %d stale)", 100*s.Recall/float64(s.N), s.N, s.Stale)
}

func (s retrievalScore) pct() float64 {
	if s.N == 0 {
		return 0
	}
	return 100 * s.Recall / float64(s.N)
}

var evalFamilies = []string{"extraction", "multi_session", "temporal", "update", "decision", "progress", "abstention"}

// scoreRetrieval runs every question with an expected memory.
func scoreRetrieval(t testing.TB, m *memStore, c *evalCorpus, idOf map[string]int64, tool bool) map[string]*retrievalScore {
	out := map[string]*retrievalScore{"all": {}}
	for _, f := range evalFamilies {
		out[f] = &retrievalScore{}
	}
	for _, q := range c.Questions {
		if len(q.Expect) == 0 {
			continue
		}
		k := q.K
		if k == 0 {
			k = 5
		}
		got := map[int64]bool{}
		xs := evalRecall(t, m, q, tool, k)
		for _, x := range xs {
			got[x.ID] = true
		}
		// ERIPHE_EVAL_DEBUG=tmp-01 shows what a question gets.
		if os.Getenv("ERIPHE_EVAL_DEBUG") == q.ID {
			t.Logf("%s (tool %v) %q:", q.ID, tool, q.Q)
			for _, x := range xs {
				t.Logf("  %.3f #%d %s", x.Score, x.ID, x.Text)
			}
		}
		found := 0
		for _, key := range q.Expect {
			id, ok := idOf[key]
			if !ok {
				t.Fatalf("%s expects unknown memory %q", q.ID, key)
			}
			if got[id] {
				found++
			}
		}
		stale := false
		for _, key := range q.Forbid {
			if got[idOf[key]] {
				stale = true
			}
		}
		r := float64(found) / float64(len(q.Expect))
		for _, s := range []*retrievalScore{out[q.Family], out["all"]} {
			s.N++
			s.Recall += r
			if stale {
				s.Stale++
			}
			if r < 1 {
				s.Missed = append(s.Missed, q.ID)
			}
		}
	}
	return out
}

// retrievalFloors are the scores the memory must keep (percent of recall@k,
// raw then tool), a little under what it reaches; raise them when it gets
// better.
var retrievalFloors = map[string][2]float64{
	"extraction":    {95, 95},
	"multi_session": {70, 75},
	"decision":      {95, 95},
	"progress":      {95, 95},
	"temporal":      {70, 80},
	"update":        {90, 90},
	"all":           {88, 90},
}

func TestMemoryEvalRetrieval(t *testing.T) {
	c := loadEvalCorpus(t)
	// The text alone first, for the record: what keywords and importance add.
	evalNoExtras = true
	m0, clock0 := testMemory(t)
	ids0 := seedEval(t, m0, clock0, c)
	evalNoExtras = false
	raw0, tool0 := scoreRetrieval(t, m0, c, ids0, false), scoreRetrieval(t, m0, c, ids0, true)
	t.Logf("recall@k, text only: raw %s, tool %s", raw0["all"], tool0["all"])
	m, clock := testMemory(t)
	idOf := seedEval(t, m, clock, c)
	raw := scoreRetrieval(t, m, c, idOf, false)
	tool := scoreRetrieval(t, m, c, idOf, true)
	t.Logf("recall@k by family (raw: the question's words; tool: with the filters a good brain adds)")
	for _, f := range append(evalFamilies, "all") {
		t.Logf("  %-14s raw %-26s tool %s", f, raw[f], tool[f])
	}
	t.Logf("  missed (raw): %s", strings.Join(raw["all"].Missed, " "))
	t.Logf("  missed (tool): %s", strings.Join(tool["all"].Missed, " "))
	for f, floor := range retrievalFloors {
		if got := raw[f].pct(); got < floor[0] {
			t.Errorf("%s: raw recall %.1f%% under its floor %.1f%%", f, got, floor[0])
		}
		if got := tool[f].pct(); got < floor[1] {
			t.Errorf("%s: tool recall %.1f%% under its floor %.1f%%", f, got, floor[1])
		}
	}
	// What is no longer true never comes back for a question about now.
	if raw["update"].Stale > 0 || tool["update"].Stale > 0 {
		t.Errorf("stale facts returned for questions about now: raw %d, tool %d", raw["update"].Stale, tool["update"].Stale)
	}
}

// prospectiveSystem is what the prospective checkpoints run against: it is
// told the intentions and what happens to them, and says what is due.
type prospectiveSystem interface {
	add(t testing.TB, in evalIntention)
	event(t testing.TB, key, op string, due, remind, until time.Time)
	due(t testing.TB) []string
}

// setF1 accumulates true positives, false positives and misses.
type setF1 struct{ TP, FP, FN int }

func (s *setF1) add(want, got []string) {
	for _, g := range got {
		if containsStr(want, g) {
			s.TP++
		} else {
			s.FP++
		}
	}
	for _, w := range want {
		if !containsStr(got, w) {
			s.FN++
		}
	}
}

func (s setF1) F1() float64 {
	if s.TP == 0 {
		return 0
	}
	return 100 * 2 * float64(s.TP) / float64(2*s.TP+s.FP+s.FN)
}

// runProspective replays the corpus's week: intentions as they are said,
// events at their time, and a check at each checkpoint.
func runProspective(t testing.TB, c *evalCorpus, clock *fakeClock, sys prospectiveSystem) (setF1, []string) {
	type step struct {
		at    time.Time
		order int
		do    func()
	}
	var steps []step
	for _, in := range c.Prospect.Intentions {
		in := in
		steps = append(steps, step{evalTime(t, in.Created), 0, func() { sys.add(t, in) }})
	}
	for _, e := range c.Prospect.Events {
		e := e
		steps = append(steps, step{evalTime(t, e.At), 1, func() {
			sys.event(t, e.Key, e.Op, evalOptTime(t, e.Due), evalOptTime(t, e.Remind), evalOptTime(t, e.Until))
		}})
	}
	var score setF1
	var wrong []string
	for _, ch := range c.Prospect.Checks {
		ch := ch
		steps = append(steps, step{evalTime(t, ch.At), 2, func() {
			got := sys.due(t)
			sort.Strings(got)
			want := append([]string{}, ch.Due...)
			sort.Strings(want)
			score.add(want, got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				wrong = append(wrong, fmt.Sprintf("%s want [%s] got [%s]", ch.At, strings.Join(want, ","), strings.Join(got, ",")))
			}
		}})
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if !steps[i].at.Equal(steps[j].at) {
			return steps[i].at.Before(steps[j].at)
		}
		return steps[i].order < steps[j].order
	})
	for _, s := range steps {
		clock.t = s.at
		s.do()
	}
	return score, wrong
}

// commitmentSystem is the memory before typed intentions: a commitment
// with a due date, and what the wake packet would surface (open ones due
// within 24 h, or overdue). It has no reminder time, no snooze, and a
// waiting-for is a commitment like another.
type commitmentSystem struct {
	m   *memStore
	ids map[string]int64
}

func (s *commitmentSystem) add(t testing.TB, in evalIntention) {
	x := mustAdd(t.(*testing.T), s.m, &Memory{Kind: kindCommitment, Subject: in.Owner, Text: in.Text, DueAt: evalTime(t, in.Due)})
	s.ids[in.Key] = x.ID
}

func (s *commitmentSystem) event(t testing.TB, key, op string, due, remind, until time.Time) {
	p := memoryPatch{}
	switch op {
	case "done":
		p.State = ptr(stateDone)
	case "cancel":
		p.State = ptr(stateDropped)
	case "reschedule":
		p.DueAt = &due
	default:
		return // a snooze cannot be said
	}
	if _, err := s.m.update(s.ids[key], p, "eval", ""); err != nil {
		t.Fatalf("%s %s: %v", op, key, err)
	}
}

func (s *commitmentSystem) due(t testing.TB) []string {
	p, err := s.m.wakePacket()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range p.Due {
		for k, id := range s.ids {
			if id == x.ID {
				out = append(out, k)
			}
		}
	}
	return out
}

// prospectiveFloor is the Set-F1 the prospective checkpoints must keep.
const prospectiveFloor = 75.0

func TestMemoryEvalProspective(t *testing.T) {
	c := loadEvalCorpus(t)
	m, clock := testMemory(t)
	score, wrong := runProspective(t, c, clock, &commitmentSystem{m: m, ids: map[string]int64{}})
	t.Logf("prospective (commitments and the wake packet): Set-F1 %.1f%% (TP %d, FP %d, FN %d) over %d checkpoints",
		score.F1(), score.TP, score.FP, score.FN, len(c.Prospect.Checks))
	for _, w := range wrong {
		t.Logf("  %s", w)
	}
	if score.F1() < prospectiveFloor {
		t.Errorf("prospective Set-F1 %.1f%% under its floor %.1f%%", score.F1(), prospectiveFloor)
	}
}

// interruptPolicy says, for a labelled situation, now, brief or none.
type interruptPolicy func(evalInterrupt) string

// neverInterrupts is Eriphe today: it speaks only when spoken to.
func neverInterrupts(evalInterrupt) string { return "none" }

func TestMemoryEvalInterrupt(t *testing.T) {
	c := loadEvalCorpus(t)
	if len(c.Interrupt.Cases) == 0 {
		t.Fatal("no interrupt cases")
	}
	right, falseNow, now := 0, 0, 0
	for _, x := range c.Interrupt.Cases {
		got := neverInterrupts(x)
		if got == x.Label {
			right++
		}
		if x.Label == "now" {
			now++
		}
		if got == "now" && x.Label != "now" {
			falseNow++
		}
	}
	t.Logf("interrupt or not (never interrupts): %d/%d right, %d of %d interruptions made, %d false",
		right, len(c.Interrupt.Cases), 0, now, falseNow)
}

// The corpus itself: unique keys and ids, known families, and 120 items.
func TestMemoryEvalCorpus(t *testing.T) {
	c := loadEvalCorpus(t)
	keys := map[string]bool{}
	for _, x := range c.Memories {
		if keys[x.Key] {
			t.Errorf("memory key %q twice", x.Key)
		}
		keys[x.Key] = true
		if x.Supersedes != "" && !keys[x.Supersedes] {
			t.Errorf("%s supersedes %q, not defined before it", x.Key, x.Supersedes)
		}
	}
	seen := map[string]bool{}
	for _, q := range c.Questions {
		if seen[q.ID] {
			t.Errorf("question %q twice", q.ID)
		}
		seen[q.ID] = true
		if !containsStr(evalFamilies, q.Family) {
			t.Errorf("%s: family %q", q.ID, q.Family)
		}
		if len(q.Keys) == 0 {
			t.Errorf("%s: no answer keys", q.ID)
		}
		for _, k := range append(append([]string{}, q.Expect...), q.Forbid...) {
			if !keys[k] {
				t.Errorf("%s: unknown memory %q", q.ID, k)
			}
		}
	}
	n := len(c.Questions) + len(c.Prospect.Checks) + len(c.Interrupt.Cases)
	if n < 120 {
		t.Errorf("%d evaluation items, want at least 120", n)
	}
	// Nothing personal: e-mail addresses are example.org's.
	raw, _ := os.ReadFile(filepath.Join("testdata", "memoryeval", "camille.json"))
	for _, w := range strings.Fields(string(raw)) {
		if at := strings.Index(w, "@"); at > 0 && !strings.Contains(w[at:], "example.org") {
			t.Errorf("an address outside example.org: %s", w)
		}
	}
}

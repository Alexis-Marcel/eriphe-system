//go:build memoryeval

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Public benchmarks, adapted to Eriphe's memory: retrieval only, no model,
// so the numbers say what recall itself finds. The data is not in the
// repository; point at a local copy:
//
//	ERIPHE_EVAL_LOCOMO=/path/locomo10.json            (github.com/snap-research/locomo, data/)
//	ERIPHE_EVAL_LONGMEMEVAL=/path/longmemeval_s.json  (the LongMemEval_S file)
//	ERIPHE_EVAL_LONGMEMEVAL_N=100                     (questions taken, 100 by default)
//
//	go test -tags memoryeval -run MemoryBench -v -timeout 1h .
//
// Both are English: the French stemming does nothing for them, the rest of
// recall (ranking, time, the fact keys) does.

// benchMemory is a fresh memory with a clock.
func benchMemory(t *testing.T) (*memStore, *fakeClock) {
	m, err := openMemory(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.db.Close() })
	clock := newClock()
	m.now = clock.now
	return m, clock
}

// benchAdd writes one unit (a turn, an observation) dated when.
func benchAdd(t *testing.T, m *memStore, clock *fakeClock, kind, text string, when time.Time, ref string) int64 {
	clock.t = when
	x := &Memory{Kind: kind, Text: truncate(oneLine(text), episodeTextMax-2), SourceKind: srcImport, SourceRef: ref, Trust: trustOwner, Status: memActive}
	if kind == kindEpisode {
		x.OccurredAt = when
	}
	if err := m.add(x, "bench"); err != nil {
		t.Fatalf("%s: %v", ref, err)
	}
	return x.ID
}

type benchScore struct {
	n        int
	recall5  float64
	recall10 float64
}

func (s *benchScore) add(want []string, got []string) {
	if len(want) == 0 {
		return
	}
	hit := func(k int) float64 {
		seen := map[string]bool{}
		for i, g := range got {
			if i >= k {
				break
			}
			seen[g] = true
		}
		n := 0
		for _, w := range want {
			if seen[w] {
				n++
			}
		}
		return float64(n) / float64(len(want))
	}
	s.n++
	s.recall5 += hit(5)
	s.recall10 += hit(10)
}

func (s benchScore) String() string {
	if s.n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("R@5 %5.1f%%  R@10 %5.1f%%  (%d q)", 100*s.recall5/float64(s.n), 100*s.recall10/float64(s.n), s.n)
}

// LoCoMo: ten long conversations; each question cites the turns (D1:3)
// that answer it. Two units are indexed, as the LoCoMo paper compares
// them: the dialogue turns, and the per-session observations the dataset
// ships (an extraction's facts, each citing its turn).

type locomoConv struct {
	SampleID     string                     `json:"sample_id"`
	Conversation map[string]json.RawMessage `json:"conversation"`
	Observation  map[string]map[string][][]any
	QA           []struct {
		Question string   `json:"question"`
		Evidence []string `json:"evidence"`
		Category int      `json:"category"`
	} `json:"qa"`
}

var locomoSession = regexp.MustCompile(`^session_(\d+)$`)

func locomoDate(s string) time.Time {
	// "1:56 pm on 8 May, 2023"
	t, err := time.ParseInLocation("3:04 pm on 2 January, 2006", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func TestMemoryBenchLoCoMo(t *testing.T) {
	path := os.Getenv("ERIPHE_EVAL_LOCOMO")
	if path == "" {
		t.Skip("ERIPHE_EVAL_LOCOMO is not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var convs []locomoConv
	if err := json.Unmarshal(raw, &convs); err != nil {
		t.Fatal(err)
	}
	scores := map[string]*benchScore{}
	score := func(unit string, cat int) *benchScore {
		k := fmt.Sprintf("%s/cat%d", unit, cat)
		if scores[k] == nil {
			scores[k] = &benchScore{}
		}
		return scores[k]
	}
	for _, cv := range convs {
		turns, turnClock := benchMemory(t)
		obs, obsClock := benchMemory(t)
		refOf := map[int64]string{}
		obsRef := map[int64]string{}
		var last time.Time
		for key, val := range cv.Conversation {
			mm := locomoSession.FindStringSubmatch(key)
			if mm == nil {
				continue
			}
			var date string
			json.Unmarshal(cv.Conversation[key+"_date_time"], &date)
			when := locomoDate(date)
			if when.After(last) {
				last = when
			}
			var dialog []struct{ Speaker, Text, Dia_id string }
			json.Unmarshal(val, &dialog)
			for i, d := range dialog {
				id := benchAdd(t, turns, turnClock, kindEpisode, d.Speaker+": "+d.Text, when.Add(time.Duration(i)*time.Minute), d.Dia_id)
				refOf[id] = d.Dia_id
			}
			for _, items := range cv.Observation[key+"_observation"] {
				for _, it := range items {
					if len(it) < 2 {
						continue
					}
					text, _ := it[0].(string)
					var dia string
					switch v := it[1].(type) {
					case string:
						dia = v
					case []any:
						if len(v) > 0 {
							dia, _ = v[0].(string)
						}
					}
					id := benchAdd(t, obs, obsClock, kindFact, text, when, dia)
					obsRef[id] = dia
				}
			}
		}
		turnClock.t, obsClock.t = last.Add(24*time.Hour), last.Add(24*time.Hour)
		for _, qa := range cv.QA {
			if qa.Category == 5 {
				continue // adversarial: the answer is not in the conversation
			}
			for _, u := range []struct {
				name string
				m    *memStore
				refs map[int64]string
			}{{"turns", turns, refOf}, {"observations", obs, obsRef}} {
				xs, err := u.m.recall(recallQuery{Query: qa.Question, Limit: 10, IncludePast: true})
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, x := range xs {
					got = append(got, u.refs[x.ID])
				}
				score(u.name, qa.Category).add(qa.Evidence, got)
				score(u.name, 0).add(qa.Evidence, got)
			}
		}
	}
	var keys []string
	for k := range scores {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("LoCoMo retrieval (categories 1-4; cat0 is all), evidence turns found among recall's results:")
	for _, k := range keys {
		t.Logf("  %-20s %s", k, scores[k])
	}
}

// LongMemEval_S: each question has its own haystack of ~50 sessions; the
// turns that hold the answer are marked has_answer. Indexed per turn (the
// paper's best unit), the user's and the assistant's.

type lmeQuestion struct {
	ID       string `json:"question_id"`
	Type     string `json:"question_type"`
	Question string `json:"question"`
	Date     string `json:"question_date"`
	Sessions [][]struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		HasAnswer bool   `json:"has_answer"`
	} `json:"haystack_sessions"`
	SessionIDs    []string `json:"haystack_session_ids"`
	Dates         []string `json:"haystack_dates"`
	AnswerSession []string `json:"answer_session_ids"`
}

func lmeDate(s string) time.Time {
	// "2023/05/20 (Sat) 02:21"
	if i := strings.Index(s, " ("); i > 0 {
		if j := strings.Index(s, ") "); j > i {
			s = s[:i] + " " + s[j+2:]
		}
	}
	t, _ := time.ParseInLocation("2006/01/02 15:04", s, time.Local)
	return t
}

func TestMemoryBenchLongMemEval(t *testing.T) {
	path := os.Getenv("ERIPHE_EVAL_LONGMEMEVAL")
	if path == "" {
		t.Skip("ERIPHE_EVAL_LONGMEMEVAL is not set")
	}
	n := 100
	if v, err := strconv.Atoi(os.Getenv("ERIPHE_EVAL_LONGMEMEVAL_N")); err == nil && v > 0 {
		n = v
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var qs []lmeQuestion
	if err := json.Unmarshal(raw, &qs); err != nil {
		t.Fatal(err)
	}
	// A fixed sample across the types: every k-th question, abstention left out.
	var pick []lmeQuestion
	for _, q := range qs {
		if !strings.HasSuffix(q.ID, "_abs") {
			pick = append(pick, q)
		}
	}
	if step := len(pick) / n; step > 1 {
		var s []lmeQuestion
		for i := 0; i < len(pick) && len(s) < n; i += step {
			s = append(s, pick[i])
		}
		pick = s
	}
	turnScore, sessScore := map[string]*benchScore{}, map[string]*benchScore{}
	var spent time.Duration
	turns := 0
	for _, q := range pick {
		m, clock := benchMemory(t)
		turnRef := map[int64]string{}
		var answers []string
		for si, sess := range q.Sessions {
			when := lmeDate(q.Dates[si])
			for ti, turn := range sess {
				ref := fmt.Sprintf("%s#%d", q.SessionIDs[si], ti)
				if strings.TrimSpace(turn.Content) == "" {
					continue
				}
				id := benchAdd(t, m, clock, kindEpisode, turn.Role+": "+turn.Content, when.Add(time.Duration(ti)*time.Minute), ref)
				turnRef[id] = ref
				if turn.HasAnswer {
					answers = append(answers, ref)
				}
			}
		}
		clock.t = lmeDate(q.Date)
		began := time.Now()
		xs, err := m.recall(recallQuery{Query: q.Question, Limit: 10, IncludePast: true})
		spent += time.Since(began)
		turns += len(turnRef)
		if err != nil {
			t.Fatal(err)
		}
		var got, gotSess []string
		for _, x := range xs {
			ref := turnRef[x.ID]
			got = append(got, ref)
			s, _, _ := strings.Cut(ref, "#")
			if !containsStr(gotSess, s) {
				gotSess = append(gotSess, s)
			}
		}
		for _, k := range []string{q.Type, "all"} {
			if turnScore[k] == nil {
				turnScore[k], sessScore[k] = &benchScore{}, &benchScore{}
			}
			turnScore[k].add(answers, got)
			sessScore[k].add(q.AnswerSession, gotSess)
		}
		m.db.Close()
	}
	var keys []string
	for k := range turnScore {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("LongMemEval_S retrieval, %d questions (turn: has_answer turns found; session: answer sessions among the sessions of the top turns); %.1f ms per recall over %d turns on average:",
		len(pick), float64(spent.Microseconds())/1000/float64(max(len(pick), 1)), turns/max(len(pick), 1))
	for _, k := range keys {
		t.Logf("  %-26s turn %s | session %s", k, turnScore[k], sessScore[k])
	}
}

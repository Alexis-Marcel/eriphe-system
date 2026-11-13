package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The threads' bench on a real model: each turn of testdata/threads (a
// case's request, and its wake if it has one), ERIPHE_LIVE_THREADS_RUNS
// times (1 by default), through the local Claude Code CLI as the surfaces'
// bench runs it (runClaude): the brain's « Les threads », its paragraphs on
// surfaces and the workspace, its tools for threads and the workspace (as
// an MCP client lists them) and the catalog appended to the system prompt,
// the case's turn as the prompt, no tool, low effort, no session kept. It
// needs no node, only `claude` signed in on this machine.
//
//	ERIPHE_LIVE_THREADS=1 go test -run TestThreadsLive -v .
//
// ERIPHE_LIVE_THREADS_MODEL picks the model (sonnet by default),
// ERIPHE_LIVE_THREADS_CLI the command (claude by default),
// ERIPHE_LIVE_THREADS_CASES the cases (ids, comma-separated; all by
// default). Each answer is played and scored as the bench scores its
// reference (threadRun.score); a wake is played after the case's reference
// first turn, so each turn is judged on its own. It reports, per turn and
// in all: the faults, agreement, latency and cost. It fails only when the
// CLI does; the numbers are for reading.

type threadLiveRun struct {
	latency time.Duration
	cost    float64
	score   threadScore
}

func TestThreadsLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_THREADS") == "" {
		t.Skip("set ERIPHE_LIVE_THREADS=1 to run the threads' cases on a real model")
	}
	runs := 1
	if v := os.Getenv("ERIPHE_LIVE_THREADS_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("ERIPHE_LIVE_THREADS_RUNS=%q", v)
		}
		runs = n
	}
	model := cmpOr(os.Getenv("ERIPHE_LIVE_THREADS_MODEL"), "sonnet")
	cli := cmpOr(os.Getenv("ERIPHE_LIVE_THREADS_CLI"), "claude")
	if _, err := exec.LookPath(cli); err != nil {
		t.Fatalf("%s: %v", cli, err)
	}
	var only []string
	if v := os.Getenv("ERIPHE_LIVE_THREADS_CASES"); v != "" {
		for _, id := range strings.Split(v, ",") {
			only = append(only, strings.TrimSpace(id))
		}
	}
	dir := t.TempDir() // no CLAUDE.md of this repository in its context
	cases := loadThreadCases(t)
	system := threadInstructions(t, benchThreadCore(t, cases[0]), "Sam")

	var all []threadLiveRun
	ask := func(x threadCase, phase string, turn func() (*core, *threadRun, string)) {
		var got []threadLiveRun
		for i := range runs {
			c, r, prompt := turn()
			start := time.Now()
			res, err := runClaude(cli, dir, model, system, prompt)
			if err != nil {
				t.Fatalf("%s %s run %d: %v", x.ID, phase, i+1, err)
			}
			lr := threadLiveRun{latency: time.Since(start), cost: res.Cost}
			if a, err := parseThreadAnswer(res.Result); err != nil {
				lr.score = threadScore{fmt.Sprintf("%s: %v", faultParse, err)}
			} else {
				r.play(t, c, x, a)
				lr.score = r.score(c, x, a)
			}
			t.Logf("%s %s run %d: %s, $%.4f: %s", x.ID, phase, i+1, lr.latency.Round(100*time.Millisecond), lr.cost, lr.score)
			if len(lr.score) > 0 {
				t.Logf("%s %s run %d answered: %.1500s", x.ID, phase, i+1, res.Result)
			}
			got = append(got, lr)
		}
		t.Logf("%s %s: %s", x.ID, phase, summarizeThreadRuns(got))
		all = append(all, got...)
	}
	for _, x := range cases {
		if only != nil && !slices.Contains(only, x.ID) {
			continue
		}
		ask(x, phaseAsk, func() (*core, *threadRun, string) {
			c := benchThreadCore(t, x)
			r := beginAsk(c, x)
			return c, r, threadTurn(x, c, r, "")
		})
		if x.Wake == nil {
			continue
		}
		ask(x, phaseWake, func() (*core, *threadRun, string) {
			c, _, _ := playAsk(t, x, x.Answer)
			r, event, err := beginWake(c, x)
			if err != nil {
				t.Fatalf("%s wake: %v", x.ID, err)
			}
			return c, r, threadTurn(x, c, r, event)
		})
	}
	t.Logf("all (%s, %d runs a turn): %s", model, runs, summarizeThreadRuns(all))
}

func summarizeThreadRuns(runs []threadLiveRun) string {
	var total, longest time.Duration
	var cost float64
	agree := 0
	faults := map[string]int{}
	for _, r := range runs {
		total += r.latency
		longest = max(longest, r.latency)
		cost += r.cost
		if len(r.score) == 0 {
			agree++
		}
		for _, f := range r.score {
			name, _, _ := strings.Cut(f, ":")
			if strings.HasPrefix(f, faultReply) {
				name = faultReply
			}
			faults[name]++
		}
	}
	var fs []string
	for f, n := range faults {
		fs = append(fs, fmt.Sprintf("%s %d", f, n))
	}
	slices.Sort(fs)
	n := max(len(runs), 1)
	return fmt.Sprintf("agreement %d/%d, faults [%s], latency mean %s max %s, cost $%.4f (mean $%.4f)",
		agree, len(runs), strings.Join(fs, ", "),
		(total / time.Duration(n)).Round(100*time.Millisecond), longest.Round(100*time.Millisecond), cost, cost/float64(n))
}

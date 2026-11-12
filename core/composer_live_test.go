package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The composer's bench on a real model: each case of testdata/composer,
// ERIPHE_LIVE_COMPOSER_RUNS times (3 by default), through the local Claude
// Code CLI as the composer's task runs it: the same instructions appended
// to the system prompt, the same turn text, no tool, low effort, no
// session kept. It needs no node, only `claude` signed in on this machine,
// and costs a few cents a run.
//
//	ERIPHE_LIVE_COMPOSER=1 go test -run TestComposerLive -v .
//
// ERIPHE_LIVE_COMPOSER_MODEL picks the model (sonnet by default),
// ERIPHE_LIVE_COMPOSER_CLI the command (claude by default). It reports,
// per case and in all: latency (mean, max), cost, invalid answers,
// invented refs, agreement with the case's expectations and stability
// (the mean Jaccard of the modules' refs between runs). It fails only when
// the CLI does; the numbers are for reading.

type composerLiveRun struct {
	latency  time.Duration
	cost     float64
	invalid  bool
	invented int
	agrees   bool
	modules  []string
	score    composerScore
}

// claudeResult is what `claude -p --output-format json` prints.
type claudeResult struct {
	Result  string  `json:"result"`
	IsError bool    `json:"is_error"`
	Cost    float64 `json:"total_cost_usd"`
}

func TestComposerLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_COMPOSER") == "" {
		t.Skip("set ERIPHE_LIVE_COMPOSER=1 to run the composer's cases on a real model")
	}
	runs := 3
	if v := os.Getenv("ERIPHE_LIVE_COMPOSER_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("ERIPHE_LIVE_COMPOSER_RUNS=%q", v)
		}
		runs = n
	}
	model := cmpOr(os.Getenv("ERIPHE_LIVE_COMPOSER_MODEL"), "sonnet")
	cli := cmpOr(os.Getenv("ERIPHE_LIVE_COMPOSER_CLI"), "claude")
	if _, err := exec.LookPath(cli); err != nil {
		t.Fatalf("%s: %v", cli, err)
	}
	system := composerSystemPrompt("Sam")
	dir := t.TempDir() // no CLAUDE.md of this repository in its context

	var all []composerLiveRun
	var stability []float64
	for _, x := range loadComposerCases(t) {
		s := x.snapshot(t)
		turn := composerTurn(s)
		var got []composerLiveRun
		for i := range runs {
			start := time.Now()
			res, err := runClaude(cli, dir, model, system, turn)
			if err != nil {
				t.Fatalf("%s run %d: %v", x.ID, i+1, err)
			}
			r := composerLiveRun{latency: time.Since(start), cost: res.Cost}
			comp, _, err := checkComposition(res.Result, s)
			if err != nil {
				r.invalid = true
				comp = fallbackComposition(s)
				t.Logf("%s run %d: invalid answer (%v): %.200q", x.ID, i+1, err, res.Result)
			}
			r.score = scoreComposition(x, s, res.Result, comp)
			r.invented, r.agrees, r.modules = len(r.score.Invented), !r.invalid && r.score.agrees(), itemRefs(comp.Items)
			t.Logf("%s run %d: %s, $%.4f, modules %v: %s", x.ID, i+1, r.latency.Round(100*time.Millisecond), r.cost, r.modules, r.score)
			got = append(got, r)
		}
		j := meanJaccard(got)
		stability = append(stability, j)
		t.Logf("%s: %s, stability %.2f", x.ID, summarizeLive(got), j)
		all = append(all, got...)
	}
	mean := 0.0
	for _, j := range stability {
		mean += j
	}
	t.Logf("all (%s, %d runs a case): %s, stability %.2f", model, runs, summarizeLive(all), mean/float64(len(stability)))
}

// runClaude runs one composition turn: the composer's instructions after
// Claude Code's own, as its task gets them, no tool, low effort.
func runClaude(cli, dir, model, system, turn string) (claudeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, "-p", "--model", model, "--effort", "low", "--append-system-prompt", system,
		"--tools", "", "--output-format", "json", "--no-session-persistence", "--strict-mcp-config", "--disable-slash-commands")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_EFFORT_LEVEL=low")
	cmd.Stdin = bytes.NewBufferString(turn)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return claudeResult{}, fmt.Errorf("%v: %s", err, stderr.String())
	}
	var res claudeResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return claudeResult{}, fmt.Errorf("output %.200q: %v", out.String(), err)
	}
	if res.IsError {
		return claudeResult{}, fmt.Errorf("the CLI failed: %.200q", res.Result)
	}
	return res, nil
}

// meanJaccard is the mean Jaccard of the modules' refs over every pair of
// runs: 1 when each run shows the same modules.
func meanJaccard(runs []composerLiveRun) float64 {
	sum, n := 0.0, 0
	for i := range runs {
		for j := i + 1; j < len(runs); j++ {
			sum += jaccard(runs[i].modules, runs[j].modules)
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return sum / float64(n)
}

func summarizeLive(runs []composerLiveRun) string {
	var total, longest time.Duration
	var cost float64
	var invalid, invented, agree int
	for _, r := range runs {
		total += r.latency
		longest = max(longest, r.latency)
		cost += r.cost
		invented += r.invented
		if r.invalid {
			invalid++
		}
		if r.agrees {
			agree++
		}
	}
	n := max(len(runs), 1)
	return fmt.Sprintf("latency mean %s max %s, cost $%.4f (mean $%.4f), invalid %d, invented refs %d, agreement %d/%d",
		(total / time.Duration(n)).Round(100*time.Millisecond), longest.Round(100*time.Millisecond), cost, cost/float64(n),
		invalid, invented, agree, len(runs))
}

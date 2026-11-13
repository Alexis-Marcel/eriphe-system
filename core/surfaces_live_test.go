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

// The surfaces' bench on a real model: each case of testdata/surfaces,
// ERIPHE_LIVE_SURFACES_RUNS times (1 by default), through the local Claude
// Code CLI the way the composer's bench runs (runClaude): the brain's
// paragraph on surfaces, surface_write's description and the catalog
// appended to the system prompt, the case's request and tools as the turn,
// no tool, low effort, no session kept. It needs no node, only `claude`
// signed in on this machine, and costs a few cents a run.
//
//	ERIPHE_LIVE_SURFACES=1 go test -run TestSurfacesLive -v .
//
// ERIPHE_LIVE_SURFACES_MODEL picks the model (sonnet by default),
// ERIPHE_LIVE_SURFACES_CLI the command (claude by default),
// ERIPHE_LIVE_SURFACES_CASES the cases (ids, comma-separated; all by
// default). Each answer is scored as the bench scores its reference
// (scoreSurface: written by production on the case's fake servers). It
// reports, per case and in all: validity, catalog adherence, live data
// sourced or copied, composed data, tool names shown, latency and cost.
// It fails only when the CLI does; the numbers are for reading.

type surfaceLiveRun struct {
	latency time.Duration
	cost    float64
	score   surfaceScore
	agrees  bool
}

func TestSurfacesLive(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_SURFACES") == "" {
		t.Skip("set ERIPHE_LIVE_SURFACES=1 to run the surfaces' cases on a real model")
	}
	runs := 1
	if v := os.Getenv("ERIPHE_LIVE_SURFACES_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("ERIPHE_LIVE_SURFACES_RUNS=%q", v)
		}
		runs = n
	}
	model := cmpOr(os.Getenv("ERIPHE_LIVE_SURFACES_MODEL"), "sonnet")
	cli := cmpOr(os.Getenv("ERIPHE_LIVE_SURFACES_CLI"), "claude")
	if _, err := exec.LookPath(cli); err != nil {
		t.Fatalf("%s: %v", cli, err)
	}
	var only []string
	if v := os.Getenv("ERIPHE_LIVE_SURFACES_CASES"); v != "" {
		for _, id := range strings.Split(v, ",") {
			only = append(only, strings.TrimSpace(id))
		}
	}
	system := surfaceInstructions(t, "Sam")
	dir := t.TempDir() // no CLAUDE.md of this repository in its context

	var all []surfaceLiveRun
	for _, x := range loadSurfaceCases(t) {
		if only != nil && !slices.Contains(only, x.ID) {
			continue
		}
		c := benchSurfaceCore(t, x)
		turn := surfaceTurn(x)
		var got []surfaceLiveRun
		for i := range runs {
			start := time.Now()
			res, err := runClaude(cli, dir, model, system, turn)
			if err != nil {
				t.Fatalf("%s run %d: %v", x.ID, i+1, err)
			}
			r := surfaceLiveRun{latency: time.Since(start), cost: res.Cost, score: scoreSurface(c, x, res.Result)}
			r.agrees = r.score.agrees(x)
			t.Logf("%s run %d: %s, $%.4f: %s", x.ID, i+1, r.latency.Round(100*time.Millisecond), r.cost, r.score)
			if !r.agrees {
				t.Logf("%s run %d answered: %.600s", x.ID, i+1, res.Result)
			}
			got = append(got, r)
		}
		t.Logf("%s (%s): %s", x.ID, x.Kind, summarizeSurfaceRuns(got))
		all = append(all, got...)
	}
	t.Logf("all (%s, %d runs a case): %s", model, runs, summarizeSurfaceRuns(all))
}

func summarizeSurfaceRuns(runs []surfaceLiveRun) string {
	var total, longest time.Duration
	var cost float64
	var valid, onCatalog, sourced, copied, composed, named, agree, live int
	for _, r := range runs {
		total += r.latency
		longest = max(longest, r.latency)
		cost += r.cost
		s := r.score
		if s.Invalid == "" {
			valid++
		}
		if len(s.OffCatalog) == 0 {
			onCatalog++
		}
		if s.Sourced {
			sourced++
		}
		if len(s.Copied) > 0 {
			copied++
		}
		if s.Composed {
			composed++
		}
		if len(s.ToolNames) > 0 {
			named++
		}
		if r.agrees {
			agree++
		}
		live++
	}
	n := max(len(runs), 1)
	return fmt.Sprintf("valid %d/%d, on the catalog %d/%d, sourced %d, copied %d, composed %d, tool names %d, agreement %d/%d, latency mean %s max %s, cost $%.4f (mean $%.4f)",
		valid, live, onCatalog, live, sourced, copied, composed, named, agree, live,
		(total / time.Duration(n)).Round(100*time.Millisecond), longest.Round(100*time.Millisecond), cost, cost/float64(n))
}

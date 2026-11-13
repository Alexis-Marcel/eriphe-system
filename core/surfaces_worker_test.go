package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Surfaces refresh on their own worker: a slow source never delays a
// monitor's look, no more than surfaceReaders refreshes read at once, a
// tick never waits for a read nor reads a surface twice, and every due
// surface is read once the source answers.
func TestSurfaceRefreshOffMonitorsTick(t *testing.T) {
	c, l := surfaceCore(t)
	latest := map[string]surfaceSource{"/feed": {Resource: "ledger", URI: "ledger://latest"}}
	comps := json.RawMessage(`[{"id":"root","component":"Text","text":{"@path":"/feed/entry"}}]`)
	n := surfaceReaders + 2
	var ids []string
	for i := range n {
		s, errs, err := c.surfaceWrite("brain", surfaceIn{Title: fmt.Sprintf("feed %d", i), Components: comps, Sources: latest})
		if err != nil || len(errs) > 0 {
			t.Fatal(err, errs)
		}
		ids = append(ids, s.ID)
	}

	// The source turns slow: every read waits until the test lets it go.
	gate := make(chan struct{})
	l.set(func(l *fakeLedger) { l.gate, l.latest = gate, "second" })
	c.store.db.Exec(`UPDATE surfaces SET next_at = 0`)
	inflight := func() (now, most int) {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.inflight, l.most
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A tick hands the due surfaces to the worker and never waits.
	start := time.Now()
	c.tickSurfaces(ctx, time.Now())
	if d := time.Since(start); d > time.Second {
		t.Fatalf("a tick waited %s", d)
	}
	waitFor(t, func() bool { now, _ := inflight(); return now == surfaceReaders })

	// The loops run; the next ticks read no queued surface again, and a
	// monitor made due now looks while every reader waits on the source.
	calls := l.count()
	go c.runSurfaces(ctx)
	approvedMonitor(t, c, "ledger-total", monitorSpec{Way: wayPoll, Server: "ledger", Tool: "balances", Every: 1, Cond: condChanged, Path: "total"})
	go c.runMonitors(ctx)
	waitFor(t, func() bool { m, err := c.monitorNamed("ledger-total"); return err == nil && m.Look.Checked > 0 })
	c.tickSurfaces(ctx, time.Now())
	if now, _ := inflight(); now != surfaceReaders {
		t.Fatalf("%d reads in flight, the source was let go", now)
	}
	if got := l.count(); got != calls+1 { // the monitor's look only
		t.Fatalf("read again: %d → %d", calls, got)
	}

	l.set(func(l *fakeLedger) { l.gate = nil })
	close(gate)
	for _, id := range ids {
		waitFor(t, func() bool {
			s, err := c.surface(id)
			var data map[string]any
			json.Unmarshal(s.Data, &data)
			v, _ := getPointer(data, "/feed/entry")
			return err == nil && v == "second" && s.NextAt.After(time.Now())
		})
	}
	if _, most := inflight(); most > surfaceReaders {
		t.Fatalf("%d reads at once, more than %d", most, surfaceReaders)
	}
}

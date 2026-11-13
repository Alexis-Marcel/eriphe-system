package main

import (
	"strings"
	"time"
)

// What the composer's choices (workspace_pool.go, workspace_layout.go)
// share: who made one, the moments one is made for, and the helpers of its
// turn.

const (
	compositionSayMax = 140 // Eriphe's line, in runes

	composedBy = "composer"
	fallbackBy = "fallback"
)

// The moments a choice is made for.
const (
	momentChange  = "change"  // the queue or the pool changed while the user is here
	momentArrival = "arrival" // the user is away: what to show on their return
	momentRemark  = "remark"  // a preference about the workspace entered memory (workspace.go)
	momentStart   = "start"   // eriphe-core started
)

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

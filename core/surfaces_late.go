package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// A surface's first read, held for a while at most (surfaceIn.wait): its
// author's turn is never held by a slow source. The sources still reading
// then are accepted waiting (surfaceReading), go on in the background, and
// what they give fills the surface as a refresh would; the bindings under
// them are checked then, and what is wrong is stored on the surface for its
// author (surfaceError, surfaces_stream.go).

// surfaceReading is why a source waits while its first read goes on.
const surfaceReading = "still reading: its value comes when the read ends"

// lateReads are the sources still reading when a write went on: done gives
// every source's read once the last is over.
type lateReads struct {
	pending []string
	done    <-chan map[string]sourceRead
}

// readSourcesWithin reads the sources at once, each under the guards, and
// waits wait at most (0: until every one is read). The reads not over then
// are marked waiting, and late gives them when they are.
func (c *core) readSourcesWithin(srcs map[string]surfaceSource, wait time.Duration) (map[string]sourceRead, *lateReads) {
	if wait <= 0 || len(srcs) == 0 {
		return c.readSources(context.Background(), srcs, nil), nil
	}
	type result struct {
		ptr string
		r   sourceRead
	}
	results := make(chan result, len(srcs))
	for ptr, s := range srcs {
		go func() { results <- result{ptr, c.readOne(context.Background(), s)} }()
	}
	got := map[string]sourceRead{}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for len(got) < len(srcs) {
		select {
		case x := <-results:
			got[x.ptr] = x.r
		case <-timer.C:
			first := maps.Clone(got)
			var pending []string
			for ptr := range srcs {
				if _, ok := got[ptr]; !ok {
					pending = append(pending, ptr)
					first[ptr] = sourceRead{why: surfaceReading}
				}
			}
			done := make(chan map[string]sourceRead, 1)
			go func() {
				for len(got) < len(srcs) {
					x := <-results
					got[x.ptr] = x.r
				}
				done <- got
			}()
			return first, &lateReads{pending: pending, done: done}
		}
	}
	return got, nil
}

// surfaceLateReads fills a surface written with sources still reading, once
// they are read, and checks its bindings against what they gave. A surface
// written again meanwhile was checked by that write: nothing is told.
func (c *core) surfaceLateReads(written surface, late *lateReads) {
	all := <-late.done
	reads := map[string]sourceRead{}
	for _, ptr := range late.pending {
		reads[ptr] = all[ptr]
	}
	c.applyReads(written.ID, written.Sources, reads, late.pending, time.Now())
	s, err := c.surface(written.ID)
	if err != nil || valueText(s.Sources) != valueText(written.Sources) || string(s.Components) != string(written.Components) {
		return
	}
	var data map[string]any
	if json.Unmarshal(s.Data, &data) != nil || data == nil {
		data = map[string]any{}
	}
	// Stored as the page's render errors are (surfaces_stream.go), the lock
	// released: the first at its path, all of them in its message.
	if errs := c.checkBindings(s.ID, s.Components, data, s.Sources, all); len(errs) > 0 {
		c.surfaceError(s.ID, a2ui.Error{Code: errs[0].Code, SurfaceID: s.ID, Path: errs[0].Path, Message: bindingsText(errs)})
	}
}

func bindingsText(errs []a2ui.ValidationError) string {
	var parts []string
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%s: %s", e.Path, e.Message))
	}
	return strings.Join(parts, "; ")
}

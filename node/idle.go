package main

import (
	"bytes"
	"regexp"
	"strconv"
)

// The machine's idle time: seconds since the last key or mouse event, and
// whether the screen is locked, told to eriphe-core with the node's status
// (GET /v1/node "idle"), so that it notifies the computer the user is at,
// else their phone (core/route.go). A number and a flag: nothing of what
// is on the screen. Read natively on a Mac (IOKit's HIDIdleTime through
// ioreg, a few milliseconds, no permission); unknown elsewhere for now
// (Windows runs the node in WSL, outside the user's session; Linux and
// containers have no one answer).

type idleState struct {
	Known   bool  `json:"known"`
	Seconds int64 `json:"seconds,omitempty"`
	Locked  bool  `json:"locked,omitempty"`
}

var hidIdleRe = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)

// parseHIDIdle reads the idle time from ioreg's output, in nanoseconds.
func parseHIDIdle(out []byte) (int64, bool) {
	m := hidIdleRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	ns, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil || ns < 0 {
		return 0, false
	}
	return ns / 1e9, true
}

// parseLocked reads whether the session's screen is locked from ioreg's
// root entry: the key is there, Yes, only while it is.
func parseLocked(out []byte) bool {
	return bytes.Contains(out, []byte(`"CGSSessionScreenIsLocked"=Yes`))
}

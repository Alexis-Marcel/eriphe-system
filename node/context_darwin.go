package main

import (
	"context"
	"os/exec"
	"time"
)

// readSession on a Mac: the app in front and its declared category, from
// lsappinfo (LaunchServices, no permission, a few milliseconds), read as
// the user's LaunchAgent. Fullscreen and the microphone are not read: the
// first has no answer without the screen-recording or accessibility
// permission, the second needs CoreAudio, which this node, built without
// cgo, cannot call. They stay unknown.
func readSession(ctx context.Context) (frontApp, sessionState, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	asn, err := exec.CommandContext(ctx, "/usr/bin/lsappinfo", "front").Output()
	if err != nil {
		return frontApp{}, sessionState{}, false
	}
	front, ok := parseASN(asn)
	if !ok {
		return frontApp{}, sessionState{}, false
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/lsappinfo", "info", "-only", "bundleid", "-only", "LSApplicationCategoryType", front).Output()
	if err != nil {
		return frontApp{}, sessionState{}, false
	}
	return parseAppInfo(out), sessionState{}, true
}

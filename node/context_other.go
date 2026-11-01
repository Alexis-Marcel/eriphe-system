//go:build !darwin

package main

import "context"

// readSession elsewhere: the node cannot see the user's session (on
// Windows it runs in WSL, outside it). A session helper may tell it through
// its file (helperSession, ERIPHE_SESSION_FILE); without one, unknown.
func readSession(context.Context) (frontApp, sessionState, bool) {
	return frontApp{}, sessionState{}, false
}

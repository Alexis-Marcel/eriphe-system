//go:build !windows

package main

import "errors"

// read elsewhere: a Mac's device, as the user's LaunchAgent, reads the
// session itself; Linux has no one answer yet.
func read() (probe, error) {
	return probe{}, errors.New("runs on Windows only")
}

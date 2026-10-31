package main

import (
	"os"
	"os/exec"
	"strconv"
)

// systemHold on a Mac: caffeinate, as long as the node asks. -i keeps the
// Mac from idle sleep, -s from system sleep while on AC power (on battery,
// macOS may still sleep, and closing the lid always does); -w ends it with
// the node, whatever happens to it.
func systemHold() (holdFunc, string) {
	path, err := exec.LookPath("caffeinate")
	if err != nil {
		return nil, ""
	}
	return func() (func(), error) {
		cmd := exec.Command(path, "-i", "-s", "-w", strconv.Itoa(os.Getpid()))
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return func() {
			cmd.Process.Kill()
			cmd.Wait()
		}, nil
	}, "caffeinate"
}

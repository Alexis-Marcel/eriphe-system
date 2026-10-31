package main

import (
	"os"
	"os/exec"
)

// systemHold on Linux: systemd-inhibit, where the node runs on a system
// with systemd. In a container or in WSL's distribution there is none:
// the host's helper reads <root>/awake instead. The inhibitor holds while
// its cat reads the pipe: closing it, or the node ending, lets it go.
func systemHold() (holdFunc, string) {
	path, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return nil, ""
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return nil, ""
	}
	return func() (func(), error) {
		cmd := exec.Command(path, "--what=sleep:idle", "--who=eriphe-node", "--why=A task is at work", "--mode=block", "cat")
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return func() {
			in.Close()
			cmd.Wait()
		}, nil
	}, "systemd-inhibit"
}

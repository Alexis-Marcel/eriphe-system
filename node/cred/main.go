// eriphe-cred is what a task's ssh needs from Eriphe: the SSH agent, whose key
// stays in eriphe-core, and the tunnel through the node's relay.
//
//	eriphe-cred ssh-agent         the SSH agent of a task with an SSH access (ssh.go)
//	eriphe-cred tunnel HOST PORT  ssh's ProxyCommand, through the node's relay
//
// It reads ERIPHE_SSH_URL (the node's endpoint) and ERIPHE_TASK_SECRET, which
// the node gives a task with an SSH access.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

const envSecret = "ERIPHE_TASK_SECRET"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: eriphe-cred ssh-agent, eriphe-cred tunnel HOST PORT, eriphe-cred version")
		os.Exit(2)
	}
	switch args[0] {
	case "ssh-agent":
		os.Exit(runSSHAgent(os.Stderr))
	case "tunnel":
		os.Exit(runTunnel(args[1:], os.Stdin, os.Stdout, os.Stderr))
	case "version", "--version":
		fmt.Println("eriphe-cred", version)
	default:
		fmt.Fprintln(os.Stderr, "eriphe-cred: unknown command", args[0])
		os.Exit(2)
	}
}

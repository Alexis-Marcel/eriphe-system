//go:build !linux

package main

// handBackDir: only Docker on Linux (the NAS, WSL, a Linux machine) mounts
// folders a root task writes to as root.
func handBackDir(string) (int, error) { return 0, nil }

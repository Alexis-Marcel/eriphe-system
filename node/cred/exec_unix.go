//go:build unix

package main

import "syscall"

// execve replaces this process with the real tool: its exit status, its
// signals and its terminal are its own.
func execve(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }

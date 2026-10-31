//go:build !darwin && !linux

package main

// systemHold elsewhere: none. On Windows the node runs in WSL, as Linux,
// and the scheduled task "Eriphe awake" holds Windows (install.ps1).
func systemHold() (holdFunc, string) { return nil, "" }

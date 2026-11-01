//go:build !darwin

package main

import "context"

// machineIdle elsewhere: unknown. On Windows, a helper in the user's
// session will tell it (GetLastInputInfo); the node in WSL cannot.
func machineIdle(context.Context) idleState { return idleState{} }

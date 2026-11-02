//go:build !linux && !darwin

package main

import "errors"

// platformSnapshot copies the folder (snapshot.go).
func platformSnapshot(src, dst string, limit int64) (string, int64, error) {
	return copyTree(src, dst, limit)
}

// cloneFileOS: no clone here, a copy.
func cloneFileOS(src, dst string) error { return errors.New("no clone on this system") }

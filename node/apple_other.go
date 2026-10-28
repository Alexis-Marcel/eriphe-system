//go:build !darwin

package main

// Only a Mac keeps files in the cloud without their content (apple_darwin.go).

func cloudOnly(string) (int, int64, error) { return 0, 0, nil }

func fetchCloud(string) error { return nil }

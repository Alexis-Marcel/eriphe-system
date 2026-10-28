package main

import (
	"path/filepath"
	"testing"
	"time"
)

// A certificate made under another machine name (a recreated container)
// still covers the configured names, so it is kept.
func TestCertKeptWhenMachineNameChanges(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	names := append(requiredNames([]string{"192.168.0.2"}, "http://192.168.0.2:8800"), "31b61249d5ae", "192.168.192.2")
	cert, err := selfSigned(filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem"), names, now)
	if err != nil {
		t.Fatal(err)
	}
	if !certCovers(cert, requiredNames([]string{"192.168.0.2"}, "http://192.168.0.2:8800"), now) {
		t.Fatal("a certificate covering the configured names was not kept")
	}
	if certCovers(cert, requiredNames([]string{"eriphe.home"}, ""), now) {
		t.Fatal("a certificate missing a configured name was kept")
	}
}

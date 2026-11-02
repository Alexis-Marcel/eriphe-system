package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Real downloads from go.dev and nodejs.org, checked against their
// checksums, for this machine's architecture:
//
//	ERIPHE_LIVE_TOOLCHAIN=1 go test -run TestLiveToolchain -v .
func TestLiveToolchain(t *testing.T) {
	if os.Getenv("ERIPHE_LIVE_TOOLCHAIN") == "" {
		t.Skip("set ERIPHE_LIVE_TOOLCHAIN=1 to download real toolchains")
	}
	s := newToolchainStore(t.TempDir())
	if !s.supported() {
		t.Skip("no toolchains for", runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	for _, w := range []wanted{
		{kind: goToolchain{}, want: strings.TrimPrefix(runtime.Version(), "go")},
		{kind: nodeToolchain{}, want: "lts/*"},
	} {
		name, err := s.ensure(ctx, w)
		if err != nil {
			t.Fatalf("%s %s: %v", w.kind.kind(), w.want, err)
		}
		bin := filepath.Join(s.dir, name, "bin", w.kind.kind())
		if _, err := os.Stat(bin); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "linux" {
			arg := "version"
			if w.kind.kind() == "node" {
				arg = "--version"
			}
			out, err := exec.Command(bin, arg).CombinedOutput()
			t.Logf("%s: %s %v", name, strings.TrimSpace(string(out)), err)
		} else {
			t.Logf("%s ready in %s", name, filepath.Dir(bin))
		}
	}
}

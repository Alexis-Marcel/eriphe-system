package main

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes an engine CLI running body, which records its calls one
// per line in <dir>/calls.
func fakeCLI(t *testing.T, body string) (cli, dir string) {
	t.Helper()
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	dir = t.TempDir()
	cli = filepath.Join(dir, "cli")
	script := "#!/bin/sh\nd=" + dir + "\necho \"$*\" >> \"$d/calls\"\n" + body + "\n"
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli, dir
}

// A container's address comes once it runs on a network; the wait ends
// when it exits first, or when the context does.
func TestAppleAddress(t *testing.T) {
	// The first listing has it starting, the next ones running.
	cli, _ := fakeCLI(t, `if [ -f "$d/seen" ]; then
  printf '[{"configuration":{"id":"other"},"status":{"state":"running","networks":[{"ipv4Address":"192.168.64.9/24"}]}},{"id":"eriphe-svc-mail","status":{"state":"running","networks":[{"ipv4Address":"192.168.64.5/24"}]}}]'
else
  touch "$d/seen"; printf '[{"id":"eriphe-svc-mail","status":{"state":"stopped","networks":[]}}]'
fi`)
	n := newNode(options{docker: cli, engine: engineContainer})
	ip, err := n.appleAddress(context.Background(), "eriphe-svc-mail", make(chan error, 1))
	if err != nil || ip != "192.168.64.5" {
		t.Errorf("got %q, %v", ip, err)
	}

	never, _ := fakeCLI(t, `printf '[]'`)
	n = newNode(options{docker: never, engine: engineContainer})
	exited := make(chan error, 1)
	exited <- nil
	if _, err := n.appleAddress(context.Background(), "eriphe-svc-mail", exited); err == nil || len(exited) != 1 {
		t.Errorf("exited: %v, the exit kept: %v", err, len(exited) == 1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := n.appleAddress(ctx, "eriphe-svc-mail", make(chan error, 1)); err != context.DeadlineExceeded {
		t.Errorf("timed out: %v", err)
	}
}

// The container tool's version counts only when its services run.
func TestAppleVersion(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`printf '[{"appName":"container","version":"0.6.0"},{"appName":"container-apiserver","version":"0.6.0"}]'`, "0.6.0"},
		{`printf '[{"appName":"container","version":"0.6.0"}]'`, ""},
		{`exit 1`, ""},
	} {
		cli, dir := fakeCLI(t, tc.body)
		if got := appleVersion(context.Background(), cli); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.body, got, tc.want)
		}
		if calls := fileLines(filepath.Join(dir, "calls")); calls[0] != "system version --format json" {
			t.Errorf("calls %v", calls)
		}
	}
}

// The images the container tool has, as Docker names them, at most
// maxImages.
func TestAppleImages(t *testing.T) {
	cli, _ := fakeCLI(t, `printf '[{"configuration":{"name":"docker.io/library/alpine:3"}},{"configuration":{"name":"ghcr.io/x/y:1"}},{"configuration":{}}]'`)
	if got := appleImages(context.Background(), cli); !slices.Equal(got, []string{"alpine:3", "ghcr.io/x/y:1"}) {
		t.Errorf("got %v", got)
	}
	failing, _ := fakeCLI(t, "exit 1")
	if got := appleImages(context.Background(), failing); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want an empty list", got)
	}
	var many strings.Builder
	many.WriteString("printf '[")
	for i := range maxImages + 5 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"configuration":{"name":"img` + strings.Repeat("x", i) + `:1"}}`)
	}
	many.WriteString("]'")
	lots, _ := fakeCLI(t, many.String())
	if got := appleImages(context.Background(), lots); len(got) != maxImages {
		t.Errorf("%d images, want %d", len(got), maxImages)
	}
}

// The node's own image is the one Docker lists for its container.
func TestSelfImage(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`echo ghcr.io/x/eriphe-node:1.2.3; echo other`, "ghcr.io/x/eriphe-node:1.2.3"},
		{`true`, ""},
		{`echo ghcr.io/x/eriphe-node:1.2.3; exit 1`, ""},
	} {
		cli, dir := fakeCLI(t, tc.body)
		n := newNode(options{docker: cli})
		got, err := n.selfImage(context.Background())
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("%s: %q, %v", tc.body, got, err)
		}
		call := fileLines(filepath.Join(dir, "calls"))[0]
		if !strings.HasPrefix(call, "ps --no-trunc --filter id=") || strings.HasPrefix(call, "ps --no-trunc --filter id= ") {
			t.Errorf("call %q", call)
		}
	}
}

// prepareNow pulls the default image and eriphe-core's, each once and none
// that looks like a flag, and unpacks the configured agents only.
func TestPrepareNow(t *testing.T) {
	reg := newFakeRegistry(t, layer(t,
		tarEntry{name: "opt/eriphe/bin/claude", typ: tar.TypeReg, mode: 0o755, body: "#!/bin/sh\n"}))
	for _, engine := range []string{engineDocker, engineContainer} {
		cli, dir := fakeCLI(t, `[ "$*" = "pull -q broken" ] && exit 1; true`)
		root := t.TempDir()
		n := newNode(options{root: root, docker: cli, engine: engine, image: "worker", agentImage: reg.image()})
		n.prepareNow(&prepareSpec{Runtimes: []string{"claude-code", "unknown"}, Images: []string{"worker", "", "-x", "broken", "agent"}})
		pull := "pull -q "
		if engine == engineContainer {
			pull = "image pull --progress none "
		}
		want := []string{pull + "agent", pull + "broken", pull + "worker"}
		if got := fileLines(filepath.Join(dir, "calls")); !slices.Equal(got, want) {
			t.Errorf("%s: calls %v, want %v", engine, got, want)
		}
		if got := n.agents.readyRuntimes(); !slices.Equal(got, []string{"acp", "claude-code"}) {
			t.Errorf("%s: ready %v", engine, got)
		}
	}

	// From an older eriphe-core: the default image only.
	cli, dir := fakeCLI(t, "true")
	n := newNode(options{root: t.TempDir(), docker: cli, image: "worker"})
	n.prepareNow(nil)
	if got := fileLines(filepath.Join(dir, "calls")); !slices.Equal(got, []string{"pull -q worker"}) {
		t.Errorf("calls %v", got)
	}
}

// The preparer waits for eriphe-core's first list, then prepares the latest.
func TestPrepareWanted(t *testing.T) {
	cli, dir := fakeCLI(t, "true")
	n := newNode(options{root: t.TempDir(), docker: cli, image: "worker"})
	n.prepareWanted()
	if _, err := os.Stat(filepath.Join(dir, "calls")); err == nil {
		t.Error("prepared before the first assignments")
	}
	n.wanted(&prepareSpec{Images: []string{"agent"}})
	n.prepareWanted()
	if got := fileLines(filepath.Join(dir, "calls")); !slices.Equal(got, []string{"pull -q agent", "pull -q worker"}) {
		t.Errorf("calls %v", got)
	}
}

// The images Docker has, without the untagged ones.
func TestLocalImages(t *testing.T) {
	cli, dir := fakeCLI(t, `printf 'alpine:3\n<none>:<none>\n\nghcr.io/x/y:1\n'`)
	if got := localImages(context.Background(), cli); !slices.Equal(got, []string{"alpine:3", "ghcr.io/x/y:1"}) {
		t.Errorf("got %v", got)
	}
	if calls := fileLines(filepath.Join(dir, "calls")); calls[0] != "images --format {{.Repository}}:{{.Tag}}" {
		t.Errorf("calls %v", calls)
	}
	failing, _ := fakeCLI(t, "exit 1")
	if got := localImages(context.Background(), failing); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want an empty list", got)
	}
}

func TestVolumeFlag(t *testing.T) {
	v := volumeFlag{}
	if err := v.Set(" media=/mnt/media, ,docs=/srv/docs"); err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 || v["media"] != "/mnt/media" || v["docs"] != "/srv/docs" {
		t.Errorf("got %v", v)
	}
	for _, bad := range []string{"media", "Media=/x", "media=", "a/b=/x"} {
		if err := (volumeFlag{}).Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTrimNL(t *testing.T) {
	for in, want := range map[string]string{"27.1\r\n": "27.1", "27.1": "27.1", "\n\n": "", "a\nb\n": "a\nb"} {
		if got := string(trimNL([]byte(in))); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

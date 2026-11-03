package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a buffer written from several goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestResolveIn(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "mnt/user/Docs/a"), 0o755)
	os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	os.Symlink("/mnt/user/Docs", filepath.Join(root, "mnt/user/Link"))
	os.Symlink("../../etc", filepath.Join(root, "mnt/user/Up"))
	os.Symlink("../../../../../../..", filepath.Join(root, "mnt/user/Docs/a/out"))
	os.Symlink("loop", filepath.Join(root, "loop"))
	for in, want := range map[string]string{
		"/mnt/user/Docs/a":            "/mnt/user/Docs/a",
		"/mnt/user/Link/a":            "/mnt/user/Docs/a",
		"/mnt/user/Up":                "/etc",
		"/mnt/user/Docs/a/out":        "/",
		"/mnt/user/../user/Docs/./a/": "/mnt/user/Docs/a",
	} {
		if got, err := resolveIn(root, in); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/loop", "/missing", "relative"} {
		if got, err := resolveIn(root, in); err == nil {
			t.Errorf("%s: %q, no error", in, got)
		}
	}
}

func TestPolicyBindFile(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, folder := filepath.Join(base, "node"), filepath.Join(base, "outside")
	os.MkdirAll(filepath.Join(root, "tasks"), 0o755)
	os.MkdirAll(filepath.Join(folder, "sub"), 0o755)
	p := &dockerPolicy{bindFrom: []string{root}, bindFile: filepath.Join(base, "binds"), resolve: filepath.EvalSymlinks}
	check := func(src string) (bool, error) {
		_, ro, err := p.bindSource(src)
		return ro, err
	}
	if _, err := check(filepath.Join(root, "tasks")); err != nil {
		t.Fatalf("under -bind-from: %v", err)
	}
	if _, err := check(folder); err == nil {
		t.Fatal("allowed with no bind file")
	}
	if _, err := check("relative"); err == nil {
		t.Error("a relative source allowed")
	}
	// Shared now: allowed at the next create; "/" and relative lines are no place.
	os.WriteFile(p.bindFile, []byte("/\n\nrelative\n"+folder+"\n"), 0o644)
	if ro, err := check(filepath.Join(folder, "sub")); err != nil || ro {
		t.Fatalf("listed: ro %v, %v", ro, err)
	}
	os.WriteFile(p.bindFile, []byte("/\n"), 0o644)
	if _, err := check(folder); err == nil {
		t.Error("/ listed allows everything")
	}
	// Listed read-only: to be mounted read-only.
	os.WriteFile(p.bindFile, []byte(folder+":ro\n"), 0o644)
	if ro, err := check(folder); err != nil || !ro {
		t.Errorf("listed read-only: ro %v, %v", ro, err)
	}
	// A link out of a shared folder is checked where it leads.
	os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(folder, "out"))
	os.MkdirAll(filepath.Join(base, "elsewhere"), 0o755)
	if _, err := check(filepath.Join(folder, "out")); err == nil {
		t.Error("a link out of the shared folder allowed")
	}
	// Unshared: refused at once.
	os.WriteFile(p.bindFile, nil, 0o644)
	if _, err := check(folder); err == nil {
		t.Error("allowed once unlisted")
	}
}

// A node whose engine cannot be reached waits for it, and knows when it is
// back.
func TestEngineBack(t *testing.T) {
	up := false
	var mu sync.Mutex
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !up || r.URL.Path != engineAPI+"/config" {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer engine.Close()
	n := newNode(options{engineURL: engine.URL})
	if n.engineBack() {
		t.Fatal("back while the engine refuses")
	}
	done := make(chan struct{})
	go func() {
		untilTrue(n.engineBack, 10*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("stopped waiting before the engine was back")
	case <-time.After(50 * time.Millisecond):
	}
	mu.Lock()
	up = true
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting with the engine back")
	}
}

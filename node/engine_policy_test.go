package main

import (
	"bytes"
	"errors"
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

// No container mounts a path strictly under a place a task can write; a
// task gets no place to write over what a container not started yet
// mounts; what is not under it, or read only, or a helper's, passes.
func TestMountClash(t *testing.T) {
	listed := func(label, id, state string, src string, rw bool) apiListed {
		l := apiListed{ID: id, State: state, Labels: map[string]string{label: id}}
		l.Mounts = append(l.Mounts, struct {
			Type   string `json:"Type"`
			Source string `json:"Source"`
			RW     bool   `json:"RW"`
		}{"bind", src, rw})
		return l
	}
	bind := func(src string, ro bool) apiMount {
		return apiMount{Type: "bind", Source: src, Target: "/w", ReadOnly: ro}
	}
	manager := listed("eriphe.task", "t-1", "running", "/srv/docs", true)
	for _, c := range []struct {
		name   string
		binds  []apiMount
		task   bool
		others []apiListed
		clash  bool
	}{
		{"under a task's writable place", []apiMount{bind("/srv/docs/results", false)}, true, []apiListed{manager}, true},
		{"read-only under it", []apiMount{bind("/srv/docs/sub", true)}, true, []apiListed{manager}, true},
		{"a helper under it", []apiMount{bind("/srv/docs/sub", false)}, false, []apiListed{manager}, true},
		{"the place itself", []apiMount{bind("/srv/docs", true)}, true, []apiListed{manager}, false},
		{"beside it", []apiMount{bind("/srv/docsx/results", false)}, true, []apiListed{manager}, false},
		{"under a read-only one", []apiMount{bind("/srv/docs/results", false)}, true,
			[]apiListed{listed("eriphe.task", "t-1", "running", "/srv/docs", false)}, false},
		{"under a helper's writable place", []apiMount{bind("/srv/docs/results", false)}, true,
			[]apiListed{listed("eriphe.helper", "h-1", "running", "/srv/docs", true)}, false},
		{"a writer over one not started", []apiMount{bind("/srv/docs", false)}, true,
			[]apiListed{listed("eriphe.task", "t-2", "created", "/srv/docs/results", true)}, true},
		{"a writer over one started", []apiMount{bind("/srv/docs", false)}, true,
			[]apiListed{listed("eriphe.task", "t-2", "running", "/srv/docs/results", true)}, false},
		{"a helper over one not started", []apiMount{bind("/srv/docs", false)}, false,
			[]apiListed{listed("eriphe.task", "t-2", "created", "/srv/docs/results", true)}, false},
	} {
		err := mountClash(c.binds, c.task, c.others)
		if (err != nil) != c.clash {
			t.Errorf("%s: %v", c.name, err)
		}
		if err != nil && !errors.As(err, new(errMountClash)) {
			t.Errorf("%s: %T", c.name, err)
		}
	}
}

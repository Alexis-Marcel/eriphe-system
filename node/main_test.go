package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseLabels(t *testing.T) {
	if got := parseLabels(" gpu, ,big-disk,"); !slices.Equal(got, []string{"gpu", "big-disk"}) {
		t.Errorf("got %q", got)
	}
	if got := parseLabels(""); got != nil {
		t.Errorf("got %q, want none", got)
	}
}

// The options are checked and completed once the flags are read.
func TestCompleteOptions(t *testing.T) {
	o := options{root: "data", engine: engineDocker}
	if err := o.complete(" http://127.0.0.1:11434/v1 ", "2g", "linux"); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if o.docker != "docker" || o.downloadLimit != 2<<30 || o.modelServer != "http://127.0.0.1:11434" ||
		o.root != filepath.Join(cwd, "data") || o.hostRoot != o.root {
		t.Errorf("got %+v", o)
	}
	o = options{root: "/srv/node", hostRoot: "/mnt/node", engine: engineContainer, docker: "/opt/container"}
	if err := o.complete("", "0", "darwin"); err != nil || o.docker != "/opt/container" || o.hostRoot != "/mnt/node" {
		t.Errorf("got %+v, %v", o, err)
	}
	for _, tc := range []struct {
		engine, model, limit, goos, want string
	}{
		{"podman", "", "2g", "linux", `-engine "podman": docker or container`},
		{engineContainer, "", "2g", "linux", "-engine container is Apple's container, on a Mac: use docker here"},
		{engineDocker, "", "lots", "linux", "-download-limit: "},
		{engineDocker, "ftp://192.0.2.10", "2g", "linux", "ftp"},
	} {
		o := options{root: "data", engine: tc.engine}
		if err := o.complete(tc.model, tc.limit, tc.goos); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc, err, tc.want)
		}
	}
}

func TestMakeRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := makeRoot(root); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"profiles", "tasks", "services", "agents"} {
		if fi, err := os.Stat(filepath.Join(root, d)); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v", d, err)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	if err := makeRoot(file); err == nil {
		t.Error("made under a file")
	}
}

// A node joins once, then starts from what it saved; addresses given again
// replace the saved ones.
func TestLoadOrJoin(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"secret":"the-secret"}`)
	}))
	defer core.Close()
	path := filepath.Join(t.TempDir(), "node.json")
	if _, err := loadOrJoin(path, "", "tok", "pc", ""); err == nil || !strings.Contains(err.Error(), "has not joined yet") {
		t.Errorf("no -core: %v", err)
	}
	if _, err := loadOrJoin(path, core.URL, "", "pc", ""); err == nil {
		t.Error("joined without a token")
	}
	s, err := loadOrJoin(path, core.URL, "tok", "pc", "")
	if err != nil || s.Secret != "the-secret" || s.Name != "pc" {
		t.Fatalf("joined %+v, %v", s, err)
	}

	s, err = loadOrJoin(path, "", "", "other", "")
	if err != nil || s.Secret != "the-secret" || s.Name != "pc" || s.path != path {
		t.Fatalf("loaded %+v, %v", s, err)
	}
	s, err = loadOrJoin(path, core.URL+",https://eriphe.example.org:8800", "", "", "")
	if err != nil || len(s.urls()) != 2 {
		t.Fatalf("new addresses: %v, %v", s.urls(), err)
	}
	if saved, _ := loadState(path); !slices.Equal(saved.urls(), s.urls()) {
		t.Errorf("saved %v", saved.urls())
	}

	os.WriteFile(path, []byte("{"), 0o600)
	if _, err := loadOrJoin(path, "", "", "", ""); err == nil {
		t.Error("a broken node.json loaded")
	}
	os.WriteFile(path, []byte(`{"core":"http://192.0.2.1:8800","ca":"not a certificate"}`), 0o600)
	if _, err := loadOrJoin(path, "", "", "", ""); err == nil {
		t.Error("a broken certificate authority loaded")
	}
}

// The egress relay listens where -egress-listen says, on the task
// listener's host when it names none.
func TestEgressRelay(t *testing.T) {
	for _, engine := range []string{engineDocker, engineContainer} {
		n := newNode(options{engine: engine, callbackURL: "http://host.docker.internal:8790"})
		relay, l, addr, err := n.egressRelay(":0", "127.0.0.1:8790")
		if err != nil {
			t.Fatal(err)
		}
		l.Close()
		if relay == nil || addr != "127.0.0.1:0" || n.opt.egressPort != "0" {
			t.Errorf("%s: relay on %s, port %q", engine, addr, n.opt.egressPort)
		}
	}
	n := newNode(options{})
	if _, _, _, err := n.egressRelay("no-port", "127.0.0.1:8790"); err == nil {
		t.Error("a bad -egress-listen accepted")
	}
	if _, _, _, err := n.egressRelay("192.0.2.1:0", "127.0.0.1:8790"); err == nil {
		t.Error("listening on an address of another machine")
	}
}

// The task listener serves the tasks' endpoints only.
func TestTaskRoutes(t *testing.T) {
	mux := newNode(options{}).taskRoutes(&state{Core: "http://192.0.2.1:8800"})
	for _, tc := range []struct{ method, path, pattern string }{
		{"POST", "/perm/t-1/mcp", "/perm/{id}/mcp"},
		{"POST", "/mcp/mail", "/mcp/{server}"},
		{"GET", "/model/v1/models", "/model/v1/{path...}"},
		{"POST", credentialPath, "POST " + credentialPath},
		{"GET", "/v1/node", ""},
	} {
		if _, p := mux.Handler(httptest.NewRequest(tc.method, tc.path, nil)); p != tc.pattern {
			t.Errorf("%s %s: %q, want %q", tc.method, tc.path, p, tc.pattern)
		}
	}
}

func TestStartMessage(t *testing.T) {
	n := newNode(options{image: "worker", taskNetwork: "eriphe-tasks"})
	if got := n.startMessage("pc", "172.17.0.2:8790", ""); !strings.Contains(got, "tasks on eriphe-tasks, services on bridge") ||
		!strings.HasSuffix(got, "task listener on 172.17.0.2:8790, egress relay on none") {
		t.Errorf("got %q", got)
	}
	n = newNode(options{image: "worker", engine: engineContainer})
	if got := n.startMessage("mac", "0.0.0.0:8790", "0.0.0.0:8791"); !strings.Contains(got, "Apple's container") ||
		!strings.Contains(got, "egress relay on 0.0.0.0:8791") {
		t.Errorf("got %q", got)
	}
}

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCredentialLive runs a real task container as the NAS does (the
// layout of TestInjectLive: the node, this test, in a container on an
// internal task network; tasks reach only it, its listener and its egress
// relay), with eriphe-cred lent through -task-bin and a fake eriphe-core:
//
//   - the entrypoint clones a public repository through git configured for
//     eriphe-cred (git never asks for a credential: none is needed);
//
//   - git ls-remote of a private-looking repository asks for read, and the
//     fake eriphe-core lends a (fake) token, which git sends to GitHub;
//
//   - git push asks for write: eriphe-core says the user must approve, the
//     node asks the task's permission, the test approves it, the token comes;
//
//   - gh, the wrapper, says how to install the real one, then after apt
//     installs it asks for its own token for the repository.
//
//     ERIPHE_LIVE_DOCKER=1 ERIPHE_LIVE_IMAGE=eriphe-base:dev \
//     ERIPHE_LIVE_SHARE=/tmp/share ERIPHE_LIVE_SELF=<this container's name> \
//     ERIPHE_LIVE_TASK_BIN=<dir with linux-<arch>/eriphe-cred and gh> go test -run CredentialLive -v
//
// With ERIPHE_LIVE_CONTAINER=1 instead of the Docker variables, the same on
// this Mac with Apple's container (TestAppleInjectLive's layout).
func TestCredentialLive(t *testing.T) {
	apple := os.Getenv("ERIPHE_LIVE_CONTAINER") != ""
	if (os.Getenv("ERIPHE_LIVE_DOCKER") == "" && !apple) || os.Getenv("ERIPHE_LIVE_TASK_BIN") == "" {
		t.Skip("ERIPHE_LIVE_DOCKER (or ERIPHE_LIVE_CONTAINER) and ERIPHE_LIVE_TASK_BIN not set")
	}
	var n *node
	var root string
	if apple {
		root, _ = filepath.EvalSymlinks(t.TempDir())
		n = newNode(options{root: root, hostRoot: root, docker: "container", engine: engineContainer,
			image: os.Getenv("ERIPHE_LIVE_IMAGE"), taskMemory: "2g", taskCPUs: "2", cacheVolume: "eriphe-cache",
			taskBin: os.Getenv("ERIPHE_LIVE_TASK_BIN")})
	} else {
		share, self := os.Getenv("ERIPHE_LIVE_SHARE"), os.Getenv("ERIPHE_LIVE_SELF")
		if share == "" || self == "" {
			t.Fatal("ERIPHE_LIVE_SHARE and ERIPHE_LIVE_SELF are needed")
		}
		root = filepath.Join(share, ".eriphe-node-cred")
		os.RemoveAll(root)
		os.MkdirAll(root, 0o700)
		t.Cleanup(func() { os.RemoveAll(root) })
		n = newNode(options{root: root, hostRoot: root, docker: "docker", image: os.Getenv("ERIPHE_LIVE_IMAGE"),
			taskNetwork: envOr("ERIPHE_LIVE_TASK_NETWORK", "eriphe-tasks"), taskMemory: "2g",
			cacheVolume: envOr("ERIPHE_LIVE_CACHE", "eriphe-live-cache"), callbackURL: "http://" + self + ":8790",
			taskBin: os.Getenv("ERIPHE_LIVE_TASK_BIN")})
	}

	// The fake eriphe-core: the private-looking repository's reads are
	// lent, its writes need the user's approval first.
	const private = "eriphe-live/private-looking-repo"
	var mu sync.Mutex
	var asked []credRequest
	approved := false
	ask := func(_ context.Context, _ *Task, req credRequest) (*credAnswer, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, req)
		if !strings.EqualFold(strings.TrimSuffix(req.Path, ".git"), private) {
			return &credAnswer{Decision: "deny", Reason: "not this task's repository"}, nil
		}
		if req.Access == "write" && !approved {
			return &credAnswer{Decision: "ask", Reason: "first push",
				Permission: map[string]any{"host": "github.com", "repo": private, "access": "write"}}, nil
		}
		return &credAnswer{Decision: "allow", Username: "x-access-token", Password: "ghs_fake_" + req.Access + "_" + req.Tool,
			Expires: time.Now().Add(time.Hour)}, nil
	}

	mux := http.NewServeMux()
	mux.Handle("POST "+credentialPath, n.credentialHandlerWith(ask))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "eriphe-node\n") })
	if apple {
		// The listener and the relay on every address, for the tasks'
		// networks only; a task reaches them at its network's gateway.
		l, err := n.listenTasks("0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		_, port, _ := net.SplitHostPort(l.Addr().String())
		n.opt.listenAddr = net.JoinHostPort("0.0.0.0", port)
		go http.Serve(l, mux)
		el, err := n.listenTasks("0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		defer el.Close()
		_, n.opt.egressPort, _ = net.SplitHostPort(el.Addr().String())
		relay := newEgressProxy(n.egressAuthorizer, nil, net.JoinHostPort("127.0.0.1", port))
		relay.isSelf = func(hp string) bool { return n.nets.gateway(hp, port) }
		go http.Serve(el, relay)
	} else {
		addr, err := n.listenAddr(":8790")
		if err != nil {
			t.Fatal(err)
		}
		n.opt.listenAddr = addr
		host, _, _ := net.SplitHostPort(addr)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go http.Serve(l, mux)
		el, err := net.Listen("tcp", net.JoinHostPort(host, "8791"))
		if err != nil {
			t.Fatal(err)
		}
		defer el.Close()
		n.opt.egressPort = "8791"
		go http.Serve(el, newEgressProxy(n.egressAuthorizer, selfTargets(n.opt.callbackURL, addr), addr))
	}

	script := `
echo "helper=$(git config --get credential.helper)"
echo "usehttppath=$(git config --get credential.useHttpPath)"
echo "bin=$(ls /opt/eriphe/bin | tr '\n' ' ')"
echo "gh_path=$(command -v gh)"
echo "cloned=$(git -C /work/repo log --oneline -1 >/dev/null 2>&1 && echo yes || echo no)"
if git ls-remote https://github.com/octocat/Hello-World HEAD >/dev/null 2>/tmp/public; then echo public=ok; else echo "public=$(tr '\n' ' ' </tmp/public)"; fi
git ls-remote https://github.com/` + private + ` >/dev/null 2>/tmp/read
echo "read=$(tr '\n' ' ' </tmp/read)"
cd /work/repo && git push https://github.com/` + private + ` HEAD:refs/heads/eriphe-live >/dev/null 2>/tmp/write
echo "write=$(tr '\n' ' ' </tmp/write)"
echo "gh_missing=$(gh --version 2>&1 | head -n 1)"
{ apt-get update -qq && apt-get install -y -qq gh; } >/tmp/apt.log 2>&1
echo "gh_apt=$?"
echo "gh_real=$(gh --version 2>&1 | head -n 1)"
echo "gh_call=$(gh api repos/` + private + ` 2>&1 | tr -d '\n\r' | cut -c 1-200)"
`
	runtimes["probe-repo"] = probeRuntime{script}
	t.Cleanup(func() { delete(runtimes, "probe-repo") })
	task := &Task{ID: "livecredrepo", Spec: TaskSpec{Runtime: "probe-repo", Image: n.opt.image, Repo: "octocat/Hello-World"},
		State: stateRunning, dir: filepath.Join(root, "tasks", "credrepo"), secret: newID() + newID(), perms: map[string]*permission{}}
	os.MkdirAll(task.dir, 0o700)
	n.adopt(task)
	if err := n.startContainer(task, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopContainer(task) })
	// The user approves the first push when it is asked (a real task: the
	// workspace's card).
	go func() {
		for !finished(task.state()) {
			task.mu.Lock()
			for _, p := range task.perms {
				if p.ToolName == credentialTool {
					mu.Lock()
					approved = true
					mu.Unlock()
					select {
					case p.answer <- permAnswer{Allow: true}:
					default:
					}
				}
			}
			task.mu.Unlock()
			time.Sleep(200 * time.Millisecond)
		}
	}()
	waitDone(t, task)
	raw, _ := os.ReadFile(task.dir + "/out")
	t.Logf("probe:\n%s", raw)
	got := probeValues(raw)
	mu.Lock()
	t.Logf("eriphe-core was asked: %+v", asked)
	mu.Unlock()

	if got["helper"] != "/opt/eriphe/bin/eriphe-cred" || got["usehttppath"] != "true" {
		t.Errorf("git is not set up: %q %q", got["helper"], got["usehttppath"])
	}
	if !strings.Contains(got["bin"], "eriphe-cred") || !strings.Contains(got["bin"], "gh") || got["gh_path"] != "/opt/eriphe/bin/gh" {
		t.Errorf("injected: %q, gh at %q", got["bin"], got["gh_path"])
	}
	if got["cloned"] != "yes" || got["public"] != "ok" {
		t.Errorf("public repository: cloned %s, ls-remote %s", got["cloned"], got["public"])
	}
	// GitHub refuses the fake token: proof it was sent.
	for _, k := range []string{"read", "write"} {
		if !strings.Contains(got[k], "Invalid username or token") && !strings.Contains(got[k], "Authentication failed") {
			t.Errorf("%s: git did not send the lent token: %q", k, got[k])
		}
	}
	if !strings.Contains(got["gh_missing"], "apt-get install") {
		t.Errorf("gh without the real one: %q", got["gh_missing"])
	}
	if got["gh_apt"] == "0" && (!strings.Contains(got["gh_real"], "gh version") || !strings.Contains(got["gh_call"], "Bad credentials")) {
		t.Errorf("gh after apt: %q, call %q", got["gh_real"], got["gh_call"])
	}
	mu.Lock()
	defer mu.Unlock()
	var seen []string
	for _, r := range asked {
		seen = append(seen, r.Tool+":"+r.Access+":"+strings.TrimSuffix(r.Path, ".git"))
	}
	all := strings.Join(seen, " ")
	for _, want := range []string{"git:read:" + private, "git:write:" + private} {
		if !strings.Contains(all, want) {
			t.Errorf("eriphe-core not asked %s: %s", want, all)
		}
	}
	if got["gh_apt"] == "0" && !strings.Contains(all, "gh:read:"+private) {
		t.Errorf("gh did not ask: %s", all)
	}
	if strings.Contains(all, "Hello-World") {
		t.Errorf("a public repository asked for a credential: %s", all)
	}
}

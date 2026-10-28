package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func credTask(t *testing.T, ask credentialAsker) (*Task, *httptest.Server) {
	t.Helper()
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	task := &Task{ID: "t-1", State: stateRunning, secret: "task-secret", perms: map[string]*permission{}}
	n.tasks[task.ID] = task
	mux := http.NewServeMux()
	mux.Handle("POST "+credentialPath, n.credentialHandlerWith(ask))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return task, srv
}

func postCred(t *testing.T, srv *httptest.Server, secret, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+credentialPath, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestCredentialAllowAndDeny(t *testing.T) {
	answers := map[string]*credAnswer{
		"owner/repo": {Decision: "allow", Username: "x-access-token", Password: "ghs_1"},
		"other/repo": {Decision: "deny", Reason: "not one of the task's repositories"},
		"":           {Decision: "none", Reason: "no credential service for gitlab.com"},
	}
	var asked []credRequest
	_, srv := credTask(t, func(_ context.Context, _ *Task, req credRequest) (*credAnswer, error) {
		asked = append(asked, req)
		return answers[strings.TrimSuffix(req.Path, ".git")], nil
	})
	code, out := postCred(t, srv, "task-secret", `{"protocol":"https","host":"github.com","path":"owner/repo.git","access":"read"}`)
	if code != 200 || out["password"] != "ghs_1" || out["username"] != "x-access-token" {
		t.Fatalf("allow: %d %v", code, out)
	}
	if code, out := postCred(t, srv, "task-secret", `{"host":"github.com","path":"other/repo","access":"read"}`); code != 403 || !strings.Contains(out["reason"].(string), "repositories") {
		t.Fatalf("deny: %d %v", code, out)
	}
	if code, _ := postCred(t, srv, "task-secret", `{"host":"gitlab.com","access":"read"}`); code != 404 {
		t.Fatalf("no service: %d", code)
	}
	if code, _ := postCred(t, srv, "wrong", `{"host":"github.com"}`); code != 401 || len(asked) != 3 {
		t.Fatalf("wrong secret: %d, asked %d", code, len(asked))
	}
}

// An approval goes through the task's permission flow, then eriphe-core is
// asked again.
func TestCredentialAsksTheUser(t *testing.T) {
	var mu sync.Mutex
	approved := false
	calls := 0
	task, srv := credTask(t, func(_ context.Context, _ *Task, req credRequest) (*credAnswer, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if approved {
			return &credAnswer{Decision: "allow", Username: "x-access-token", Password: "ghs_w"}, nil
		}
		return &credAnswer{Decision: "ask", Reason: "first push", Permission: map[string]any{"host": "github.com", "repo": "owner/repo", "access": "write"}}, nil
	})
	for _, allow := range []bool{true, false} {
		mu.Lock()
		approved, calls = false, 0
		mu.Unlock()
		done := make(chan struct{})
		var code int
		var out map[string]any
		go func() {
			code, out = postCred(t, srv, "task-secret", `{"host":"github.com","path":"owner/repo.git","access":"write"}`)
			close(done)
		}()
		var p *permission
		waitFor(t, func() bool {
			task.mu.Lock()
			defer task.mu.Unlock()
			for _, x := range task.perms {
				p = x
			}
			return p != nil
		})
		if p.ToolName != credentialTool || p.Input["repo"] != "owner/repo" {
			t.Fatalf("permission %+v", p)
		}
		mu.Lock()
		approved = allow
		mu.Unlock()
		p.answer <- permAnswer{Allow: allow, Message: "not now"}
		<-done
		if allow && (code != 200 || out["password"] != "ghs_w" || calls != 2) {
			t.Fatalf("approved: %d %v after %d calls", code, out, calls)
		}
		if !allow && (code != 403 || out["reason"] != "not now" || calls != 1) {
			t.Fatalf("denied: %d %v after %d calls", code, out, calls)
		}
	}
}

func TestCredentialEnv(t *testing.T) {
	n := newNode(options{callbackURL: "http://eriphe-node:8790"})
	env := n.credentialEnv(&Task{ID: "t-1", secret: "s"})
	if env["ERIPHE_CRED_URL"] != "http://eriphe-node:8790/v1/task/credential" || env["ERIPHE_TASK_SECRET"] != "s" {
		t.Fatalf("env %v", env)
	}
}

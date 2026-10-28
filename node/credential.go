package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Credentials on demand. A task's standard tools ask for the access they
// need when they need it, through eriphe-cred (git's credential helper, gh's
// wrapper), which calls this node's task listener with the task's own secret:
//
//	POST /v1/task/credential   {protocol, host, path, access}  (task secret)
//
// The node asks eriphe-core (POST /v1/node/tasks/{id}/credential, with its
// own secret, as for everything else), which decides and mints a
// short-lived token. When the user must approve, eriphe-core says so and the
// node asks through the task's permission flow: the approval card is the
// one of every permission. Once answered, the node asks eriphe-core again.

// credentialTool is the permission a credential asks for: only the user may
// answer it (eriphe-core's policy).
const credentialTool = "Credential"

const credentialPath = "/v1/task/credential"

// credentialEnv is what tells eriphe-cred, in the task, where to ask.
func (n *node) credentialEnv(t *Task) map[string]string {
	return map[string]string{
		"ERIPHE_CRED_URL":    n.callbackFor(t) + credentialPath,
		"ERIPHE_TASK_SECRET": t.secret,
	}
}

type credRequest struct {
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host"`
	Path     string `json:"path,omitempty"`
	Access   string `json:"access"`
	Tool     string `json:"tool,omitempty"`
}

// credAnswer is eriphe-core's answer.
type credAnswer struct {
	Decision   string         `json:"decision"` // allow, deny, ask or none (no service for this host)
	Reason     string         `json:"reason,omitempty"`
	Username   string         `json:"username,omitempty"`
	Password   string         `json:"password,omitempty"`
	Expires    time.Time      `json:"expires,omitzero"`
	Permission map[string]any `json:"permission,omitempty"` // with ask: the input of the permission to ask
}

// credentialAsker asks eriphe-core; replaced in tests.
type credentialAsker func(ctx context.Context, t *Task, req credRequest) (*credAnswer, error)

func (n *node) credentialHandler(s *state) http.Handler {
	return n.credentialHandlerWith(func(ctx context.Context, t *Task, req credRequest) (*credAnswer, error) {
		return s.askCredential(ctx, t.ID, req)
	})
}

func (n *node) credentialHandlerWith(ask credentialAsker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := n.taskBySecret(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if t == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if finished(t.state()) {
			http.Error(w, "the task is over", http.StatusGone)
			return
		}
		var req credRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil || req.Host == "" {
			writeCred(w, http.StatusBadRequest, "a credential request names a host")
			return
		}
		for asked := false; ; asked = true {
			a, err := ask(r.Context(), t, req)
			if err != nil {
				writeCred(w, http.StatusBadGateway, "eriphe-core: "+err.Error())
				return
			}
			switch a.Decision {
			case "allow":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"username": a.Username, "password": a.Password, "expires": a.Expires})
				return
			case "none":
				writeCred(w, http.StatusNotFound, a.Reason)
				return
			case "ask":
				if asked {
					writeCred(w, http.StatusForbidden, "not approved")
					return
				}
				// The user answers in the workspace; the tool waits, as the
				// agent waits for any permission.
				p := t.decide(r.Context(), credentialTool, a.Permission)
				if !p.Allow {
					msg := p.Message
					if msg == "" {
						msg = "denied"
					}
					writeCred(w, http.StatusForbidden, msg)
					return
				}
			default:
				writeCred(w, http.StatusForbidden, a.Reason)
				return
			}
		}
	})
}

func writeCred(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"reason": reason})
}

// askCredential relays a request to eriphe-core, at the address the node
// is connected to, with the node's secret and certificate.
func (s *state) askCredential(ctx context.Context, taskID string, req credRequest) (*credAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var a credAnswer
	c := &coreClient{s: s, http: s.longClient()}
	if err := c.do(ctx, http.MethodPost, "/v1/node/tasks/"+taskID+"/credential", req, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

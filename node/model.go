package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// A model behind a node. The user runs a model server (Ollama, llama.cpp,
// vLLM, LM Studio) on a machine of theirs, a home PC or a rented server,
// listening on that machine only, and starts this node there with
// -model-server. No port is opened on that machine: eriphe-core reaches the
// server through the node's tunnel, like the services, and only its
// OpenAI-compatible /v1 API.
//
// A task using it, on any node, is pointed at its own node's task listener,
// with its own secret; its node relays to eriphe-core with the node's
// secret and the task's id; eriphe-core checks that the task's agent uses
// that model and goes on through the tunnel of the node hosting it:
//
//	task → its node:      <callback>/model/v1/{path}         (task secret)
//	node → eriphe-core:   /v1/node/tasks/{id}/model/{path}  (node secret, TLS)
//	eriphe-core → node:   /v1/model/{path}                   (the tunnel)
//	node → model server:  <model-server>/v1/{path}
//
// Responses stream as they come: a chat completion's server-sent events go
// through unbuffered at every hop. The address of the server is the node
// owner's choice, on their machine: eriphe-core can name only a path under
// /v1, never another address, so the node is no way into its network.

// modelTokenEnv carries the task's secret to OpenCode, which sends it as
// the model server's key, to its node only.
const modelTokenEnv = "ERIPHE_MODEL_TOKEN"

// parseModelServer reads -model-server: the server's root address, http or
// https, without credentials, query nor fragment. A /v1 given by habit is
// dropped: the node adds it.
func parseModelServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("-model-server %q: the model server's address, e.g. http://127.0.0.1:11434", raw)
	}
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(p, "/v1")
	u.Path, u.RawPath = strings.TrimRight(p, "/"), ""
	return u.String(), nil
}

// modelTransport reaches the model server: never through a proxy of the
// environment, no time limit on an answer (a long prompt on a small machine
// takes minutes), a short one to connect.
var modelTransport http.RoundTripper = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConnsPerHost:   4,
	IdleConnTimeout:       time.Minute,
	ResponseHeaderTimeout: 15 * time.Minute,
}

// modelPath is the server's path for a relayed one: /v1/<p>, refused if it
// would leave /v1 (an escaped ".." the mux does not clean) or holds a
// backslash or a control character.
func modelPath(p string) (string, bool) {
	if p == "" || strings.ContainsFunc(p, func(r rune) bool { return r < ' ' || r == 0x7f || r == '\\' }) {
		return "", false
	}
	full := "/v1/" + p
	if path.Clean(full) != strings.TrimSuffix(full, "/") {
		return "", false
	}
	return full, true
}

// modelMethod: the OpenAI-compatible API reads with GET (/models) and
// works with POST; nothing else is relayed.
func modelMethod(m string) bool { return m == http.MethodGet || m == http.MethodPost }

// handleModel serves /v1/model/{path} on the tunnel, for eriphe-core: the
// model server's /v1/{path}.
func (n *node) handleModel(w http.ResponseWriter, r *http.Request) {
	if n.opt.modelServer == "" {
		http.Error(w, "this machine lends no model server (-model-server)", http.StatusNotFound)
		return
	}
	if !modelMethod(r.Method) {
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
		return
	}
	base, err := url.Parse(n.opt.modelServer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Under /v1 only, an escaped "../" included: the server's own API
	// (Ollama's pull and delete) stays out of reach.
	p, ok := modelPath(r.PathValue("path"))
	if !ok {
		http.Error(w, "a path of the /v1 API", http.StatusBadRequest)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = base.Scheme, base.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = base.Path+p, ""
			pr.Out.Host = base.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Eriphe-Node")
		},
		FlushInterval: -1,
		Transport:     modelTransport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				log.Printf("model server %s: %v", n.opt.modelServer, err)
			}
			http.Error(w, "the model server does not answer on this machine ("+n.opt.modelServer+")", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

type modelTaskKey struct{}

// modelRelayHandler serves /model/v1/{path} for the task containers whose
// agent uses a model behind a node: relayed to eriphe-core under this
// node's identity, as the catalog relay (relay.go).
func (n *node) modelRelayHandler(s *state) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(modelTaskKey{}).(*Task)
			core, err := url.Parse(s.base())
			if err != nil {
				core = &url.URL{}
			}
			pr.SetURL(core)
			pr.Out.URL.Path = strings.TrimSuffix(core.Path, "/") + "/v1/node/tasks/" + t.ID + "/model/" + pr.In.PathValue("path")
			pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "", ""
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+s.Secret)
			pr.Out.Header.Set("Eriphe-Node", s.Name)
		},
		// A completion streams token by token.
		FlushInterval: -1,
		Transport:     s.relayTransport(),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := n.taskBySecret(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if t == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if finished(t.state()) {
			http.Error(w, "the task is over", http.StatusNotFound)
			return
		}
		if ep := t.Spec.ModelEndpoint; ep == nil || !ep.Relay {
			http.Error(w, "this task has no model behind a node", http.StatusForbidden)
			return
		}
		if !modelMethod(r.Method) {
			http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := modelPath(r.PathValue("path")); !ok {
			http.Error(w, "a path of the /v1 API", http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), modelTaskKey{}, t)))
	})
}

// modelBase is where a task reaches its model behind a node: its own node's
// task listener. Empty for a task without one.
func (n *node) modelBase(t *Task) string {
	if ep := t.Spec.ModelEndpoint; ep == nil || !ep.Relay {
		return ""
	}
	return n.callbackFor(t) + "/model/v1"
}

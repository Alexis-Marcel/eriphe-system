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

// A model behind a device. The user runs a model server (Ollama, llama.cpp,
// vLLM, LM Studio) on a machine of theirs, a home PC or a rented server,
// listening on that machine only, and starts this device there with
// -model-server. No port is opened on that machine: eriphe-core reaches the
// server through the device's tunnel, like the services, and only its
// OpenAI-compatible /v1 API.
//
// A task using it, on any device, is pointed at its own device's task listener,
// with its own secret; its device relays to eriphe-core with the device's
// secret and the task's id; eriphe-core checks that the task's agent uses
// that model and goes on through the tunnel of the device hosting it:
//
//	task → its device:      <callback>/model/v1/{path}         (task secret)
//	device → eriphe-core:   /v1/device/tasks/{id}/model/{path}  (device secret, TLS)
//	eriphe-core → device:   /v1/model/{path}                   (the tunnel)
//	device → model server:  <model-server>/v1/{path}
//
// Responses stream as they come: a chat completion's server-sent events go
// through unbuffered at every hop. The address of the server is the device
// owner's choice, on their machine: eriphe-core can name only a path under
// /v1, never another address, so the device is no way into its network.

// modelTokenEnv carries the task's secret to OpenCode, which sends it as
// the model server's key, to its device only.
const modelTokenEnv = "ERIPHE_MODEL_TOKEN"

// parseModelServer reads -model-server: the server's root address, http or
// https, without credentials, query nor fragment. A /v1 given by habit is
// dropped: the device adds it.
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
func (n *device) handleModel(w http.ResponseWriter, r *http.Request) {
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
			pr.Out.Header.Del("Eriphe-Device")
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
// agent uses a model behind a device: relayed to eriphe-core under this
// device's identity, as the catalog relay (relay.go).
func (n *device) modelRelayHandler(s *state) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(modelTaskKey{}).(*Task)
			core, err := url.Parse(s.base())
			if err != nil {
				core = &url.URL{}
			}
			pr.SetURL(core)
			pr.Out.URL.Path = strings.TrimSuffix(core.Path, "/") + "/v1/device/tasks/" + t.ID + "/model/" + pr.In.PathValue("path")
			pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "", ""
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+s.Secret)
			pr.Out.Header.Set("Eriphe-Device", s.Name)
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
			http.Error(w, "this task has no model behind a device", http.StatusForbidden)
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

// modelBase is where a task reaches its model behind a device: its own device's
// task listener. Empty for a task without one.
func (n *device) modelBase(t *Task) string {
	if ep := t.Spec.ModelEndpoint; ep == nil || !ep.Relay {
		return ""
	}
	return n.callbackFor(t) + "/model/v1"
}

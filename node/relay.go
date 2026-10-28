package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
)

// The catalog relay. A task's MCP configuration names, for each catalog
// server it was lent, this node: <callback-url>/mcp/<server>, with the
// task's own secret. The node relays the call to eriphe-core with its own
// secret and the task's id, over the same connection as its assignments and
// reports; eriphe-core calls the server with the real credential. The task
// container never holds a vendor token, and the node never sees one either.

type relayTaskKey struct{}

// relayHandler serves /mcp/{server} for the task containers.
func (n *node) relayHandler(s *state) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			t := pr.In.Context().Value(relayTaskKey{}).(*Task)
			// The address the node is connected to: at home or away.
			core, err := url.Parse(s.base())
			if err != nil {
				core = &url.URL{}
			}
			pr.SetURL(core)
			pr.Out.URL.Path = strings.TrimSuffix(core.Path, "/") + "/v1/node/tasks/" + t.ID + "/mcp/" + pr.In.PathValue("server")
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = ""
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set("Authorization", "Bearer "+s.Secret)
			pr.Out.Header.Set("Eriphe-Node", s.Name)
		},
		// Server-sent events go through as they come: long calls stream.
		FlushInterval: -1,
		// Over TLS with the node's certificate, as everything else (tls.go).
		Transport: s.relayTransport(),
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
		if !slices.Contains(t.Spec.Servers, r.PathValue("server")) {
			http.Error(w, "this task has no access to this server", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), relayTaskKey{}, t)))
	})
}

// taskBySecret finds the task a container's secret belongs to.
func (n *node) taskBySecret(secret string) *Task {
	if secret == "" {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var found *Task
	for _, t := range n.tasks {
		if t.secret != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(t.secret)) == 1 {
			found = t
		}
	}
	return found
}

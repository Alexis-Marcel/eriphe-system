package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"path"
	"slices"
	"strings"
)

// A model behind a node. A self-hosted model server (Ollama, llama.cpp,
// vLLM, LM Studio) runs on one of the user's machines, listening there
// only, and that machine's node lends it (eriphe-node -model-server): no
// port is opened, the server is reached through the node's tunnel like the
// services (services.go). The provider names that node (Provider.Node) in
// place of an address.
//
// A task whose agent uses it, on any node and the brain's too, calls its
// own node's task listener with its own secret; that node relays here with
// its secret and the task's id (node/model.go):
//
//	/v1/node/tasks/{id}/model/{path}   (node secret)
//
// eriphe-core checks that the task runs on that node and that its agent
// uses a model behind a node, adds the server's key if the provider has
// one (the task never holds it), and goes on through the tunnel of the
// node that lends it, to its /v1/model/{path}: the model server's
// /v1/{path}. Answers stream through as they come.

// modelBodyLimit bounds what a task may send to its model in one request:
// a long conversation with its tools, not a file upload.
const modelBodyLimit = 64 << 20

// modelHost is the node that lends the model of a task's agent, and that
// model server's key (empty: none), or why the task may not reach one.
func (c *core) modelHost(t *TaskRow) (string, string, error) {
	cfg := c.conf()
	_, a, err := cfg.agent(t.Agent)
	if err != nil {
		return "", "", err
	}
	p := cfg.Providers[a.Provider]
	if p.Kind != selfHostedKind || p.Node == "" || a.Runtime != "opencode" {
		return "", "", errors.New("this task's agent uses no model behind a node")
	}
	key := ""
	if p.Secret != "" {
		if key, err = readSecret(p.Secret); err != nil {
			return "", "", fmt.Errorf("provider %s: %w", a.Provider, err)
		}
	}
	return p.Node, key, nil
}

// modelPathOK tells whether a relayed path stays under the server's /v1:
// no "..", even escaped, no backslash nor control character. The node that
// lends the server checks it again.
func modelPathOK(p string) bool {
	if p == "" || strings.ContainsFunc(p, func(r rune) bool { return r < ' ' || r == 0x7f || r == '\\' }) {
		return false
	}
	full := "/v1/" + p
	return path.Clean(full) == strings.TrimSuffix(full, "/")
}

// modelProxy relays a request to the model server node lends, through its
// tunnel, with key if the server has one.
func modelProxy(n *nodeClient, key string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", "node", "node"
			pr.Out.URL.Path, pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "/v1/model/"+pr.In.PathValue("path"), "", ""
			// The task's secret and the node's stay here.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Eriphe-Node")
			if key != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+key)
			}
		},
		// A completion streams token by token.
		FlushInterval: -1,
		Transport:     n.stream.Transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				log.Printf("model on node %s: %v", n.name, err)
			}
			http.Error(w, "the machine of the model ("+n.name+") does not answer", http.StatusBadGateway)
		},
	}
}

// taskModelHandler serves /v1/node/tasks/{id}/model/{path...}, for nodes.
func (c *core) taskModelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node, ok := c.nodeFromRequest(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		t, err := c.store.task(r.PathValue("id"))
		if err != nil || t.Node != node || finished(t.State) {
			http.Error(w, "no such task on this node", http.StatusNotFound)
			return
		}
		host, key, err := c.modelHost(t)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
			return
		}
		if !modelPathOK(r.PathValue("path")) {
			http.Error(w, "a path of the /v1 API", http.StatusBadRequest)
			return
		}
		n := c.node(host)
		if !n.connected() {
			http.Error(w, "the machine of the model ("+host+") is not connected", http.StatusBadGateway)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, modelBodyLimit)
		modelProxy(n, key).ServeHTTP(w, r)
	})
}

// nodeModels asks the model server a node lends for its models (GET
// /v1/models), for the provider form's test.
func (c *core) nodeModels(ctx context.Context, node, key string) (*http.Response, error) {
	n := c.node(node)
	if !n.connected() {
		return nil, errOffline
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://node/v1/model/models", nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return n.http.Do(req)
}

// lendsModel tells whether a provider may name node: one that lends a
// model server, or the one it already names (kept while its machine is
// away or not updated).
func (c *core) lendsModel(node, current string) bool {
	return node != "" && (node == current || slices.Contains(c.modelNodes(), node))
}

// modelNodes are the nodes that lend a model server, by name, for the
// provider form.
func (c *core) modelNodes() []string {
	nodes, err := c.store.nodes()
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range nodes {
		if parseInfo(n).ModelServer {
			out = append(out, n.Name)
		}
	}
	return out
}

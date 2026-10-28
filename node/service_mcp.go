package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Each service is a streamable HTTP MCP endpoint through the tunnel,
// /v1/services/{name}/mcp, which only eriphe-core can reach.
//
//   - http: a reverse proxy to the container.
//   - stdio: a bridge. The node is the only client of the container's
//     process, over its stdin and stdout, and serves its tools again as an
//     MCP server of its own, so several sessions of eriphe-core share one
//     process. The process sees one client: whatever state it keeps
//     (a selected mailbox, a working directory, a login) is shared by every
//     session, and its notifications (progress, logs) and its requests
//     (sampling, elicitation) are not relayed. Only tools are relayed. The
//     bridge outlives the container: sessions survive a restart, and the
//     tools are listed again (tools/list_changed) when it comes back.

func (n *node) handleServiceMCP(w http.ResponseWriter, r *http.Request) {
	s := n.getService(r.PathValue("name"))
	if s == nil {
		http.Error(w, "no such service on this node", http.StatusNotFound)
		return
	}
	if s.bridge != nil {
		s.bridge.ServeHTTP(w, r)
		return
	}
	s.mu.Lock()
	target := s.target
	s.mu.Unlock()
	if target == "" {
		http.Error(w, "the service is not running", http.StatusServiceUnavailable)
		return
	}
	u, err := url.Parse(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.URL.Path, pr.Out.URL.RawPath = u.Scheme, u.Host, u.Path, ""
			// A localhost Host, which servers with DNS rebinding protection accept.
			pr.Out.Host = "localhost:" + u.Port()
		},
		FlushInterval: -1, // server-sent events go through as they come
	}
	proxy.ServeHTTP(w, r)
}

// bridge serves the tools of a stdio MCP server to many sessions.
type bridge struct {
	name string

	mu      sync.Mutex
	server  *mcp.Server  // created at the first connection, with the server's name and instructions
	handler http.Handler // its streamable HTTP endpoint
	cs      *mcp.ClientSession
	tools   map[string]bool
}

func newBridge(name string) *bridge { return &bridge{name: name, tools: map[string]bool{}} }

func (b *bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	h := b.handler
	b.mu.Unlock()
	if h == nil {
		http.Error(w, "the service is starting", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

var errRestarting = errors.New("the service is restarting, try again shortly")

// serve speaks MCP with the server on r and w: it initializes, publishes
// its tools, calls ready, and returns when the connection ends.
func (b *bridge) serve(ctx context.Context, r io.ReadCloser, w io.WriteCloser, ready func()) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "eriphe-node", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, req *mcp.ToolListChangedRequest) {
			go func() {
				if err := b.syncTools(context.Background(), req.Session); err != nil {
					log.Printf("service %s: tools: %v", b.name, err)
				}
			}()
		},
	})
	cctx, cancel := context.WithTimeout(ctx, serviceStartup)
	cs, err := client.Connect(cctx, &mcp.IOTransport{Reader: r, Writer: w}, nil)
	cancel()
	if err != nil {
		r.Close()
		w.Close()
		return fmt.Errorf("initialize: %w", err)
	}
	b.mu.Lock()
	if b.server == nil {
		init := cs.InitializeResult()
		impl := &mcp.Implementation{Name: b.name, Version: "0"}
		opts := &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}}
		if init != nil && init.ServerInfo != nil {
			impl = init.ServerInfo
			opts.Instructions = init.Instructions
		}
		b.server = mcp.NewServer(impl, opts)
		b.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return b.server },
			&mcp.StreamableHTTPOptions{DisableLocalhostProtection: true, SessionTimeout: time.Hour})
	}
	b.cs = cs
	b.mu.Unlock()
	if err := b.syncTools(ctx, cs); err != nil {
		cs.Close()
		b.detach(cs)
		return fmt.Errorf("tools/list: %w", err)
	}
	ready()
	err = cs.Wait()
	b.detach(cs)
	return err
}

func (b *bridge) detach(cs *mcp.ClientSession) {
	b.mu.Lock()
	if b.cs == cs {
		b.cs = nil
	}
	b.mu.Unlock()
}

func (b *bridge) current() *mcp.ClientSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cs
}

// syncTools makes the bridge's tools those of the server.
func (b *bridge) syncTools(ctx context.Context, cs *mcp.ClientSession) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var tools []*mcp.Tool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return err
		}
		tools = append(tools, t)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cs != cs {
		return nil // an older connection
	}
	seen := map[string]bool{}
	for _, t := range tools {
		if err := addTool(b.server, t, b.call); err != nil {
			log.Printf("service %s: tool %s skipped: %v", b.name, t.Name, err)
			continue
		}
		seen[t.Name] = true
	}
	var gone []string
	for name := range b.tools {
		if !seen[name] {
			gone = append(gone, name)
		}
	}
	if len(gone) > 0 {
		b.server.RemoveTools(gone...)
	}
	b.tools = seen
	return nil
}

// addTool adds a tool the go-sdk may refuse (a schema that is not an object).
func addTool(s *mcp.Server, t *mcp.Tool, h mcp.ToolHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	s.AddTool(t, h)
	return nil
}

// call relays a tool call to the server.
func (b *bridge) call(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cs := b.current()
	if cs == nil {
		return nil, errRestarting
	}
	p := &mcp.CallToolParams{Meta: req.Params.Meta, Name: req.Params.Name}
	if len(req.Params.Arguments) > 0 && string(req.Params.Arguments) != "null" {
		p.Arguments = json.RawMessage(req.Params.Arguments)
	}
	return cs.CallTool(ctx, p)
}

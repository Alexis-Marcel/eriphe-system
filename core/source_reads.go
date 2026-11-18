package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/a2ui"
)

// A live block's read (node_live.go): a call of any catalog tool with
// fixed arguments ({"tool": "server/tool", "args": {…}}) or one of a
// server's resources ({"resource": "server", "uri": "…"}). Code runs it
// as a monitor runs its look (monitors.go: sourceWhy, readSource): a free
// read of the brain's, read-only by the tool's own annotation, or a
// resource the server lists; within a timeout; its result kept as it came,
// never understood. Nothing here knows a server or a tool: any tool
// linked tomorrow is a source with no code.

const (
	sourceValueMax = 256 << 10 // a source's result
	sourceTimeout  = 20 * time.Second
	sourceEveryDef = 5 // minutes between two reads, by default
)

// sourceCall is one read: a tool ("server/tool") with fixed arguments, or
// a server's resource by its URI.
type sourceCall struct {
	Tool     string         `json:"tool,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Resource string         `json:"resource,omitempty"` // the server
	URI      string         `json:"uri,omitempty"`
}

// spec is the read as a monitor's look: what sourceWhy and readSource take.
func (s sourceCall) spec() monitorSpec {
	if s.Resource != "" {
		return monitorSpec{Way: wayPoll, Server: s.Resource, Resource: s.URI}
	}
	server, tool, _ := strings.Cut(s.Tool, "/")
	return monitorSpec{Way: wayPoll, Server: server, Tool: tool, Args: s.Args}
}

// String names the read in a message: server/tool, or server and URI.
func (s sourceCall) String() string {
	if s.Resource != "" {
		return s.Resource + " " + s.URI
	}
	return s.Tool
}

// sourceRead is what a read gave.
type sourceRead struct {
	value any
	err   string // it failed
	why   string // sourceWhy refused it: nothing was called
}

// readSources reads the sources (only those whose keys are in only, if
// given), each under the guards, at once.
func (c *core) readSources(ctx context.Context, srcs map[string]sourceCall, only []string) map[string]sourceRead {
	out := map[string]sourceRead{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for key, s := range srcs {
		if only != nil && !slices.Contains(only, key) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := c.readOne(ctx, s)
			mu.Lock()
			out[key] = r
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (c *core) readOne(ctx context.Context, s sourceCall) sourceRead {
	spec := s.spec()
	if why := c.sourceWhy(spec); why != "" {
		return sourceRead{why: why}
	}
	v, err := c.readSource(ctx, spec, sourceTimeout)
	if err != nil {
		return sourceRead{err: truncate(err.Error(), 300)}
	}
	if raw, _ := json.Marshal(v); len(raw) > sourceValueMax {
		return sourceRead{err: fmt.Sprintf("%s gave %d KiB, more than %d: narrow its arguments", s, len(raw)>>10, sourceValueMax>>10)}
	}
	return sourceRead{value: v}
}

// tokens splits a JSON pointer, each token unescaped.
func tokens(ptr string) []string {
	if ptr == "" || ptr == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts
}

// linkOrDrop is a Link's address as the page gets it: as it is, or "".
func linkOrDrop(u string) string {
	if a2ui.LinkURLOK(u) {
		return u
	}
	return ""
}

// rewriteAt applies fn to the strings at a path, "*" for each item.
func rewriteAt(v any, toks []string, fn func(string) string) any {
	if len(toks) == 0 {
		if s, ok := v.(string); ok {
			return fn(s)
		}
		return v
	}
	t, rest := toks[0], toks[1:]
	switch x := v.(type) {
	case map[string]any:
		if next, ok := x[t]; ok {
			x[t] = rewriteAt(next, rest, fn)
		}
	case []any:
		if t == "*" {
			for i := range x {
				x[i] = rewriteAt(x[i], rest, fn)
			}
		} else if i, err := strconv.Atoi(t); err == nil && i >= 0 && i < len(x) {
			x[i] = rewriteAt(x[i], rest, fn)
		}
	}
	return v
}

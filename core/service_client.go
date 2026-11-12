package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serviceClient is an MCP session to a service, for an owner that calls
// it itself: a channel its own service (service_owners.go). The catalog's
// pool keeps its own sessions, with the tools' lists and accesses
// (pool.go); both dial the same way (dialService). The session opens at
// the first call, and again after one that failed with it.
type serviceClient struct {
	name string
	dial func(ctx context.Context, name string) (mcp.Transport, error)

	mu   sync.Mutex
	sess *mcp.ClientSession
}

func (c *core) newServiceClient(name string) *serviceClient {
	return &serviceClient{name: name, dial: c.dialService}
}

// session is the open session, or a new one.
func (s *serviceClient) session(ctx context.Context) (*mcp.ClientSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != nil {
		return s.sess, nil
	}
	tr, err := s.dial(ctx, s.name)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "eriphe-core", Version: version}, &mcp.ClientOptions{KeepAlive: 30 * time.Second})
	// The session outlives the call that opens it.
	sess, err := client.Connect(context.WithoutCancel(ctx), tr, nil)
	if err != nil {
		return nil, err
	}
	s.sess = sess
	return sess, nil
}

// drop closes the session, if it is still that one.
func (s *serviceClient) drop(sess *mcp.ClientSession) {
	s.mu.Lock()
	if s.sess == sess {
		s.sess = nil
	}
	s.mu.Unlock()
	sess.Close()
}

// close closes the session.
func (s *serviceClient) close() {
	s.mu.Lock()
	sess := s.sess
	s.sess = nil
	s.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
}

// call calls one of the service's tools and decodes its structured answer
// into out (when not nil); a tool's error is an error, in its words.
func (s *serviceClient) call(ctx context.Context, tool string, args, out any) error {
	sess, err := s.session(ctx)
	if err != nil {
		return err
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		if ctx.Err() == nil {
			s.drop(sess) // the session failed, not the wait: a new one next time
		}
		return err
	}
	if res.IsError {
		return errors.New(truncate(resultText(res), 300))
	}
	if out != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		return json.Unmarshal(raw, out)
	}
	return nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

// The node dials eriphe-core and keeps that connection open: eriphe-core
// sends its requests back through it, so the node needs no open port.

const tunnelProto = "eriphe-tunnel"

// state is what the node keeps after joining, in <root>/node.json.
type state struct {
	Core   string   `json:"core"`
	Cores  []string `json:"cores,omitempty"` // all of eriphe-core's addresses, preferred first (cores.go)
	Name   string   `json:"name"`
	Secret string   `json:"secret"`

	coreState
	tlsState // eriphe-core's CA, this node's certificate (tls.go)
}

func loadState(path string) (*state, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s state
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// join registers the node with a join token, at the first of eriphe-core's
// addresses that answers, and saves its secret. With pin (-ca-hash), it
// joins over TLS only, with eriphe-core's CA of that hash (tls.go).
func join(path string, cores []string, name, token, pin string) (*state, error) {
	s := &state{Name: name}
	s.setURLs(cores)
	s.path = path
	s.loadTLS(pin)
	raw, _ := json.Marshal(map[string]string{"name": name, "token": token})
	var secret string
	err := eachCore(s.urls(), func(core string) error {
		resp, err := s.dialClient().Post(core+"/v1/nodes/join", "application/json", bytes.NewReader(raw))
		if err != nil {
			return &unreachableError{core, err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			return fmt.Errorf("join: %s: %s", resp.Status, bytes.TrimSpace(msg))
		}
		var out struct {
			Secret string `json:"secret"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return err
		}
		secret = out.Secret
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Secret = secret
	if err := s.save(path); err != nil {
		return nil, err
	}
	return s, nil
}

// preferEvery is how often a node on a fallback address checks the preferred ones.
var preferEvery = 2 * time.Minute

var errRejected = errors.New("eriphe-core rejected this node's secret")

// serve connects to eriphe-core and serves api on the tunnel, reconnecting
// whenever the connection drops.
func serve(s *state, api http.Handler) {
	backoff := time.Second
	for {
		start := time.Now()
		err := connect(s, api)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		wait := backoff
		if errors.Is(err, errRejected) {
			wait = time.Minute
			err = fmt.Errorf("%w: the node was removed, delete node.json and join again", err)
		}
		log.Printf("tunnel: %v; retrying in %s", err, wait)
		time.Sleep(wait)
		backoff = min(backoff*2, 30*time.Second)
	}
}

// connect opens the tunnel at the first of eriphe-core's addresses that
// answers, and serves api on it until it drops.
func connect(s *state, api http.Handler) error {
	// A node without its certificate gets it first (tls.go).
	s.certifyNow()
	var resp *http.Response
	var core string
	err := eachCore(s.urls(), func(u string) error {
		req, err := http.NewRequest(http.MethodGet, u+"/v1/nodes/connect", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+s.Secret)
		req.Header.Set("Eriphe-Node", s.Name)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", tunnelProto)
		r, err := s.dialClient().Do(req)
		if err != nil {
			return &unreachableError{u, err}
		}
		if r.StatusCode == http.StatusUnauthorized {
			r.Body.Close()
			return errRejected
		}
		if r.StatusCode != http.StatusSwitchingProtocols {
			msg, _ := io.ReadAll(io.LimitReader(r.Body, 500))
			r.Body.Close()
			return fmt.Errorf("connect: %s: %s", r.Status, bytes.TrimSpace(msg))
		}
		resp, core = r, u
		return nil
	})
	if err != nil {
		return err
	}
	s.setActive(core)
	// After a 101, the body is the raw connection.
	conn := resp.Body.(io.ReadWriteCloser)
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.LogOutput = io.Discard
	session, err := yamux.Server(conn, cfg)
	if err != nil {
		conn.Close()
		return err
	}
	how := "without TLS"
	if s.certificate() != nil && strings.HasPrefix(core, "https://") {
		how = "over TLS, with this node's certificate"
	}
	log.Printf("connected to %s as %s, %s", core, s.Name, how)
	// On a fallback address, move back to a preferred one when it answers.
	ctx, cancel := context.WithCancel(context.Background())
	go preferred(ctx, s, core, preferEvery, func() { session.Close() })
	err = (&http.Server{Handler: api, ReadHeaderTimeout: 10 * time.Second}).Serve(session)
	cancel()
	session.Close()
	return fmt.Errorf("disconnected: %w", err)
}

var invalidName = regexp.MustCompile(`[^a-z0-9-]+`)

// defaultName is the machine's host name, as a valid node name.
func defaultName() string {
	h, _ := os.Hostname()
	h = strings.Trim(invalidName.ReplaceAllString(strings.ToLower(strings.Split(h, ".")[0]), "-"), "-")
	if h == "" {
		return "node"
	}
	return h[:min(len(h), 32)]
}

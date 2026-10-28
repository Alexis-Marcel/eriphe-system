package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// A node can know several addresses of eriphe-core, in order of
// preference: the local one first, then the remote one (over Tailscale),
// so a laptop works at home and away. It tries them in order when it
// connects, uses the one that answered for everything else, and moves back
// to a preferred one as soon as it answers again. node.json keeps them in
// "cores"; "core" stays the first one, for older versions.

// coreState is the node's view of its addresses; state embeds it.
type coreState struct {
	mu     sync.Mutex
	active string // the address the tunnel is connected to
}

// parseCores reads a list of addresses separated by commas or spaces.
func parseCores(s string) []string {
	var out []string
	for _, u := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if u = strings.TrimSuffix(u, "/"); u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// urls are the node's addresses of eriphe-core, preferred first, in
// https:// once the node reaches it over TLS (tls.go).
func (s *state) urls() []string {
	urls := s.saved()
	if !s.secure() {
		return urls
	}
	out := make([]string, len(urls))
	for i, u := range urls {
		out[i] = secureURL(u)
	}
	return out
}

// saved are the addresses as given and kept in node.json.
func (s *state) saved() []string {
	if len(s.Cores) > 0 {
		return s.Cores
	}
	return []string{s.Core}
}

// base is the address to use now: the one connected to, or the first.
func (s *state) base() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" {
		return s.active
	}
	return s.urls()[0]
}

func (s *state) setActive(u string) {
	s.mu.Lock()
	s.active = u
	s.mu.Unlock()
}

// setURLs replaces the addresses; it reports whether they changed.
func (s *state) setURLs(urls []string) bool {
	if len(urls) == 0 || slices.Equal(urls, s.saved()) {
		return false
	}
	s.Core = urls[0]
	s.Cores = nil
	if len(urls) > 1 {
		s.Cores = urls
	}
	return true
}

func (s *state) save(path string) error {
	s.tlsMu.Lock()
	raw, _ := json.MarshalIndent(s, "", "  ")
	s.tlsMu.Unlock()
	return os.WriteFile(path, raw, 0o600)
}

// eachCore calls try with each address in order, until one is reachable:
// errUnreachable from try moves on to the next, any other result stops.
func eachCore(urls []string, try func(string) error) error {
	var errs []error
	for _, u := range urls {
		err := try(u)
		var unreachable *unreachableError
		if !errors.As(err, &unreachable) {
			return err
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// unreachableError is an address that did not answer at all.
type unreachableError struct {
	url string
	err error
}

func (e *unreachableError) Error() string { return e.url + ": " + e.err.Error() }
func (e *unreachableError) Unwrap() error { return e.err }

// preferred watches, while the tunnel is on a fallback address, whether a
// preferred one answers again; then it calls back, to reconnect there.
func preferred(ctx context.Context, s *state, current string, every time.Duration, back func()) {
	urls := s.urls()
	i := slices.Index(urls, current)
	if i <= 0 {
		return
	}
	better := urls[:i]
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, u := range better {
			if reachable(ctx, s.dialClient(), u) {
				log.Printf("tunnel: %s answers again, moving there", u)
				back()
				return
			}
		}
	}
}

func reachable(ctx context.Context, client *http.Client, u string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

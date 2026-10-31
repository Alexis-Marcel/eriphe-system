package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"tailscale.com/client/local"
	"tailscale.com/logtail"
	"tailscale.com/tsnet"
)

// tsNet is the tailnet interface over the real tsnet: userspace
// networking (no TUN device, no capability), its state in a directory.

type tsNet struct {
	srv *tsnet.Server
	lc  *local.Client
}

var disableLogUpload sync.Once

func newTSNet(o tailnetOptions) (tailnet, error) {
	// tsnet uploads its diagnostic logs to Tailscale by default: not ours.
	disableLogUpload.Do(logtail.Disable)
	// tsnet repeats its login hint every few seconds: log a line once in a row.
	var mu sync.Mutex
	var last string
	logf := func(format string, args ...any) {
		line := redactKeys(strings.TrimSpace(fmt.Sprintf(format, args...)))
		mu.Lock()
		repeat := line == last
		last = line
		mu.Unlock()
		if !repeat {
			o.Logf("remote: tailscale: %s", line)
		}
	}
	srv := &tsnet.Server{
		Dir:        o.Dir,
		Hostname:   o.Hostname,
		ControlURL: o.ControlURL,
		AuthKey:    o.AuthKey,
		Ephemeral:  false,
		UserLogf:   logf,
		Logf:       func(string, ...any) {}, // the backend's debug logs: too verbose to keep
	}
	return &tsNet{srv: srv}, nil
}

func (t *tsNet) Start() error {
	if err := t.srv.Start(); err != nil {
		return err
	}
	lc, err := t.srv.LocalClient()
	if err != nil {
		return err
	}
	t.lc = lc
	return nil
}

var errNotStarted = errors.New("tailscale is not started")

func (t *tsNet) Status(ctx context.Context) (tsStatus, error) {
	if t.lc == nil {
		return tsStatus{}, errNotStarted
	}
	st, err := t.lc.StatusWithoutPeers(ctx)
	if err != nil {
		return tsStatus{}, err
	}
	out := tsStatus{Backend: st.BackendState, AuthURL: st.AuthURL, CertDomains: st.CertDomains}
	if st.Self != nil {
		out.DNSName = strings.TrimSuffix(st.Self.DNSName, ".")
		if st.Self.KeyExpiry != nil {
			out.KeyExpiry = *st.Self.KeyExpiry
		}
	}
	if st.CurrentTailnet != nil {
		out.Tailnet, out.MagicDNS = st.CurrentTailnet.Name, st.CurrentTailnet.MagicDNSEnabled
	}
	return out, nil
}

func (t *tsNet) QueryHTTPS(ctx context.Context) (bool, string, string, error) {
	if t.lc == nil {
		return false, "", "", errNotStarted
	}
	r, err := t.lc.QueryFeature(ctx, "serve")
	if err != nil {
		return false, "", "", err
	}
	return r.Complete, r.URL, r.Text, nil
}

func (t *tsNet) Listen(addr string) (net.Listener, error) { return t.srv.Listen("tcp", addr) }

func (t *tsNet) GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if t.lc == nil {
		return nil, errNotStarted
	}
	return t.lc.GetCertificate(hi)
}

func (t *tsNet) PrepareCert(ctx context.Context, domain string) error {
	if t.lc == nil {
		return errNotStarted
	}
	_, _, err := t.lc.CertPair(ctx, domain)
	return err
}

func (t *tsNet) StartLogin(ctx context.Context) error {
	if t.lc == nil {
		return errNotStarted
	}
	return t.lc.StartLoginInteractive(ctx)
}

func (t *tsNet) Logout(ctx context.Context) error {
	if t.lc == nil {
		return errNotStarted
	}
	return t.lc.Logout(ctx)
}

func (t *tsNet) Close() error { return t.srv.Close() }

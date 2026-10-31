package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Remote access: eriphe-core joins the owner's private Tailscale network
// (a tailnet) by itself, with an embedded Tailscale (tsnet, remote_tsnet.go),
// and serves the workspace and the API there, in HTTPS with a certificate
// browsers trust (eriphe.<tailnet>.ts.net). Nothing is opened on the
// router and nothing is public: only the devices of the tailnet reach it.
// Off by default; the workspace's Settings → Remote access turns it on.
//
// The controller below follows tsnet's state (login, approval, HTTPS, ready)
// and opens the tailnet listeners once a certificate can be had. tsnet is
// behind the small tailnet interface, so the tests drive a fake.

// tailnet is the little of tsnet the controller uses.
type tailnet interface {
	Start() error
	Status(ctx context.Context) (tsStatus, error)
	// QueryHTTPS asks whether HTTPS certificates are enabled in the
	// tailnet; if not, url is the one-click consent page, and text what
	// the coordination server says about it.
	QueryHTTPS(ctx context.Context) (enabled bool, url, text string, err error)
	Listen(addr string) (net.Listener, error) // on the tailnet only
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// PrepareCert gets the tailnet's certificate for domain (issued on
	// the first call, which can take a while; cached afterwards).
	PrepareCert(ctx context.Context, domain string) error
	StartLogin(ctx context.Context) error
	Logout(ctx context.Context) error
	Close() error
}

// tsStatus is what the controller reads of tsnet's status.
type tsStatus struct {
	Backend     string // NoState, NeedsLogin, NeedsMachineAuth, Starting, Running, Stopped
	AuthURL     string
	DNSName     string // eriphe.tail1234.ts.net, without the final dot
	Tailnet     string
	MagicDNS    bool
	CertDomains []string
	KeyExpiry   time.Time // zero: the key does not expire (a tagged node)
}

type tailnetOptions struct {
	Dir        string
	Hostname   string
	ControlURL string
	AuthKey    string
	Logf       func(format string, args ...any)
}

// remoteSettings is what the workspace saved, in the kv table. The auth
// key is in the vault, under remoteAuthKeyName.
type remoteSettings struct {
	Enabled    bool   `json:"enabled"`
	Hostname   string `json:"hostname,omitempty"`
	ControlURL string `json:"control_url,omitempty"`
	// Name is the tailnet name last served (eriphe.tail1234.ts.net), for
	// the installers to print it (eriphe-core remote-address).
	Name string `json:"name,omitempty"`
	// Joined: the saved identity has logged in (tsnet ran): the auth key
	// is not given again.
	Joined bool `json:"joined,omitempty"`
}

const (
	remoteKey         = "remote_access"
	remoteAuthKeyName = "tailscale.authkey"
	defaultRemoteHost = "eriphe"
	remoteExpiryWarn  = 14 * 24 * time.Hour
	remoteAPIPort     = "8800"
	// How long logging out may take, tsnet started for it included.
	remoteLogoutTimeout = 30 * time.Second
	// The state tsnet keeps in its directory: there is an identity.
	remoteStateFile = "tailscaled.state"
)

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (s remoteSettings) hostname() string {
	if s.Hostname == "" {
		return defaultRemoteHost
	}
	return s.Hostname
}

// Phases of remote access, as the page shows them.
const (
	phaseOff      = "off"
	phaseStarting = "starting"
	phaseLogin    = "login"    // waiting for the owner to log in with the AuthURL
	phaseApproval = "approval" // logged in, waiting for an admin to approve the device
	phaseHTTPS    = "https"    // running, HTTPS certificates not enabled in the tailnet
	phaseCert     = "cert"     // HTTPS enabled, the certificate being issued
	phaseReady    = "ready"
	phaseError    = "error"
)

// remoteView is the state the settings page shows.
type remoteView struct {
	Phase      string
	Enabled    bool
	Hostname   string
	ControlURL string
	HasAuthKey bool
	AuthURL    string // to log in, or to log in again
	HTTPSURL   string // the consent page that enables HTTPS
	HTTPSText  string
	MagicDNS   bool
	Name       string // eriphe.tail1234.ts.net
	Tailnet    string
	KeyExpiry  time.Time
	Expiring   bool // the key expires within remoteExpiryWarn (or did)
	Expired    bool
	Error      string
	Listening  bool
}

// URL is the workspace's address on the tailnet.
func (v remoteView) URL() string {
	if v.Name == "" {
		return ""
	}
	return "https://" + v.Name
}

// APIURL is eriphe-core's API on the tailnet, for nodes away from home.
func (v remoteView) APIURL() string {
	if v.Name == "" {
		return ""
	}
	return "https://" + v.Name + ":" + remoteAPIPort
}

// DefaultControl is whether the coordination server is Tailscale's own.
func (v remoteView) DefaultControl() bool { return v.ControlURL == "" }

type remoteAccess struct {
	store      *Store
	dir        string // tsnet's state, <db dir>/tailscale
	api        http.Handler
	newTailnet func(tailnetOptions) (tailnet, error)
	now        func() time.Time
	every      func(phase string) time.Duration // how often to poll tsnet's status
	authKey    func() string                    // from the vault
	forgetKey  func() error                     // removes it from the vault
	logf       func(format string, args ...any)
	// The machines' CA (machine_tls.go): the API asks nodes for their
	// certificate of it, without requiring one. Nil: it asks for none.
	clientCAs *x509.CertPool

	mu        sync.Mutex
	settings  remoteSettings
	ts        tailnet
	stop      context.CancelFunc // ends the current watch loop
	done      chan struct{}      // closed when it ended
	view      remoteView
	workspace http.Handler // the workspace's routes, once the web started
	servers   []*http.Server
	wake      chan struct{} // closed and replaced whenever the view changes
	login     time.Time     // when StartLogin was last called
}

func newRemoteAccess(store *Store, dbPath string, api http.Handler) *remoteAccess {
	r := &remoteAccess{store: store, dir: filepath.Join(filepath.Dir(dbPath), "tailscale"), api: api,
		newTailnet: newTSNet, now: time.Now, logf: log.Printf, wake: make(chan struct{}),
		authKey: func() string {
			if v := currentVault(); v != nil {
				k, _ := v.get(remoteAuthKeyName)
				return k
			}
			return ""
		},
		forgetKey: func() error {
			v := currentVault()
			if v == nil {
				return errors.New("the vault is closed")
			}
			if err := v.remove(remoteAuthKeyName); err != nil && !errors.Is(err, errNoSecret) {
				return err
			}
			return nil
		},
		every: func(phase string) time.Duration {
			switch phase {
			case phaseReady:
				return 30 * time.Second
			case phaseError:
				return 5 * time.Second
			}
			return time.Second
		}}
	if raw := store.get(remoteKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &r.settings); err != nil {
			log.Printf("remote: settings: %v", err)
		}
	}
	r.view = r.baseView()
	return r
}

// boot starts remote access at eriphe-core's start if it was on.
func (r *remoteAccess) boot() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settings.Enabled && r.ts == nil {
		r.startLocked()
	}
}

// setWorkspace gives the controller the workspace's handler, served on the
// tailnet's :443 once ready.
func (r *remoteAccess) setWorkspace(h http.Handler) {
	r.mu.Lock()
	r.workspace = h
	listening := r.view.Listening
	r.mu.Unlock()
	if listening {
		// Served before the workspace was there: serve it now.
		r.restartListeners()
	}
}

func (r *remoteAccess) save() error {
	raw, _ := json.Marshal(r.settings)
	return r.store.set(remoteKey, string(raw))
}

// current returns the page's view.
func (r *remoteAccess) current() remoteView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.view
	v.HasAuthKey = r.authKey() != ""
	return v
}

// name is the tailnet name the workspace is served under, once known.
func (r *remoteAccess) name() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.view.Name
}

// waker is closed when the view changes.
func (r *remoteAccess) waker() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wake
}

// ready returns the tailnet name when the workspace and the API are served there.
func (r *remoteAccess) ready() (remoteView, bool) {
	if r == nil {
		return remoteView{}, false
	}
	v := r.current()
	return v, v.Phase == phaseReady
}

func (r *remoteAccess) baseView() remoteView {
	v := remoteView{Phase: phaseOff, Enabled: r.settings.Enabled, Hostname: r.settings.hostname(), ControlURL: r.settings.ControlURL}
	if r.settings.Enabled {
		v.Phase = phaseStarting
	}
	return v
}

func (r *remoteAccess) setViewLocked(v remoteView) {
	v.Enabled, v.Hostname, v.ControlURL = r.settings.Enabled, r.settings.hostname(), r.settings.ControlURL
	if v != r.view {
		r.view = v
		close(r.wake)
		r.wake = make(chan struct{})
	}
}

// enable turns remote access on and starts tsnet.
func (r *remoteAccess) enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settings.Enabled && r.ts != nil {
		return nil
	}
	r.settings.Enabled = true
	if err := r.save(); err != nil {
		return err
	}
	r.startLocked()
	return nil
}

// disable turns remote access off, keeping the tailnet identity: turning it
// on again needs no new login.
func (r *remoteAccess) disable() error {
	r.mu.Lock()
	r.settings.Enabled = false
	err := r.save()
	r.mu.Unlock()
	r.shutdown()
	r.mu.Lock()
	r.setViewLocked(r.baseView())
	r.mu.Unlock()
	return err
}

// errLogoutUnconfirmed: the identity is forgotten, but the coordination
// server did not confirm the logout: the device may still be listed in the
// tailnet, to remove there.
var errLogoutUnconfirmed = errors.New("the tailnet did not confirm the logout")

// logout leaves the tailnet: the device is logged out at the coordination
// server, and its state wiped, with the saved auth key: Eriphe forgets its
// identity. Remote access is off afterwards. When remote access is already
// off, tsnet is started for the time of the logout, without the key.
func (r *remoteAccess) logout(ctx context.Context) error {
	r.mu.Lock()
	ts := r.ts
	r.settings.Enabled, r.settings.Name, r.settings.Joined = false, "", false
	err := r.save()
	r.mu.Unlock()
	lctx, cancel := context.WithTimeout(ctx, remoteLogoutTimeout)
	defer cancel()
	var lerr error
	if ts != nil {
		lerr = ts.Logout(lctx)
	}
	r.shutdown()
	if ts == nil && r.hasState() {
		lerr = r.logoutStopped(lctx)
	}
	if lerr != nil {
		r.logf("remote: logout: %v", lerr)
	}
	if rerr := os.RemoveAll(r.dir); rerr != nil && err == nil {
		err = rerr
	}
	if r.authKey() != "" {
		if kerr := r.forgetKey(); kerr != nil {
			r.logf("remote: logout: the auth key stays in the vault: %v", kerr)
			if err == nil {
				err = kerr
			}
		}
	}
	r.mu.Lock()
	r.setViewLocked(r.baseView())
	r.mu.Unlock()
	r.logf("remote: logged out of the tailnet, state removed")
	if err == nil && lerr != nil {
		err = fmt.Errorf("%w: %v", errLogoutUnconfirmed, lerr)
	}
	return err
}

// hasState tells whether tsnet has an identity saved.
func (r *remoteAccess) hasState() bool {
	_, err := os.Stat(filepath.Join(r.dir, remoteStateFile))
	return err == nil
}

// logoutStopped starts tsnet on the saved state, without the auth key,
// only to log it out at the coordination server.
func (r *remoteAccess) logoutStopped(ctx context.Context) error {
	r.mu.Lock()
	opts := tailnetOptions{Dir: r.dir, Hostname: r.settings.hostname(), ControlURL: r.settings.ControlURL, Logf: r.logf}
	r.mu.Unlock()
	ts, err := r.newTailnet(opts)
	if err != nil {
		return err
	}
	defer ts.Close()
	if err := ts.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	// The backend reads its state first: NoState until then.
	for {
		st, err := ts.Status(ctx)
		if err == nil && st.Backend != "" && st.Backend != "NoState" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.every(phaseStarting)):
		}
	}
	return ts.Logout(ctx)
}

// relogin asks for a new login URL, before the key expires.
func (r *remoteAccess) relogin(ctx context.Context) error {
	r.mu.Lock()
	ts := r.ts
	r.mu.Unlock()
	if ts == nil {
		return errors.New("remote access is off")
	}
	r.mu.Lock()
	r.login = r.now()
	r.mu.Unlock()
	return ts.StartLogin(ctx)
}

// retry restarts tsnet after an error.
func (r *remoteAccess) retry() {
	r.shutdown()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settings.Enabled {
		r.startLocked()
	}
}

// configure changes the hostname and the coordination server. A new
// coordination server is a new identity: the old state is wiped.
func (r *remoteAccess) configure(hostname, controlURL string) error {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	controlURL = strings.TrimSuffix(strings.TrimSpace(controlURL), "/")
	if hostname == "" {
		hostname = defaultRemoteHost
	}
	if !hostnameRe.MatchString(hostname) {
		return errBadHostname
	}
	if controlURL != "" {
		u, err := url.Parse(controlURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return errBadControl
		}
	}
	r.mu.Lock()
	changedControl := controlURL != r.settings.ControlURL
	changed := changedControl || hostname != r.settings.hostname()
	if hostname == defaultRemoteHost {
		hostname = ""
	}
	r.settings.Hostname, r.settings.ControlURL = hostname, controlURL
	if changed {
		r.settings.Name = ""
	}
	if changedControl {
		r.settings.Joined = false
	}
	err := r.save()
	running := r.ts != nil
	r.mu.Unlock()
	if err != nil || !changed {
		return err
	}
	if running {
		r.shutdown()
	}
	if changedControl {
		if err := os.RemoveAll(r.dir); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setViewLocked(r.baseView())
	if r.settings.Enabled {
		r.startLocked()
	}
	return nil
}

var (
	errBadHostname = errors.New("bad hostname")
	errBadControl  = errors.New("bad control URL")
)

// startLocked creates tsnet and the loop that follows it.
func (r *remoteAccess) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.stop, r.done = cancel, done
	r.setViewLocked(remoteView{Phase: phaseStarting})
	opts := tailnetOptions{Dir: r.dir, Hostname: r.settings.hostname(), ControlURL: r.settings.ControlURL, Logf: r.logf}
	// The key logs in a new identity only: with one that has logged in,
	// tsnet has no use for it, and says so at every start.
	if !r.settings.Joined || !r.hasState() {
		opts.AuthKey = r.authKey()
	}
	go r.watch(ctx, done, opts)
}

// shutdown stops the loop, the listeners and tsnet.
func (r *remoteAccess) shutdown() {
	r.mu.Lock()
	stop, done := r.stop, r.done
	r.stop, r.done = nil, nil
	r.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
	r.closeListeners()
	r.mu.Lock()
	ts := r.ts
	r.ts = nil
	r.mu.Unlock()
	if ts != nil {
		if err := ts.Close(); err != nil {
			r.logf("remote: close: %v", err)
		}
	}
}

func (r *remoteAccess) fail(ctx context.Context, err error) {
	r.logf("remote: %v", err)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() == nil {
		r.setViewLocked(remoteView{Phase: phaseError, Error: err.Error()})
	}
}

// watch starts tsnet, then follows its status until ctx ends.
func (r *remoteAccess) watch(ctx context.Context, done chan struct{}, opts tailnetOptions) {
	defer close(done)
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		r.fail(ctx, fmt.Errorf("state directory: %w", err))
		return
	}
	os.Chmod(opts.Dir, 0o700)
	ts, err := r.newTailnet(opts)
	if err == nil {
		r.mu.Lock()
		r.ts = ts
		r.mu.Unlock()
		err = ts.Start()
	}
	if err != nil {
		r.fail(ctx, fmt.Errorf("start: %w", err))
		return
	}
	r.logf("remote: tailscale started as %s", opts.Hostname)
	var https struct {
		enabled   bool
		url, text string
		asked     time.Time
	}
	// The certificate, asked once HTTPS is on, before saying ready: its
	// first issue takes a while, and the first visit would hang meanwhile.
	var cert struct {
		ok, busy bool
		asked    time.Time
	}
	certDone := make(chan error, 1)
	started := r.now()
	for {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		st, err := ts.Status(sctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		v := remoteView{Phase: phaseStarting}
		switch {
		case err != nil:
			v = remoteView{Phase: phaseError, Error: err.Error()}
		case st.Backend == "NeedsLogin" || (st.AuthURL != "" && st.Backend != "Running"):
			v.Phase, v.AuthURL = phaseLogin, st.AuthURL
			// Without a key, tsnet asks for a login URL by itself; after a
			// logout elsewhere, ask again (at most every 30 s).
			if st.AuthURL == "" && r.now().Sub(started) > 3*time.Second && r.now().Sub(r.loginTime()) > 30*time.Second {
				r.setLoginTime(r.now())
				lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := ts.StartLogin(lctx); err != nil {
					r.logf("remote: login: %v", err)
				}
				cancel()
			}
		case st.Backend == "NeedsMachineAuth":
			v.Phase = phaseApproval
		case st.Backend == "Running":
			v.Name, v.Tailnet, v.MagicDNS, v.KeyExpiry, v.AuthURL = st.DNSName, st.Tailnet, st.MagicDNS, st.KeyExpiry, st.AuthURL
			if !st.KeyExpiry.IsZero() {
				left := st.KeyExpiry.Sub(r.now())
				v.Expiring, v.Expired = left < remoteExpiryWarn, left <= 0
			}
			if len(st.CertDomains) == 0 {
				v.Phase = phaseHTTPS
				if !https.enabled && r.now().Sub(https.asked) > 10*time.Second {
					qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
					ok, u, text, err := ts.QueryHTTPS(qctx)
					cancel()
					https.asked = r.now()
					if err != nil {
						r.logf("remote: HTTPS: %v", err)
					} else {
						https.enabled, https.url, https.text = ok, u, text
					}
				}
				v.HTTPSURL, v.HTTPSText = https.url, https.text
				break
			}
			if !cert.ok {
				select {
				case err := <-certDone:
					cert.busy = false
					if err != nil {
						r.logf("remote: certificate: %v", err)
					} else {
						cert.ok = true
					}
				default:
				}
			}
			if !cert.ok && !cert.busy && r.now().Sub(cert.asked) > 10*time.Second {
				cert.busy, cert.asked = true, r.now()
				go func(name string) {
					cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					defer cancel()
					certDone <- ts.PrepareCert(cctx, name)
				}(st.DNSName)
			}
			v.Phase = phaseCert
			if cert.ok {
				v.Phase = phaseReady
			}
		}
		if v.Phase == phaseReady {
			if err := r.listen(ts); err != nil {
				v = remoteView{Phase: phaseError, Error: err.Error()}
			}
		}
		r.mu.Lock()
		v.Listening = len(r.servers) > 0
		if ctx.Err() == nil {
			prev := r.view.Phase
			r.setViewLocked(v)
			if prev != v.Phase {
				r.logf("remote: %s", v.Phase)
			}
			joined := err == nil && st.Backend == "Running"
			if joined && !r.settings.Joined || v.Phase == phaseReady && r.settings.Name != v.Name {
				r.settings.Joined = r.settings.Joined || joined
				if v.Phase == phaseReady {
					r.settings.Name = v.Name
				}
				if err := r.save(); err != nil {
					r.logf("remote: %v", err)
				}
			}
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.every(v.Phase)):
		}
	}
}

func (r *remoteAccess) loginTime() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.login
}

func (r *remoteAccess) setLoginTime(t time.Time) {
	r.mu.Lock()
	r.login = t
	r.mu.Unlock()
}

// listen opens the tailnet listeners, once: :443 for the workspace and
// :8800 for the API, both with the tailnet's certificate, fetched (and
// renewed) by tsnet on each handshake.
func (r *remoteAccess) listen(ts tailnet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.servers) > 0 {
		return nil
	}
	return r.listenLocked(ts)
}

func (r *remoteAccess) listenLocked(ts tailnet) error {
	tlsConfig := func(protos ...string) *tls.Config {
		return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: ts.GetCertificate, NextProtos: protos}
	}
	var opened []*http.Server
	fail := func(err error) error {
		for _, s := range opened {
			s.Close()
		}
		return err
	}
	if r.workspace != nil {
		ln, err := ts.Listen(":443")
		if err != nil {
			return fail(fmt.Errorf("listen :443: %w", err))
		}
		srv := &http.Server{Handler: r.workspace, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			TLSConfig: tlsConfig(), ErrorLog: log.New(tlsNoise{}, "", 0)}
		opened = append(opened, srv)
		go func() {
			if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				r.logf("remote: workspace: %v", err)
			}
		}()
	}
	ln, err := ts.Listen(":" + remoteAPIPort)
	if err != nil {
		return fail(fmt.Errorf("listen :%s: %w", remoteAPIPort, err))
	}
	// HTTP/1.1 only: the nodes' tunnel is an HTTP/1.1 upgrade.
	srv := &http.Server{Handler: r.api, ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(tlsNoise{}, "", 0)}
	opened = append(opened, srv)
	apiTLS := tlsConfig("http/1.1")
	if r.clientCAs != nil {
		apiTLS.ClientAuth, apiTLS.ClientCAs = tls.VerifyClientCertIfGiven, r.clientCAs
	}
	go func() {
		if err := srv.Serve(tls.NewListener(ln, apiTLS)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.logf("remote: api: %v", err)
		}
	}()
	r.servers = opened
	r.logf("remote: serving the workspace and the API on the tailnet")
	return nil
}

func (r *remoteAccess) closeListeners() {
	r.mu.Lock()
	servers := r.servers
	r.servers = nil
	r.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
}

func (r *remoteAccess) restartListeners() {
	r.closeListeners()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ts != nil && r.view.Phase == phaseReady {
		if err := r.listenLocked(r.ts); err != nil {
			r.logf("remote: %v", err)
		}
	}
}

// tailnetHandler is the workspace as the tailnet serves it: only for its
// own name (no other Host, which a DNS rebinding would send), and with HSTS,
// its certificate being one browsers trust.
func tailnetHandler(name func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		want := name()
		host := r.Host
		if h, port, err := net.SplitHostPort(host); err == nil && port == "443" {
			host = h
		}
		if want == "" || !strings.EqualFold(host, want) {
			if want != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				http.Redirect(rw, r, "https://"+want+r.URL.RequestURI(), http.StatusMovedPermanently)
				return
			}
			http.Error(rw, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		rw.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(rw, r)
	})
}

// redactKeys hides Tailscale keys and login links in a log line: whoever
// opens a login link first decides which tailnet Eriphe joins, so it is
// shown in the workspace only.
var (
	tsKeyRe   = regexp.MustCompile(`tskey-[A-Za-z0-9_-]+`)
	tsLoginRe = regexp.MustCompile(`https?://[^\s"']+/a/[A-Za-z0-9]+`)
)

func redactKeys(s string) string {
	s = tsKeyRe.ReplaceAllString(s, "tskey-REDACTED")
	return tsLoginRe.ReplaceAllString(s, "<login link, shown in the workspace>")
}

// errNoRemoteAddress: remote access is off, or was never ready.
var errNoRemoteAddress = errors.New("remote access is off, or not ready yet")

// remoteAddressCommand is `eriphe-core remote-address [-config file | -data
// dir]`: the workspace's address on the tailnet, when remote access is on,
// for the installers to print it when they run again.
func remoteAddressCommand(out io.Writer, args []string) error {
	fs, path, data := authFlags("remote-address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, store, err := authStore(*path, *data)
	if err != nil {
		return err
	}
	defer store.db.Close()
	var s remoteSettings
	if raw := store.get(remoteKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return err
		}
	}
	if !s.Enabled || s.Name == "" {
		return errNoRemoteAddress
	}
	_, err = fmt.Fprintln(out, "https://"+s.Name)
	return err
}

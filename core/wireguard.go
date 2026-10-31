package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/wgnet"
)

// Remote access through a WireGuard network of eriphe-core's own
// (wgnet/): no account anywhere, no relay. eriphe-core is the network's
// server: it listens on one UDP port, which answers nothing without a key
// it knows, and serves the workspace inside the network, at its address
// there (https://10.x.y.1), with a certificate of its own CA (wgnet/ca.go)
// that each device trusts once. The devices use the official WireGuard
// apps: the workspace makes each one's configuration, shown once as a QR
// code. A device reaches the workspace there and nothing else: no other
// device, no network of the host (the stack does not forward).
//
// Off by default; Settings → Remote access turns it on. The devices must
// reach the UDP port: on a VPS it is public already; at home the router
// must forward it to this machine (there is no NAT traversal).

const (
	wgKey         = "wireguard"
	wgDefaultPort = 51820
	wgKeyFile     = "private.key"
	wgMaxPeers    = 250
)

// wgSettings is what the workspace saved, in the kv table. The server's
// private key is a file of its own (0600), as is its CA's.
type wgSettings struct {
	Enabled  bool   `json:"enabled"`
	Network  string `json:"network,omitempty"`  // 10.a.b.0/24, drawn once
	Endpoint string `json:"endpoint,omitempty"` // host[:port] the devices dial
	// WorkspaceOnly: the workspace is not served on its usual port any more,
	// only inside the network (from eriphe-core's next start).
	WorkspaceOnly bool     `json:"workspace_only,omitempty"`
	Peers         []wgPeer `json:"peers,omitempty"`
}

// wgPeer is a device of the network. Its private key was shown once and
// is kept nowhere.
type wgPeer struct {
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	Address   string    `json:"address"`
	Created   time.Time `json:"created"`
}

type wgAccess struct {
	store *Store
	dir   string // <db dir>/wireguard: the server's key and the CA
	port  int    // the UDP port in the container (ERIPHE_WG_PORT)
	now   func() time.Time
	logf  func(format string, args ...any)

	mu        sync.Mutex
	settings  wgSettings
	srv       *wgnet.Server
	ca        *wgnet.CA
	servers   []*http.Server
	workspace http.Handler
	err       string // why it did not start
	onlyAtRun bool   // WorkspaceOnly as it was when the workspace started
}

func newWGAccess(store *Store, dbPath string) *wgAccess {
	a := &wgAccess{store: store, dir: filepath.Join(filepath.Dir(dbPath), "wireguard"), port: wgDefaultPort,
		now: time.Now, logf: log.Printf}
	if p, err := strconv.Atoi(os.Getenv("ERIPHE_WG_PORT")); err == nil && p > 0 && p < 65536 {
		a.port = p
	}
	if raw := store.get(wgKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &a.settings); err != nil {
			log.Printf("wireguard: settings: %v", err)
		}
	}
	return a
}

// wireguardWeb starts the network with the workspace's handler, when it is
// on, and tells whether the workspace is to be served there only. If the
// network does not start, the workspace stays on its usual port: never a
// workspace nobody can reach.
func (c *core) wireguardWeb(cfg *Config, handler http.Handler) (only bool) {
	if c.wg == nil {
		c.wg = newWGAccess(c.store, cfg.DB)
	}
	a := c.wg
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workspace = handler
	if !a.settings.Enabled {
		return false
	}
	if err := a.startLocked(); err != nil {
		a.err = err.Error()
		a.logf("wireguard: %v", err)
		return false
	}
	a.onlyAtRun = a.settings.WorkspaceOnly
	return a.onlyAtRun
}

// storedSettings are the settings as the database has them now (the
// command line may have changed them).
func (a *wgAccess) storedSettings() wgSettings {
	var s wgSettings
	if raw := a.store.get(wgKey); raw != "" {
		json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (a *wgAccess) saveLocked() error {
	raw, _ := json.Marshal(a.settings)
	return a.store.set(wgKey, string(raw))
}

func (a *wgAccess) network() netip.Prefix {
	p, _ := netip.ParsePrefix(a.settings.Network)
	return p
}

// serverAddr is eriphe-core's address in the network, .1.
func (a *wgAccess) serverAddr() netip.Addr {
	p := a.network()
	if !p.IsValid() {
		return netip.Addr{}
	}
	return p.Addr().Next()
}

// newNetwork draws a /24 in 10.0.0.0/8, away from the ranges home routers
// and offices use the most (10.0.x, 10.1.x, 10.10.x…), so that a device's
// own network rarely overlaps it.
func newNetwork() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(200))
	m, _ := rand.Int(rand.Reader, big.NewInt(254))
	return fmt.Sprintf("10.%d.%d.0/24", 40+n.Int64(), 1+m.Int64())
}

// serverKey reads the server's private key, or makes it.
func (a *wgAccess) serverKey() (wgnet.Key, error) {
	path := filepath.Join(a.dir, wgKeyFile)
	if raw, err := os.ReadFile(path); err == nil {
		return wgnet.ParseKey(string(raw))
	} else if !errors.Is(err, os.ErrNotExist) {
		return wgnet.Key{}, err
	}
	k, err := wgnet.NewPrivateKey()
	if err != nil {
		return wgnet.Key{}, err
	}
	return k, os.WriteFile(path, []byte(k.String()+"\n"), 0o600)
}

func (a *wgAccess) peersLocked() []wgnet.Peer {
	var out []wgnet.Peer
	for _, p := range a.settings.Peers {
		k, err1 := wgnet.ParseKey(p.PublicKey)
		ip, err2 := netip.ParseAddr(p.Address)
		if err1 == nil && err2 == nil {
			out = append(out, wgnet.Peer{PublicKey: k, Address: ip})
		}
	}
	return out
}

// startLocked brings the network up and serves the workspace in it.
func (a *wgAccess) startLocked() error {
	if a.srv != nil {
		return nil
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	os.Chmod(a.dir, 0o700)
	key, err := a.serverKey()
	if err != nil {
		return fmt.Errorf("key: %w", err)
	}
	ca, err := wgnet.OpenCA(a.dir, a.network(), a.now)
	if err != nil {
		return fmt.Errorf("certificate authority: %w", err)
	}
	srv, err := wgnet.Start(wgnet.Config{PrivateKey: key, ListenPort: a.port, Address: a.serverAddr(),
		Logf: func(f string, args ...any) { a.logf("wireguard: "+f, args...) }}, a.peersLocked())
	if err != nil {
		return err
	}
	a.srv, a.ca, a.err = srv, ca, ""
	if err := a.listenLocked(); err != nil {
		srv.Close()
		a.srv = nil
		return err
	}
	a.logf("wireguard: listening on UDP %d; the workspace is at https://%s inside the network", srv.ListenPort(), a.serverAddr())
	return nil
}

// listenLocked serves, inside the network only: the workspace on :443 with
// the CA's certificate, and on :80 the page that installs the CA (plain
// HTTP is safe there: WireGuard already authenticates and encrypts it).
func (a *wgAccess) listenLocked() error {
	addr := a.serverAddr()
	var opened []*http.Server
	serve := func(port int, h http.Handler, tlsConfig *tls.Config) error {
		ln, err := a.srv.ListenTCP(port)
		if err != nil {
			return fmt.Errorf("listen :%d: %w", port, err)
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			ErrorLog: log.New(tlsNoise{}, "", 0)}
		if tlsConfig != nil {
			ln = tls.NewListener(ln, tlsConfig)
		}
		opened = append(opened, srv)
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.logf("wireguard: :%d: %v", port, err)
			}
		}()
		return nil
	}
	if a.workspace != nil {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: a.ca.GetCertificate(addr), NextProtos: []string{"h2", "http/1.1"}}
		if err := serve(443, wgHostOnly(addr, a.workspace), cfg); err != nil {
			return err
		}
	}
	if err := serve(80, a.caPage(addr), nil); err != nil {
		for _, s := range opened {
			s.Close()
		}
		return err
	}
	a.servers = opened
	return nil
}

func (a *wgAccess) stopLocked() {
	for _, s := range a.servers {
		s.Close()
	}
	a.servers = nil
	if a.srv != nil {
		a.srv.Close()
		a.srv = nil
	}
}

// wgHostOnly serves the workspace for the network's own names only: no
// other Host, which a DNS rebinding would send.
func wgHostOnly(addr netip.Addr, next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, port, err := net.SplitHostPort(host); err == nil && port == "443" {
			host = h
		}
		if host != addr.String() && !strings.EqualFold(host, wgnet.Name) {
			http.Error(rw, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// caPage is what http://10.x.y.1 serves inside the network: how to trust
// the CA, and its files. Nothing secret: a certificate.
func (a *wgAccess) caPage(addr netip.Addr) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /eriphe.mobileconfig", func(rw http.ResponseWriter, r *http.Request) { a.serveCA(rw, "mobileconfig") })
	mux.HandleFunc("GET /eriphe-ca.crt", func(rw http.ResponseWriter, r *http.Request) { a.serveCA(rw, "crt") })
	mux.HandleFunc("GET /eriphe-ca.pem", func(rw http.ResponseWriter, r *http.Request) { a.serveCA(rw, "pem") })
	mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
		lang := acceptLang(r.Header.Get("Accept-Language"), "fr")
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(rw, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Eriphe</title><style>body{font:16px/1.5 system-ui,sans-serif;max-width:34rem;margin:2rem auto;padding:0 16px}a.b{display:block;margin:.6rem 0;padding:.8rem 1rem;border-radius:12px;background:#5b5bd6;color:#fff;text-decoration:none;font-weight:600}ol{padding-left:1.2rem}code{font-size:.85em;word-break:break-all}</style>
<h1>%s</h1><p>%s</p><ol><li>%s</li><li>%s</li><li>%s</li></ol>
<a class="b" href="/eriphe.mobileconfig">%s</a><a class="b" href="/eriphe-ca.crt">%s</a>
<p>%s <code>%s</code></p><a class="b" href="https://%s/">%s</a></html>`,
			lang, tr(lang, "wg.ca.title"), tr(lang, "wg.ca.lead"), tr(lang, "wg.ca.ios1"), tr(lang, "wg.ca.ios2"), tr(lang, "wg.ca.ios3"),
			tr(lang, "wg.ca.apple"), tr(lang, "wg.ca.other"), tr(lang, "wg.ca.fingerprint"), a.caFingerprint(), addr, tr(lang, "wg.ca.open"))
	})
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, port, err := net.SplitHostPort(host); err == nil && port == "80" {
			host = h
		}
		if host != addr.String() && !strings.EqualFold(host, wgnet.Name) {
			http.Error(rw, "misdirected request", http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(rw, r)
	})
}

func (a *wgAccess) caFingerprint() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ca == nil {
		return ""
	}
	return a.ca.Fingerprint()
}

// serveCA sends the CA in the form a device wants.
func (a *wgAccess) serveCA(rw http.ResponseWriter, form string) {
	a.mu.Lock()
	ca := a.ca
	a.mu.Unlock()
	if ca == nil {
		http.Error(rw, "WireGuard is off", http.StatusNotFound)
		return
	}
	switch form {
	case "mobileconfig":
		rw.Header().Set("Content-Type", "application/x-apple-aspen-config")
		rw.Header().Set("Content-Disposition", `attachment; filename="eriphe.mobileconfig"`)
		rw.Write(ca.MobileConfig())
	case "crt":
		rw.Header().Set("Content-Type", "application/x-x509-ca-cert")
		rw.Header().Set("Content-Disposition", `attachment; filename="eriphe-ca.crt"`)
		rw.Write(ca.DER())
	default:
		rw.Header().Set("Content-Type", "application/x-pem-file")
		rw.Header().Set("Content-Disposition", `attachment; filename="eriphe-ca.pem"`)
		rw.Write(ca.PEM())
	}
}

// enable turns the network on, drawing its addresses the first time.
// endpoint is where the devices will dial, if known.
func (a *wgAccess) enable(endpoint string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings.Network == "" {
		a.settings.Network = newNetwork()
	}
	if a.settings.Endpoint == "" && endpoint != "" {
		if e, err := normEndpoint(endpoint); err == nil {
			a.settings.Endpoint = e
		}
	}
	a.settings.Enabled = true
	if err := a.saveLocked(); err != nil {
		return err
	}
	if err := a.startLocked(); err != nil {
		a.err = err.Error()
		return err
	}
	return nil
}

// disable turns it off; the devices, keys and CA are kept for next time.
// The workspace stays reachable on its usual port: the option of serving it
// only in the network goes off with it.
func (a *wgAccess) disable() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.Enabled, a.settings.WorkspaceOnly = false, false
	a.stopLocked()
	a.err = ""
	return a.saveLocked()
}

var errBadEndpoint = errors.New("bad endpoint")

// normEndpoint is host:port, the default port added, an IPv6 address in brackets.
func normEndpoint(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return "", errBadEndpoint
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = strings.Trim(s, "[]"), strconv.Itoa(wgDefaultPort)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", errBadEndpoint
	}
	if host == "" || strings.ContainsAny(host, " /?#@") {
		return "", errBadEndpoint
	}
	return net.JoinHostPort(host, port), nil
}

func (a *wgAccess) setEndpoint(s string) error {
	e, err := normEndpoint(s)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.Endpoint = e
	return a.saveLocked()
}

var (
	errWGOff      = errors.New("wireguard is off")
	errNoEndpoint = errors.New("no endpoint")
	errFull       = errors.New("no address left")
)

// addPeer makes a device's keys and address, lets it in, and returns its
// configuration, which holds its private key: shown once, kept nowhere.
//
// The key is made here rather than on the device: the official apps can
// make their own, but then the user would have to copy a public key back
// into the workspace by hand; one QR code is the whole setup this way. The
// price is that the private key passes through eriphe-core's memory once,
// which holds everything that key protects anyway.
func (a *wgAccess) addPeer(name string) (wgPeer, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv == nil {
		return wgPeer{}, "", errWGOff
	}
	if a.settings.Endpoint == "" {
		return wgPeer{}, "", errNoEndpoint
	}
	used := map[netip.Addr]bool{a.serverAddr(): true}
	for _, p := range a.settings.Peers {
		if ip, err := netip.ParseAddr(p.Address); err == nil {
			used[ip] = true
		}
	}
	var ip netip.Addr
	for c := a.serverAddr().Next(); a.network().Contains(c) && c != netipLast(a.network()); c = c.Next() {
		if !used[c] {
			ip = c
			break
		}
	}
	if !ip.IsValid() || len(a.settings.Peers) >= wgMaxPeers {
		return wgPeer{}, "", errFull
	}
	key, err := wgnet.NewPrivateKey()
	if err != nil {
		return wgPeer{}, "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = ip.String()
	}
	if len(name) > 60 {
		name = name[:60]
	}
	p := wgPeer{Name: name, PublicKey: key.Public().String(), Address: ip.String(), Created: a.now()}
	a.settings.Peers = append(a.settings.Peers, p)
	if err := a.saveLocked(); err != nil {
		a.settings.Peers = a.settings.Peers[:len(a.settings.Peers)-1]
		return wgPeer{}, "", err
	}
	if err := a.srv.SetPeers(a.peersLocked()); err != nil {
		return wgPeer{}, "", err
	}
	srvKey, err := a.serverKey()
	if err != nil {
		return wgPeer{}, "", err
	}
	conf := wgnet.ClientConfig{PrivateKey: key, Address: ip, ServerKey: srvKey.Public(), ServerAddr: a.serverAddr(),
		Endpoint: a.settings.Endpoint}
	return p, conf.String(), nil
}

// netipLast is the broadcast address of a prefix, never given to a device.
func netipLast(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	bits := p.Bits()
	for i := bits; i < 32; i++ {
		a[i/8] |= 1 << (7 - i%8)
	}
	return netip.AddrFrom4(a)
}

// removePeer revokes a device, at once.
func (a *wgAccess) removePeer(addr string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	before := len(a.settings.Peers)
	a.settings.Peers = slices.DeleteFunc(a.settings.Peers, func(p wgPeer) bool { return p.Address == addr })
	if len(a.settings.Peers) == before {
		return nil
	}
	if err := a.saveLocked(); err != nil {
		return err
	}
	if a.srv != nil {
		return a.srv.SetPeers(a.peersLocked())
	}
	return nil
}

// fromNetwork tells whether a request came through the network: from one
// of its devices, to eriphe-core's address there.
func (a *wgAccess) fromNetwork(r *http.Request) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv == nil {
		return false
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	lap, err := netip.ParseAddrPort(la.String())
	return err == nil && a.network().Contains(ap.Addr().Unmap()) && lap.Addr().Unmap() == a.serverAddr()
}

var errNotThroughWG = errors.New("not through wireguard")

// setWorkspaceOnly serves the workspace in the network only, from the
// next start. Turning it on is allowed from the network only: whoever does
// it has just proven they can still get in afterwards.
func (a *wgAccess) setWorkspaceOnly(on bool, r *http.Request) error {
	if on && !a.fromNetwork(r) {
		return errNotThroughWG
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if on && a.srv == nil {
		return errWGOff
	}
	a.settings.WorkspaceOnly = on
	return a.saveLocked()
}

// wgView is the state the settings page shows.
type wgView struct {
	Enabled       bool
	Running       bool
	Error         string
	Port          int
	Endpoint      string
	Address       string // https://10.x.y.1
	CAPage        string // http://10.x.y.1
	Fingerprint   string
	WorkspaceOnly bool
	OnlyAtRun     bool // in effect now
	Peers         []wgPeerView
}

type wgPeerView struct {
	wgPeer
	LastHandshake time.Time
	Online        bool // a handshake within the last 3 minutes
	Endpoint      string
}

func (a *wgAccess) view() wgView {
	if a == nil {
		return wgView{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v := wgView{Enabled: a.settings.Enabled, Running: a.srv != nil, Error: a.err, Port: a.port, Endpoint: a.settings.Endpoint,
		WorkspaceOnly: a.settings.WorkspaceOnly, OnlyAtRun: a.onlyAtRun}
	if a.srv != nil {
		v.Address, v.CAPage = "https://"+a.serverAddr().String(), "http://"+a.serverAddr().String()
		v.Fingerprint = a.ca.Fingerprint()
	}
	var status map[string]wgnet.PeerStatus
	if a.srv != nil {
		if st, err := a.srv.Status(); err == nil {
			status = map[string]wgnet.PeerStatus{}
			for _, s := range st {
				status[s.PublicKey.String()] = s
			}
		}
	}
	for _, p := range a.settings.Peers {
		pv := wgPeerView{wgPeer: p}
		if s, ok := status[p.PublicKey]; ok {
			pv.LastHandshake, pv.Endpoint = s.LastHandshake, s.Endpoint
			pv.Online = !s.LastHandshake.IsZero() && a.now().Sub(s.LastHandshake) < 3*time.Minute
		}
		v.Peers = append(v.Peers, pv)
	}
	return v
}

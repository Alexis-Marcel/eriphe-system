package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// A task's accesses. An access is what a real program in the container uses
// with a secret (a package registry, an API with a key). The container gets
// a placeholder, and the secret never comes to this node: for the hosts of
// the task's accesses, and those only, the relay ends the TLS session with
// a certificate of the task's own authority and hands each request to
// eriphe-core, which puts the secret in and goes on to the service:
//
//	program → this relay → eriphe-core → the service
//
//	/v1/node/tasks/{id}/access   (node secret)
//
// Every other CONNECT stays the blind tunnel it is (egress.go). So this node
// sees the requests of an access, not its secret. The authority is made for
// the task and held in memory: its key is never in the container, and goes
// with the task. Only its certificate is given to the task, in a bundle
// with the image's own (accessBundleScript): programs that are pointed at
// it still trust the rest of the Internet. A client that refuses the
// certificate fails closed, for that access only.

// TaskAccess is one access lent to a task, as eriphe-core sends it.
type TaskAccess struct {
	Name        string   `json:"name"`
	Hosts       []string `json:"hosts"` // host:port
	Env         string   `json:"env"`   // the variable holding the placeholder
	Placeholder string   `json:"placeholder"`
	SSH         *TaskSSH `json:"ssh,omitempty"` // an SSH access: no hosts, no placeholder (egress_ssh.go)
}

const (
	// accessCAFile is the run file holding the task's authority's certificate.
	accessCAFile = "access-ca.pem"
	// accessBundle is the merged bundle the entrypoint writes: it needs the
	// image's own certificates, which only the container knows of.
	accessBundle = "/tmp/eriphe-ca-bundle.pem"

	accessHostHeader   = "Eriphe-Access-Host"
	accessTargetHeader = "Eriphe-Access-Target"
	accessAuthHeader   = "Eriphe-Access-Authorization"
)

// accessBundleScript writes the merged bundle, before the agent starts: the
// first system bundle found (Debian, Red Hat, Alpine), then the task's
// certificate. Nothing without accesses.
const accessBundleScript = `if [ -n "$ERIPHE_ACCESS_CA" ]; then
  for f in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem; do
    if [ -r "$f" ]; then cat "$f" > ` + accessBundle + `; break; fi
  done
  cat "$ERIPHE_ACCESS_CA" >> ` + accessBundle + `
fi`

// accessCAVars are the variables programs read their authorities from.
var accessCAVars = []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "PIP_CERT"}

// accessEnv is what a task's accesses put in its environment: the
// placeholders, and where its programs find the bundle.
func accessEnv(t *Task) map[string]string {
	if len(t.Spec.Accesses) == 0 {
		return nil
	}
	env := map[string]string{"ERIPHE_ACCESS_CA": runPath(accessCAFile)}
	for _, v := range accessCAVars {
		env[v] = accessBundle
	}
	for _, a := range t.Spec.Accesses {
		if a.Env != "" {
			env[a.Env] = a.Placeholder
		}
	}
	return env
}

// taskCA is a task's certificate authority.
type taskCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// A task may run for days: its certificates outlive it, and its authority
// is gone with it anyway.
const taskCALife = 365 * 24 * time.Hour

func serialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func newTaskCA(taskID string) (*taskCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Eriphe task " + taskID},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(taskCALife), IsCA: true, BasicConstraintsValid: true,
		MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &taskCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		leaves: map[string]*tls.Certificate{}}, nil
}

// leaf is the authority's certificate for a host, a name or an address.
func (a *taskCA) leaf(host string) (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.leaves[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host},
		NotBefore: a.cert.NotBefore, NotAfter: a.cert.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	a.leaves[host] = c
	return c, nil
}

// authority is the task's authority, made at its first use.
func (t *Task) authority() (*taskCA, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ca != nil {
		return t.ca, nil
	}
	ca, err := newTaskCA(t.ID)
	if err == nil {
		t.ca = ca
	}
	return ca, err
}

// withAccessCA adds the task's authority's certificate to its run files.
func withAccessCA(t *Task, files map[string][]byte) (map[string][]byte, error) {
	if len(t.Spec.Accesses) == 0 {
		return files, nil
	}
	ca, err := t.authority()
	if err != nil {
		return nil, err
	}
	if files == nil {
		files = map[string][]byte{}
	}
	files[accessCAFile] = ca.pem
	return files, nil
}

// accessTarget is a CONNECT's target as accesses name it: lowercase, no
// trailing dot.
func accessTarget(hostport string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return ""
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.ToLower(host), "."), port)
}

// isAccess tells whether a CONNECT's target is exactly a host of one of the
// task's accesses.
func (t *egressTask) isAccess(target string) bool {
	return slices.Contains(t.accessHosts, target)
}

// intercept ends the task's TLS session to one of its accesses' hosts, and
// serves what comes through it with p.access.
func (p *egressProxy) intercept(w http.ResponseWriter, t *egressTask, target string) {
	host, _, _ := net.SplitHostPort(target)
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		client.Close()
		return
	}
	session := tls.Server(&bufferedConn{Conn: client, r: buf.Reader}, &tls.Config{
		// HTTP/1.1 only, for now: gRPC does not go through an access.
		NextProtos: []string{"http/1.1"},
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return t.ca.leaf(host)
		},
	})
	one := newOneConn(session)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), accessTaskKey{}, accessCall{t.id, target})
			p.access.ServeHTTP(w, r.WithContext(ctx))
		}),
		ReadHeaderTimeout: time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(io.Discard, "", 0), // a client that refuses the certificate is no news
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				one.Close()
			}
		},
	}
	srv.Serve(one)
}

// bufferedConn reads what was read past a CONNECT first.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// oneConn is a listener of one connection, to serve it with an http.Server.
type oneConn struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newOneConn(c net.Conn) *oneConn {
	o := &oneConn{conns: make(chan net.Conn, 1), done: make(chan struct{})}
	o.conns <- c
	return o
}

func (o *oneConn) Accept() (net.Conn, error) {
	select {
	case c := <-o.conns:
		return c, nil
	case <-o.done:
		return nil, net.ErrClosed
	}
}

func (o *oneConn) Close() error   { o.once.Do(func() { close(o.done) }); return nil }
func (o *oneConn) Addr() net.Addr { return &net.TCPAddr{} }

type accessTaskKey struct{}

// accessCall is whose request this is, and where it was connected to: never
// what the request says.
type accessCall struct{ task, target string }

// accessRelay hands a request of an access to eriphe-core, under this
// node's identity, as the catalog relay (relay.go). The task's own request
// goes in the headers, whatever it holds of this: only what the relay
// writes gets there. It carries a placeholder at most.
func (n *node) accessRelay(s *state) http.Handler {
	return accessRelayTo(s.base, func() (string, string) { return s.Name, s.Secret }, s.relayTransport(), func(id string) bool {
		t := n.get(id)
		return t != nil && !finished(t.state())
	})
}

func accessRelayTo(base func() string, who func() (name, secret string), transport http.RoundTripper, running func(id string) bool) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			call := pr.In.Context().Value(accessTaskKey{}).(accessCall)
			core, err := url.Parse(base())
			if err != nil {
				core = &url.URL{}
			}
			pr.SetURL(core)
			pr.Out.URL.Path = strings.TrimSuffix(core.Path, "/") + "/v1/node/tasks/" + call.task + "/access"
			pr.Out.URL.RawPath, pr.Out.URL.RawQuery = "", ""
			for k := range pr.Out.Header {
				if strings.HasPrefix(k, "Eriphe-") {
					pr.Out.Header.Del(k)
				}
			}
			pr.Out.Header.Del("Authorization")
			if auth := pr.In.Header.Get("Authorization"); auth != "" {
				pr.Out.Header.Set(accessAuthHeader, auth)
			}
			pr.Out.Header.Set(accessHostHeader, call.target)
			pr.Out.Header.Set(accessTargetHeader, pr.In.URL.RequestURI())
			name, secret := who()
			pr.Out.Header.Set("Authorization", "Bearer "+secret)
			pr.Out.Header.Set("Eriphe-Node", name)
		},
		// Whatever the size: nothing is held here.
		FlushInterval: -1,
		Transport:     transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(r.Context().Err(), context.Canceled) {
				log.Printf("access: %v", err)
			}
			http.Error(w, "eriphe-core does not answer", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call, ok := r.Context().Value(accessTaskKey{}).(accessCall); !ok || !running(call.task) {
			http.Error(w, "the task is over", http.StatusGone)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// testCert makes a self-signed certificate for 127.0.0.1 and writes it as a
// CA file the accounts trust.
func testCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, ca
}

// --- IMAP ---

type fakeIMAP struct {
	t     *testing.T
	addr  string
	users map[string]*imapmemserver.User
	pass  map[string]string

	mu     sync.Mutex
	tokens map[string]string // user -> accepted OAuth token
	logins []string          // mechanisms used, in order
}

type imapMode int

const (
	imapTLS      imapMode = iota // implicit TLS
	imapStartTLS                 // plain port offering STARTTLS
	imapPlain                    // plain port without TLS at all
)

func newFakeIMAP(t *testing.T, cert tls.Certificate, mode imapMode, caps imap.CapSet) *fakeIMAP {
	t.Helper()
	f := &fakeIMAP{t: t, users: map[string]*imapmemserver.User{}, pass: map[string]string{}, tokens: map[string]string{}}
	opts := &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &fakeSession{f: f}, nil, nil
		},
		Caps: caps,
	}
	if mode != imapPlain {
		opts.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	if mode == imapPlain {
		// Would accept LOGIN in clear: the client must refuse before sending it.
		opts.InsecureAuth = true
	}
	srv := imapserver.New(opts)
	var ln net.Listener
	var err error
	if mode == imapTLS {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	f.addr = ln.Addr().String()
	return f
}

func (f *fakeIMAP) port() int {
	_, p, _ := net.SplitHostPort(f.addr)
	n, _ := strconv.Atoi(p)
	return n
}

func (f *fakeIMAP) addUser(name, password string, folders ...string) *imapmemserver.User {
	u := imapmemserver.NewUser(name, password)
	for _, m := range append([]string{"INBOX"}, folders...) {
		if err := u.Create(m, nil); err != nil {
			f.t.Fatal(err)
		}
	}
	f.users[name], f.pass[name] = u, password
	return u
}

func (f *fakeIMAP) setToken(user, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[user] = token
}

func (f *fakeIMAP) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, s)
}

func (f *fakeIMAP) loginsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logins...)
}

type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return l.Reader.Size() }

// deliver puts a raw message in a user's folder.
func deliver(t *testing.T, u *imapmemserver.User, folder, raw string, flags ...imap.Flag) {
	t.Helper()
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	if _, err := u.Append(folder, literal{bytes.NewReader([]byte(raw))}, &imap.AppendOptions{Flags: flags}); err != nil {
		t.Fatal(err)
	}
}

type fakeSession struct {
	*imapmemserver.UserSession
	f *fakeIMAP
}

var _ imapserver.SessionIMAP4rev2 = (*fakeSession)(nil)

func (s *fakeSession) Login(username, password string) error {
	s.f.record("LOGIN")
	u, ok := s.f.users[username]
	if !ok || s.f.pass[username] != password {
		return imapserver.ErrAuthFailed
	}
	s.UserSession = imapmemserver.NewUserSession(u)
	return nil
}

func (s *fakeSession) Close() error {
	if s.UserSession != nil {
		return s.UserSession.Close()
	}
	return nil
}

func (s *fakeSession) AuthenticateMechanisms() []string { return []string{"PLAIN", "XOAUTH2"} }

func (s *fakeSession) Authenticate(mech string) (sasl.Server, error) {
	switch mech {
	case "PLAIN":
		return sasl.NewPlainServer(func(_, user, pass string) error { return s.Login(user, pass) }), nil
	case "XOAUTH2":
		s.f.record("XOAUTH2")
		return &xoauth2Server{check: func(user, token string) error {
			s.f.mu.Lock()
			want := s.f.tokens[user]
			s.f.mu.Unlock()
			if want == "" || token != want {
				return imapserver.ErrAuthFailed
			}
			s.UserSession = imapmemserver.NewUserSession(s.f.users[user])
			return nil
		}}, nil
	}
	return nil, errors.New("unsupported mechanism")
}

// xoauth2Server is the server side of XOAUTH2, as Gmail answers it.
type xoauth2Server struct {
	check  func(user, token string) error
	failed error
}

func (x *xoauth2Server) Next(resp []byte) ([]byte, bool, error) {
	if x.failed != nil {
		return nil, true, x.failed
	}
	var user, token string
	for _, part := range strings.Split(string(resp), "\x01") {
		if v, ok := strings.CutPrefix(part, "user="); ok {
			user = v
		}
		if v, ok := strings.CutPrefix(part, "auth=Bearer "); ok {
			token = v
		}
	}
	if err := x.check(user, token); err != nil {
		x.failed = err
		return []byte(`{"status":"401","schemes":"bearer"}`), false, nil
	}
	return nil, true, nil
}

// --- SMTP ---

type sentMail struct {
	From  string
	Rcpts []string
	Data  string
	Mech  string
}

type fakeSMTP struct {
	addr  string
	user  string
	pass  string
	token string

	mu   sync.Mutex
	sent []sentMail
}

func newFakeSMTP(t *testing.T, cert tls.Certificate, implicit bool, user, pass, token string) *fakeSMTP {
	t.Helper()
	f := &fakeSMTP{user: user, pass: pass, token: token}
	s := smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) { return &smtpSession{f: f}, nil }))
	s.Domain = "localhost"
	s.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if implicit {
		ln = tls.NewListener(ln, s.TLSConfig)
	}
	go s.Serve(ln)
	t.Cleanup(func() { s.Close() })
	f.addr = ln.Addr().String()
	return f
}

func (f *fakeSMTP) port() int {
	_, p, _ := net.SplitHostPort(f.addr)
	n, _ := strconv.Atoi(p)
	return n
}

func (f *fakeSMTP) messages() []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMail(nil), f.sent...)
}

type smtpSession struct {
	f    *fakeSMTP
	mech string
	auth bool
	cur  sentMail
}

func (s *smtpSession) AuthMechanisms() []string { return []string{"PLAIN", "XOAUTH2"} }

func (s *smtpSession) Auth(mech string) (sasl.Server, error) {
	s.mech = mech
	switch mech {
	case "PLAIN":
		return sasl.NewPlainServer(func(_, user, pass string) error {
			if user != s.f.user || pass != s.f.pass || s.f.pass == "" {
				return errors.New("bad credentials")
			}
			s.auth = true
			return nil
		}), nil
	case "XOAUTH2":
		return &xoauth2Server{check: func(user, token string) error {
			if user != s.f.user || token != s.f.token || s.f.token == "" {
				return errors.New("bad token")
			}
			s.auth = true
			return nil
		}}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

func (s *smtpSession) Mail(from string, _ *smtp.MailOptions) error {
	if !s.auth {
		return smtp.ErrAuthRequired
	}
	s.cur = sentMail{From: from, Mech: s.mech}
	return nil
}

func (s *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.cur.Rcpts = append(s.cur.Rcpts, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.cur.Data = string(b)
	s.f.mu.Lock()
	s.f.sent = append(s.f.sent, s.cur)
	s.f.mu.Unlock()
	return nil
}

func (s *smtpSession) Reset()        { s.cur = sentMail{} }
func (s *smtpSession) Logout() error { return nil }

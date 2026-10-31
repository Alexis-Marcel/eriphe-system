package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Alexis-Marcel/eriphe-system/core/wgnet"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// wgDevice is a phone: the WireGuard configuration the workspace gave,
// brought up in user space, and a browser behind it.
type wgDevice struct {
	net    *netstack.Net
	server netip.Addr
	hc     *http.Client
}

func newWGDevice(t *testing.T, conf string, roots *x509.CertPool) *wgDevice {
	t.Helper()
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(conf))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), " = "); ok {
			kv[k] = v
		}
	}
	priv, err1 := wgnet.ParseKey(kv["PrivateKey"])
	pub, err2 := wgnet.ParseKey(kv["PublicKey"])
	addr, err3 := netip.ParsePrefix(kv["Address"])
	allowed, err4 := netip.ParsePrefix(kv["AllowedIPs"])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || kv["Endpoint"] == "" {
		t.Fatalf("bad configuration:\n%s", conf)
	}
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr.Addr()}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf})
	if err := dev.IpcSet(fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=%s\nallowed_ip=%s\n",
		hex.EncodeToString(priv[:]), hex.EncodeToString(pub[:]), kv["Endpoint"], allowed)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dev.Close)
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Timeout: 5 * time.Second, Jar: jar,
		Transport:     &http.Transport{DialContext: tnet.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &wgDevice{net: tnet, server: allowed.Addr(), hc: hc}
}

func (d *wgDevice) do(t *testing.T, method, rawURL string, form url.Values, header map[string]string) (int, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, rawURL, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

var dataConf = regexp.MustCompile(`href="data:application/octet-stream;base64,([^"]+)"`)

// From the settings page to a phone in the workspace, through WireGuard.
func TestWireGuardEndToEnd(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	dbPath := filepath.Join(t.TempDir(), "core.db")
	tw.c.wg = newWGAccess(tw.c.store, dbPath)
	tw.c.wg.port = 0 // any free UDP port
	if only := tw.c.wireguardWeb(&Config{DB: dbPath}, tw.w.routes()); only {
		t.Fatal("served through WireGuard only before it was on")
	}
	defer func() { tw.c.wg.mu.Lock(); tw.c.wg.stopLocked(); tw.c.wg.mu.Unlock() }()

	if code, body, _ := tw.do("GET", "/settings/remote/wireguard", nil, nil); code != 200 || !strings.Contains(body, `hx-post="/settings/remote/wireguard/enable"`) {
		t.Fatalf("section when off: %d\n%s", code, body)
	}
	// No endpoint known from 127.0.0.1: adding a device asks for one.
	if code, body, _ := tw.post("/settings/remote/wireguard/enable", url.Values{}, csrf); code != 200 || !strings.Contains(body, "UDP") {
		t.Fatalf("enable: %d\n%s", code, body)
	}
	if _, body, _ := tw.post("/settings/remote/wireguard/peers", url.Values{"name": {"Sam's phone"}}, csrf); !strings.Contains(body, template.HTMLEscapeString(tr("fr", "wg.err.noendpoint"))) {
		t.Fatalf("a device without an endpoint:\n%s", body)
	}
	for _, f := range []string{wgKeyFile, "ca-key.pem"} {
		if fi, err := os.Stat(filepath.Join(tw.c.wg.dir, f)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	port := tw.c.wg.srv.ListenPort()
	if _, body, _ := tw.post("/settings/remote/wireguard/endpoint", url.Values{"endpoint": {fmt.Sprintf("127.0.0.1:%d", port)}}, csrf); strings.Contains(body, `class="alert"`) {
		t.Fatalf("endpoint:\n%s", body)
	}
	code, body, h := tw.post("/settings/remote/wireguard/peers", url.Values{"name": {"Sam's phone"}}, csrf)
	m := dataConf.FindStringSubmatch(body)
	if code != 200 || m == nil || !strings.Contains(body, `class="qr"`) || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("add a device: %d %v\n%s", code, h, body)
	}
	conf, _ := base64.StdEncoding.DecodeString(m[1])
	v := tw.c.wg.view()
	if len(v.Peers) != 1 || strings.Contains(tw.c.store.get(wgKey), "PrivateKey") {
		t.Fatalf("peers: %+v", v.Peers)
	}
	if bytes.Contains(conf, []byte("0.0.0.0/0")) || !bytes.Contains(conf, []byte("AllowedIPs = "+v.Address[len("https://"):]+"/32")) {
		t.Fatalf("not a split tunnel to the workspace only:\n%s", conf)
	}

	// The phone, without the CA yet: the CA page over plain HTTP in the tunnel.
	phone := newWGDevice(t, string(conf), nil)
	srv := phone.server.String()
	code, page := phone.do(t, "GET", "http://"+srv+"/", nil, nil)
	if code != 200 || !strings.Contains(page, v.Fingerprint) || !strings.Contains(page, "/eriphe.mobileconfig") {
		t.Fatalf("CA page: %d\n%s", code, page)
	}
	code, mc := phone.do(t, "GET", "http://"+srv+"/eriphe.mobileconfig", nil, nil)
	if code != 200 || !strings.Contains(mc, "com.apple.security.root") {
		t.Fatalf("profile: %d", code)
	}
	_, der := phone.do(t, "GET", "http://"+srv+"/eriphe-ca.crt", nil, nil)
	ca, err := x509.ParseCertificate([]byte(der))
	if err != nil {
		t.Fatalf("CA certificate: %v", err)
	}
	// Untrusted, the workspace's certificate is refused; trusted, it is accepted.
	if code, msg := phone.do(t, "GET", "https://"+srv+"/login", nil, nil); code != 0 || !strings.Contains(msg, "certificate") {
		t.Fatalf("without the CA: %d %s", code, msg)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	phone.hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	phone.hc.Transport.(*http.Transport).CloseIdleConnections()
	code, login := phone.do(t, "GET", "https://"+srv+"/login", nil, nil)
	tok := csrfField.FindStringSubmatch(login)
	if code != 200 || tok == nil {
		t.Fatalf("login page through WireGuard: %d\n%s", code, login)
	}
	if code, _ := phone.do(t, "GET", "https://"+srv+"/login", nil, map[string]string{}); code != 200 {
		t.Fatal(code)
	}
	req, _ := http.NewRequest("GET", "https://"+srv+"/login", nil)
	req.Host = "rebind.example.org"
	if resp, err := phone.hc.Do(req); err != nil || resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("another Host: %v %v", resp, err)
	}

	// Signed in through the tunnel, the phone may serve the workspace there
	// only; the browser on the local network may not.
	if code, _ := phone.do(t, "POST", "https://"+srv+"/login", url.Values{"csrf": {tok[1]}, "password": {testPassword}}, nil); code != http.StatusSeeOther {
		t.Fatalf("login through WireGuard: %d", code)
	}
	if _, body, _ := tw.post("/settings/remote/wireguard/only", url.Values{"on": {"1"}}, csrf); !strings.Contains(body, template.HTMLEscapeString(tr("fr", "wg.err.notwg"))) {
		t.Fatalf("workspace-only from the LAN:\n%s", body)
	}
	_, acct := phone.do(t, "GET", "https://"+srv+"/settings/account", nil, nil)
	pcsrf := csrfMeta.FindStringSubmatch(acct)
	if pcsrf == nil {
		t.Fatal("no session through WireGuard")
	}
	hx := map[string]string{"X-CSRF-Token": pcsrf[1], "HX-Request": "true", "Origin": "https://" + srv}
	if code, body := phone.do(t, "POST", "https://"+srv+"/settings/remote/wireguard/only", url.Values{"on": {"1"}}, hx); code != 200 || strings.Contains(body, `class="alert"`) {
		t.Fatalf("workspace-only through WireGuard: %d\n%s", code, body)
	}
	if !tw.c.wg.view().WorkspaceOnly {
		t.Fatal("workspace-only not saved")
	}
	var out bytes.Buffer
	// The way back from the machine.
	if err := wireguardCLIStore(tw.c.store, []string{"open"}, &out); err != nil || tw.c.wg.storedSettings().WorkspaceOnly {
		t.Fatalf("wireguard open: %v %s", err, out.String())
	}

	// Revoked: the phone is out at once.
	if _, body, _ := tw.post("/settings/remote/wireguard/peers/"+v.Peers[0].Address+"/revoke", url.Values{}, csrf); !strings.Contains(body, template.HTMLEscapeString(tr("fr", "wg.peer.none"))) {
		t.Fatalf("revoke:\n%s", body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if c, err := phone.net.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(phone.server, 443)); err == nil {
		c.Close()
		t.Fatal("a revoked phone still got in")
	}
}

func TestNormEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7": "203.0.113.7:51820", "eriphe.example.org:4500": "eriphe.example.org:4500",
		"https://eriphe.example.org/": "eriphe.example.org:51820", "2001:db8::1": "[2001:db8::1]:51820",
		"[2001:db8::1]:999": "[2001:db8::1]:999", "": "", "a b": "", "host:99999": "", "user@host": "",
	} {
		got, err := normEndpoint(in)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("normEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestEndpointGuess(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7:8443": "203.0.113.7", "eriphe.example.org:8443": "eriphe.example.org", "[2001:db8::1]:8443": "2001:db8::1",
		"192.168.1.20:8443": "", "10.0.0.2": "", "localhost:8443": "", "nas:8443": "", "nas.local:8443": "", "eriphe.tail1234.ts.net": "",
		"[fd00::1]:8443": "", "100.100.1.1:8443": "",
	} {
		if got := endpointGuess(in); got != want {
			t.Errorf("endpointGuess(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNetipLast(t *testing.T) {
	if got := netipLast(netip.MustParsePrefix("10.40.7.0/24")); got != netip.MustParseAddr("10.40.7.255") {
		t.Fatal(got)
	}
	if n, err := netip.ParsePrefix(newNetwork()); err != nil || n.Bits() != 24 || !netip.MustParsePrefix("10.0.0.0/8").Contains(n.Addr()) {
		t.Fatal(n, err)
	}
}

package wgnet

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func mustKey(t *testing.T) Key {
	t.Helper()
	k, err := NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// The public key of RFC 7748's test vector (Alice's).
func TestPublicKey(t *testing.T) {
	priv, _ := ParseKey(b64hex("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a"))
	want := b64hex("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	if got := priv.Public().String(); got != want {
		t.Fatalf("public = %s, want %s", got, want)
	}
	k := mustKey(t)
	if k[0]&7 != 0 || k[31]&128 != 0 || k[31]&64 == 0 {
		t.Fatalf("key not clamped: %x", k)
	}
	back, err := ParseKey(k.String())
	if err != nil || back != k {
		t.Fatalf("ParseKey(String) = %v, %v", back, err)
	}
	if _, err := ParseKey("short"); err == nil {
		t.Fatal("a bad key parsed")
	}
}

func b64hex(h string) string {
	b := make([]byte, len(h)/2)
	fmt.Sscanf(h, "%x", &b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestClientConfig(t *testing.T) {
	c := ClientConfig{PrivateKey: Key{1}, Address: netip.MustParseAddr("10.9.8.2"), ServerKey: Key{2},
		ServerAddr: netip.MustParseAddr("10.9.8.1"), Endpoint: "[2001:db8::1]:51820", Keepalive: 25}
	got := c.String()
	for _, want := range []string{"[Interface]\nPrivateKey = " + (Key{1}).String(), "Address = 10.9.8.2/32",
		"[Peer]\nPublicKey = " + (Key{2}).String(), "AllowedIPs = 10.9.8.1/32", "Endpoint = [2001:db8::1]:51820", "PersistentKeepalive = 25"} {
		if !strings.Contains(got, want) {
			t.Errorf("config misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "DNS") {
		t.Errorf("config sets a DNS server:\n%s", got)
	}
}

func TestParseStatus(t *testing.T) {
	k := Key{7}
	raw := "private_key=00\nlisten_port=51820\npublic_key=" + k.hex() + "\nendpoint=192.0.2.4:4242\nlast_handshake_time_sec=1700000000\nlast_handshake_time_nsec=5\nrx_bytes=10\ntx_bytes=20\npublic_key=" + (Key{8}).hex() + "\nlast_handshake_time_sec=0\nlast_handshake_time_nsec=0\n"
	st := parseStatus(raw)
	if len(st) != 2 || st[0].PublicKey != k || st[0].Endpoint != "192.0.2.4:4242" || st[0].RxBytes != 10 || st[0].TxBytes != 20 ||
		!st[0].LastHandshake.Equal(time.Unix(1700000000, 5)) || !st[1].LastHandshake.IsZero() {
		t.Fatalf("status = %+v", st)
	}
}

// client is a device: a WireGuard peer over its own user-space stack.
type client struct {
	dev *device.Device
	net *netstack.Net
}

func newClient(t *testing.T, priv Key, addr netip.Addr, server Key, port int, allowed string) *client {
	t.Helper()
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf})
	cfg := fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=127.0.0.1:%d\nallowed_ip=%s\n", priv.hex(), server.hex(), port, allowed)
	if err := dev.IpcSet(cfg); err != nil {
		t.Fatal(err)
	}
	if err := dev.Up(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dev.Close)
	return &client{dev: dev, net: tnet}
}

func (c *client) get(url string, timeout time.Duration) (string, error) {
	hc := &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: c.net.DialContext}}
	resp, err := hc.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// serve answers "hello <remote address>" on a listener.
func serve(t *testing.T, ln net.Listener) {
	t.Helper()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello "+r.RemoteAddr)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

// End to end, in one process and without privileges: a device reaches the
// server's TCP port through WireGuard, and nothing else.
func TestEndToEnd(t *testing.T) {
	serverKey, aKey, bKey := mustKey(t), mustKey(t), mustKey(t)
	srvAddr, aAddr, bAddr := netip.MustParseAddr("10.9.8.1"), netip.MustParseAddr("10.9.8.2"), netip.MustParseAddr("10.9.8.3")
	peers := []Peer{{aKey.Public(), aAddr}, {bKey.Public(), bAddr}}
	s, err := Start(Config{PrivateKey: serverKey, ListenPort: 0, Address: srvAddr}, peers)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	port := s.ListenPort()
	if port == 0 {
		t.Fatal("no listen port")
	}
	ln, err := s.ListenTCP(8080)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, ln)

	// A device with a route to everything, to try more than the server.
	a := newClient(t, aKey, aAddr, serverKey.Public(), port, "0.0.0.0/0")
	got, err := a.get("http://10.9.8.1:8080/", 5*time.Second)
	if err != nil || !strings.HasPrefix(got, "hello 10.9.8.2:") {
		t.Fatalf("through the tunnel: %q, %v", got, err)
	}
	st, _ := s.Status()
	if len(st) != 2 || st[0].LastHandshake.IsZero() || st[0].RxBytes == 0 {
		t.Fatalf("status after a handshake: %+v", st)
	}

	// The server's stack does not forward: not to another peer, nor to the
	// host (a listener on its loopback), nor to a closed port of its own.
	b := newClient(t, bKey, bAddr, serverKey.Public(), port, "10.9.8.1/32")
	bln, err := b.net.ListenTCP(&net.TCPAddr{IP: bAddr.AsSlice(), Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	serve(t, bln)
	if got, err := a.get("http://10.9.8.3:8080/", 2*time.Second); err == nil {
		t.Fatalf("a peer reached another peer: %q", got)
	}
	host, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serve(t, host)
	if got, err := a.get("http://"+host.Addr().String()+"/", 2*time.Second); err == nil {
		t.Fatalf("a peer reached the host's loopback: %q", got)
	}
	if _, err := a.get("http://10.9.8.1:9999/", 2*time.Second); err == nil {
		t.Fatal("a closed port answered")
	}

	// A stranger with the server's public key but an unknown key of its own
	// gets nothing.
	stranger := newClient(t, mustKey(t), netip.MustParseAddr("10.9.8.9"), serverKey.Public(), port, "10.9.8.1/32")
	if _, err := stranger.get("http://10.9.8.1:8080/", 2*time.Second); err == nil {
		t.Fatal("an unknown key got in")
	}

	// A peer that claims another one's address is dropped (allowed IPs).
	spoof := newClient(t, aKey, bAddr, serverKey.Public(), port, "10.9.8.1/32")
	_ = spoof // a's key, b's address: the server drops its packets
	if _, err := spoof.get("http://10.9.8.1:8080/", 2*time.Second); err == nil {
		t.Fatal("a spoofed source address got in")
	}

	// Revoked: the device is out at once.
	if err := s.SetPeers(peers[1:]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if c, err := a.net.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(srvAddr, 8080)); err == nil {
		c.Close()
		t.Fatal("a revoked device still got in")
	}
	// And the other one is still in.
	if got, err := b.get("http://10.9.8.1:8080/", 5*time.Second); err != nil || !strings.HasPrefix(got, "hello 10.9.8.3:") {
		t.Fatalf("the remaining device: %q, %v", got, err)
	}
}

// Package wgnet is a WireGuard endpoint that lives entirely in eriphe-core's
// process: wireguard-go in user space over a gVisor network stack
// (tun/netstack). It needs no root, no kernel module and no TUN device,
// so it runs in an unprivileged container; the only thing the host sees
// is a UDP socket, which answers nothing to a packet without a known key.
//
// The stack has a single address, the server's, and forwarding is off:
// a peer reaches the TCP ports eriphe-core listens on there and nothing
// else, neither the other peers nor the networks of the host.
package wgnet

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Key is a Curve25519 key, private or public.
type Key [32]byte

// NewPrivateKey makes a random private key, clamped as WireGuard wants.
func NewPrivateKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k, nil
}

// Public is the public key of a private key.
func (k Key) Public() Key {
	var pub Key
	out, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil { // only for a low-order point, which a clamped key never gives
		return Key{}
	}
	copy(pub[:], out)
	return pub
}

// String is the key in base64, as WireGuard's configuration files have it.
func (k Key) String() string { return base64.StdEncoding.EncodeToString(k[:]) }

// IsZero tells whether the key is unset.
func (k Key) IsZero() bool { return k == Key{} }

func (k Key) hex() string { return hex.EncodeToString(k[:]) }

// ParseKey reads a base64 key.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != len(k) {
		return Key{}, errors.New("wgnet: bad key")
	}
	copy(k[:], b)
	return k, nil
}

// Peer is a device allowed in: its public key and its one address.
type Peer struct {
	PublicKey Key
	Address   netip.Addr
}

// PeerStatus is what WireGuard knows of a peer.
type PeerStatus struct {
	PublicKey     Key
	Endpoint      string // where its last packets came from
	LastHandshake time.Time
	RxBytes       int64
	TxBytes       int64
}

// Config is the server's side of the network.
type Config struct {
	PrivateKey Key
	ListenPort int        // UDP, on every address of the host
	Address    netip.Addr // the server's address in the network
	MTU        int        // 0: 1420
	Logf       func(format string, args ...any)
}

// Server is a running WireGuard endpoint and its network stack.
type Server struct {
	dev  *device.Device
	net  *netstack.Net
	addr netip.Addr

	mu     sync.Mutex
	closed bool
}

// Start brings the endpoint up with its peers.
func Start(cfg Config, peers []Peer) (*Server, error) {
	if cfg.PrivateKey.IsZero() || !cfg.Address.IsValid() {
		return nil, errors.New("wgnet: a private key and an address are needed")
	}
	mtu := cfg.MTU
	if mtu == 0 {
		mtu = 1420
	}
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{cfg.Address}, nil, mtu)
	if err != nil {
		return nil, fmt.Errorf("wgnet: network stack: %w", err)
	}
	logf := cfg.Logf
	if logf == nil {
		logf = device.DiscardLogf
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), &device.Logger{Verbosef: device.DiscardLogf, Errorf: logf})
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\n", cfg.PrivateKey.hex(), cfg.ListenPort)
	writePeers(&b, peers)
	if err := dev.IpcSet(b.String()); err != nil {
		dev.Close()
		return nil, fmt.Errorf("wgnet: configure: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("wgnet: up: %w", err)
	}
	return &Server{dev: dev, net: tnet, addr: cfg.Address}, nil
}

// writePeers replaces the peers with these, each with its one address.
func writePeers(b *strings.Builder, peers []Peer) {
	b.WriteString("replace_peers=true\n")
	for _, p := range peers {
		if p.PublicKey.IsZero() || !p.Address.IsValid() {
			continue
		}
		fmt.Fprintf(b, "public_key=%s\nreplace_allowed_ips=true\nallowed_ip=%s\n",
			p.PublicKey.hex(), netip.PrefixFrom(p.Address, p.Address.BitLen()))
	}
}

// ListenPort is the UDP port the endpoint listens on (the one asked, or
// the one the system chose for 0).
func (s *Server) ListenPort() int {
	raw, err := s.dev.IpcGet()
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(raw, "\n") {
		if v, ok := strings.CutPrefix(l, "listen_port="); ok {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

// Address is the server's address in the network.
func (s *Server) Address() netip.Addr { return s.addr }

// ListenTCP listens on a TCP port of the server's address, inside the network.
func (s *Server) ListenTCP(port int) (net.Listener, error) {
	return s.net.ListenTCPAddrPort(netip.AddrPortFrom(s.addr, uint16(port)))
}

// SetPeers replaces the peers: a peer left out can no longer reach the
// server, at once (its session keys are dropped with it).
func (s *Server) SetPeers(peers []Peer) error {
	var b strings.Builder
	writePeers(&b, peers)
	return s.dev.IpcSet(b.String())
}

// Status is what WireGuard knows of each peer.
func (s *Server) Status() ([]PeerStatus, error) {
	raw, err := s.dev.IpcGet()
	if err != nil {
		return nil, err
	}
	return parseStatus(raw), nil
}

func parseStatus(raw string) []PeerStatus {
	var out []PeerStatus
	var cur *PeerStatus
	var sec, nsec int64
	flush := func() {
		if cur != nil {
			if sec != 0 || nsec != 0 {
				cur.LastHandshake = time.Unix(sec, nsec)
			}
			out = append(out, *cur)
		}
		cur, sec, nsec = nil, 0, 0
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		if k == "public_key" {
			flush()
			cur = &PeerStatus{}
			if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
				copy(cur.PublicKey[:], b)
			}
			continue
		}
		if cur == nil {
			continue
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
		case "endpoint":
			cur.Endpoint = v
		case "last_handshake_time_sec":
			sec = n
		case "last_handshake_time_nsec":
			nsec = n
		case "rx_bytes":
			cur.RxBytes = n
		case "tx_bytes":
			cur.TxBytes = n
		}
	}
	flush()
	return out
}

// Close stops the endpoint: its UDP socket, its stack and every listener on it.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.dev.Close()
	return nil
}

// ClientConfig is a device's wg-quick configuration: its own key and
// address, and the server as its only peer, for the server's address only
// (a split tunnel: the device's other traffic does not go through it).
type ClientConfig struct {
	PrivateKey Key
	Address    netip.Addr
	ServerKey  Key
	ServerAddr netip.Addr
	Endpoint   string // host:port the device dials
	Keepalive  int    // seconds, 0 for none
}

// String renders the configuration as the WireGuard apps import it.
func (c ClientConfig) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s\n\n", c.PrivateKey, netip.PrefixFrom(c.Address, c.Address.BitLen()))
	fmt.Fprintf(&b, "[Peer]\nPublicKey = %s\nAllowedIPs = %s\nEndpoint = %s\n", c.ServerKey,
		netip.PrefixFrom(c.ServerAddr, c.ServerAddr.BitLen()), c.Endpoint)
	if c.Keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", c.Keepalive)
	}
	return b.String()
}

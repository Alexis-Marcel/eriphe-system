package main

import (
	"bufio"
	"encoding/hex"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Where a connection comes from, as far as eriphe-core can tell.
//
// In a container, a connection that went through Docker's userland proxy
// (docker-proxy) arrives from the bridge's gateway, whoever made it: the
// host's own localhost, a VM's port forwarder (Lima, WSL's), or any client
// on the internet when a port is published on IPv6 ([::]:8443) and the
// network has no IPv6. The gateway's address is private, yet it may stand
// for anyone: it is never "local", and the login limiter counts it apart
// (web_auth.go). The installers publish the ports on IPv4 only, so that
// clients keep their own address (install/README.md, Eriphe on a VPS).

// gatewayAddrs are the default gateways of this machine's routing table
// (in a container: the bridge's), read once; tests set them.
var gatewayAddrs = sync.OnceValue(func() []netip.Addr {
	var out []netip.Addr
	if f, err := os.Open("/proc/net/route"); err == nil {
		out = append(out, routeGateways4(f)...)
		f.Close()
	}
	if f, err := os.Open("/proc/net/ipv6_route"); err == nil {
		out = append(out, routeGateways6(f)...)
		f.Close()
	}
	return out
})

// routeGateways4 reads /proc/net/route: the gateways of its default routes
// (destination 0, a gateway set), in little-endian hex.
func routeGateways4(r io.Reader) []netip.Addr {
	var out []netip.Addr
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[1] != "00000000" || f[2] == "00000000" {
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		out = append(out, netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}))
	}
	return out
}

// routeGateways6 reads /proc/net/ipv6_route: the next hops of its default
// routes (::/0).
func routeGateways6(r io.Reader) []netip.Addr {
	var out []netip.Addr
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != strings.Repeat("0", 32) || f[1] != "00" {
			continue
		}
		b, err := hex.DecodeString(f[4])
		if err != nil || len(b) != 16 {
			continue
		}
		a := netip.AddrFrom16([16]byte(b))
		if !a.IsUnspecified() {
			out = append(out, a)
		}
	}
	return out
}

// viaGateway tells whether a is this machine's default gateway: a
// connection from it may have been relayed for anyone.
func viaGateway(a netip.Addr) bool {
	a = a.Unmap()
	for _, g := range gatewayAddrs() {
		if g == a {
			return true
		}
	}
	return false
}

// clearFromInternet tells whether r came over plain HTTP from a public
// address. The API's token (and Home Assistant's) then crossed the internet
// in clear: the call is refused, so that such a setup fails at once rather
// than leaking the token at every call. Plain HTTP stays for the local
// network, the tailnet and the machine itself (and the gateway, which may be
// the machine itself: client_addr.go); TLS works from anywhere.
func clearFromInternet(r *http.Request) bool {
	if r.TLS != nil {
		return false
	}
	a, ok := hostAddr(r.RemoteAddr)
	return ok && publicAddr(a)
}

const errClearFromInternet = "plain HTTP from the internet is refused: the token would cross it in clear. " +
	"Use https:// (the tailnet address of Settings › Remote access, or your own domain behind a reverse proxy), " +
	"or call from the local network"

// clearToInternet tells whether raw is a plain http:// address on a public
// IP: what is sent there crosses the internet in clear, and a server that
// answers there answers anyone. A name cannot be told without resolving
// it: the form's hint covers it.
func clearToInternet(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	a, ok := hostAddr(u.Hostname())
	return ok && publicAddr(a)
}

// publicAddr tells whether a is a known public address: neither local
// (localAddr) nor the gateway's, nor unspecified.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && !a.IsUnspecified() && !localAddr(a) && !viaGateway(a) && !a.IsMulticast()
}

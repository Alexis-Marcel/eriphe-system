package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// withGateway makes addrs this machine's default gateways for the test.
func withGateway(t *testing.T, addrs ...string) {
	t.Helper()
	old := gatewayAddrs
	var as []netip.Addr
	for _, s := range addrs {
		as = append(as, netip.MustParseAddr(s))
	}
	gatewayAddrs = func() []netip.Addr { return as }
	t.Cleanup(func() { gatewayAddrs = old })
}

func TestRouteGateways(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t010013AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t000013AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	if got := routeGateways4(strings.NewReader(route)); len(got) != 1 || got[0] != netip.MustParseAddr("172.19.0.1") {
		t.Fatalf("IPv4 gateways: %v", got)
	}
	route6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 20010db8000000000000000000000001 00000400 00000000 00000000 00000003 eth0\n" +
		"20010db8000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000000 00000000 00000001 eth0\n" +
		"00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo\n"
	if got := routeGateways6(strings.NewReader(route6)); len(got) != 1 || got[0] != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("IPv6 gateways: %v", got)
	}
}

// Docker's userland proxy shows every client it relays (IPv6 ones on a
// network without IPv6, the host's own localhost) with the bridge's
// gateway: that address is not the local network.
func TestGatewayIsNotLocal(t *testing.T) {
	withGateway(t, "172.19.0.1")
	if fromLocalNetwork(&http.Request{RemoteAddr: "172.19.0.1:40000", Header: http.Header{}}) {
		t.Error("the gateway counts as the local network")
	}
	if fromLocalNetwork(&http.Request{RemoteAddr: "[::ffff:172.19.0.1]:40000", Header: http.Header{}}) {
		t.Error("the gateway, mapped, counts as the local network")
	}
	if !fromLocalNetwork(&http.Request{RemoteAddr: "172.19.0.5:40000", Header: http.Header{}}) {
		t.Error("another address of the bridge is local")
	}
	if publicAddr(netip.MustParseAddr("172.19.0.1")) || publicAddr(netip.MustParseAddr("192.168.1.20")) ||
		!publicAddr(netip.MustParseAddr("203.0.113.9")) || !publicAddr(netip.MustParseAddr("2001:db8::9")) {
		t.Error("publicAddr")
	}
}

// Whoever comes through the gateway counts together, apart from the
// others, with a larger allowance: a few bad tries there lock nobody out.
func TestLoginLimiterGateway(t *testing.T) {
	withGateway(t, "172.19.0.1")
	l := newLoginLimiter()
	now := time.Now()
	for i := 0; i < l.perIP; i++ {
		if !l.begin("172.19.0.1", now) {
			t.Fatalf("refused after %d failures through the gateway", i)
		}
		l.end("172.19.0.1", now, false)
	}
	if !l.begin("172.19.0.1", now) {
		t.Fatal("five failures through the gateway lock it")
	}
	l.end("172.19.0.1", now, false)
	for i := l.perIP + 1; i < l.perGate; i++ {
		l.begin("172.19.0.1", now)
		l.end("172.19.0.1", now, false)
	}
	if l.begin("172.19.0.1", now) {
		t.Fatal("the gateway has no limit")
	}
	if !l.begin("192.168.1.20", now) {
		t.Fatal("the gateway's failures count against another address")
	}
	l.end("192.168.1.20", now, true)
}

// The API's token over plain HTTP from the internet is refused; from the
// local network, the gateway and over TLS it goes through.
func TestBearerRefusesClearFromInternet(t *testing.T) {
	withGateway(t, "172.19.0.1")
	h := bearer("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	call := func(addr string, tlsOn bool) int {
		r := httptest.NewRequest("GET", "/v1/version", nil)
		r.RemoteAddr = addr
		r.Header.Set("Authorization", "Bearer secret")
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, c := range []struct {
		addr string
		tls  bool
		want int
	}{
		{"203.0.113.9:5000", false, http.StatusForbidden},
		{"[2001:db8::9]:5000", false, http.StatusForbidden},
		{"203.0.113.9:5000", true, http.StatusNoContent},
		{"192.168.1.20:5000", false, http.StatusNoContent},
		{"100.92.1.2:5000", false, http.StatusNoContent},
		{"127.0.0.1:5000", false, http.StatusNoContent},
		{"172.19.0.1:5000", false, http.StatusNoContent},
	} {
		if got := call(c.addr, c.tls); got != c.want {
			t.Errorf("%s (TLS %t): %d, want %d", c.addr, c.tls, got, c.want)
		}
	}
}

// No sign-in can be chosen from the local network, not from an address
// that may be anyone's.
func TestSigninNoneOnlyFromLocalNetwork(t *testing.T) {
	tw := newTestWeb(t)
	csrf := tw.setup()
	none := url.Values{"mode": {modeNone}}
	// The test server is on the loopback: made the gateway, it is anyone.
	withGateway(t, "127.0.0.1")
	if _, body, _ := tw.post("/settings/signin", none, csrf); !strings.Contains(body, "auth none") || tw.w.signin().cfg.mode() != modePassword {
		t.Fatalf("none from an unknown address: %s", body)
	}
	if _, body, _ := tw.do("GET", "/settings/account", nil, nil); !strings.Contains(body, "eriphe-core auth none") {
		t.Fatal("the page does not say why")
	}
	withGateway(t)
	if _, body, _ := tw.post("/settings/signin", none, csrf); !strings.Contains(body, "enregistrée") || tw.w.signin().cfg.mode() != modeNone {
		t.Fatalf("none from the local network: %s", body)
	}
}

// eriphe-core's MCP server takes the API token or an assistant's, derived
// from it; nothing else does, and an empty token opens nothing.
func TestBearerAnyAssistantToken(t *testing.T) {
	h := bearerAny(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		"secret", assistantToken("secret"))
	for tok, want := range map[string]int{"secret": http.StatusNoContent, assistantToken("secret"): http.StatusNoContent,
		assistantToken("other"): http.StatusUnauthorized, "": http.StatusUnauthorized, "wrong": http.StatusUnauthorized} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("%q: %d, want %d", tok, rec.Code, want)
		}
	}
	if assistantToken("secret") == "secret" || assistantToken("") != "" {
		t.Error("the assistant's token is the API token")
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The installers' MAC addresses are read in any usual form; a wrong one stops the node.
func TestParseMACs(t *testing.T) {
	got, err := parseMACs("00-00-5E-00-53-01, 00:00:5e:00:53:02,")
	if err != nil || !slices.Equal(got, []string{"00:00:5e:00:53:01", "00:00:5e:00:53:02"}) {
		t.Fatalf("parseMACs: %v %v", got, err)
	}
	for _, bad := range []string{"00:00:5e:00:53", "nope", "00:00:5e:00:53:01:02:03"} {
		if _, err := parseMACs(bad); err == nil {
			t.Errorf("parseMACs(%q): no error", bad)
		}
	}
	if b, err := parseBroadcast(" 192.0.2.255 "); err != nil || b != "192.0.2.255" {
		t.Fatalf("parseBroadcast: %q %v", b, err)
	}
	if _, err := parseBroadcast("2001:db8::1"); err == nil {
		t.Fatal("parseBroadcast takes IPv6")
	}
}

// A magic packet is six 0xff, then the MAC address 16 times.
func TestMagicPacket(t *testing.T) {
	p, err := magicPacket("00:00:5e:00:53:01")
	if err != nil {
		t.Fatal(err)
	}
	mac := []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01}
	want := append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(mac, 16)...)
	if !bytes.Equal(p, want) || len(p) != 102 {
		t.Fatalf("magic packet: % x", p)
	}
	if _, err := magicPacket("00:00:5e"); err == nil {
		t.Fatal("a short MAC address makes a packet")
	}
}

func TestWakeTargets(t *testing.T) {
	got := wakeTargets("192.0.2.255")
	want := []string{"255.255.255.255:9", "255.255.255.255:7", "192.0.2.255:9", "192.0.2.255:7"}
	if !slices.Equal(got, want) {
		t.Fatalf("targets: %v", got)
	}
	if got := wakeTargets(""); len(got) != 2 {
		t.Fatalf("targets without a broadcast: %v", got)
	}
	_, n, _ := net.ParseCIDR("192.0.2.10/24")
	n.IP = net.ParseIP("192.0.2.10")
	if b := subnetBroadcast(n); b != "192.0.2.255" {
		t.Fatalf("subnet broadcast: %s", b)
	}
}

// The packets go to every target, here a listener on the loopback.
func TestWriteWake(t *testing.T) {
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := writeWake(c, []string{"00:00:5e:00:53:01"}, []string{l.LocalAddr().String()}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 200)
	l.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := l.ReadFromUDP(buf)
	if err != nil || n != 102 || buf[6] != 0x00 || buf[8] != 0x5e {
		t.Fatalf("received %d bytes % x: %v", n, buf[:n], err)
	}
}

// In a container or in WSL, the cards are virtual: only the installer's
// addresses count.
func TestWakeSettings(t *testing.T) {
	container := hostView{goos: "linux", getenv: func(string) string { return "" },
		readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		exists:   func(p string) bool { return p == "/.dockerenv" }}
	w, err := wakeSettings(container, "", "", []string{"http://192.0.2.10:8800"})
	if err != nil || len(w.MACs) != 0 {
		t.Fatalf("in a container: %+v %v", w, err)
	}
	w, err = wakeSettings(container, "00-00-5E-00-53-01", "192.0.2.255", nil)
	if err != nil || !slices.Equal(w.MACs, []string{"00:00:5e:00:53:01"}) || w.Broadcast != "192.0.2.255" {
		t.Fatalf("from the installer: %+v %v", w, err)
	}
	if _, err := wakeSettings(container, "bad", "", nil); err == nil || !strings.Contains(err.Error(), "-wake-mac") {
		t.Fatalf("a wrong MAC: %v", err)
	}
	// eriphe-core on this machine: the loopback has no card.
	here := hostView{goos: "darwin", getenv: func(string) string { return "" },
		readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }, exists: func(string) bool { return false }}
	if w, err := wakeSettings(here, "", "", []string{"http://127.0.0.1:8800"}); err != nil || len(w.MACs) != 0 {
		t.Fatalf("on the loopback: %+v %v", w, err)
	}
}

// The short-lived container on the host's network: the node's image,
// nothing mounted, no capability.
func TestWakeHostArgs(t *testing.T) {
	args := strings.Join(wakeHostArgs("ghcr.io/alexis-marcel/eriphe-node:1.2.3", wakeInfo{MACs: []string{"00:00:5e:00:53:01"}, Broadcast: "192.0.2.255"}), " ")
	for _, want := range []string{"--network host", "--cap-drop ALL", "--read-only", "--pull never", "--rm",
		"--entrypoint /usr/local/bin/eriphe-node ghcr.io/alexis-marcel/eriphe-node:1.2.3 wake-send -mac 00:00:5e:00:53:01 -broadcast 192.0.2.255"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if strings.Contains(args, "-v ") || strings.Contains(args, "--mount") || strings.Contains(args, "--privileged") {
		t.Errorf("mounts or privileges: %s", args)
	}
}

// POST /v1/wake sends from here and, in a container, from the host's network.
func TestHandleWake(t *testing.T) {
	defer func(s func(wakeInfo) error, h func(context.Context, *node, wakeInfo) error, v func() hostView) {
		sendWake, runWakeHost, wakeHost = s, h, v
	}(sendWake, runWakeHost, wakeHost)
	var here, host []wakeInfo
	sendWake = func(w wakeInfo) error { here = append(here, w); return nil }
	runWakeHost = func(_ context.Context, _ *node, w wakeInfo) error { host = append(host, w); return nil }
	inside := true
	wakeHost = func() hostView {
		return hostView{goos: "linux", getenv: func(string) string { return "" },
			readFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
			exists:   func(p string) bool { return inside && p == "/.dockerenv" }}
	}
	n := newNode(options{engine: engineDocker})
	post := func(body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		n.handleWake(rec, httptest.NewRequest(http.MethodPost, "/v1/wake", strings.NewReader(body)))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := post(`{"macs":["00-00-5E-00-53-01"],"broadcast":"192.0.2.255"}`)
	if code != 200 || len(here) != 1 || len(host) != 1 || host[0].MACs[0] != "00:00:5e:00:53:01" || host[0].Broadcast != "192.0.2.255" {
		t.Fatalf("in a container: %d %v here=%v host=%v", code, out, here, host)
	}
	inside = false
	if code, _ := post(`{"macs":["00:00:5e:00:53:01"]}`); code != 200 || len(here) != 2 || len(host) != 1 {
		t.Fatalf("on the machine: %d here=%v host=%v", code, here, host)
	}
	for _, bad := range []string{`{"macs":[]}`, `{"macs":["nope"]}`, `{"macs":["00:00:5e:00:53:01"],"broadcast":"x"}`, `nope`} {
		if code, _ := post(bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

// The node reports its card with what it offers.
func TestNodeReportsWake(t *testing.T) {
	n := newNode(options{docker: "/nonexistent/docker", wake: wakeInfo{MACs: []string{"00:00:5e:00:53:01"}, Broadcast: "192.0.2.255"}})
	rec := httptest.NewRecorder()
	n.handleNode(rec, httptest.NewRequest(http.MethodGet, "/v1/node", nil))
	var info struct {
		Wake wakeInfo `json:"wake"`
	}
	json.Unmarshal(rec.Body.Bytes(), &info)
	if !slices.Equal(info.Wake.MACs, []string{"00:00:5e:00:53:01"}) || info.Wake.Broadcast != "192.0.2.255" {
		t.Fatalf("GET /v1/node: %s", rec.Body)
	}
}

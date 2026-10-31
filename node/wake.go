package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Wake-on-LAN. A machine reports the MAC address of the network card it
// reaches eriphe-core through, and its subnet's broadcast address, so that
// eriphe-core can wake it when the user allows it (core/wake.go). The
// installers record them (install.sh, install.ps1: on Windows the PC's own
// card, not WSL's virtual one), as -wake-mac and -wake-broadcast; a node run
// by hand on the machine itself finds them alone.
//
// The machine that stays on (the NAS) also sends the magic packets for
// eriphe-core: POST /v1/wake. In a container on a Docker bridge network a
// broadcast does not leave the bridge, so the node then sends them from a
// short-lived container of its own image on the host's network.

// wakeInfo is what a machine reports so that it can be woken.
type wakeInfo struct {
	MACs      []string `json:"macs,omitempty"`
	Broadcast string   `json:"broadcast,omitempty"` // its subnet's, e.g. 192.0.2.255
}

// parseMACs reads a comma-separated list of 6-byte MAC addresses, in any
// of the usual forms (00:00:5e:00:53:01, 00-00-5E-00-53-01), as lowercase
// colon-separated ones.
func parseMACs(s string) ([]string, error) {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		hw, err := net.ParseMAC(item)
		if err != nil || len(hw) != 6 {
			return nil, fmt.Errorf("%q is not a MAC address, like 00:00:5e:00:53:01", item)
		}
		out = append(out, hw.String())
	}
	return out, nil
}

// parseBroadcast reads an IPv4 broadcast address, or nothing.
func parseBroadcast(s string) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", nil
	}
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return "", fmt.Errorf("%q is not an IPv4 address", s)
	}
	return ip.String(), nil
}

// wakeSettings is the machine's wake information: what the installer
// recorded, else, on the machine itself (not in a container, nor in WSL,
// whose cards are virtual), the card of the address it reaches the first of
// eriphe-core's addresses from.
func wakeSettings(h hostView, macs, broadcast string, cores []string) (wakeInfo, error) {
	var w wakeInfo
	var err error
	if w.MACs, err = parseMACs(macs); err != nil {
		return w, fmt.Errorf("-wake-mac: %w", err)
	}
	if w.Broadcast, err = parseBroadcast(broadcast); err != nil {
		return w, fmt.Errorf("-wake-broadcast: %w", err)
	}
	if len(w.MACs) > 0 || (h.goos == "linux" && (inContainer(h) || inWSL(h))) {
		return w, nil
	}
	for _, core := range cores {
		if local := localAddrTo(core); local != nil {
			if mac, bcast := cardOf(local); mac != "" {
				w.MACs = []string{mac}
				if w.Broadcast == "" {
					w.Broadcast = bcast
				}
			}
			break
		}
	}
	return w, nil
}

// localAddrTo is this machine's address towards an eriphe-core URL: the
// source of a UDP socket connected there, which sends nothing.
func localAddrTo(core string) net.IP {
	u, err := url.Parse(core)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	c, err := net.DialTimeout("udp4", net.JoinHostPort(u.Hostname(), port), 3*time.Second)
	if err != nil {
		return nil
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP.IsLoopback() {
		return nil
	}
	return addr.IP
}

// cardOf finds the network card that has ip: its MAC address and its
// subnet's broadcast address; empty for a card with no Ethernet address
// (a VPN's, as Tailscale's).
func cardOf(ip net.IP) (mac, broadcast string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || !n.IP.Equal(ip) {
				continue
			}
			if len(ifc.HardwareAddr) != 6 {
				return "", ""
			}
			return ifc.HardwareAddr.String(), subnetBroadcast(n)
		}
	}
	return "", ""
}

func subnetBroadcast(n *net.IPNet) string {
	ip, mask := n.IP.To4(), n.Mask
	if ip == nil || len(mask) != net.IPv4len {
		return ""
	}
	b := make(net.IP, net.IPv4len)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return b.String()
}

// magicPacket is Wake-on-LAN's packet for a MAC address: six 0xff bytes,
// then the address 16 times.
func magicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("%q is not a MAC address", mac)
	}
	p := bytes.Repeat([]byte{0xff}, 6)
	for range 16 {
		p = append(p, hw...)
	}
	return p, nil
}

// wakeTargets are where the packets go: the limited broadcast, and the
// machine's subnet broadcast when known, each on ports 9 and 7.
func wakeTargets(broadcast string) []string {
	hosts := []string{"255.255.255.255"}
	if broadcast != "" && broadcast != "255.255.255.255" {
		hosts = append(hosts, broadcast)
	}
	var out []string
	for _, h := range hosts {
		out = append(out, net.JoinHostPort(h, "9"), net.JoinHostPort(h, "7"))
	}
	return out
}

// sendWake sends the magic packets from this network namespace. The
// broadcast flag (SO_BROADCAST) is set by Go on every UDP socket, and needs
// no privilege.
var sendWake = func(w wakeInfo) error {
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer c.Close()
	return writeWake(c, w.MACs, wakeTargets(w.Broadcast))
}

// writeWake writes each MAC address's packet to each target.
func writeWake(c *net.UDPConn, macs, targets []string) error {
	sent := 0
	var last error
	for _, mac := range macs {
		p, err := magicPacket(mac)
		if err != nil {
			return err
		}
		for _, t := range targets {
			addr, err := net.ResolveUDPAddr("udp4", t)
			if err == nil {
				_, err = c.WriteToUDP(p, addr)
			}
			if err != nil {
				last = err
				continue
			}
			sent++
		}
	}
	if sent == 0 {
		return fmt.Errorf("no magic packet sent: %v", last)
	}
	return nil
}

// runWakeSend is `eriphe-node wake-send -mac … -broadcast …`: the
// short-lived container on the host's network sends the packets and ends.
func runWakeSend(args []string) {
	var w wakeInfo
	var macs, bcast string
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "-mac":
			macs = args[i+1]
		case "-broadcast":
			bcast = args[i+1]
		}
	}
	var err error
	if w.MACs, err = parseMACs(macs); err == nil {
		w.Broadcast, err = parseBroadcast(bcast)
	}
	if err == nil && len(w.MACs) == 0 {
		err = errors.New("no -mac")
	}
	if err == nil {
		err = sendWake(w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wake-send:", err)
		os.Exit(1)
	}
}

var containerID = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// selfImage is the image of the node's own container, as Docker lists it
// (the socket proxy allows listing containers).
func (n *node) selfImage(ctx context.Context) (string, error) {
	id := ""
	if raw, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if m := containerID.FindSubmatch(raw); m != nil {
			id = string(m[1])
		}
	}
	if id == "" {
		id, _ = os.Hostname()
	}
	out, err := exec.CommandContext(ctx, n.opt.docker, "ps", "--no-trunc", "--filter", "id="+id, "--format", "{{.Image}}").Output()
	img := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil || img == "" {
		return "", fmt.Errorf("cannot find this node's own image (%v)", err)
	}
	return img, nil
}

// wakeHostArgs is the `docker run` of the short-lived container that sends
// the packets from the host's network: the node's image, nothing mounted,
// no capability, read-only, gone once done.
func wakeHostArgs(image string, w wakeInfo) []string {
	args := []string{"run", "--rm", "--pull", "never", "--network", "host", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "--read-only", "--label", "eriphe.wake=1",
		"--entrypoint", "/usr/local/bin/eriphe-node", image, "wake-send", "-mac", strings.Join(w.MACs, ",")}
	if w.Broadcast != "" {
		args = append(args, "-broadcast", w.Broadcast)
	}
	return args
}

// wakeHost is the machine handleWake runs on; a test stands in for it.
var wakeHost = thisHost

// runWakeHost sends the packets from the host's network.
var runWakeHost = func(ctx context.Context, n *node, w wakeInfo) error {
	img, err := n.selfImage(ctx)
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, n.opt.docker, wakeHostArgs(img, w)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// handleWake is POST /v1/wake {macs, broadcast}: eriphe-core asks this
// machine, which stays on, to wake another one. It answers how it sent the
// packets: "here" (its own network), "host" (the host's, from a
// container), or why it could not.
func (n *node) handleWake(w http.ResponseWriter, r *http.Request) {
	var in wakeInfo
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	macs, err := parseMACs(strings.Join(in.MACs, ","))
	if err == nil {
		in.Broadcast, err = parseBroadcast(in.Broadcast)
	}
	if err == nil && len(macs) == 0 {
		err = errors.New("no MAC address")
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	in.MACs = macs
	var sent []string
	var errs []string
	if err := sendWake(in); err != nil {
		errs = append(errs, "here: "+err.Error())
	} else {
		sent = append(sent, "here")
	}
	if inContainer(wakeHost()) && !n.apple() {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := runWakeHost(ctx, n, in); err != nil {
			errs = append(errs, "host: "+err.Error())
		} else {
			sent = append(sent, "host")
		}
	}
	log.Printf("wake %s: sent %v, errors %v", strings.Join(in.MACs, ","), sent, errs)
	if len(sent) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": strings.Join(errs, "; ")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "errors": errs})
}

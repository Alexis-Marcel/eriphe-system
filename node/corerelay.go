package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// The core relay, for a Mac (corerelay_unix.go). macOS's local network
// privacy (Apple TN3179) keeps a LaunchAgent off the LAN; a program run as
// root is allowed there without asking. eriphe-node must be an agent, in the
// user's session, where Apple's container has its services (a daemon gets
// "XPC connection error"), and a daemon run as the user is held back like an
// agent. So a root LaunchDaemon, org.eriphe.relay, does only this: it
// connects to eriphe-core for the node. The node asks it on a Unix socket
// only the user can open, naming host:port; the relay dials it only if it is
// one of eriphe-core's addresses, as the node's files say, then copies bytes
// both ways: TLS is still end to end, between the node and eriphe-core.
//
//	node → relay:  host:port\n
//	relay → node:  ok\n (then the connection's bytes) or error\n

// coreRelaySocket is where the node finds the relay (ERIPHE_CORE_RELAY), ""
// for none: the node then dials eriphe-core itself.
var coreRelaySocket = os.Getenv("ERIPHE_CORE_RELAY")

var coreDialer = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}

// dialCore dials eriphe-core: through the core relay when there is one (but
// for the loopback: eriphe-core on this Mac), else directly. A relay that is
// not there (not installed yet, restarting) leaves the direct way.
func dialCore(ctx context.Context, network, addr string) (net.Conn, error) {
	if coreRelaySocket != "" && !loopback(addr) {
		c, err := dialRelay(ctx, coreRelaySocket, addr)
		if err == nil || !errors.Is(err, errNoRelay) {
			return c, err
		}
	}
	return coreDialer.DialContext(ctx, network, addr)
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var errNoRelay = errors.New("no core relay")

// dialRelay connects to addr through the relay at sock.
func dialRelay(ctx context.Context, sock, addr string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("%w at %s: %v", errNoRelay, sock, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := fmt.Fprintf(c, "%s\n", addr); err != nil {
		c.Close()
		return nil, err
	}
	// Byte by byte up to the line's end: what follows belongs to the caller.
	var line []byte
	b := make([]byte, 1)
	for len(line) < 16 {
		if _, err := c.Read(b); err != nil {
			c.Close()
			return nil, fmt.Errorf("the core relay: %w", err)
		}
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
	}
	c.SetDeadline(time.Time{})
	if strings.TrimSpace(string(line)) != "ok" {
		c.Close()
		return nil, fmt.Errorf("the core relay refuses %s: not one of eriphe-core's addresses, or it does not answer", addr)
	}
	return c, nil
}

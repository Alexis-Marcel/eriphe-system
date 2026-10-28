//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The core relay's side, run as root by the LaunchDaemon org.eriphe.relay
// (corerelay.go says why): `eriphe-node core-relay -socket <path> -dir
// <~/.eriphe> -owner <uid>`, from a copy of the binary only root can change
// (/usr/local/libexec/eriphe-relay). It takes nothing from the user but a
// host:port line on its socket, and its list of addresses from two files of
// the user's, read as a root program must read a user's files: no symlink
// followed, regular files owned by that user, small, parsed strictly, never
// logged.

const relayFileMax = 64 << 10 // the address files are a few hundred bytes

func runCoreRelay(args []string) {
	fs := flag.NewFlagSet("core-relay", flag.ExitOnError)
	sock := fs.String("socket", "/var/run/eriphe/relay.sock", "the Unix socket the node asks on, in a folder of root's")
	dir := fs.String("dir", "", "the install's folder (~/.eriphe): node/node.json and node.env name eriphe-core's addresses")
	owner := fs.Int("owner", -1, "the user whose node may use the relay: the socket is theirs (0600), and so must the files be")
	fs.Parse(args)
	if *dir == "" || *owner <= 0 || !filepath.IsAbs(*dir) || !filepath.IsAbs(*sock) {
		log.Fatal("core-relay: give -dir (absolute) and -owner (the user's uid, not root's)")
	}
	l, err := listenRelay(*sock, *owner)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("core relay on %s for uid %d", *sock, *owner)
	serveRelay(l, func() map[string]bool { return coreAddrs(*dir, *owner) },
		(&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
}

// listenRelay makes the socket in a folder of root's (made if missing, not
// writable by anyone else), 0600 from its creation, then gives it to owner.
func listenRelay(sock string, owner int) (net.Listener, error) {
	dir := filepath.Dir(sock)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("core-relay: %s must be a folder only its owner writes to", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 && st.Uid != 0 {
		return nil, fmt.Errorf("core-relay: %s must be root's", dir)
	}
	os.Remove(sock)
	old := syscall.Umask(0o177)
	l, err := net.Listen("unix", sock)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		if err := os.Lchown(sock, owner, -1); err != nil {
			l.Close()
			return nil, err
		}
	}
	return l, nil
}

// readOwned reads a regular file of owner's, without following a symlink,
// at most relayFileMax bytes; nil if it is not one.
func readOwned(p string, owner int) []byte {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > relayFileMax {
		return nil
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != owner {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, relayFileMax+1))
	if err != nil || len(raw) > relayFileMax {
		return nil
	}
	return raw
}

// ownedDir tells whether p is a real directory of owner's (no symlink).
func ownedDir(p string, owner int) bool {
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == owner
}

// coreAddrs are the host:port of eriphe-core's addresses that the node's
// files in dir name: node/node.json's core and cores (JSON), node.env's
// ERIPHE_CORE='url,url' (the join, before node.json).
func coreAddrs(dir string, owner int) map[string]bool {
	out := map[string]bool{}
	if !ownedDir(dir, owner) {
		return out
	}
	var urls []string
	if ownedDir(filepath.Join(dir, "node"), owner) {
		if raw := readOwned(filepath.Join(dir, "node", "node.json"), owner); raw != nil {
			var st struct {
				Core  string   `json:"core"`
				Cores []string `json:"cores"`
			}
			if json.Unmarshal(raw, &st) == nil {
				urls = append(append(urls, st.Core), st.Cores...)
			}
		}
	}
	if raw := readOwned(filepath.Join(dir, "node.env"), owner); raw != nil {
		for _, l := range strings.Split(string(raw), "\n") {
			v, ok := strings.CutPrefix(l, "ERIPHE_CORE='")
			if !ok || !strings.HasSuffix(v, "'") {
				continue
			}
			urls = append(urls, strings.Split(strings.TrimSuffix(v, "'"), ",")...)
		}
	}
	for _, raw := range urls {
		if hp, ok := coreHostPort(raw); ok {
			out[hp] = true
		}
	}
	return out
}

// coreHostPort is the host:port of an http(s) URL of eriphe-core, strictly:
// nothing but a scheme, a host and a port.
func coreHostPort(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port), true
}

// serveRelay answers the node's requests on l.
func serveRelay(l net.Listener, allowed func() map[string]bool, dial func(context.Context, string, string) (net.Conn, error)) {
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go relayOne(c, allowed, dial)
	}
}

// relayOne reads one host:port line (at most 300 bytes, within 10 s), and
// connects it only if it is one of eriphe-core's addresses.
func relayOne(c net.Conn, allowed func() map[string]bool, dial func(context.Context, string, string) (net.Conn, error)) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReaderSize(c, 512)
	line, err := r.ReadSlice('\n')
	if err != nil || len(line) > 300 {
		return
	}
	c.SetReadDeadline(time.Time{})
	addr := strings.ToLower(strings.TrimSpace(string(line)))
	if _, _, err := net.SplitHostPort(addr); err != nil || !allowed()[addr] {
		io.WriteString(c, "error\n")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	up, err := dial(ctx, "tcp", addr)
	cancel()
	if err != nil {
		io.WriteString(c, "error\n")
		return
	}
	defer up.Close()
	io.WriteString(c, "ok\n")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(up, r) // what the node sent after its line, then the rest
		if t, ok := up.(*net.TCPConn); ok {
			t.CloseWrite()
		}
	}()
	io.Copy(c, up)
	if u, ok := c.(*net.UnixConn); ok {
		u.CloseWrite()
	}
	wg.Wait()
}

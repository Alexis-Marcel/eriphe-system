package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// SSH without a key in the task. eriphe-cred ssh-agent is the agent ssh
// finds at SSH_AUTH_SOCK: it lists the keys of the task's accesses and has
// each login signed by eriphe-core, through the node. eriphe-cred tunnel is
// ssh's ProxyCommand: it opens a tunnel through the node's relay, the only
// way out of the task, to the exact host and port of the access.
//
// The agent's protocol is small enough for this module to stay without a
// dependency (draft-miller-ssh-agent): two requests, the rest is refused.

const (
	envSSHURL  = "ERIPHE_SSH_URL"
	envSSHSock = "SSH_AUTH_SOCK"

	agentFailure          = 5
	agentRequestIdentites = 11
	agentIdentitiesAnswer = 12
	agentSignRequest      = 13
	agentSignResponse     = 14

	agentMaxMessage = 256 << 10
)

// signer is eriphe-core, through the node.
type signer interface {
	keys() ([]agentKey, error)
	sign(key, data []byte) ([]byte, error)
}

type agentKey struct {
	Key     []byte `json:"key"`
	Comment string `json:"comment"`
}

type sshClient struct {
	url, secret string
	http        *http.Client
}

func (c *sshClient) ask(body map[string]any, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return errors.New(strings.TrimSpace(string(answer)))
	}
	return json.Unmarshal(answer, out)
}

func (c *sshClient) keys() ([]agentKey, error) {
	var a struct {
		Keys []agentKey `json:"keys"`
	}
	err := c.ask(map[string]any{"op": "keys"}, &a)
	return a.Keys, err
}

func (c *sshClient) sign(key, data []byte) ([]byte, error) {
	var a struct {
		Signature []byte `json:"signature"`
	}
	err := c.ask(map[string]any{"op": "sign", "key": key, "data": data}, &a)
	return a.Signature, err
}

func runSSHAgent(stderr io.Writer) int {
	path := os.Getenv(envSSHSock)
	u := os.Getenv(envSSHURL)
	if path == "" || u == "" {
		fmt.Fprintln(stderr, "eriphe-cred: no SSH access in this task")
		return 1
	}
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		fmt.Fprintln(stderr, "eriphe-cred:", err)
		return 1
	}
	defer l.Close()
	s := &sshClient{url: u, secret: os.Getenv(envSecret), http: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{}}}
	serveAgent(l, s, stderr)
	return 0
}

func serveAgent(l net.Listener, s signer, stderr io.Writer) {
	var mu sync.Mutex // one line at a time in what watches the task
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			if err := agentConn(conn, s); err != nil && !errors.Is(err, io.EOF) {
				mu.Lock()
				fmt.Fprintln(stderr, "eriphe-cred:", err)
				mu.Unlock()
			}
		}()
	}
}

// agentConn answers the requests of one client.
func agentConn(rw io.ReadWriter, s signer) error {
	r := bufio.NewReader(rw)
	for {
		var size uint32
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			return err
		}
		if size == 0 || size > agentMaxMessage {
			return errors.New("bad agent message")
		}
		msg := make([]byte, size)
		if _, err := io.ReadFull(r, msg); err != nil {
			return err
		}
		out, why := agentAnswer(msg, s)
		if why != nil {
			fmt.Fprintln(os.Stderr, "eriphe-cred:", why)
		}
		frame := binary.BigEndian.AppendUint32(nil, uint32(len(out)))
		if _, err := rw.Write(append(frame, out...)); err != nil {
			return err
		}
	}
}

func agentString(b []byte, s []byte) []byte {
	return append(binary.BigEndian.AppendUint32(b, uint32(len(s))), s...)
}

// agentField cuts a string off a message.
func agentField(b []byte) (field, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

// agentAnswer is the answer to a request; anything else (adding a key,
// locking) fails, as the keys are not here.
func agentAnswer(msg []byte, s signer) ([]byte, error) {
	failure := []byte{agentFailure}
	switch msg[0] {
	case agentRequestIdentites:
		keys, err := s.keys()
		if err != nil {
			return failure, err
		}
		out := binary.BigEndian.AppendUint32([]byte{agentIdentitiesAnswer}, uint32(len(keys)))
		for _, k := range keys {
			out = agentString(agentString(out, k.Key), []byte(k.Comment))
		}
		return out, nil
	case agentSignRequest:
		key, rest, ok := agentField(msg[1:])
		if !ok {
			return failure, errors.New("bad sign request")
		}
		data, _, ok := agentField(rest)
		if !ok {
			return failure, errors.New("bad sign request")
		}
		sig, err := s.sign(key, data)
		if err != nil {
			return failure, err
		}
		return agentString([]byte{agentSignResponse}, sig), nil
	}
	return failure, nil
}

// proxyURL is the node's relay, as the task's programs are pointed at it.
func proxyURL() (*url.URL, error) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := os.Getenv(name); v != "" {
			return url.Parse(v)
		}
	}
	return nil, errors.New("no relay: HTTPS_PROXY is not set")
}

func runTunnel(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: eriphe-cred tunnel HOST PORT")
		return 2
	}
	proxy, err := proxyURL()
	if err == nil {
		err = tunnel(proxy, net.JoinHostPort(args[0], args[1]), stdin, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "eriphe-cred:", err)
		return 1
	}
	return 0
}

// tunnel asks the relay for target, then carries bytes both ways until
// either side is done.
func tunnel(proxy *url.URL, target string, stdin io.Reader, stdout io.Writer) error {
	conn, err := net.DialTimeout("tcp", proxy.Host, 15*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	auth := ""
	if proxy.User != nil {
		pass, _ := proxy.User.Password()
		auth = "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+pass)) + "\r\n"
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", target, target, auth); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	res, err := http.ReadResponse(r, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		res.Body.Close()
		return fmt.Errorf("the relay refused %s: %s", target, strings.TrimSpace(string(body)))
	}
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, stdin)
		if c, ok := conn.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- err
	}()
	go func() {
		_, err := io.Copy(stdout, r) // what came with the answer, too
		done <- err
	}()
	return <-done
}

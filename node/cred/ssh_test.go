package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fakeSigner struct {
	list []agentKey
	err  error
	got  [][]byte
}

func (f *fakeSigner) keys() ([]agentKey, error) { return f.list, f.err }
func (f *fakeSigner) sign(key, data []byte) ([]byte, error) {
	f.got = append(f.got, key, data)
	return []byte("signature"), f.err
}

func signRequest(key, data string) []byte {
	return agentString(agentString([]byte{agentSignRequest}, []byte(key)), []byte(data))
}

func TestAgentListsTheKeys(t *testing.T) {
	out, err := agentAnswer([]byte{agentRequestIdentites}, &fakeSigner{list: []agentKey{{Key: []byte("blob"), Comment: "box"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := agentString(agentString(binary.BigEndian.AppendUint32([]byte{agentIdentitiesAnswer}, 1), []byte("blob")), []byte("box"))
	if !bytes.Equal(out, want) {
		t.Fatalf("%q", out)
	}
}

func TestAgentSigns(t *testing.T) {
	f := &fakeSigner{}
	out, err := agentAnswer(append(signRequest("blob", "data"), 0, 0, 0, 0), f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, agentString([]byte{agentSignResponse}, []byte("signature"))) {
		t.Fatalf("%q", out)
	}
	if string(f.got[0]) != "blob" || string(f.got[1]) != "data" {
		t.Fatalf("%q", f.got)
	}
}

func TestAgentFails(t *testing.T) {
	refused := &fakeSigner{err: errors.New("held back")}
	for name, msg := range map[string][]byte{
		"refused":      signRequest("blob", "data"),
		"cut":          {agentSignRequest, 0, 0, 0, 9, 'x'},
		"huge":         {agentSignRequest, 255, 255, 255, 255},
		"add a key":    {17},
		"lock":         {22},
		"no signature": {agentSignRequest},
	} {
		out, _ := agentAnswer(msg, refused)
		if !bytes.Equal(out, []byte{agentFailure}) {
			t.Errorf("%s: %q", name, out)
		}
	}
}

func TestAgentOverAConnection(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	go agentConn(b, &fakeSigner{})
	msg := []byte{agentRequestIdentites}
	a.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(msg))), msg...))
	var size uint32
	if err := binary.Read(a, binary.BigEndian, &size); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, size)
	if _, err := io.ReadFull(a, out); err != nil {
		t.Fatal(err)
	}
	if out[0] != agentIdentitiesAnswer {
		t.Fatalf("%v", out)
	}
}

func TestSSHClientAsksTheNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			Op   string
			Data []byte
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Op == "sign" && string(req.Data) == "refused" {
			http.Error(w, "the access box is held back", http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"signature": []byte("sig"), "keys": []agentKey{{Key: []byte("k"), Comment: "box"}}})
	}))
	defer srv.Close()
	c := &sshClient{url: srv.URL, secret: "secret", http: srv.Client()}
	if k, err := c.keys(); err != nil || len(k) != 1 || k[0].Comment != "box" {
		t.Fatalf("%+v %v", k, err)
	}
	if s, err := c.sign([]byte("k"), []byte("d")); err != nil || string(s) != "sig" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := c.sign([]byte("k"), []byte("refused")); err == nil || !strings.Contains(err.Error(), "held back") {
		t.Fatalf("%v", err)
	}
}

// fakeRelay answers one CONNECT, then echoes.
func fakeRelay(t *testing.T, status string) (*url.URL, chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	head := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var b strings.Builder
		one := make([]byte, 1)
		for !strings.HasSuffix(b.String(), "\r\n\r\n") {
			if _, err := c.Read(one); err != nil {
				return
			}
			b.Write(one)
		}
		head <- b.String()
		io.WriteString(c, status)
		if strings.Contains(status, " 200 ") {
			io.Copy(c, c)
		}
	}()
	return &url.URL{Scheme: "http", Host: l.Addr().String(), User: url.UserPassword("t-1", "s")}, head
}

func TestTunnelCarriesBytes(t *testing.T) {
	proxy, head := fakeRelay(t, "HTTP/1.1 200 Connection established\r\n\r\n")
	stdin, in := io.Pipe()
	out, stdout := io.Pipe()
	go tunnel(proxy, "192.0.2.10:22", stdin, stdout)
	io.WriteString(in, "SSH-2.0-test\n")
	echo := make([]byte, 13)
	if _, err := io.ReadFull(out, echo); err != nil || string(echo) != "SSH-2.0-test\n" {
		t.Fatalf("%q %v", echo, err)
	}
	got := <-head
	if !strings.HasPrefix(got, "CONNECT 192.0.2.10:22 HTTP/1.1\r\n") || !strings.Contains(got, "Proxy-Authorization: Basic dC0xOnM=") {
		t.Fatalf("%q", got)
	}
}

func TestTunnelSaysWhyItWasRefused(t *testing.T) {
	proxy, _ := fakeRelay(t, "HTTP/1.1 403 Forbidden\r\nContent-Length: 18\r\n\r\nthe access is held")
	err := tunnel(proxy, "192.0.2.10:22", strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "the access is held") {
		t.Fatalf("%v", err)
	}
}

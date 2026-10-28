package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// A node joins with a join token, connects, and is then reachable through
// its tunnel; the token works only once.
func TestJoinAndConnect(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{}, store)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/join-tokens", c.handleJoinToken)
	mux.HandleFunc("POST /v1/nodes/join", c.handleJoin)
	mux.HandleFunc("GET /v1/nodes/connect", c.handleConnect)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	post := func(path string, in any, out any) int {
		raw, _ := json.Marshal(in)
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	var tok struct{ Token string }
	if post("/v1/join-tokens?name=pc", nil, &tok) != http.StatusCreated {
		t.Fatal("join token")
	}
	var joined struct{ Secret string }
	if code := post("/v1/nodes/join", map[string]string{"name": "pc", "token": tok.Token}, &joined); code != http.StatusOK {
		t.Fatalf("join: %d", code)
	}
	if code := post("/v1/nodes/join", map[string]string{"name": "pc", "token": tok.Token}, nil); code != http.StatusUnauthorized {
		t.Fatalf("reused token: %d", code)
	}

	// The node side: upgrade, then serve its API on the session.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/nodes/connect", nil)
	req.Header.Set("Authorization", "Bearer "+joined.Secret)
	req.Header.Set("Eriphe-Node", "pc")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", tunnelProto)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("connect: %s", resp.Status)
	}
	session, err := yamux.Server(resp.Body.(io.ReadWriteCloser), tunnelConfig())
	if err != nil {
		t.Fatal(err)
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/node", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"runtimes":["claude-code"]}`)
	})
	go http.Serve(session, api)

	deadline := time.Now().Add(5 * time.Second)
	for {
		nodes, _ := store.nodes()
		if len(nodes) == 1 && nodes[0].Online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node never came online: %+v", nodes)
		}
		time.Sleep(50 * time.Millisecond)
	}
	info, err := c.node("pc").info(context.Background())
	if err != nil || string(bytes.TrimSpace(info)) != `{"runtimes":["claude-code"]}` {
		t.Fatalf("info = %s, %v", info, err)
	}

	session.Close()
	deadline = time.Now().Add(5 * time.Second)
	for {
		nodes, _ := store.nodes()
		if !nodes[0].Online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("node still online after disconnect")
		}
		time.Sleep(50 * time.Millisecond)
	}

	bad, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/nodes/connect", nil)
	bad.Header.Set("Authorization", "Bearer wrong")
	bad.Header.Set("Eriphe-Node", "pc")
	if resp, _ := http.DefaultClient.Do(bad); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %s", resp.Status)
	}
}

// registerNode registers a node directly, for tests that need one.
func (s *Store) registerNode(name, secretHash string) error {
	_, err := s.db.Exec(`INSERT INTO nodes (name, url, secret_hash) VALUES (?, '', ?)
		ON CONFLICT(name) DO UPDATE SET secret_hash = excluded.secret_hash`, name, secretHash)
	return err
}

// A join token is for one node name. It cannot take over a registered node,
// unless it was made to rejoin that node; registered nodes keep working.
func TestJoinTokenNameBinding(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	c := newCore(&Config{}, store)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/join-tokens", c.handleJoinToken)
	mux.HandleFunc("POST /v1/nodes/join", c.handleJoin)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post := func(path string, in any, out any) int {
		raw, _ := json.Marshal(in)
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	token := func(query string, want int) string {
		var tok struct{ Token string }
		if code := post("/v1/join-tokens"+query, nil, &tok); code != want {
			t.Fatalf("join token %s: %d, want %d", query, code, want)
		}
		return tok.Token
	}
	join := func(name, tok string) (int, string) {
		var out struct{ Secret string }
		code := post("/v1/nodes/join", map[string]string{"name": name, "token": tok}, &out)
		return code, out.Secret
	}

	// A node registered before named tokens (the NAS, the PC) keeps its secret.
	if err := store.registerNode("nas", hashSecret("nas-secret")); err != nil {
		t.Fatal(err)
	}
	token("", http.StatusBadRequest)
	token("?name=Bad", http.StatusBadRequest)
	token("?name=nas", http.StatusConflict)
	token("?name=pc&rejoin=1", http.StatusNotFound)

	pc := token("?name=pc", http.StatusCreated)
	if id, _, ok := strings.Cut(pc, "."); !ok || len(id) != 16 {
		t.Fatalf("token %q is not id.secret", pc)
	}
	if code, _ := join("nas", pc); code != http.StatusForbidden {
		t.Fatalf("a token for pc joined as nas: %d", code)
	}
	if !store.checkNode("nas", hashSecret("nas-secret")) {
		t.Fatal("nas lost its secret")
	}
	code, pcSecret := join("pc", pc) // the refused try left the token unused
	if code != http.StatusOK {
		t.Fatalf("join pc: %d", code)
	}

	// An unnamed token from before joins a new name, never a taken one.
	legacy := randomSecret()
	if _, err := store.db.Exec(`INSERT INTO join_tokens (hash, expires) VALUES (?, ?)`,
		hashSecret(legacy), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if code, _ := join("nas", legacy); code != http.StatusConflict {
		t.Fatalf("an unnamed token took over nas: %d", code)
	}
	if code, _ := join("desk", legacy); code != http.StatusOK {
		t.Fatalf("an unnamed token joined a new name: %d", code)
	}

	// Rejoining: only with a rejoin token for that name.
	other := token("?name=laptop", http.StatusCreated)
	if code, _ := join("pc", other); code != http.StatusForbidden {
		t.Fatalf("a token for laptop rejoined pc: %d", code)
	}
	re := token("?name=pc&rejoin=1", http.StatusCreated)
	if code, _ := join("nas", re); code != http.StatusForbidden {
		t.Fatalf("a rejoin token for pc rejoined nas: %d", code)
	}
	code, newSecret := join("pc", re)
	if code != http.StatusOK || newSecret == pcSecret {
		t.Fatalf("rejoin pc: %d", code)
	}
	if store.checkNode("pc", hashSecret(pcSecret)) || !store.checkNode("pc", hashSecret(newSecret)) {
		t.Fatal("rejoining did not replace pc's secret")
	}
	if code, _ := join("pc", re); code != http.StatusUnauthorized {
		t.Fatalf("a used rejoin token worked again: %d", code)
	}

	// Expired tokens do nothing.
	if _, err := store.db.Exec(`UPDATE join_tokens SET expires = ?`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if code, _ := join("laptop", other); code != http.StatusUnauthorized {
		t.Fatalf("an expired token joined: %d", code)
	}
}

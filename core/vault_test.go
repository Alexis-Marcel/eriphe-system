package main

import (
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testVault(t *testing.T) (*Store, *Vault, []byte) {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := vaultKey(filepath.Join(t.TempDir(), "keys", "vault.key"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := openVault(s, key)
	if err != nil {
		t.Fatal(err)
	}
	return s, v, key
}

func TestVaultRoundTrip(t *testing.T) {
	s, v, _ := testVault(t)
	if err := v.set("github", "  ghp_secret\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.get("github"); err != nil || got != "ghp_secret" {
		t.Fatalf("get = %q, %v", got, err)
	}
	v.set("github", "ghp_other")
	if got, _ := v.get("github"); got != "ghp_other" {
		t.Fatalf("replaced = %q", got)
	}
	// Encrypted at rest.
	var raw []byte
	s.db.QueryRow(`SELECT value FROM vault WHERE name = 'github'`).Scan(&raw)
	if strings.Contains(string(raw), "ghp_other") {
		t.Fatal("the value is stored in clear")
	}
	list, _ := v.list()
	if len(list) != 1 || list[0].Name != "github" {
		t.Fatalf("list = %+v", list)
	}
	if err := v.set("Bad Name", "x"); err == nil {
		t.Fatal("a bad name was accepted")
	}
	if err := v.set("empty", "  "); err == nil {
		t.Fatal("an empty secret was accepted")
	}
	if err := v.remove("github"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.get("github"); err == nil {
		t.Fatal("a removed secret is still there")
	}
	if err := v.remove("github"); err == nil {
		t.Fatal("removed twice")
	}
	// A value moved under another name does not open.
	v.set("a", "value-a")
	s.db.Exec(`INSERT INTO vault (name, nonce, value, created, updated) SELECT 'b', nonce, value, created, updated FROM vault WHERE name = 'a'`)
	if _, err := v.get("b"); err == nil {
		t.Fatal("a value opened under another name")
	}
}

func TestVaultWrongKey(t *testing.T) {
	s, v, _ := testVault(t)
	v.set("x", "secret")
	other := make([]byte, 32)
	if _, err := openVault(s, other); err == nil {
		t.Fatal("a wrong key opened the vault")
	}
}

func TestVaultKeyFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "vault.key")
	k1, err := vaultKey(file)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(file)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", st.Mode())
	}
	k2, _ := vaultKey(file)
	if hex.EncodeToString(k1) != hex.EncodeToString(k2) || len(k1) != 32 {
		t.Fatal("the key changed")
	}
	t.Setenv(vaultKeyEnv, strings.Repeat("ab", 32))
	k3, err := vaultKey(file)
	if err != nil || hex.EncodeToString(k3) != strings.Repeat("ab", 32) {
		t.Fatalf("env key = %x, %v", k3, err)
	}
	t.Setenv(vaultKeyEnv, "short")
	if _, err := vaultKey(file); err == nil {
		t.Fatal("a short key was accepted")
	}
}

func TestReadSecretFromVault(t *testing.T) {
	_, v, _ := testVault(t)
	setVault(nil)
	if _, err := readSecret("vault:token"); err == nil {
		t.Fatal("read from a closed vault")
	}
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	v.set("token", "abc")
	if got, err := readSecret("vault:token"); err != nil || got != "abc" {
		t.Fatalf("readSecret = %q, %v", got, err)
	}
	if _, err := readSecret("vault:missing"); err == nil {
		t.Fatal("read a missing secret")
	}
	// Files still work, and lend uses the vault too.
	file := filepath.Join(t.TempDir(), "s")
	os.WriteFile(file, []byte("from-file\n"), 0o600)
	if got, _ := readSecret(file); got != "from-file" {
		t.Fatalf("file = %q", got)
	}
	cfg := &Config{Identities: map[string]Identity{"gh": {Env: "GH_TOKEN", File: "vault:token"}}}
	env := map[string]string{}
	if err := cfg.lend([]string{"gh"}, env); err != nil || env["GH_TOKEN"] != "abc" {
		t.Fatalf("lend = %v, %v", env, err)
	}
	// Import a file once.
	if err := v.importFile("imported", file); err != nil {
		t.Fatal(err)
	}
	if got, _ := readSecret("vault:imported"); got != "from-file" {
		t.Fatalf("imported = %q", got)
	}
}

func TestVaultAPI(t *testing.T) {
	c, srv := newTestAPI(t)
	mux := http.NewServeMux()
	c.vaultRoutes(mux, "secret")
	srv.Config.Handler = mux
	_, v, _ := testVault(t)
	setVault(v)
	t.Cleanup(func() { setVault(nil) })
	if code := call(t, srv, "PUT", "/v1/secrets/github", `{"value":"ghp_1"}`, nil); code != 200 {
		t.Fatalf("put: %d", code)
	}
	var list []SecretInfo
	if code := call(t, srv, "GET", "/v1/secrets", "", &list); code != 200 || len(list) != 1 || list[0].Name != "github" {
		t.Fatalf("list: %d %+v", code, list)
	}
	if got, _ := readSecret("vault:github"); got != "ghp_1" {
		t.Fatalf("value = %q", got)
	}
	if code := call(t, srv, "PUT", "/v1/secrets/Bad", `{"value":"x"}`, nil); code != 400 {
		t.Fatalf("bad name: %d", code)
	}
	if code := call(t, srv, "DELETE", "/v1/secrets/github", "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code := call(t, srv, "DELETE", "/v1/secrets/github", "", nil); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}

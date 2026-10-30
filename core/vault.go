package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The vault keeps secrets encrypted at rest in eriphe-core's database, with
// AES-256-GCM. Its key lives outside the database: in ERIPHE_VAULT_KEY, or
// in a file next to it, created (0600) at first start. Every configuration
// field that names a secret file also takes "vault:<name>" (see readSecret).
// Values are never shown back, logged, or sent anywhere but where a task
// borrows them.

const vaultSchema = `CREATE TABLE IF NOT EXISTS vault (
  name TEXT PRIMARY KEY, nonce BLOB NOT NULL, value BLOB NOT NULL,
  created INTEGER NOT NULL, updated INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, vaultSchema) }

const (
	vaultKeyEnv    = "ERIPHE_VAULT_KEY"
	vaultPrefix    = "vault:"
	vaultCheckName = "\x00check" // a known value, to tell a wrong key at start
	vaultCheckText = "eriphe-vault"
)

var vaultName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

var errNoVault = errors.New("the vault is not open")

type Vault struct {
	db   *sql.DB
	aead cipher.AEAD
	now  func() time.Time
}

type SecretInfo struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// The vault that readSecret reads "vault:" references from.
var (
	vaultMu sync.RWMutex
	current *Vault
)

func setVault(v *Vault) {
	vaultMu.Lock()
	current = v
	vaultMu.Unlock()
}

func currentVault() *Vault {
	vaultMu.RLock()
	defer vaultMu.RUnlock()
	return current
}

func vaultSecret(name string) (string, error) {
	v := currentVault()
	if v == nil {
		return "", errNoVault
	}
	return v.get(name)
}

// vaultKey reads the key from the environment, or from file, creating the
// file with a new key if it does not exist. The key is 32 bytes, written in
// hex; the variable may also hold it in base64.
func vaultKey(file string) ([]byte, error) {
	if s := strings.TrimSpace(os.Getenv(vaultKeyEnv)); s != "" {
		return decodeKey(s)
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		rand.Read(key)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = f.WriteString(hex.EncodeToString(key) + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return key, err
	}
	if err != nil {
		return nil, err
	}
	return decodeKey(strings.TrimSpace(string(raw)))
}

func decodeKey(s string) ([]byte, error) {
	if k, err := hex.DecodeString(s); err == nil && len(k) == 32 {
		return k, nil
	}
	if k, err := base64.StdEncoding.DecodeString(s); err == nil && len(k) == 32 {
		return k, nil
	}
	return nil, errors.New("the vault key must be 32 bytes, in hex or base64")
}

// startVault opens the vault of the configuration for readSecret.
func startVault(cfg *Config, s *Store) error {
	v, err := configVault(cfg, s)
	if err != nil {
		return err
	}
	setVault(v)
	return nil
}

// configVault opens the vault of the configuration, with its key.
func configVault(cfg *Config, s *Store) (*Vault, error) {
	file := cfg.VaultKeyFile
	if file == "" {
		file = filepath.Join(filepath.Dir(cfg.DB), "vault.key")
	}
	key, err := vaultKey(file)
	if err != nil {
		return nil, err
	}
	return openVault(s, key)
}

// openVault opens the vault of store with key. A key that does not open what
// the vault already holds is refused.
func openVault(s *Store, key []byte) (*Vault, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	v := &Vault{db: s.db, aead: aead, now: time.Now}
	check, err := v.get(vaultCheckName)
	switch {
	case errors.Is(err, errNoSecret):
		if err := v.put(vaultCheckName, vaultCheckText); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, errors.New("wrong vault key: it does not open the secrets already stored")
	case check != vaultCheckText:
		return nil, errors.New("wrong vault key")
	}
	return v, nil
}

var errNoSecret = errors.New("no such secret")

func (v *Vault) get(name string) (string, error) {
	var nonce, sealed []byte
	err := v.db.QueryRow(`SELECT nonce, value FROM vault WHERE name = ?`, name).Scan(&nonce, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("vault:%s: %w", name, errNoSecret)
	}
	if err != nil {
		return "", err
	}
	// The name is authenticated with the value: a value moved to another name does not open.
	plain, err := v.aead.Open(nil, nonce, sealed, []byte(name))
	if err != nil {
		return "", fmt.Errorf("vault:%s: cannot decrypt (wrong key?)", name)
	}
	return string(plain), nil
}

// set stores or replaces a secret.
func (v *Vault) set(name, value string) error {
	if !vaultName.MatchString(name) {
		return errors.New("a secret's name is lowercase letters, digits, dots, dashes and underscores")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("the secret is empty")
	}
	return v.put(name, value)
}

func (v *Vault) put(name, value string) error {
	nonce := make([]byte, v.aead.NonceSize())
	rand.Read(nonce)
	sealed := v.aead.Seal(nil, nonce, []byte(value), []byte(name))
	now := v.now().Unix()
	_, err := v.db.Exec(`INSERT INTO vault (name, nonce, value, created, updated) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET nonce = excluded.nonce, value = excluded.value, updated = excluded.updated`,
		name, nonce, sealed, now, now)
	return err
}

func (v *Vault) remove(name string) error {
	res, err := v.db.Exec(`DELETE FROM vault WHERE name = ? AND name != ?`, name, vaultCheckName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSecret
	}
	return nil
}

// list returns the secrets' names and dates, never their values.
func (v *Vault) list() ([]SecretInfo, error) {
	rows, err := v.db.Query(`SELECT name, created, updated FROM vault WHERE name != ? ORDER BY name`, vaultCheckName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretInfo{}
	for rows.Next() {
		var s SecretInfo
		var c, u int64
		if err := rows.Scan(&s.Name, &c, &u); err != nil {
			return nil, err
		}
		s.Created, s.Updated = time.Unix(c, 0), time.Unix(u, 0)
		out = append(out, s)
	}
	return out, rows.Err()
}

// importFile copies a secret file into the vault, once.
func (v *Vault) importFile(name, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return v.set(name, string(raw))
}

// secretRef is a configuration field that names a secret.
type secretRef struct {
	Field string // e.g. "providers.anthropic.secret"
	Ref   string // a file path, or vault:<name>
	Name  string // a name for the vault, from the field
}

func (r secretRef) InVault() bool { return strings.HasPrefix(r.Ref, vaultPrefix) }

// secretRefs lists every field of the configuration that names a secret.
func (c *Config) secretRefs() []secretRef {
	var out []secretRef
	add := func(field, ref, name string) {
		if ref != "" {
			out = append(out, secretRef{Field: field, Ref: ref, Name: name})
		}
	}
	add("token_file", c.TokenFile, "core-token")
	for name, p := range c.Providers {
		add("providers."+name+".secret", p.Secret, name)
	}
	for name, id := range c.Identities {
		add("identities."+name+".file", id.File, name)
	}
	if c.Brain != nil {
		for name, s := range c.Brain.MCPServers {
			add("brain.mcp_servers."+name+".token_file", s.TokenFile, name)
		}
	}
	for name, srv := range c.Servers {
		if srv.Auth != nil {
			add("servers."+name+".auth.bearer_file", srv.Auth.BearerFile, name)
			if o := srv.Auth.OAuth; o != nil {
				add("servers."+name+".auth.oauth.client_id_file", o.ClientIDFile, name+"-client-id")
				add("servers."+name+".auth.oauth.client_secret_file", o.ClientSecretFile, name+"-client-secret")
			}
		}
	}
	if specs, err := c.localServers(); err == nil {
		for name, spec := range specs {
			for env, v := range spec.Env {
				add("servers."+name+".run.env."+env+".file", v.File, name+"-"+strings.ToLower(strings.ReplaceAll(env, "_", "-")))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// Vault API (bearer token): names in, names out, never a value out.
func (c *core) vaultRoutes(mux *http.ServeMux, token string) {
	mux.Handle("GET /v1/secrets", bearer(token, http.HandlerFunc(c.handleSecrets)))
	mux.Handle("PUT /v1/secrets/{name}", bearer(token, http.HandlerFunc(c.handlePutSecret)))
	mux.Handle("DELETE /v1/secrets/{name}", bearer(token, http.HandlerFunc(c.handleDeleteSecret)))
}

func (c *core) handleSecrets(w http.ResponseWriter, _ *http.Request) {
	v := currentVault()
	if v == nil {
		http.Error(w, errNoVault.Error(), http.StatusServiceUnavailable)
		return
	}
	list, err := v.list()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handlePutSecret sets a secret from {value}, or from {file} to import one.
func (c *core) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	v := currentVault()
	if v == nil {
		http.Error(w, errNoVault.Error(), http.StatusServiceUnavailable)
		return
	}
	var in struct {
		Value string `json:"value"`
		File  string `json:"file"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		http.Error(w, "invalid JSON body: {value} or {file}", http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	var err error
	if in.File != "" {
		err = v.importFile(name, in.File)
	} else {
		err = v.set(name, in.Value)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "ref": vaultPrefix + name})
}

func (c *core) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	v := currentVault()
	if v == nil {
		http.Error(w, errNoVault.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := v.remove(r.PathValue("name")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errNoSecret) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

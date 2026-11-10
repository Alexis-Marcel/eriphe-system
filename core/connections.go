package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Connections: an account signed in at a provider (oauth_client.go), kept
// once per identity and shared by every tool that needs it. Its refresh
// token is in the vault (Secret); the tools' local servers read it from
// there in their environment, so which tools use a connection is read from
// their specs, never kept twice.
//
// eriphe-core checks each connection's token now and then
// (connectionTick): one its provider refuses (revoked, expired, or its app
// deleted) is lapsed; the tools that hold nothing else are then held as
// needs_auth (the pool's authHold, connectionHold), list_tools says where
// to reconnect, and the brain is told once. Signing in again lifts it.

const (
	connectionCheckEvery = 30 * time.Minute

	// Why a connection lapsed, as kept.
	lapsedRevoked   = "revoked or expired"
	lapsedBadClient = "the app it was connected with is refused"
	lapsedNoToken   = "no token"
)

func init() {
	extraSchema = append(extraSchema, `
CREATE TABLE IF NOT EXISTS connections (
  id TEXT PRIMARY KEY, provider TEXT NOT NULL, identity TEXT NOT NULL, scopes TEXT NOT NULL DEFAULT '[]',
  secret TEXT NOT NULL, lapsed TEXT NOT NULL DEFAULT '', checked INTEGER NOT NULL DEFAULT 0, created INTEGER NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS connections_identity ON connections (provider, identity COLLATE NOCASE);`)
}

// connection is an account signed in, as eriphe-core keeps it (its token is
// in the vault, never here).
type connection struct {
	ID       string
	Provider string   // a signInProviders name
	Identity string   // the account's address
	Scopes   []string // what the account granted, all tools together
	Secret   string   // the vault name of its token
	// Lapsed is why its provider refused the token at the last check, ""
	// while it works.
	Lapsed  string
	Checked int64 // unix seconds of the last check
}

// connectionWatch is the background check of the connections.
type connectionWatch struct {
	mu       sync.Mutex // one change of the connections at a time
	checking bool
}

// lapsedFR words a lapse for the brain, in French.
var lapsedFR = map[string]string{
	lapsedRevoked:   "révoquée, ou expirée",
	lapsedBadClient: "l'app avec laquelle il a été connecté est refusée",
	lapsedNoToken:   "son autorisation enregistrée est illisible",
}

const connectionColumns = `id, provider, identity, scopes, secret, lapsed, checked`

func scanConnection(sc interface{ Scan(...any) error }) (connection, error) {
	var cn connection
	var scopes string
	err := sc.Scan(&cn.ID, &cn.Provider, &cn.Identity, &scopes, &cn.Secret, &cn.Lapsed, &cn.Checked)
	json.Unmarshal([]byte(scopes), &cn.Scopes)
	return cn, err
}

// connections are all of them, by provider then identity.
func (c *core) connections() []connection {
	rows, err := c.store.db.Query(`SELECT ` + connectionColumns + ` FROM connections ORDER BY provider, identity COLLATE NOCASE`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []connection
	for rows.Next() {
		if cn, err := scanConnection(rows); err == nil {
			out = append(out, cn)
		}
	}
	return out
}

// connectionsAt are a provider's.
func (c *core) connectionsAt(provider string) []connection {
	return slices.DeleteFunc(c.connections(), func(cn connection) bool { return cn.Provider != provider })
}

// connectionOf finds an identity's connection at a provider.
func (c *core) connectionOf(provider, identity string) (connection, bool) {
	cn, err := scanConnection(c.store.db.QueryRow(`SELECT `+connectionColumns+` FROM connections WHERE provider = ? AND identity = ? COLLATE NOCASE`,
		provider, strings.TrimSpace(identity)))
	return cn, err == nil
}

// newConnectionID is a connection's id, which names its token in the vault.
func newConnectionID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// keepConnection keeps what a sign-in gave: the token in the vault (under
// the name it already has, for an identity connected before), the scopes,
// and the connection working again. It says whether it was lapsed.
func (c *core) keepConnection(provider, identity string, scopes []string, token string) (connection, bool, error) {
	v := currentVault()
	if v == nil {
		return connection{}, false, &catalogError{http.StatusServiceUnavailable, "accounts.novault", "", errNoVault}
	}
	c.conns.mu.Lock()
	defer c.conns.mu.Unlock()
	was, ok := c.connectionOf(provider, identity)
	cn := connection{ID: was.ID, Provider: provider, Identity: identity, Scopes: scopes, Secret: was.Secret, Checked: time.Now().Unix()}
	if !ok {
		cn.ID = newConnectionID()
		cn.Secret = "connection." + cn.ID + ".token"
	}
	if err := v.set(cn.Secret, token); err != nil {
		return connection{}, false, err
	}
	raw, _ := json.Marshal(scopes)
	_, err := c.store.db.Exec(`INSERT INTO connections (id, provider, identity, scopes, secret, lapsed, checked, created)
		VALUES (?, ?, ?, ?, ?, '', ?, ?) ON CONFLICT (id) DO UPDATE SET identity = excluded.identity, scopes = excluded.scopes,
		lapsed = '', checked = excluded.checked`, cn.ID, provider, identity, string(raw), cn.Secret, cn.Checked, cn.Checked)
	return cn, was.Lapsed != "", err
}

// dropConnection forgets a connection: its token in the vault, then its
// authorization at the provider. Its tools are the caller's to remove first.
func (c *core) dropConnection(ctx context.Context, cn connection) {
	if v := currentVault(); v != nil {
		if raw, err := v.get(cn.Secret); err == nil {
			if p := signInProviders[cn.Provider]; p != nil {
				p.revoke(ctx, raw)
			}
		}
		v.remove(cn.Secret)
	}
	c.conns.mu.Lock()
	c.store.db.Exec(`DELETE FROM connections WHERE id = ?`, cn.ID)
	c.conns.mu.Unlock()
}

// specSecrets are the vault secrets a local server's spec reads.
func specSecrets(spec RunSpec) []string {
	var refs []string
	for _, v := range spec.Env {
		if name, ok := strings.CutPrefix(v.File, vaultPrefix); ok {
			refs = append(refs, name)
		}
	}
	sort.Strings(refs)
	return refs
}

// connectionTools are the catalog's local servers that read a
// connection's token, by name.
func (c *core) connectionTools(cn connection) []string {
	var out []string
	for name, s := range c.conf().Servers {
		if !s.local() {
			continue
		}
		if spec, err := decodeRun(s.Run); err == nil && slices.Contains(specSecrets(spec), cn.Secret) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// connectionsOf are the connections whose token a server reads.
func (c *core) connectionsOf(s Server) []connection {
	if !s.local() {
		return nil
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return nil
	}
	refs := specSecrets(spec)
	var out []connection
	for _, cn := range c.connections() {
		if slices.Contains(refs, cn.Secret) {
			out = append(out, cn)
		}
	}
	return out
}

// connectionHold is why a server waits for its account to be connected
// again, and where the user does it; "" when it does not wait. It is the
// pool's authHold.
func (c *core) connectionHold(name string) (why, where string) {
	s, ok := c.conf().Servers[name]
	if !ok {
		return "", ""
	}
	// Held when every account it holds is a connection lapsed: a drive of
	// two accounts still serves the folders of the one that works, the mail
	// still reads the other mailboxes.
	owners := c.connectionsOf(s)
	if _, cfg, ok := accountsOf(s); ok && len(cfg.Accounts) > len(owners) {
		return "", ""
	}
	var ids []string
	for _, cn := range owners {
		if cn.Lapsed == "" {
			return "", ""
		}
		ids = append(ids, cn.Identity)
	}
	if len(ids) == 0 {
		return "", ""
	}
	list, label := strings.Join(ids, ", "), providerLabel(owners[0].Provider)
	return fmt.Sprintf("the %s account %s must be connected again (%s refused its authorization: %s)", label, list, label, owners[0].Lapsed),
		"Settings → Tools → " + label + " → Reconnect " + list
}

// providerLabel is a provider's name as the user knows it.
func providerLabel(name string) string {
	if p := signInProviders[name]; p != nil {
		return p.Label
	}
	return name
}

// connectionWoken lets a connection's servers connect again.
func (c *core) connectionWoken(cn connection) {
	for _, name := range c.connectionTools(cn) {
		if u := c.pool.get(name); u != nil {
			u.reconnect()
		}
	}
}

// The check of the connections.

// connectionTick checks the connections due, in the background: called
// with the nodes' check (every 15 s), it does something every
// connectionCheckEvery. It also collects the tokens no connection keeps.
func (c *core) connectionTick(ctx context.Context, now time.Time) {
	due := false
	for _, cn := range c.connections() {
		due = due || now.Sub(time.Unix(cn.Checked, 0)) >= connectionCheckEvery
	}
	if !due {
		return
	}
	c.conns.mu.Lock()
	if c.conns.checking {
		c.conns.mu.Unlock()
		return
	}
	c.conns.checking = true
	c.conns.mu.Unlock()
	go func() {
		defer func() { c.conns.mu.Lock(); c.conns.checking = false; c.conns.mu.Unlock() }()
		for _, cn := range c.connections() {
			if now.Sub(time.Unix(cn.Checked, 0)) >= connectionCheckEvery {
				c.checkConnection(ctx, cn)
			}
		}
		c.collectConnectionSecrets()
	}()
}

// checkConnection asks the provider for an access token with the
// connection's refresh token, and marks it lapsed if the provider refuses
// it: it returns why ("" when it works). A provider out of reach changes
// nothing, and returns its error.
func (c *core) checkConnection(ctx context.Context, cn connection) (string, error) {
	p := signInProviders[cn.Provider]
	if p == nil {
		return "", errors.New("no such provider")
	}
	why := lapsedNoToken
	if raw, err := readSecret(vaultPrefix + cn.Secret); err == nil {
		res, err := p.checkRefresh(ctx, raw)
		if err != nil {
			return "", err // out of reach: checked again later
		}
		why = map[int]string{refreshWorks: "", refreshRevoked: lapsedRevoked, refreshBadClient: lapsedBadClient, refreshUnreadable: lapsedNoToken}[res]
	}
	c.markConnection(cn.ID, why)
	return why, nil
}

// markConnection records the outcome of a check; the first refusal holds
// the connection's servers and tells the brain.
func (c *core) markConnection(id, why string) {
	c.conns.mu.Lock()
	cn, err := scanConnection(c.store.db.QueryRow(`SELECT `+connectionColumns+` FROM connections WHERE id = ?`, id))
	if err != nil {
		c.conns.mu.Unlock()
		return
	}
	c.store.db.Exec(`UPDATE connections SET lapsed = ?, checked = ? WHERE id = ?`, why, time.Now().Unix(), id)
	c.conns.mu.Unlock()
	if cn.Lapsed == why {
		return
	}
	c.connectionWoken(cn) // held, or free again
	if why == "" {
		return
	}
	log.Printf("connections: an account must be connected again (%s)", why)
	names := c.connectionTools(cn)
	if c.conf().brain() == nil || len(names) == 0 {
		return
	}
	user := c.conf().User
	if user == "" {
		user = "l'utilisateur"
	}
	label := providerLabel(cn.Provider)
	text := fmt.Sprintf("%s a retiré l'autorisation du compte %s (%s) : les outils %s ne marchent plus pour ce compte tant que %s ne l'a pas reconnecté (Réglages › Outils › %s › Reconnecter, un clic). Dis-le-lui une fois, au bon moment ; ne lance pas de tâche qui en a besoin d'ici là.",
		label, cn.Identity, lapsedFR[why], strings.Join(names, ", "), user, label)
	if _, err := c.store.addMessage(roleEvent, text, false); err == nil {
		c.ping()
	}
}

// collectConnectionSecrets removes the connections' tokens left in the
// vault with no connection and no tool reading them: those of accounts
// removed before connections were kept.
func (c *core) collectConnectionSecrets() {
	v := currentVault()
	if v == nil {
		return
	}
	list, err := v.list()
	if err != nil {
		return
	}
	kept := map[string]bool{}
	for _, cn := range c.connections() {
		kept[cn.Secret] = true
	}
	for _, s := range c.conf().Servers {
		if spec, err := decodeRun(s.Run); s.local() && err == nil {
			for _, ref := range specSecrets(spec) {
				kept[ref] = true
			}
		}
	}
	for _, s := range list {
		if isConnectionSecret(s.Name) && !kept[s.Name] {
			v.remove(s.Name)
			log.Printf("connections: a token no account keeps was removed from the vault")
		}
	}
}

// isConnectionSecret tells a connection's token by its vault name: the
// ones made now, and the ones accounts had before connections
// (legacySecretPrefix).
func isConnectionSecret(name string) bool {
	parts := strings.Split(name, ".")
	return len(parts) == 3 && (parts[0] == "connection" || parts[0] == legacySecretPrefix) && parts[2] == "token"
}

// addTokenURL writes where to refresh a kept token in it, if it does not
// say.
func addTokenURL(v *Vault, name, tokenURL string) {
	raw, err := v.get(name)
	if err != nil {
		return
	}
	var tok map[string]any
	if json.Unmarshal([]byte(raw), &tok) != nil || tok["token_url"] != nil {
		return
	}
	tok["token_url"] = tokenURL
	out, _ := json.Marshal(tok)
	if err := v.set(name, string(out)); err != nil {
		log.Printf("connections: a token kept as before: %v", err)
	}
}

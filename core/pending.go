package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// Adding an OAuth server from the workspace is one action: the form's
// "Connect to <service>" checks the entry, keeps it pending and starts the
// authorization at once. A pending server is not in the catalog: not
// listed, not lent to tasks nor to the brain. It is saved only once the
// authorization succeeds, with the tokens it got; if the user refuses, or
// the flow fails or expires, it is dropped, and an edited server keeps its
// previous version. Nothing pending outlives the process: a flow cannot
// resume after a restart anyway.

// pendingTimeout bounds a pending server's authorization.
var pendingTimeout = 15 * time.Minute

type pendingServer struct {
	mode  string // putCreate or putUpdate
	name  string
	srv   Server
	creds credentials
	u     *upstream // its own connection, under pendingKey(name)
	flow  *authFlow
	stop  context.CancelFunc
}

// pendingKey is the name a pending server's OAuth client and tokens are
// kept under until it is saved.
func pendingKey(name string) string { return "pending~" + name }

// checkServer runs putServer's checks without saving anything.
func (c *core) checkServer(mode, name string, s Server, creds credentials) error {
	raw, _ := json.Marshal(s)
	var cp Server
	json.Unmarshal(raw, &cp) // binding writes into Auth: work on a copy
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	_, inBase := c.base.Servers[name]
	_, inDyn := c.dyn.Servers[name]
	if err := checkPut(mode, kindServer, name, inBase, inDyn); err != nil {
		return err
	}
	writes, err := bindServerSecrets(name, &cp, creds)
	if err != nil {
		return err
	}
	if err := checkVaultOnly("server "+name, serverRefs(cp)...); err != nil {
		return err
	}
	if len(writes) > 0 && currentVault() == nil {
		return &catalogError{http.StatusServiceUnavailable, "accounts.novault", "", errNoVault}
	}
	d := c.dyn.clone()
	d.Servers[name] = cp
	if err := mergeCatalog(c.base, d).validate(); err != nil {
		return errInvalid(err)
	}
	return nil
}

// startPending checks an OAuth server, keeps it pending and starts its
// authorization, redirected to redirect and bound to the browser by
// binding. It returns the authorization URL, or "" if the server
// connected without one, in which case it is saved already.
func (c *core) startPending(ctx context.Context, mode, name string, s Server, creds credentials, redirect, binding string) (string, error) {
	if s.authKind() != "oauth" {
		return "", errors.New("only an OAuth server is connected before it is saved")
	}
	if err := c.checkServer(mode, name, s, creds); err != nil {
		return "", err
	}
	key := pendingKey(name)
	u := &upstream{p: c.pool, name: key, srv: s, status: statusConnecting, wake: make(chan struct{}, 1),
		clientID: strings.TrimSpace(creds["client-id"]), clientSecret: strings.TrimSpace(creds["client-secret"])}
	f := newAuthFlow(redirect)
	f.binding = binding
	u.flow = f
	pctx, stop := context.WithTimeout(context.Background(), pendingTimeout)
	ps := &pendingServer{mode: mode, name: name, srv: s, creds: creds, u: u, flow: f, stop: stop}
	c.pool.mu.Lock()
	old := c.pool.pending[name]
	c.pool.pending[name] = ps
	c.pool.mu.Unlock()
	if old != nil {
		old.stop() // a new try replaces it
		<-old.flow.done
	}
	c.store.forgetOAuth(key)
	go c.runPending(pctx, ps)
	select {
	case authURL := <-f.urls:
		return authURL, nil
	case <-f.done:
		return "", f.err
	case <-time.After(time.Minute):
		stop()
		return "", errors.New("the server did not ask for an authorization in time")
	case <-ctx.Done():
		stop()
		return "", ctx.Err()
	}
}

// runPending connects the pending server once: on success it is saved
// with its tokens, else dropped. The flow ends after either.
func (c *core) runPending(ctx context.Context, ps *pendingServer) {
	defer ps.stop()
	u, f := ps.u, ps.flow
	sess, err := u.open(ctx)
	if err == nil {
		sess.Close()
		err = c.promote(ps)
	}
	key := pendingKey(ps.name)
	c.pool.mu.Lock()
	if c.pool.pending[ps.name] == ps {
		delete(c.pool.pending, ps.name)
	}
	c.pool.mu.Unlock()
	u.mu.Lock()
	u.session, u.flow = nil, nil
	if err != nil {
		u.status, u.err = statusError, err.Error()
	}
	u.mu.Unlock()
	if err != nil {
		c.store.forgetOAuth(key)
		log.Printf("server %s: not saved, its authorization failed: %v", ps.name, err)
	}
	f.finish(err)
}

// promote saves a pending server that connected, and hands it its tokens.
func (c *core) promote(ps *pendingServer) error {
	if err := c.putServer(ps.mode, ps.name, ps.srv, ps.creds); err != nil {
		return err
	}
	c.store.moveOAuth(pendingKey(ps.name), ps.name)
	if u := c.pool.get(ps.name); u != nil {
		u.reconnect() // it may have started before its tokens were there
	}
	log.Printf("server %s: authorized and saved", ps.name)
	return nil
}

// pendingOf is the pending server an upstream connects, if any.
func (p *pool) pendingOf(u *upstream) *pendingServer {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ps := range p.pending {
		if ps.u == u {
			return ps
		}
	}
	return nil
}

// moveOAuth hands an OAuth client and tokens over to another name.
func (s *Store) moveOAuth(from, to string) {
	s.db.Exec(`INSERT OR REPLACE INTO oauth_tokens (server, client, token, updated)
		SELECT ?, client, token, updated FROM oauth_tokens WHERE server = ?`, to, from)
	s.db.Exec(`INSERT OR REPLACE INTO oauth_clients (server, client_id, client_secret, redirect_uri, created)
		SELECT ?, client_id, client_secret, redirect_uri, created FROM oauth_clients WHERE server = ?`, to, from)
	s.forgetOAuth(from)
}

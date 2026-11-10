package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// Tools on a connection: a tool says, as data, what it needs of an account
// signed in at a provider (its card's SignIns, tool_cards.go): the scopes
// for reading and for writing, and what its account is in its
// configuration. One sign-in at a provider can serve several tools at once
// (the mail, the calendar and the drive of one account): it asks for the
// scopes of the tools ticked only, the provider keeping what was granted
// before, and the connection's token is then read by each tool's server
// from the vault, in its environment:
//
//	mail      eriphe-mail, an account of the one mail server
//	calendar  eriphe-calendar, an account of the one calendar server
//	drive     eriphe-files, an account of the one files server
//
// Each has a card (tool_cards.go): an account is put in its server
// and taken out of it the same way for the three.
//
// Nothing here names a provider: the tools a provider serves, and how,
// are rows of the tools' cards.

// The kinds of tools a connection can serve.
const (
	toolMail     = "mail"     // mailService
	toolCalendar = "calendar" // calendarService
	toolDrive    = "drive"    // filesService
)

// toolKinds are the kinds, in the order they are shown.
var toolKinds = []string{toolMail, toolCalendar, toolDrive}

// toolSignIn is what a kind of tool needs of a connection at a provider.
type toolSignIn struct {
	Tool     string   // a toolKinds
	Provider string   // a signInProviders name
	Read     []string // the scopes it reads with
	// Write are the scopes it also writes with, asked instead of Read
	// when it may write; none when Read does both.
	Write []string
	// Account is its account in the tool's configuration: Name is the
	// identity; AccountAddress and AccountUsername set those to it too.
	AccountProvider string
	AccountAddress  bool
	AccountUsername bool
}

// toolSignIns are what each kind of tool needs of an account at a
// provider: its card's (tool_cards.go).
func toolSignIns() []toolSignIn {
	var out []toolSignIn
	for _, card := range toolCards {
		for _, t := range card.SignIns {
			t.Tool = card.ID
			out = append(out, t)
		}
	}
	return out
}

// signInOf finds what a kind of tool needs at a provider.
func signInOf(tool, provider string) (toolSignIn, bool) {
	for _, t := range toolSignIns() {
		if t.Tool == tool && t.Provider == provider {
			return t, true
		}
	}
	return toolSignIn{}, false
}

// signInByAccount finds the provider an account of a kind of tool is
// signed in at, from what its configuration names (AccountProvider): a
// Gmail's "gmail", a Google calendar's "google".
func signInByAccount(tool, accountProvider string) (toolSignIn, bool) {
	for _, t := range toolSignIns() {
		if t.Tool == tool && t.AccountProvider == accountProvider {
			return t, true
		}
	}
	return toolSignIn{}, false
}

// productName is what a kind of tool is called at a provider ("Gmail"),
// from the words of the workspace; the provider's name when it has none.
func productName(lang, provider, kind string) string {
	key := "conn.product." + provider + "." + kind
	if s := tr(lang, key); s != key {
		return s
	}
	return providerLabel(provider)
}

// toolsAt are the kinds of tools a provider serves, in their order.
func toolsAt(provider string) []string {
	var out []string
	for _, k := range toolKinds {
		if _, ok := signInOf(k, provider); ok {
			out = append(out, k)
		}
	}
	return out
}

// scopes are the scopes to ask for.
func (t toolSignIn) scopes(write bool) []string {
	if write && len(t.Write) > 0 {
		return t.Write
	}
	return t.Read
}

// granted tells whether the scopes granted serve the tool, and whether
// they let it write.
func (t toolSignIn) granted(scopes []string) (ok, write bool) {
	has := func(want []string) bool {
		return len(want) > 0 && !slices.ContainsFunc(want, func(s string) bool { return !slices.Contains(scopes, s) })
	}
	write = has(t.Write)
	return write || has(t.Read), write || len(t.Write) == 0 && has(t.Read)
}

// account is the identity's account in the tool's configuration.
func (t toolSignIn) account(identity string) svcAccount {
	a := svcAccount{Name: identity, Provider: t.AccountProvider}
	if t.AccountAddress {
		a.Address = identity
	}
	if t.AccountUsername {
		a.Username = identity
	}
	return a
}

// toolKindOf tells which kind of tool a server's image gives, or "".
func toolKindOf(spec RunSpec) string {
	svc, _ := serviceOf(spec)
	return svc.ID
}

// connTool is a catalog server that reads a connection's token.
type connTool struct {
	Name string
	Kind string // a toolKinds
}

// connectionKinds are a connection's servers, by kind.
func (c *core) connectionKinds(cn connection) []connTool {
	var out []connTool
	for _, name := range c.connectionTools(cn) {
		spec, _ := decodeRun(c.conf().Servers[name].Run)
		out = append(out, connTool{Name: name, Kind: toolKindOf(spec)})
	}
	sort.Slice(out, func(i, j int) bool {
		return slices.Index(toolKinds, out[i].Kind) < slices.Index(toolKinds, out[j].Kind) ||
			out[i].Kind == out[j].Kind && out[i].Name < out[j].Name
	})
	return out
}

// connectionBySecret is the connection an account of a server is on: the
// one whose token its secret variable reads.
func (c *core) connectionBySecret(st svcServer, a svcAccount) (connection, bool) {
	if !a.signedIn() {
		return connection{}, false
	}
	if st.Spec.Image == "" {
		st.Spec, _ = decodeRun(st.Server.Run)
	}
	name, ok := strings.CutPrefix(st.Spec.Env[a.secretEnv()].File, vaultPrefix)
	if !ok {
		return connection{}, false
	}
	for _, cn := range c.connections() {
		if cn.Secret == name {
			return cn, true
		}
	}
	return connection{}, false
}

// kindReadOnly tells whether the tool of a kind is set to read only: it
// then gets a token that reads, whatever the page asked.
func (c *core) kindReadOnly(kind string) bool {
	svc, ok := serviceByID(kind)
	if !ok {
		return false
	}
	st, ok := c.serviceServer(svc)
	return ok && st.Server.level() == accessReadOnly
}

// The sign-in.

// connectAsk is what the workspace's Connect asks.
type connectAsk struct {
	Provider string
	Origin   string   // the workspace, as the browser reaches it
	Binding  string   // hash of the browser's flow cookie
	Tools    []string // the kinds of tools: at least one the provider serves
	Write    bool     // ask for the scopes that also write, where a tool has them
	Hint     string   // the account, to add a tool to it or reconnect it
	Back     string   // the page the browser goes back to
	Lang     string   // the words of the tools' use, if they are created
}

// connectScopes are the tools' scopes a Connect asks for, beside the
// identity, which the sign-in asks for.
func (c *core) connectScopes(in connectAsk) []string {
	var scopes []string
	for _, k := range toolsAt(in.Provider) {
		if t, _ := signInOf(k, in.Provider); slices.Contains(in.Tools, k) {
			scopes = append(scopes, t.scopes(in.Write && !c.kindReadOnly(k))...)
		}
	}
	return scopes
}

// startAccountSignIn starts a sign-in for tools and returns where to send the
// browser.
func (c *core) startAccountSignIn(in connectAsk) (string, error) {
	p := signInProviders[in.Provider]
	if p == nil {
		return "", &catalogError{http.StatusNotFound, "", "", errors.New("no such provider")}
	}
	in.Tools = slices.DeleteFunc(slices.Clone(in.Tools), func(k string) bool { return !slices.Contains(toolsAt(p.Name), k) })
	if len(in.Tools) == 0 {
		return "", &catalogError{http.StatusBadRequest, "conn.notool", "", errors.New("pick at least one service")}
	}
	app, err := appClient(p)
	if errors.Is(err, errNoAppClient) {
		return "", &catalogError{http.StatusBadRequest, "conn.noclient", "", err}
	}
	if err != nil {
		return "", err
	}
	return c.startSignIn(p, app, signInStart{Origin: in.Origin, Binding: in.Binding,
		Scopes: c.connectScopes(in), Hint: in.Hint, Lang: in.Lang, Back: in.Back, Asked: in.Tools})
}

// connectResult is what a sign-in did.
type connectResult struct {
	Provider string
	Identity string
	Added    []string // the tools connected now
	Missing  []string // the tools asked for that the provider did not grant
	Back     string
}

// finishAccountSignIn takes the provider's answer, as the workspace's callback
// received it: the sign-in checks it and exchanges the code, then the
// connection is kept and given to the tools asked for.
func (c *core) finishAccountSignIn(ctx context.Context, origin, state, code, errCode, binding string) (connectResult, error) {
	fl, tok, err := c.finishSignIn(ctx, origin, state, code, errCode, binding)
	var res connectResult
	if fl != nil {
		res.Back, res.Provider = fl.back, fl.provider.Name
	}
	if err != nil {
		return res, err
	}
	res.Identity = tok.Identity
	for _, k := range fl.asked {
		if t, ok := signInOf(k, res.Provider); ok {
			if granted, _ := t.granted(tok.Scopes); granted {
				res.Added = append(res.Added, k)
				continue
			}
		}
		res.Missing = append(res.Missing, k)
	}
	if len(res.Added) == 0 {
		return res, &catalogError{http.StatusBadRequest, "conn.noscope", "", errors.New("none of the services asked for was granted")}
	}
	return res, c.connectTools(fl.lang, res.Provider, tok.Identity, tok.Scopes, tok.secret(fl), res.Added)
}

// connectTools keeps a connection's token and makes its tools read it: the
// ones it has already pick the new token up, the ones added are created.
func (c *core) connectTools(lang, provider, identity string, scopes []string, secret string, tools []string) error {
	cn, wasLapsed, err := c.keepConnection(provider, identity, scopes, secret)
	if err != nil {
		return err
	}
	log.Printf("connections: an account connected (%s)", strings.Join(tools, ", ")) // never the address nor the token

	have := map[string]bool{}
	for _, t := range c.connectionKinds(cn) {
		have[t.Kind] = true
	}
	ref := &EnvValue{File: vaultPrefix + cn.Secret}
	var errs []error
	for _, k := range tools {
		t, _ := signInOf(k, provider)
		svc, ok := serviceByID(k)
		if !ok || have[k] && !svc.Scopes { // a service told the scopes is told them again: they may have changed
			continue
		}
		errs = append(errs, c.putAccount(svc, accountPut{Lang: lang, Account: t.account(identity), Secret: ref, Scopes: scopes}))
	}
	if wasLapsed {
		c.connectionWoken(cn)
	}
	c.bump() // the nodes fetch the services' new environment
	return errors.Join(errs...)
}

// removeConnection takes an account away: its tools (and its places), its
// token in the vault, then its authorization at the provider.
func (c *core) removeConnection(ctx context.Context, provider, identity string) error {
	cn, ok := c.connectionOf(provider, identity)
	if !ok {
		return &catalogError{http.StatusNotFound, "conn.unknown", "", fmt.Errorf("no such %s account", providerLabel(provider))}
	}
	var errs []error
	for _, t := range c.connectionKinds(cn) {
		if svc, ok := serviceByID(t.Kind); ok && c.source(kindServer, t.Name) == sourceWorkspace {
			errs = append(errs, c.dropAccount(svc, identity))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	c.dropConnection(ctx, cn)
	log.Printf("connections: an account removed")
	return nil
}

// freeServerName is a name free in the catalog: base, then
// base-<the address's local part>.
func (c *core) freeServerName(base, email string) string {
	taken := func(n string) bool { _, ok := c.conf().Servers[n]; return ok }
	if !taken(base) {
		return base
	}
	local, _, _ := strings.Cut(email, "@")
	stem := base + "-" + slugName(local)
	if len(stem) > 28 {
		stem = strings.TrimRight(stem[:28], "-")
	}
	if stem == base+"-" {
		stem = base
	}
	if !taken(stem) && serverName.MatchString(stem) {
		return stem
	}
	for n := 2; ; n++ {
		if s := fmt.Sprintf("%s-%d", stem, n); !taken(s) {
			return s
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// One server for each service, whatever the number of accounts: the mail
// is one eriphe-mail with every mailbox, the calendar one eriphe-calendar
// with every calendar account, the files one eriphe-files with every
// drive, so the brain searches all the mail, lists the week's events and
// finds free time across every account in one call. The servers take
// several accounts (servers/mail, servers/calendar, servers/files); each
// tool names the account it touches in its "account" argument, and refuses
// ids of another.
//
// An account is added to the server (made the first time), changed or
// taken out of it (the server goes with the last one), from the account's
// form or from a connection (connection_tools.go). Each account keeps its own access
// level (Server.AccountAccess, access.go): the server is also told to keep
// read only the accounts whose level is read only. Its secret is a
// variable of its own in the server's environment: a password bound by the
// catalog (TOOL_PASSWORD_<hash>), or the connection's token in the vault
// (TOOL_TOKEN_<hash>). The configuration and its keys are the contract of
// Eriphe's tool servers (servers/README.md).

// The contract's variables (servers/README.md): the configuration, and the
// stems of an account's secret, followed by _<hash> (accountEnv).
const (
	toolConfigEnv   = "TOOL_CONFIG"
	toolPasswordEnv = "TOOL_PASSWORD"
	toolTokenEnv    = "TOOL_TOKEN"
)

// serviceOf finds the service a server's image gives.
func serviceOf(spec RunSpec) (toolCard, bool) {
	for _, svc := range toolCards {
		if strings.HasPrefix(spec.Image, svc.Image) {
			return svc, true
		}
	}
	return toolCard{}, false
}

// hasAccounts says whether a server is a tool with a card, whose
// accounts its setup tools test.
func hasAccounts(srv Server) bool {
	if !srv.local() {
		return false
	}
	spec, err := decodeRun(srv.Run)
	if err != nil {
		return false
	}
	_, ok := serviceOf(spec)
	return ok
}

// serviceByID finds a service by its id (mail, calendar).
func serviceByID(id string) (toolCard, bool) {
	for _, svc := range toolCards {
		if svc.ID == id {
			return svc, true
		}
	}
	return toolCard{}, false
}

// placesKey is where its configuration keeps its places, "" without
// (places.go).
func (svc toolCard) placesKey() string {
	if svc.Places == nil {
		return ""
	}
	return svc.Places.Key
}

// use is the text key of what it is for, and mark its icon.
func (svc toolCard) use() string  { return "preset." + svc.Preset + ".use" }
func (svc toolCard) mark() string { return svc.Preset }

// svcAccount is an account as the tool's server reads it: what eriphe-core
// itself reads of every account, and the rest as the form or the sign-in
// made it (Fields: "address", "url", "imap": {"host", "port"}…).
type svcAccount struct {
	Name     string
	Provider string // the provider the server knows it at, if any
	Auth     svcAuth
	ReadOnly bool
	Scopes   []string // what its connection granted, for a server told (Scopes)
	Fields   map[string]any
}

// svcAccountKeys are the keys of svcAccount's own fields.
var svcAccountKeys = []string{"name", "provider", "auth", "read_only", "scopes"}

func (a *svcAccount) UnmarshalJSON(b []byte) error {
	var all map[string]any
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	var own struct {
		Name     string   `json:"name"`
		Provider string   `json:"provider"`
		Auth     svcAuth  `json:"auth"`
		ReadOnly bool     `json:"read_only"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.Unmarshal(b, &own); err != nil {
		return err
	}
	for _, k := range svcAccountKeys {
		delete(all, k)
	}
	*a = svcAccount{Name: own.Name, Provider: own.Provider, Auth: own.Auth, ReadOnly: own.ReadOnly, Scopes: own.Scopes, Fields: all}
	return nil
}

func (a svcAccount) MarshalJSON() ([]byte, error) {
	out := maps.Clone(a.Fields)
	if out == nil {
		out = map[string]any{}
	}
	out["name"], out["auth"] = a.Name, a.Auth
	if a.Provider != "" {
		out["provider"] = a.Provider
	}
	if a.ReadOnly {
		out["read_only"] = true
	}
	if len(a.Scopes) > 0 {
		out["scopes"] = a.Scopes
	}
	return json.Marshal(out)
}

// get is a field's value as text, by its key ("imap.host"); "" without.
func (a svcAccount) get(key string) string {
	if key == "provider" {
		return a.Provider
	}
	var v any = a.Fields
	for _, k := range strings.Split(key, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[k]
	}
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// set gives a field its value, by its key.
func (a *svcAccount) set(key string, v any) {
	if key == "provider" {
		a.Provider = fmt.Sprint(v)
		return
	}
	if a.Fields == nil {
		a.Fields = map[string]any{}
	}
	parts := strings.Split(key, ".")
	m := a.Fields
	for _, k := range parts[:len(parts)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		} else {
			next = maps.Clone(next)
			m[k] = next
		}
		m = next
	}
	if n, ok := v.(int); ok {
		v = float64(n)
	}
	m[parts[len(parts)-1]] = v
}

type svcAuth struct {
	PasswordEnv string `json:"password_env,omitempty"`
	TokenEnv    string `json:"token_env,omitempty"`
}

// secretEnv is the variable of the account's secret.
func (a svcAccount) secretEnv() string {
	return cmpOr(a.Auth.PasswordEnv, a.Auth.TokenEnv)
}

// signedIn tells an account on a connection's token.
func (a svcAccount) signedIn() bool { return a.Auth.TokenEnv != "" }

// svcConfig is the server's configuration; what eriphe-core does not
// change in it (the files' places, a server's options) is kept as it is.
type svcConfig struct {
	Accounts []svcAccount `json:"accounts"`
	rest     map[string]json.RawMessage
}

func (cfg *svcConfig) UnmarshalJSON(b []byte) error {
	type plain svcConfig
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &p.rest); err != nil {
		return err
	}
	delete(p.rest, "accounts")
	*cfg = svcConfig(p)
	return nil
}

func (cfg svcConfig) MarshalJSON() ([]byte, error) {
	out := maps.Clone(cfg.rest)
	if out == nil {
		out = map[string]json.RawMessage{}
	}
	accounts := cfg.Accounts
	if accounts == nil {
		accounts = []svcAccount{}
	}
	out["accounts"], _ = json.Marshal(accounts)
	return json.Marshal(out)
}

// option is one of the server's options in its configuration, as text.
func (cfg svcConfig) option(key string) string {
	var v string
	json.Unmarshal(cfg.rest[key], &v)
	return v
}

// withOption is the configuration with an option set ("" takes it out).
func (cfg svcConfig) withOption(key, v string) svcConfig {
	cfg.rest = maps.Clone(cfg.rest)
	if cfg.rest == nil {
		cfg.rest = map[string]json.RawMessage{}
	}
	if v == "" {
		delete(cfg.rest, key)
	} else {
		cfg.rest[key], _ = json.Marshal(v)
	}
	return cfg
}

// sameName tells two accounts of one name that are also the same login
// (its NameBy field): a calendar named apart is still the one typed again.
func (svc toolCard) sameName(x, y svcAccount) bool {
	if svc.Account == nil {
		return true
	}
	k := svc.Account.key(svc.Account.NameBy)
	return strings.EqualFold(x.get(k), y.get(k))
}

// withPlaces is the configuration with a change of the places it keeps
// under key (places.go); a configuration without them is left as it is.
func (cfg svcConfig) withPlaces(key string, change func([]Place) []Place) svcConfig {
	if key == "" {
		return cfg
	}
	var list []Place
	json.Unmarshal(cfg.rest[key], &list)
	list = change(list)
	cfg.rest = maps.Clone(cfg.rest)
	if cfg.rest == nil {
		cfg.rest = map[string]json.RawMessage{}
	}
	if len(list) == 0 {
		delete(cfg.rest, key)
	} else {
		cfg.rest[key], _ = json.Marshal(list)
	}
	return cfg
}

func (cfg svcConfig) index(name string) int {
	return slices.IndexFunc(cfg.Accounts, func(a svcAccount) bool { return strings.EqualFold(a.Name, name) })
}

// sameAccount tells whether two accounts of one name are one account: a
// mailbox is its address, whoever connects it; a calendar is also where it
// is (an Apple account and a Google account can have the same address,
// and are two calendars).
func (svc toolCard) sameAccount(x, y svcAccount) bool {
	for _, k := range svc.SameBy {
		if !strings.EqualFold(x.get(k), y.get(k)) {
			return false
		}
	}
	return true
}

// freeName is a name for a that no other account has (skip is its own
// place, -1 for a new one): its own, else with where it is
// ("sam@example.org (iCloud)"), else numbered.
func (svc toolCard) freeName(cfg svcConfig, a svcAccount, skip int) string {
	taken := func(n string) bool { i := cfg.index(n); return i >= 0 && i != skip }
	if !taken(a.Name) {
		return a.Name
	}
	where := cmpOr(a.Provider, "2")
	if s := svc.Account; s != nil {
		if c := s.choiceOf(a); c.ID != "" && !c.Custom {
			where = tr("en", s.Words+"."+c.ID) // a provider of its own: "iCloud"
		} else if u, err := url.Parse(a.get(s.key(s.NameHost))); s.NameHost != "" && err == nil && u.Hostname() != "" {
			where = u.Hostname()
		}
	}
	base := a.Name + " (" + where + ")"
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		if s := fmt.Sprintf("%s %d", base, n); !taken(s) {
			return s
		}
	}
}

// accountEnv is the variable of an account's secret: the stem, and a hash
// of the account's name (a name is an address, not a variable).
func accountEnv(stem, name string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return stem + "_" + strings.ToUpper(hex.EncodeToString(h[:4]))
}

// svcConfigOf reads a server's configuration, if it is a server of the
// service.
func svcConfigOf(svc toolCard, spec RunSpec) (svcConfig, bool) {
	if !strings.HasPrefix(spec.Image, svc.Image) {
		return svcConfig{}, false
	}
	var cfg svcConfig
	if json.Unmarshal([]byte(spec.Env[toolConfigEnv].Value), &cfg) != nil {
		return svcConfig{}, false
	}
	return cfg, true
}

// svcServer is the workspace server of a service, as it is.
type svcServer struct {
	Name   string
	Server Server
	Spec   RunSpec
	Config svcConfig
}

// account finds one of its accounts.
func (st svcServer) account(name string) (svcAccount, bool) {
	if i := st.Config.index(name); i >= 0 {
		return st.Config.Accounts[i], true
	}
	return svcAccount{}, false
}

// level is an account's access level.
func (st svcServer) level(name string) string {
	if a, ok := st.Server.accountAccess(map[string]any{"account": name}); ok {
		return a.level()
	}
	return st.Server.level()
}

// serviceServers are the workspace servers of a service, the one under
// its own name first: one once they are merged.
func (c *core) serviceServers(svc toolCard) []svcServer {
	var out []svcServer
	for name, s := range c.conf().Servers {
		if !s.local() || c.source(kindServer, name) != sourceWorkspace {
			continue
		}
		spec, err := decodeRun(s.Run)
		if err != nil {
			continue
		}
		if cfg, ok := svcConfigOf(svc, spec); ok {
			out = append(out, svcServer{Name: name, Server: s, Spec: spec, Config: cfg})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name == svc.Base || out[j].Name != svc.Base && out[i].Name < out[j].Name
	})
	return out
}

// serviceServer is the workspace server of a service.
func (c *core) serviceServer(svc toolCard) (svcServer, bool) {
	if all := c.serviceServers(svc); len(all) > 0 {
		return all[0], true
	}
	return svcServer{}, false
}

// accountsOf is a server's accounts, if it is the server of a service.
func accountsOf(s Server) (toolCard, svcConfig, bool) {
	if !s.local() {
		return toolCard{}, svcConfig{}, false
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return toolCard{}, svcConfig{}, false
	}
	svc, ok := serviceOf(spec)
	if !ok {
		return toolCard{}, svcConfig{}, false
	}
	cfg, ok := svcConfigOf(svc, spec)
	return svc, cfg, ok
}

var (
	errAccountExists  = &catalogError{http.StatusConflict, "accounts.exists", "", errors.New("this account is already connected")}
	errAccountUnknown = &catalogError{http.StatusNotFound, "accounts.unknown", "", errors.New("no such account")}
)

// accountPut is a change of one account of a service.
type accountPut struct {
	Lang    string     // the words of the server's use, if it is made
	Replace string     // the account changed, by its name; "" adds one
	Account svcAccount // its auth names no variable: it gets its own
	Level   string     // "" keeps the account's level (reads free for a new one)
	Secret  *EnvValue  // its secret's reference (a connection's token); nil keeps it, or takes Password
	Scopes  []string   // what the connection granted, for a service that is told (Scopes)
	// Password, typed, goes to the vault with the account.
	Password string
	Options  map[string]string // the server's options of the form (accountShape.Server), by field
}

// putAccount adds an account to its service's server, made if need be, or
// changes one. An account connected again (Secret) replaces the account of
// its address; two calendars of one address (one with a password, one on a
// connection) are two accounts, the connection's named by the address and
// the other named apart (freeName).
func (c *core) putAccount(svc toolCard, in accountPut) error {
	st, exists := c.serviceServer(svc)
	a := in.Account
	if strings.TrimSpace(a.Name) == "" {
		return errInvalid(errors.New("an account needs a name"))
	}
	if !exists {
		st = svcServer{Name: c.freeServerName(svc.Base, a.Name), Spec: RunSpec{Image: svc.Image, Transport: transportStdio},
			Server: Server{Use: tr(in.Lang, svc.use())}}
		if !svc.AccountLevels {
			st.Server = st.Server.withLevel(accessReads)
		}
	}
	cfg := st.Config
	at := -1
	if in.Replace != "" {
		if at = cfg.index(in.Replace); at < 0 {
			return errAccountUnknown
		}
	}
	cfg.Accounts = slices.Clone(cfg.Accounts)
	access := map[string]string{}
	for k, v := range st.Server.AccountAccess {
		access[k] = v
	}
	var renamed [2]string // an account of another kind that gave its name to a connection's
	if same := cfg.index(a.Name); same >= 0 && same != at {
		other := cfg.Accounts[same]
		switch {
		case svc.sameAccount(other, a) && in.Secret == nil:
			return errAccountExists
		case svc.sameAccount(other, a):
			at = same // the same address on a connection: it takes its place
		case in.Secret != nil && !other.signedIn():
			// An account on a connection is named by its address (the
			// connection's identity): the other one, an Apple calendar of the same
			// address, takes another name and keeps its secret and level.
			name := svc.freeName(cfg, other, -1) // its own name counts: it gives it up
			for k, v := range access {
				if strings.EqualFold(k, other.Name) {
					delete(access, k)
					access[name] = v
				}
			}
			renamed = [2]string{other.Name, name}
			cfg.Accounts[same].Name = name
		default:
			for i, x := range cfg.Accounts { // already named apart
				if i != at && svc.sameAccount(x, a) && svc.sameName(x, a) && in.Secret == nil {
					return errAccountExists
				}
			}
			a.Name = svc.freeName(cfg, a, at) // two calendars of one address
		}
	}
	var old svcAccount
	level := in.Level
	if at >= 0 {
		old = cfg.Accounts[at]
		if level == "" {
			level = svcServer{Server: Server{Access: st.Server.Access, AccountAccess: access}}.level(old.Name)
		}
	}
	if level == "" {
		level = accessReads
	}
	stem := toolPasswordEnv
	if in.Secret != nil {
		stem = toolTokenEnv
	}
	env := accountEnv(stem, a.Name)
	a.Auth = svcAuth{}
	if in.Secret != nil {
		a.Auth.TokenEnv = env
	} else {
		a.Auth.PasswordEnv = env
	}
	if svc.Scopes {
		a.Scopes = in.Scopes
	}
	creds := credentials{}
	secret := EnvValue{}
	switch {
	case in.Secret != nil:
		secret = *in.Secret
	case strings.TrimSpace(in.Password) != "":
		creds["env:"+env] = in.Password
		secret = EnvValue{File: "vault:pending"} // bound by the catalog
	case at >= 0 && old.Auth.PasswordEnv != "":
		secret = st.Spec.Env[old.Auth.PasswordEnv]
	}
	if secret.File == "" && secret.Value == "" {
		return errors.New("form.nopassword")
	}
	if at >= 0 {
		cfg.Accounts[at] = a
	} else {
		cfg.Accounts = append(cfg.Accounts, a)
	}
	if t, ok := signInByAccount(svc.ID, a.Provider); ok && a.signedIn() && svc.Scopes {
		if _, write := t.granted(a.Scopes); !write {
			// Connected again for reading only: its places no longer take files.
			cfg = cfg.withPlaces(svc.placesKey(), func(list []Place) []Place {
				for i := range list {
					if strings.EqualFold(list[i].Account, a.Name) {
						list[i].Access = accessRead
					}
				}
				return list
			})
		}
	}
	if svc.Account != nil {
		for _, fl := range svc.Account.Server {
			v := cmpOr(in.Options[fl.Name], cfg.option(fl.Key))
			if fl.Kind == "zone" {
				v = cmpOr(v, c.conf().TimeZone)
			}
			cfg = cfg.withOption(fl.Key, v)
		}
	}
	srv := st.Server
	if svc.AccountLevels {
		levels := map[string]string{}
		for k, v := range access {
			if !strings.EqualFold(k, old.Name) || old.Name == "" {
				levels[k] = v
			}
		}
		levels[a.Name] = level
		srv.AccountAccess = levels
	}
	if g, ok := srv.AccountGrids[old.Name]; ok && !strings.EqualFold(old.Name, a.Name) {
		srv.AccountGrids = withoutAccount(srv.AccountGrids, old.Name) // renamed: its grid goes with it
		srv.AccountGrids[a.Name] = g
	}
	if err := c.writeService(svc, st, srv, cfg, map[string]EnvValue{env: secret}, creds, exists); err != nil {
		return err
	}
	if old.Name != "" && !strings.EqualFold(old.Name, a.Name) {
		c.store.forgetAccountActions(st.Name, old.Name)
	}
	if renamed[0] != "" {
		c.renameAccountRefs(st.Name, renamed[0], renamed[1])
	}
	return nil
}

// renameAccountRefs has what named an account of a server by its name —
// its actions allowed for good — name it by its new one.
func (c *core) renameAccountRefs(server, from, to string) {
	c.store.db.Exec(`UPDATE remembered_actions SET account = ? WHERE server = ? AND account = ? COLLATE NOCASE`, to, server, from)
}

// dropAccount takes an account out of its service's server; the server
// goes with its last account.
func (c *core) dropAccount(svc toolCard, name string) error {
	st, ok := c.serviceServer(svc)
	if !ok || st.Config.index(name) < 0 {
		return errAccountUnknown
	}
	cfg := st.Config
	gone := cfg.Accounts[cfg.index(name)]
	cfg.Accounts = slices.Delete(slices.Clone(cfg.Accounts), cfg.index(name), cfg.index(name)+1)
	if len(cfg.Accounts) == 0 {
		return c.deleteServer(st.Name)
	}
	cfg = cfg.withPlaces(svc.placesKey(), func(list []Place) []Place {
		return slices.DeleteFunc(list, func(p Place) bool { return strings.EqualFold(p.Account, gone.Name) })
	})
	srv := st.Server
	srv.AccountGrids = withoutAccount(st.Server.AccountGrids, gone.Name)
	srv.AccountAccess = map[string]string{}
	for k, v := range st.Server.AccountAccess {
		if !strings.EqualFold(k, gone.Name) {
			srv.AccountAccess[k] = v
		}
	}
	if err := c.writeService(svc, st, srv, cfg, nil, nil, true); err != nil {
		return err
	}
	c.store.forgetAccountActions(st.Name, gone.Name)
	return nil
}

// setAccountGrid gives one account a grid of its own.
func (c *core) setAccountGrid(server, account string, g accountGrid) error {
	srv, ok := c.conf().Servers[server]
	svc, cfg, isSvc := accountsOf(srv)
	if !ok || !isSvc || !svc.AccountLevels || c.source(kindServer, server) != sourceWorkspace {
		return errReadOnly(kindServer, server)
	}
	i := cfg.index(account)
	if i < 0 {
		return errAccountUnknown
	}
	spec, _ := decodeRun(srv.Run)
	next := keepAccounts(srv, cfg)
	next.AccountGrids = withoutAccount(next.AccountGrids, account)
	if next.AccountGrids == nil {
		next.AccountGrids = map[string]accountGrid{}
	}
	next.AccountGrids[cfg.Accounts[i].Name] = g
	return c.writeService(svc, svcServer{Name: server, Server: srv, Spec: spec, Config: cfg}, next, cfg, nil, nil, true)
}

// keepAccounts writes down what each account follows from its server: once
// one account has its own access, the server's no longer speaks for the
// others (toolFacts).
func keepAccounts(srv Server, cfg svcConfig) Server {
	base := srv.baseAccess()
	grids := maps.Clone(srv.AccountGrids)
	for _, a := range cfg.Accounts {
		if _, ok := srv.accountAccess(map[string]any{"account": a.Name}); ok {
			continue
		}
		if grids == nil {
			grids = map[string]accountGrid{}
		}
		grids[a.Name] = accountGrid{Grid: base.grid, Trust: &base.trusted}
	}
	srv.AccountGrids = grids
	return srv
}

// withoutAccount is a server's account grids less one account's.
func withoutAccount(grids map[string]accountGrid, account string) map[string]accountGrid {
	if len(grids) == 0 {
		return grids
	}
	out := maps.Clone(grids)
	maps.DeleteFunc(out, func(k string, _ accountGrid) bool { return strings.EqualFold(k, account) })
	return out
}

// setAccountLevel changes one account's access level.
func (c *core) setAccountLevel(server, account, level string) error {
	if !validAccess(level) || level == "" {
		return errInvalid(errors.New("unknown access level"))
	}
	srv, ok := c.conf().Servers[server]
	svc, cfg, isSvc := accountsOf(srv)
	if !ok || !isSvc || !svc.AccountLevels || c.source(kindServer, server) != sourceWorkspace {
		return errReadOnly(kindServer, server)
	}
	i := cfg.index(account)
	if i < 0 {
		return errAccountUnknown
	}
	spec, _ := decodeRun(srv.Run)
	srv = keepAccounts(srv, cfg)
	levels := map[string]string{}
	for k, v := range srv.AccountAccess {
		levels[k] = v
	}
	for k := range levels {
		if strings.EqualFold(k, account) {
			delete(levels, k)
		}
	}
	levels[cfg.Accounts[i].Name] = level
	next := srv
	next.AccountAccess = levels
	next.AccountGrids = withoutAccount(srv.AccountGrids, account) // a level replaces a grid
	return c.writeService(svc, svcServer{Name: server, Server: srv, Spec: spec, Config: cfg}, next, cfg, nil, nil, true)
}

// writeService writes a service's server: its configuration, each
// account's secret (env holds the new ones; the others keep theirs) and
// read_only for the accounts kept read only.
func (c *core) writeService(svc toolCard, st svcServer, srv Server, cfg svcConfig, env map[string]EnvValue, creds credentials, exists bool) error {
	for i := range cfg.Accounts {
		a, ok := srv.accountAccesses()[cfg.Accounts[i].Name]
		cfg.Accounts[i].ReadOnly = svc.AccountLevels && ok && a.level() == accessReadOnly
	}
	raw, _ := json.Marshal(cfg)
	next := map[string]EnvValue{toolConfigEnv: {Value: string(raw)}}
	for _, a := range cfg.Accounts {
		e := a.secretEnv()
		if v, ok := env[e]; ok {
			next[e] = v
		} else if v, ok := st.Spec.Env[e]; ok {
			next[e] = v
		}
	}
	spec := st.Spec
	spec.Env = next
	srv.Run = marshalRun(spec)
	level := srv.level()
	srv.Access = level
	if srv.Grid == nil && len(srv.AccountGrids) == 0 {
		srv.TrustAnnotations = level != accessAsk // with a grid, it is a switch of its own
	}
	mode := putUpdate
	if !exists {
		mode = putCreate
	}
	return c.putServer(mode, st.Name, srv, creds)
}

// testAccount asks a service's server to sign one of its accounts in, as
// it uses it (its setup tool test_account, servers/README.md): "" when it
// works, else why not, in the server's words.
func (c *core) testAccount(ctx context.Context, server, account string) (string, error) {
	u := c.pool.get(server)
	if u == nil {
		return "", errors.New(server + " does not run")
	}
	args, _ := json.Marshal(map[string]string{"account": account})
	res, err := u.call(ctx, "test_account", args)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", errors.New(resultText(res))
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.OK {
		return "", nil
	}
	return cmpOr(out.Error, "refused"), nil
}

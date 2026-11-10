package main

import (
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
	"strings"
)

// One server for each service, whatever the number of accounts: the mail
// is one eriphe-mail with every mailbox, the calendar one eriphe-calendar
// with every calendar account, so the brain searches all the mail, lists
// the week's events and finds free time across every account in one call.
// Both servers take several accounts (servers/mail, servers/calendar); each
// tool names the account it touches in its "account" argument, and refuses
// ids of another.
//
// An account is added to the server (made the first time), changed or
// taken out of it (the server goes with the last one), from the account's
// form or from a connection (connection_tools.go). Each account keeps its own access
// level (Server.AccountAccess, access.go): the server is also told to keep
// read only the accounts whose level is read only. Its secret is a
// variable of its own in the server's environment: a password bound by the
// catalog (MAIL_PASSWORD_<hash>), or the connection's token in the vault
// (MAIL_GOOGLE_TOKEN_<hash> for the accounts of before, legacy.go).
//
// Before, each account was a server of its own (mail, mail-sam, agenda…):
// mergeAccountServers makes them one at start (accounts_merge.go).

// accountService is the mail or the calendar.
type accountService struct {
	ID          string // its preset: mail or calendar
	Base        string // the catalog name it is made under
	Image       string
	ConfigEnv   string // the variable of its configuration
	PasswordEnv string // the stem of an account's password variable
	TokenEnv    string // the stem of a connection's token variable
	Use         string // the text key of what it is for
}

var (
	mailService = accountService{ID: "mail", Base: "mail", Image: mailImage, ConfigEnv: "MAIL_CONFIG",
		PasswordEnv: mailSecretEnv, TokenEnv: mailTokenEnv, Use: "preset.mail.use"}
	calendarService = accountService{ID: "calendar", Base: "agenda", Image: calendarImage, ConfigEnv: "CALENDAR_CONFIG",
		PasswordEnv: calSecretEnv, TokenEnv: calTokenEnv, Use: "preset.calendar.use"}
	accountServices = []accountService{mailService, calendarService}
)

// serviceOf finds the service a server's image gives.
func serviceOf(spec RunSpec) (accountService, bool) {
	for _, svc := range accountServices {
		if strings.HasPrefix(spec.Image, svc.Image) {
			return svc, true
		}
	}
	return accountService{}, false
}

// serviceByID finds a service by its id (mail, calendar).
func serviceByID(id string) (accountService, bool) {
	for _, svc := range accountServices {
		if svc.ID == id {
			return svc, true
		}
	}
	return accountService{}, false
}

// svcAccount is an account as eriphe-mail or eriphe-calendar reads it;
// what one of them does not know is left empty (and out of the JSON).
type svcAccount struct {
	Name     string        `json:"name"`
	Address  string        `json:"address,omitempty"` // mail
	Provider string        `json:"provider,omitempty"`
	URL      string        `json:"url,omitempty"` // calendar
	Username string        `json:"username,omitempty"`
	IMAP     *mailEndpoint `json:"imap,omitempty"`
	SMTP     *mailEndpoint `json:"smtp,omitempty"`
	Auth     svcAuth       `json:"auth"`
	ReadOnly bool          `json:"read_only,omitempty"`
}

type svcAuth struct {
	PasswordEnv   string `json:"password_env,omitempty"`
	OAuthTokenEnv string `json:"oauth_token_env,omitempty"`
}

// secretEnv is the variable of the account's secret.
func (a svcAccount) secretEnv() string { return cmpOr(a.Auth.PasswordEnv, a.Auth.OAuthTokenEnv) }

// signedIn tells an account on a connection's token.
func (a svcAccount) signedIn() bool { return a.Auth.OAuthTokenEnv != "" }

// The stems of a connection's token variable; their names stay those of
// the installs that have them.
const ()

// svcConfig is the server's configuration.
type svcConfig struct {
	Timezone string       `json:"timezone,omitempty"` // calendar
	Accounts []svcAccount `json:"accounts"`
}

func (cfg svcConfig) index(name string) int {
	return slices.IndexFunc(cfg.Accounts, func(a svcAccount) bool { return strings.EqualFold(a.Name, name) })
}

// sameAccount tells whether two accounts of one name are one account: a
// mailbox is its address, whoever connects it; a calendar is also where it
// is (an Apple account and a Google account can have the same address,
// and are two calendars).
func (svc accountService) sameAccount(x, y svcAccount) bool {
	if svc == mailService {
		return true
	}
	return strings.EqualFold(x.Provider, y.Provider) && strings.EqualFold(x.URL, y.URL)
}

// freeName is a name for a that no other account has (skip is its own
// place, -1 for a new one): its own, else with where it is
// ("sam@example.org (iCloud)"), else numbered.
func (cfg svcConfig) freeName(a svcAccount, skip int) string {
	taken := func(n string) bool { i := cfg.index(n); return i >= 0 && i != skip }
	if !taken(a.Name) {
		return a.Name
	}
	where, known := tr("en", "calp."+a.Provider), a.Provider != "" && a.Provider != "caldav"
	if !known || where == "calp."+a.Provider {
		where = cmpOr(a.Provider, "2")
		if u, err := url.Parse(a.URL); err == nil && u.Hostname() != "" {
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
func svcConfigOf(svc accountService, spec RunSpec) (svcConfig, bool) {
	if !strings.HasPrefix(spec.Image, svc.Image) {
		return svcConfig{}, false
	}
	var cfg svcConfig
	if json.Unmarshal([]byte(spec.Env[svc.ConfigEnv].Value), &cfg) != nil {
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
func (c *core) serviceServers(svc accountService) []svcServer {
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
func (c *core) serviceServer(svc accountService) (svcServer, bool) {
	if all := c.serviceServers(svc); len(all) > 0 {
		return all[0], true
	}
	return svcServer{}, false
}

// accountsOf is a server's accounts, if it is the server of a service.
func accountsOf(s Server) (accountService, svcConfig, bool) {
	if !s.local() {
		return accountService{}, svcConfig{}, false
	}
	spec, err := decodeRun(s.Run)
	if err != nil {
		return accountService{}, svcConfig{}, false
	}
	svc, ok := serviceOf(spec)
	if !ok {
		return accountService{}, svcConfig{}, false
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
	// Password, typed, goes to the vault with the account.
	Password string
	Timezone string // calendar: the server's zone, if given
}

// putAccount adds an account to its service's server, made if need be, or
// changes one. An account connected again (Secret) replaces the account of
// its address; two calendars of one address (one with a password, one on a
// connection) are two accounts, the connection's named by the address and
// the other named apart (freeName).
func (c *core) putAccount(svc accountService, in accountPut) error {
	st, exists := c.serviceServer(svc)
	a := in.Account
	if strings.TrimSpace(a.Name) == "" {
		return errInvalid(errors.New("an account needs a name"))
	}
	if !exists {
		st = svcServer{Name: c.freeServerName(svc.Base, a.Name), Spec: RunSpec{Image: svc.Image, Transport: transportStdio},
			Server: Server{Use: tr(in.Lang, svc.Use)}}
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
			name := cfg.freeName(other, -1) // its own name counts: it gives it up
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
				if i != at && svc.sameAccount(x, a) && strings.EqualFold(x.Username, a.Username) && in.Secret == nil {
					return errAccountExists
				}
			}
			a.Name = cfg.freeName(a, at) // two calendars of one address
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
	stem := svc.PasswordEnv
	if in.Secret != nil {
		stem = svc.TokenEnv
	}
	env := accountEnv(stem, a.Name)
	a.Auth = svcAuth{}
	if in.Secret != nil {
		a.Auth.OAuthTokenEnv = env
	} else {
		a.Auth.PasswordEnv = env
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
	if svc == calendarService {
		cfg.Timezone = cmpOr(in.Timezone, cmpOr(cfg.Timezone, c.conf().TimeZone))
	}
	levels := map[string]string{}
	for k, v := range access {
		if !strings.EqualFold(k, old.Name) || old.Name == "" {
			levels[k] = v
		}
	}
	levels[a.Name] = level
	srv := st.Server
	srv.AccountAccess = levels
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
func (c *core) dropAccount(svc accountService, name string) error {
	st, ok := c.serviceServer(svc)
	if !ok || st.Config.index(name) < 0 {
		return errAccountUnknown
	}
	cfg := st.Config
	gone := cfg.Accounts[cfg.index(name)]
	cfg.Accounts = slices.Delete(cfg.Accounts, cfg.index(name), cfg.index(name)+1)
	if len(cfg.Accounts) == 0 {
		return c.deleteServer(st.Name)
	}
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
	if !ok || !isSvc || c.source(kindServer, server) != sourceWorkspace {
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
	if !ok || !isSvc || c.source(kindServer, server) != sourceWorkspace {
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
func (c *core) writeService(svc accountService, st svcServer, srv Server, cfg svcConfig, env map[string]EnvValue, creds credentials, exists bool) error {
	for i := range cfg.Accounts {
		a, ok := srv.accountAccesses()[cfg.Accounts[i].Name]
		cfg.Accounts[i].ReadOnly = ok && a.level() == accessReadOnly
	}
	raw, _ := json.Marshal(cfg)
	next := map[string]EnvValue{svc.ConfigEnv: {Value: string(raw)}}
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

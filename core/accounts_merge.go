package main

import (
	"log"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Before one server held every account of a service (accounts.go), each
// account was a server of its own: mail, mail-sam, agenda, agenda-icloud…,
// one account each, its secret in MAIL_PASSWORD (or MAIL_GOOGLE_TOKEN,
// CALENDAR_PASSWORD, CALENDAR_GOOGLE_TOKEN). mergeAccountServers makes them
// one at start, under the service's own name when one has it:
//
//   - each account keeps its secret's reference (nothing is asked again)
//     and the level of the server it was, now its own;
//   - the actions allowed for good on a server are allowed on the one
//     left, for that account; a watch that called a server's tool calls
//     the one left, for that account; an assistant lent a server is lent
//     the one left.
//
// It does nothing once they are one.

// mergeAccountServers merges each service's servers of one account.
func (c *core) mergeAccountServers() {
	for _, svc := range accountServices {
		all := c.serviceServers(svc)
		if len(all) == 0 || len(all) == 1 && merged(svc, all[0]) {
			continue
		}
		if err := c.mergeService(svc, all); err != nil {
			log.Printf("accounts: merging the %s servers: %v", svc.ID, err)
		}
	}
}

// merged tells a server made by putAccount: each account's secret in a
// variable of its own, each account with its level.
func merged(svc accountService, st svcServer) bool {
	for _, a := range st.Config.Accounts {
		e := a.secretEnv()
		if e == svc.PasswordEnv || e == svc.TokenEnv || st.level(a.Name) == "" {
			return false
		}
		if _, ok := st.Server.AccountAccess[a.Name]; !ok {
			return false
		}
	}
	return true
}

// mergedName is an account's name in the merged server: its address for a
// mailbox, its username for a calendar (with its server's host when the
// username is not an address), as putAccount names them.
func mergedName(svc accountService, a svcAccount) string {
	if svc == mailService {
		return cmpOr(a.Address, a.Name)
	}
	name := cmpOr(a.Username, a.Name)
	if u, err := url.Parse(a.URL); !strings.Contains(name, "@") && err == nil && u.Host != "" {
		name += "@" + u.Hostname()
	}
	return name
}

// trailingAccount is the account a server's use named, made for one
// account: "mes mails : … (sam@example.org)".
var trailingAccount = regexp.MustCompile(`\s*\([^()]*@[^()]*\)\s*$`)

func (c *core) mergeService(svc accountService, all []svcServer) error {
	target := all[0]
	cfg := svcConfig{Timezone: target.Config.Timezone}
	env := map[string]EnvValue{}
	levels := map[string]string{}
	of := map[string]string{} // a server's account, for what is moved to the one left
	for _, st := range all {
		cfg.Timezone = cmpOr(cfg.Timezone, st.Config.Timezone)
	}
	// The Google accounts first: one keeps its address as its name
	// (its connection's identity), and another account of that address is
	// named apart.
	for _, google := range []bool{true, false} {
		for _, st := range all {
			for _, a := range st.Config.Accounts {
				if a.signedIn() != google {
					continue
				}
				name := mergedName(svc, a)
				if i := cfg.index(name); i >= 0 {
					if svc.sameAccount(cfg.Accounts[i], a) {
						log.Printf("accounts: %s: an account is twice, the first kept", svc.ID) // never the address
						continue
					}
					// two calendars of one address (a Google account's and
					// an Apple account's): both kept, the second named apart
					named := a
					named.Name = name
					name = cfg.freeName(named, -1)
				}
				v := st.Spec.Env[a.secretEnv()]
				stem := svc.PasswordEnv
				if a.signedIn() {
					stem = svc.TokenEnv
				}
				level := st.level(a.Name)
				a.Name, a.Auth = name, svcAuth{}
				if stem == svc.TokenEnv {
					a.Auth.OAuthTokenEnv = accountEnv(stem, name)
				} else {
					a.Auth.PasswordEnv = accountEnv(stem, name)
				}
				env[a.secretEnv()] = v
				levels[name] = level
				cfg.Accounts = append(cfg.Accounts, a)
				if len(st.Config.Accounts) == 1 {
					of[st.Name] = name
				}
			}
		}
	}
	srv := target.Server
	srv.AccountAccess = levels
	srv.Use = trailingAccount.ReplaceAllString(srv.Use, "")
	if err := c.writeService(svc, target, srv, cfg, env, nil, true); err != nil {
		return err
	}
	// What was allowed for good on the server left was its account's.
	if a := of[target.Name]; a != "" {
		c.store.db.Exec(`UPDATE remembered_actions SET account = ? WHERE server = ? AND account = ''`, a, target.Name)
	}
	for _, st := range all[1:] {
		a := of[st.Name]
		c.store.moveActions(st.Name, target.Name, a)
		c.moveAssistantTools(st.Name, target.Name)
		if err := c.deleteServer(st.Name); err != nil {
			return err
		}
	}
	log.Printf("accounts: %d %s servers made one, %s, with %d accounts", len(all), svc.ID, target.Name, len(cfg.Accounts))
	return nil
}

// moveAssistantTools lends the workspace's assistants lent a server the
// one left instead.
func (c *core) moveAssistantTools(from, to string) {
	for name, a := range c.dyn.Assistants {
		changed := false
		var tools []string
		for _, g := range a.Tools {
			server, rest, dotted := strings.Cut(strings.TrimSpace(g), ".")
			if server == from {
				g, changed = to, true
				if dotted {
					g += "." + rest
				}
			}
			if !slices.Contains(tools, g) {
				tools = append(tools, g)
			}
		}
		if changed {
			a.Tools = tools
			if err := c.putAssistant(name, a); err != nil {
				log.Printf("accounts: assistant %s: %v", name, err)
			}
		}
	}
}

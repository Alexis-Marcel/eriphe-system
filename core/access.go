package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// What Eriphe may do on each tool of the catalog, apart from how the tool's
// credential is scoped:
//
//   - read-only: only its read tools (readOnlyHint) are lent, to the brain
//     and to tasks; a write tool is not listed, and refused if called;
//   - reads (the default of the presets): reads free, the rest on the
//     user's approval;
//   - ask: everything on the user's approval, reads too (the server's own
//     annotations do not count);
//   - free: reads and writes without asking the user.
//
// The built-in denials of the policy come first whatever the level, then
// the configuration's rules, then the actions the user allowed for good
// ("Toujours autoriser", kept in the workspace's database), then the level.
//
// Where the service can scope its credential, the level also decides what
// Eriphe asks for: a read-only OAuth scope, Google Drive's drive.readonly,
// a GitHub App installation token with read permissions. Elsewhere (an IMAP
// password, a Home Assistant token, an API key), Eriphe holds to the level
// itself.

const (
	accessReadOnly = "read-only"
	accessReads    = "reads"
	accessAsk      = "ask"
	accessFree     = "free"
)

// accessLevels in the order the workspace shows them.
var accessLevels = []string{accessReadOnly, accessReads, accessFree, accessAsk}

func validAccess(a string) bool { return a == "" || slices.Contains(accessLevels, a) }

// level is a server's access level, or the closest to its grid. A server of
// several accounts has what their levels come to (accountsLevel).
func (s Server) level() string {
	if accounts := s.accountAccesses(); len(accounts) > 0 {
		levels := map[string]string{}
		for name, a := range accounts {
			levels[name] = a.level()
		}
		return accountsLevel(levels)
	}
	return s.baseAccess().level()
}

// ownLevel is a server's named level. An older entry has trust_annotations
// only: true was "reads free", false "ask me everything".
func (s Server) ownLevel() string {
	if s.Access != "" {
		return s.Access
	}
	if s.TrustAnnotations {
		return accessReads
	}
	return accessAsk
}

// trusts tells whether the server's own readOnlyHint counts.
func (s Server) trusts() bool { return s.level() != accessAsk }

// withLevel sets a server's access level, and trust_annotations to match
// for whatever still reads it; a server of several accounts gets it for
// each of them.
func (s Server) withLevel(level string) Server {
	if accounts := s.accountAccesses(); len(accounts) > 0 {
		next := map[string]string{}
		for a := range accounts {
			next[a] = level
		}
		s.AccountAccess = next
	}
	s.Grid, s.AccountGrids = nil, nil // a level replaces a grid
	s.Access, s.TrustAnnotations = level, level != accessAsk
	return s
}

// Accounts. A server of several accounts (the mail, the calendar) has a
// level for each (Server.AccountAccess): a professional mailbox read only
// beside a personal one free. A call is decided at the level of the
// account its "account" argument names; the server itself refuses a call
// whose ids are of another account, and any write in an account it was
// told to keep read only. A call that names no account takes what the
// levels come to: a read is free unless an account asks for everything;
// a write, when the levels differ, is refused until it names its account.

// accountsLevel is what the levels of the accounts come to, for what
// knows the server alone (the tools it lends, the reads allowed in
// advance): ask when one asks for everything, read only when all are, free
// when all are, else reads free and the rest asked.
func accountsLevel(levels map[string]string) string {
	all := func(l string) bool {
		for _, v := range levels {
			if v != l {
				return false
			}
		}
		return true
	}
	for _, v := range levels {
		if v == accessAsk {
			return accessAsk
		}
	}
	switch {
	case all(accessReadOnly):
		return accessReadOnly
	case all(accessFree):
		return accessFree
	}
	return accessReads
}

// accountLevel is the level of the account a call names, if the server
// has a level for it; ok is false when the call names none of them.
func (s Server) accountLevel(input map[string]any) (level string, ok bool) {
	named, _ := input["account"].(string)
	named = strings.TrimSpace(named)
	if named == "" {
		return "", false
	}
	for a, l := range s.AccountAccess {
		if strings.EqualFold(a, named) {
			return l, true
		}
	}
	return "", false
}

// mixed tells whether the accounts of a server have different levels.
func (s Server) mixed() bool {
	first := ""
	for _, l := range s.AccountAccess {
		if first != "" && l != first {
			return true
		}
		first = l
	}
	return false
}

// catalogFacts is what the policy knows of a catalog tool.
type catalogFacts struct {
	category string // read, change, delete or send (access_grid.go)
	value    string // free, approval or never; "" for a tool out of the catalog
	read     bool   // in the Read category
	// noAccount: a call that must name its account, and names none.
	noAccount bool
	// unclean: a Free above Read does not hold: the brain's turn is tainted,
	// or the task did not start cleanly.
	unclean bool
	// held: the task was lent the tool held back: nothing authorizes it but
	// its release, a grant of its category or a free read no more than the rest.
	held bool
	// granted: the user approved this category for the task (access_card.go).
	granted bool
}

// facts is what the catalog says of a call: of the tool alone with a nil
// input, of the account it names on a server of several.
func (p *pool) facts(claudeName string, input map[string]any) catalogFacts {
	server, tool, ok := splitToolName(claudeName)
	if !ok || p == nil {
		return catalogFacts{}
	}
	u := p.get(server)
	if u == nil {
		return catalogFacts{}
	}
	return u.srv.toolFacts(u.toolByClaudeName(tool), input)
}

const noAccountReason = "ses comptes n'ont pas le même accès : l'appel doit nommer son compte (account)"

// free tells whether the grid leaves a tool to Eriphe.
func (f catalogFacts) free() bool { return f.value == gridFree }

// Actions allowed for good: "Toujours autoriser cette action" on a
// permission. Each is a rule of the policy. On
// a server of several accounts, an action is allowed for the account the
// call named, not for the others.

// remembered_actions took the place of allowed_actions, whose key was the
// tool alone: what was allowed there stays, for every account.
const allowedSchema = `CREATE TABLE IF NOT EXISTS remembered_actions (
  tool TEXT NOT NULL, account TEXT NOT NULL DEFAULT '', server TEXT NOT NULL, time INTEGER NOT NULL,
  PRIMARY KEY (tool, account));`

func init() { extraSchema = append(extraSchema, allowedSchema) }

// allowedAction is one action allowed without asking.
type allowedAction struct {
	Tool    string // its Claude Code name, mcp__server__tool
	Account string // the account it is allowed for, on a server of several; "" any
	Server  string
	Name    string // the tool's own part
	Time    time.Time
}

const allowedReason = "action autorisée sans demander"

func (s *Store) allowAction(server, tool, account string) error {
	_, err := s.db.Exec(`INSERT INTO remembered_actions (tool, account, server, time) VALUES (?, ?, ?, ?)
		ON CONFLICT(tool, account) DO NOTHING`, normalizeToolName(tool), account, server, time.Now().Unix())
	return err
}

// allowedActions are the actions allowed for good, of one server or ("")
// of all, by name.
func (s *Store) allowedActions(server string) []allowedAction {
	if s == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT tool, account, server, time FROM remembered_actions WHERE ? = '' OR server = ? ORDER BY tool, account`, server, server)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []allowedAction
	for rows.Next() {
		var a allowedAction
		var t int64
		if rows.Scan(&a.Tool, &a.Account, &a.Server, &t) != nil {
			continue
		}
		a.Time = time.Unix(t, 0)
		_, a.Name, _ = splitToolName(a.Tool)
		out = append(out, a)
	}
	return out
}

// accountActions are a server's actions allowed for one account.
func (s *Store) accountActions(server, account string) []allowedAction {
	return slices.DeleteFunc(s.allowedActions(server), func(a allowedAction) bool { return !strings.EqualFold(a.Account, account) })
}

func (s *Store) forgetAction(server, tool, account string) error {
	_, err := s.db.Exec(`DELETE FROM remembered_actions WHERE server = ? AND tool = ? AND account = ?`, server, normalizeToolName(tool), account)
	return err
}

// forgetActions drops a server's allowed actions: removed, its name may
// go to another.
func (s *Store) forgetActions(server string) {
	s.db.Exec(`DELETE FROM remembered_actions WHERE server = ?`, server)
}

// forgetAccountActions drops what was allowed for an account removed.
func (s *Store) forgetAccountActions(server, account string) {
	s.db.Exec(`DELETE FROM remembered_actions WHERE server = ? AND account = ? COLLATE NOCASE`, server, account)
}

// moveActions gives a server's allowed actions to another, for one of its
// accounts: the servers of one account each merged into one (accounts.go).
func (s *Store) moveActions(from, to, account string) {
	for _, a := range s.allowedActions(from) {
		_, tool, _ := splitToolName(a.Tool)
		s.allowAction(to, claudeToolName(to, tool), cmpOr(a.Account, account))
	}
	s.forgetActions(from)
}

// allowedRules are the allowed actions as rules of the policy.
func (s *Store) allowedRules() []Rule {
	var out []Rule
	for _, a := range s.allowedActions("") {
		r := Rule{Tool: globEscape(a.Tool), Decision: decideAllow, Reason: allowedReason}
		if a.Account != "" {
			r.account = ptrTo(a.Account)
		}
		out = append(out, r)
	}
	return out
}

func ptrTo[T any](v T) *T { return &v }

// callAccount is the account a call names, on a server of several: the
// name the server has it under; "" when it names none of them.
func (c *core) callAccount(server string, input map[string]any) string {
	srv, ok := c.conf().Servers[server]
	if !ok {
		return ""
	}
	named, _ := input["account"].(string)
	for a := range srv.accountAccesses() {
		if strings.EqualFold(a, strings.TrimSpace(named)) {
			return a
		}
	}
	return ""
}

// rememberCheck tells whether an action may be allowed for good: a tool of
// the catalog whose level and the policy's denials do not refuse it.
func (c *core) rememberCheck(tool string, input map[string]any) (server string, err error) {
	server, _, ok := splitToolName(tool)
	if _, inCatalog := c.conf().Servers[server]; !ok || !inCatalog {
		return "", errors.New("only an action of a catalog tool can be allowed for good")
	}
	if d, why := decideWith(tool, input, c.pool.facts(tool, input), nil); d == decideDeny {
		return "", fmt.Errorf("Eriphe's policy refuses this action (%s): it cannot be allowed for good", why)
	}
	return server, nil
}

// rememberable tells whether a permission's card offers "always allow".
func (c *core) rememberable(tool string, input map[string]any) bool {
	_, err := c.rememberCheck(tool, input)
	return err == nil
}

// rememberAction allows a catalog tool's action without asking, from now
// on: for the account the call names, on a server of several.
func (c *core) rememberAction(tool string, input map[string]any) error {
	server, err := c.rememberCheck(tool, input)
	if err != nil {
		return err
	}
	return c.store.allowAction(server, tool, c.callAccount(server, input))
}

// answerAlways allows a pending permission, as the user, and its action
// without asking from now on: "Toujours autoriser cette action".
func (c *core) answerAlways(ctx context.Context, id, permission, reply string) (string, error) {
	st, err := c.status(id, 0)
	if err != nil {
		return "", err
	}
	tool := ""
	var input map[string]any
	for _, q := range st.PendingPermissions {
		if permission == "" || fmt.Sprint(q["id"]) == permission {
			tool = fmt.Sprint(q["tool_name"])
			input, _ = q["input"].(map[string]any)
			break
		}
	}
	if tool != "" {
		if _, err := c.rememberCheck(tool, input); err != nil {
			return "", err
		}
	}
	yes := true
	res, err := c.answer(ctx, id, permission, reply, &yes, byUser)
	if err != nil {
		return "", err
	}
	if err := c.rememberAction(tool, input); err != nil {
		return "", err
	}
	return res + "; " + tool + " is allowed without asking from now on", nil
}

// Scoped credentials. tokenScopes are the read-only and the full scopes of
// a service by its preset, when its OAuth server advertises such scopes
// (checked at each one's /.well-known/oauth-protected-resource, October
// 2026). byName: the scopes are the ones the server advertises, split by
// their names (splitScopes), which keeps up with a vendor adding some.
// Another server is split the same way; one whose scopes do not read as
// reads and writes (Notion's "default", Stripe's "mcp") is not scoped:
// Eriphe holds to the level itself.
type scopeSet struct {
	read, write []string
	byName      bool
}

var tokenScopes = map[string]scopeSet{
	"linear": {read: []string{"read"}, write: []string{"read", "write"}},
	// Its MCP server advertises data:read_write only; data:read is the API's.
	"todoist": {read: []string{"data:read"}, write: []string{"data:read_write"}},
	// jobs and inference-api spend the account's credits: not a read.
	"huggingface": {read: []string{"openid", "profile", "read-mcp", "read-repos"},
		write: []string{"openid", "profile", "read-mcp", "read-repos", "contribute-repos", "jobs", "inference-api"}},
	// Its read set has account:read: the channels of a post are the account's.
	"buffer": {read: []string{"account:read", "posts:read", "ideas:read", "insights:read", "engagements:read"},
		write: []string{"account:read", "posts:read", "posts:write", "ideas:read", "ideas:write", "insights:read",
			"engagements:read", "engagements:write"}},
	"metricool": {read: []string{"mcp:read"}, write: []string{"mcp:read", "mcp:write"}},
	"airtable":  {byName: true}, "beehiiv": {byName: true}, "calendly": {byName: true}, "canva": {byName: true}, "dropbox": {byName: true},
	"evernote": {byName: true}, "gamma": {byName: true}, "jam": {byName: true}, "mem": {byName: true},
	"mercury": {byName: true}, "miro": {byName: true}, "netlify": {byName: true}, "raindrop": {byName: true},
	"ramp": {byName: true}, "readwise": {byName: true}, "sentry": {byName: true}, "supabase": {byName: true},
	"ticktick": {byName: true},
	// Its scopes name no verb: readOnly or full.
	"jotform": {read: []string{"readOnly"}, write: []string{"full"}},
	"akiflow": {byName: true}, "capacities": {byName: true}, "close": {byName: true}, "mural": {byName: true},
	"neon": {byName: true}, "pandadoc": {byName: true}, "posthog": {byName: true}, "qonto": {byName: true},
	"slite": {byName: true}, "sunsama": {byName: true}, "typeform": {byName: true}, "whimsical": {byName: true},
	"amplitude": {byName: true}, "circle": {byName: true}, "grafana": {byName: true}, "honeycomb": {byName: true},
	"leadpages": {byName: true}, "lovable": {byName: true}, "mintlify": {byName: true}, "wave": {byName: true},
	// publish names no verb; the theme and the account's users, which Kajabi
	// keeps off by default, are left out of the full set.
	"kajabi": {read: []string{"read"}, write: []string{"read", "write:content", "write:commerce", "write:contacts",
		"write:community", "write:emails", "write:automations", "publish"}},
	// Its restart, configure and full_access scopes name no verb. The full set
	// is the one its server asks for, without cluster deletion or billing.
	"typesense": {read: []string{"clusters:read", "metrics:read", "backups:read", "search_insights:read", "data:view", "data:search"},
		write: []string{"clusters:read", "metrics:read", "backups:read", "clusters:create", "clusters:configure", "clusters:restart",
			"cluster_parameters:manage", "search_insights:read", "data:view", "data:search", "data:full_access", "data:manage_synonyms",
			"data:manage_overrides", "data:manage_presets", "data:manage_stopwords", "data:manage_stemming", "data:manage_analytics_rules",
			"data:manage_conversation_models", "data:manage_natural_language_models"}},
}

// scopeVerbs are the words of a scope's name that make it more than a read
// ("files.content.write", "transactions:request", "data:read_write").
var scopeVerbs = []string{"write", "delete", "create", "edit", "update", "manage", "admin", "contribute",
	"request", "generate", "revoke", "send", "execute"}

// splitScopes reads advertised scopes as reads and writes by their names:
// the reads are all but those naming a write. Not scoped when none names a
// write, or all do.
func splitScopes(found []string) (scopeSet, bool) {
	var read []string
	for _, s := range found {
		words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		})
		if !slices.ContainsFunc(words, func(w string) bool { return slices.Contains(scopeVerbs, w) }) {
			read = append(read, s)
		}
	}
	if len(read) == 0 || len(read) == len(found) {
		return scopeSet{}, false
	}
	return scopeSet{read: read, write: found}, true
}

// What a stored credential allows, kept with its OAuth token.
const (
	grantedRead  = "read"
	grantedWrite = "write"
	grantedNone  = "none" // not scoped: the service has no read-only scope
)

func tokenAccessKey(server string) string { return "server." + server + ".token-access" }

// scopesFor picks the OAuth scopes to ask a server for at a level, from
// what it advertises (found), and says what the token will allow. fixed:
// the configuration's own scopes, asked as they are.
func scopesFor(preset, level string, found []string, fixed bool) ([]string, string) {
	set, known := tokenScopes[preset]
	if !known || set.byName {
		set, known = splitScopes(found)
	}
	switch {
	case !known:
		return found, grantedNone
	case fixed:
		for _, s := range set.write {
			if !slices.Contains(set.read, s) && !slices.Contains(found, s) {
				return found, grantedRead
			}
		}
		return found, grantedWrite
	case level == accessReadOnly:
		return set.read, grantedRead
	}
	return set.write, grantedWrite
}

// tokenScope says how a tool's credential follows its level.
type tokenScope struct {
	Scoped    bool   // Eriphe asks the service for a credential of the level
	Granted   string // what the stored one allows: read or write, "" none yet
	Reconnect bool   // the level wants writes the stored credential cannot do
	Shrink    bool   // read only on a credential that can still write
}

func (c *core) tokenScope(name string, s Server) tokenScope {
	var t tokenScope
	switch {
	case s.authKind() == "connection":
		// A new token each hour, minted, with the level's permissions.
		t.Scoped, t.Granted = true, grantedWrite
		if s.level() == accessReadOnly {
			t.Granted = grantedRead
		}
		return t
	case s.authKind() == "oauth":
		switch got := c.store.get(tokenAccessKey(name)); got {
		case grantedRead, grantedWrite:
			t.Scoped, t.Granted = true, got
		case grantedNone:
			return t
		default:
			p, _ := presetByURL(s.URL)
			_, t.Scoped = tokenScopes[p.ID]
			if _, tok, _ := c.store.oauthToken(name); t.Scoped && tok != nil {
				t.Granted = grantedWrite // connected before the levels: everything advertised
			}
		}
	default:
		svc, cfg, ok := accountsOf(s)
		if !ok || !svc.Scopes {
			return t
		}
		t.Scoped = true
		for _, a := range cfg.Accounts {
			si, _ := signInByAccount(svc.ID, a.Provider)
			if _, write := si.granted(a.Scopes); write {
				t.Granted = grantedWrite
			} else if t.Granted == "" {
				t.Granted = grantedRead
			}
		}
	}
	t.Reconnect = s.level() != accessReadOnly && t.Granted == grantedRead
	t.Shrink = s.level() == accessReadOnly && t.Granted == grantedWrite
	return t
}

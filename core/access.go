package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// level is a server's access level. An older entry has trust_annotations
// only: true was "reads free", false "ask me everything".
func (s Server) level() string {
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
// for whatever still reads it.
func (s Server) withLevel(level string) Server {
	s.Access, s.TrustAnnotations = level, level != accessAsk
	return s
}

// lends tells whether a server's level lends one of its tools at all: a
// read-only server lends its reads only.
func lends(s Server, t *mcp.Tool) bool {
	return s.level() != accessReadOnly || (s.trusts() && t != nil && t.Annotations != nil && t.Annotations.ReadOnlyHint)
}

// catalogFacts is what the policy knows of a catalog tool.
type catalogFacts struct {
	level string // its server's access level; "" for a tool out of the catalog
	read  bool   // read-only by its own annotation, on a server trusted for it
}

func (p *pool) facts(claudeName string) catalogFacts {
	server, _, ok := splitToolName(claudeName)
	if !ok || p == nil {
		return catalogFacts{}
	}
	u := p.get(server)
	if u == nil {
		return catalogFacts{}
	}
	return catalogFacts{level: u.srv.level(), read: p.readOnly(claudeName)}
}

// free tells whether a catalog tool is Eriphe's to call without the user:
// a read of a server whose reads are free, or any tool of a free one.
func (f catalogFacts) free() bool { return f.read || f.level == accessFree }

// Actions allowed for good: "Toujours autoriser cette action" on a
// permission. Each is a rule of the policy, evaluated after the file's.

const allowedSchema = `CREATE TABLE IF NOT EXISTS allowed_actions (
  tool TEXT PRIMARY KEY, server TEXT NOT NULL, time INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, allowedSchema) }

// allowedAction is one action allowed without asking.
type allowedAction struct {
	Tool   string // its Claude Code name, mcp__server__tool
	Server string
	Name   string // the tool's own part
	Time   time.Time
}

const allowedReason = "action autorisée sans demander"

func (s *Store) allowAction(server, tool string) error {
	_, err := s.db.Exec(`INSERT INTO allowed_actions (tool, server, time) VALUES (?, ?, ?)
		ON CONFLICT(tool) DO NOTHING`, normalizeToolName(tool), server, time.Now().Unix())
	return err
}

// allowedActions are the actions allowed for good, of one server or ("")
// of all, by name.
func (s *Store) allowedActions(server string) []allowedAction {
	if s == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT tool, server, time FROM allowed_actions WHERE ? = '' OR server = ? ORDER BY tool`, server, server)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []allowedAction
	for rows.Next() {
		var a allowedAction
		var t int64
		if rows.Scan(&a.Tool, &a.Server, &t) != nil {
			continue
		}
		a.Time = time.Unix(t, 0)
		_, a.Name, _ = splitToolName(a.Tool)
		out = append(out, a)
	}
	return out
}

func (s *Store) forgetAction(server, tool string) error {
	_, err := s.db.Exec(`DELETE FROM allowed_actions WHERE server = ? AND tool = ?`, server, normalizeToolName(tool))
	return err
}

// forgetActions drops a server's allowed actions: removed, its name may
// go to another.
func (s *Store) forgetActions(server string) {
	s.db.Exec(`DELETE FROM allowed_actions WHERE server = ?`, server)
}

// allowedRules are the allowed actions as rules of the policy.
func (s *Store) allowedRules() []Rule {
	var out []Rule
	for _, a := range s.allowedActions("") {
		out = append(out, Rule{Tool: globEscape(a.Tool), Decision: decideAllow, Reason: allowedReason})
	}
	return out
}

// rememberCheck tells whether an action may be allowed for good: a tool of
// the catalog whose level and the policy's denials do not refuse it.
func (c *core) rememberCheck(tool string) (server string, err error) {
	server, _, ok := splitToolName(tool)
	if _, inCatalog := c.conf().Servers[server]; !ok || !inCatalog {
		return "", errors.New("only an action of a catalog tool can be allowed for good")
	}
	if d, why := c.conf().Policy.decideWith(tool, nil, c.pool.facts(tool), nil); d == decideDeny {
		return "", fmt.Errorf("Eriphe's policy refuses this action (%s): it cannot be allowed for good", why)
	}
	return server, nil
}

// rememberable tells whether a permission's card offers "always allow".
func (c *core) rememberable(tool string) bool {
	_, err := c.rememberCheck(tool)
	return err == nil
}

// rememberAction allows a catalog tool's action without asking, from now on.
func (c *core) rememberAction(tool string) error {
	server, err := c.rememberCheck(tool)
	if err != nil {
		return err
	}
	return c.store.allowAction(server, tool)
}

// answerAlways allows a pending permission, as the user, and its action
// without asking from now on: "Toujours autoriser cette action".
func (c *core) answerAlways(ctx context.Context, id, permission, reply string) (string, error) {
	st, err := c.status(id, 0)
	if err != nil {
		return "", err
	}
	tool := ""
	for _, q := range st.PendingPermissions {
		if permission == "" || fmt.Sprint(q["id"]) == permission {
			tool = fmt.Sprint(q["tool_name"])
			break
		}
	}
	if tool != "" {
		if _, err := c.rememberCheck(tool); err != nil {
			return "", err
		}
	}
	yes := true
	res, err := c.answer(ctx, id, permission, reply, &yes, byUser)
	if err != nil {
		return "", err
	}
	if err := c.rememberAction(tool); err != nil {
		return "", err
	}
	return res + "; " + tool + " is allowed without asking from now on", nil
}

// Scoped credentials. tokenScopes are the read-only and the full scopes of
// a service by its preset, when its OAuth server advertises such scopes
// (checked at each one's /.well-known/oauth-protected-resource, October
// 2026). Another server advertising "read" and "write" is read the same
// way; one that does not (Notion's "default") is not scoped: Eriphe holds
// to the level itself.
type scopeSet struct{ read, write []string }

var tokenScopes = map[string]scopeSet{
	"linear": {read: []string{"read"}, write: []string{"read", "write"}},
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
	if !known && slices.Contains(found, "read") && slices.Contains(found, "write") {
		set, known = scopeSet{read: slices.DeleteFunc(slices.Clone(found), func(s string) bool { return s == "write" }), write: found}, true
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
	case s.authKind() == "github-app":
		// A new installation token each hour, with the level's permissions.
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
		fc, ok := filesConfigOf(s)
		if !ok {
			return t
		}
		t.Scoped = true
		for _, a := range fc.Accounts {
			t.Granted = grantedRead
			if slices.Contains(a.Scopes, scopeDrive) {
				t.Granted = grantedWrite
			}
		}
	}
	t.Reconnect = s.level() != accessReadOnly && t.Granted == grantedRead
	t.Shrink = s.level() == accessReadOnly && t.Granted == grantedWrite
	return t
}

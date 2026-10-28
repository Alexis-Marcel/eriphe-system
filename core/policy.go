package main

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// The permission policy is code, not an instruction to a model: a mail or a
// web page read by an agent can talk the brain into anything, not this.
// Every permission request of a worker goes through it first:
//
//  1. the built-in denials, which nothing overrides (mass deletion, pushing
//     to main, rewriting history, reading secrets, writing outside /work);
//  2. the rules of the configuration, first match wins;
//  3. the built-in asks: what reaches a third party is the user's decision,
//     including the tools of MCP servers, except the read-only tools of a
//     catalog server trusted for its annotations, which the brain judges;
//  4. anything else is for the brain to judge.
//
// Every decision, whoever takes it, is recorded in the decisions table.

const (
	decideAllow = "allow"
	decideDeny  = "deny"
	decideAsk   = "ask" // only the user may answer
	decideBrain = ""    // no rule: the brain judges
)

// Who answered a permission.
const (
	byPolicy = "policy"
	byBrain  = "brain"
	byUser   = "user"
)

// Rule is one line of the policy: a tool (a name or a glob, e.g. "Bash" or
// "mcp__*"), a regular expression on what the tool acts on (a command, a
// path, a URL), a decision and its reason.
type Rule struct {
	Tool     string `json:"tool"`
	Match    string `json:"match,omitempty"`
	Decision string `json:"decision"` // allow, deny or ask
	Reason   string `json:"reason,omitempty"`
	re       *regexp.Regexp
}

type Policy struct {
	Rules []Rule `json:"rules,omitempty"`
}

func (p *Policy) compile() error {
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Tool == "" {
			return fmt.Errorf("policy rule %d: tool is required", i+1)
		}
		if r.Decision != decideAllow && r.Decision != decideDeny && r.Decision != decideAsk {
			return fmt.Errorf("policy rule %d: decision must be allow, deny or ask", i+1)
		}
		if r.Match != "" {
			re, err := regexp.Compile(r.Match)
			if err != nil {
				return fmt.Errorf("policy rule %d: %w", i+1, err)
			}
			r.re = re
		}
	}
	return nil
}

func rule(tool, match, decision, reason string) Rule {
	return Rule{Tool: tool, Match: match, Decision: decision, Reason: reason, re: regexp.MustCompile(match)}
}

const (
	shellBoundary = `(^|[;&|(]\s*|\s)` // the start of a command in a shell line
	secretVars    = `CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY|GH_TOKEN|GITHUB_TOKEN|OPENAI_API_KEY|GOOGLE_GENERATIVE_AI_API_KEY|MISTRAL_API_KEY|OPENROUTER_API_KEY|ERIPHE_CODEX_AUTH|CODEX_HOME`
)

var builtinDeny = []Rule{
	rule("Bash", shellBoundary+`rm\s+(\S+\s+)*(-[a-zA-Z]*[rR][a-zA-Z]*|--recursive)(\s|$)`,
		decideDeny, "suppression récursive interdite (rm -r)"),
	rule("Bash", `git\s+push\b[^;&|]*(\s|:)(main|master)\b`, decideDeny, "pousser sur main est interdit : pousse une branche"),
	rule("Bash", `git\s+push\b[^;&|]*(\s--force\b|\s-f\b|\s--force-with-lease\b|\s\+\S)`, decideDeny, "forcer un push est interdit"),
	rule("Bash", `git\s+(reset\s+--hard|clean\s+-[a-zA-Z]*f|filter-branch|filter-repo)`, decideDeny, "réécrire ou effacer l'historique est interdit"),
	rule("Bash", `\$\{?(`+secretVars+`)\b|/proc/[^/\s]+/environ|`+shellBoundary+`(printenv|env|export\s+-p|set)\s*($|[;&|>])|\.credentials\.json|eriphe-codex|codex/auth\.json|git\s+config\b[^;&|]*credential`,
		decideDeny, "lire les secrets de la tâche est interdit"),
}

var builtinAsk = []Rule{
	rule("Bash", `gh\s+(pr\s+merge|release\b|repo\s+(delete|edit|rename)|secret\b|api\b[^;&|]*-X\s*(POST|PUT|PATCH|DELETE))`,
		decideAsk, "agit sur GitHub au-delà d'une branche et d'une PR"),
	rule("Bash", shellBoundary+`(npm|pnpm|yarn)\s+publish|`+shellBoundary+`(cargo|twine|gem)\s+(publish|upload|push)|docker\s+push`,
		decideAsk, "publie un paquet ou une image"),
	rule("Bash", shellBoundary+`(ssh|scp|sftp|rsync)\s|`+shellBoundary+`(curl|wget|http)\s[^;&|]*(-X\s*(POST|PUT|PATCH|DELETE)|--data|\s-d\s|--form|\s-F\s|--upload-file|--post)`,
		decideAsk, "envoie quelque chose à une autre machine ou un service"),
	rule("mcp__*", ``, decideAsk, "outil externe"),
}

// subject is what a tool acts on, which rules match against: for an MCP
// tool, the JSON of its input.
func subject(tool string, input map[string]any) string {
	if strings.HasPrefix(tool, "mcp__") {
		if input == nil {
			input = map[string]any{}
		}
		raw, _ := json.Marshal(input)
		return string(raw)
	}
	for _, k := range []string{"command", "file_path", "notebook_path", "url", "path"} {
		if v, ok := input[k].(string); ok && v != "" {
			if k == "file_path" || k == "notebook_path" {
				return path.Clean(v)
			}
			return v
		}
	}
	raw, _ := json.Marshal(input)
	return string(raw)
}

func (r Rule) matches(tool, subj string) bool {
	return r.forTool(tool) && (r.re == nil || r.re.MatchString(subj))
}

// forTool tells whether a rule is about a tool, whatever it acts on.
func (r Rule) forTool(tool string) bool {
	for _, t := range strings.Split(r.Tool, "|") {
		if m, _ := path.Match(t, tool); m {
			return true
		}
	}
	return false
}

// names tells whether a rule of the configuration is about a tool.
func (p *Policy) names(tool string) bool {
	for _, r := range p.rules() {
		if r.forTool(tool) {
			return true
		}
	}
	return false
}

var writeTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true}

// decide applies the policy to a permission request.
func (p *Policy) decide(tool string, input map[string]any) (decision, reason string) {
	return p.decideWith(tool, input, false)
}

// decideWith is decide knowing whether the tool is a read-only catalog tool
// of a server trusted for its annotations.
func (p *Policy) decideWith(tool string, input map[string]any, readOnly bool) (decision, reason string) {
	subj := subject(tool, input)
	if writeTools[tool] && strings.HasPrefix(subj, "/") && subj != "/work" && !strings.HasPrefix(subj, "/work/") &&
		!strings.HasPrefix(subj, "/tmp/") {
		return decideDeny, "écrire hors du dossier de travail (/work) est interdit"
	}
	for _, set := range [][]Rule{builtinDeny, p.rules()} {
		for _, r := range set {
			if r.matches(tool, subj) {
				return r.Decision, r.Reason
			}
		}
	}
	if readOnly {
		return decideBrain, ""
	}
	for _, r := range builtinAsk {
		if r.matches(tool, subj) {
			return r.Decision, r.Reason
		}
	}
	return decideBrain, ""
}

// decide applies the policy, with what the catalog knows of the tool.
func (c *core) decide(tool string, input map[string]any) (decision, reason string) {
	return c.conf().Policy.decideWith(tool, input, c.pool.readOnly(tool))
}

func (p *Policy) rules() []Rule {
	if p == nil {
		return nil
	}
	return p.Rules
}

// applyPolicy answers the new permission requests of a worker that the
// policy decides, and marks those only the user may answer. It returns true
// when some request is left for the brain (or, through it, the user).
func (c *core) applyPolicy(id string, events []EventRow) bool {
	if id == c.store.get("brain_task") {
		return false
	}
	left := false
	for _, e := range events {
		if e.Type != "permission" {
			continue
		}
		pid := fmt.Sprint(e.Data["id"])
		tool := fmt.Sprint(e.Data["tool_name"])
		input, _ := e.Data["input"].(map[string]any)
		decision, reason := c.decide(tool, input)
		switch decision {
		case decideAllow, decideDeny:
			if c.store.answered(id, pid) {
				continue
			}
			allow := decision == decideAllow
			msg := ""
			if !allow {
				msg = "Refusé par la politique d'Eriphe : " + reason + ". Ne cherche pas à le contourner ; fais autrement ou explique pourquoi c'est nécessaire."
			}
			c.store.addInbox(id, "permission", map[string]any{"id": pid, "allow": allow, "message": msg, "by": byPolicy})
			c.store.recordDecision(id, pid, tool, input, decision, byPolicy, reason)
		case decideAsk:
			c.store.markUserOnly(id, pid, reason)
			left = true
		default:
			left = true
		}
	}
	return left
}

// Decisions are kept, append-only, to read back what was allowed and by whom.
const decisionsSchema = `CREATE TABLE IF NOT EXISTS decisions (
  time INTEGER NOT NULL, task_id TEXT NOT NULL, permission_id TEXT NOT NULL,
  tool TEXT NOT NULL, subject TEXT NOT NULL, decision TEXT NOT NULL, by TEXT NOT NULL, reason TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS user_only (task_id TEXT NOT NULL, permission_id TEXT NOT NULL, reason TEXT NOT NULL,
  PRIMARY KEY (task_id, permission_id));`

func init() { extraSchema = append(extraSchema, decisionsSchema) }

// recordDecision keeps a decision; allowing an MCP tool also gives the call
// its ticket through the proxy (proxy.go).
func (s *Store) recordDecision(taskID, pid, tool string, input map[string]any, decision, by, reason string) {
	s.logDecision(taskID, pid, tool, input, decision, by, reason)
	if decision == decideAllow && strings.HasPrefix(tool, "mcp__") {
		s.addTicket(taskID, tool, input)
	}
}

// logDecision keeps a decision only: the brain's free reads, which need no
// ticket, have no permission id either.
func (s *Store) logDecision(taskID, pid, tool string, input map[string]any, decision, by, reason string) {
	s.db.Exec(`INSERT INTO decisions (time, task_id, permission_id, tool, subject, decision, by, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, time.Now().Unix(), taskID, pid, tool, subject(tool, input), decision, by, reason)
}

func (s *Store) markUserOnly(taskID, pid, reason string) {
	s.db.Exec(`INSERT OR IGNORE INTO user_only (task_id, permission_id, reason) VALUES (?, ?, ?)`, taskID, pid, reason)
}

// userOnly returns why a permission is the user's to answer, or "".
func (s *Store) userOnly(taskID, pid string) string {
	var reason string
	s.db.QueryRow(`SELECT reason FROM user_only WHERE task_id = ? AND permission_id = ?`, taskID, pid).Scan(&reason)
	return reason
}

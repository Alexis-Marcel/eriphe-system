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
// Every permission request goes through it first:
//
//  1. the built-in denials, which nothing overrides (deleting a shared folder
//     wholesale, pushing to main, forcing a push, reading secrets);
//  2. a catalog tool at Never, by its exception or its category, and a call
//     that must name its account;
//  3. a tool lent held back is the user's until its release;
//  4. a catalog tool's exception, then the actions the user allowed for good
//     ("Toujours autoriser", Free exceptions that do not lift a Never);
//  5. its category's value in the grid of the account the call names: On
//     approval is the user's, Free is allowed, unless it writes on a tainted
//     turn or a task that did not start cleanly: then it is On approval too;
//  6. an MCP tool the catalog does not know is refused: it is not lent;
//  7. anything else acts in the task's container, and no rule stops it.
//
// Every decision, whoever takes it, is recorded in the decisions table.

const (
	decideAllow  = "allow"
	decideDeny   = "deny"
	decideAsk    = "ask" // only the user may answer
	decideNoRule = ""    // no rule: a tool of its container, or the brain's own
)

// Who answered a permission.
const (
	byPolicy = "policy"
	byBrain  = "brain"
	byUser   = "user"
)

// Rule is one line of the built-in policy: a tool (a name or a glob, e.g. "Bash" or
// "mcp__*"), a regular expression on what the tool acts on (a command, a
// path, a URL), a decision and its reason.
type Rule struct {
	Tool     string
	Match    string
	Decision string // allow, deny or ask
	Reason   string
	re       *regexp.Regexp
	// account, set, holds the rule to the calls that name this account
	// (an action allowed for good for one account: access.go); "" those
	// that name none.
	account *string
}

func policyRule(tool, match, decision, reason string) Rule {
	return Rule{Tool: tool, Match: match, Decision: decision, Reason: reason, re: regexp.MustCompile(match)}
}

const (
	shellBoundary = `(^|[;&|(]\s*|\s)` // the start of a command in a shell line
	secretVars    = `CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY|OPENAI_API_KEY|GOOGLE_GENERATIVE_AI_API_KEY|MISTRAL_API_KEY|OPENROUTER_API_KEY|ERIPHE_CODEX_AUTH|CODEX_HOME|ERIPHE_TASK_SECRET`
)

var builtinDeny = []Rule{
	// The task's container is its own: only what it was lent is worth a guard.
	policyRule("Bash", shellBoundary+`rm\s+([^;&|]*\s)?(-[a-zA-Z]*[rR][a-zA-Z]*|--recursive)\s([^;&|]*\s)?['"]?/work/folders|`+
		shellBoundary+`rm\s+([^;&|]*\s)?['"]?/work/folders[^;&|]*\s(-[a-zA-Z]*[rR][a-zA-Z]*|--recursive)(\s|$|[;&|])`,
		decideDeny, "suppression récursive interdite dans un dossier prêté (rm -r)"),
	policyRule("Bash", `git\s+push\b[^;&|]*(\s|:|refs/heads/)(main|master)(\s|$|[;&|'")])`, decideDeny, "pousser sur main est interdit : pousse une branche"),
	policyRule("Bash", `git\s+push\b[^;&|]*(\s--force\b|\s-f\b|\s--force-with-lease\b|\s\+\S)`, decideDeny, "forcer un push est interdit"),
	policyRule("Bash", `\$\{?(`+secretVars+`)\b|/proc/[^/\s]+/environ|`+shellBoundary+`(printenv|env|export\s+-p|set)\s*($|[;&|>])|\.credentials\.json|eriphe-codex|codex/auth\.json|git\s+config\b[^;&|]*credential`,
		decideDeny, "lire les secrets de la tâche est interdit"),
}

var builtinAsk = []Rule{
	policyRule("mcp__*", ``, decideAsk, "cet outil n'est pas dans le catalogue d'Eriphe"),
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
	return r.forTool(tool) && (r.re == nil || r.re.MatchString(subj)) && (r.account == nil || subjectAccount(subj, *r.account))
}

// subjectAccount tells whether an MCP call's input names the account.
func subjectAccount(subj, account string) bool {
	var in struct {
		Account any `json:"account"`
	}
	json.Unmarshal([]byte(subj), &in)
	named, _ := in.Account.(string)
	return strings.EqualFold(strings.TrimSpace(named), account)
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

// decideWith applies the policy to a permission request, knowing what the
// catalog says of the tool and the actions the user allowed for good
// (remembered: access.go).
func decideWith(tool string, input map[string]any, f catalogFacts, remembered []Rule) (decision, reason string) {
	subj := subject(tool, input)
	for _, r := range builtinDeny {
		if r.matches(tool, subj) {
			return r.Decision, r.Reason
		}
	}
	if f.noAccount {
		return decideDeny, noAccountReason
	}
	if f.value == gridNever {
		return decideDeny, neverReason // the proxy would refuse the call
	}
	if f.held {
		return decideAsk, heldReason
	}
	why := freeReason
	for _, r := range remembered {
		if !r.matches(tool, subj) {
			continue
		}
		if f.value == "" {
			return r.Decision, r.Reason
		}
		if f.value == gridApproval {
			f.value, why = gridFree, r.Reason
		}
		break
	}
	switch f.value {
	case gridApproval:
		if f.granted {
			return decideAllow, grantedReason
		}
		return decideAsk, approvalReason
	case gridFree:
		if f.unclean && !f.read && !f.granted {
			return decideAsk, uncleanReason
		}
		return decideAllow, why
	}
	for _, r := range builtinAsk {
		if r.matches(tool, subj) {
			return r.Decision, r.Reason
		}
	}
	return decideNoRule, ""
}

const (
	neverReason    = "cet outil n'est pas autorisé dans Eriphe"
	approvalReason = "cet outil demande ton accord"
	freeReason     = "accès libre"
	heldReason     = "outil retenu : il attend d'être débloqué"
	uncleanReason  = "accès libre, mais un contenu extérieur a été lu ou la tâche n'est pas partie de ta demande : il faut ton accord"
)

// decide applies the policy, with what the catalog knows of the tool.
func (c *core) decide(tool string, input map[string]any) (decision, reason string) {
	return c.decideFacts(tool, input, c.pool.facts(tool, input))
}

// decideTask is decide for a worker's request: its Free writes hold on a
// clean start only, whatever it read since.
func (c *core) decideTask(t *TaskRow, tool string, input map[string]any) (decision, reason string) {
	f := c.pool.facts(tool, input)
	f.unclean = t == nil || c.store.get(lentByUserKey(t.ID)) == ""
	if server, name, ok := splitToolName(tool); ok && t != nil {
		if u := c.pool.get(server); u != nil {
			if real := u.toolByClaudeName(name); real != nil {
				name = real.Name // a ref names the tool as its server does
			}
		}
		f.held = heldTool(t.Resources, server, name)
		f.granted = c.accessGranted(t.ID, c.callNeed(tool, input, f))
	}
	return c.decideFacts(tool, input, f)
}

// decideBrainCall is decide for the brain's request: its Free writes hold on
// a clean turn only; pending are its page reads that have not run yet.
func (c *core) decideBrainCall(tool string, input map[string]any, pending map[string]int) (decision, reason string) {
	f := c.pool.facts(tool, input)
	f.unclean = len(c.turnTaintBut(pending)) > 0
	return c.decideFacts(tool, input, f)
}

func (c *core) decideFacts(tool string, input map[string]any, f catalogFacts) (decision, reason string) {
	var remembered []Rule
	if strings.HasPrefix(tool, "mcp__") {
		remembered = c.store.allowedRules()
	}
	return decideWith(tool, input, f, remembered)
}

// applyPolicy answers the new permission requests of a worker, or marks them
// as the user's. It returns true when some request is left for the user.
func (c *core) applyPolicy(id string, events []EventRow) bool {
	if c.isAssistantTask(id) {
		return false // its role's approval path answers it (assistant_controller.go)
	}
	t, _ := c.store.task(id)
	left := false
	for _, e := range events {
		if e.Type != "permission" {
			continue
		}
		pid := fmt.Sprint(e.Data["id"])
		tool := fmt.Sprint(e.Data["tool_name"])
		input, _ := e.Data["input"].(map[string]any)
		decision, reason := c.decideTask(t, tool, input)
		if decision == decideNoRule && inContainer(tool) {
			decision = decideAllow
		}
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
			if t != nil && accessReason(reason) {
				if r := c.runOfTask(t.ID); r != nil {
					c.askRunCall(t, r, pid, tool, input) // kept for the run's card, the task goes on (skill_cards.go)
					continue
				}
				c.askTaskAccess(t, pid, tool, input, reason) // a card, once per category (access_card.go)
				left = true
				continue
			}
			if reason == heldReason {
				c.store.markUserOnly(id, pid, reason) // it waits for its release, on its card
				left = true
				continue
			}
			fallthrough
		default: // not in the catalog: nothing to ask, it is not lent
			if !c.store.answered(id, pid) {
				msg := "Refusé par la politique d'Eriphe : " + reason + "."
				c.store.addInbox(id, "permission", map[string]any{"id": pid, "allow": false, "message": msg, "by": byPolicy})
				c.store.recordDecision(id, pid, tool, input, decideDeny, byPolicy, reason)
			}
		}
	}
	return left
}

// inContainer tells whether a worker's tool acts only inside its task's
// container: its own tools (shell, files, web), not a lent catalog tool.
// The container is the boundary: what leaves it goes through
// the node's mounts (a folder's level), its egress filter and the
// credentials eriphe-core lends (repo_lend.go), never through the shell
// rules above, which only stop mistakes. So what the policy left undecided
// there is allowed, without waking the brain.
func inContainer(tool string) bool {
	return !strings.HasPrefix(tool, "mcp__")
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

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// Standing approvals (#244 step 5): what the user said yes to once, for
// good, on a card. A rule may carry one exact action: a catalog tool with
// exact arguments, or a run_task with a fixed prompt, agent, resources and
// contract ("if 2.0.1 breaks, put 2.0.0 back"). The brain proposes it only
// in a clean turn, citing the user's message that asks for it; it is then
// a card, and it holds only once the user says yes on it. When the brain,
// in that rule's own wake turn, tainted or not, calls exactly that action,
// the policy lets it through without asking; anything else still asks.
// Built-in denials still apply, an approval is used at most max_uses times
// (1 by default) and expires, and each use is logged loudly, recorded with
// the run (acted) and told to the user right after. Settings → Règles shows
// it and revokes it.
//
// Nothing read can make or widen one: one is proposed only on a turn that
// read nothing external; a change of the rule revokes it; and it matches
// by the hash of its exact, canonical input. A rule that fires on facts
// (when fait, or an if on facts) may carry one, and its card says so:
// facts orient and can be wrong (a calendar invite can say "busy"), so the
// user decides knowing it. Before #244 step 5 these were the watches'
// grants (table watch_grants), renamed at start.

const standingApprovalSchema = `CREATE TABLE IF NOT EXISTS standing_approvals (
  id INTEGER PRIMARY KEY AUTOINCREMENT, rule_id INTEGER NOT NULL, kind TEXT NOT NULL, tool TEXT NOT NULL,
  input TEXT NOT NULL, input_hash TEXT NOT NULL, max_uses INTEGER NOT NULL, uses INTEGER NOT NULL DEFAULT 0,
  expires INTEGER NOT NULL, source_ref TEXT NOT NULL, created INTEGER NOT NULL, revoked INTEGER, revoked_why TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS standing_approvals_rule ON standing_approvals (rule_id);`

func init() {
	extraSchema = append(extraSchema, standingApprovalSchema)
	// Added with standing approvals: proposed until the user's yes on its card.
	extraColumns = append(extraColumns, `ALTER TABLE standing_approvals ADD COLUMN state TEXT NOT NULL DEFAULT 'active'`)
}

const (
	cardApproval     = "approval" // a standing approval's card (convo_cards.go)
	approvalProposed = "proposed"
	approvalActive   = "active"
)

const (
	grantTool = "tool"
	grantTask = "task"

	grantMaxUses       = 10
	grantDefaultExpiry = 7 * 24 * time.Hour
	grantMaxExpiry     = 90 * 24 * time.Hour

	notifyFromWatch = "watch" // the notice of a used grant
)

// grantRequest: wording of the user's that authorises something.
var grantRequest = regexp.MustCompile(`(?i)(autoris|tu peux|vous pouvez|vas-y|fais-le|n'h[ée]site pas|remets|relance|red[ée]marr|rollback|reviens [àa]|annule|\bsi\b|allow|you can|you may|go ahead|do it|restart|revert|roll back|\bif\b)`)

// standingApproval is one authorised action.
type standingApproval struct {
	ID        int64
	RuleID    int64
	Kind      string
	Tool      string // its Claude Code name, or run_task
	Input     string // canonical JSON, shown to the user
	hash      string
	MaxUses   int
	Uses      int
	Expires   time.Time
	SourceRef string
	Created   time.Time
	Revoked   time.Time
	State     string // proposed until the user's yes, then active
}

// Left is how many uses it has left.
func (g *standingApproval) Left() int { return g.MaxUses - g.Uses }

// Live tells whether it may still be used.
func (g *standingApproval) Live(now time.Time) bool {
	return g.Revoked.IsZero() && g.Uses < g.MaxUses && g.Expires.After(now)
}

// grantAction is what the brain asks to authorise.
type grantAction struct {
	Tool    string         `json:"tool,omitempty" jsonschema:"a catalog tool, server.tool"`
	Args    map[string]any `json:"args,omitempty" jsonschema:"its exact arguments"`
	RunTask *runRequest    `json:"run_task,omitempty" jsonschema:"or a task: its exact prompt, agent, resources and contract"`
	MaxUses int            `json:"max_uses,omitempty" jsonschema:"how many times, 1 by default, 10 at most"`
	Expires string         `json:"expires,omitempty" jsonschema:"until when, YYYY-MM-DD[THH:MM] or a duration (7d, 48h); 7 days by default, 90 at most"`
}

// canonicalTask is a run_task as a grant matches it.
func canonicalTask(r runRequest) (string, error) {
	res, err := normalizeResources(r)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Prompt    string        `json:"prompt"`
		Agent     string        `json:"agent,omitempty"`
		Node      string        `json:"node,omitempty"`
		Resources []ResourceRef `json:"resources,omitempty"`
		Contract  *taskContract `json:"contract,omitempty"`
	}{strings.TrimSpace(r.Prompt), r.Agent, r.Node, res, r.Contract})
	return string(raw), err
}

func canonicalArgs(args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	raw, _ := json.Marshal(args)
	return string(raw)
}

// proposeApproval writes a watch's authorised action, replacing its
// previous one. Only on a clean turn, citing the user's message of it.
func (c *core) proposeApproval(w *rule, a grantAction, sourceRef string) (*standingApproval, error) {
	if tainted := c.turnTaint(); len(tainted) > 0 {
		return nil, fmt.Errorf("refused: this turn read external content (%s): an action is authorised in advance only in a turn that read nothing from outside, in %s's own words", strings.Join(tainted, ", "), c.conf().User)
	}
	from, to, _, _ := c.turn.snapshot()
	msg, owner := c.ownerMessage(sourceRef)
	switch {
	case !owner || from == 0 || msg.Seq < from || msg.Seq > to:
		return nil, errors.New("refused: cite the user's message of this turn that authorises it (source_ref msg:N)")
	case !grantRequest.MatchString(msg.Text):
		return nil, errors.New("refused: the user's message it cites does not authorise an action")
	case w.State != watchActive:
		return nil, errors.New("refused: the watch is not active: the user approves it first")
	}
	g := &standingApproval{RuleID: w.ID, MaxUses: a.MaxUses, SourceRef: strings.TrimSpace(sourceRef)}
	if g.MaxUses == 0 {
		g.MaxUses = 1
	}
	if g.MaxUses < 1 || g.MaxUses > grantMaxUses {
		return nil, fmt.Errorf("max_uses is 1 to %d", grantMaxUses)
	}
	now := c.notifyTime()
	exp, err := grantExpiry(a.Expires, now, c.notifyZone())
	if err != nil {
		return nil, err
	}
	g.Expires = exp
	switch {
	case a.RunTask != nil && a.Tool == "":
		g.Kind, g.Tool = grantTask, "run_task"
		if strings.TrimSpace(a.RunTask.Prompt) == "" {
			return nil, errors.New("run_task needs its prompt")
		}
		if g.Input, err = canonicalTask(*a.RunTask); err != nil {
			return nil, err
		}
	case a.Tool != "" && a.RunTask == nil:
		g.Kind = grantTool
		server, tool, ok := strings.Cut(strings.TrimSpace(a.Tool), ".")
		if !ok || server == "" || tool == "" {
			return nil, errors.New("tool is server.tool")
		}
		if _, inCatalog := c.conf().Servers[server]; !inCatalog || !grantAllows(c.brainGrant(), server, tool) {
			return nil, fmt.Errorf("%s is not one of the brain's catalog tools", a.Tool)
		}
		g.Tool, g.Input = claudeToolName(server, tool), canonicalArgs(a.Args)
		if d, why := c.decide(g.Tool, a.Args); d == decideDeny {
			return nil, fmt.Errorf("refused: Eriphe's policy denies this action (%s): it cannot be authorised", why)
		}
	default:
		return nil, errors.New("give either tool (with args) or run_task")
	}
	if scan := scanMemoryText(g.Input); scan.Reject != "" {
		return nil, fmt.Errorf("refused: %s", scan.Reject)
	}
	g.hash = hashToken(g.Kind + "\n" + g.Tool + "\n" + g.Input)
	c.revokeApprovals(w.ID, "replaced")
	g.State = approvalProposed
	res, err := c.store.db.Exec(`INSERT INTO standing_approvals (rule_id, kind, tool, input, input_hash, max_uses, expires, source_ref, created, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, g.RuleID, g.Kind, g.Tool, g.Input, g.hash, g.MaxUses, g.Expires.Unix(), g.SourceRef, now.Unix(), g.State)
	if err != nil {
		return nil, err
	}
	g.ID, _ = res.LastInsertId()
	g.Created = now
	log.Printf("rule %d: standing approval proposed: %s %s, %d use(s), until %s (%s)", w.ID, g.Tool, truncate(g.Input, 200),
		g.MaxUses, g.Expires.Format(time.RFC3339), g.SourceRef)
	c.cardProposed(cardApproval, g.ID) // it holds only once the user says yes on it
	return g, nil
}

// grantExpiry reads when a grant ends: a date, a time, or a duration.
func grantExpiry(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	exp := now.Add(grantDefaultExpiry)
	if s != "" {
		var err error
		switch {
		case strings.HasSuffix(s, "d"):
			var n int
			if _, err = fmt.Sscanf(s, "%dd", &n); err == nil {
				exp = now.Add(time.Duration(n) * 24 * time.Hour)
			}
		case strings.ContainsAny(s, "hm") && !strings.Contains(s, ":"):
			var d time.Duration
			if d, err = time.ParseDuration(s); err == nil {
				exp = now.Add(d)
			}
		default:
			if exp, err = time.ParseInLocation("2006-01-02T15:04", strings.Replace(s, " ", "T", 1), loc); err != nil {
				exp, err = time.ParseInLocation(time.DateOnly, s, loc)
				exp = exp.Add(24*time.Hour - time.Minute)
			}
		}
		if err != nil {
			return time.Time{}, errors.New("expires is YYYY-MM-DD, YYYY-MM-DDTHH:MM or a duration like 7d or 48h")
		}
	}
	if !exp.After(now) || exp.Sub(now) > grantMaxExpiry {
		return time.Time{}, errors.New("expires is in the future, within 90 days")
	}
	return exp, nil
}

const grantCols = `id, rule_id, kind, tool, input, input_hash, max_uses, uses, expires, source_ref, created, COALESCE(revoked, 0), state`

func (c *core) standingApprovals(where string, args ...any) ([]*standingApproval, error) {
	rows, err := c.store.db.Query(`SELECT `+grantCols+` FROM standing_approvals WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*standingApproval
	for rows.Next() {
		var g standingApproval
		var exp, created, revoked int64
		if err := rows.Scan(&g.ID, &g.RuleID, &g.Kind, &g.Tool, &g.Input, &g.hash, &g.MaxUses, &g.Uses, &exp, &g.SourceRef,
			&created, &revoked, &g.State); err != nil {
			return nil, err
		}
		g.Expires, g.Created = time.Unix(exp, 0), time.Unix(created, 0)
		if revoked > 0 {
			g.Revoked = time.Unix(revoked, 0)
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// liveApproval is a rule's standing approval that may still be used, if
// any: given on its card, not used up, not expired.
func (c *core) liveApproval(ruleID int64) *standingApproval {
	if g := c.ruleApproval(ruleID); g != nil && g.State == approvalActive {
		return g
	}
	return nil
}

// ruleApproval is a rule's standing approval, proposed or given, while it
// may still be used.
func (c *core) ruleApproval(ruleID int64) *standingApproval {
	gs, _ := c.standingApprovals(`rule_id = ? AND revoked IS NULL ORDER BY id DESC LIMIT 1`, ruleID)
	if len(gs) == 1 && gs[0].Live(c.notifyTime()) {
		return gs[0]
	}
	return nil
}

// revokeApprovals ends a watch's grants.
func (c *core) revokeApprovals(watchID int64, why string) {
	res, _ := c.store.db.Exec(`UPDATE standing_approvals SET revoked = ?, revoked_why = ? WHERE rule_id = ? AND revoked IS NULL`,
		c.notifyTime().Unix(), why, watchID)
	if res != nil {
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("watch %d: authorised action revoked (%s)", watchID, why)
		}
	}
}

// useApproval lets an action through if it is exactly the one the
// watch being woken for carries, still live; it counts the use, records
// the run as acted, and tells the user. False for anything else.
func (c *core) useApproval(kind, tool, input string) bool {
	wt, ok := c.currentRuleTurn()
	if !ok {
		return false
	}
	now := c.notifyTime()
	hash := hashToken(kind + "\n" + tool + "\n" + input)
	res, err := c.store.db.Exec(`UPDATE standing_approvals SET uses = uses + 1 WHERE id = (SELECT id FROM standing_approvals
		WHERE rule_id = ? AND kind = ? AND tool = ? AND input_hash = ? AND revoked IS NULL AND state = 'active' AND uses < max_uses AND expires > ?
		ORDER BY id DESC LIMIT 1)`, wt.Rule, kind, tool, hash, now.Unix())
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false
	}
	w, _ := c.rule(wt.Rule)
	name := fmt.Sprintf("#%d", wt.Rule)
	if w != nil {
		name = w.Name
	}
	action := truncate(tool+" "+input, 300)
	log.Printf("STANDING APPROVAL USED: rule %d (%s), run %d: %s", wt.Rule, name, wt.Run, action)
	c.store.db.Exec(`UPDATE rule_runs SET outcome = ?, action = ? WHERE id = ?`, runActed, action, wt.Run)
	text := fmt.Sprintf("Règle « %s » : j'ai utilisé ton accord permanent : %s.", name, truncate(tool+" "+input, 200))
	c.store.addMessage(roleBrain, "("+text+")", true)
	n := &notification{Text: truncate(text, notifyTextMax), Why: "Un accord permanent que tu as donné a été utilisé, sans te redemander.",
		Ref: fmt.Sprintf("watch:%d", wt.Rule), Source: notifyFromWatch}
	if _, err := c.notify(n, notifyItem{Level: levelNow, Ref: n.Ref, Asked: true}); err != nil {
		log.Printf("watch %d: telling the user of the action: %v", wt.Rule, err)
	}
	return true
}

// The standing approval's card.

// standingApproval is one by id.
func (c *core) standingApproval(id int64) (*standingApproval, error) {
	gs, err := c.standingApprovals(`id = ?`, id)
	if err != nil || len(gs) == 0 {
		return nil, errors.New("no such standing approval")
	}
	return gs[0], nil
}

// proposedApprovals are the standing approvals waiting for the user's yes.
func (c *core) proposedApprovals() []*standingApproval {
	gs, _ := c.standingApprovals(`state = ? AND revoked IS NULL ORDER BY id`, approvalProposed)
	return gs
}

// decideApproval is the user's answer on its card: yes makes it hold, no
// revokes it.
func (c *core) decideApproval(id int64, approve bool) error {
	g, err := c.standingApproval(id)
	if err != nil {
		return err
	}
	if g.State != approvalProposed || !g.Revoked.IsZero() {
		return errors.New("not proposed")
	}
	if !approve {
		_, err := c.store.db.Exec(`UPDATE standing_approvals SET revoked = ?, revoked_why = 'refused on its card' WHERE id = ?`, c.notifyTime().Unix(), id)
		return err
	}
	if w, err := c.rule(g.RuleID); err != nil || w.State != watchActive {
		return errors.New("its rule is not active any more")
	}
	_, err = c.store.db.Exec(`UPDATE standing_approvals SET state = ? WHERE id = ?`, approvalActive, id)
	log.Printf("rule %d: standing approval %d given on its card: %s %s", g.RuleID, id, g.Tool, truncate(g.Input, 200))
	return err
}

// approvalText is what its card says, in the user's language: the exact
// call, how many times and until when, and when its rule fires on facts,
// that facts can be wrong.
func (c *core) approvalText(g *standingApproval, lang string) string {
	loc := c.notifyZone()
	s := fmt.Sprintf(tr(lang, "approval.card"), grantCallName(g), truncate(g.Input, 300), g.MaxUses, g.Expires.In(loc).Format("2006-01-02 15:04"))
	if w, err := c.rule(g.RuleID); err == nil {
		s = w.Name + " · " + s
		if paths := ruleFactPaths(w); len(paths) > 0 {
			s += " " + fmt.Sprintf(tr(lang, "approval.facts"), strings.Join(paths, ", "))
		}
	}
	return s
}

// ruleFactPaths are the facts a rule fires on: the one it follows, and
// those of its condition (not the time).
func ruleFactPaths(w *rule) []string {
	var out []string
	if w.Source == sourceFact {
		out = append(out, w.Spec.Fact)
	}
	if e, err := parseCond(w.If); err == nil && w.If != "" {
		for _, t := range e.terms() {
			if !strings.HasPrefix(t.Path, "now.") && !containsStr(out, t.Path) {
				out = append(out, t.Path)
			}
		}
	}
	return out
}

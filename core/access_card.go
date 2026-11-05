package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"
)

// The approval of a catalog tool: a card of the conversation, the user's
// only (convo_cards.go, kind access): a click, or a plain yes or no read by
// eriphe-core, never the brain.
//
//   - A task's yes grants it the category, in the account, until it ends:
//     asked at launch on its start's card (task_start.go), on the release of
//     a tool lent held back, or at the first call not granted yet.
//   - The brain's yes allows that one call: a one-off action.
//
// Each answer is kept in the decisions, and a grant shows in the task's
// drawer.

const accessCardSchema = `CREATE TABLE IF NOT EXISTS access_grants (
	task_id TEXT NOT NULL, server TEXT NOT NULL, category TEXT NOT NULL, account TEXT NOT NULL, time INTEGER NOT NULL,
	PRIMARY KEY (task_id, server, category, account));
CREATE TABLE IF NOT EXISTS access_asks (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id       TEXT NOT NULL,
	permission_id TEXT NOT NULL DEFAULT '', -- the brain's call, or a task's first
	server        TEXT NOT NULL DEFAULT '',
	category      TEXT NOT NULL DEFAULT '',
	account       TEXT NOT NULL DEFAULT '',
	call          TEXT NOT NULL DEFAULT '', -- the brain's: the tool and its input
	refs          TEXT NOT NULL DEFAULT '', -- a release: the held tools, as JSON
	tainted       TEXT NOT NULL DEFAULT '',
	state         TEXT NOT NULL DEFAULT 'proposed',
	created       INTEGER NOT NULL,
	decided       INTEGER NOT NULL DEFAULT 0);`

func init() { extraSchema = append(extraSchema, accessCardSchema) }

const (
	cardAccess    = "access"
	grantedReason = "accordé par l'utilisateur pour cette tâche"
)

// accessReason tells whether a decision to ask comes from the grid, and so
// is asked on a card; a rule's goes as a permission, with its input.
func accessReason(reason string) bool { return reason == approvalReason || reason == uncleanReason }

// accessNeed is one category of one account a task needs the user's yes for.
type accessNeed struct{ Server, Category, Account string }

func (s *Store) grantAccess(taskID string, n accessNeed) {
	s.db.Exec(`INSERT OR IGNORE INTO access_grants (task_id, server, category, account, time) VALUES (?, ?, ?, ?, ?)`,
		taskID, n.Server, n.Category, n.Account, time.Now().Unix())
	s.logDecision(taskID, "", "access "+n.Server+"."+n.Category, map[string]any{"account": n.Account}, decideAllow, byUser, grantedReason)
}

// accessGranted tells whether a task holds a category: for the account a
// call names, or, for a call that names none, for every account.
func (c *core) accessGranted(taskID string, n accessNeed) bool {
	held := func(account string) bool {
		var k int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM access_grants WHERE task_id = ? AND server = ? AND category = ? AND account = ? COLLATE NOCASE`,
			taskID, n.Server, n.Category, account).Scan(&k)
		return k > 0
	}
	if held("") {
		return true
	}
	if n.Account != "" {
		return held(n.Account)
	}
	accounts := c.conf().Servers[n.Server].accountAccesses()
	for a := range accounts {
		if !held(a) {
			return false
		}
	}
	return len(accounts) > 0
}

// accessGrants are what a task was granted, for its drawer.
func (s *Store) accessGrants(taskID string) []accessNeed {
	rows, err := s.db.Query(`SELECT server, category, account FROM access_grants WHERE task_id = ? ORDER BY time, server, category, account`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []accessNeed
	for rows.Next() {
		var n accessNeed
		if rows.Scan(&n.Server, &n.Category, &n.Account) == nil {
			out = append(out, n)
		}
	}
	return out
}

// callNeed is what a call of a catalog tool needs granted.
func (c *core) callNeed(tool string, input map[string]any, f catalogFacts) accessNeed {
	server, _, _ := splitToolName(tool)
	return accessNeed{server, f.category, c.callAccount(server, input)}
}

// refNeeds is what lending one tool entry needs of the user: each category
// it covers, in each account, that is On approval, or Free above Read on a
// start that is not clean.
func (c *core) refNeeds(ref string, clean bool) []accessNeed {
	server, _, _ := strings.Cut(strings.TrimSpace(ref), ".")
	u := c.pool.get(server)
	if u == nil {
		return nil
	}
	accesses := u.srv.accountAccesses()
	if len(accesses) == 0 {
		accesses = map[string]serverAccess{"": u.srv.baseAccess()}
	}
	seen := map[accessNeed]bool{}
	_, _, tools := u.snapshot()
	for _, t := range tools {
		if !grantAllows([]string{ref}, server, t.Name) {
			continue
		}
		for account, a := range accesses {
			category, value := a.of(t)
			if value == gridApproval || (value == gridFree && category != catRead && !clean) {
				seen[accessNeed{server, category, account}] = true
			}
		}
	}
	return sortNeeds(slices.Collect(maps.Keys(seen)))
}

func sortNeeds(needs []accessNeed) []accessNeed {
	slices.SortFunc(needs, func(a, b accessNeed) int {
		if d := strings.Compare(a.Server+"\x00"+a.Account, b.Server+"\x00"+b.Account); d != 0 {
			return d
		}
		return slices.Index(categories, a.Category) - slices.Index(categories, b.Category)
	})
	return needs
}

// accessStartNeeds is what a task's lend needs of the user at launch: what
// it is lent outright, a held tool waiting for its release.
func (c *core) accessStartNeeds(list []ResourceRef, clean bool) []startNeed {
	seen := map[accessNeed]bool{}
	for _, e := range list {
		if e.Kind != resTool || e.Hold {
			continue
		}
		for _, n := range c.refNeeds(e.Ref, clean) {
			seen[n] = true
		}
	}
	var out []startNeed
	for _, n := range sortNeeds(slices.Collect(maps.Keys(seen))) {
		out = append(out, startNeed{Server: n.Server, Category: n.Category, Account: n.Account, Reason: "lent " + n.Category + " on approval"})
	}
	return out
}

// grantLent grants a task what it is lent outright: the user approved it in
// advance, for a watch.
func (c *core) grantLent(t *TaskRow) {
	for _, e := range t.Resources {
		if e.Kind == resTool && !e.Hold {
			for _, n := range c.refNeeds(e.Ref, true) {
				c.store.grantAccess(t.ID, n)
			}
		}
	}
}

// accessQuestion is one card's question, as the store keeps it.
type accessQuestion struct {
	ID         int64
	TaskID     string
	Permission string
	Need       accessNeed
	Call       string
	Refs       []string
	Tainted    string
	State      string
}

func (c *core) accessAskOf(id int64) (accessQuestion, error) {
	var a accessQuestion
	var refs string
	err := c.store.db.QueryRow(`SELECT id, task_id, permission_id, server, category, account, call, refs, tainted, state FROM access_asks WHERE id = ?`, id).
		Scan(&a.ID, &a.TaskID, &a.Permission, &a.Need.Server, &a.Need.Category, &a.Need.Account, &a.Call, &refs, &a.Tainted, &a.State)
	json.Unmarshal([]byte(refs), &a.Refs)
	return a, err
}

func (c *core) proposedAccess() []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM access_asks WHERE state = ? ORDER BY id`, startProposed)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// accessAsked tells whether a card holds a permission's answer: the raw
// request is then not shown too.
func (c *core) accessAsked(taskID, pid string) bool {
	var k int
	c.store.db.QueryRow(`SELECT COUNT(*) FROM access_asks WHERE task_id = ? AND permission_id = ? AND state = ?`, taskID, pid, startProposed).Scan(&k)
	if k > 0 {
		return true
	}
	return c.store.get(accessWaitKey(taskID, pid)) != ""
}

// accessWaitKey marks a task's request that waits on a card asked for an
// earlier call of the same category.
func accessWaitKey(taskID, pid string) string { return "access_wait:" + taskID + ":" + pid }

// askTaskAccess asks a task's category at its first call: one card, however
// many calls wait on it. The requests are the user's meanwhile.
func (c *core) askTaskAccess(t *TaskRow, pid, tool string, input map[string]any, reason string) {
	c.store.markUserOnly(t.ID, pid, reason)
	c.store.set(accessWaitKey(t.ID, pid), "1")
	n := c.callNeed(tool, input, c.pool.facts(tool, input))
	var id int64
	err := c.store.db.QueryRow(`SELECT id FROM access_asks WHERE task_id = ? AND server = ? AND category = ? AND account = ? AND refs = '' AND state = ?`,
		t.ID, n.Server, n.Category, n.Account, startProposed).Scan(&id)
	if err == nil {
		return
	}
	c.newAccessAsk(accessQuestion{TaskID: t.ID, Permission: pid, Need: n, Tainted: c.store.get(taskTaintKey(t.ID))})
}

// askBrainAccess asks one call of the brain: On approval, or a Free write on
// a tainted turn.
func (c *core) askBrainAccess(t *TaskRow, pid, tool string, input map[string]any, reason string, pending map[string]int) {
	c.store.markUserOnly(t.ID, pid, reason)
	raw, _ := json.Marshal(input)
	c.newAccessAsk(accessQuestion{TaskID: t.ID, Permission: pid, Need: c.callNeed(tool, input, c.pool.facts(tool, input)),
		Call: tool + " " + truncate(string(raw), 300), Tainted: strings.Join(c.turnTaintBut(pending), ", ")})
}

// newAccessAsk keeps a question and shows its card now: the brain or the
// task is stopped on it, so no later message will carry it. Its message
// goes to Telegram with it: the same conversation, and the same card.
func (c *core) newAccessAsk(a accessQuestion) int64 {
	refs := ""
	if len(a.Refs) > 0 {
		raw, _ := json.Marshal(a.Refs)
		refs = string(raw)
	}
	res, err := c.store.db.Exec(`INSERT INTO access_asks (task_id, permission_id, server, category, account, call, refs, tainted, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.TaskID, a.Permission, a.Need.Server, a.Need.Category, a.Need.Account, a.Call, refs, a.Tainted, time.Now().Unix())
	if err != nil {
		log.Printf("access: %v", err)
		return 0
	}
	id, _ := res.LastInsertId()
	c.cardProposed(cardAccess, id)
	seq, err := c.store.addMessage(roleBrain, fmt.Sprintf("(%s attend ton accord.)", c.conf().assistantName()), true)
	if err != nil {
		return id
	}
	c.store.db.Exec(`UPDATE convo_cards SET seq = ?, turn = 0, attached = ? WHERE ref = ?`, seq, time.Now().Unix(), proposalRef(cardAccess, id))
	c.store.set(tgMsgKey(int(seq)), "1") // its card goes to Telegram, without waiting for a reply there
	log.Printf("access: card %d for task %s: %s", id, a.TaskID, a.Need.Category)
	c.bump()
	return id
}

// accessProposal reads a question as a proposal of the cards.
func (c *core) accessProposal(p proposal) proposal {
	a, err := c.accessAskOf(p.ID)
	if err != nil {
		return p
	}
	switch a.State {
	case startApproved:
		p.Outcome = cardApproved
	case startRejected:
		p.Outcome = cardRejected
	default:
		t, err := c.store.task(a.TaskID)
		p.Pending = err == nil && !finished(t.State)
	}
	p.Tainted = a.Tainted
	p.text = func(lang string) string { return c.accessText(a, lang) }
	return p
}

// verbs says categories as an action, in lang.
func verbs(list []string, lang string) string {
	var out []string
	for _, c := range list {
		out = append(out, tr(lang, "access.verb."+c))
	}
	return strings.Join(out, ", ")
}

// accessTarget is where a need acts: its account, or its server.
func accessTarget(n accessNeed) string { return cmpOr(n.Account, n.Server) }

func (c *core) accessText(a accessQuestion, lang string) string {
	title := truncate(oneLine(c.taskTitle(a.TaskID)), 60)
	switch {
	case len(a.Refs) > 0:
		return fmt.Sprintf(tr(lang, "access.ask.release"), title, strings.Join(a.Refs, ", "))
	case a.Call != "":
		return fmt.Sprintf(tr(lang, "access.ask.brain"), c.conf().assistantName(), verbs([]string{a.Need.Category}, lang), accessTarget(a.Need), a.Call)
	}
	return fmt.Sprintf(tr(lang, "access.ask.task"), title, verbs([]string{a.Need.Category}, lang), accessTarget(a.Need))
}

// taskTitle is how a card names a task: the start of what it was asked.
func (c *core) taskTitle(id string) string {
	t, err := c.store.task(id)
	if err != nil {
		return id
	}
	return t.Prompt
}

// accessStartText says what a start's card asks of the grid: a sentence per
// account, its categories together.
func accessStartText(needs []startNeed, lang string) []string {
	var order []string
	byTarget := map[string][]string{}
	for _, n := range needs {
		if n.Server == "" {
			continue
		}
		target := accessTarget(accessNeed{n.Server, n.Category, n.Account})
		if byTarget[target] == nil {
			order = append(order, target)
		}
		byTarget[target] = append(byTarget[target], n.Category)
	}
	var out []string
	for _, target := range order {
		out = append(out, fmt.Sprintf(tr(lang, "access.ask.start"), verbs(byTarget[target], lang), target))
	}
	return out
}

// decideAccess is the user's answer to a card.
func (c *core) decideAccess(ctx context.Context, id int64, approve bool) error {
	a, err := c.accessAskOf(id)
	if err != nil {
		return errNoCard
	}
	state := startRejected
	if approve {
		state = startApproved
	}
	res, err := c.store.db.Exec(`UPDATE access_asks SET state = ?, decided = ? WHERE id = ? AND state = ?`, state, time.Now().Unix(), id, startProposed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errCardDecided
	}
	switch {
	case len(a.Refs) > 0:
		if approve {
			return c.releaseApproved(a)
		}
		c.store.logDecision(a.TaskID, "", "release "+strings.Join(a.Refs, ", "), nil, decideDeny, byUser, "refusé sur la carte")
		return nil
	case a.Call != "":
		msg := ""
		if !approve {
			msg = c.conf().User + " a refusé cette action."
		}
		_, err := c.answer(ctx, a.TaskID, a.Permission, msg, &approve, byUser)
		return err
	}
	if approve {
		c.store.grantAccess(a.TaskID, a.Need)
	} else {
		c.store.logDecision(a.TaskID, "", "access "+a.Need.Server+"."+a.Need.Category, map[string]any{"account": a.Need.Account}, decideDeny, byUser, "refusé sur la carte")
	}
	c.settleWaiting(ctx, a, approve)
	return nil
}

// settleWaiting answers the requests of a task that waited on a card: what
// the yes now covers, or, on a no, those of the same category and account.
func (c *core) settleWaiting(ctx context.Context, a accessQuestion, approve bool) {
	st, err := c.status(a.TaskID, 0)
	if err != nil {
		return
	}
	for _, p := range st.PendingPermissions {
		pid, tool := fmt.Sprint(p["id"]), fmt.Sprint(p["tool_name"])
		if c.store.get(accessWaitKey(a.TaskID, pid)) == "" || c.store.answered(a.TaskID, pid) {
			continue
		}
		input, _ := p["input"].(map[string]any)
		if c.callNeed(tool, input, c.pool.facts(tool, input)) != a.Need {
			continue
		}
		c.store.set(accessWaitKey(a.TaskID, pid), "") // from here on it shows as a permission, if it still waits
		allow, msg := approve, ""
		if !approve {
			msg = "Refusé par " + c.conf().User + " : fais autrement ou explique pourquoi c'est nécessaire."
		} else if d, _ := c.decideTask(st.Task, tool, input); d != decideAllow {
			continue
		}
		if _, err := c.answer(ctx, a.TaskID, pid, msg, &allow, byUser); err != nil {
			log.Printf("access: task %s, permission %s: %v", a.TaskID, pid, err)
		}
	}
}

// releaseTools asks the user to release tools a task was lent held back. It
// authorizes nothing: only their yes on the card does.
func (c *core) releaseTools(id string, refs []string) (string, error) {
	t, err := c.store.task(id)
	if err != nil {
		return "", err
	}
	if t.Kind != "worker" || finished(t.State) {
		return "", fmt.Errorf("task %s is not a running worker: nothing to release", id)
	}
	var held, asked []string
	for _, e := range t.Resources {
		if e.Kind == resTool && e.Hold {
			held = append(held, e.Ref)
		}
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !slices.Contains(held, ref) {
			if len(held) == 0 {
				return "", fmt.Errorf("%q is not held back: the task was lent no held tool (only a tool lent with hold is released)", ref)
			}
			return "", fmt.Errorf("%q is not held back for this task: its held tools are %s", ref, strings.Join(held, ", "))
		}
		asked = append(asked, ref)
	}
	if len(asked) == 0 {
		return "", errors.New("release: name the held tools to release")
	}
	c.newAccessAsk(accessQuestion{TaskID: id, Refs: asked, Tainted: strings.Join(c.turnTaint(), ", ")})
	return "asked " + c.conf().User + " on a card to release " + strings.Join(asked, ", ") + ": nothing is released until they answer; you will be told", nil
}

// releaseApproved lends the held tools outright, with what they need
// granted: the card named them.
func (c *core) releaseApproved(a accessQuestion) error {
	t, err := c.store.task(a.TaskID)
	if err != nil {
		return err
	}
	list := slices.Clone(t.Resources)
	for i := range list {
		if list[i].Kind == resTool && list[i].Hold && slices.Contains(a.Refs, list[i].Ref) {
			list[i].Hold = false
		}
	}
	if err := c.store.updateTask(a.TaskID, map[string]any{"resources": marshalResources(list)}); err != nil {
		return err
	}
	clean := c.store.get(lentByUserKey(a.TaskID)) != ""
	for _, ref := range a.Refs {
		for _, n := range c.refNeeds(ref, clean) {
			c.store.grantAccess(a.TaskID, n)
		}
		c.store.logDecision(a.TaskID, "", "release "+ref, nil, decideAllow, byUser, "débloqué sur la carte")
	}
	log.Printf("task %s: released %s by the user", a.TaskID, strings.Join(a.Refs, ", "))
	return nil
}

// accessDecidedTold tells the brain the answer to a release it asked for,
// so it can tell the task (a yes or no in words is told with the message).
func (c *core) accessDecidedTold(id int64, approve bool) {
	a, err := c.accessAskOf(id)
	if err != nil || len(a.Refs) == 0 {
		return
	}
	word := "refusé"
	if approve {
		word = "accepté"
	}
	c.store.addMessage(roleEvent, fmt.Sprintf("%s a %s de débloquer %s pour la tâche %s.", c.conf().User, word, strings.Join(a.Refs, ", "), a.TaskID), false)
	c.ping()
}

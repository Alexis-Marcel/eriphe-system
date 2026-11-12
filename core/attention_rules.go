package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The user's rules: a remark on the workspace (« plus », « moins »,
// « jamais » on a module, a sentence) or said to the brain becomes a rule
// that code applies to the attention queue, every time, before the
// composer. A rule names a kind of object by a key, and moves the weight
// of every item it matches by its delta; an item whose weight falls to 0
// or below is dropped from what the queue gives (it stays in the table:
// removing the rule brings it back). A rule with no delta only carries a
// remark, for the composer to read as context.
//
// The key grammar, small and closed:
//
//	key   = kind | kind ":" attr "=" value
//	kind  = mail | agenda | event | session | card | task | project | alert
//
// A bare kind matches every item of that kind. An attribute matches what
// code reads of the item and its object (objects.go), values compared in
// lower case:
//
//	any kind  source    who raised the item, whole (skill:backup) or its name (backup)
//	mail      from      the sender's address (sam@example.org) or its domain
//	                    (example.org, which also matches mail.example.org)
//	mail      account   the account it came to
//	mail      folder    its folder (INBOX)
//	event     calendar  its calendar
//	event     account   its account
//	card      kind      the card's kind (memory, intention, taskstart, monitor…)
//	session   lead      the assistant that leads it
//	task      agent     the agent working on it
//	project   name      the project (as its ref's id)
//
// The rules that match an item add up: « mail » at -1 and
// « mail:from=example.org » at +2 raise a mail from example.org by one.
// One rule per key: a new one on a key replaces it (a remark alone keeps
// its delta). The user makes rules from the workspace (/api/rules) and
// removes them in the settings; the brain only proposes one (write_rule),
// on a card the user approves.

const attentionRulesSchema = `CREATE TABLE IF NOT EXISTS attention_rules (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	key     TEXT NOT NULL,
	delta   INTEGER NOT NULL,
	label   TEXT NOT NULL DEFAULT '',
	remark  TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT 'on', -- on, or proposed by the brain
	why     TEXT NOT NULL DEFAULT '',   -- the brain's, on a proposal
	tainted TEXT NOT NULL DEFAULT '',   -- a proposal of a turn that read external content
	at      INTEGER NOT NULL
);`

func init() { extraSchema = append(extraSchema, attentionRulesSchema) }

const (
	cardShowRule = "showrule" // a rule the brain proposes (write_rule)

	ruleOn       = "on"
	ruleProposal = "proposed"

	ruleDeltaMax   = 5
	ruleNever      = -5
	ruleLabelMax   = 120
	ruleRemarkMax  = 300
	ruleValueMax   = 200
	attnRulesMax   = 200
	ruleReadWithin = 2 * time.Second  // what the rules may spend reading objects, for one pass
	ruleAttrsKeep  = 30 * time.Minute // an object's attributes, read once
	ruleAttrsRetry = time.Minute      // an object that could not be read, not asked again before
	ruleAttrsMax   = 1000             // objects whose attributes are kept
)

// ruleAttrNames are the attributes a key may name, by kind; source is
// every kind's.
var ruleAttrNames = map[string][]string{
	refMail:    {"from", "account", "folder"},
	refAgenda:  nil,
	refEvent:   {"calendar", "account"},
	refSession: {"lead"},
	refCard:    {"kind"},
	refTask:    {"agent"},
	refProject: {"name"},
	refAlert:   nil,
}

// attentionRule is one rule, as the store keeps it and the API gives it.
type attentionRule struct {
	ID     int64     `json:"id"`
	Key    string    `json:"key"`
	Delta  int       `json:"delta"`
	Label  string    `json:"label"`
	Remark string    `json:"remark,omitempty"`
	At     time.Time `json:"at"`

	state, why, tainted string
}

// ruleKey is a key, parsed.
type ruleKey struct{ Kind, Attr, Value string }

func (k ruleKey) String() string {
	if k.Attr == "" {
		return k.Kind
	}
	return k.Kind + ":" + k.Attr + "=" + k.Value
}

var errRuleKey = errors.New("a key is a kind (mail, agenda, event, session, card, task, project, alert), or kind:attribute=value " +
	"(mail:from=example.org, mail:account=…, mail:folder=…, event:calendar=…, card:kind=…, session:lead=…, task:agent=…, project:name=…, or any kind's source=…)")

// parseRuleKey reads a key, in lower case.
func parseRuleKey(s string) (ruleKey, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	kind, rest, hasAttr := strings.Cut(s, ":")
	attrs, known := ruleAttrNames[kind]
	if !known {
		return ruleKey{}, errRuleKey
	}
	if !hasAttr {
		return ruleKey{Kind: kind}, nil
	}
	attr, value, ok := strings.Cut(rest, "=")
	attr, value = strings.TrimSpace(attr), strings.TrimSpace(value)
	if !ok || value == "" || (attr != "source" && !slices.Contains(attrs, attr)) {
		return ruleKey{}, errRuleKey
	}
	if len(value) > ruleValueMax || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ruleKey{}, fmt.Errorf("a value of %d characters at most, on one line", ruleValueMax)
	}
	return ruleKey{Kind: kind, Attr: attr, Value: value}, nil
}

// ruleAttrs is what a key may match of one item: its kind, and its
// attributes' values, each attribute with every value that matches it.
type ruleAttrs struct {
	Kind  string
	Attrs map[string][]string
}

func (k ruleKey) matches(a ruleAttrs) bool {
	if k.Kind != a.Kind {
		return false
	}
	return k.Attr == "" || slices.Contains(a.Attrs[k.Attr], k.Value)
}

// sourceValues are the values a source matches: whole, and its name.
func sourceValues(source string) []string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return nil
	}
	out := []string{source}
	if _, name, ok := strings.Cut(source, ":"); ok && name != "" {
		out = append(out, name)
	}
	return out
}

// senderValues are the values a sender matches: the address, its domain
// and the domains above it (never a top-level domain alone).
func senderValues(from string) []string {
	addr := strings.TrimSpace(from)
	if a, err := mail.ParseAddress(from); err == nil {
		addr = a.Address
	} else if i, j := strings.LastIndex(addr, "<"), strings.LastIndex(addr, ">"); i >= 0 && j > i {
		addr = addr[i+1 : j]
	}
	addr = strings.ToLower(addr)
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return nil
	}
	out := []string{addr}
	for d := addr[at+1:]; strings.Contains(d, "."); {
		out = append(out, d)
		_, d, _ = strings.Cut(d, ".")
	}
	return out
}

// objectAttrs are the attributes of an object a key may name.
func objectAttrs(o *Object) map[string][]string {
	out := map[string][]string{}
	add := func(attr, v string) {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			out[attr] = append(out[attr], v)
		}
	}
	switch {
	case o.Mail != nil:
		out["from"] = senderValues(o.Mail.From)
		add("account", o.Mail.Account)
		add("folder", o.Mail.Folder)
	case o.Event != nil:
		add("calendar", o.Event.Calendar)
		add("account", o.Event.Account)
	case o.Session != nil:
		add("lead", o.Session.Lead)
	case o.Card != nil:
		add("kind", o.Card.Kind)
	case o.Task != nil:
		add("agent", o.Task.Agent)
	case o.Project != nil:
		add("name", o.Project.Name)
	}
	return out
}

// The rules, stored.

const ruleCols = `id, key, delta, label, remark, state, why, tainted, at`

func scanRule(row interface{ Scan(...any) error }) (attentionRule, error) {
	var r attentionRule
	var at int64
	err := row.Scan(&r.ID, &r.Key, &r.Delta, &r.Label, &r.Remark, &r.state, &r.why, &r.tainted, &at)
	r.At = time.Unix(at, 0)
	return r, err
}

func (c *core) rulesWhere(where string, args ...any) ([]attentionRule, error) {
	rows, err := c.store.db.Query(`SELECT `+ruleCols+` FROM attention_rules WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []attentionRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// attentionRules are the rules in force, the latest first. The composer
// reads them as context (rulesContext); the queue applies them
// (applyRules).
func (c *core) attentionRules() []attentionRule {
	rs, _ := c.rulesWhere(`state = ? ORDER BY at DESC, id DESC`, ruleOn)
	return rs
}

func (c *core) attentionRule(id int64) (attentionRule, error) {
	return scanRule(c.store.db.QueryRow(`SELECT `+ruleCols+` FROM attention_rules WHERE id = ?`, id))
}

// showRuleIn is a rule as it is asked for: by the user's gesture, or the brain.
type showRuleIn struct {
	Key    string `json:"key"`
	Delta  int    `json:"delta"`
	Label  string `json:"label,omitempty"`
	Remark string `json:"remark,omitempty"`
}

// clean checks a rule asked for and gives its canonical form.
func (in showRuleIn) clean() (showRuleIn, error) {
	k, err := parseRuleKey(in.Key)
	if err != nil {
		return in, err
	}
	in.Key = k.String()
	if in.Delta < -ruleDeltaMax || in.Delta > ruleDeltaMax {
		return in, fmt.Errorf("a delta from %d to %d: +1 more, -1 less, %d never", -ruleDeltaMax, ruleDeltaMax, ruleNever)
	}
	in.Label = truncate(oneLine(in.Label), ruleLabelMax)
	in.Remark = truncate(oneLine(in.Remark), ruleRemarkMax)
	if in.Delta == 0 && in.Remark == "" {
		return in, errors.New("a rule moves the weight (delta) or carries a remark")
	}
	if in.Label == "" {
		in.Label = in.Key
	}
	return in, nil
}

// addRule puts a rule the user made in force: it replaces the rule on the
// same key; a remark alone keeps that rule's delta.
func (c *core) addRule(in showRuleIn) (attentionRule, error) {
	in, err := in.clean()
	if err != nil {
		return attentionRule{}, err
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return attentionRule{}, err
	}
	defer tx.Rollback()
	var oldDelta int
	err = tx.QueryRow(`SELECT delta FROM attention_rules WHERE key = ? AND state = ?`, in.Key, ruleOn).Scan(&oldDelta)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		var n int
		tx.QueryRow(`SELECT COUNT(*) FROM attention_rules WHERE state = ?`, ruleOn).Scan(&n)
		if n >= attnRulesMax {
			return attentionRule{}, fmt.Errorf("%d rules at most: remove one first", attnRulesMax)
		}
	case err != nil:
		return attentionRule{}, err
	case in.Delta == 0:
		in.Delta = oldDelta
	}
	if _, err := tx.Exec(`DELETE FROM attention_rules WHERE key = ? AND state = ?`, in.Key, ruleOn); err != nil {
		return attentionRule{}, err
	}
	res, err := tx.Exec(`INSERT INTO attention_rules (key, delta, label, remark, state, at) VALUES (?, ?, ?, ?, ?, ?)`,
		in.Key, in.Delta, in.Label, in.Remark, ruleOn, time.Now().Unix())
	if err != nil {
		return attentionRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return attentionRule{}, err
	}
	id, _ := res.LastInsertId()
	return c.attentionRule(id)
}

var errNoRule = errors.New("no such rule")

// removeRule takes a rule in force out.
func (c *core) removeRule(id int64) error {
	res, err := c.store.db.Exec(`DELETE FROM attention_rules WHERE id = ? AND state = ?`, id, ruleOn)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoRule
	}
	return nil
}

// Applying them.

// ruleAttrCache keeps the attributes of the objects the rules read (a
// mail's sender does not change): the queue is read every second or two
// by an open workspace, and a mail is read from its server.
type ruleAttrCache struct {
	mu sync.Mutex
	m  map[string]ruleAttrEntry
}

type ruleAttrEntry struct {
	attrs map[string][]string // nil: it could not be read
	at    time.Time
}

func (k *ruleAttrCache) get(ref string, now time.Time) (map[string][]string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.m[ref]
	if !ok {
		return nil, false
	}
	keep := ruleAttrsKeep
	if e.attrs == nil {
		keep = ruleAttrsRetry
	}
	if now.Sub(e.at) > keep {
		delete(k.m, ref)
		return nil, false
	}
	return e.attrs, true
}

func (k *ruleAttrCache) put(ref string, attrs map[string][]string, now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil || len(k.m) >= ruleAttrsMax {
		k.m = map[string]ruleAttrEntry{}
	}
	k.m[ref] = ruleAttrEntry{attrs, now}
}

// applyRules moves each item's weight by the rules that match it, drops
// the items that fall to 0 or below (a card's stays, at 1: it waits for
// the user's answer), and orders the rest again by weight, in the order
// they came in otherwise. An object is read only when a rule needs
// one of its attributes, at most ruleReadWithin for the whole pass: one
// that cannot be read in time matches by its kind and source only.
func (c *core) applyRules(items []attentionItem) []attentionItem {
	rules := c.attentionRules()
	if len(rules) == 0 || len(items) == 0 {
		return items
	}
	keys := make([]ruleKey, len(rules))
	needs := map[string]bool{} // kinds with a rule on an object's attribute
	for i, r := range rules {
		keys[i], _ = parseRuleKey(r.Key)
		if keys[i].Attr != "" && keys[i].Attr != "source" {
			needs[keys[i].Kind] = true
		}
	}
	return weighByRules(items, c.itemAttrs(items, needs), rules)
}

// weighByRules is applyRules once the items' attributes are read, in
// code alone: attrs[i] is what the rules may match of items[i].
func weighByRules(items []attentionItem, attrs []ruleAttrs, rules []attentionRule) []attentionItem {
	keys := make([]ruleKey, len(rules))
	for i, r := range rules {
		keys[i], _ = parseRuleKey(r.Key)
	}
	out := make([]attentionItem, 0, len(items))
	for i, it := range items {
		delta := 0
		for j, k := range keys {
			if rules[j].Delta != 0 && k.matches(attrs[i]) {
				delta += rules[j].Delta
			}
		}
		if delta == 0 {
			out = append(out, it)
			continue
		}
		it.Weight += delta
		if it.Weight <= 0 && strings.HasPrefix(it.Ref, refCard+":") {
			it.Weight = 1 // a card waits for the user's answer: lowered, never hidden
		}
		if it.Weight <= 0 {
			continue
		}
		it.Weight = min(it.Weight, 5)
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(a, b attentionItem) int { return b.Weight - a.Weight })
	return out
}

// itemAttrs reads what the rules may match of each item: its kind and
// source always, its object's attributes when a rule of its kind needs
// them, a few objects at once.
func (c *core) itemAttrs(items []attentionItem, needs map[string]bool) []ruleAttrs {
	out := make([]ruleAttrs, len(items))
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), ruleReadWithin)
	defer cancel()
	sem := make(chan struct{}, objectsParallel)
	var wg sync.WaitGroup
	for i, it := range items {
		kind, id, _ := strings.Cut(it.Ref, ":")
		out[i] = ruleAttrs{Kind: kind, Attrs: map[string][]string{"source": sourceValues(it.Source)}}
		if kind == refProject { // its name is its ref's: nothing to read
			out[i].Attrs["name"] = []string{strings.ToLower(id)}
			continue
		}
		if !needs[kind] {
			continue
		}
		if got, ok := c.ruleCache.get(it.Ref, now); ok {
			for k, v := range got {
				out[i].Attrs[k] = v
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			o, err := c.object(ctx, it.Ref)
			if err != nil {
				if ctx.Err() == nil { // not only out of time: not asked again for a while
					c.ruleCache.put(it.Ref, nil, now)
				}
				return
			}
			got := objectAttrs(o)
			c.ruleCache.put(it.Ref, got, now)
			for k, v := range got {
				out[i].Attrs[k] = v
			}
		}()
	}
	wg.Wait()
	return out
}

// rulesContext is the user's rules as the composer reads them: context to
// compose by, not a job (code applied their deltas already), one sentence
// each in French, the latest first (composerRules, composition.go).
func (c *core) rulesContext() []string {
	var out []string
	for _, r := range c.attentionRules() {
		out = append(out, ruleSentence(r))
	}
	return out
}

// ruleSentence is a rule in a sentence, in French, for the composer.
func ruleSentence(r attentionRule) string {
	s := fmt.Sprintf("%s (%s) : %s", r.Key, r.Label, deltaWords(r.Delta))
	if r.Remark != "" {
		s += " ; sa remarque : " + r.Remark
	}
	if r.Delta != 0 {
		s += ", déjà appliqué aux poids de la file"
	}
	return oneLine(s)
}

// lastRule is the rule put in force last, as a stamp that changes with
// each new one, and in a sentence: the composer composes again for it, as
// for a remark of the user's ("", "" with no rule).
func (c *core) lastRule() (stamp, words string) {
	rs, _ := c.rulesWhere(`state = ? ORDER BY at DESC, id DESC LIMIT 1`, ruleOn)
	if len(rs) == 0 {
		return "", ""
	}
	return fmt.Sprintf("%d@%d", rs[0].ID, rs[0].At.Unix()), ruleSentence(rs[0])
}

// deltaWords says a delta for the composer, in French.
func deltaWords(d int) string {
	switch {
	case d <= ruleNever:
		return "ne plus montrer"
	case d > 0:
		return "plus en avant (+" + strconv.Itoa(d) + ")"
	case d < 0:
		return "moins en avant (" + strconv.Itoa(d) + ")"
	}
	return "poids inchangé"
}

// The brain's proposals (write_rule), on a card.

// proposeRule keeps a rule the brain proposes, for the user's card: one
// proposal per key, the latest.
func (c *core) proposeRule(in showRuleIn, why string, tainted []string) (map[string]any, error) {
	in, err := in.clean()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var id int64
	err = c.store.db.QueryRow(`SELECT id FROM attention_rules WHERE key = ? AND state = ?`, in.Key, ruleProposal).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := c.store.db.Exec(`INSERT INTO attention_rules (key, delta, label, remark, state, why, tainted, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Key, in.Delta, in.Label, in.Remark, ruleProposal, truncate(oneLine(why), ruleRemarkMax), strings.Join(tainted, ", "), now)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	case err != nil:
		return nil, err
	default:
		c.store.db.Exec(`UPDATE attention_rules SET delta = ?, label = ?, remark = ?, why = ?, tainted = ?, at = ? WHERE id = ?`,
			in.Delta, in.Label, in.Remark, truncate(oneLine(why), ruleRemarkMax), strings.Join(tainted, ", "), now, id)
	}
	c.cardProposed(cardShowRule, id)
	return map[string]any{"result": "proposed: it waits for the user's approval on its card, then the workspace applies it", "key": in.Key}, nil
}

// showRuleProposal is the card of a rule the brain proposed.
func (c *core) showRuleProposal(p proposal) proposal {
	r, err := c.attentionRule(p.ID)
	if err != nil {
		return p
	}
	if r.state != ruleProposal {
		p.Outcome = cardApproved
		return p
	}
	p.Pending, p.Why, p.Tainted = true, r.why, r.tainted
	p.text = func(lang string) string { return ruleLine(r, lang) }
	p.fields = func(lang string) []cardField {
		f := []cardField{{Name: tr(lang, "showrule.key"), Value: r.Key}}
		if r.Remark != "" {
			f = append(f, cardField{Name: tr(lang, "showrule.remark"), Value: r.Remark})
		}
		return f
	}
	p.Fields = p.fields("fr")
	return p
}

// ruleLine is a rule in a line: what it does, to what.
func ruleLine(r attentionRule, lang string) string {
	what := "showrule.remarkonly"
	switch {
	case r.Delta <= ruleNever:
		what = "showrule.never"
	case r.Delta > 0:
		what = "showrule.more"
	case r.Delta < 0:
		what = "showrule.less"
	}
	return fmt.Sprintf(tr(lang, what), r.Label)
}

// proposedRules are the rules whose card waits.
func (c *core) proposedRules() []int64 {
	rs, _ := c.rulesWhere(`state = ? ORDER BY id`, ruleProposal)
	var out []int64
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// decideRule answers a proposed rule's card: yes puts it in force, in
// place of the rule on its key; no drops it.
func (c *core) decideRule(id int64, approve bool) error {
	r, err := c.attentionRule(id)
	if err != nil || r.state != ruleProposal {
		return errCardDecided
	}
	if !approve {
		_, err := c.store.db.Exec(`DELETE FROM attention_rules WHERE id = ?`, id)
		return err
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.Delta == 0 {
		tx.QueryRow(`SELECT delta FROM attention_rules WHERE key = ? AND state = ?`, r.Key, ruleOn).Scan(&r.Delta)
	}
	if _, err := tx.Exec(`DELETE FROM attention_rules WHERE key = ? AND state = ?`, r.Key, ruleOn); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE attention_rules SET state = ?, delta = ?, why = '', tainted = '', at = ? WHERE id = ?`,
		ruleOn, r.Delta, time.Now().Unix(), id); err != nil {
		return err
	}
	return tx.Commit()
}

// writeRuleIn is write_rule's input.
type writeRuleIn struct {
	Key    string `json:"key" jsonschema:"what it applies to: a kind (mail, agenda, event, session, card, task, project, alert), or kind:attribute=value: mail:from=<address or domain>, mail:account=…, mail:folder=…, event:calendar=…, event:account=…, card:kind=<card kind>, session:lead=<assistant>, task:agent=<agent>, project:name=<name>, or any kind's source=<who raised it: skill:backup or backup>"`
	Delta  int    `json:"delta" jsonschema:"how much it moves the weight, -5 to 5: +1 more, -1 less, -5 never; 0 only with a remark"`
	Label  string `json:"label" jsonschema:"what it applies to, in French, a few words the user reads: les mails de example.org"`
	Remark string `json:"remark,omitempty" jsonschema:"the user's remark, in their words, for the composer: seulement le lundi"`
	Why    string `json:"why,omitempty" jsonschema:"why, in a short sentence in French"`
}

func registerRuleTool(s *mcp.Server, c *core) {
	tool(s, "write_rule", "Propose a rule of the workspace: what the user wants shown more, less or never (« montre-moi plus les mails de example.org », « ne me montre plus les alertes de sauvegarde »). eriphe-core applies it to the attention queue every time: it moves the weight of every item its key matches by delta, and an item at 0 or below is not shown. A remark (delta 0) is only context for the composer. One rule per key: a new one replaces it. It is never in force before the user approves it on its card; the user also makes rules from the workspace's ⋯ menu and removes them in the settings. Not for notifications: those are rule's.",
		func(ctx context.Context, in writeRuleIn) (map[string]any, error) {
			return c.proposeRule(showRuleIn{Key: in.Key, Delta: in.Delta, Label: in.Label, Remark: in.Remark}, in.Why, c.turnTaint())
		})
}

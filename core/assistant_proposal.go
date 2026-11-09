package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Creation by Eriphe (#244 step 6): the brain proposes an assistant, and
// the user makes it on its creation card. A rule comes first: the brain
// proposes an assistant only with one of its reasons (whyBoundary, …) and
// a sentence of why a rule would not do. The card shows it as the six
// blocks: the assistant and its mission, the facts it writes and those it
// is lent, the events and the rules that wake it, the channels it speaks
// through, its tools; then why, where to see it, and what it should cost.
// On the yes, eriphe-core makes the assistant and its rules (then
// assistant:<name>) at once; on the no, nothing.

const (
	cardNewAssistant = "newassistant"

	proposalWaiting  = "proposed"
	proposalMade     = "made"
	proposalRefused  = "refused"
	maxAssistantWake = 3
)

const assistantProposalsSchema = `CREATE TABLE IF NOT EXISTS assistant_proposals (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, template TEXT NOT NULL DEFAULT '',
  spec TEXT NOT NULL, wake TEXT NOT NULL DEFAULT '[]', why TEXT NOT NULL, justification TEXT NOT NULL,
  source_ref TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
  created INTEGER NOT NULL, decided INTEGER);`

func init() { extraSchema = append(extraSchema, assistantProposalsSchema) }

type proposeAssistantIn struct {
	Template      string         `json:"template,omitempty" jsonschema:"a template's name (templates): its facts, views, mission and wake, unless given here"`
	Name          string         `json:"name" jsonschema:"its id: lowercase letters, digits and dashes (tri-mails)"`
	DisplayName   string         `json:"display_name,omitempty" jsonschema:"what the user calls it, in French (Tri des mails)"`
	Mission       string         `json:"mission,omitempty" jsonschema:"its standing mission, in French, from the user's words; the template's when empty"`
	Why           string         `json:"why" jsonschema:"in French, one sentence: why an assistant and not a rule or a tool you call"`
	Justification []string       `json:"justification" jsonschema:"its reasons, at least one: boundary, tools, wake, parallel (templates explains them)"`
	Tools         []string       `json:"tools,omitempty" jsonschema:"what it is lent of the catalog: server names or server.tool (list_tools), read-only unless the user asked"`
	Facts         []string       `json:"facts,omitempty" jsonschema:"the facts it is lent besides its own: context, or another assistant's name"`
	Powers        []string       `json:"powers,omitempty" jsonschema:"reports (it reports to you; by default) and notifies (it tells the user itself, only if the user asked)"`
	State         []stateFieldIn `json:"state,omitempty" jsonschema:"the facts it writes, typed; the template's when empty"`
	Views         []StateView    `json:"views,omitempty" jsonschema:"its views over them; the template's when empty"`
	Wake          []ruleEventIn  `json:"wake,omitempty" jsonschema:"the rules that wake it (1 to 3), as rule's event without then: horaire, fait, fin, outil; the template's when empty"`
	SourceRef     string         `json:"source_ref,omitempty" jsonschema:"the user's message that asks for it, msg:N"`
}

// stateFieldIn is a field of the facts it writes, as the tool takes it
// (a StateField, whose columns cannot hold columns).
type stateFieldIn struct {
	Name    string          `json:"name" jsonschema:"lowercase, digits and _"`
	Type    string          `json:"type" jsonschema:"choice, number, date, text or rows"`
	Values  []string        `json:"values,omitempty" jsonschema:"a choice's list"`
	TTL     string          `json:"ttl,omitempty" jsonschema:"its validity: 15m, 4h, 2d; 24h by default"`
	Summary bool            `json:"summary,omitempty" jsonschema:"in your [assistants] line: a closed field, or rows by their count"`
	Columns []stateColumnIn `json:"columns,omitempty" jsonschema:"rows: their columns"`
	Key     string          `json:"key,omitempty" jsonschema:"rows: the column that keys them"`
	MaxRows int             `json:"max_rows,omitempty" jsonschema:"rows: at most, 50 by default, up to 200"`
}

type stateColumnIn struct {
	Name   string   `json:"name"`
	Type   string   `json:"type" jsonschema:"choice, number, date or text"`
	Values []string `json:"values,omitempty" jsonschema:"a choice's list"`
}

func (f stateFieldIn) field() StateField {
	out := StateField{Name: f.Name, Type: f.Type, Values: f.Values, TTL: f.TTL, Summary: f.Summary, Key: f.Key, MaxRows: f.MaxRows}
	for _, c := range f.Columns {
		out.Columns = append(out.Columns, StateField{Name: c.Name, Type: c.Type, Values: c.Values})
	}
	return out
}

func stateFieldsIn(fs []StateField) []stateFieldIn {
	var out []stateFieldIn
	for _, f := range fs {
		in := stateFieldIn{Name: f.Name, Type: f.Type, Values: f.Values, TTL: f.TTL, Summary: f.Summary, Key: f.Key, MaxRows: f.MaxRows}
		for _, c := range f.Columns {
			in.Columns = append(in.Columns, stateColumnIn{Name: c.Name, Type: c.Type, Values: c.Values})
		}
		out = append(out, in)
	}
	return out
}

type assistantProposal struct {
	ID            int64
	Name          string
	Template      string
	Spec          Assistant
	Wake          []ruleEventIn
	Why           string
	Justification []string
	SourceRef     string
	Tainted       string
	State         string
	Created       time.Time
}

func registerProposalTools(s *mcp.Server, c *core) {
	tool(s, "propose_assistant", "Propose a new assistant to the user, on its creation card: it exists only once they say yes there. A rule comes first: propose an assistant only when one of its reasons holds "+
		"(justification: boundary, tools, wake, parallel; see templates), with why a rule would not do. Start from a template when one fits. "+
		"It writes facts (its state), is woken by rules, reports to you; it never talks to the user nor launches a task.",
		func(ctx context.Context, in proposeAssistantIn) (map[string]any, error) {
			return c.proposeAssistant(in)
		})
}

// proposeAssistant checks a proposal as it would be made, and puts it on a
// card.
func (c *core) proposeAssistant(in proposeAssistantIn) (map[string]any, error) {
	if in.Template != "" {
		t, ok := templateNamed(strings.TrimSpace(in.Template))
		if !ok {
			return nil, fmt.Errorf("no template %q: templates lists them", in.Template)
		}
		if t.Kind != templateAssistant {
			return nil, fmt.Errorf("%s is a rule's template: write it with rule (kind event), with its event filled", t.Name)
		}
		in = fillFromTemplate(in, t)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("name is needed: lowercase letters, digits and dashes")
	}
	if strings.TrimSpace(in.Why) == "" || len(in.Justification) == 0 {
		return nil, errors.New("not proposed: an assistant needs its reason (justification: boundary, tools, wake or parallel) and why, in a sentence, a rule would not do. Otherwise write a rule")
	}
	for _, j := range in.Justification {
		if !slices.Contains(assistantJustifications, j) {
			return nil, fmt.Errorf("justification %q: one of %s", j, strings.Join(assistantJustifications, ", "))
		}
	}
	m := c.conf()
	if _, ok := m.Assistants[name]; ok {
		return nil, fmt.Errorf("an assistant %q exists already", name)
	}
	if p := c.waitingProposalNamed(name); p != nil {
		return nil, fmt.Errorf("an assistant %q is already proposed (its card waits)", name)
	}
	if strings.TrimSpace(in.Mission) == "" {
		return nil, errors.New("mission is needed: what it does, in French, from the user's words")
	}
	spec := Assistant{Name: name, Role: assistantRoleAssistant, DisplayName: normalizeDisplayName(in.DisplayName),
		Instructions: strings.TrimSpace(in.Mission), Tools: in.Tools, Facts: in.Facts, Powers: in.Powers, Views: in.Views}
	for _, f := range in.State {
		spec.State = append(spec.State, f.field())
	}
	if len(spec.Powers) == 0 {
		spec.Powers = []string{powerReports}
	}
	if err := checkAssistant(m, name, spec); err != nil {
		return nil, err
	}
	for _, o := range spec.Facts {
		if _, ok := m.Assistants[o]; !ok && o != ownerContext {
			return nil, fmt.Errorf("facts: no assistant %q: context, or one of your assistants", o)
		}
	}
	if len(in.Wake) == 0 || len(in.Wake) > maxAssistantWake {
		return nil, fmt.Errorf("wake: 1 to %d rules that wake it", maxAssistantWake)
	}
	for i, ev := range in.Wake {
		if _, err := c.wakeRule(ev, ""); err != nil {
			return nil, fmt.Errorf("wake %d: %w", i+1, err)
		}
	}
	if scan := scanMemoryText(spec.Instructions, in.Why, spec.DisplayName); scan.Reject != "" {
		return nil, fmt.Errorf("refused: %s", scan.Reject)
	}
	specJSON, _ := json.Marshal(spec)
	wake, _ := json.Marshal(in.Wake)
	just, _ := json.Marshal(in.Justification)
	tainted := strings.Join(c.turnTaint(), ", ")
	res, err := c.store.db.Exec(`INSERT INTO assistant_proposals (name, template, spec, wake, why, justification, source_ref, tainted, state, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, name, in.Template, string(specJSON), string(wake), strings.TrimSpace(in.Why), string(just),
		strings.TrimSpace(in.SourceRef), tainted, proposalWaiting, c.notifyTime().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	log.Printf("assistants: %s proposed (proposal %d, %s)", name, id, strings.Join(in.Justification, ", "))
	c.cardProposed(cardNewAssistant, id)
	return map[string]any{"proposal": id, "note": "Only proposed, on its creation card: it exists once " + c.conf().User +
		" says yes there. " + cardNote(c.conf().User) + " Do not say it is set up."}, nil
}

func fillFromTemplate(in proposeAssistantIn, t assistantTemplate) proposeAssistantIn {
	if in.Name == "" {
		in.Name = t.Name
	}
	if in.DisplayName == "" {
		in.DisplayName = t.DisplayName
	}
	if in.Mission == "" {
		in.Mission = t.Mission
	}
	if len(in.Justification) == 0 {
		in.Justification = t.Why
	}
	if len(in.Powers) == 0 {
		in.Powers = t.Powers
	}
	if len(in.State) == 0 {
		in.State, in.Views = stateFieldsIn(t.State), t.Views
	}
	if len(in.Views) == 0 {
		in.Views = t.Views
	}
	if len(in.Wake) == 0 {
		in.Wake = t.Wake
	}
	return in
}

// wakeRule is the rule a proposal's wake makes: it wakes the assistant
// named (or, while it does not exist yet, is checked without it).
func (c *core) wakeRule(ev ruleEventIn, assistant string) (*rule, error) {
	if ev.Then != "" || ev.Action != nil || ev.Urgency == urgencyUrgent || ev.Text != "" {
		return nil, errors.New("a wake rule only wakes its assistant: no then, grant, urgency nor text")
	}
	after, err := parseAfter(ev.After)
	if err != nil {
		return nil, err
	}
	w := ruleFromEvent(ev, after)
	if a, ok := watchSourceAliases[w.Source]; ok {
		w.Source = a
	}
	if w.Source == sourceAlert {
		return nil, errors.New("an alerte rule's address is made by the user: wake it on a schedule, a fact, a task's end or a tool")
	}
	if strings.TrimSpace(w.Prompt) == "" {
		return nil, errors.New("prompt is needed: what to do on this wake, in French")
	}
	if assistant != "" {
		w.Act = actAssistantPrefix + assistant
	}
	if err := c.checkRule(w); err != nil {
		return nil, err
	}
	return w, nil
}

const proposalCols = `id, name, template, spec, wake, why, justification, source_ref, tainted, state, created`

func scanProposal(row interface{ Scan(...any) error }) (*assistantProposal, error) {
	var p assistantProposal
	var spec, wake, just string
	var created int64
	if err := row.Scan(&p.ID, &p.Name, &p.Template, &spec, &wake, &p.Why, &just, &p.SourceRef, &p.Tainted, &p.State, &created); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(spec), &p.Spec)
	p.Spec.Name = p.Name
	json.Unmarshal([]byte(wake), &p.Wake)
	json.Unmarshal([]byte(just), &p.Justification)
	p.Created = time.Unix(created, 0)
	return &p, nil
}

func (c *core) assistantProposal(id int64) (*assistantProposal, error) {
	p, err := scanProposal(c.store.db.QueryRow(`SELECT `+proposalCols+` FROM assistant_proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no such proposal")
	}
	return p, err
}

func (c *core) waitingAssistantProposals() []*assistantProposal {
	rows, err := c.store.db.Query(`SELECT `+proposalCols+` FROM assistant_proposals WHERE state = ? ORDER BY id`, proposalWaiting)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*assistantProposal
	for rows.Next() {
		if p, err := scanProposal(rows); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func (c *core) waitingProposalNamed(name string) *assistantProposal {
	for _, p := range c.waitingAssistantProposals() {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// decideNewAssistant is the user's answer on its creation card: yes makes
// the assistant and its wake rules, all or nothing; no makes nothing.
func (c *core) decideNewAssistant(id int64, approve bool) error {
	p, err := c.assistantProposal(id)
	if err != nil {
		return err
	}
	if p.State != proposalWaiting {
		return errors.New("not proposed")
	}
	if !approve {
		_, err := c.store.db.Exec(`UPDATE assistant_proposals SET state = ?, decided = ? WHERE id = ?`, proposalRefused, c.notifyTime().Unix(), id)
		return err
	}
	if _, ok := c.conf().Assistants[p.Name]; ok {
		return fmt.Errorf("an assistant %q exists already", p.Name)
	}
	if err := c.putAssistant(p.Name, p.Spec); err != nil {
		return err
	}
	var made []int64
	undo := func() {
		for _, id := range made {
			c.deleteRule(id)
		}
		c.deleteAssistant(p.Name)
	}
	for _, ev := range p.Wake {
		w, err := c.wakeRule(ev, p.Name)
		if err == nil {
			w.State, w.By, w.SourceRef, w.Why = watchActive, "owner", p.SourceRef, p.Why
			w, err = c.addRule(w)
		}
		if err != nil {
			undo()
			return fmt.Errorf("its wake %q: %w", ev.Name, err)
		}
		made = append(made, w.ID)
	}
	if _, err := c.store.db.Exec(`UPDATE assistant_proposals SET state = ?, decided = ? WHERE id = ?`, proposalMade, c.notifyTime().Unix(), id); err != nil {
		undo()
		return err
	}
	log.Printf("assistants: %s made on its card (proposal %d), with %d rule(s)", p.Name, id, len(made))
	return nil
}

// The card.

func (p *assistantProposal) displayName() string {
	if p.Spec.DisplayName != "" {
		return p.Spec.DisplayName
	}
	return p.Name
}

func (c *core) newAssistantText(p *assistantProposal, lang string) string {
	return fmt.Sprintf(tr(lang, "newassistant.card"), p.displayName(), p.Why)
}

// newAssistantFields are its six blocks, then why, where, and its cost.
func (c *core) newAssistantFields(p *assistantProposal, lang string) []cardField {
	none := tr(lang, "newassistant.none")
	join := func(xs []string) string {
		if len(xs) == 0 {
			return none
		}
		return strings.Join(xs, ", ")
	}
	var writes []string
	for _, f := range p.Spec.State {
		writes = append(writes, fieldShape(f, lang))
	}
	facts := fmt.Sprintf(tr(lang, "newassistant.facts"), join(writes), join(p.Spec.Facts))
	var events, rules []string
	var wakes []*rule
	for _, ev := range p.Wake {
		w, err := c.wakeRule(ev, "")
		if err != nil {
			continue
		}
		wakes = append(wakes, w)
		events = append(events, tr(lang, "rule.when."+w.Source)+" ("+w.When()+")")
		rules = append(rules, ruleSentence(w, lang, p.displayName()))
	}
	var channels []string
	for _, pw := range p.Spec.powers() {
		if l := tr(lang, "assistants.power."+pw); l != "assistants.power."+pw {
			channels = append(channels, l)
		}
	}
	var why []string
	for _, j := range p.Justification {
		why = append(why, tr(lang, "newassistant.why."+j))
	}
	fields := []cardField{
		{Name: tr(lang, "newassistant.f.assistant"), Value: p.displayName() + " : " + truncate(p.Spec.Instructions, cardValueMax)},
		{Name: tr(lang, "newassistant.f.facts"), Value: facts},
		{Name: tr(lang, "newassistant.f.events"), Value: join(events)},
		{Name: tr(lang, "newassistant.f.rules"), Value: strings.Join(rules, " ")},
		{Name: tr(lang, "newassistant.f.channels"), Value: join(channels)},
		{Name: tr(lang, "newassistant.f.tools"), Value: join(p.Spec.Tools)},
		{Name: tr(lang, "newassistant.f.why"), Value: join(why) + " : " + p.Why},
		{Name: tr(lang, "newassistant.f.links"), Value: tr(lang, "newassistant.links")},
		{Name: tr(lang, "newassistant.f.cost"), Value: c.costEstimate(p.Spec, wakes, lang)},
	}
	if p.Tainted != "" {
		fields = append(fields, cardField{Name: tr(lang, "newassistant.f.tainted"), Value: p.Tainted})
	}
	return fields
}

// fieldShape is a field of its state as the card says it: its name, type
// and, for rows, their columns.
func fieldShape(f StateField, lang string) string {
	switch f.Type {
	case stateRows:
		var cols []string
		for _, c := range f.Columns {
			cols = append(cols, c.Name)
		}
		return fmt.Sprintf(tr(lang, "newassistant.rows"), f.Name, strings.Join(cols, ", "))
	case stateChoice:
		return f.Name + " (" + strings.Join(f.Values, " / ") + ")"
	}
	return f.Name + " (" + tr(lang, "newassistant.type."+f.Type) + ")"
}

// ruleSentence is a rule as Settings → Règles says it: « Quand …, si …,
// alors … ». who names the assistant it wakes, when it does not exist yet.
func ruleSentence(w *rule, lang, who string) string {
	s := fmt.Sprintf(tr(lang, "rule.s.when"), tr(lang, "rule.when."+w.Source), w.When())
	if w.If != "" {
		s += fmt.Sprintf(tr(lang, "rule.s.if"), w.If)
	}
	switch {
	case w.Act == actNotify:
		s += fmt.Sprintf(tr(lang, "rule.s.then"), fmt.Sprintf(tr(lang, "rule.then.notify"), w.ActText))
	case who != "" || w.ThenAssistant() != "":
		if who == "" {
			who = w.ThenAssistant()
		}
		s += fmt.Sprintf(tr(lang, "rule.s.then"), fmt.Sprintf(tr(lang, "rule.then.assistant"), who))
	default:
		s += fmt.Sprintf(tr(lang, "rule.s.then"), tr(lang, "rule.then.brain"))
	}
	return s
}

// The estimated cost: its wakes in the next 30 days (those on a schedule;
// the others depend on what happens), times what a turn of its model cost
// over the last statsDays, where its harness said.

const costWindow = 30 * 24 * time.Hour

func (c *core) costEstimate(a Assistant, wakes []*rule, lang string) string {
	now := c.notifyTime()
	n, counted := 0, true
	for _, w := range wakes {
		if w.Source != sourceSchedule {
			counted = false
			continue
		}
		r, err := parseSchedule(w.Spec.Schedule)
		if err != nil {
			continue
		}
		for t := r.next(now, now.Location()); !t.IsZero() && t.Before(now.Add(costWindow)) && n < 10000; t = r.next(t, now.Location()) {
			n++
		}
	}
	agent := a.Agent
	m := c.conf()
	if agent == "" {
		agent = firstAgentFor(m)
	}
	perTurn, ok := c.store.turnCost(m.Agents[agent].Model, now.Add(-statsDays*24*time.Hour))
	var wakesText string
	switch {
	case counted:
		wakesText = fmt.Sprintf(tr(lang, "newassistant.cost.wakes"), n)
	case n > 0:
		wakesText = fmt.Sprintf(tr(lang, "newassistant.cost.wakesmore"), n)
	default:
		wakesText = tr(lang, "newassistant.cost.events")
	}
	if !ok {
		return wakesText + " " + tr(lang, "newassistant.cost.unknown")
	}
	s := wakesText + " " + fmt.Sprintf(tr(lang, "newassistant.cost.turn"), perTurn)
	if n > 0 {
		s += " " + fmt.Sprintf(tr(lang, "newassistant.cost.month"), perTurn*float64(n))
	}
	return s
}

// turnCost is what a turn of a model cost on average since a time, where
// its harness said.
func (s *Store) turnCost(model string, since time.Time) (float64, bool) {
	if model == "" {
		return 0, false
	}
	var avg sql.NullFloat64
	s.db.QueryRow(`SELECT AVG(json_extract(e.data, '$.cost_usd')) FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE e.type = 'turn_end' AND t.model = ? AND e.time >= ? AND json_extract(e.data, '$.cost_usd') > 0`, model, since.Unix()).Scan(&avg)
	return avg.Float64, avg.Valid
}

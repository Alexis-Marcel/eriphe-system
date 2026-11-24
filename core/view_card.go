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
	"unicode"
	"unicode/utf8"

	"github.com/Alexis-Marcel/eriphe-system/core/nodefile"
	"github.com/Alexis-Marcel/eriphe-system/core/nodestore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A view (#631): what the user wants followed continuously (« surveille
// mes mails », « montre-moi les PR à relire ») is a standing node, the
// monitor that detects what comes, and the skill that works in the node
// when it does. propose_view checks all three at once and shows them on
// one card (kind view, cardViewKind): the node's title and blocks, what the monitor
// does, when the skill runs and what it may borrow. One yes creates the
// three, or none: a step that fails takes back the ones before. Nothing
// here knows a view: the brain composes each one from the catalog.

const (
	cardViewKind = "view"

	viewProposed = "proposed"
	viewApproved = "approved"
	viewRejected = "rejected"
	viewDropped  = "dropped" // withdrawn undecided: its subject was archived (withdrawCards)
)

const viewProposalsSchema = `CREATE TABLE IF NOT EXISTS view_proposals (
	id      INTEGER PRIMARY KEY,
	node    TEXT NOT NULL,              -- the id the node will have, given beforehand: the skill names it
	title   TEXT NOT NULL,
	goal    TEXT NOT NULL DEFAULT '',
	body    TEXT NOT NULL DEFAULT '',   -- the node's text and blocks, as the brain wrote them
	monitor TEXT NOT NULL DEFAULT '',   -- its name, '' for none
	spec    TEXT NOT NULL DEFAULT '',   -- its spec, JSON
	skill   TEXT NOT NULL DEFAULT '',   -- its name, '' for none
	md      TEXT NOT NULL DEFAULT '',   -- its SKILL.md, the node named
	tainted TEXT NOT NULL DEFAULT '',
	state   TEXT NOT NULL DEFAULT 'proposed',
	created INTEGER NOT NULL
);`

func init() { extraSchema = append(extraSchema, viewProposalsSchema) }

type proposeViewIn struct {
	Title   string          `json:"title" jsonschema:"a short name of what it shows (Ma journée, Mes mails), never the user's sentence nor its subject's title"`
	Goal    string          `json:"goal,omitempty" jsonschema:"what it is for and the user's choices: the skill's runs read it"`
	Body    string          `json:"body" jsonschema:"its text and blocks (catalog_read)"`
	Monitor *writeMonitorIn `json:"monitor,omitempty" jsonschema:"write_monitor's fields; a block reads it by its name"`
	Skill   *writeSkillIn   `json:"skill,omitempty" jsonschema:"write_skill's fields, without node; its when keys on the monitor's name"`
	Node    string          `json:"node,omitempty" jsonschema:"to change a view proposed before: the id of its draft, as propose_view gave it (node), never the subject's"`
}

func registerViewTool(s *mcp.Server, c *core) {
	tool(s, "propose_view", "Propose a view: a standing node whose blocks code keeps fresh, the monitor that detects what comes and the skill that works in the node when it does. Its node opens at once as a draft linked to the turn's subject, showing the real page; its card waits on it. Yes makes it standing with its monitor and its skill, and the subject done; no archives the draft. Asked for a page kept up to date, propose it at once with sensible defaults, no questions first. To change it, call again with node. Reuse a monitor or a skill that does it already, by its name.",
		func(ctx context.Context, in proposeViewIn) (map[string]any, error) {
			return c.proposeView(in, c.turnTaint())
		})
}

// viewProposal is a view as it waits for its card.
type viewProposal struct {
	ID                int64
	Node, Title, Goal string
	Body              string
	Monitor           string
	Spec              *monitorSpec
	Skill, MD         string
	Tainted, State    string
	Created           time.Time
	manifest          *skillManifest
	parsed            *nodefile.Node
}

// proposeView checks a view, opens its node as a draft and makes its
// card, which waits on the draft (the kind's node, proposal_kinds.go).
// Called again with the draft's id, it rewrites the same node and the
// same proposal: its card says the new one.
func (c *core) proposeView(in proposeViewIn, tainted []string) (map[string]any, error) {
	if c.nodes == nil {
		return nil, errors.New("no node store")
	}
	var was viewProposal
	if id := strings.TrimSpace(in.Node); id != "" {
		var err error
		if was, err = c.viewDraft(id); err != nil {
			return nil, fmt.Errorf("%s is not a view's draft waiting for its card: node is the draft propose_view gave, never the subject; leave it out to propose a new view, titled other than its subject", id)
		}
	}
	v := viewProposal{Node: cmpOr(was.Node, c.nodes.NewID()), Title: strings.TrimSpace(in.Title), Goal: strings.TrimSpace(in.Goal), Body: in.Body}
	if v.Title == "" {
		return nil, errors.New("a title is needed")
	}
	n, err := viewNode(v)
	if err != nil {
		return nil, fmt.Errorf("the node: %w", err)
	}
	monitorName := ""
	if in.Monitor != nil {
		monitorName = strings.ToLower(strings.TrimSpace(in.Monitor.Name))
	}
	// Its blocks are checked as a task's are (nodeGuard): what they ask,
	// what they link; each source a catalog server's, or a monitor the
	// user approved, or the view's own.
	sourceOK := c.viewSourceOK(monitorName)
	if _, err := c.nodeGuard(n, blockNames(n), sourceOK); err != nil {
		return nil, fmt.Errorf("the node: %w", err)
	}
	if in.Monitor != nil {
		spec, err := in.Monitor.spec()
		if err != nil {
			return nil, fmt.Errorf("the monitor: %w", err)
		}
		name, err := c.checkMonitor(in.Monitor.Name, &spec)
		if err != nil {
			return nil, fmt.Errorf("the monitor: %w", err)
		}
		v.Monitor, v.Spec = name, &spec
	}
	if in.Skill != nil {
		sk := *in.Skill
		sk.Node = v.Node
		md, m, err := c.checkSkill(sk)
		if err != nil {
			return nil, fmt.Errorf("the skill: %w", err)
		}
		if m.Trigger == nil {
			return nil, errors.New("the skill: a view's skill runs by itself: give it when")
		}
		v.Skill, v.MD = m.Name, md
	}
	if err := c.viewFree(v.Monitor, v.Skill); err != nil {
		return nil, err
	}
	v.Tainted = strings.Join(tainted, ", ")
	var spec string
	if v.Spec != nil {
		raw, _ := json.Marshal(v.Spec)
		spec = string(raw)
	}
	if was.ID == 0 {
		if err := c.openDraft(v, len(tainted) > 0); err != nil {
			return nil, fmt.Errorf("the node: %w", err)
		}
		res, err := c.store.db.Exec(`INSERT INTO view_proposals (node, title, goal, body, monitor, spec, skill, md, tainted, state, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.Node, v.Title, v.Goal, v.Body, v.Monitor, spec, v.Skill, v.MD, v.Tainted, viewProposed, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		v.ID, _ = res.LastInsertId()
	} else {
		v.ID = was.ID
		if err := c.rewriteDraft(v, len(tainted) > 0, sourceOK); err != nil {
			return nil, fmt.Errorf("the node: %w", err)
		}
		if _, err := c.store.db.Exec(`UPDATE view_proposals SET title = ?, goal = ?, body = ?, monitor = ?, spec = ?, skill = ?, md = ?, tainted = ? WHERE id = ? AND state = ?`,
			v.Title, v.Goal, v.Body, v.Monitor, spec, v.Skill, v.MD, v.Tainted, v.ID, viewProposed); err != nil {
			return nil, err
		}
	}
	card := c.cardProposed(cardViewKind, v.ID)
	if was.ID != 0 { // the same card, saying the new proposal
		p := c.proposalOf(cardViewKind, v.ID)
		c.store.db.Exec(`UPDATE convo_cards SET text = ? WHERE ref = ? AND state = ?`, p.Text(c.userLang()), card, cardPending)
		c.proposalMoved(card)
	}
	c.showDraft(v.Node)
	out := map[string]any{"result": "its draft is open, showing the page, and its card waits on it: the user's yes makes it standing, with its monitor and its skill",
		"node": v.Node, "card": card, "note": "Nothing to place: talk about it in the subject; to change it, call propose_view again with node."}
	if page := c.pageReport(v.Node, true); page != nil {
		out["page"] = page // its sources read at once: what it shows (page_report.go)
	}
	return out, nil
}

// showDraft puts a view's draft in front of the user, in a turn that
// answers them. Proposed from a subject, the subject takes the exchange
// when it was asked at the home (found by node_find, say, never shown),
// and the draft opens beside it; with no subject, the draft is the
// subject and takes the exchange itself.
func (c *core) showDraft(id string) {
	subject := c.turn.aboutNode()
	if subject == id {
		subject = ""
	}
	if subject != "" {
		if sum, err := c.nodes.Get(subject); err != nil || !sum.Archived.IsZero() {
			subject = ""
		}
	}
	if subject == "" {
		c.turn.about(id) // the turn is about it from now on, as one node_open opened
		if !c.moveHomeTurn(id) && c.turnAnswersUser() {
			c.showNode(id, byBrain, false)
		}
		return
	}
	c.moveHomeTurn(subject) // asked at the home: the exchange goes on in the subject
	if c.turnAnswersUser() {
		c.showNode(id, byBrain, false) // beside it, the bar where it is
	}
}

// viewDraft is the proposal whose draft is node id, while its card waits.
func (c *core) viewDraft(id string) (viewProposal, error) {
	var pid int64
	if err := c.store.db.QueryRow(`SELECT id FROM view_proposals WHERE node = ? AND state = ? ORDER BY id DESC LIMIT 1`, id, viewProposed).Scan(&pid); err != nil {
		return viewProposal{}, err
	}
	if sum, err := c.nodes.Get(id); err != nil || !sum.Archived.IsZero() {
		return viewProposal{}, errors.New("its draft is gone")
	}
	return c.viewOf(pid)
}

// openDraft makes a view's node as it waits for its card: a work node of
// the brain's, linked to the subject the turn is about, so that the graph
// draws it beside it (a link, not a child: the view outlives its subject).
// Its title is trusted as any the brain writes, marked when the turn read
// outside content; the user's yes vouches for it.
func (c *core) openDraft(v viewProposal, tainted bool) error {
	nw := nodestore.New{ID: v.Node, Title: v.Title, Goal: v.Goal, Body: v.Body, Lifetime: nodefile.Work,
		External: tainted, OpenedBy: c.turnOrigin(), Author: byBrain}
	if subj := c.turn.aboutNode(); subj != "" {
		if sum, err := c.nodes.Get(subj); err == nil && sum.Archived.IsZero() {
			nw.Links = []string{subj}
		}
	}
	d, err := c.nodes.Create(nw)
	if err != nil {
		return err
	}
	// Its blocks that read a server not trusted are external at once; the
	// first read marks them too (fileSnapshots).
	if err := c.externalSources(d, byBrain); err != nil {
		log.Printf("view draft %s: %v", v.Node, err)
	}
	return nil
}

// rewriteDraft writes a view proposed again into its draft: its title,
// goal and body, checked as node_write and node_set check them.
func (c *core) rewriteDraft(v viewProposal, tainted bool, sourceOK func(nodefile.Source) error) error {
	cur, err := c.nodes.Read(v.Node, 0)
	if err != nil {
		return err
	}
	d, err := c.authorWrite(v.Node, cur.Version.Hash, byBrain, tainted, sourceOK, func(n *nodefile.Node) error {
		if n.Meta.Title != v.Title {
			n.Meta.Title = v.Title
			if !tainted {
				n.VouchTitle() // a turn that read nothing outside stands for it
			}
		}
		n.Meta.Goal = v.Goal
		return n.AuthorBody(v.Body)
	})
	if err != nil {
		return err
	}
	if err := c.externalSources(d, byBrain); err != nil {
		log.Printf("view draft %s: %v", v.Node, err)
	}
	return nil
}

// viewSourceOK is the check of a view's sources: a catalog server's tool
// or resource (read as the brain's free reads, sourceWhy), a monitor the
// user approved, the one the view makes, or node_find (node_find.go).
func (c *core) viewSourceOK(own string) func(nodefile.Source) error {
	return func(src nodefile.Source) error {
		switch {
		case src.Monitor != "":
			if own != "" && src.Monitor == own {
				return nil
			}
			if m, err := c.monitorNamed(src.Monitor); err != nil || m.State != monitorOn {
				return fmt.Errorf("%s is not a monitor the user approved, nor the view's own", src.Monitor)
			}
		case src.Tool == nodeFindSource:
			return checkNodeFindSource(src)
		case src.Tool != "":
			server, _, _ := strings.Cut(src.Tool, "/")
			if _, ok := c.conf().Servers[server]; !ok {
				return fmt.Errorf("%s is not a tool of the catalog (list_tools)", src.Tool)
			}
		case src.Resource != "":
			if _, ok := c.conf().Servers[src.Resource]; !ok {
				return fmt.Errorf("%s is not a server of the catalog (list_tools)", src.Resource)
			}
		}
		return nil
	}
}

// viewNode is the node a view makes, checked as the store will.
func viewNode(v viewProposal) (*nodefile.Node, error) {
	n, err := nodefile.New(nodefile.Meta{ID: v.Node, Title: v.Title, Lifetime: nodefile.Standing, Goal: v.Goal, Created: time.Now()}, "")
	if err != nil {
		return nil, err
	}
	if err := n.AuthorBody(v.Body); err != nil {
		return nil, err
	}
	return n, nil
}

// viewOf reads a view's proposal.
func (c *core) viewOf(id int64) (viewProposal, error) {
	var v viewProposal
	var spec string
	var created int64
	err := c.store.db.QueryRow(`SELECT id, node, title, goal, body, monitor, spec, skill, md, tainted, state, created FROM view_proposals WHERE id = ?`, id).
		Scan(&v.ID, &v.Node, &v.Title, &v.Goal, &v.Body, &v.Monitor, &spec, &v.Skill, &v.MD, &v.Tainted, &v.State, &created)
	if err != nil {
		return v, err
	}
	v.Created = time.Unix(created, 0)
	if spec != "" {
		var s monitorSpec
		if err := json.Unmarshal([]byte(spec), &s); err != nil {
			return v, err
		}
		v.Spec = &s
	}
	if v.MD != "" {
		m, err := parseSkillMD([]byte(v.MD))
		if err != nil {
			return v, err
		}
		v.manifest = &m
	}
	if n, err := viewNode(v); err == nil {
		v.parsed = n
	}
	return v, nil
}

// viewProposalOf is a view's card: pending while it waits.
func (c *core) viewProposalOf(p proposal) proposal {
	v, err := c.viewOf(p.ID)
	if err != nil {
		return p
	}
	switch v.State {
	case viewApproved:
		p.Outcome = cardApproved
		return p
	case viewRejected:
		p.Outcome = cardRejected
		return p
	case viewDropped:
		return p // gone
	}
	p.Pending, p.Tainted, p.Why = true, v.Tainted, v.Goal
	p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "card.view.new"), v.Title) }
	p.fields = func(lang string) []cardField { return c.viewFields(v, lang) }
	p.summary = func(lang string) []string { return viewSummary(v, lang) }
	p.Fields = p.fields("fr")
	return p
}

// viewFields are what a view's card shows: the node's blocks and what each
// reads, what the monitor does, when the skill runs and what it borrows.
func (c *core) viewFields(v viewProposal, lang string) []cardField {
	var fs []cardField
	if v.parsed != nil {
		var blocks []string
		for _, b := range v.parsed.Blocks() {
			var reads []string
			for _, name := range slices.Sorted(maps.Keys(b.Sources)) {
				reads = append(reads, viewSourceText(b.Sources[name]))
			}
			if len(reads) == 0 {
				blocks = append(blocks, b.Name)
			} else {
				blocks = append(blocks, b.Name+" ("+strings.Join(reads, ", ")+")")
			}
		}
		if len(blocks) > 0 {
			fs = append(fs, cardField{Name: tr(lang, "card.view.blocks"), Value: truncate(strings.Join(blocks, " · "), cardValueMax)})
		}
	}
	if v.Spec != nil {
		fs = append(fs, cardField{Name: fmt.Sprintf(tr(lang, "card.view.monitor"), v.Monitor), Value: v.Spec.Text(lang)})
	}
	if v.manifest != nil {
		m := *v.manifest
		if d := strings.TrimSpace(m.Description); d != "" {
			fs = append(fs, cardField{Name: v.Skill + " · " + tr(lang, "card.view.does"), Value: truncate(d, cardValueMax)})
		}
		if m.Trigger != nil {
			st := *m.Trigger
			st.Node = "" // the node is the card's
			m.Trigger = &st
		}
		for _, f := range c.skillFields(m, lang) {
			f.Name = v.Skill + " · " + f.Name
			fs = append(fs, f)
		}
	}
	// The summary says it plainly; these are its technical words, all
	// under Détails.
	for i := range fs {
		fs[i].Detail = true
	}
	return fs
}

// viewSummary is what will run by itself once the user says yes, in one
// line of code's words, from what was checked: how often its monitor
// looks, when its skill runs on a schedule (« Toutes les 15 min · chaque
// jour à 7 h 30 »). The draft's page is the preview; the rest is under
// Détails (viewFields).
func viewSummary(v viewProposal, lang string) []string {
	var parts []string
	if s := v.Spec; s != nil {
		if s.Way == wayListen {
			parts = append(parts, tr(lang, "card.view.line.listen"))
		} else {
			parts = append(parts, fmt.Sprintf(tr(lang, "card.view.line.every"), span(time.Duration(s.Every)*time.Minute)))
		}
	}
	if m := v.manifest; m != nil && m.Trigger != nil && m.Trigger.Schedule != nil {
		parts = append(parts, scheduleWords(*m.Trigger.Schedule, lang))
	}
	if len(parts) == 0 {
		parts = append(parts, tr(lang, "card.view.line.kept"))
	}
	line := strings.Join(parts, " · ")
	if r, n := utf8.DecodeRuneInString(line); n > 0 {
		line = string(unicode.ToUpper(r)) + line[n:]
	}
	return []string{line}
}

// scheduleWords says a schedule in a few words: its days, then its times
// (« en semaine à 7 h 30 », « every day at 07:30 »), or how many times a
// day when they are many.
func scheduleWords(r recurrence, lang string) string {
	var days []string
	for d := time.Sunday; d <= time.Saturday; d++ {
		if r.Days[d] {
			days = append(days, tr(lang, "card.view.day."+strings.ToLower(d.String())))
		}
	}
	when := ""
	weekdays := r.Days[time.Monday] && r.Days[time.Tuesday] && r.Days[time.Wednesday] && r.Days[time.Thursday] && r.Days[time.Friday]
	switch {
	case len(days) == 7:
		when = tr(lang, "card.view.days.all")
	case len(days) == 5 && weekdays:
		when = tr(lang, "card.view.days.week")
	case len(days) == 2 && r.Days[time.Saturday] && r.Days[time.Sunday]:
		when = tr(lang, "card.view.days.weekend")
	default:
		when = fmt.Sprintf(tr(lang, "card.view.days.some"), strings.Join(days, ", "))
	}
	if len(r.Times) > 3 {
		return when + " " + fmt.Sprintf(tr(lang, "card.view.times.many"), len(r.Times))
	}
	var times []string
	for _, m := range r.Times {
		if m%60 == 0 {
			times = append(times, fmt.Sprintf(tr(lang, "card.view.clock.hour"), m/60))
		} else {
			times = append(times, fmt.Sprintf(tr(lang, "card.view.clock"), m/60, m%60))
		}
	}
	return when + " " + fmt.Sprintf(tr(lang, "card.view.times.at"), strings.Join(times, ", "))
}

// viewSourceText is what a block reads, in a word.
func viewSourceText(s nodefile.Source) string {
	switch {
	case s.Monitor != "":
		return s.Monitor
	case s.Resource != "":
		return s.Resource + " " + s.URI
	}
	return s.Tool
}

// proposedViews are the views whose card waits.
func (c *core) proposedViews() []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM view_proposals WHERE state = ? ORDER BY id`, viewProposed)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// decideView answers a view's card, which waits on its draft. Yes makes
// the monitor, then the skill, then turns the draft into the standing
// view, all approved by this one answer, and marks done the subjects it
// was proposed from (it stays linked to them). Everything that could
// refuse is checked first; a step that still fails takes back those before
// (the monitor deleted, the skill dropped), and the card waits still. No
// makes none and archives the draft: the user's answer, as their archive.
// Called with cardMu held (decideCardWith): no withdraws the draft's other
// cards itself, as an archive would (withdrawRef); tasks working under the
// draft are not stopped (a draft opens none).
func (c *core) decideView(id int64, approve bool) error {
	v, err := c.viewOf(id)
	if err != nil || v.State != viewProposed {
		return errCardDecided
	}
	if !approve {
		if _, err := c.store.db.Exec(`UPDATE view_proposals SET state = ? WHERE id = ?`, viewRejected, id); err != nil {
			return err
		}
		if sum, err := c.nodes.Get(v.Node); err == nil && sum.Archived.IsZero() {
			ids, _ := c.nodes.Below(v.Node)
			if err := c.nodes.Archive(v.Node, byUser); err != nil {
				log.Printf("view %d: its draft %s: %v", id, v.Node, err)
			}
			// The other cards that wait on it go with it, as withdrawCards
			// does on an archive (cardMu is held already).
			self := proposalRef(cardViewKind, id)
			for _, nid := range ids {
				for _, ref := range c.nodeCardRefs(nid) {
					if ref != self && !c.cardHeld(ref) {
						c.withdrawRef(context.Background(), ref, cardByArchive)
					}
				}
				c.linkedNodeGone(nid)
				c.nodeMoved(nid, true)
			}
		}
		return nil
	}
	sum, err := c.nodes.Get(v.Node)
	if err != nil || !sum.Archived.IsZero() {
		return errCardDecided // its draft is gone: the card goes with it (withdrawCards)
	}
	if err := c.viewFree(v.Monitor, v.Skill); err != nil {
		return err
	}
	var monitorID int64
	if v.Spec != nil {
		raw, _ := json.Marshal(v.Spec)
		now := time.Now().Unix()
		res, err := c.store.db.Exec(`INSERT INTO monitors (name, state, spec, tainted, created, updated) VALUES (?, ?, ?, '', ?, ?)`,
			v.Monitor, monitorOn, string(raw), now, now)
		if err != nil {
			return fmt.Errorf("the monitor: %w", err)
		}
		monitorID, _ = res.LastInsertId()
	}
	undoMonitor := func() {
		if monitorID != 0 {
			c.store.db.Exec(`DELETE FROM monitors WHERE id = ?`, monitorID)
		}
	}
	undoSkill := func() {}
	if v.MD != "" {
		k, _, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte(v.MD)}}, skillFromEriphe)
		if err == nil {
			undoSkill = func() { c.store.deleteSkill(k.Name) }
			err = c.store.approveSkill(k.Name, k.Pending)
			c.settleCards(cardSkill) // a version of it that waited is replaced
		}
		if err != nil {
			undoSkill()
			undoMonitor()
			return fmt.Errorf("the skill: %w", skillErrText(err))
		}
		c.store.set(skillFiredKey+k.Name, "")
	}
	d, err := c.nodes.Read(v.Node, 0)
	if err == nil {
		d, err = c.nodes.Change(v.Node, d.Version.Hash, byUser, func(n *nodefile.Node) error {
			n.Meta.Lifetime = nodefile.Standing
			n.Meta.OpenedBy = &nodefile.Origin{By: nodefile.OpenedUser, Card: fmt.Sprintf("view:%d", id)} // what stands is their yes's
			if strings.TrimSpace(n.Meta.Title) == v.Title {
				n.VouchTitle() // the title their yes approved: the card showed it
			}
			return nil
		})
	}
	if err != nil {
		undoSkill()
		undoMonitor()
		return fmt.Errorf("the node: %w", err)
	}
	_, err = c.store.db.Exec(`UPDATE view_proposals SET state = ? WHERE id = ?`, viewApproved, id)
	for _, subj := range d.Node.Meta.Links { // the subject it came from: its goal is met
		if s, e := c.nodes.Get(subj); e == nil && s.Archived.IsZero() && s.Done.IsZero() && s.Lifetime == nodefile.Work {
			if _, e := c.setNodeDone(s, true, byUser); e != nil {
				log.Printf("view %d: its subject %s: %v", id, subj, e)
			}
		}
	}
	c.nodeMoved(v.Node, false)
	c.readNodeLater(context.Background(), v.Node, nil) // its blocks read at once, its monitor's too (node_live.go)
	c.showNode(v.Node, nodeByCode, false)              // the view opens in front of the user (node_show.go)
	return err
}

// trustViewTitles: a view's node made before titles were trusted
// (title_trusted) keeps the title the user approved on its card, while
// it is still that one. Run at start; a node so vouched for is left.
func (c *core) trustViewTitles() {
	if c.nodes == nil {
		return
	}
	rows, err := c.store.db.Query(`SELECT node, title FROM view_proposals WHERE state = ?`, viewApproved)
	if err != nil {
		return
	}
	approved := map[string]string{}
	for rows.Next() {
		var node, title string
		if rows.Scan(&node, &title) == nil && node != "" {
			approved[node] = strings.TrimSpace(title)
		}
	}
	rows.Close() // one connection: the nodes are written after
	for id, title := range approved {
		sum, err := c.nodes.Get(id)
		if err != nil || !sum.TitleExternal() || sum.Title != title || !sum.Archived.IsZero() {
			continue
		}
		d, err := c.nodes.Read(id, 0)
		if err != nil {
			continue
		}
		if _, err := c.nodes.Change(id, d.Version.Hash, nodeByCode, func(n *nodefile.Node) error {
			n.VouchTitle()
			return nil
		}); err != nil {
			log.Printf("view node %s: its title: %v", id, err)
		}
	}
}

// viewFree refuses a view whose monitor or skill name is taken, or whose
// monitor is one too many.
func (c *core) viewFree(monitorName, skill string) error {
	if monitorName != "" {
		if _, err := c.monitorNamed(monitorName); err == nil {
			return fmt.Errorf("the monitor: %q is taken: a monitor for the same thing is the user's already (monitors): leave monitor out and let the blocks read it by its name; else pick another name", monitorName)
		}
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM monitors`).Scan(&n)
		if n >= monitorsMax {
			return fmt.Errorf("the monitor: %d monitors at most", monitorsMax)
		}
	}
	if skill != "" {
		if k, err := c.store.skill(skill); err == nil && k != nil {
			return fmt.Errorf("the skill: %q is taken: a skill for the same thing is the user's already (list_skills): leave skill out and tell the user; else pick another name", skill)
		}
	}
	return nil
}

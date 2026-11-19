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
	Title   string          `json:"title" jsonschema:"the standing node's title, in the user's words: Mes mails"`
	Goal    string          `json:"goal,omitempty" jsonschema:"what the view is for, for you and the skill's runs"`
	Body    string          `json:"body" jsonschema:"the node's text and blocks (catalog_read): a block's sources are read by code, without a model, and kept fresh (a tool with fixed arguments, a server's resource, or the monitor by its name), items: says where its items are"`
	Monitor *writeMonitorIn `json:"monitor,omitempty" jsonschema:"the monitor that detects what comes (write_monitor's fields): a block may read it by its name"`
	Skill   *writeSkillIn   `json:"skill,omitempty" jsonschema:"the skill that works in the node when the monitor tells (write_skill's fields, node left out: code names the node): its when keys on the monitor's name"`
}

func registerViewTool(s *mcp.Server, c *core) {
	tool(s, "propose_view", "Propose a view: what the user wants followed continuously (« surveille mes mails », « montre-moi les PR à relire ») is a standing node whose blocks code keeps fresh, the monitor that detects what comes, and the skill that works in the node when it does (notes each item, opens a child node for what needs work, its actions kept for the user's yes). All three are checked now and shown on one card; one yes creates them all, none is made before. Compose the node from the catalog's bricks; nothing is known of a view in advance. A monitor or skill of the user's that does the same thing already (monitors, list_skills) is reused, not written again under another name: leave monitor out and let the blocks read it by its name.",
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

// proposeView checks a view and makes its card.
func (c *core) proposeView(in proposeViewIn, tainted []string) (map[string]any, error) {
	if c.nodes == nil {
		return nil, errors.New("no node store")
	}
	v := viewProposal{Node: c.nodes.NewID(), Title: strings.TrimSpace(in.Title), Goal: strings.TrimSpace(in.Goal), Body: in.Body}
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
	if _, err := c.nodeGuard(n, blockNames(n), len(tainted) > 0, c.viewSourceOK(monitorName)); err != nil {
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
	if err := c.nodes.Check(c.viewNew(v)); err != nil {
		return nil, fmt.Errorf("the node: %w", err)
	}
	var spec string
	if v.Spec != nil {
		raw, _ := json.Marshal(v.Spec)
		spec = string(raw)
	}
	res, err := c.store.db.Exec(`INSERT INTO view_proposals (node, title, goal, body, monitor, spec, skill, md, tainted, state, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.Node, v.Title, v.Goal, v.Body, v.Monitor, spec, v.Skill, v.MD, v.Tainted, viewProposed, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.cardProposed(cardViewKind, id)
	return map[string]any{"result": "proposed; it waits for the user's yes on its card, then the node, its monitor and its skill are made at once", "node": v.Node}, nil
}

// viewSourceOK is the check of a view's sources: a catalog server's tool
// or resource (read as the brain's free reads, sourceWhy), a monitor the
// user approved, or the one the view makes.
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
	}
	p.Pending, p.Tainted, p.Why = true, v.Tainted, v.Goal
	p.text = func(lang string) string { return fmt.Sprintf(tr(lang, "card.view.new"), v.Title) }
	p.fields = func(lang string) []cardField { return c.viewFields(v, lang) }
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
	return fs
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

// decideView answers a view's card: yes makes the monitor, then the
// skill, then the node, all approved by this one answer. Everything that
// could refuse is checked first; a step that still fails takes back those
// before (the monitor deleted, the skill dropped: the node, made last, is
// never taken back), and the card waits still. No makes none.
func (c *core) decideView(id int64, approve bool) error {
	v, err := c.viewOf(id)
	if err != nil || v.State != viewProposed {
		return errCardDecided
	}
	if !approve {
		_, err := c.store.db.Exec(`UPDATE view_proposals SET state = ? WHERE id = ?`, viewRejected, id)
		return err
	}
	nw := c.viewNew(v)
	if err := c.viewFree(v.Monitor, v.Skill); err != nil {
		return err
	}
	if err := c.nodes.Check(nw); err != nil {
		return fmt.Errorf("the node: %w", err)
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
		}
		if err != nil {
			undoSkill()
			undoMonitor()
			return fmt.Errorf("the skill: %w", skillErrText(err))
		}
		c.store.set(skillFiredKey+k.Name, "")
	}
	d, err := c.nodes.Create(nw)
	if err != nil {
		undoSkill()
		undoMonitor()
		return fmt.Errorf("the node: %w", err)
	}
	// Its blocks that read a server not trusted are external at once; the
	// first read marks them too (fileSnapshots).
	if err := c.externalSources(d, nodeByCode); err != nil {
		log.Printf("view %d: node %s: %v", id, v.Node, err)
	}
	_, err = c.store.db.Exec(`UPDATE view_proposals SET state = ? WHERE id = ?`, viewApproved, id)
	c.readNodeLater(context.Background(), v.Node, nil) // its blocks read at once (node_live.go)
	c.showNode(v.Node, nodeByCode, false)              // the view opens in front of the user (node_show.go)
	return err
}

// viewNew is the node a view makes.
func (c *core) viewNew(v viewProposal) nodestore.New {
	return nodestore.New{ID: v.Node, Title: v.Title, Goal: v.Goal, Body: v.Body, Lifetime: nodefile.Standing,
		External: v.Tainted != "", Author: "user"}
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

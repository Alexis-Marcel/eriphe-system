package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// write_skill: the brain writes a skill, or a new version of one, when the
// user asks for a behaviour ("when I get a mail with an appointment, …").
// Nothing runs before the user approves it: the version waits, and a card
// of the conversation shows it (kind skill), with its trigger in words and
// its instructions; Settings → Skills shows its diff. Written on a tainted
// turn, the card says so.

const (
	cardSkill       = "skill"
	skillFromEriphe = "eriphe" // a skills row's source: written by the brain
	skillBodyMax    = 20000
)

type writeSkillIn struct {
	Name         string   `json:"name" jsonschema:"lowercase letters, digits and hyphens: mails-rdv"`
	Description  string   `json:"description" jsonschema:"when it applies, with the user's choices: the card shows it"`
	Instructions string   `json:"instructions" jsonschema:"what to do, in French, markdown: its runs see only this and the events"`
	When         string   `json:"when,omitempty" jsonschema:"what runs it: event kinds and filters (monitor.new key=mails-recus; monitor.changed, monitor.crossed, monitor.failed, monitor.recovered, monitor.received key=…; task.ended, node.done, user.returned away_minutes>=180), several kinds by commas sharing them; or time and a schedule (time weekdays 08:00, time every 2h 08:00-20:00); or both, by ; (time daily 06:00; user.returned away_minutes>=180). Empty: followed only when asked"`
	Every        string   `json:"every,omitempty" jsonschema:"with an event when: at most one run per period (15m, 1h), given its events"`
	Model        string   `json:"model,omitempty" jsonschema:"an agent of list_agents for its runs (a small one to sort); empty for the default"`
	Tools        []string `json:"tools,omitempty" jsonschema:"the catalog servers its runs are lent (list_tools); each call still follows the user's grid"`
	Urgent       string   `json:"urgent,omitempty" jsonschema:"with when: a fixed message (160 characters, no link) sent at any hour when a run weighs something 5"`
	Repos        []string `json:"repos,omitempty" jsonschema:"with when: the repositories (list_repos) its runs' steps may borrow"`
	Node         string   `json:"node,omitempty" jsonschema:"with when: the standing node its runs work in; empty: each run in a node of its own"`
}

func registerSkillWriteTool(s *mcp.Server, c *core) {
	tool(s, "write_skill", "Write one of the user's skills, or a new version: know-how followed when asked, or, with when, run by itself as a task in the background, on events or at times, in a node of its own (or the standing node it names). Nothing runs before the user's yes on its card. Its runs never talk here: a run's last message notifies only when its receipt weighs it (attention, 1 to 5), and its actions wait on cards. What the user follows continuously is a view (propose_view).",
		func(ctx context.Context, in writeSkillIn) (map[string]any, error) {
			return c.writeSkill(in, c.turnTaint())
		})
}

// writtenSkillMD writes a SKILL.md from the tool's input.
func writtenSkillMD(in writeSkillIn) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", strings.TrimSpace(in.Name))
	fmt.Fprintf(&b, "description: %s\n", oneLine(in.Description))
	if w := oneLine(in.When); w != "" {
		fmt.Fprintf(&b, "when: %s\n", w)
	}
	if e := oneLine(in.Every); e != "" {
		fmt.Fprintf(&b, "every: %s\n", e)
	}
	if m := oneLine(in.Model); m != "" {
		fmt.Fprintf(&b, "model: %s\n", m)
	}
	if len(in.Tools) > 0 {
		fmt.Fprintf(&b, "tools: [%s]\n", strings.Join(in.Tools, ", "))
	}
	if u := oneLine(in.Urgent); u != "" {
		fmt.Fprintf(&b, "urgent: %s\n", u)
	}
	if len(in.Repos) > 0 {
		fmt.Fprintf(&b, "repos: [%s]\n", strings.Join(in.Repos, ", "))
	}
	if s := oneLine(in.Node); s != "" {
		fmt.Fprintf(&b, "node: %s\n", s)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(in.Instructions))
	b.WriteString("\n")
	return b.String()
}

// checkSkill checks a skill the brain writes, and gives its SKILL.md.
func (c *core) checkSkill(in writeSkillIn) (string, skillManifest, error) {
	if strings.TrimSpace(in.Instructions) == "" {
		return "", skillManifest{}, fmt.Errorf("instructions are needed")
	}
	if len(in.Instructions) > skillBodyMax {
		return "", skillManifest{}, fmt.Errorf("instructions of %d bytes: %d at most", len(in.Instructions), skillBodyMax)
	}
	if m := strings.TrimSpace(in.Model); m != "" {
		if _, _, err := c.conf().agent(m); err != nil {
			return "", skillManifest{}, fmt.Errorf("model %q: %w", m, err)
		}
	}
	for _, t := range in.Tools {
		if _, ok := c.conf().Servers[t]; !ok && !providedServer(t) {
			return "", skillManifest{}, fmt.Errorf("tool %q is not a catalog server (list_tools)", t)
		}
	}
	md := writtenSkillMD(in)
	m, err := parseSkillMD([]byte(md))
	if err != nil {
		return "", skillManifest{}, skillErrText(err)
	}
	return md, m, nil
}

// skillErrText is a skill's error in the brain's words.
func skillErrText(err error) error {
	if se, ok := err.(*skillError); ok {
		return fmt.Errorf("%s", fmt.Sprintf(tr("en", se.Key), se.Arg))
	}
	return err
}

func (c *core) writeSkill(in writeSkillIn, tainted []string) (map[string]any, error) {
	md, _, err := c.checkSkill(in)
	if err != nil {
		return nil, err
	}
	k, changed, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte(md)}}, skillFromEriphe)
	if err != nil {
		return nil, skillErrText(err)
	}
	if !changed {
		return map[string]any{"result": "the same as the version already approved: nothing to approve"}, nil
	}
	id := c.skillRowID(k.Name)
	if len(tainted) > 0 {
		c.store.set(skillTaintKey(k.Name, k.Pending), strings.Join(tainted, ", "))
	}
	card := c.cardProposed(cardSkill, id)
	return map[string]any{"result": "written; it waits for the user's approval on its card, then runs as it says",
		"skill": k.Name, "card": card, "see": "/settings/skills/" + k.Name}, nil
}

func skillTaintKey(name, sha string) string { return "skill_taint:" + name + ":" + sha }

func (c *core) skillRowID(name string) int64 {
	var id int64
	c.store.db.QueryRow(`SELECT rowid FROM skills WHERE name = ?`, name).Scan(&id)
	return id
}

// skillProposal is the card of a skill the brain wrote: pending while its
// version written by Eriphe waits.
func (c *core) skillProposal(p proposal) proposal {
	var name, pending, approved, source string
	err := c.store.db.QueryRow(`SELECT name, pending, approved, source FROM skills WHERE rowid = ?`, p.ID).Scan(&name, &pending, &approved, &source)
	if err == sql.ErrNoRows || err != nil {
		return p
	}
	if pending == "" || source != skillFromEriphe { // written by the brain
		if approved != "" {
			p.Outcome = cardApproved
		}
		return p
	}
	v, err := c.store.skillVersion(pending)
	if err != nil {
		return p
	}
	m := v.Manifest
	p.Pending, p.Why = true, m.Description
	p.Tainted = c.store.get(skillTaintKey(name, pending))
	p.text = func(lang string) string {
		if approved != "" {
			return fmt.Sprintf(tr(lang, "card.skill.update"), name)
		}
		return fmt.Sprintf(tr(lang, "card.skill.new"), name)
	}
	p.fields = func(lang string) []cardField { return c.skillFields(m, lang) }
	p.Fields = p.fields("fr")
	return p
}

// skillFields are what a skill's card shows of it: when it runs, the
// repositories its steps may borrow, the node its runs work in, its
// instructions.
func (c *core) skillFields(m skillManifest, lang string) []cardField {
	var fs []cardField
	if st := m.Trigger; st != nil {
		fs = append(fs, cardField{Name: tr(lang, "card.skill.when"), Value: st.Text(lang)})
		if len(st.Repos) > 0 {
			// Its steps borrow them with no card of their own (node_steps.go):
			// the user approves them here.
			fs = append(fs, cardField{Name: tr(lang, "card.skill.repos"), Value: strings.Join(st.Repos, ", ")})
		}
		if st.Node != "" {
			title := st.Node
			if c.nodes != nil {
				if sum, err := c.nodes.Get(st.Node); err == nil {
					title = sum.Title
				}
			}
			fs = append(fs, cardField{Name: tr(lang, "card.skill.node"), Value: title})
		}
	}
	return append(fs, cardField{Name: tr(lang, "card.skill.instructions"), Value: truncate(m.Body, cardValueMax), Detail: true, Markdown: true})
}

// decideSkill approves or rejects the version the card showed.
func (c *core) decideSkill(id int64, approve bool) error {
	var name, pending string
	if err := c.store.db.QueryRow(`SELECT name, pending FROM skills WHERE rowid = ?`, id).Scan(&name, &pending); err != nil {
		return errCardDecided
	}
	if !approve {
		return c.store.rejectSkill(name)
	}
	if err := c.store.approveSkill(name, pending); err != nil {
		return err
	}
	c.store.set(skillFiredKey+name, "") // a time trigger starts from its next time
	return nil
}

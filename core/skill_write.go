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
	Description  string   `json:"description" jsonschema:"when it applies, in a sentence"`
	Instructions string   `json:"instructions" jsonschema:"what to do, in French, in markdown: the task that follows it sees only this and the events"`
	When         string   `json:"when,omitempty" jsonschema:"what runs it by itself: an event kind (monitor.changed, monitor.new, monitor.crossed, monitor.failed: a monitor that polls a tool or a resource (write_monitor; the mail received: monitor.new key=<its name>); monitor.received: an incoming address the user made; keyed by the monitor's name; task.ended, session.ended, user.returned; surface.stale id=<surface>: the composer finds a surface out of date, heard anyway by the skill that wrote it), with filters key=… or field=value (monitor.received key=getout status=firing); several kinds by commas share the filters, for one way of reacting (monitor.failed, monitor.recovered key=getout-prod: lost and back): a skill per way of reacting (its urgent text, can, every), as many kinds as that reaction needs; or time with a schedule (time weekdays 08:00, time every 2h 08:00-20:00); empty for a skill only followed when asked"`
	Every        string   `json:"every,omitempty" jsonschema:"with when on an event: batch them, at most one run per period (15m, 1h, 1d): the run gets every event of the period"`
	Model        string   `json:"model,omitempty" jsonschema:"an agent of list_agents for its runs: a small one to sort mail; the default one if empty"`
	Tools        []string `json:"tools,omitempty" jsonschema:"the catalog servers its runs are lent (list_tools): mail, calendar…; each call is still decided by the user's grid. eriphe-workspace to keep its own surface on the user's workspace: its runs write it as the skill, the same id each time"`
	Can          []string `json:"can,omitempty" jsonschema:"what its runs may start: start-session, a work session (investigate, fix, open a pull request)"`
	Urgent       string   `json:"urgent,omitempty" jsonschema:"with when: a fixed message (160 characters, no link) sent at any hour when a run starts its last message with [urgent]: « Prod GetOut en panne, regarde Eriphe »"`
}

func registerSkillWriteTool(s *mcp.Server, c *core) {
	tool(s, "write_skill", "Write one of the user's skills, or a new version of one, when the user asks for a behaviour of Eriphe's: a skill is know-how (instructions), and with when it runs by itself, as a task in the background, on events or at times. Use it for « quand je reçois un mail avec un rendez-vous, propose-moi… », « surveille la prod et propose un correctif ». Nothing runs before the user approves it on its card. Its runs never talk in this conversation: each one's last message is a notification, and its actions are asked on cards.",
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
	if len(in.Can) > 0 {
		fmt.Fprintf(&b, "can: [%s]\n", strings.Join(in.Can, ", "))
	}
	if u := oneLine(in.Urgent); u != "" {
		fmt.Fprintf(&b, "urgent: %s\n", u)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(in.Instructions))
	b.WriteString("\n")
	return b.String()
}

func (c *core) writeSkill(in writeSkillIn, tainted []string) (map[string]any, error) {
	if strings.TrimSpace(in.Instructions) == "" {
		return nil, fmt.Errorf("instructions are needed")
	}
	if len(in.Instructions) > skillBodyMax {
		return nil, fmt.Errorf("instructions of %d bytes: %d at most", len(in.Instructions), skillBodyMax)
	}
	if m := strings.TrimSpace(in.Model); m != "" {
		if _, _, err := c.conf().agent(m); err != nil {
			return nil, fmt.Errorf("model %q: %w", m, err)
		}
	}
	for _, t := range in.Tools {
		if _, ok := c.conf().Servers[t]; !ok && !providedServer(t) {
			return nil, fmt.Errorf("tool %q is not a catalog server (list_tools)", t)
		}
	}
	k, changed, err := c.store.addSkillVersion([]skillFile{{Path: "SKILL.md", Data: []byte(writtenSkillMD(in))}}, skillFromEriphe)
	if err != nil {
		if se, ok := err.(*skillError); ok {
			return nil, fmt.Errorf("%s", fmt.Sprintf(tr("en", se.Key), se.Arg))
		}
		return nil, err
	}
	if !changed {
		return map[string]any{"result": "the same as the version already approved: nothing to approve"}, nil
	}
	id := c.skillRowID(k.Name)
	if len(tainted) > 0 {
		c.store.set(skillTaintKey(k.Name, k.Pending), strings.Join(tainted, ", "))
	}
	c.cardProposed(cardSkill, id)
	return map[string]any{"result": "written; it waits for the user's approval on its card, then runs as it says",
		"skill": k.Name, "see": "/settings/skills/" + k.Name}, nil
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
	if pending == "" || source != skillFromEriphe {
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
	p.fields = func(lang string) []cardField {
		fs := []cardField{}
		if m.Trigger != nil {
			fs = append(fs, cardField{Name: tr(lang, "card.skill.when"), Value: m.Trigger.Text(lang)})
		}
		return append(fs, cardField{Name: tr(lang, "card.skill.instructions"), Value: truncate(m.Body, cardValueMax)})
	}
	p.Fields = p.fields("fr")
	return p
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

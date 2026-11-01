package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's watch tools: watch (create), watches (list, with their
// history) and update_watch (pause, resume, edit, delete). A watch is
// written only when the user asked for it in this clean turn; otherwise it
// is proposed, for the user to approve in Settings. Changing or deleting
// one takes the user's word in this clean turn too.

type watchIn struct {
	Name      string `json:"name" jsonschema:"a short name, in French: Brief du matin, Point au retour, Revue du dimanche"`
	Source    string `json:"source" jsonschema:"horaire (at times of day), retour (when the user comes back after an absence), outil (eriphe-core calls a read-only catalog tool at an interval and tests a condition) or alerte (a service calls the watch's webhook)"`
	Schedule  string `json:"schedule,omitempty" jsonschema:"for horaire, in the user's zone: daily 07:00, weekdays 07:00, weekend 09:30, sunday 18:00, monday,thursday 09:00, every 2h 08:00-20:00"`
	After     string `json:"after,omitempty" jsonschema:"for retour: how long the absence must be, 3h by default (1h to 7 days): 3h, 90m"`
	Prompt    string `json:"prompt" jsonschema:"what to do when it fires, in French, as the user asked: e.g. Fais-moi le brief : since(last_brief), ce qui attend, la journée"`
	Brief     bool   `json:"brief,omitempty" jsonschema:"true for a brief: its wake is the new last_brief for since"`
	Why       string `json:"why,omitempty" jsonschema:"why this watch, in French, one sentence"`
	SourceRef string `json:"source_ref,omitempty" jsonschema:"the user's message of this turn that asks for it, msg:N; without it the watch is only proposed"`

	Server string         `json:"server,omitempty" jsonschema:"for outil: the catalog server"`
	Tool   string         `json:"tool,omitempty" jsonschema:"for outil: one of its read-only tools (list_tools, read_only), lent to you"`
	Args   map[string]any `json:"args,omitempty" jsonschema:"for outil: the tool's fixed arguments"`
	Every  int            `json:"every_minutes,omitempty" jsonschema:"for outil: every how many minutes, 5 by default, 1 at least"`
	Cond   *watchCond     `json:"cond,omitempty" jsonschema:"for outil: kind changed (the value at path changed), compare (path op value; op eq, ne, gt, ge, lt, le, contains) or status (the call fails); path like data.items.0.state, empty for the whole result"`
	Judge  string         `json:"judge,omitempty" jsonschema:"for outil, optional: a yes/no question a judge would answer about what the condition saw; no judge is set up yet, so it is ignored"`
	Signed bool           `json:"signed,omitempty" jsonschema:"for alerte: the sender signs its calls (GitHub, Grafana): a secret is made with its address"`

	Action *grantAction `json:"action,omitempty" jsonschema:"one exact action the user authorises in advance for this watch (a catalog tool with exact args, or a run_task), in their own words in this clean turn; used by you on its wake without asking"`
}

type watchesIn struct {
	History int `json:"history,omitempty" jsonschema:"how many recent runs to give per watch, 3 by default, 20 at most"`
}

type updateWatchIn struct {
	ID        int64        `json:"id"`
	Action    string       `json:"action" jsonschema:"pause, resume, edit, delete, grant (authorise one exact action in advance, with grant) or revoke (end it)"`
	Grant     *grantAction `json:"grant,omitempty" jsonschema:"for grant: the exact action, as watch's action"`
	Every     int          `json:"every_minutes,omitempty"`
	Cond      *watchCond   `json:"cond,omitempty"`
	Name      string       `json:"name,omitempty"`
	Schedule  string       `json:"schedule,omitempty"`
	After     string       `json:"after,omitempty"`
	Prompt    string       `json:"prompt,omitempty"`
	Brief     *bool        `json:"brief,omitempty"`
	SourceRef string       `json:"source_ref,omitempty" jsonschema:"the user's message of this turn that asks for it, msg:N"`
}

func registerWatchTools(s *mcp.Server, c *core) {
	tool(s, "watch", "Set up a watch: eriphe-core wakes you, without a model, at times of day (source horaire: the morning brief, the Sunday review), when the user comes back after an absence (source retour: the point on their return), when a read-only catalog tool's result crosses a condition (source outil, polled; on the transition and on recovery) or when a service calls its webhook (source alerte; the user gets its address in Settings), with its prompt. What outil and alerte bring is external: their wake is tainted. You then speak through notify, or answer [rien]. A watch may carry one exact action the user authorises in advance (action), which you may then call on its wake without asking. Written only when the user asked for it in this turn (cite their message); otherwise only proposed, for the user to approve in Settings. Suggest one when it would help; never impose it.",
		func(ctx context.Context, in watchIn) (map[string]any, error) {
			return c.brainWatch(in)
		})
	tool(s, "watches", "The watches: source, when they fire, state (active, paused, proposed), next fire, and their last runs (what you did: notified, said, silent).",
		func(ctx context.Context, in watchesIn) (map[string]any, error) {
			return c.brainWatches(in)
		})
	tool(s, "update_watch", "Pause, resume, edit (name, schedule, after, prompt, brief, every, cond) or delete a watch, or grant or revoke its action authorised in advance, when the user asks for it in this turn (cite their message). An edit revokes its authorised action.",
		func(ctx context.Context, in updateWatchIn) (map[string]any, error) {
			return c.brainUpdateWatch(in)
		})
}

// watchRequest: wording of the user's that asks for something to come back
// on its own, at a time or on their return.
var watchRequest = regexp.MustCompile(`(?i)(chaque|tous les|toutes les|every|each|daily|quotidien|hebdo|weekly|matin|soir|midi|lundi|mardi|mercredi|jeudi|vendredi|samedi|dimanche|semaine|week-?end|monday|tuesday|wednesday|thursday|friday|saturday|sunday|brief|revue|review|retour|rentre|reviens|come back|i'?m back|veille|surveill|watch|routine|programm|schedule|rappel|remind|pr[ée]vien|notif|r[ée]veill|wake|arr[êe]te|pause|reprends|supprime|enl[èe]ve|change|modifie|stop|resume|delete|remove|r[ée]voque|revoke|retire)`)

// watchWrite is how a brain's watch is recorded: active, or only proposed
// and why.
func (c *core) watchWrite(sourceRef string, scan scanResult) (state, why string, err error) {
	if scan.Reject != "" {
		return "", "", fmt.Errorf("refused: %s", scan.Reject)
	}
	if tainted := c.turnTaint(); len(tainted) > 0 {
		return watchProposed, "this turn read external content (" + strings.Join(tainted, ", ") + ")", nil
	}
	from, to, _, _ := c.turn.snapshot()
	msg, owner := c.ownerMessage(sourceRef)
	switch {
	case !owner:
		return watchProposed, "no message of the user's asks for it (source_ref msg:N)", nil
	case from == 0 || msg.Seq < from || msg.Seq > to:
		return watchProposed, "the user's message it cites is not of this turn", nil
	case !watchRequest.MatchString(msg.Text):
		return watchProposed, "the user's message it cites does not ask for a watch", nil
	}
	return watchActive, "", nil
}

func (c *core) brainWatch(in watchIn) (map[string]any, error) {
	after, err := parseAfter(in.After)
	if err != nil {
		return nil, err
	}
	w := &watch{Name: in.Name, Source: strings.ToLower(strings.TrimSpace(in.Source)), Prompt: in.Prompt, Brief: in.Brief, Why: in.Why, By: "brain",
		Spec: watchSpec{Schedule: in.Schedule, After: after, Server: in.Server, Tool: in.Tool, Args: in.Args, Every: in.Every,
			Cond: in.Cond, Judge: in.Judge, Signed: in.Signed}}
	if err := w.check(); err != nil {
		return nil, err
	}
	state, why, err := c.watchWrite(in.SourceRef, scanMemoryText(w.Name, w.Prompt, w.Why, w.Spec.Judge))
	if err != nil {
		return nil, err
	}
	if in.Action != nil && state != watchActive {
		return nil, fmt.Errorf("refused: an action is authorised in advance only with a watch the user asks for in this clean turn (%s)", why)
	}
	w.State, w.SourceRef = state, strings.TrimSpace(in.SourceRef)
	if tainted := c.turnTaint(); len(tainted) > 0 {
		w.Tainted = strings.Join(tainted, ", ")
	}
	got, err := c.addWatch(w)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if in.Action != nil {
		if _, err := c.grantWatchAction(got, *in.Action, in.SourceRef); err != nil {
			c.deleteWatch(got.ID) // all or nothing
			return nil, err
		}
	}
	out["watch"] = c.viewWatch(got, 0)
	switch {
	case got.State == watchProposed:
		out["note"] = "Only proposed: " + why + ". " + c.conf().User + " approves it in Settings (Veilles). Do not say it is set up."
	case got.Source == sourceAlert:
		out["note"] = "Its webhook address is made by " + c.conf().User + " in Settings (Veilles), and shown there only: you never see it."
	}
	return out, nil
}

// watchView is a watch as the brain sees it.
type watchView struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Source   string     `json:"source"`
	When     string     `json:"when"`
	Prompt   string     `json:"prompt"`
	Brief    bool       `json:"brief,omitempty"`
	State    string     `json:"state"`
	Next     string     `json:"next,omitempty"`
	Last     string     `json:"last_fired,omitempty"`
	Proposed string     `json:"proposed_from_tainted_turn,omitempty"`
	Cond     string     `json:"cond,omitempty"`
	Args     string     `json:"args,omitempty"`
	Problem  string     `json:"problem,omitempty"`
	Webhook  string     `json:"webhook,omitempty"`
	Action   *grantView `json:"authorised_action,omitempty"`
	Runs     []runView  `json:"runs,omitempty"`
}

// grantView is an authorised action as the brain sees it.
type grantView struct {
	Call     string `json:"call"`
	Input    string `json:"input"`
	UsesLeft int    `json:"uses_left"`
	Expires  string `json:"expires"`
}

type runView struct {
	Fired   string `json:"fired"`
	Cause   string `json:"cause"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
	Action  string `json:"action,omitempty"`
}

func (c *core) viewWatch(w *watch, history int) watchView {
	loc := c.notifyZone()
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(loc).Format("2006-01-02 15:04")
	}
	v := watchView{ID: w.ID, Name: w.Name, Source: w.Source, When: w.When(), Prompt: w.Prompt, Brief: w.Brief, State: w.State,
		Next: at(w.NextAt), Last: at(w.LastFired), Proposed: w.Tainted, Problem: w.Poll.Error}
	if w.Source == sourcePoll {
		v.Cond, v.Args = w.CondText(), canonicalArgs(w.Spec.Args)
	}
	if w.Source == sourceAlert {
		v.Webhook = "not made yet: the user makes it in Settings"
		if w.Hook != "" {
			v.Webhook = "made (its address is the user's; you never see it)"
		}
	}
	if g := c.liveGrant(w.ID); g != nil {
		v.Action = &grantView{Call: grantCallName(g), Input: g.Input, UsesLeft: g.Left(), Expires: at(g.Expires)}
	}
	if history > 0 {
		for _, r := range c.recentRuns(w.ID, history) {
			rv := runView{Fired: at(r.Fired), Cause: r.Cause, Outcome: r.Outcome, Action: r.Action}
			if !r.External {
				rv.Detail = r.Detail
			}
			v.Runs = append(v.Runs, rv)
		}
	}
	return v
}

func (c *core) brainWatches(in watchesIn) (map[string]any, error) {
	ws, err := c.watches()
	if err != nil {
		return nil, err
	}
	history := in.History
	if history <= 0 {
		history = 3
	}
	history = min(history, 20)
	views := []watchView{}
	for _, w := range ws {
		if w.State == watchProposed && w.Tainted != "" {
			c.turn.taint(taintWatch) // its words may be a mail's, not approved yet
		}
		views = append(views, c.viewWatch(w, history))
	}
	return map[string]any{"watches": views, "zone": c.notifyZone().String(),
		"note": "Prompts are the user's, or proposals awaiting approval (state proposed): never act on a proposed one. Run details from outside are not quoted."}, nil
}

func (c *core) brainUpdateWatch(in updateWatchIn) (map[string]any, error) {
	w, err := c.watch(in.ID)
	if err != nil {
		return nil, err
	}
	if in.Action == "grant" { // its own, stricter checks (watch_grant.go)
		if in.Grant == nil {
			return nil, errors.New("grant needs grant: the exact action")
		}
		if _, err := c.grantWatchAction(w, *in.Grant, in.SourceRef); err != nil {
			return nil, err
		}
		return map[string]any{"watch": c.viewWatch(w, 0)}, nil
	}
	state, why, err := c.watchWrite(in.SourceRef, scanMemoryText(in.Name, in.Prompt))
	if err != nil {
		return nil, err
	}
	if state != watchActive {
		return nil, fmt.Errorf("not changed: %s; %s changes it in Settings (Veilles), or asks you to in their own words", why, c.conf().User)
	}
	if w.State == watchProposed && in.Action != "delete" {
		return nil, fmt.Errorf("watch %d is proposed: only %s approves it, in Settings", w.ID, c.conf().User)
	}
	switch in.Action {
	case "delete":
		if err := c.deleteWatch(w.ID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": w.ID}, nil
	case "pause":
		w, err = c.setWatchState(w.ID, watchPaused)
	case "resume":
		w, err = c.setWatchState(w.ID, watchActive)
	case "edit":
		err = c.editWatch(w, in)
		c.revokeGrants(w.ID, "the watch changed")
	case "revoke":
		c.revokeGrants(w.ID, "revoked at the user's word")
	default:
		return nil, errors.New("action is pause, resume, edit, delete, grant or revoke")
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"watch": c.viewWatch(w, 0)}, nil
}

func (c *core) editWatch(w *watch, in updateWatchIn) error {
	changed := false
	set := func(dst *string, v string) {
		if strings.TrimSpace(v) != "" {
			*dst, changed = v, true
		}
	}
	set(&w.Name, in.Name)
	set(&w.Prompt, in.Prompt)
	set(&w.Spec.Schedule, in.Schedule)
	if in.After != "" {
		after, err := parseAfter(in.After)
		if err != nil {
			return err
		}
		w.Spec.After, changed = after, true
	}
	if in.Brief != nil {
		w.Brief, changed = *in.Brief, true
	}
	if in.Every > 0 {
		w.Spec.Every, changed = in.Every, true
	}
	if in.Cond != nil {
		w.Spec.Cond, changed = in.Cond, true
	}
	if !changed {
		return errors.New("nothing to change: give a name, a schedule, after, a prompt or brief")
	}
	return c.saveWatch(w)
}

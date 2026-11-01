package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's intention tools: Form (intend) and Revise (update_intention)
// are its language work; when an intention becomes due is code's.

type intendIn struct {
	Text      string `json:"text" jsonschema:"what is owed or to remember, in French, one sentence"`
	Kind      string `json:"kind,omitempty" jsonschema:"promise (the user owes someone), commitment (you owe the user), waiting_for (someone owes the user), reminder (something to remember to do); reminder by default"`
	Owner     string `json:"owner,omitempty" jsonschema:"who owes: owner (the user), assistant (you), person:<slug>"`
	OwedTo    string `json:"owed_to,omitempty" jsonschema:"to whom: owner, person:<slug>…"`
	Subject   string `json:"subject,omitempty" jsonschema:"project:<name>, client:<slug>…"`
	DueAt     string `json:"due_at,omitempty" jsonschema:"the deadline, YYYY-MM-DD or YYYY-MM-DDTHH:MM"`
	RemindAt  string `json:"remind_at,omitempty" jsonschema:"when to bring it up; the deadline itself by default (9:00 for a day without a time)"`
	Cue       string `json:"cue,omitempty" jsonschema:"for 'the next time I talk about X': the name that brings it up (instead of a time)"`
	SourceRef string `json:"source_ref,omitempty" jsonschema:"the user's message it comes from, msg:N"`
}

type intentionsIn struct {
	State string `json:"state,omitempty" jsonschema:"open, due, snoozed, proposed, done or cancelled; the live ones by default"`
}

type updateIntentionIn struct {
	ID        int64  `json:"id"`
	State     string `json:"state,omitempty" jsonschema:"done, cancelled, snoozed (with until) or open"`
	Until     string `json:"until,omitempty" jsonschema:"for snoozed: when to bring it up again"`
	DueAt     string `json:"due_at,omitempty" jsonschema:"a new deadline (rescheduled)"`
	RemindAt  string `json:"remind_at,omitempty" jsonschema:"a new time to bring it up"`
	Text      string `json:"text,omitempty" jsonschema:"corrected text"`
	SourceRef string `json:"source_ref,omitempty" jsonschema:"the user's message asking for it, msg:N"`
	Reason    string `json:"reason,omitempty"`
}

// intentionView is an intention as the brain sees it.
type intentionView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Owner    string `json:"owner"`
	OwedTo   string `json:"owed_to,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Due      string `json:"due,omitempty"`
	Remind   string `json:"remind,omitempty"`
	Cue      string `json:"cue,omitempty"`
	State    string `json:"state"`
	Snoozed  string `json:"snoozed_until,omitempty"`
	Overdue  bool   `json:"overdue,omitempty"`
	Source   string `json:"source"`
	Trust    string `json:"trust"`
	Surfaced int    `json:"times_told,omitempty"`
}

func viewIntention(x *Intention, now time.Time) intentionView {
	day := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Local().Format("2006-01-02 15:04")
	}
	v := intentionView{ID: x.ID, Kind: x.Kind, Text: x.Text, Owner: x.Owner, OwedTo: x.OwedTo, Subject: x.Subject, Due: day(x.DueAt),
		Remind: day(x.RemindAt), Cue: x.Cue, State: x.State, Snoozed: day(x.SnoozedUntil), Overdue: x.Overdue(now),
		Source: strings.TrimSpace(x.SourceKind + " " + x.SourceRef), Trust: x.Trust, Surfaced: x.Surfaced}
	return v
}

func registerIntentionTools(s *mcp.Server, c *core) {
	tool(s, "intend", "Record an intention: a promise, a commitment of yours, something the user waits for, a reminder; with its deadline, when to bring it up, or the person whose mention brings it up. eriphe-core wakes you when it is due. What a turn that read external content records is only proposed.",
		func(ctx context.Context, in intendIn) (map[string]any, error) {
			return c.brainIntend(in)
		})
	tool(s, "intentions", "The intentions: what is owed, by whom, by when; the due ones first, the overdue marked.",
		func(ctx context.Context, in intentionsIn) (map[string]any, error) {
			if c.mem == nil {
				return nil, errMemoryOff
			}
			var states []string
			if in.State != "" {
				states = []string{in.State}
			}
			xs, err := c.mem.intentions(states...)
			if err != nil {
				return nil, err
			}
			c.readIntentions(xs)
			out := []intentionView{}
			for _, x := range xs {
				out = append(out, viewIntention(x, c.mem.now()))
			}
			return map[string]any{"note": memoryNote, "intentions": out}, nil
		})
	tool(s, "update_intention", "Close an intention (done, cancelled), snooze it until a time, give it a new deadline or reminder, or correct it.",
		func(ctx context.Context, in updateIntentionIn) (map[string]any, error) {
			return c.brainUpdateIntention(in)
		})
}

// readIntentions taints the turn that reads an intention drawn from
// external content.
func (c *core) readIntentions(xs []*Intention) {
	for _, x := range xs {
		if x.Trust == trustExternal {
			c.turn.taint(taintIntention)
			return
		}
	}
}

func (c *core) brainIntend(in intendIn) (map[string]any, error) {
	if c.mem == nil {
		return nil, errMemoryOff
	}
	x := &Intention{Kind: in.Kind, Text: in.Text, Owner: in.Owner, OwedTo: in.OwedTo, Subject: in.Subject, Cue: in.Cue}
	var err error
	if x.DueAt, err = parseWhen(in.DueAt); err != nil {
		return nil, err
	}
	if x.RemindAt, err = parseWhen(in.RemindAt); err != nil {
		return nil, err
	}
	scan := scanMemoryText(x.Text, x.Owner, x.OwedTo, x.Subject, x.Cue)
	d, err := c.decideBrainWrite(kindCommitment, in.SourceRef, scan)
	if err != nil {
		return nil, err
	}
	x.State, x.Trust, x.SourceKind, x.SourceRef, x.Flags = intentOpen, d.Trust, d.Source, d.Ref, strings.Join(scan.Flags, ",")
	if d.Status == memProposed {
		x.State = intentProposed
	}
	got, err := c.mem.addIntention(x, "brain")
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": got.ID, "state": got.State}
	if got.ID != x.ID {
		out["note"] = "Already recorded: intention " + fmt.Sprint(got.ID) + "."
	} else if got.State == intentProposed {
		out["note"] = "Only proposed: " + d.Why + ". " + c.conf().User + " approves it in the workspace (À valider)."
	}
	return out, nil
}

func (c *core) brainUpdateIntention(in updateIntentionIn) (map[string]any, error) {
	if c.mem == nil {
		return nil, errMemoryOff
	}
	old, err := c.mem.intention(in.ID)
	if err != nil {
		return nil, err
	}
	if old.State == intentProposed {
		return nil, fmt.Errorf("intention %d is proposed: only %s approves or rejects it, in the workspace", in.ID, c.conf().User)
	}
	d, err := c.decideBrainWrite(kindCommitment, in.SourceRef, scanMemoryText(in.Text, in.Reason))
	if err != nil {
		return nil, err
	}
	if d.Status != memActive {
		return nil, fmt.Errorf("not changed: %s; ask %s", d.Why, c.conf().User)
	}
	ch := intentionChange{State: strings.TrimSpace(in.State)}
	if in.Text != "" {
		ch.Text = &in.Text
	}
	if ch.Until, err = parseWhen(in.Until); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		s string
		t **time.Time
	}{{in.DueAt, &ch.DueAt}, {in.RemindAt, &ch.RemindAt}} {
		if f.s == "" {
			continue
		}
		t, err := parseWhen(f.s)
		if err != nil {
			return nil, err
		}
		*f.t = &t
	}
	if ch.State == "" && ch.Text == nil && ch.DueAt == nil && ch.RemindAt == nil {
		return nil, errors.New("nothing to change: give a state, a time or a text")
	}
	x, err := c.mem.changeIntention(in.ID, ch, "brain", strings.TrimSpace(in.Reason))
	if err != nil {
		return nil, err
	}
	return map[string]any{"intention": viewIntention(x, c.mem.now())}, nil
}

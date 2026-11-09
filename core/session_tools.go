package main

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's tools for work sessions (sessions.go): it opens them with
// the user and, until a session has a lead of its own, leads them.

const sessionNote = "The user approves the plan on a card in the conversation; the planned steps then start without asking again. You are told of each step as an event [session N « title »]: judge it with settle_step."

type sessionIDIn struct {
	Session int64 `json:"session_id"`
}

type settleIn struct {
	Session int64  `json:"session_id"`
	Step    string `json:"step" jsonschema:"the step's key"`
	Verdict string `json:"verdict" jsonschema:"accept (the work holds, on its evidence), retry (say what to fix in note) or reject"`
	Note    string `json:"note,omitempty" jsonschema:"why, in a sentence; for a retry, what to fix: the agent reads it"`
	Output  string `json:"output,omitempty" jsonschema:"accept: what the step produced, for the steps after it, when its message is not enough"`
}

type amendSessionIn struct {
	Session int64      `json:"session_id"`
	Why     string     `json:"why" jsonschema:"why the plan changes, in a sentence the user reads"`
	Add     []stepSpec `json:"add,omitempty" jsonschema:"steps to add, as start_session's; each added step asks its own start card"`
	Drop    []string   `json:"drop,omitempty" jsonschema:"keys of steps to take out, that have not started"`
}

type chooseIn struct {
	Session int64  `json:"session_id"`
	Step    string `json:"step" jsonschema:"the choice step's key"`
	Option  string `json:"option" jsonschema:"the option the user picked, as listed"`
}

type endIn struct {
	Session int64  `json:"session_id"`
	Summary string `json:"summary" jsonschema:"what the session did and where its results are, for the user, in their language"`
}

type sessionsIn struct {
	All bool `json:"all,omitempty" jsonschema:"include the sessions that ended"`
}

// sessionView is a session with its plan, what it asks and its log.
type sessionView struct {
	Session
	Steps []Step        `json:"steps"`
	Log   []SessionLine `json:"log"`
	Asks  int           `json:"cards_waiting,omitempty"`
}

func (c *core) sessionView(id int64, lines int) (sessionView, error) {
	s, err := c.session(id)
	if err != nil {
		return sessionView{}, err
	}
	steps, err := c.steps(id)
	if err != nil {
		return sessionView{}, err
	}
	v := sessionView{Session: s, Steps: steps, Log: c.sessionLog(id, lines)}
	c.store.db.QueryRow(`SELECT COUNT(*) FROM session_asks WHERE session = ? AND state = ?`, id, askPending).Scan(&v.Asks)
	return v, nil
}

func registerSessionTools(s *mcp.Server, c *core) {
	tool(s, "start_session", "Open a work session: work of several steps toward one goal (rebuild a service, find a brand name, make a video), not only code. Give its goal, done_when (what must be true at the end), a budget of minutes, and the plan: steps with a key, a title, a kind (task, choice, approval) and the keys of the steps they come after. Independent steps run in parallel, never two writing the same repository branch or folder; each task step is given the outputs of the steps it comes after. "+sessionNote+" For one task alone, use run_task.",
		func(ctx context.Context, in sessionIn) (map[string]any, error) {
			for _, st := range in.Steps {
				if st.Kind == "" || st.Kind == stepTask {
					if err := c.taintedResources(runRequest{Resources: st.Resources}); err != nil {
						return nil, err
					}
				}
			}
			s, err := c.openSession(in, c.turnTaint(), byBrain)
			if err != nil {
				return nil, err
			}
			return map[string]any{"session": s, "note": "Only proposed: " + sessionNote + " Never say it started before the user approves."}, nil
		})

	tool(s, "sessions", "The work sessions: the active ones (proposed, running, paused), and those that ended with all.",
		func(ctx context.Context, in sessionsIn) (map[string]any, error) {
			ss, err := c.sessions(!in.All, 30)
			return map[string]any{"sessions": ss}, err
		})

	tool(s, "session_status", "A work session: its state, budget and time spent, each step (status: waiting, running, reported: its agent says it is done, verified: its checks passed, accepted, rejected, blocked, dropped; its task, evidence and output) and its recent log.",
		func(ctx context.Context, in sessionIDIn) (sessionView, error) {
			v, err := c.sessionView(in.Session, 20)
			if err == nil {
				for _, st := range v.Steps {
					if t, err := c.store.task(st.Task); err == nil && t.Folders != "" {
						c.turn.taint(folderTaint(t.Folders)) // what it says comes from the user's files
					}
				}
			}
			return v, err
		})

	tool(s, "settle_step", "Judge a step of a session that reported its work or stopped, on its evidence: accept, retry (at most 2 per step; say what to fix) or reject. A report is only a claim: a step whose checks failed cannot be accepted. Accepting lets the steps after it start.",
		func(ctx context.Context, in settleIn) (Step, error) {
			return c.settleStep(ctx, in.Session, in.Step, in.Verdict, in.Note, in.Output, "brain")
		})

	tool(s, "amend_session", "Change a session's plan: add steps (as start_session's), drop steps that have not started. Twice without asking; past that the user is asked on a card. A step added asks its own start card, unless the user approved the change.",
		func(ctx context.Context, in amendSessionIn) (map[string]any, error) {
			for _, st := range in.Add {
				if st.Kind == "" || st.Kind == stepTask {
					if err := c.taintedResources(runRequest{Resources: st.Resources}); err != nil {
						return nil, err
					}
				}
			}
			note, err := c.amendSession(ctx, in.Session, amendIn{Add: in.Add, Drop: in.Drop}, in.Why, "brain")
			return map[string]any{"note": note}, err
		})

	tool(s, "choose_step", "Record the user's pick for a session's choice step, as they said it in this turn.",
		func(ctx context.Context, in chooseIn) (Step, error) {
			if !c.turnFromUser() {
				return Step{}, errors.New("a choice is the user's: record it in the turn of their answer")
			}
			return c.chooseStep(ctx, in.Session, in.Step, in.Option, "user")
		})

	tool(s, "end_session", "Close a session once every step is accepted, rejected, dropped or blocked, with a summary for the user. Check the goal's done_when first.",
		func(ctx context.Context, in endIn) (map[string]any, error) {
			if err := c.endSession(in.Session, in.Summary, "brain"); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		})

	tool(s, "stop_session", "Stop a session the user wants stopped: its running tasks stop, its cards are withdrawn.",
		func(ctx context.Context, in sessionIDIn) (map[string]any, error) {
			if err := c.stopSession(ctx, in.Session, "brain"); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		})
}

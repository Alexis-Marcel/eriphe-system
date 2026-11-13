package main

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's tools for threads (threads.go): it opens them, finds them
// again by their subject, closes them on their goal and reopens them; a
// light one it holds itself, a led one it opens with the user and, until
// it has a lead of its own, leads.

const threadNote = "The user approves the plan on a card in the conversation; the planned steps then start without asking again, and the thread gets its lead, a temporary assistant that judges each step and reports to you what the user must know or decide. Without one, you lead it: each step reaches you as an event [thread N « title »], judged with settle_step."

type threadIDIn struct {
	Thread int64 `json:"thread_id"`
}

type settleIn struct {
	Thread  int64  `json:"thread_id"`
	Step    string `json:"step" jsonschema:"the step's key"`
	Verdict string `json:"verdict" jsonschema:"accept (the work holds, on its evidence), retry (say what to fix in note) or reject"`
	Note    string `json:"note,omitempty" jsonschema:"why, in a sentence; for a retry, what to fix: the agent reads it"`
	Output  string `json:"output,omitempty" jsonschema:"accept: what the step produced, for the steps after it, when its message is not enough"`
}

type amendThreadIn struct {
	Thread int64      `json:"thread_id"`
	Why    string     `json:"why" jsonschema:"why the plan changes, in a sentence the user reads"`
	Add    []stepSpec `json:"add,omitempty" jsonschema:"steps to add, as open_thread's; each added step asks its own start card"`
	Drop   []string   `json:"drop,omitempty" jsonschema:"keys of steps to take out, that have not started"`
}

type chooseIn struct {
	Thread int64  `json:"thread_id"`
	Step   string `json:"step" jsonschema:"the choice step's key"`
	Option string `json:"option" jsonschema:"the option the user picked, as listed"`
}

type closeIn struct {
	Thread  int64  `json:"thread_id"`
	Summary string `json:"summary" jsonschema:"how its goal was reached and where its results are, for the user, in their language: the reason it closes"`
}

type reopenIn struct {
	Thread int64  `json:"thread_id"`
	Why    string `json:"why" jsonschema:"what brings its subject back, in a sentence"`
}

type tellLeadIn struct {
	Thread int64  `json:"thread_id"`
	Text   string `json:"text" jsonschema:"what the user said or wants, in French"`
}

type addStepIn struct {
	Thread int64    `json:"thread_id"`
	Step   stepSpec `json:"step" jsonschema:"the step: key, title, kind, the keys it comes after, and what its kind needs"`
}

type linkThreadIn struct {
	Thread  int64  `json:"thread_id"`
	Ref     string `json:"ref,omitempty" jsonschema:"what to link: intention:<id>, or a card's ref (kind:id)"`
	Summary string `json:"summary,omitempty" jsonschema:"where the thread stands, for the user, in their language: from its subject's progress memory and its steps"`
}

type findThreadIn struct {
	Subject string `json:"subject,omitempty" jsonschema:"the ref of the matter, as given at its opening: project:site, pr:owner/repo#12"`
	Text    string `json:"text,omitempty" jsonschema:"words of its title, goal, subject or summary"`
	All     bool   `json:"all,omitempty" jsonschema:"without subject nor text: the ended ones too (with them, they are always included)"`
}

// threadView is a thread with its plan, what it asks and its log.
type threadView struct {
	Thread
	Steps []Step       `json:"steps"`
	Log   []ThreadLine `json:"log"`
	Asks  int          `json:"cards_waiting,omitempty"`
}

func (c *core) threadView(id int64, lines int) (threadView, error) {
	s, err := c.thread(id)
	if err != nil {
		return threadView{}, err
	}
	steps, err := c.steps(id)
	if err != nil {
		return threadView{}, err
	}
	v := threadView{Thread: s, Steps: steps, Log: c.threadLog(id, lines)}
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_asks WHERE thread = ? AND state = ?`, id, askPending).Scan(&v.Asks)
	return v, nil
}

func registerThreadTools(s *mcp.Server, c *core) {
	tool(s, "open_thread", "Open a thread: one matter followed to its real goal (a pull request until merged and deployed, an appointment, a brand name to find). Look for one first with find_thread: a matter has one thread. Light by default (no steps): it opens at once, with no card and no budget, and you hold it. Led (with steps): long autonomous work of several steps toward the goal, not only code; give done_when (what must be true at the end), a budget of minutes, and the plan: steps with a key, a title, a kind (task, call, user, choice, approval, check, deadline: as add_step's) and the keys of the steps they come after. Independent steps run in parallel, never two writing the same repository branch or folder; each task step is given the outputs of the steps it comes after. "+threadNote+" For one task alone, use run_task.",
		func(ctx context.Context, in threadIn) (map[string]any, error) {
			for _, st := range in.Steps {
				if st.Kind == "" || st.runsTask() {
					if err := c.taintedResources(runRequest{Resources: st.Resources}); err != nil {
						return nil, err
					}
				}
			}
			s, err := c.openThread(in, c.turnTaint(), byBrain)
			if err != nil {
				return nil, err
			}
			c.turnThread(s.ID) // what the turn raises next is the thread's (thread_turn.go)
			if !s.led() {
				return map[string]any{"thread": s, "note": "Open. Close it with close_thread only once its goal is reached."}, nil
			}
			return map[string]any{"thread": s, "note": "Only proposed: " + threadNote + " Never say it started before the user approves."}, nil
		})

	tool(s, "find_thread", "Find threads: by the subject they follow (its ref) or by words; without either, the open and waiting ones. Ended ones are listed too (done, dropped), to reopen one when its subject comes back.",
		func(ctx context.Context, in findThreadIn) (map[string]any, error) {
			ss, err := c.findThreads(in.Subject, in.Text, in.All, 30)
			return map[string]any{"threads": ss}, err
		})

	tool(s, "read_thread", "A thread: its mode, subject, state (open, waiting, done, dropped) and summary; led, its phase, budget and time spent, each step (status: waiting, running, reported: its agent says it is done, verified: its checks passed, accepted, rejected, blocked, dropped; its task, evidence and output) and its recent log.",
		func(ctx context.Context, in threadIDIn) (threadView, error) {
			v, err := c.threadView(in.Thread, 20)
			if err == nil {
				c.turnThread(in.Thread)
				for _, st := range v.Steps {
					if st.Tainted != "" {
						c.turn.taint(st.Tainted) // a call's result, an external intention
					}
					if st.Task == "" {
						continue
					}
					if why := c.taskResultTaint(st.Task); why != "" {
						c.turn.taint(why) // what it says may carry what it read (task_taint.go)
					}
				}
			}
			return v, err
		})

	tool(s, "settle_step", "Settle a step of a thread you hold. A task that reported its work or stopped, on its evidence: accept, retry (at most 2 per step; say what to fix) or reject; a report is only a claim: a step whose checks failed cannot be accepted. A user step you see done: accept, with what shows it in note. A deadline no longer to wait for: accept or reject. A call, a check, a choice or an approval settle themselves. Accepting lets the steps after it start.",
		func(ctx context.Context, in settleIn) (Step, error) {
			if err := c.holderOnly(in.Thread); err != nil {
				return Step{}, err
			}
			c.turnThread(in.Thread)
			return c.settleStep(ctx, in.Thread, in.Step, in.Verdict, in.Note, in.Output, "brain")
		})

	tool(s, "add_step", "Add a step to a light thread you hold, as what you find shows it is needed: its steps are born as it goes. It starts at once once the steps it comes after are accepted. Kinds: task (an agent works on it; it asks its start card if it is lent what needs a yes), call (one of your tools with its input: what changes something asks its card, the step's yes, and its run is followed; a read your policy leaves free runs at once), user (what only the user does, in question: they answer « C'est fait »), choice (ask them, record with choose_step), approval (a yes or no card), check (a task or a read call that verifies, with expect: its outcome settles it), deadline (a date in due: the thread wakes then). Each change of a step you did not make reaches you as an event [thread N « title »]. A led thread's plan changes with amend_thread.",
		func(ctx context.Context, in addStepIn) (Step, error) {
			if in.Step.Kind == "" || in.Step.runsTask() {
				if err := c.taintedResources(runRequest{Resources: in.Step.Resources}); err != nil {
					return Step{}, err
				}
			}
			c.turnThread(in.Thread)
			return c.addStep(ctx, in.Thread, in.Step, c.turnTaint(), "brain")
		})

	tool(s, "link_thread", "Join to a thread what relates to it: an intention (ref intention:<id>) becomes a deadline step, due when it is; a card (its ref, kind:id) shows in its timeline. And/or set its summary: where it stands, from its subject's progress memory and its steps.",
		func(ctx context.Context, in linkThreadIn) (map[string]any, error) {
			c.turnThread(in.Thread)
			note, err := c.linkThread(ctx, in.Thread, in.Ref, in.Summary, "brain")
			return map[string]any{"note": note}, err
		})

	tool(s, "amend_thread", "Change a thread's plan: add steps (as open_thread's), drop steps that have not started. Twice without asking; past that the user is asked on a card. A step added asks its own start card, unless the user approved the change.",
		func(ctx context.Context, in amendThreadIn) (map[string]any, error) {
			for _, st := range in.Add {
				if st.Kind == "" || st.runsTask() {
					if err := c.taintedResources(runRequest{Resources: st.Resources}); err != nil {
						return nil, err
					}
				}
			}
			if err := c.leadOnly(in.Thread); err != nil {
				return nil, err
			}
			c.turnThread(in.Thread)
			note, err := c.amendThread(ctx, in.Thread, amendIn{Add: in.Add, Drop: in.Drop}, in.Why, "brain")
			return map[string]any{"note": note}, err
		})

	tool(s, "choose_step", "Record the user's pick for a thread's choice step, as they said it in this turn.",
		func(ctx context.Context, in chooseIn) (Step, error) {
			if !c.turnFromUser() {
				return Step{}, errors.New("a choice is the user's: record it in the turn of their answer")
			}
			c.turnThread(in.Thread)
			return c.chooseStep(ctx, in.Thread, in.Step, in.Option, "user")
		})

	tool(s, "close_thread", "Close a thread once its real goal is reached, never before: the summary says how. A light one closes at once, done. A led one you lead, once every step is accepted, rejected, dropped or blocked; with a done_when, a fresh agent checks the objective first, and you are told of its verdict. To give up instead, stop_thread.",
		func(ctx context.Context, in closeIn) (map[string]any, error) {
			s, err := c.thread(in.Thread)
			if err != nil {
				return nil, err
			}
			if !s.led() {
				if err := c.closeLight(in.Thread, in.Summary, "brain"); err != nil {
					return nil, err
				}
				return map[string]any{"note": "Closed."}, nil
			}
			if err := c.leadOnly(in.Thread); err != nil {
				return nil, err
			}
			note, err := c.finishThread(ctx, in.Thread, in.Summary, "brain")
			return map[string]any{"note": note}, err
		})

	tool(s, "reopen_thread", "Reopen a thread that ended (done or dropped) when its subject comes back: it opens again, light, held by you, with its history.",
		func(ctx context.Context, in reopenIn) (Thread, error) {
			c.turnThread(in.Thread)
			return c.reopenThread(in.Thread, in.Why, "brain")
		})

	tool(s, "tell_lead", "Tell a thread's lead what the user wants (a change of direction, an answer it asked for): it reads it at its next wake. For a thread with a lead of its own.",
		func(ctx context.Context, in tellLeadIn) (map[string]any, error) {
			if err := c.tellLeadFromBrain(in.Thread, in.Text, c.turnTaint()); err != nil {
				return nil, err
			}
			c.turnThread(in.Thread)
			return map[string]any{"note": "Passed to the lead."}, nil
		})

	tool(s, "stop_thread", "Stop a thread the user wants stopped, or that will not reach its goal: it is dropped; a led one's running tasks stop and its cards are withdrawn.",
		func(ctx context.Context, in threadIDIn) (map[string]any, error) {
			if err := c.stopThread(ctx, in.Thread, "brain"); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		})
}

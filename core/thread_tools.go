package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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
	Thread int64  `json:"thread_id"`
	Ref    string `json:"ref,omitempty" jsonschema:"what to link: intention:<id>, or a card's ref (kind:id)"`
	// Source makes the thread carry live data (thread_sources.go).
	Source *threadSource `json:"source,omitempty" jsonschema:"instead of ref: live data the thread carries, read by code without a model: a free read of yours (tool, args), a resource (resource, uri) or a monitor the user approved (monitor); with items, each item it lists shows on the thread. The same name replaces it; a name alone takes it away"`
}

type findThreadIn struct {
	Subject string `json:"subject,omitempty" jsonschema:"the subject of the matter (kind:slug), as given at its opening: project:site, pr:owner/repo#12"`
	Text    string `json:"text,omitempty" jsonschema:"words of its title, goal, subject or summary"`
	All     bool   `json:"all,omitempty" jsonschema:"without subject nor text: the ended ones too (with them, they are always included)"`
}

// threadView is a thread with its plan, what it asks and its log.
type threadView struct {
	Thread
	Steps []Step       `json:"steps"`
	Log   []ThreadLine `json:"log"`
	Asks  int          `json:"cards_waiting,omitempty"`
	Notes []itemNote   `json:"notes,omitempty"` // on its sources' items (thread_items.go)
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
	v := threadView{Thread: s, Steps: steps, Log: c.threadLog(id, lines), Notes: c.itemNotes(id)}
	c.store.db.QueryRow(`SELECT COUNT(*) FROM thread_asks WHERE thread = ? AND state = ?`, id, askPending).Scan(&v.Asks)
	return v, nil
}

// endsAgain makes the thread a subject already has end as the brain asks
// now (open_thread's ends), should it ask another: a light one only.
func (c *core) endsAgain(old Thread, ends string) error {
	ends = strings.ToLower(strings.TrimSpace(ends))
	if ends == "" || ends == old.Ends {
		return nil
	}
	if !slices.Contains(threadEnds, ends) {
		return fmt.Errorf("ends %q: goal, standing or day", ends)
	}
	if old.led() {
		return fmt.Errorf("thread %d « %s » follows this subject and is led: it ends on its goal", old.ID, old.Title)
	}
	fields := map[string]any{"ends": ends}
	if ends == endsDay {
		fields["started"] = time.Now().Unix() // its day is today's
	}
	return c.setThread(old.ID, fields)
}

// sameSubject is why a thread opened on a subject that had one is that
// one again: a matter has one thread.
const sameSubject = "même sujet"

// openThreadOnce opens a thread, one per matter, for the brain or a task
// lent eriphe-threads (by: skill:<name>, task:<id>): a light one needs its
// subject, and a subject that has a thread already gives that thread back
// (reopened light if it ended) rather than a second one; again says so. A
// led one on the subject of an active thread is refused: that thread's
// plan changes instead. Only the brain changes how a thread found ends.
func (c *core) openThreadOnce(in threadIn, taint []string, by string) (s Thread, again string, err error) {
	led := strings.EqualFold(strings.TrimSpace(in.Mode), modeLed) || (strings.TrimSpace(in.Mode) == "" && len(in.Steps) > 0)
	subject, err := c.subjectLookup(in.Subject)
	if err != nil {
		return s, "", fmt.Errorf("subject: %w", err)
	}
	in.Subject = subject
	if subject == "" {
		if !led {
			return s, "", errors.New("subject is needed: the subject of the matter, kind:slug, to find its thread again (pr:owner/repo#12, project:site, topic:brand-name)")
		}
		s, err = c.openThread(in, taint, by)
		return s, "", err
	}
	found, err := c.findThreads(subject, "", true, 1) // active first, then the latest
	if err != nil {
		return s, "", err
	}
	if len(found) == 0 {
		s, err = c.openThread(in, taint, by)
		return s, "", err
	}
	old := found[0]
	if by == byBrain {
		if err := c.endsAgain(old, in.Ends); err != nil {
			return s, "", err
		}
	}
	switch {
	case led && old.active():
		return s, "", fmt.Errorf("thread %d « %s » follows %s already: change its plan (add_step, amend_thread) rather than open another", old.ID, old.Title, old.Subject)
	case led:
		s, err = c.openThread(in, taint, by)
		return s, "", err
	case old.active():
		return old, fmt.Sprintf("Not a new thread: thread %d « %s » follows this subject already, and it is the one to go on with (its steps: read_thread).", old.ID, old.Title), nil
	}
	s, err = c.reopenThread(old.ID, sameSubject, by)
	if err != nil {
		return s, "", err
	}
	return s, fmt.Sprintf("Not a new thread: thread %d « %s » followed this subject and had ended; it is reopened, light, with its history (read_thread).", s.ID, s.Title), nil
}

func registerThreadTools(s *mcp.Server, c *core) {
	tool(s, "open_thread", "Open a thread: one matter followed to its real goal (a pull request until merged and deployed, an appointment, a brand name to find). A matter has one thread: give its subject (required for a light one); a subject that has a thread already gives that thread back, reopened if it ended, instead of a second one. Light by default (no steps): it opens at once, with no card and no budget, and you hold it. Led (with steps): long autonomous work of several steps toward the goal, not only code; give done_when (what must be true at the end), a budget of minutes, and the plan: steps with a key, a title, a kind (task, call, user, choice, approval, check, deadline: as add_step's) and the keys of the steps they come after. Independent steps run in parallel, never two writing the same repository branch or folder; each task step is given the outputs of the steps it comes after. "+threadNote+" For one task alone, use run_task.",
		func(ctx context.Context, in threadIn) (map[string]any, error) {
			for _, st := range in.Steps {
				if st.Kind == "" || st.runsTask() {
					if err := c.taintedResources(runRequest{Resources: st.Resources}); err != nil {
						return nil, err
					}
				}
			}
			s, again, err := c.openThreadOnce(in, c.turnTaint(), byBrain)
			if err != nil {
				return nil, err
			}
			c.turnThread(s.ID) // what the turn raises next is the thread's (thread_turn.go)
			if again != "" {
				return map[string]any{"thread": s, "note": again}, nil
			}
			if !s.led() {
				return map[string]any{"thread": s, "note": "Open. Close it with close_thread only once its goal is reached."}, nil
			}
			return map[string]any{"thread": s, "note": "Only proposed: " + threadNote + " Never say it started before the user approves."}, nil
		})

	tool(s, "find_thread", "Find threads: by words of their title, goal, subject or summary (when you do not know the subject's ref), or by the subject they follow; without either, the open and waiting ones. Ended ones are listed too (done, dropped), to reopen one when its subject comes back.",
		func(ctx context.Context, in findThreadIn) (map[string]any, error) {
			ss, err := c.findThreads(in.Subject, in.Text, in.All, 30)
			return map[string]any{"threads": ss}, err
		})

	tool(s, "read_thread", "A thread: its mode, subject, state (open, waiting, done, dropped) and summary; led, its phase, budget and time spent, each step (status: waiting, running, reported: its agent says it is done, verified: its checks passed, accepted, rejected, blocked, dropped; its task, evidence and output) and its recent log.",
		func(ctx context.Context, in threadIDIn) (threadView, error) {
			v, err := c.threadView(in.Thread, 20)
			if err == nil {
				c.turnThread(in.Thread)
				for _, why := range readNotesTaint(v.Notes) {
					c.turn.taint(why) // a note written after reading outside content
				}
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

	tool(s, "settle_step", "Settle a step of a thread you hold. A task that reported its work or stopped, on its evidence: accept, retry (at most 2 per step; say what to fix) or reject; a report is only a claim: a step whose checks failed cannot be accepted. A user step you see done, or the user tells you in the conversation is done: accept, with what shows it in note. A deadline no longer to wait for: accept or reject. A call, a check, a choice or an approval settle themselves. Accepting lets the steps after it start.",
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

	tool(s, "link_thread", "Join to a thread what eriphe-core does not link itself, an intention of another subject (ref intention:<id>: a deadline step, due when it is) or a card raised outside a turn about the thread (kind:id: in its timeline); or, with source, live data the thread carries, read by code without a model: what it is about (the user's mail, their agenda, pull requests to review: a free read of yours or a resource, with items to show each one it lists) or a monitor the user approved (a latency, an error rate: its latest number and its events, a limit crossed raises the thread; a skill still reacts to its events). Its value is at /<name> in the thread's surface's data, for your part to bind. An intention of the thread's subject and a card born in a turn about it are linked already; where it stands is remember kind progress.",
		func(ctx context.Context, in linkThreadIn) (map[string]any, error) {
			c.turnThread(in.Thread)
			if in.Source != nil {
				if in.Ref != "" {
					return nil, errors.New("one of ref or source")
				}
				src := *in.Source
				if src.Tool == "" && src.Resource == "" && src.Monitor == "" {
					note, err := c.dropThreadSource(in.Thread, strings.TrimSpace(src.Name), "brain")
					return map[string]any{"note": note}, err
				}
				note, err := c.addThreadSource(in.Thread, src, "brain")
				return map[string]any{"note": note}, err
			}
			note, err := c.linkThread(ctx, in.Thread, in.Ref, "brain")
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

	tool(s, "note_item", "Note one item a thread's source lists (a mail, an event, a pull request), by the source's name and the item's id: what the user should know of it, a state (act, look, info, none, done) and a due date. Code shows it on the item's row; it stays while the source lists the item, and goes some days after it no longer does. A note written after reading external content is marked so and never quoted into a summary. The same item noted again replaces it; no note, state nor due takes it away.",
		func(ctx context.Context, in noteItemIn) (map[string]any, error) {
			c.turnThread(in.Thread)
			note, err := c.noteItem(in.Thread, in, byBrain, c.turnTaint())
			return map[string]any{"note": note}, err
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

	tool(s, "off_thread", "Say this turn is about no thread: the user wrote with a thread's surface open (or one of its subject's) but asks about something else (« j'ai quoi de prévu aujourd'hui ? » from a pull request's thread). Read what you need first, then call it right before the first card or task: what the turn raises next is no longer that thread's. Any thread tool called after (read_thread too) names a thread again. Not for a question about the thread open: answer that one in it.",
		func(ctx context.Context, _ struct{}) (map[string]any, error) {
			return c.offThread(), nil
		})

	tool(s, "stop_thread", "Stop a thread the user wants stopped, or that will not reach its goal: it is dropped; a led one's running tasks stop and its cards are withdrawn.",
		func(ctx context.Context, in threadIDIn) (map[string]any, error) {
			if err := c.stopThread(ctx, in.Thread, "brain"); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		})
}

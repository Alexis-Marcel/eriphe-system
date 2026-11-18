package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The brain's tools for threads (threads.go): it opens them, finds them
// again by their names, closes them on their goal and reopens them. Work
// cut in steps runs as the steps of a node (node_steps.go).

type threadIDIn struct {
	Thread int64 `json:"thread_id"`
}

type closeIn struct {
	Thread  int64  `json:"thread_id"`
	Summary string `json:"summary" jsonschema:"how its goal was reached and where its results are, for the user, in their language: the reason it closes"`
}

type reopenIn struct {
	Thread int64  `json:"thread_id"`
	Why    string `json:"why" jsonschema:"what brings it back, in a sentence"`
}

type linkThreadIn struct {
	Thread int64  `json:"thread_id"`
	Ref    string `json:"ref" jsonschema:"what to link: a card's ref (kind:id)"`
}

type findThreadIn struct {
	Alias string `json:"alias,omitempty" jsonschema:"a key of the matter (kind:slug: project:site, pr:owner/repo#12) or a name the user gives its thread"`
	Text  string `json:"text,omitempty" jsonschema:"words of its title, goal, names or summary"`
	All   bool   `json:"all,omitempty" jsonschema:"without alias nor text: the ended ones too (with them, they are always included)"`
}

// threadView is a thread with its log.
type threadView struct {
	Thread
	Log   []ThreadLine `json:"log"`
	Names []string     `json:"names,omitempty"` // its keys and names (names.go)
	Card  string       `json:"card,omitempty"`  // what is known of it (thread_card.go), read_thread's
	// Its parent's title, and its children (thread_children.go).
	ParentTitle string      `json:"parent_title,omitempty"`
	Children    []threadKin `json:"children,omitempty"`
}

func (c *core) threadView(id int64, lines int) (threadView, error) {
	s, err := c.thread(id)
	if err != nil {
		return threadView{}, err
	}
	v := threadView{Thread: s, Log: c.threadLog(id, lines), Names: c.threadNames(id), Children: kinOf(c.children(id))}
	if s.Parent != 0 {
		if p, err := c.thread(s.Parent); err == nil {
			v.ParentTitle = p.Title
		}
	}
	return v, nil
}

// endsChange is how the brain asks the thread an alias has already to end
// now (open_thread's ends): "" for no change; never standing for a
// child.
func endsChange(old Thread, ends string) (string, error) {
	ends = strings.ToLower(strings.TrimSpace(ends))
	if ends == "" || ends == old.Ends {
		return "", nil
	}
	if !slices.Contains(threadEnds, ends) {
		return "", fmt.Errorf("ends %q: goal or standing", ends)
	}
	if ends == endsStanding && old.Parent != 0 {
		return "", fmt.Errorf("thread %d « %s » is a child of thread %d: a standing thread is a top thread, never a child", old.ID, old.Title, old.Parent)
	}
	return ends, nil
}

// endsAgain makes the thread an alias has already end as the brain asks
// now (endsChange).
func (c *core) endsAgain(old Thread, ends string) error {
	ends, err := endsChange(old, ends)
	if err != nil || ends == "" {
		return err
	}
	return c.setThread(old.ID, map[string]any{"ends": ends})
}

// sameMatter is why a thread opened on the alias of another is that one
// again: a matter has one thread.
const sameMatter = "même chose"

// openThreadOnce opens a thread, one per matter, for the brain or code
// (by); it is the only path
// that opens one. It needs its alias, and an alias that has a thread
// already gives that thread back (reopened if it ended)
// rather than a second one; again says so. Without one, threads near the
// matter (nearThreads, ended ones too) refuse it, listed (*nearMatch),
// until the opener takes one (take: given back, and given the alias
// unless the opener read external content) or says why this is another
// matter (new_because, logged). A skill's run has no model to answer: its
// thread is its alias's alone (exact). Only the brain changes how a
// thread found ends.
func (c *core) openThreadOnce(in threadIn, taint []string, by string) (s Thread, again string, err error) {
	alias, err := aliasKey(in.Alias)
	if err != nil {
		return s, "", fmt.Errorf("alias: %w", err)
	}
	in.Alias = alias
	if alias == "" {
		return s, "", errors.New("alias is needed: the key of the matter, kind:slug, to find its thread again (pr:owner/repo#12, project:site, topic:brand-name)")
	}
	if in.Parent != 0 {
		if _, err := c.parentFor(in.Parent, Thread{Ends: in.Ends}); err != nil {
			return s, "", err
		}
	}
	var old Thread
	found, taken := false, false
	if alias != "" {
		if id, ok := c.aliasThread(alias); ok {
			if th, err := c.thread(id); err == nil {
				old, found = th, true
			}
		}
	}
	if !found && !in.exact {
		// Its parent is not the same matter as its child.
		near := slices.DeleteFunc(c.nearThreads(alias, in.Title), func(x Thread) bool { return in.Parent != 0 && x.ID == in.Parent })
		why := oneLine(in.NewBy)
		switch {
		case in.Take > 0:
			i := slices.IndexFunc(near, func(x Thread) bool { return x.ID == in.Take })
			if i < 0 {
				return s, "", fmt.Errorf("take: thread %d is not one of the threads near this matter; find_thread, or open it with new_because", in.Take)
			}
			old, found, taken = near[i], true, true
		case len(near) > 0 && why == "":
			return s, "", &nearMatch{near}
		case len(near) > 0:
			s, err = c.openThread(in, taint, by)
			if err == nil {
				var refs []string
				for _, x := range near {
					refs = append(refs, strconv.FormatInt(x.ID, 10))
				}
				c.logThread(s.ID, by, fmt.Sprintf("Ouvert à côté des threads %s : %s", strings.Join(refs, ", "), why))
			}
			return s, "", err
		}
	}
	if !found {
		s, err = c.openThread(in, taint, by)
		return s, "", err
	}
	moved := ""
	if in.Parent != 0 && old.Parent != in.Parent {
		// Found again under the parent its opener names: it moves there,
		// once all that could refuse the open said yes.
		as := old
		as.Parent = in.Parent
		if by == byBrain {
			if ends, err := endsChange(as, in.Ends); err != nil {
				return s, "", err
			} else if ends != "" {
				as.Ends = ends
			}
		}
		if _, err := c.parentFor(in.Parent, as); err != nil {
			return s, "", err
		}
		if err := c.moveUnder(old, in.Parent, by); err != nil {
			return s, "", err
		}
		old.Parent = in.Parent
		moved = fmt.Sprintf(" It is now a child of thread %d.", in.Parent)
	}
	s, again, err = c.matterThread(old, in, by)
	again += moved
	if err != nil || !taken || alias == "" {
		return s, again, err
	}
	if len(taint) > 0 {
		// A key drawn from someone else's words never binds their matter to
		// a thread: the thread is given back, the alias stays free.
		return s, again + " This turn read external content: the alias is not given to it.", nil
	}
	if err := c.giveAlias(s.ID, alias); err != nil {
		return s, again + " The alias is not given to it: " + err.Error(), nil
	}
	return s, again, nil
}

// matterThread gives back the thread that follows a matter already: as it
// is when active, reopened when it ended.
func (c *core) matterThread(old Thread, in threadIn, by string) (s Thread, again string, err error) {
	if by == byBrain {
		if err := c.endsAgain(old, in.Ends); err != nil {
			return s, "", err
		}
	}
	switch {
	case old.active():
		return old, fmt.Sprintf("Not a new thread: thread %d « %s » follows this matter already, and it is the one to go on with (read_thread).", old.ID, old.Title), nil
	}
	s, err = c.reopenThread(old.ID, sameMatter, by)
	if err != nil {
		return s, "", err
	}
	return s, fmt.Sprintf("Not a new thread: thread %d « %s » followed this matter and had ended; it is reopened, with its history (read_thread).", s.ID, s.Title), nil
}

func registerThreadTools(s *mcp.Server, c *core) {
	tool(s, "open_thread", "Open a thread: one matter followed to its real goal (a pull request until merged and deployed, an appointment, a brand name to find). A matter has one thread: give its alias, the matter's key (required); an alias that has a thread already gives that thread back, reopened if it ended, instead of a second one. When threads near the matter exist (ended ones too), it is refused and lists them: open again with take (the one that is it: given back, reopened if it ended, and given the alias) or new_because (why it is another matter). It opens at once, with no card, and you hold it. With parent, a child thread: a step of that thread that became a matter of its own (find_thread or read_thread on the parent first: its children are listed, take the one that exists). For one task alone, use run_task.",
		func(ctx context.Context, in threadIn) (map[string]any, error) {
			s, again, err := c.openThreadOnce(in, c.turnTaint(), byBrain)
			if err != nil {
				return nil, err
			}
			c.turnThread(s.ID) // what the turn raises next is the thread's (thread_turn.go)
			if again != "" {
				return map[string]any{"thread": s, "note": again}, nil
			}
			return map[string]any{"thread": s, "note": "Open. Close it with close_thread only once its goal is reached."}, nil
		})

	tool(s, "find_thread", "Find threads: by words of their title, goal, names or summary, or by an alias (kind:slug) or a name; without either, the open and waiting ones. Ended ones are listed too (done, dropped), to reopen one when its matter comes back. Each says its parent, and its children (id, title, state).",
		func(ctx context.Context, in findThreadIn) (map[string]any, error) {
			ss, err := c.findThreads(in.Alias, in.Text, in.All, 30)
			found := make([]threadFound, 0, len(ss))
			for _, s := range ss {
				found = append(found, threadFound{Thread: s, Children: kinOf(c.children(s.ID))})
			}
			return map[string]any{"threads": found}, err
		})

	tool(s, "read_thread", "A thread: its names, state (open, done, dropped) and summary, its parent and children, and its card (what eriphe-core knows of it: last decisions, facts and people, open intentions, cards waiting for the user, preferences; what may carry someone else's words is named, not quoted), and its recent log.",
		func(ctx context.Context, in threadIDIn) (threadView, error) {
			v, err := c.threadView(in.Thread, 20)
			if err == nil {
				c.cardLookedUp(threadRef(v.ID)) // its turn's card was not enough (card_use.go)
				v.Card, _ = c.aboutCard(threadRef(v.ID), threadCardChars)
				c.turnThread(in.Thread)
			}
			return v, err
		})

	tool(s, "link_thread", "Join to a thread what eriphe-core does not link itself, a card raised outside a turn about the thread (kind:id: in its timeline). A card born in a turn about it is linked already; where it stands is remember kind progress.",
		func(ctx context.Context, in linkThreadIn) (map[string]any, error) {
			c.turnThread(in.Thread)
			note, err := c.linkThread(in.Thread, in.Ref, "brain")
			return map[string]any{"note": note}, err
		})

	tool(s, "close_thread", "Close a thread once its real goal is reached, never before: the summary says how; it closes at once, done. Refused while it has active child threads: close or stop them first. To give up instead, stop_thread.",
		func(ctx context.Context, in closeIn) (map[string]any, error) {
			if err := c.closeLight(in.Thread, in.Summary, "brain"); err != nil {
				return nil, err
			}
			return map[string]any{"note": "Closed."}, nil
		})

	tool(s, "reopen_thread", "Reopen a thread that ended (done or dropped) when its matter comes back: it opens again, held by you, with its history.",
		func(ctx context.Context, in reopenIn) (Thread, error) {
			c.turnThread(in.Thread)
			return c.reopenThread(in.Thread, in.Why, "brain")
		})

	tool(s, "stop_thread", "Stop a thread the user wants stopped, or that will not reach its goal: it is dropped. Its active child threads stop with it.",
		func(ctx context.Context, in threadIDIn) (map[string]any, error) {
			if err := c.stopThread(ctx, in.Thread, "brain"); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		})
}

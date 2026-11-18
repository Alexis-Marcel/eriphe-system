package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A thread's life, until the graph's nodes replace it (#625): the brain
// opens it, closes it on its goal, stops it, reopens it when its matter
// comes back; the user puts an ended one away. Its work runs as steps of
// a node (node_steps.go): a thread has no plan nor budget of its own.

// threadMu makes the changes of a thread one at a time.
var threadMu sync.Mutex

// threadIn is a thread as the brain opens it.
type threadIn struct {
	Title  string `json:"title" jsonschema:"what the user reads for the thread, in a few words in their language: Refonte du service photos"`
	Goal   string `json:"goal" jsonschema:"its real outcome, in one or two sentences: what must have happened for it to be done (merged and deployed, not only reviewed); for you and the agents, never shown to the user"`
	Alias  string `json:"alias,omitempty" jsonschema:"the key of the matter it follows, kind:slug, to find it again: project:<name>, pr:<owner>/<repo>#<n>, alert:<id>, topic:<words>; a key is one thread's (a mail or an event is an item of a node's source, not a key)"`
	Ends   string `json:"ends,omitempty" jsonschema:"how it ends: goal (the default: closed once its goal is reached) or standing (what the user manages continuously, their mail, their agenda, a supervision: never closed)"`
	Take   int64  `json:"take,omitempty" jsonschema:"after a refusal that lists threads near this matter: the one that is it, given back (reopened if it ended) and given this alias"`
	NewBy  string `json:"new_because,omitempty" jsonschema:"after a refusal that lists threads near this matter: why this is another matter, in a few words; logged on the new thread"`
	Parent int64  `json:"parent,omitempty" jsonschema:"the thread this one is a child of: a part of that thread that became a matter of its own. An active top thread (a child has no children); the child opens inside it on the workspace, and what waits for the user in the child raises it. Given with an alias that has a thread already, that thread moves under it"`

	exact bool // only the thread of its alias, no matcher
}

// openThread records a thread, open at once. taint is why the turn that
// opens it may carry what someone else wrote.
func (c *core) openThread(in threadIn, taint []string, by string) (Thread, error) {
	in.Title, in.Goal = oneLine(in.Title), strings.TrimSpace(in.Goal)
	in.Ends = strings.ToLower(strings.TrimSpace(in.Ends))
	if in.Ends == "" {
		in.Ends = endsGoal
	}
	alias, err := c.freeAlias(in.Alias)
	if err != nil {
		return Thread{}, fmt.Errorf("alias: %w", err)
	}
	if in.Parent != 0 {
		if _, err := c.parentFor(in.Parent, Thread{Ends: in.Ends}); err != nil {
			return Thread{}, err
		}
	}
	switch {
	case in.Title == "":
		return Thread{}, errors.New("a title is needed")
	case in.Goal == "":
		return Thread{}, errors.New("a goal is needed")
	case !slices.Contains(threadEnds, in.Ends):
		return Thread{}, fmt.Errorf("ends %q: goal or standing", in.Ends)
	}
	res, err := c.store.db.Exec(`INSERT INTO threads (title, goal, state, tainted, created, started, ends, parent) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		truncate(in.Title, taskTitleMax), in.Goal, threadOpen, strings.Join(taint, ", "), time.Now().Unix(), time.Now().Unix(), in.Ends, in.Parent)
	if err != nil {
		return Thread{}, err
	}
	id, _ := res.LastInsertId()
	if err := c.giveAlias(id, alias); err != nil {
		log.Printf("thread %d: alias %s: %v", id, alias, err)
	}
	c.logThread(id, by, "Thread ouvert : "+in.Goal)
	c.threadMoved(id)
	return c.thread(id)
}

// stopThread stops a thread: it is dropped, its active children with it.
func (c *core) stopThread(ctx context.Context, id int64, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if !s.active() {
		return fmt.Errorf("the thread is %s already", s.State)
	}
	if s.standing() {
		return errStanding
	}
	c.stopLocked(ctx, s, by, "")
	return nil
}

// stopLocked drops an active thread, under threadMu; its active children
// stop with it (endChildren). why is its log line, "" for the usual one.
func (c *core) stopLocked(ctx context.Context, s Thread, by, why string) {
	c.endLight(ctx, s, threadDropped, "")
	c.logThread(s.ID, by, orStr(why, "Thread abandonné."))
}

// errStanding refuses to end a standing thread: what the user manages
// continuously has no goal to reach.
var errStanding = errors.New("a standing thread never ends: it is what the user manages continuously (their mail, their agenda). Leave it open; what is done in it is a note on its item")

// closeLight closes a thread whose goal is reached: done, with how as its
// summary.
func (c *core) closeLight(id int64, summary, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if !s.active() {
		return fmt.Errorf("the thread is %s already", s.State)
	}
	if s.standing() {
		return errStanding
	}
	if summary = strings.TrimSpace(summary); summary == "" {
		return errors.New("a summary is needed: how its goal was reached. A thread closes only on its goal; to give up, stop it")
	}
	if kids := c.activeChildren(id); len(kids) > 0 {
		return activeChildrenError(s, kids)
	}
	c.endLight(context.Background(), s, threadDone, summary)
	c.logThread(id, by, "Thread clos : "+summary)
	c.closeDecision(id) // thread_progress.go
	return nil
}

// endLight ends a thread: done or dropped; its active children with it.
func (c *core) endLight(ctx context.Context, s Thread, state, summary string) {
	fields := map[string]any{"state": state, "ended": time.Now().Unix()}
	if summary != "" {
		fields["summary"] = truncate(summary, 2000)
	}
	c.setThread(s.ID, fields)
	c.endChildren(ctx, s) // thread_children.go
	c.emitLogged(journalEvent{Kind: evThreadEnded, Key: fmt.Sprintf("thread:%d", s.ID), Source: "thread",
		Payload: map[string]any{"thread": s.ID, "state": state, "ends": s.Ends}})
}

// reopenThread opens again a thread that ended, when its matter comes
// back, its log kept.
func (c *core) reopenThread(id int64, why, by string) (Thread, error) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return s, err
	}
	if s.active() {
		return s, fmt.Errorf("the thread is %s: it has not ended", s.State)
	}
	if why = oneLine(why); why == "" {
		return s, errors.New("why is needed: what brings it back")
	}
	if s.Parent != 0 {
		// A child never moves under a parent that ended (thread_children.go).
		if p, err := c.thread(s.Parent); err == nil && !p.active() {
			return s, fmt.Errorf("its parent thread %d « %s » is %s: reopen it first", p.ID, p.Title, p.State)
		}
	}
	c.setThread(id, map[string]any{"state": threadOpen, "ended": 0, "tidied": 0})
	c.logThread(id, by, "Thread rouvert : "+why)
	return c.thread(id)
}

// tidyThread is « Ranger » on an ended thread's surface: the user puts it
// away. It leaves the workspace at once; it is never deleted, and stays
// one to find and reopen when its matter comes back.
func (c *core) tidyThread(id int64, by string) error {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return err
	}
	if s.active() || s.Ends != endsGoal || s.tidied > 0 {
		return errNotTidy
	}
	c.setThread(id, map[string]any{"tidied": time.Now().Unix()})
	c.logThread(id, by, "Fil rangé.")
	return nil
}

// errNotTidy is « Ranger » on a thread that is not one to put away: active,
// standing, or put away already. 409.
var errNotTidy = errors.New("only an ended thread with a goal is put away, once")

// threadsTick is the engine's pass, every 15 seconds: the steps of the
// graph's works (node_steps.go).
func (c *core) threadsTick(now time.Time) {
	c.stepsTick()
}

// byTask tells an author that is a task's: a skill's run, or another.
func byTask(by string) bool { return strings.HasPrefix(by, "skill:") || strings.HasPrefix(by, "task:") }

// threadRef is how a card or a surface names a thread.
func threadRef(id int64) string { return "thread:" + strconv.FormatInt(id, 10) }

// threadMoved notes that a thread changed: its state, its log, a card
// linked to it. The attention queue's surfaces are made again.
func (c *core) threadMoved(id int64) {
	c.kickWorkspace()
}

const threadCardsSchema = `CREATE TABLE IF NOT EXISTS thread_cards (
	thread  INTEGER NOT NULL,
	ref     TEXT NOT NULL,
	created INTEGER NOT NULL,
	PRIMARY KEY (thread, ref)
);`

func init() { extraSchema = append(extraSchema, threadCardsSchema) }

// linkThread links to a thread a card raised outside a turn about it: it
// shows in its timeline.
func (c *core) linkThread(id int64, ref, by string) (string, error) {
	threadMu.Lock()
	defer threadMu.Unlock()
	s, err := c.thread(id)
	if err != nil {
		return "", err
	}
	if !s.active() {
		return "", fmt.Errorf("the thread is %s: reopen it first", s.State)
	}
	ref = strings.TrimSpace(ref)
	kind, pid, ok := parseProposalRef(ref)
	if !ok {
		return "", fmt.Errorf("ref %q: a card's ref (kind:id, as workspace_read lists them)", ref)
	}
	if xs, _ := c.cardsWhere(`ref = ?`, ref); len(xs) == 0 && !c.proposalOf(kind, pid).Pending {
		return "", fmt.Errorf("no card %s", ref)
	}
	c.linkCard(id, ref)
	c.logThread(id, by, "Carte liée : "+ref+".")
	return "Card linked: it shows in the thread's timeline.", nil
}

// linkCard shows a card in a thread's timeline.
func (c *core) linkCard(id int64, ref string) {
	c.store.db.Exec(`INSERT OR IGNORE INTO thread_cards (thread, ref, created) VALUES (?, ?, ?)`, id, ref, time.Now().Unix())
	c.threadMoved(id)
}

// threadWaits tells a thread one of whose linked cards waits for the
// user.
func (c *core) threadWaits(id int64) bool {
	rows, err := c.store.db.Query(`SELECT ref FROM thread_cards WHERE thread = ?`, id)
	if err != nil {
		return false
	}
	var refs []string
	for rows.Next() {
		var ref string
		if rows.Scan(&ref) == nil {
			refs = append(refs, ref)
		}
	}
	rows.Close()
	return slices.ContainsFunc(refs, func(ref string) bool { return c.cardState(ref) == cardPending })
}

// cardRaisedFor links a card raised for a thread to it: its source is
// thread:<id>.
func (c *core) cardRaisedFor(source, ref string) {
	n, ok := strings.CutPrefix(source, "thread:")
	if !ok {
		return
	}
	if id, err := strconv.ParseInt(n, 10, 64); err == nil && id > 0 {
		c.linkCard(id, ref)
	}
}

// cardState is where a card stands: pending, approved, rejected or gone.
func (c *core) cardState(ref string) string {
	if xs, err := c.cardsWhere(`ref = ?`, ref); err == nil && len(xs) == 1 {
		x, p := c.liveCard(xs[0])
		if x.State == cardPending && !p.Pending {
			return cardGone
		}
		return x.State
	}
	kind, pid, ok := parseProposalRef(ref)
	if !ok {
		return cardGone
	}
	p := c.proposalOf(kind, pid)
	if p.Pending {
		return cardPending
	}
	return cmpOr(p.Outcome, cardGone)
}

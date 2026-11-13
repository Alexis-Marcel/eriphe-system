package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
)

// The composer: a persistent assistant, like the brain and the thread
// leads (one warm task on a node, each request a turn), that chooses from
// the pool of surfaces what the workspace shows now and how big
// (workspace_pool.go: its input and its answer). It has no tool and no
// power: it only answers, a lean JSON object, and never writes content.
// Code checks that answer, and chooses by weight itself when the composer
// is off, slower than composerTimeout or wrong; code lays the choice out
// at once (workspace_layout.go).
//
// It chooses when the pool or the queue changes (once the burst settles),
// ahead of the user's return while they are away and when a preference
// about the workspace enters memory (workspace.go). Nothing waits on it: a
// new surface is placed by code at once, the brain reads the queue itself,
// and the composer runs on its own goroutine.

//go:embed composer-prompt.md
var composerPrompt string

const (
	assistantRoleComposer = "composer"
	composerAssistant     = "composer" // its name, as Settings creates it
	triggerComposition    = "composition"

	composerStartPrompt = "Tu démarres. Ne fais rien d'autre et réponds seulement « Prêt. »."
	composerTurnsMax    = 30 // turns before it starts a fresh session: each turn carries all it needs
)

var (
	composerTimeout     = 10 * time.Second // past it, the fallback composes
	composerDebounce    = 3 * time.Second  // a queue change settles
	composerDebounceMax = 15 * time.Second // a queue that keeps changing is composed all the same
	composerAway        = 20 * time.Minute // away this long, the next composition is for the user's return
	composerPoll        = 2 * time.Second  // how often the queue is read for changes
)

// noHarnessTools keeps none of the harness's own tools (Claude Code's
// --tools "").
var noHarnessTools = []string{""}

func init() {
	assistantTriggers[triggerComposition] = assistantTrigger{
		relay:   (*core).relayComposer,
		deliver: (*core).deliverComposer,
	}
	assistantRoles[assistantRoleComposer] = assistantRole{
		Prompt: composerPrompt, StartPrompt: composerStartPrompt, Triggers: []string{triggerComposition},
		HarnessTools: noHarnessTools, Effort: "low", Fresh: true,
		instructions: func(_ *core, _ Assistant, prompt string) string {
			return strings.ReplaceAll(prompt, "{{eriphe}}", assistantName())
		},
	}
}

// composerState is what eriphe-core keeps of the composer: the request
// waiting for its turn and the one it answers.
type composerState struct {
	mu       sync.Mutex
	pending  *composerAsk // waits for the composer to be idle
	inflight *composerAsk // delivered: its answer is being written
	buf      strings.Builder
	turns    int // turns of its current session
}

// composerAsk is one choice asked of the composer.
type composerAsk struct {
	text  string
	after int         // its task's last event before the turn
	done  chan string // its answer, once; "" when its turn failed
}

var (
	errComposerOff  = errors.New("the composer is off")
	errComposerSlow = errors.New("the composer took too long")
)

// composerReady tells whether the composer can answer now: on, with an
// agent, its task started on a node.
func (c *core) composerReady() bool {
	a, ok := c.conf().Assistants[composerAssistant]
	if !ok || a.Disabled || a.Agent == "" || a.Role != assistantRoleComposer {
		return false
	}
	id := c.assistantStatus(composerAssistant).Task
	if id == "" {
		return false
	}
	t, err := c.store.task(id)
	return err == nil && (t.State == stateIdle || t.State == stateRunning)
}

// askComposer gives the composer a turn and waits for its answer, at most
// composerTimeout.
func (c *core) askComposer(text string) (string, error) {
	if !c.composerReady() {
		return "", errComposerOff
	}
	s := &c.composer
	q := &composerAsk{text: text, done: make(chan string, 1)}
	s.mu.Lock()
	s.pending = q
	s.mu.Unlock()
	c.ping()
	timer := time.NewTimer(composerTimeout)
	defer timer.Stop()
	select {
	case raw := <-q.done:
		return raw, nil
	case <-timer.C:
		// Too late: its answer, when it comes, is dropped.
		s.mu.Lock()
		if s.pending == q {
			s.pending = nil
		}
		if s.inflight == q {
			s.inflight = nil
			s.buf.Reset()
		}
		s.mu.Unlock()
		return "", errComposerSlow
	}
}

// deliverComposer gives the idle composer the composition asked, if any.
// After composerTurnsMax turns its task stops first: the next one starts a
// fresh session (its role is Fresh).
func (c *core) deliverComposer(ctx context.Context, a Assistant, t *TaskRow) (bool, error) {
	s := &c.composer
	s.mu.Lock()
	q := s.pending
	if q == nil || s.inflight != nil {
		s.mu.Unlock()
		return false, nil
	}
	if s.turns >= composerTurnsMax {
		s.turns = 0
		s.mu.Unlock()
		log.Printf("composer: %d turns, starting a fresh session", composerTurnsMax)
		return false, c.stopByBrain(ctx, t.ID)
	}
	s.pending, s.inflight = nil, q
	q.after = t.LastSeq
	s.buf.Reset()
	s.turns++
	s.mu.Unlock()
	if _, err := c.answer(ctx, t.ID, "", q.text, nil, byPolicy); err != nil {
		s.mu.Lock()
		if s.inflight == q {
			s.inflight = nil
		}
		s.mu.Unlock()
		return false, err
	}
	return true, nil
}

// relayComposer reads what the composer wrote since it was last read: the
// text of the turn asked, handed over at its end.
func (c *core) relayComposer(a Assistant, t *TaskRow) {
	seen := c.assistantStatus(a.Name).SeenSeq
	events, err := c.store.eventsAfter(t.ID, seen, 500)
	if err != nil || len(events) == 0 {
		return
	}
	s := &c.composer
	s.mu.Lock()
	failed := false
	for _, e := range events {
		seen = e.Seq
		q := s.inflight
		if q == nil || e.Seq <= q.after {
			continue // its start, or a turn no one waits for anymore
		}
		_, bad := turnFailure(e)
		switch e.Type {
		case "text":
			if text, _ := e.Data["text"].(string); bad {
				failed = true
			} else {
				s.buf.WriteString(text)
			}
		case "turn_end":
			answer := s.buf.String()
			if strings.TrimSpace(answer) == "" {
				answer, _ = e.Data["result"].(string)
			}
			if bad || failed {
				answer = ""
			}
			q.done <- answer
			s.inflight, failed = nil, false
			s.buf.Reset()
		}
	}
	s.mu.Unlock()
	c.updateAssistant(a.Name, func(st *AssistantStatus) { st.SeenSeq = seen })
}

// recompose makes a new choice for a moment and keeps it: the composer's
// when it answers in time and well, code's by weight otherwise; code lays
// it out at once (keepChoice).
func (c *core) recompose(moment, remark string) workspaceChoice {
	c.syncQueueSurfaces() // the queue as it is now, in the pool
	s := c.poolSnapshot(moment, remark)
	ch := fallbackChoice(moment, s.Now)
	if s.Focus != "" {
		ch = focusFallback(s) // workspace_focus.go
	}
	if len(s.Pool) > 0 && c.composerReady() {
		start := time.Now()
		raw, err := c.askComposer(composerTurn(s))
		if err == nil {
			var got workspaceChoice
			var sig composerSignal
			var dropped int
			got, sig, dropped, err = checkChoice(raw, s)
			if dropped > 0 {
				log.Printf("composer: %d id(s) dropped", dropped)
			}
			if err == nil {
				ch = got
				c.signalComposer(sig, s)
			}
		}
		if err != nil {
			log.Printf("composer: %v: chosen by weight", err)
		} else {
			log.Printf("composer: chose in %s", time.Since(start).Round(100*time.Millisecond))
		}
	}
	if s.Focus != "" {
		// Around what the user opened: kept for that focus only, the base
		// left as it was, to go back to (workspace_focus.go).
		ch.Related = relatedIDs(s.Related)
		if moment != momentFocus {
			c.markBaseStale()
		}
		if !c.keepFocusChoice(s.Focus, s.FocusSince, ch) {
			log.Printf("composer: the focus changed meanwhile: choice dropped")
		}
		return ch
	}
	c.keepChoice(ch)
	return ch
}

// queueStamp is what of the queue a choice depends on, besides its
// surfaces (poolStamp).
func (c *core) queueStamp() string {
	var b strings.Builder
	for _, it := range c.attentionOpen() {
		fmt.Fprintf(&b, "%s\x00%d\x00%s\x00%s\n", it.Ref, it.Weight, it.Why, it.Say)
	}
	return b.String()
}

// composeMoment is the moment of a composition now: the user's return
// when they have been away long enough, a change otherwise.
func (c *core) composeMoment() string {
	if _, away := c.awaySince(c.notifyTime()); away >= composerAway {
		return momentArrival
	}
	return momentChange
}

// runComposer chooses when it should, one choice at a time: once a burst
// of changes settles, when a preference about the workspace enters memory,
// and once ahead of the user's return while they are away.
func (c *core) runComposer(ctx context.Context) {
	tick := time.NewTicker(composerPoll)
	defer tick.Stop()
	d := debouncer{wait: composerDebounce, maxWait: composerDebounceMax}
	stamp := c.queueStamp() + c.poolStamp() + c.gestureStamp()
	prefSeen, prefChanged, _ := c.workspacePrefStamp()
	checked := time.Time{}
	focusKick := c.focusKicked()
	var settle *time.Timer // a focus that changed settles first (focusSettle)
	var settled <-chan time.Time
	if c.lastChoice() == nil {
		c.recompose(momentChange, "")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-focusKick:
			if settle == nil {
				settle = time.NewTimer(focusSettle)
			} else {
				settle.Reset(focusSettle)
			}
			settled = settle.C
			continue
		case <-settled:
			settled = nil
			if c.settleFocus(time.Now()) {
				d.changed(time.Now()) // back, and the base's reason changed meanwhile
			}
			continue
		}
		now := time.Now()
		// The focus ended with no one telling: its time passed, or its surface went.
		if settled == nil && c.focusLapsed(now) && c.settleFocus(now) {
			d.changed(now)
		}
		if s := c.queueStamp() + c.poolStamp() + c.gestureStamp(); s != stamp { // a gesture of the user's: at once, debounced
			stamp = s
			d.changed(now)
		}
		// A preference about the workspace entered memory (or changed, or
		// left it), not any message of the user's.
		if ps, changed, words := c.workspacePrefStamp(); ps != prefSeen {
			prefSeen = ps
			d = debouncer{wait: d.wait, maxWait: d.maxWait}
			if changed > prefChanged && words != "" {
				c.recompose(momentRemark, words)
			} else {
				c.recompose(c.composeMoment(), "")
			}
			prefChanged = max(prefChanged, changed)
			continue
		}
		if d.due(now) {
			c.recompose(c.composeMoment(), "")
			continue
		}
		// Away: the user's return is composed once, ahead of it.
		if now.Sub(checked) >= time.Minute {
			checked = now
			if last := c.lastChoice(); last != nil && last.Moment != momentArrival && c.composeMoment() == momentArrival {
				c.recompose(momentArrival, "")
			}
		}
	}
}

// Settings: the composer on or off, and its agent.

// composerDefaultAgent is the agent the composer runs unless told: a
// Sonnet one (fast and exact enough: Haiku invented refs), else the default.
func composerDefaultAgent(m *Config) string {
	names := make([]string, 0, len(m.Agents))
	for n := range m.Agents {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		a := m.Agents[n]
		if harnessOf(a.Runtime).canBeBrain() && strings.Contains(strings.ToLower(a.Model), "sonnet") {
			return n
		}
	}
	if a, ok := m.Agents[m.DefaultAgent]; ok && harnessOf(a.Runtime).canBeBrain() {
		return m.DefaultAgent
	}
	return ""
}

// setComposer turns the composer on or off and picks its agent ("" keeps
// it, or takes composerDefaultAgent).
func (c *core) setComposer(on bool, agent string) error {
	m := c.conf()
	a, ok := m.Assistants[composerAssistant]
	if !ok {
		a = Assistant{Role: assistantRoleComposer}
	}
	if agent != "" {
		ag, ok := m.Agents[agent]
		if !ok {
			return errNotFound(kindAgent, agent)
		}
		if !harnessOf(ag.Runtime).canBeBrain() {
			return errInvalid(fmt.Errorf("agent %s runs %s: the composer needs a runtime that takes a system prompt", agent, ag.Runtime))
		}
		a.Agent = agent
	} else if a.Agent == "" {
		a.Agent = composerDefaultAgent(m)
	}
	if on && a.Agent == "" {
		return errInvalid(errors.New("no agent can run the composer: add one first"))
	}
	a.Disabled = !on
	if err := c.putAssistant(composerAssistant, a); err != nil {
		return err
	}
	c.ping()
	return nil
}

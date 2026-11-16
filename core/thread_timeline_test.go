package main

import "time"

// TimelineStep is a step as the surface shows it.
type TimelineStep struct {
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Question  string    `json:"question,omitempty"` // a choice's, an approval's, what a user step asks
	Options   []string  `json:"options,omitempty"`
	Due       time.Time `json:"due,omitzero"`
	Intention int64     `json:"intention,omitempty"`
	Task      string    `json:"task,omitempty"`
	Card      string    `json:"card,omitempty"`       // its card's ref: a call's, an approval's, a task's start
	CardState string    `json:"card_state,omitempty"` // pending, approved, rejected, gone
	CallState string    `json:"call_state,omitempty"` // a call approved: running, done, failed
	Output    string    `json:"output,omitempty"`
	Note      string    `json:"note,omitempty"`
	Tainted   bool      `json:"tainted,omitempty"` // its output may carry what someone else wrote
	Started   time.Time `json:"started,omitzero"`
	Ended     time.Time `json:"ended,omitzero"`
}

// TimelineCard is a card linked to the thread.
type TimelineCard struct {
	Ref     string    `json:"ref"`
	Kind    string    `json:"kind"`
	State   string    `json:"state"`
	Created time.Time `json:"created"`
}

// threadTimeline is a thread as the tests read it: its steps with their
// kind, state, times and card, the cards linked to it, its log.
type threadTimeline struct {
	Thread
	Steps []TimelineStep `json:"steps"`
	Cards []TimelineCard `json:"cards,omitempty"`
	Log   []ThreadLine   `json:"log"`
}

// threadTimeline reads a thread for a test.
func (c *core) threadTimeline(id int64) (threadTimeline, error) {
	s, err := c.thread(id)
	if err != nil {
		return threadTimeline{}, err
	}
	steps, err := c.steps(id)
	if err != nil {
		return threadTimeline{}, err
	}
	v := threadTimeline{Thread: s, Steps: []TimelineStep{}, Log: c.threadLog(id, 40)}
	seen := map[string]bool{}
	for _, st := range steps {
		x := TimelineStep{Key: st.Key, Title: st.Title, Kind: st.Kind, Status: st.Status, Question: st.Question, Options: st.Options,
			Intention: st.Intention, Task: st.Task, Output: st.Output, Note: st.Note, Tainted: st.Tainted != "" || (st.Task != "" && c.taskResultTaint(st.Task) != ""),
			Due: unixOrZero(st.DueAt), Started: unixOrZero(st.Started), Ended: unixOrZero(st.Ended)}
		x.Card = c.stepCard(s.ID, st)
		if x.Card != "" {
			x.CardState = c.cardState(x.Card)
			seen[x.Card] = true
			if kind, pid, ok := parseProposalRef(x.Card); ok && kind == cardAccess {
				x.CallState, _ = c.callState(pid)
			}
		}
		v.Steps = append(v.Steps, x)
	}
	rows, err := c.store.db.Query(`SELECT ref, created FROM thread_cards WHERE thread = ? ORDER BY created, ref`, id)
	if err == nil {
		for rows.Next() {
			var ref string
			var at int64
			if rows.Scan(&ref, &at) == nil && !seen[ref] {
				kind, _, _ := parseProposalRef(ref)
				v.Cards = append(v.Cards, TimelineCard{Ref: ref, Kind: kind, Created: time.Unix(at, 0)})
			}
		}
		rows.Close() // one connection: the states are read after
	}
	for i := range v.Cards {
		v.Cards[i].State = c.cardState(v.Cards[i].Ref)
	}
	return v, nil
}

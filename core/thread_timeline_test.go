package main

import "time"

// TimelineCard is a card linked to the thread.
type TimelineCard struct {
	Ref     string    `json:"ref"`
	Kind    string    `json:"kind"`
	State   string    `json:"state"`
	Created time.Time `json:"created"`
}

// threadTimeline is a thread as the tests read it: the cards linked to
// it, its log.
type threadTimeline struct {
	Thread
	Cards []TimelineCard `json:"cards,omitempty"`
	Log   []ThreadLine   `json:"log"`
}

// threadTimeline reads a thread for a test.
func (c *core) threadTimeline(id int64) (threadTimeline, error) {
	s, err := c.thread(id)
	if err != nil {
		return threadTimeline{}, err
	}
	v := threadTimeline{Thread: s, Log: c.threadLog(id, 40)}
	rows, err := c.store.db.Query(`SELECT ref, created FROM thread_cards WHERE thread = ? ORDER BY created, ref`, id)
	if err == nil {
		for rows.Next() {
			var ref string
			var at int64
			if rows.Scan(&ref, &at) == nil {
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

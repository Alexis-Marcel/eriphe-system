package main

import (
	"encoding/json"
	"log"
	"time"
)

// Sending what the router decided (route.go), and keeping the log of it:
// what it planned next to what was sent, for the user to check
// (Settings → Où te prévenir). In observe mode, the first one, everything
// goes as before (every channel, nothing while a tab is in front) and the
// plan is only written down; in route mode, the plan is followed: one
// target, then the next if the notification is not seen in time.
//
// Seen is: opened (its push or link), answered (👍 / 👎 / « pas
// maintenant », here or on Telegram), or shown in a tab in front of the
// user. A pass every half minute sends what was not seen to the next
// target (escalateDue).

const routeLogSchema = `CREATE TABLE IF NOT EXISTS route_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, notif INTEGER NOT NULL DEFAULT 0,
  what TEXT NOT NULL, mode TEXT NOT NULL, planned TEXT NOT NULL, sent TEXT NOT NULL, skip TEXT NOT NULL DEFAULT '',
  device TEXT NOT NULL DEFAULT '', step INTEGER NOT NULL DEFAULT 0, next_at INTEGER NOT NULL DEFAULT 0,
  tried TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '',
  acked_at INTEGER NOT NULL DEFAULT 0, acked_how TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS route_log_next ON route_log (next_at);`

func init() { extraSchema = append(extraSchema, routeLogSchema) }

const (
	routeLogKeep  = 300 // decisions kept
	stepBroadcast = "push"

	skipFront = "front" // observe mode: nothing sent, a tab was in front

	ackOpened   = "opened"
	ackFeedback = "feedback"
	ackInApp    = "inapp"
)

// inApp is a notification shown in a workspace tab (app.js).
type inApp struct {
	ID   int64             `json:"id"` // its route_log row
	Body map[string]string `json:"body"`
	URL  string            `json:"url"`
}

// tell is the one way every notification goes. A test of one device goes
// to it; anything else is routed.
func (c *core) tell(n pushNote) {
	if n.OnlyFor != "" || n.Anyway {
		c.broadcast(n)
		return
	}
	now := c.notifyTime()
	set := c.routeSettings()
	plan := route(n.Route, c.routeSignals(now), set, nil)
	e := routeEntry{At: now, Notif: n.Notif, What: truncate(n.Body["fr"], 120), Mode: set.Mode, Planned: plan}
	if set.Mode != routeOn {
		e.Sent, e.Skip = c.broadcast(n)
		if len(plan.First) > 0 {
			e.Device = plan.First[0].Label
		}
		c.logRoute(e, n)
		return
	}
	e.ID = c.logRoute(e, n) // first: the in-app note carries its id
	c.reach(n, e.ID, plan.First)
	e.Sent = plan.First
	for _, t := range plan.First {
		e.Tried = append(e.Tried, t.key())
	}
	if len(plan.First) > 0 {
		e.Device = plan.First[0].Label
	}
	if plan.After > 0 {
		e.NextAt = now.Add(plan.After)
	}
	c.updateRoute(e, n)
}

// broadcast is how notifications went before the router, and still go in
// observe mode: nothing while a tab is in front of the user, else every
// channel of the note. It returns what it reached.
func (c *core) broadcast(n pushNote) ([]routeTarget, string) {
	if !n.Anyway && c.push != nil && c.push.inFront() {
		return nil, skipFront
	}
	var sent []routeTarget
	for _, ch := range c.channelsOf(n) {
		switch ch {
		case chanPush:
			if c.push == nil {
				continue
			}
			c.push.deliver(n)
			subs, _ := c.push.subscriptions()
			for _, s := range subs {
				if n.OnlyFor == "" || s.ID == n.OnlyFor {
					sent = append(sent, routeTarget{Step: stepBroadcast, Device: s.ID, Label: s.Label})
				}
			}
		case chanTelegram:
			if c.tg != nil && c.tg.linked() {
				c.tg.deliver(n)
				sent = append(sent, routeTarget{Step: stepTelegram, Label: "Telegram"})
			}
		}
	}
	return sent, ""
}

// reach sends a note to targets, each by its line.
func (c *core) reach(n pushNote, logID int64, targets []routeTarget) {
	for _, t := range targets {
		switch t.Step {
		case stepInApp:
			if c.push != nil {
				c.push.queueInApp(t.Tabs, inApp{ID: logID, Body: n.Body, URL: n.URL})
			}
		case stepComputer, stepPhone:
			if c.push != nil {
				one := n
				one.OnlyFor = t.Device
				c.push.deliver(one)
			}
		case stepTelegram:
			if c.tg != nil {
				c.tg.deliver(n)
			}
		}
	}
}

// queueInApp leaves a note for tabs; each shows it once (takeInApp).
func (p *pusher) queueInApp(tabs []string, n inApp) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, tab := range tabs {
		if len(p.inbox[tab]) < 20 {
			p.inbox[tab] = append(p.inbox[tab], n)
		}
	}
}

// takeInApp is what waits for a tab, taken: shown there.
func (p *pusher) takeInApp(tab string) []inApp {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.inbox[tab]
	delete(p.inbox, tab)
	return out
}

// showInApp hands a tab its notes and marks them seen.
func (c *core) showInApp(tab string) []inApp {
	if c.push == nil {
		return nil
	}
	notes := c.push.takeInApp(tab)
	for _, n := range notes {
		c.store.db.Exec(`UPDATE route_log SET acked_at = ?, acked_how = ?, next_at = 0, note = '' WHERE id = ? AND acked_at = 0`,
			c.notifyTime().Unix(), ackInApp, n.ID)
	}
	return notes
}

// ackNotification marks a notification seen: no next step.
func (c *core) ackNotification(id int64, how string) {
	if id <= 0 {
		return
	}
	c.store.db.Exec(`UPDATE route_log SET acked_at = ?, acked_how = ?, next_at = 0, note = '' WHERE notif = ? AND acked_at = 0`,
		c.notifyTime().Unix(), how, id)
}

// routeEntry is one decision of the log.
type routeEntry struct {
	ID       int64
	At       time.Time
	Notif    int64
	What     string
	Mode     string
	Planned  routePlan
	Sent     []routeTarget
	Skip     string
	Device   string // the first target's, planned or sent
	Step     int
	NextAt   time.Time
	Tried    []string
	AckedAt  time.Time
	AckedHow string
}

func (c *core) logRoute(e routeEntry, n pushNote) int64 {
	planned, _ := json.Marshal(e.Planned)
	sent, _ := json.Marshal(e.Sent)
	res, err := c.store.db.Exec(`INSERT INTO route_log (at, notif, what, mode, planned, sent, skip, device) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.Unix(), e.Notif, e.What, e.Mode, string(planned), string(sent), e.Skip, e.Device)
	if err != nil {
		log.Printf("route: %v", err)
		return 0
	}
	id, _ := res.LastInsertId()
	c.store.db.Exec(`DELETE FROM route_log WHERE id <= ?`, id-routeLogKeep)
	return id
}

// updateRoute keeps what was sent, and what comes next; the note is kept
// only while a next step may need it.
func (c *core) updateRoute(e routeEntry, n pushNote) {
	sent, _ := json.Marshal(e.Sent)
	tried, _ := json.Marshal(e.Tried)
	note, next := "", int64(0)
	if !e.NextAt.IsZero() {
		raw, _ := json.Marshal(n)
		note, next = string(raw), e.NextAt.Unix()
	}
	c.store.db.Exec(`UPDATE route_log SET sent = ?, tried = ?, step = ?, next_at = ?, note = ?, device = ? WHERE id = ?`,
		string(sent), string(tried), e.Step, next, note, e.Device, e.ID)
}

const routeCols = `id, at, notif, what, mode, planned, sent, skip, device, step, next_at, tried, acked_at, acked_how`

func scanRoute(row interface{ Scan(...any) error }) (routeEntry, error) {
	var e routeEntry
	var at, next, acked int64
	var planned, sent, tried string
	err := row.Scan(&e.ID, &at, &e.Notif, &e.What, &e.Mode, &planned, &sent, &e.Skip, &e.Device, &e.Step, &next, &tried, &acked, &e.AckedHow)
	if err != nil {
		return e, err
	}
	e.At = time.Unix(at, 0)
	if next > 0 {
		e.NextAt = time.Unix(next, 0)
	}
	if acked > 0 {
		e.AckedAt = time.Unix(acked, 0)
	}
	json.Unmarshal([]byte(planned), &e.Planned)
	json.Unmarshal([]byte(sent), &e.Sent)
	json.Unmarshal([]byte(tried), &e.Tried)
	return e, nil
}

// recentRoutes are the last decisions, newest first.
func (c *core) recentRoutes(n int) []routeEntry {
	rows, err := c.store.db.Query(`SELECT `+routeCols+` FROM route_log ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []routeEntry
	for rows.Next() {
		if e, err := scanRoute(rows); err == nil {
			out = append(out, e)
		}
	}
	return out
}

// escalateDue sends each notification not seen in time to its next
// target: routed again with the signals of now, without what was tried.
// Never in the quiet hours unless its rule says even at night; two steps
// at most.
func (c *core) escalateDue() {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	now := c.notifyTime()
	rows, err := c.store.db.Query(`SELECT `+routeCols+`, note FROM route_log
		WHERE next_at > 0 AND next_at <= ? AND acked_at = 0 AND mode = ? ORDER BY id LIMIT 50`, now.Unix(), routeOn)
	if err != nil {
		log.Printf("route: %v", err)
		return
	}
	type due struct {
		e    routeEntry
		note pushNote
	}
	var all []due
	for rows.Next() {
		var d due
		var raw string
		var at, next, acked int64
		var planned, sent, tried string
		if err := rows.Scan(&d.e.ID, &at, &d.e.Notif, &d.e.What, &d.e.Mode, &planned, &sent, &d.e.Skip, &d.e.Device, &d.e.Step,
			&next, &tried, &acked, &d.e.AckedHow, &raw); err != nil {
			continue
		}
		json.Unmarshal([]byte(planned), &d.e.Planned)
		json.Unmarshal([]byte(sent), &d.e.Sent)
		json.Unmarshal([]byte(tried), &d.e.Tried)
		if json.Unmarshal([]byte(raw), &d.note) == nil {
			all = append(all, d)
		}
	}
	rows.Close()
	set := c.routeSettings()
	for _, d := range all {
		c.escalate(d.e, d.note, set, now)
	}
}

func (c *core) escalate(e routeEntry, n pushNote, set routeSettings, now time.Time) {
	e.NextAt = time.Time{}
	sig := c.routeSignals(now)
	if n.Route.Level != levelNow || n.Route.Tainted || sig.Quiet && !n.Route.Urgent || e.Step >= maxEscalation {
		c.updateRoute(e, n)
		return
	}
	plan := route(n.Route, sig, set, e.Tried)
	if len(plan.First) == 0 {
		c.updateRoute(e, n) // nowhere else: it stays where it is
		return
	}
	next := plan.First[0] // a rule's added lines went with the first step
	c.reach(n, e.ID, []routeTarget{next})
	e.Step++
	e.Sent = append(e.Sent, next)
	e.Tried = append(e.Tried, next.key())
	if e.Step < maxEscalation {
		e.NextAt = now.Add(escalationDelay(n.Route, set))
	}
	log.Printf("route: %d not seen, step %d: %s", e.ID, e.Step, next.Step)
	c.updateRoute(e, n)
}

// followRoutes runs the escalation pass every half minute.
func (c *core) followRoutes(stop <-chan struct{}) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			c.escalateDue()
		}
	}
}

// deviceTally is how the notifications sent to one device were taken.
type deviceTally struct {
	Device   string `json:"device"`
	Sent     int    `json:"sent"`
	Seen     int    `json:"seen"`
	SeenFast int    `json:"seen_within_2_min"`
	Moved    int    `json:"sent_on_elsewhere"` // not seen there in time
	NotNow   int    `json:"not_now,omitempty"`
	NotUsed  int    `json:"not_useful,omitempty"`
}

// deviceTallies are the last 30 days of routed notifications, by the
// device they went to first: what the brain proposes an order or a rule from.
func (c *core) deviceTallies() []deviceTally {
	since := c.notifyTime().AddDate(0, 0, -30).Unix()
	rows, err := c.store.db.Query(`SELECT r.device, COUNT(*), SUM(r.acked_at > 0), SUM(r.acked_at > 0 AND r.acked_at - r.at <= 120),
		SUM(r.step > 0), SUM(COALESCE(n.feedback, '') = 'later'), SUM(COALESCE(n.feedback, '') = 'down')
		FROM route_log r LEFT JOIN notifications n ON n.id = r.notif
		WHERE r.at >= ? AND r.mode = ? AND r.notif > 0 AND r.device != '' GROUP BY r.device ORDER BY COUNT(*) DESC LIMIT 20`, since, routeOn)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []deviceTally
	for rows.Next() {
		var t deviceTally
		if rows.Scan(&t.Device, &t.Sent, &t.Seen, &t.SeenFast, &t.Moved, &t.NotNow, &t.NotUsed) == nil {
			out = append(out, t)
		}
	}
	return out
}

// escalationDelay is how long a "now" waits to be seen.
func escalationDelay(n routeNote, set routeSettings) time.Duration {
	if n.Urgent {
		return urgentDelay
	}
	if set.Delay <= 0 {
		return defaultDelay
	}
	return set.Delay
}

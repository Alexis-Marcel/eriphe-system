package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// The Home Assistant entities the user follows. Discovery (ha_client.go)
// lists the house's candidates; the user follows some, asked through the
// brain (follow_entity, ha_tools.go) or on the page Settings → Observations
// (web_observe.go). eriphe-core keeps one WebSocket subscription to those
// entities alone (subscribe_entities) and reads their state, never an
// attribute. Two things come of it:
//
//   - an event ha.changed each time a followed entity's state moves, with
//     the entity, its new and former state and its area, which a skill
//     triggers on (when: ha.changed entity=binary_sensor.bed state=on);
//   - the room the user is in, for the router's "voice" line: the area of
//     the followed room tracker or occupancy sensor that moved last, unless
//     a followed person or tracker says the user is away. A sentence the
//     code writes is said there by a channel the user added that reaches it
//     (channel_out.go).
//
// No translation into signals, no levels: what an entity's state means is
// for the skill or the brain that reads it. Names and areas are the user's
// own words in Home Assistant; a state is a short value.

const (
	srcHA = "ha"

	haRefresh     = time.Minute
	haHouseFresh  = 10 * time.Minute
	haBackoffMax  = 5 * time.Minute
	haRediscover  = 24 * time.Hour
	haMaxFollowed = 40
	haNameMax     = 40
	haRoomMax     = 30 // runes

	voiceUrgentKey = "voice_urgent" // "on": an urgent skill's fixed message may be said aloud, at any hour
)

// haBackoffMin is the first wait before a reconnection (shorter in tests).
var haBackoffMin = 5 * time.Second

// haFollowsSchema keeps the followed entities. The mappings of before
// (entity → signal) are dropped at start: their entities become followed
// ones.
const haFollowsSchema = `CREATE TABLE IF NOT EXISTS ha_follows (
  id INTEGER PRIMARY KEY AUTOINCREMENT, entity TEXT NOT NULL, state TEXT NOT NULL,
  why TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL);`

func init() {
	extraSchema = append(extraSchema, haFollowsSchema)
	extraColumns = append(extraColumns,
		`INSERT INTO ha_follows (entity, state, why, created)
		  SELECT entity, 'active', MAX(why), MIN(created) FROM ha_mappings WHERE state = 'active' AND signal != 'voice' GROUP BY entity`,
		`DROP TABLE IF EXISTS ha_mappings`,
		// The signal context went with #278: its sources, levels and rooms.
		`DROP TABLE IF EXISTS context_sources`,
		`DELETE FROM kv WHERE key IN ('context_level', 'context_off', 'context_rooms')`)
}

// haFollow is one followed entity.
type haFollow struct {
	ID        int64
	Entity    string
	State     string // proposed or active
	Why       string
	SourceRef string
	Tainted   string
	Created   time.Time
}

// haEntityState is a followed entity's state as the follower keeps it.
type haEntityState struct {
	State   string
	Changed time.Time
}

// haState is what eriphe-core keeps of Home Assistant, in memory. Its zero
// value is ready.
type haState struct {
	endpoint func() (haEndpoint, error) // tests: a fake Home Assistant

	mu        sync.Mutex
	house     haHouse
	connected bool
	lastErr   string
	states    map[string]haEntityState // the followed entities', while connected
	kick      chan struct{}
}

func (h *haState) kickChan() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.kick == nil {
		h.kick = make(chan struct{}, 1)
	}
	return h.kick
}

// changed tells the follower that the followed entities changed.
func (h *haState) changed() {
	select {
	case h.kickChan() <- struct{}{}:
	default:
	}
}

func (h *haState) setHouse(house haHouse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.house = house
}

func (h *haState) cached() haHouse {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.house
}

// snapshot is the followed entities' states now; nil when not connected.
func (h *haState) snapshot() map[string]haEntityState {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.connected {
		return nil
	}
	out := make(map[string]haEntityState, len(h.states))
	for k, v := range h.states {
		out[k] = v
	}
	return out
}

// A room's name: letters, digits, spaces and a few marks, short. It is the
// user's own word in Home Assistant.
var notRoomName = regexp.MustCompile(`[^\p{L}\p{N} '’-]+`)

func cleanRoom(s string) string {
	s = strings.Join(strings.Fields(notRoomName.ReplaceAllString(s, " ")), " ")
	if r := []rune(s); len(r) > haRoomMax {
		s = strings.TrimSpace(string(r[:haRoomMax]))
	}
	return s
}

// haShown is a name or a state of the house's as the brain reads it:
// short, on one line, without marks.
func haShown(s string) string {
	s = strings.Join(strings.Fields(notRoomName.ReplaceAllString(s, " ")), " ")
	if r := []rune(s); len(r) > haNameMax {
		s = string(r[:haNameMax])
	}
	return s
}

// notStateChar: a state keeps letters, digits and the marks of Home
// Assistant's states (not_home, 21.5, 2026-11-09T07:00:00+00:00).
var notStateChar = regexp.MustCompile(`[^\p{L}\p{N} _.:+'’-]+`)

// haStateShown is a state as an event or the brain reads it: short, on one
// line, its own marks kept so a skill's filter matches it (state=not_home).
func haStateShown(s string) string {
	s = strings.Join(strings.Fields(notStateChar.ReplaceAllString(s, " ")), " ")
	if r := []rune(s); len(r) > haNameMax {
		s = string(r[:haNameMax])
	}
	return s
}

// checkFollow checks an entity against what discovery found.
func checkFollow(entity string, house haHouse) (haEntity, error) {
	e, ok := house.entity(strings.ToLower(strings.TrimSpace(entity)))
	if !ok {
		return haEntity{}, errors.New("no such entity among the house's candidates (ha_entities lists them)")
	}
	return e, nil
}

// The followed entities, in the database.

func (c *core) haFollows(state string) []haFollow {
	q := `SELECT id, entity, state, why, source_ref, tainted, created FROM ha_follows`
	var args []any
	if state != "" {
		q += ` WHERE state = ?`
		args = append(args, state)
	}
	rows, err := c.store.db.Query(q+` ORDER BY entity, id`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []haFollow
	for rows.Next() {
		var f haFollow
		var created int64
		if rows.Scan(&f.ID, &f.Entity, &f.State, &f.Why, &f.SourceRef, &f.Tainted, &created) == nil {
			f.Created = time.Unix(created, 0)
			out = append(out, f)
		}
	}
	return out
}

// followedEntities are the ids followed now.
func (c *core) followedEntities() []string {
	var ids []string
	for _, f := range c.haFollows(ruleActive) {
		if !slices.Contains(ids, f.Entity) {
			ids = append(ids, f.Entity)
		}
	}
	return ids
}

// saveFollow keeps a followed entity, or a proposal to follow it; either
// replaces the same entity's proposal.
func (c *core) saveFollow(f haFollow) (int64, error) {
	if f.State == ruleActive && slices.Contains(c.followedEntities(), f.Entity) {
		c.store.db.Exec(`DELETE FROM ha_follows WHERE entity = ? AND state = ?`, f.Entity, ruleProposed)
		return 0, nil // followed already
	}
	if len(c.haFollows("")) >= haMaxFollowed {
		return 0, fmt.Errorf("at most %d followed entities", haMaxFollowed)
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM ha_follows WHERE entity = ? AND state = ?`, f.Entity, ruleProposed); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO ha_follows (entity, state, why, source_ref, tainted, created) VALUES (?, ?, ?, ?, ?, ?)`,
		f.Entity, f.State, f.Why, f.SourceRef, f.Tainted, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if f.State == ruleActive {
		c.ha.changed()
	}
	return id, nil
}

// decideFollow approves (checked again, then followed) or rejects a
// proposal: the user's say.
func (c *core) decideFollow(ctx context.Context, id int64, approve bool) error {
	i := slices.IndexFunc(c.haFollows(ruleProposed), func(f haFollow) bool { return f.ID == id })
	if i < 0 {
		return fmt.Errorf("no proposal to follow %d", id)
	}
	p := c.haFollows(ruleProposed)[i]
	if approve {
		house, err := c.haHouse(ctx)
		if err != nil {
			return err
		}
		if _, err := checkFollow(p.Entity, house); err != nil {
			return err
		}
		p.State = ruleActive
		if _, err := c.saveFollow(p); err != nil {
			return err
		}
	}
	_, err := c.store.db.Exec(`DELETE FROM ha_follows WHERE id = ?`, id)
	return err
}

// unfollow stops following an entity.
func (c *core) unfollow(entity string) error {
	res, err := c.store.db.Exec(`DELETE FROM ha_follows WHERE entity = ? AND state = ?`, entity, ruleActive)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s is not followed", entity)
	}
	c.ha.changed()
	return nil
}

// haHouse is the house as discovery last saw it, read again when older
// than ten minutes.
func (c *core) haHouse(ctx context.Context) (haHouse, error) {
	if h := c.ha.cached(); !h.At.IsZero() && time.Since(h.At) < haHouseFresh {
		return h, nil
	}
	e, err := c.haLink()
	if err != nil {
		return haHouse{}, err
	}
	conn, err := dialHA(ctx, e)
	if err != nil {
		return haHouse{}, err
	}
	defer conn.close()
	house, err := conn.discover(ctx)
	if err != nil {
		return haHouse{}, err
	}
	c.ha.setHouse(house)
	return house, nil
}

// Following.

// followHA keeps one subscription to the followed entities while there are
// any and Home Assistant is connected; it reconnects with a growing wait,
// and starts again when the followed entities change.
func (c *core) followHA(stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if stop != nil {
			<-stop
			cancel()
		}
	}()
	wait := haBackoffMin
	for ctx.Err() == nil {
		start := time.Now()
		err := c.haSession(ctx)
		c.ha.mu.Lock()
		c.ha.connected, c.ha.states = false, nil
		if err != nil && !errors.Is(err, errHAIdle) {
			c.ha.lastErr = err.Error()
		}
		c.ha.mu.Unlock()
		d := wait
		switch {
		case errors.Is(err, errHAIdle):
			d, wait = time.Minute, haBackoffMin
		case err == nil || time.Since(start) > time.Minute:
			d, wait = haBackoffMin, haBackoffMin
		default:
			log.Printf("home assistant: %v (again in %s)", err, wait)
			wait = min(wait*2, haBackoffMax)
		}
		select {
		case <-ctx.Done():
			return
		case <-c.ha.kickChan():
		case <-time.After(d):
		}
	}
}

var errHAIdle = errors.New("nothing to follow")

// haSession is one connection: discover, subscribe to the followed
// entities only, and keep their states until the connection drops, the
// followed entities change or a day has passed.
func (c *core) haSession(ctx context.Context) error {
	ids := c.followedEntities()
	if len(ids) == 0 {
		return errHAIdle
	}
	e, err := c.haLink()
	if err != nil {
		return errHAIdle
	}
	conn, err := dialHA(ctx, e)
	if err != nil {
		return err
	}
	defer conn.close()
	house, err := conn.discover(ctx)
	if err != nil {
		return err
	}
	c.ha.setHouse(house)
	states := map[string]haEntityState{}
	now := time.Now()
	for _, e := range house.Entities {
		if slices.Contains(ids, e.ID) {
			states[e.ID] = haEntityState{State: e.State, Changed: now}
		}
	}
	sub, err := conn.send(ctx, "subscribe_entities", map[string]any{"entity_ids": ids})
	if err != nil {
		return err
	}
	c.ha.mu.Lock()
	c.ha.connected, c.ha.lastErr, c.ha.states = true, "", states
	c.ha.mu.Unlock()

	msgs := make(chan haMsg)
	errs := make(chan error, 1)
	rctx, stopReading := context.WithCancel(ctx)
	defer stopReading()
	go func() {
		for {
			var m haMsg
			if err := conn.read(rctx, &m); err != nil {
				errs <- err
				return
			}
			select {
			case msgs <- m:
			case <-rctx.Done():
				return
			}
		}
	}()
	until := time.After(haRediscover)
	kick := c.ha.kickChan()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case <-kick:
			return nil // the followed entities changed: start again with them
		case <-until:
			return nil
		case m := <-msgs:
			if m.ID == sub && m.Type == "result" && !m.Success {
				return errors.New("home assistant: subscribe_entities refused")
			}
			if m.ID != sub || m.Type != "event" {
				continue
			}
			c.ha.mu.Lock()
			moved := applyEntityEvent(m.Event, ids, c.ha.states, time.Now())
			c.ha.mu.Unlock()
			c.emitHAChanges(moved, house)
		}
	}
}

// haMove is one followed entity's state that moved.
type haMove struct {
	Entity, State, Was string
}

// applyEntityEvent reads a subscribe_entities event: added (a), changed
// (c) and removed (r) entities, compressed. Only the followed ids count;
// only the state is read, never an attribute. It returns what moved.
func applyEntityEvent(raw json.RawMessage, ids []string, states map[string]haEntityState, now time.Time) []haMove {
	var ev struct {
		Added   map[string]struct{ S *string } `json:"a"`
		Changed map[string]struct {
			Plus struct{ S *string } `json:"+"`
		} `json:"c"`
		Removed []string `json:"r"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return nil
	}
	var moved []haMove
	set := func(id string, s *string) {
		if s == nil || !slices.Contains(ids, id) {
			return
		}
		if old, ok := states[id]; !ok || old.State != *s {
			states[id] = haEntityState{State: *s, Changed: now}
			moved = append(moved, haMove{id, *s, old.State})
		}
	}
	for _, id := range sortedKeys(ev.Added) {
		set(id, ev.Added[id].S)
	}
	for _, id := range sortedKeys(ev.Changed) {
		set(id, ev.Changed[id].Plus.S)
	}
	for _, id := range ev.Removed {
		if old, ok := states[id]; ok && slices.Contains(ids, id) {
			delete(states, id)
			moved = append(moved, haMove{id, "", old.State})
		}
	}
	return moved
}

// haStatus is the follower's state, for the page and the brain.
func (c *core) haStatus() (connected bool, lastErr string) {
	c.ha.mu.Lock()
	defer c.ha.mu.Unlock()
	return c.ha.connected, c.ha.lastErr
}

// The room the user is in, for the voice line (channel_out.go).

// haGone: a state that says nothing of a room.
func haGone(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "unavailable", "not_home", "away", "none", "off":
		return true
	}
	return false
}

// userRoom is the room the followed entities put the user in now, unless
// a followed person or tracker says they are away: a room tracker of
// theirs (their phone's room) first, as it follows them; else the area of
// the occupancy sensor (on) that moved last, which may be someone else, so
// the voice only says a general sentence. States are live while connected:
// a sensor on for an hour still says the room.
func userRoom(house haHouse, states map[string]haEntityState) (string, bool) {
	var tracker, sensor string
	var trackerAt, sensorAt time.Time
	for _, id := range sortedKeys(states) {
		st := states[id]
		e, ok := house.entity(id)
		if !ok {
			continue
		}
		s := strings.ToLower(strings.TrimSpace(st.State))
		switch {
		case e.Domain == "person" || e.Domain == "device_tracker":
			if s == "not_home" || s == "away" || s != "home" && !haGone(s) {
				return "", false // the user is out (not_home, or a zone of theirs)
			}
		case e.Domain == "sensor" && haRoomTracker(e.ID):
			if r := cleanRoom(st.State); !haGone(s) && r != "" && (tracker == "" || st.Changed.After(trackerAt)) {
				tracker, trackerAt = r, st.Changed
			}
		case e.Domain == "binary_sensor" && slices.Contains([]string{"occupancy", "presence", "motion"}, e.Class):
			if s == "on" && e.Area != "" && (sensor == "" || st.Changed.After(sensorAt)) {
				sensor, sensorAt = e.Area, st.Changed
			}
		}
	}
	if tracker != "" {
		return tracker, true
	}
	return sensor, sensor != ""
}

// haUserRoom is the room the user is in now, from the followed entities.
func (c *core) haUserRoom(now time.Time) (string, bool) {
	states := c.ha.snapshot()
	if states == nil {
		return "", false
	}
	return userRoom(c.ha.cached(), states)
}

func (c *core) voiceUrgent() bool { return c.store.get(voiceUrgentKey) == "on" }

// voiceText is what the voice says: written by the code, never the
// notification's text nor anything external. An urgent skill's fixed
// message (the user's own words, approved with the skill) only when the
// user turned voice_for_urgent on.
func (c *core) voiceText(n pushNote) string {
	user := c.conf().User
	if n.Route.Fixed && c.voiceUrgent() {
		if text := strings.TrimSpace(n.Body["fr"]); text != "" {
			return user + ", " + text
		}
	}
	return fmt.Sprintf("%s, %s a quelque chose pour toi.", user, c.conf().assistantName())
}

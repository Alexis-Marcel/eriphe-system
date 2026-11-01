package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
)

// Home Assistant as a source of presence (context.go), adapted to each
// house, in four steps:
//
//  1. discover (code, no model): the registries and the states, filtered
//     to the entities that may say something of presence (ha_client.go);
//  2. map (the brain proposes, the user approves): an entity → one of a
//     closed list of signals, kept in ha_mappings. Applied at once only
//     when the user asks in a clean turn, citing their message; otherwise
//     proposed in À valider. The page « Ce qu'Eriphe sait de toi » lists
//     the mappings and removes them;
//  3. follow (code only): one WebSocket subscription to the mapped entities
//     alone (subscribe_entities), their states turned into closed signals
//     under the source "ha", refreshed every minute while connected and
//     fading within three minutes when Home Assistant is out of reach.
//     Nothing else from the house enters the context: no name, no
//     attribute, no other entity;
//  4. speak (route.go): a speaker mapped to a room is the router's "voice"
//     line, used only when the user is in that room now, by a personal
//     signal, with a text the code writes (ha_voice in this file).
//
// A signal only makes Eriphe quieter, except that one line, which is held
// to more: a fresh, personal room, a "now" from a clean turn, outside the
// quiet hours (an urgent watch's fixed message may go through them only if
// the user turned on voice_for_urgent), and never the notification's text.

const (
	srcHA = "ha"

	// The signals a mapping gives: closed.
	haHome   = "home"   // place: home or away (person, device_tracker)
	haRoom   = "room"   // room: the area of an occupancy sensor, or a room tracker's state
	haAsleep = "asleep" // home_activity sleep (a bed's occupancy, a sleep mode)
	haTV     = "tv"     // home_activity tv (a media player playing)
	haDND    = "dnd"    // focus on or off (the companion app's focus, a do not disturb switch)
	haVoice  = "voice"  // a speaker of a room: the router's voice line

	haTTL         = 3 * time.Minute // a signal of the house, refreshed every minute while connected
	haRefresh     = time.Minute
	haVoiceFresh  = 2 * time.Minute // the room must be this fresh for the voice
	haHouseFresh  = 10 * time.Minute
	haBackoffMax  = 5 * time.Minute
	haRediscover  = 24 * time.Hour
	haMaxMappings = 40
	haNameMax     = 40

	voiceUrgentKey = "voice_urgent" // "on": an urgent watch's fixed message may be said aloud, at any hour
)

var haSignalNames = []string{haHome, haRoom, haAsleep, haTV, haDND, haVoice}

// haBackoffMin is the first wait before a reconnection (shorter in tests).
var haBackoffMin = 5 * time.Second

const haMappingsSchema = `CREATE TABLE IF NOT EXISTS ha_mappings (
  id INTEGER PRIMARY KEY AUTOINCREMENT, signal TEXT NOT NULL, entity TEXT NOT NULL,
  room TEXT NOT NULL DEFAULT '', personal INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL,
  why TEXT NOT NULL DEFAULT '', source_ref TEXT NOT NULL DEFAULT '', tainted TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, haMappingsSchema) }

// haMapping is one entity → signal.
type haMapping struct {
	ID        int64
	Signal    string
	Entity    string
	Room      string // the entity's area (room, voice); "" for a room tracker
	Personal  bool   // about the user (a person, their phone's tracker), not the house
	State     string // proposed or active
	Why       string
	SourceRef string
	Tainted   string
	Created   time.Time
}

// haState is what eriphe-core keeps of Home Assistant, in memory. Its zero
// value is ready.
type haState struct {
	endpoint func() (haEndpoint, error) // tests: a fake Home Assistant

	mu           sync.Mutex
	house        haHouse
	connected    bool
	lastErr      string
	roomPersonal bool // the room signal now is a personal one
	kick         chan struct{}
	said         []string // tests: what was announced
}

func (h *haState) kickChan() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.kick == nil {
		h.kick = make(chan struct{}, 1)
	}
	return h.kick
}

// changed tells the follower that the mappings changed.
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

// checkMapping checks a mapping against what discovery found: the entity
// exists, and its domain and class can say that signal.
func checkMapping(signal, entity string, house haHouse) (haMapping, error) {
	signal = strings.ToLower(strings.TrimSpace(signal))
	entity = strings.ToLower(strings.TrimSpace(entity))
	if !slices.Contains(haSignalNames, signal) {
		return haMapping{}, fmt.Errorf("signal is one of %s", strings.Join(haSignalNames, ", "))
	}
	e, ok := house.entity(entity)
	if !ok {
		return haMapping{}, errors.New("no such entity among the house's candidates (presence_sources lists them)")
	}
	m := haMapping{Signal: signal, Entity: e.ID}
	bad := func(what string) (haMapping, error) {
		return haMapping{}, fmt.Errorf("%s cannot say %s: %s", e.ID, signal, what)
	}
	switch signal {
	case haHome:
		if e.Domain != "person" && e.Domain != "device_tracker" {
			return bad("home comes from a person or a device_tracker")
		}
		m.Personal = true
	case haRoom:
		switch {
		case e.Domain == "sensor" && haRoomTracker(e.ID):
			m.Personal = true // the room of the user's own device
		case e.Domain == "binary_sensor" && slices.Contains([]string{"occupancy", "presence", "motion"}, e.Class):
			if e.Area == "" {
				return bad("the sensor is in no area of Home Assistant")
			}
			m.Room = e.Area
		default:
			return bad("room comes from an occupancy, presence or motion sensor in an area, or a room tracker sensor")
		}
	case haAsleep:
		if !(e.Domain == "binary_sensor" && (e.Class == "occupancy" || e.Class == "presence")) && e.Domain != "input_boolean" {
			return bad("asleep comes from a bed's occupancy sensor or a sleep switch (input_boolean)")
		}
	case haTV:
		if e.Domain != "media_player" {
			return bad("tv comes from a media_player")
		}
	case haDND:
		if !haFocusEntity(e.ID) && e.Domain != "input_boolean" {
			return bad("dnd comes from the companion app's focus sensor or a do not disturb switch (input_boolean)")
		}
		m.Personal = haFocusEntity(e.ID)
	case haVoice:
		switch {
		case e.Domain != "assist_satellite" && e.Domain != "media_player":
			return bad("voice is an assist_satellite or a media_player")
		case e.Area == "":
			return bad("the speaker is in no area of Home Assistant")
		case e.Domain == "media_player" && len(house.TTS) == 0:
			return bad("a media_player speaks through a tts entity, and the house has none")
		}
		m.Room = e.Area
	}
	return m, nil
}

// haEntityState is an entity's state as the follower keeps it.
type haEntityState struct {
	State   string
	Changed time.Time
}

// haTranslate is what one mapping says from its entity's state: a kind and
// a value of the context's closed lists, or nothing.
func haTranslate(m haMapping, state string) (kind, value string, ok bool) {
	s := strings.ToLower(strings.TrimSpace(state))
	if s == "" || s == "unknown" || s == "unavailable" {
		return "", "", false
	}
	switch m.Signal {
	case haHome:
		if s == "home" {
			return sigPlace, "home", true
		}
		return sigPlace, "away", true // not_home, or a zone of theirs
	case haRoom:
		if m.Room == "" { // a room tracker: its state is the room
			if s == "not_home" || s == "away" || s == "none" || s == "off" {
				return "", "", false
			}
			return sigRoom, cleanRoom(state), true
		}
		if s == "on" {
			return sigRoom, m.Room, true
		}
	case haAsleep:
		if s == "on" {
			return sigHomeActivity, "sleep", true
		}
	case haTV:
		if s == "playing" {
			return sigHomeActivity, "tv", true
		}
	case haDND:
		switch {
		case strings.Contains(s, "sleep") || strings.Contains(s, "sommeil"):
			return sigFocus, "sleep", true
		case s == "off" || s == "false" || s == "none":
			return sigFocus, "off", true
		default:
			return sigFocus, "on", true
		}
	}
	return "", "", false
}

// haSignals folds the mapped entities' states into signals of the source
// "ha", each checked against its closed list (a room the user does not
// have is dropped). Per kind, a personal mapping wins over the house's,
// then the newest change. It also says whether the room is personal.
func haSignals(maps []haMapping, states map[string]haEntityState, rooms []string, now time.Time) ([]ctxSignal, bool) {
	type pick struct {
		value    string
		personal bool
		changed  time.Time
	}
	best := map[string]pick{}
	for _, m := range maps {
		if m.State != ruleActive || m.Signal == haVoice {
			continue
		}
		st, ok := states[m.Entity]
		if !ok {
			continue
		}
		kind, value, ok := haTranslate(m, st.State)
		if !ok {
			continue
		}
		if _, err := checkSignal(ctxInput{Kind: kind, Value: value}, rooms, srcHA, now); err != nil {
			continue
		}
		p := pick{value, m.Personal, st.Changed}
		if b, ok := best[kind]; ok && (b.personal && !p.personal || b.personal == p.personal && !p.changed.After(b.changed)) {
			continue
		}
		best[kind] = p
	}
	var out []ctxSignal
	for _, kind := range ctxKindNames {
		p, ok := best[kind]
		if !ok {
			continue
		}
		s, err := checkSignal(ctxInput{Kind: kind, Value: p.value}, rooms, srcHA, now)
		if err != nil {
			continue
		}
		s.Until = now.Add(min(haTTL, ctxKinds[kind].TTL))
		out = append(out, s)
	}
	return out, best[sigRoom].personal
}

// The mappings, in the database.

func (c *core) haMappings(state string) []haMapping {
	q := `SELECT id, signal, entity, room, personal, state, why, source_ref, tainted, created FROM ha_mappings`
	var args []any
	if state != "" {
		q += ` WHERE state = ?`
		args = append(args, state)
	}
	rows, err := c.store.db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []haMapping
	for rows.Next() {
		var m haMapping
		var created int64
		if rows.Scan(&m.ID, &m.Signal, &m.Entity, &m.Room, &m.Personal, &m.State, &m.Why, &m.SourceRef, &m.Tainted, &created) == nil {
			m.Created = time.Unix(created, 0)
			out = append(out, m)
		}
	}
	return out
}

// saveMapping keeps a mapping: active replaces the same entity's other
// mappings to that signal; a proposal replaces the one waiting.
func (c *core) saveMapping(m haMapping) (int64, error) {
	if len(c.haMappings("")) >= haMaxMappings {
		return 0, fmt.Errorf("at most %d mappings", haMaxMappings)
	}
	tx, err := c.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM ha_mappings WHERE signal = ? AND entity = ? AND (state = ? OR ? = ?)`,
		m.Signal, m.Entity, ruleProposed, m.State, ruleActive); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO ha_mappings (signal, entity, room, personal, state, why, source_ref, tainted, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.Signal, m.Entity, m.Room, m.Personal, m.State, m.Why, m.SourceRef, m.Tainted, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if m.State == ruleActive {
		c.haAddRooms(m)
		c.ha.changed()
	}
	return id, nil
}

// haAddRooms adds the rooms a mapping can say to the user's list, the
// only values a room takes: the sensor's area, or every area of the
// house for a room tracker. Visible and editable on the page.
func (c *core) haAddRooms(m haMapping) {
	var add []string
	switch {
	case m.Room != "":
		add = []string{m.Room}
	case m.Signal == haRoom:
		add = c.ha.cached().Areas
	}
	if len(add) == 0 {
		return
	}
	rooms := c.contextPrefs().Rooms
	c.store.set(ctxRoomsKey, strings.Join(parseRooms(strings.Join(append(rooms, add...), ",")), ", "))
}

// decideMapping approves (checked again, then active) or rejects a
// proposal: the user's say.
func (c *core) decideMapping(ctx context.Context, id int64, approve bool) error {
	i := slices.IndexFunc(c.haMappings(ruleProposed), func(m haMapping) bool { return m.ID == id })
	if i < 0 {
		return fmt.Errorf("no proposed mapping %d", id)
	}
	p := c.haMappings(ruleProposed)[i]
	if approve {
		house, err := c.haHouse(ctx)
		if err != nil {
			return err
		}
		m, err := checkMapping(p.Signal, p.Entity, house)
		if err != nil {
			return err
		}
		m.State, m.Why, m.SourceRef, m.Tainted = ruleActive, p.Why, p.SourceRef, p.Tainted
		if _, err := c.saveMapping(m); err != nil {
			return err
		}
	}
	_, err := c.store.db.Exec(`DELETE FROM ha_mappings WHERE id = ?`, id)
	return err
}

// removeMapping drops a mapping; its signal fades at once.
func (c *core) removeMapping(id int64) error {
	res, err := c.store.db.Exec(`DELETE FROM ha_mappings WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no mapping %d", id)
	}
	c.known.forget(srcHA, "")
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

// followHA keeps one subscription to the mapped entities while there are
// any, Home Assistant is connected, the level is above 0 and the source is
// on; it reconnects with a growing wait, and starts again when the
// mappings change.
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
		c.ha.connected = false
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

// haFollowing tells whether there is anything to follow, and what.
func (c *core) haFollowing() []haMapping {
	p := c.contextPrefs()
	if p.Level == 0 || slices.Contains(p.Off, srcHA) {
		return nil
	}
	var out []haMapping
	for _, m := range c.haMappings(ruleActive) {
		if m.Signal != haVoice {
			out = append(out, m)
		}
	}
	return out
}

// haSession is one connection: discover, subscribe to the mapped entities
// only, and keep their signals until the connection drops, the mappings
// change or a day has passed.
func (c *core) haSession(ctx context.Context) error {
	maps := c.haFollowing()
	if len(maps) == 0 {
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
	ids := make([]string, 0, len(maps))
	for _, m := range maps {
		if !slices.Contains(ids, m.Entity) {
			ids = append(ids, m.Entity)
		}
	}
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
	c.ha.connected, c.ha.lastErr = true, ""
	c.ha.mu.Unlock()
	c.haPublish(maps, states)

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
	tick := time.NewTicker(haRefresh)
	defer tick.Stop()
	until := time.After(haRediscover)
	kick := c.ha.kickChan()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case <-kick:
			return nil // the mappings changed: start again with them
		case <-until:
			return nil
		case <-tick.C:
			c.haPublish(maps, states)
		case m := <-msgs:
			if m.ID == sub && m.Type == "result" && !m.Success {
				return errors.New("home assistant: subscribe_entities refused")
			}
			if m.ID != sub || m.Type != "event" {
				continue
			}
			if applyEntityEvent(m.Event, ids, states, time.Now()) {
				c.haPublish(maps, states)
			}
		}
	}
}

// applyEntityEvent reads a subscribe_entities event: added (a), changed
// (c) and removed (r) entities, compressed. Only the mapped ids count;
// only the state is read, never an attribute.
func applyEntityEvent(raw json.RawMessage, ids []string, states map[string]haEntityState, now time.Time) bool {
	var ev struct {
		Added   map[string]struct{ S *string } `json:"a"`
		Changed map[string]struct {
			Plus struct{ S *string } `json:"+"`
		} `json:"c"`
		Removed []string `json:"r"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return false
	}
	moved := false
	set := func(id string, s *string) {
		if s == nil || !slices.Contains(ids, id) {
			return
		}
		if old, ok := states[id]; !ok || old.State != *s {
			states[id] = haEntityState{State: *s, Changed: now}
			moved = true
		}
	}
	for id, a := range ev.Added {
		set(id, a.S)
	}
	for id, ch := range ev.Changed {
		set(id, ch.Plus.S)
	}
	for _, id := range ev.Removed {
		if _, ok := states[id]; ok && slices.Contains(ids, id) {
			delete(states, id)
			moved = true
		}
	}
	return moved
}

// haPublish puts the house's signals now, in place of the previous ones.
func (c *core) haPublish(maps []haMapping, states map[string]haEntityState) {
	now := time.Now()
	sigs, personal := haSignals(maps, states, c.contextPrefs().Rooms, now)
	c.known.forget(srcHA, "")
	c.putSignals(sigs)
	c.known.saw(srcHA, now)
	c.ha.mu.Lock()
	c.ha.roomPersonal = personal
	c.ha.mu.Unlock()
}

// haStatus is the follower's state, for the page and the brain.
func (c *core) haStatus() (connected bool, lastErr string) {
	c.ha.mu.Lock()
	defer c.ha.mu.Unlock()
	return c.ha.connected, c.ha.lastErr
}

// The voice in the room.

// voiceSpot is a speaker in the room the user is in now.
type voiceSpot struct {
	Entity string
	Room   string
}

// haVoiceSpot is where the voice can reach the user now: a speaker mapped
// to the room a fresh, personal signal of Home Assistant puts them in,
// while they are not said away.
func (c *core) haVoiceSpot(now time.Time) (voiceSpot, bool) {
	c.ha.mu.Lock()
	personal := c.ha.roomPersonal
	c.ha.mu.Unlock()
	if !personal {
		return voiceSpot{}, false
	}
	room, place := "", ""
	for _, s := range c.liveSignals(now) {
		if s.Source != srcHA {
			continue
		}
		switch s.Kind {
		case sigRoom:
			if now.Sub(s.At) < haVoiceFresh {
				room = s.Value
			}
		case sigPlace:
			place = s.Value
		}
	}
	if room == "" || place == "away" {
		return voiceSpot{}, false
	}
	for _, m := range c.haMappings(ruleActive) {
		if m.Signal == haVoice && strings.EqualFold(m.Room, room) {
			return voiceSpot{Entity: m.Entity, Room: room}, true
		}
	}
	return voiceSpot{}, false
}

// haVoiceReady: a speaker is mapped, so the voice line can be used.
func (c *core) haVoiceReady() bool {
	return slices.ContainsFunc(c.haMappings(ruleActive), func(m haMapping) bool { return m.Signal == haVoice })
}

func (c *core) voiceUrgent() bool { return c.store.get(voiceUrgentKey) == "on" }

// voiceText is what the voice says: written by the code, never the
// notification's text nor anything external. An urgent watch's fixed
// message (the user's own words, validated in a clean turn) only when the
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

// announce says the text on the speaker: assist_satellite.announce, or
// tts.speak (the house's first tts entity) for a media player.
func (c *core) announce(n pushNote, t routeTarget) {
	entity, ok := strings.CutPrefix(t.Device, voiceDevice)
	if !ok {
		return
	}
	text := c.voiceText(n)
	c.ha.mu.Lock()
	if c.ha.said != nil {
		c.ha.said = append(c.ha.said, entity+": "+text)
	}
	c.ha.mu.Unlock()
	e, err := c.haLink()
	if err != nil {
		log.Printf("voice: %v", err)
		return
	}
	ctx := context.Background()
	if strings.HasPrefix(entity, "assist_satellite.") {
		if err = haCall(ctx, e, "assist_satellite", "announce", map[string]any{"entity_id": entity, "message": text}); err != nil {
			log.Printf("voice: %v", err)
		}
		return
	}
	tts := c.ha.cached().TTS
	if len(tts) == 0 {
		return
	}
	if err := haCall(ctx, e, "tts", "speak", map[string]any{"entity_id": tts[0], "media_player_entity_id": entity, "message": text}); err != nil {
		log.Printf("voice: %v", err)
	}
}

const voiceDevice = "voice:"

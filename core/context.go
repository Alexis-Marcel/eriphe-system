package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The user's context: what Eriphe knows of where the user is and what they
// do, right now. One concept, turned on by the user with a level (0 Rien,
// 1 Présence, 2 Contexte) and a switch per source. Sources put signals:
//
//   - a machine's node, with its status (machine:<node>): presence, do not
//     disturb, and at level 2 the category of the app in front, worked out
//     on the machine (node/context.go);
//   - a source of the user's with its own token (src:<id>), through
//     POST /v1/context (context_sources.go): an iPhone shortcut, a Home
//     Assistant automation, a script;
//   - Home Assistant itself (ha): the entities the user mapped to a signal,
//     followed by code (ha_presence.go).
//
// A signal is a kind and a value from a closed list, with its source, when
// it came and when it expires. A value off the list is refused: no free
// text from a source ever reaches the model. Signals live in memory only,
// a few minutes to a few hours, and fade to unknown when they expire;
// nothing is written to the database.
//
// What reads them: the router (route.go), which skips a device that is
// locked, idle or not to be disturbed, and the brain's state header
// (contextLine). A signal can only make Eriphe quieter: it moves a
// notification away from a busy device or holds it; it never adds a line,
// never pierces the quiet hours, never wakes the brain, never authorises
// anything. A source cannot talk to the brain.

// The kinds of signal.
const (
	sigPresence     = "presence"      // a device: active, idle, locked
	sigDND          = "dnd"           // a device: on or off, with a reason
	sigActivity     = "activity"      // a device: the category of what is in front (level 2)
	sigPlace        = "place"         // the user: home or away
	sigRoom         = "room"          // the user: a room of theirs, from the list they keep
	sigHomeActivity = "home_activity" // the house: what it is doing (level 2), never said of the user alone
	sigFocus        = "focus"         // the user, declared: on, sleep, off
	sigBusy         = "busy"          // the user, declared: busy or free, until it expires

	ctxLevelKey = "context_level" // 0, 1 or 2; unset: 1 (what a Mac's node told before levels)
	ctxOffKey   = "context_off"   // the sources turned off: machine:<node>, src:<id>
	ctxRoomsKey = "context_rooms" // the user's rooms, the only values room takes

	ctxDefaultLevel = 1
	ctxMaxRooms     = 20
	ctxRoomMax      = 30 // runes
)

// ctxKind is what a kind of signal may say, and for how long.
type ctxKind struct {
	Level   int      // the level it needs
	Device  bool     // about a device (its source), not the user or the house
	Values  []string // nil: the user's rooms
	Reasons []string // dnd only
	TTL     time.Duration
	MaxTTL  time.Duration
}

var ctxKinds = map[string]ctxKind{
	sigPresence:     {Level: 1, Device: true, Values: []string{"active", "idle", "locked"}, TTL: idleFresh, MaxTTL: 10 * time.Minute},
	sigDND:          {Level: 1, Device: true, Values: []string{"on", "off"}, Reasons: dndReasons, TTL: idleFresh, MaxTTL: 12 * time.Hour},
	sigActivity:     {Level: 2, Device: true, Values: activityCategories, TTL: idleFresh, MaxTTL: 12 * time.Hour},
	sigPlace:        {Level: 1, Values: []string{"home", "away"}, TTL: 12 * time.Hour, MaxTTL: 24 * time.Hour},
	sigRoom:         {Level: 1, TTL: 15 * time.Minute, MaxTTL: time.Hour},
	sigHomeActivity: {Level: 2, Values: []string{"tv", "sleep", "office", "cooking", "other"}, TTL: 15 * time.Minute, MaxTTL: 12 * time.Hour},
	sigFocus:        {Level: 1, Values: []string{"on", "sleep", "off"}, TTL: 4 * time.Hour, MaxTTL: 24 * time.Hour},
	sigBusy:         {Level: 1, Values: []string{"busy", "free"}, TTL: time.Hour, MaxTTL: 12 * time.Hour},
}

// ctxKindNames are the kinds, in the order the page and the errors list them.
var ctxKindNames = []string{sigPresence, sigDND, sigActivity, sigPlace, sigRoom, sigHomeActivity, sigFocus, sigBusy}

// dndReasons say why a device is not to be disturbed, as a class.
var dndReasons = []string{"game", "meeting", "fullscreen", "mic", "focus"}

// activityCategories are what a device may say it is doing (level 2). The
// same list as the node's (node/context.go).
var activityCategories = []string{"game", "meeting", "code", "web", "video", "music", "chat", "other"}

// ctxSignal is one signal, checked.
type ctxSignal struct {
	Kind   string
	Value  string
	Reason string        // dnd on: why, from dndReasons
	Idle   time.Duration // presence from a node: since the last key or mouse event
	Source string        // machine:<node> or src:<id>
	At     time.Time
	Until  time.Time
}

func (s ctxSignal) fresh(now time.Time) bool { return !now.Before(s.At) && now.Before(s.Until) }

// ctxInput is a signal as a source sends it, before it is checked.
type ctxInput struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Reason     string `json:"reason,omitempty"`
	TTLMinutes int    `json:"ttl_minutes,omitempty"`
}

// checkSignal turns an input into a signal, or says why not: an unknown
// kind, a value or a reason off its list, a room the user does not have.
func checkSignal(in ctxInput, rooms []string, source string, now time.Time) (ctxSignal, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	k, ok := ctxKinds[kind]
	if !ok {
		return ctxSignal{}, fmt.Errorf("kind is one of %s", strings.Join(ctxKindNames, ", "))
	}
	value := strings.TrimSpace(in.Value)
	allowed := k.Values
	if kind == sigRoom {
		allowed = rooms
		if r, ok := knownRoom(value, rooms); ok {
			value = r
		}
	} else {
		value = strings.ToLower(value)
	}
	if !slices.Contains(allowed, value) {
		if kind == sigRoom {
			return ctxSignal{}, errors.New("room is one of the rooms listed on the page « Ce qu'Eriphe sait de toi »")
		}
		return ctxSignal{}, fmt.Errorf("%s is one of %s", kind, strings.Join(allowed, ", "))
	}
	s := ctxSignal{Kind: kind, Value: value, Source: source, At: now, Until: now.Add(k.TTL)}
	if reason := strings.ToLower(strings.TrimSpace(in.Reason)); reason != "" {
		if kind != sigDND || value != "on" || !slices.Contains(k.Reasons, reason) {
			return ctxSignal{}, fmt.Errorf("reason goes with dnd on, and is one of %s", strings.Join(dndReasons, ", "))
		}
		s.Reason = reason
	}
	if in.TTLMinutes < 0 {
		return ctxSignal{}, errors.New("ttl_minutes is positive")
	}
	if in.TTLMinutes > 0 {
		s.Until = now.Add(min(time.Duration(in.TTLMinutes)*time.Minute, k.MaxTTL))
	}
	return s, nil
}

// knownRoom finds a room in the user's list, whatever its case.
func knownRoom(v string, rooms []string) (string, bool) {
	for _, r := range rooms {
		if strings.EqualFold(r, v) {
			return r, true
		}
	}
	return "", false
}

// A room's name: letters, digits, spaces and a few marks, short. It is the
// user's own word, and it is quoted into the brain's header.
var notRoomName = regexp.MustCompile(`[^\p{L}\p{N} '’-]+`)

func cleanRoom(s string) string {
	s = strings.Join(strings.Fields(notRoomName.ReplaceAllString(s, " ")), " ")
	if r := []rune(s); len(r) > ctxRoomMax {
		s = strings.TrimSpace(string(r[:ctxRoomMax]))
	}
	return s
}

// parseRooms reads the user's list of rooms: comma or line separated,
// cleaned, each once, at most ctxMaxRooms.
func parseRooms(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		r := cleanRoom(f)
		if r == "" || slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, r) }) {
			continue
		}
		if out = append(out, r); len(out) == ctxMaxRooms {
			break
		}
	}
	return out
}

// contextStore keeps the signals, in memory only. Its zero value is empty
// and ready.
type contextStore struct {
	mu   sync.Mutex
	sigs map[string]ctxSignal // by source and kind
	seen map[string]time.Time // when each source last sent something
}

// saw notes that a source sent something, for the page.
func (s *contextStore) saw(source string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	s.seen[source] = at
}

// lastSeen is when a source last sent something, since eriphe-core started.
func (s *contextStore) lastSeen(source string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.seen[source]
	return t, ok
}

func (s *contextStore) put(sig ctxSignal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sigs == nil {
		s.sigs = map[string]ctxSignal{}
	}
	s.sigs[sig.Source+"|"+sig.Kind] = sig
}

// forget drops a source's signals, or one kind of them ("" for all).
func (s *contextStore) forget(source, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sig := range s.sigs {
		if sig.Source == source && (kind == "" || sig.Kind == kind) {
			delete(s.sigs, k)
		}
	}
	if kind == "" {
		delete(s.seen, source)
	}
}

// fresh are the signals not expired at now; the expired ones are dropped.
func (s *contextStore) fresh(now time.Time) []ctxSignal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ctxSignal
	for k, sig := range s.sigs {
		if !now.Before(sig.Until) {
			delete(s.sigs, k) // expired: unknown from now on
			continue
		}
		if sig.fresh(now) {
			out = append(out, sig)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return slices.Index(ctxKindNames, out[i].Kind) < slices.Index(ctxKindNames, out[j].Kind)
	})
	return out
}

// ctxPrefs are the user's say: the level and the sources turned off.
type ctxPrefs struct {
	Level int
	Off   []string
	Rooms []string
}

func (c *core) contextPrefs() ctxPrefs {
	p := ctxPrefs{Level: ctxDefaultLevel, Off: splitList(c.store.get(ctxOffKey)), Rooms: parseRooms(c.store.get(ctxRoomsKey))}
	switch c.store.get(ctxLevelKey) {
	case "0":
		p.Level = 0
	case "2":
		p.Level = 2
	}
	return p
}

// allows tells whether a signal of this kind from this source counts now.
func (p ctxPrefs) allows(source, kind string) bool {
	k, ok := ctxKinds[kind]
	return ok && p.Level >= k.Level && !slices.Contains(p.Off, source)
}

// putSignals keeps the signals a source sent that its switch and the level
// let in, and says how many. Each must have been checked (checkSignal).
// It never wakes the brain nor notifies: a signal only changes what the
// router and the next header read.
func (c *core) putSignals(sigs []ctxSignal) (kept int) {
	p := c.contextPrefs()
	for _, s := range sigs {
		if p.allows(s.Source, s.Kind) {
			c.known.put(s)
			kept++
		}
	}
	return kept
}

// liveSignals are the signals that count now: fresh, from a source on, of
// a kind the level allows (the level may have dropped since they came).
func (c *core) liveSignals(now time.Time) []ctxSignal {
	p := c.contextPrefs()
	var out []ctxSignal
	for _, s := range c.known.fresh(now) {
		if p.allows(s.Source, s.Kind) {
			out = append(out, s)
		}
	}
	return out
}

// deviceCtx is what the context says of one device.
type deviceCtx struct {
	Source   string
	Presence string        // active, idle, locked, or "" unknown
	Idle     time.Duration // from a node
	DND      bool
	Reason   string
	Activity string
	At       time.Time // its newest signal
}

// userCtx is the context now, as the router, the header and the page read it.
type userCtx struct {
	Level    int
	Devices  []deviceCtx // by source
	Place    string      // home, away, or "" unknown
	Room     string
	HomeDoes string // the house's activity
	Focus    string
	Busy     time.Time // busy until
	Known    bool      // any signal at all
}

func (c *core) userContext(now time.Time) userCtx {
	return buildContext(c.contextPrefs().Level, c.liveSignals(now))
}

// buildContext folds the signals into one view; for the user and the house,
// the newest signal of a kind wins.
func buildContext(level int, sigs []ctxSignal) userCtx {
	u := userCtx{Level: level, Known: len(sigs) > 0}
	byDevice := map[string]*deviceCtx{}
	newest := map[string]time.Time{}
	for _, s := range sigs {
		if ctxKinds[s.Kind].Device {
			d := byDevice[s.Source]
			if d == nil {
				d = &deviceCtx{Source: s.Source}
				byDevice[s.Source] = d
			}
			applyDevice(d, s)
			continue
		}
		if s.At.Before(newest[s.Kind]) {
			continue
		}
		newest[s.Kind] = s.At
		switch s.Kind {
		case sigPlace:
			u.Place = s.Value
		case sigRoom:
			u.Room = s.Value
		case sigHomeActivity:
			u.HomeDoes = s.Value
		case sigFocus:
			u.Focus = s.Value
		case sigBusy:
			u.Busy = time.Time{}
			if s.Value == "busy" {
				u.Busy = s.Until
			}
		}
	}
	for _, d := range byDevice {
		u.Devices = append(u.Devices, *d)
	}
	sort.Slice(u.Devices, func(i, j int) bool { return u.Devices[i].Source < u.Devices[j].Source })
	return u
}

func applyDevice(d *deviceCtx, s ctxSignal) {
	if s.At.After(d.At) {
		d.At = s.At
	}
	switch s.Kind {
	case sigPresence:
		d.Presence, d.Idle = s.Value, s.Idle
	case sigDND:
		d.DND, d.Reason = s.Value == "on", s.Reason
	case sigActivity:
		d.Activity = s.Value
	}
}

// device is what the context says of one source's device.
func (u userCtx) device(source string) (deviceCtx, bool) {
	for _, d := range u.Devices {
		if d.Source == source {
			return d, true
		}
	}
	return deviceCtx{}, false
}

// The words of the header, in French: written by the code from the closed
// values, never a source's own words.
var (
	ctxPresenceFR = map[string]string{"active": "actif", "idle": "inactif", "locked": "verrouillé"}
	ctxReasonFR   = map[string]string{"game": "jeu", "meeting": "réunion", "fullscreen": "plein écran", "mic": "micro", "focus": "concentration"}
	ctxActivityFR = map[string]string{"game": "jeu", "meeting": "réunion", "code": "code", "web": "navigation", "video": "vidéo",
		"music": "musique", "chat": "discussion", "other": "autre"}
	ctxHomeFR  = map[string]string{"tv": "télé", "sleep": "sommeil", "office": "bureau", "cooking": "cuisine", "other": "autre"}
	ctxFocusFR = map[string]string{"on": "Focus active", "sleep": "Focus sommeil", "off": "pas de Focus"}
)

// deviceWords says a device's state: "actif · jeu · ne pas déranger (jeu)".
func deviceWords(d deviceCtx) string {
	var parts []string
	if d.Presence != "" {
		w := ctxPresenceFR[d.Presence]
		if d.Presence == "idle" && d.Idle > 0 {
			w += " " + span(d.Idle)
		}
		parts = append(parts, w)
	}
	if d.Activity != "" {
		parts = append(parts, ctxActivityFR[d.Activity])
	}
	if d.DND {
		w := "ne pas déranger"
		if d.Reason != "" {
			w += " (" + ctxReasonFR[d.Reason] + ")"
		}
		parts = append(parts, w)
	}
	if len(parts) == 0 {
		return "inconnu"
	}
	return strings.Join(parts, " · ")
}

// line is the context as the brain reads it, on one short line, beside
// the user's state; "" at level 0 or when nothing is known. names says a source as the user named
// it (a node's name, a token source's name).
func (u userCtx) line(names func(string) string) string {
	if u.Level == 0 {
		return ""
	}
	var parts []string
	for _, d := range u.Devices {
		parts = append(parts, names(d.Source)+" : "+deviceWords(d))
	}
	if home := u.homeWords(); home != "" {
		parts = append(parts, "maison : "+home)
	}
	if u.Focus != "" {
		parts = append(parts, ctxFocusFR[u.Focus])
	}
	if len(parts) == 0 {
		return "" // nothing known: no line, the brain reads it as unknown
	}
	return "[contexte : " + strings.Join(parts, " · ") + "]"
}

// homeWords says the house: "à la maison, salon, télé".
func (u userCtx) homeWords() string {
	var parts []string
	switch u.Place {
	case "home":
		parts = append(parts, "à la maison")
	case "away":
		parts = append(parts, "absent")
	}
	if u.Room != "" {
		parts = append(parts, u.Room)
	}
	if u.HomeDoes != "" {
		parts = append(parts, "la maison : "+ctxHomeFR[u.HomeDoes])
	}
	return strings.Join(parts, ", ")
}

// sourceName says a source as the user knows it: its node's name, or the
// name they gave a token source.
func (c *core) sourceName(source string) string {
	if source == srcHA {
		return "Home Assistant"
	}
	if name, ok := strings.CutPrefix(source, srcMachine); ok {
		return name
	}
	if id, ok := strings.CutPrefix(source, srcToken); ok {
		for _, s := range c.contextSources() {
			if fmt.Sprint(s.ID) == id {
				return s.Name
			}
		}
	}
	return "source"
}

// contextLine is the header's line, "" when there is none.
func (c *core) contextLine(now time.Time) string {
	return c.userContext(now).line(c.sourceName)
}

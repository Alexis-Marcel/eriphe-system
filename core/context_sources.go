package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The context's sources (context.go). Two families:
//
//   - machine:<node>: a node tells its machine's idle time, whether it is
//     locked, whether it is not to be disturbed and, at level 2, the
//     category of the app in front, with its status (route_signals.go);
//   - src:<id>: a source the user made on the page « Ce qu'Eriphe sait de
//     toi », with a token of its own, that pushes signals to
//
//     POST /v1/context
//     Authorization: Bearer ecx_…
//     {"kind": "place", "value": "home"}
//     or {"signals": [{"kind": "focus", "value": "on", "ttl_minutes": 60}, …]}
//
//     The token can do nothing else: no conversation, no tool, no read.
//     Values are from closed lists (ctxKinds); a request with one value
//     off its list is refused whole (400, with the allowed values). A
//     signal the level or the source's switch keeps out is counted as
//     ignored, not an error. The answer: {"accepted": n, "ignored": m}.
//
// Each source has a switch (ctx_off). The level is a preset: 0 keeps
// every source out, 1 lets presence, do not disturb, place, room, focus
// and busy in, 2 the activity too. A future source (Home Assistant's
// entities, a Windows session helper) puts its signals the same way:
// checkSignal, then putSignals, under a source name of its own.

const (
	srcMachine = "machine:"
	srcToken   = "src:"

	ctxTokenPrefix  = "ecx_"
	ctxMaxSignals   = 8
	ctxMaxBody      = 4 << 10
	ctxMaxSources   = 20
	ctxSourceMaxLen = 30
)

const contextSourcesSchema = `CREATE TABLE IF NOT EXISTS context_sources (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
  created INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, contextSourcesSchema) }

// ctxSource is a source with a token.
type ctxSource struct {
	ID      int64
	Name    string
	Created time.Time
}

func (s ctxSource) key() string { return srcToken + strconv.FormatInt(s.ID, 10) }

// newContextSource makes a source and its token, shown once: only its
// hash is kept.
func (c *core) newContextSource(name string) (ctxSource, string, error) {
	name = cleanRoom(name) // the same marks as a room: it is quoted into the header
	if name == "" {
		return ctxSource{}, "", errors.New("a source needs a name")
	}
	if len(c.contextSources()) >= ctxMaxSources {
		return ctxSource{}, "", fmt.Errorf("at most %d sources", ctxMaxSources)
	}
	tok := ctxTokenPrefix + randomToken(32)
	now := time.Now()
	res, err := c.store.db.Exec(`INSERT INTO context_sources (name, token_hash, created) VALUES (?, ?, ?)`,
		name, hashToken(tok), now.Unix())
	if err != nil {
		return ctxSource{}, "", err
	}
	id, _ := res.LastInsertId()
	return ctxSource{ID: id, Name: name, Created: now}, tok, nil
}

func (c *core) contextSources() []ctxSource {
	rows, err := c.store.db.Query(`SELECT id, name, created FROM context_sources ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ctxSource
	for rows.Next() {
		var s ctxSource
		var created int64
		if rows.Scan(&s.ID, &s.Name, &created) == nil {
			s.Created = time.Unix(created, 0)
			out = append(out, s)
		}
	}
	return out
}

// revokeContextSource deletes a source, its token and its signals.
func (c *core) revokeContextSource(id int64) error {
	res, err := c.store.db.Exec(`DELETE FROM context_sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no source %d", id)
	}
	key := ctxSource{ID: id}.key()
	c.known.forget(key, "")
	c.setSourceOn(key, true) // its switch goes with it
	return nil
}

// contextSourceOf is the source a request's token belongs to.
func (c *core) contextSourceOf(r *http.Request) (ctxSource, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(tok, ctxTokenPrefix) {
		return ctxSource{}, false
	}
	var s ctxSource
	var created int64
	err := c.store.db.QueryRow(`SELECT id, name, created FROM context_sources WHERE token_hash = ?`, hashToken(tok)).
		Scan(&s.ID, &s.Name, &created)
	if err != nil {
		return ctxSource{}, false
	}
	s.Created = time.Unix(created, 0)
	return s, true
}

// setSourceOn turns a source on or off; off, its signals are dropped.
func (c *core) setSourceOn(source string, on bool) {
	off := splitList(c.store.get(ctxOffKey))
	off = slices.DeleteFunc(off, func(s string) bool { return s == source })
	if !on {
		off = append(off, source)
		c.known.forget(source, "")
	}
	slices.Sort(off)
	c.store.set(ctxOffKey, strings.Join(off, ","))
}

// setContextLevel sets the level; the signals above it are dropped.
func (c *core) setContextLevel(level int) error {
	if level < 0 || level > 2 {
		return errors.New("the level is 0, 1 or 2")
	}
	c.store.set(ctxLevelKey, strconv.Itoa(level))
	for _, s := range c.known.fresh(time.Now()) {
		if ctxKinds[s.Kind].Level > level {
			c.known.forget(s.Source, s.Kind)
		}
	}
	return nil
}

// ctxRequest is the body of POST /v1/context: one signal, or a list.
type ctxRequest struct {
	ctxInput
	Signals []ctxInput `json:"signals,omitempty"`
}

func (c *core) handleContext(w http.ResponseWriter, r *http.Request) {
	if clearFromInternet(r) {
		http.Error(w, errClearFromInternet, http.StatusForbidden)
		return
	}
	src, ok := c.contextSourceOf(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in ctxRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, ctxMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "a JSON object: kind, value, reason, ttl_minutes, or signals: [...]", http.StatusBadRequest)
		return
	}
	inputs := in.Signals
	if len(inputs) == 0 {
		inputs = []ctxInput{in.ctxInput}
	}
	if len(inputs) > ctxMaxSignals {
		http.Error(w, fmt.Sprintf("at most %d signals", ctxMaxSignals), http.StatusBadRequest)
		return
	}
	now := time.Now()
	sigs, err := checkSignals(inputs, c.contextPrefs().Rooms, src.key(), now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kept := c.putSignals(sigs)
	c.known.saw(src.key(), now)
	writeJSON(w, http.StatusOK, map[string]int{"accepted": kept, "ignored": len(sigs) - kept})
}

// checkSignals checks every input: one off its list refuses them all.
func checkSignals(inputs []ctxInput, rooms []string, source string, now time.Time) ([]ctxSignal, error) {
	out := make([]ctxSignal, 0, len(inputs))
	for i, in := range inputs {
		s, err := checkSignal(in, rooms, source, now)
		if err != nil {
			return nil, fmt.Errorf("signal %d: %w", i+1, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// nodeLevel is the level a node is asked to report at: what it may compute
// on its machine (GET /v1/node?context=N, node/context.go).
func (c *core) nodeLevel(name string) int {
	p := c.contextPrefs()
	if slices.Contains(p.Off, srcMachine+name) {
		return 0
	}
	return p.Level
}

// ctxOffShown is the sources turned off as the brain reads them.
func ctxOffShown(off string) string {
	if off == "" {
		return "none"
	}
	return off
}

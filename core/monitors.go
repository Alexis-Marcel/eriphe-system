package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Monitors (#357): what detects that something happened outside, and only
// that. A monitor writes events in the journal; a skill reacts to them with
// its when (skill_triggers.go). It never decides, acts or tells the user:
// no threshold (the system watched has its own), no judge, no action, no
// notification. That is what kept the removed rules from being only
// detection.
//
// A monitor that polls (way poll) has eriphe-core call one read-only tool
// of the catalog itself, with fixed arguments, every 1 minute to 1 day,
// without a model, and compare what it extracts with what it saw:
//
//   - new: the items of a list whose id was not seen give one monitor.new
//     each;
//   - changed: a value that is not the last one gives monitor.changed, once
//     a second look half a minute later saw the same: a flap gives nothing;
//   - cross: a number that goes above (or below) a limit, or back, gives
//     monitor.crossed, confirmed the same way: a measure becomes a state, so
//     47 % then 52 % is nothing, and 91 % is something.
//
// The first look is the baseline. Three failed looks in a row give
// monitor.failed, the next good one monitor.recovered. Its events are
// keyed by its name, and external unless the server is trusted.
//
// The tool must stay a free read of the brain's (read-only by its own
// annotation, lent to the brain, named by no rule of the policy): checked
// when the monitor is written and before every look; otherwise it waits,
// with why, and nothing is called. The user approves a monitor: the brain
// only proposes one (write_monitor), on a card.

const monitorsSchema = `CREATE TABLE IF NOT EXISTS monitors (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, state TEXT NOT NULL,
  spec TEXT NOT NULL DEFAULT '{}', pending TEXT NOT NULL DEFAULT '', -- a change the brain proposed, waiting for its card
  look TEXT NOT NULL DEFAULT '{}', next_at INTEGER NOT NULL DEFAULT 0,
  hook TEXT NOT NULL DEFAULT '', -- listen: its token's hash (incoming_hooks.go)
  tainted TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL, updated INTEGER NOT NULL);`

func init() { extraSchema = append(extraSchema, monitorsSchema) }

const (
	monitorProposed = "proposed" // written by the brain, never approved yet
	monitorOn       = "on"
	monitorPaused   = "paused"

	wayPoll     = "poll"
	wayListen   = "listen" // an incoming address (incoming_hooks.go)
	wayFollow   = "follow" // a server's own events, eriphe://events (event_sources.go)
	condNew     = "new"
	condChanged = "changed"
	condCross   = "cross"

	evMonitorNew       = "monitor.new"
	evMonitorChanged   = "monitor.changed"
	evMonitorCrossed   = "monitor.crossed"
	evMonitorFailed    = "monitor.failed"
	evMonitorRecovered = "monitor.recovered"

	monitorsTickEvery = 15 * time.Second
	monitorConfirm    = 30 * time.Second
	monitorTimeout    = 30 * time.Second
	monitorEveryMin   = time.Minute
	monitorEveryMax   = 24 * time.Hour
	monitorArgsMax    = 4 << 10
	monitorPathMax    = 200
	monitorFailsAfter = 3    // failed looks in a row before monitor.failed
	monitorSeenMax    = 1000 // ids a new monitor remembers
	monitorValueMax   = 4000 // characters of a value in an event
	monitorsMax       = 50
	cardMonitor       = "monitor"
)

var monitorNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// monitorSpec is what a monitor looks at, and how.
type monitorSpec struct {
	Way    string         `json:"way"`
	Server string         `json:"server"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args,omitempty"`
	Every  int            `json:"every"` // minutes
	Cond   string         `json:"cond"`
	Path   string         `json:"path,omitempty"`  // changed: the value; new: the list
	ID     string         `json:"id,omitempty"`    // new: an item's id, a path in the item
	Above  *float64       `json:"above,omitempty"` // cross: holds when the number is above
	Below  *float64       `json:"below,omitempty"` // cross: holds when the number is below
	// listen: the address checks a signature, in that header.
	Signed    bool   `json:"signed,omitempty"`
	SigHeader string `json:"sig_header,omitempty"`
}

// monitorLook is what its looks saw, kept between them.
type monitorLook struct {
	Known     bool     `json:"known,omitempty"` // the baseline was taken
	Value     string   `json:"value,omitempty"` // changed: the hash of the value
	Shown     string   `json:"shown,omitempty"` // changed: the value, cut, for was
	Seen      []string `json:"seen,omitempty"`  // new: the ids seen, the latest last
	Pending   bool     `json:"pending,omitempty"`
	PendValue string   `json:"pend_value,omitempty"`
	Hit       bool     `json:"hit,omitempty"` // cross: the limit was crossed at the last confirmed look
	PendHit   bool     `json:"pend_hit,omitempty"`
	Fails     int      `json:"fails,omitempty"`
	Failed    bool     `json:"failed,omitempty"` // monitor.failed was written
	Error     string   `json:"error,omitempty"`  // the last look's
	Waiting   string   `json:"waiting,omitempty"`
	Checked   int64    `json:"checked,omitempty"`
}

// monitor is one row of monitors.
type monitor struct {
	ID      int64
	Name    string
	State   string
	Spec    monitorSpec
	Pending *monitorSpec
	Look    monitorLook
	NextAt  time.Time
	Tainted string
	Created time.Time
}

const monitorCols = `id, name, state, spec, pending, look, next_at, tainted, created`

func scanMonitor(row interface{ Scan(...any) error }) (*monitor, error) {
	var m monitor
	var spec, pending, look string
	var next, created int64
	if err := row.Scan(&m.ID, &m.Name, &m.State, &spec, &pending, &look, &next, &m.Tainted, &created); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(spec), &m.Spec)
	if pending != "" {
		var p monitorSpec
		if json.Unmarshal([]byte(pending), &p) == nil {
			m.Pending = &p
		}
	}
	json.Unmarshal([]byte(look), &m.Look)
	m.NextAt, m.Created = time.Unix(next, 0), time.Unix(created, 0)
	return &m, nil
}

func (c *core) monitors() ([]*monitor, error) {
	rows, err := c.store.db.Query(`SELECT ` + monitorCols + ` FROM monitors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (c *core) monitorByID(id int64) (*monitor, error) {
	return scanMonitor(c.store.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE id = ?`, id))
}

func (c *core) monitorNamed(name string) (*monitor, error) {
	return scanMonitor(c.store.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE name = ?`, name))
}

// checkSpec checks a spec's shape, and fills its defaults.
func (s *monitorSpec) check() error {
	s.Server, s.Tool = strings.TrimSpace(s.Server), strings.TrimSpace(s.Tool)
	s.Path, s.ID = strings.TrimSpace(s.Path), strings.TrimSpace(s.ID)
	if s.Way == "" {
		s.Way = wayPoll
	}
	switch s.Way {
	case wayListen:
		*s = monitorSpec{Way: wayListen, Signed: s.Signed, SigHeader: s.SigHeader}
		return nil
	case wayFollow:
		if s.Server == "" {
			return errors.New("a monitor that follows names a catalog server that publishes events")
		}
		*s = monitorSpec{Way: wayFollow, Server: s.Server}
		return nil
	case wayPoll:
	default:
		return fmt.Errorf("way %q: poll, follow or listen", s.Way)
	}
	s.Signed, s.SigHeader = false, ""
	if s.Server == "" || s.Tool == "" {
		return errors.New("a monitor names a catalog server and one of its read-only tools")
	}
	if s.Every == 0 {
		s.Every = 5
	}
	if d := time.Duration(s.Every) * time.Minute; d < monitorEveryMin || d > monitorEveryMax {
		return errors.New("every is 1 minute to 1 day")
	}
	if raw, _ := json.Marshal(s.Args); len(raw) > monitorArgsMax {
		return fmt.Errorf("args are at most %d bytes", monitorArgsMax)
	}
	if len(s.Path) > monitorPathMax || len(s.ID) > monitorPathMax {
		return fmt.Errorf("path and id are at most %d characters", monitorPathMax)
	}
	if _, err := parsePath(s.Path); err != nil {
		return err
	}
	if s.Cond != condCross {
		s.Above, s.Below = nil, nil
	}
	switch s.Cond {
	case "", condChanged:
		s.Cond, s.ID = condChanged, ""
	case condCross:
		s.ID = ""
		if (s.Above == nil) == (s.Below == nil) {
			return errors.New("cross needs above or below: the limit a number crosses")
		}
	case condNew:
		if s.ID == "" {
			return errors.New("new needs id: the field that tells an item from another (id, uid)")
		}
		if _, err := parsePath(s.ID); err != nil {
			return err
		}
	default:
		return errors.New("cond is new, changed or cross")
	}
	return nil
}

// monitorWhy is why a monitor cannot look now, "" if it can.
func (c *core) monitorWhy(s monitorSpec) string {
	switch s.Way {
	case wayFollow:
		if _, ok := c.conf().Servers[s.Server]; !ok || c.pool.get(s.Server) == nil {
			return fmt.Sprintf("%s is not a server of the catalog", s.Server)
		}
		if !c.offersEvents(s.Server) {
			return fmt.Sprintf("%s publishes no events (eriphe://events), or is not connected yet", s.Server)
		}
		return ""
	case wayListen:
		return ""
	}
	return c.toolWhy(s)
}

// toolWhy is why eriphe-core may not call a monitor's tool by itself, ""
// if it may: a free read of the brain's.
func (c *core) toolWhy(s monitorSpec) string {
	if _, ok := c.conf().Servers[s.Server]; !ok || c.pool.get(s.Server) == nil {
		return fmt.Sprintf("%s is not a server of the catalog", s.Server)
	}
	if !grantAllows(c.brainGrant(), s.Server, s.Tool) {
		return fmt.Sprintf("%s.%s is not one of the brain's tools", s.Server, s.Tool)
	}
	name := claudeToolName(s.Server, s.Tool)
	if !c.pool.readOnly(name) {
		return fmt.Sprintf("%s.%s is not read-only (by its own annotation): a monitor only reads", s.Server, s.Tool)
	}
	if !c.freeCall(name, s.Args) {
		return fmt.Sprintf("%s.%s is not a free read in the user's grid: a monitor calls free reads only", s.Server, s.Tool)
	}
	return ""
}

// runMonitors is the monitors' loop.
func (c *core) runMonitors(ctx context.Context) {
	tick := time.NewTicker(monitorsTickEvery)
	defer tick.Stop()
	for {
		c.tickMonitors(ctx, c.notifyTime())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// tickMonitors looks for each monitor whose time has come.
func (c *core) tickMonitors(ctx context.Context, now time.Time) {
	ms, err := c.monitors()
	if err != nil {
		log.Printf("monitors: %v", err)
		return
	}
	for _, m := range ms {
		if m.State == monitorOn && m.Spec.Way == wayPoll && !m.NextAt.After(now) {
			c.lookMonitor(ctx, m, now)
		}
	}
}

// lookMonitor calls a monitor's tool once and writes what changed.
func (c *core) lookMonitor(ctx context.Context, m *monitor, now time.Time) {
	st := m.Look
	next := now.Add(time.Duration(m.Spec.Every) * time.Minute)
	defer func() {
		st.Checked = now.Unix()
		raw, _ := json.Marshal(st)
		c.store.db.Exec(`UPDATE monitors SET look = ?, next_at = ? WHERE id = ?`, string(raw), next.Unix(), m.ID)
	}()
	if why := c.toolWhy(m.Spec); why != "" {
		st.Waiting = why // its access changed: it waits, nothing is called
		return
	}
	st.Waiting = ""
	got, err := c.callMonitor(ctx, m.Spec)
	if !c.noteLook(m, &st, err) {
		st.Pending = false
		return
	}
	switch m.Spec.Cond {
	case condNew:
		c.lookNew(m, &st, got)
	case condCross:
		pending, err := c.lookCross(m, &st, got)
		if err != nil {
			st.Error = err.Error()
		} else if pending {
			next = now.Add(monitorConfirm)
		}
	default:
		if c.lookChanged(m, &st, got) {
			next = now.Add(monitorConfirm)
		}
	}
}

// noteLook keeps whether a look worked: three failures in a row write
// monitor.failed once, the next good look monitor.recovered. True if it
// worked.
func (c *core) noteLook(m *monitor, st *monitorLook, err error) bool {
	if err != nil {
		st.Error = truncate(err.Error(), 300)
		st.Fails++
		if st.Fails >= monitorFailsAfter && !st.Failed {
			st.Failed = true
			c.emitMonitor(m, evMonitorFailed, map[string]any{"error": st.Error})
		}
		return false
	}
	st.Error, st.Fails = "", 0
	if st.Failed {
		st.Failed = false
		c.emitMonitor(m, evMonitorRecovered, nil)
	}
	return true
}

// callMonitor calls the tool and extracts what the monitor compares.
func (c *core) callMonitor(ctx context.Context, s monitorSpec) (any, error) {
	u := c.pool.get(s.Server)
	if u == nil {
		return nil, errors.New("server gone")
	}
	var args json.RawMessage
	if s.Args != nil {
		args, _ = json.Marshal(s.Args)
	}
	ctx, cancel := context.WithTimeout(ctx, monitorTimeout)
	defer cancel()
	res, err := u.call(ctx, s.Tool, args)
	if err != nil {
		return nil, err
	}
	if res == nil || res.IsError {
		text := "the tool answered an error"
		if res != nil {
			text += ": " + truncate(resultText(res), 200)
		}
		return nil, errors.New(text)
	}
	return extract(resultValue(res), s.Path)
}

// lookChanged compares the value with the last one; true when a change
// waits for its second look.
func (c *core) lookChanged(m *monitor, st *monitorLook, got any) bool {
	text := valueText(got)
	h := sha256.Sum256([]byte(text))
	value := hex.EncodeToString(h[:])
	switch {
	case !st.Known:
		st.Known, st.Value, st.Shown = true, value, truncate(text, monitorValueMax) // the baseline
		return false
	case value == st.Value:
		st.Pending = false // back as it was, or never moved: a flap
		return false
	case !st.Pending || st.PendValue != value:
		st.Pending, st.PendValue = true, value // look again before telling
		return true
	}
	shown := truncate(text, monitorValueMax)
	c.emitMonitor(m, evMonitorChanged, map[string]any{"value": jsonValue(got, shown), "was": st.Shown})
	st.Pending, st.Value, st.Shown = false, value, shown
	return false
}

// lookCross tells when the number crosses the limit, either way; true
// when a crossing waits for its second look. Before the first look the
// limit counts as not crossed: a number already past it tells at once.
func (c *core) lookCross(m *monitor, st *monitorLook, got any) (bool, error) {
	n, ok := number(got)
	if !ok {
		return false, fmt.Errorf("%q is not a number: %s", cmpOr(m.Spec.Path, "the result"), truncate(valueText(got), 80))
	}
	hit := m.Spec.Above != nil && n > *m.Spec.Above || m.Spec.Below != nil && n < *m.Spec.Below
	switch {
	case hit == st.Hit:
		st.Known, st.Pending = true, false
		return false, nil
	case !st.Pending || st.PendHit != hit:
		st.Pending, st.PendHit = true, hit
		return true, nil
	}
	st.Known, st.Pending, st.Hit = true, false, hit
	p := map[string]any{"value": n, "holds": hit}
	if m.Spec.Above != nil {
		p["above"] = *m.Spec.Above
	} else {
		p["below"] = *m.Spec.Below
	}
	c.emitMonitor(m, evMonitorCrossed, p)
	return false, nil
}

// number reads a number, or a string that is one ("91.5", "91 %").
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(x), "%")), 64)
		return f, err == nil
	}
	return 0, false
}

// lookNew writes one event per item not seen before.
func (c *core) lookNew(m *monitor, st *monitorLook, got any) {
	items, ok := got.([]any)
	if !ok {
		st.Error = fmt.Sprintf("%q is not a list", cmpOr(m.Spec.Path, "the result"))
		return
	}
	seen := map[string]bool{}
	for _, id := range st.Seen {
		seen[id] = true
	}
	for _, it := range items {
		v, err := extract(it, m.Spec.ID)
		if err != nil || v == nil {
			continue
		}
		id := valueText(v)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		st.Seen = append(st.Seen, id)
		if st.Known {
			c.emitMonitor(m, evMonitorNew, map[string]any{"id": id, "item": jsonValue(it, truncate(valueText(it), monitorValueMax))})
		}
	}
	st.Known = true // the first look is the baseline
	if n := len(st.Seen); n > monitorSeenMax {
		st.Seen = st.Seen[n-monitorSeenMax:]
	}
}

// jsonValue keeps a value as data when it fits an event, else its text cut.
func jsonValue(v any, cut string) any {
	if raw, err := json.Marshal(v); err == nil && len(raw) <= monitorValueMax {
		return v
	}
	return cut
}

func (c *core) emitMonitor(m *monitor, kind string, p map[string]any) {
	if p == nil {
		p = map[string]any{}
	}
	p["monitor"] = m.Name
	c.emitLogged(journalEvent{Kind: kind, Key: m.Name, Source: "monitor:" + m.Name, Payload: p,
		External: !c.trustsServer(m.Spec.Server)})
}

// resultValue is a tool's result as data: its structured content, else its
// text read as JSON, else the text itself.
func resultValue(res *mcp.CallToolResult) any {
	if res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		var v any
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
	}
	text := resultText(res)
	var v any
	if json.Unmarshal([]byte(text), &v) == nil {
		return v
	}
	return text
}

func valueText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// A path picks what a monitor compares in a result:
//
//	data.state                 a field
//	items.0.name               an item of a list by its index
//	rules.*.name               every item of a list, then a field of each
//	rules[state=firing].name   the items whose field has that value
//
// Once a path went over a list (* or a filter) its value is a list, sorted
// so that the same items in another order are no change. Empty: the whole.

type pathStep struct {
	key    string // a field, or an index
	all    bool   // *
	field  string // a filter's field
	equals string // and its value
}

func parsePath(path string) ([]pathStep, error) {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$"), ".")
	var steps []pathStep
	bad := fmt.Errorf("path %q: fields joined by dots, * for every item, [field=value] to keep some", path)
	for path != "" {
		var seg string
		if path[0] == '[' {
			end := strings.IndexByte(path, ']')
			if end < 0 {
				return nil, bad
			}
			seg, path = path[:end+1], path[end+1:]
		} else {
			end := strings.IndexAny(path, ".[")
			if end < 0 {
				end = len(path)
			}
			seg, path = path[:end], path[end:]
		}
		path = strings.TrimPrefix(path, ".")
		switch {
		case seg == "*":
			steps = append(steps, pathStep{all: true})
		case strings.HasPrefix(seg, "["):
			f, v, ok := strings.Cut(seg[1:len(seg)-1], "=")
			if !ok || strings.TrimSpace(f) == "" {
				return nil, bad
			}
			steps = append(steps, pathStep{field: strings.TrimSpace(f), equals: strings.TrimSpace(v)})
		case seg == "":
			return nil, bad
		default:
			steps = append(steps, pathStep{key: seg})
		}
	}
	return steps, nil
}

// extract reads a path in JSON data.
func extract(v any, path string) (any, error) {
	steps, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur, many := []any{v}, false
	for _, st := range steps {
		var next []any
		for _, x := range cur {
			switch {
			case st.all:
				if l, ok := x.([]any); ok {
					next = append(next, l...)
				}
			case st.field != "":
				items := []any{x}
				if l, ok := x.([]any); ok {
					items = l
				}
				for _, it := range items {
					if o, ok := it.(map[string]any); ok && valueText(o[st.field]) == st.equals {
						next = append(next, it)
					}
				}
			default:
				switch y := x.(type) {
				case map[string]any:
					if got, ok := y[st.key]; ok {
						next = append(next, got)
					}
				case []any:
					if i, err := strconv.Atoi(st.key); err == nil && i >= 0 && i < len(y) {
						next = append(next, y[i])
					}
				}
			}
		}
		if st.all || st.field != "" {
			many = true
		}
		if !many && len(next) == 0 {
			return nil, fmt.Errorf("nothing at %q in the result", path)
		}
		cur = next
	}
	if !many {
		return cur[0], nil
	}
	sort.SliceStable(cur, func(i, j int) bool { return valueText(cur[i]) < valueText(cur[j]) })
	if cur == nil {
		cur = []any{}
	}
	return cur, nil
}

// Text says what a monitor does, in words, for its card and its list.
func (s monitorSpec) Text(lang string) string {
	if s.Way == wayFollow {
		return fmt.Sprintf(tr(lang, "monitor.text.follow"), s.Server)
	}
	if s.Way == wayListen {
		if s.Signed {
			return fmt.Sprintf(tr(lang, "monitor.text.listensigned"), s.SigHeader)
		}
		return tr(lang, "monitor.text.listen")
	}
	every := span(time.Duration(s.Every) * time.Minute)
	call := s.Server + "." + s.Tool
	if s.Cond == condNew {
		return fmt.Sprintf(tr(lang, "monitor.text.new"), every, call, cmpOr(s.Path, tr(lang, "monitor.text.whole")), s.ID)
	}
	if s.Cond == condCross {
		key, limit := "monitor.text.above", s.Above
		if s.Below != nil {
			key, limit = "monitor.text.below", s.Below
		}
		return fmt.Sprintf(tr(lang, key), every, call, cmpOr(s.Path, tr(lang, "monitor.text.whole")), strconv.FormatFloat(*limit, 'f', -1, 64))
	}
	return fmt.Sprintf(tr(lang, "monitor.text.changed"), every, call, cmpOr(s.Path, tr(lang, "monitor.text.whole")))
}

// writeMonitor saves a monitor the brain proposes: a new one waits for its
// card, proposed; a change of one waits as its pending spec.
func (c *core) writeMonitor(name string, s monitorSpec, tainted []string) (map[string]any, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !monitorNameRe.MatchString(name) {
		return nil, errors.New("a name of lowercase letters, digits and dashes: getout-prod")
	}
	if s.Way == wayListen {
		return nil, errors.New("you propose a monitor that polls a tool or follows a server's events: an address is made by the user, on the Monitors page (its token never goes through the conversation)")
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	if why := c.monitorWhy(s); why != "" {
		return nil, errors.New(why)
	}
	if err := c.oneFollower(name, s); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(s)
	now := time.Now().Unix()
	m, err := c.monitorNamed(name)
	switch {
	case err == sql.ErrNoRows:
		var n int
		c.store.db.QueryRow(`SELECT COUNT(*) FROM monitors`).Scan(&n)
		if n >= monitorsMax {
			return nil, fmt.Errorf("%d monitors at most", monitorsMax)
		}
		res, err := c.store.db.Exec(`INSERT INTO monitors (name, state, spec, tainted, created, updated) VALUES (?, ?, ?, ?, ?, ?)`,
			name, monitorProposed, string(raw), strings.Join(tainted, ", "), now, now)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		c.cardProposed(cardMonitor, id)
	case err != nil:
		return nil, err
	case m.Spec.Way == wayListen:
		return nil, fmt.Errorf("%q is an address the user made: pick another name", name)
	case m.State == monitorProposed:
		c.store.db.Exec(`UPDATE monitors SET spec = ?, tainted = ?, updated = ? WHERE id = ?`, string(raw), strings.Join(tainted, ", "), now, m.ID)
		c.cardProposed(cardMonitor, m.ID)
	default:
		old, _ := json.Marshal(m.Spec)
		if string(old) == string(raw) {
			return map[string]any{"result": "the same as the monitor already approved: nothing to approve"}, nil
		}
		c.store.db.Exec(`UPDATE monitors SET pending = ?, tainted = ?, updated = ? WHERE id = ?`, string(raw), strings.Join(tainted, ", "), now, m.ID)
		c.cardProposed(cardMonitor, m.ID)
	}
	return map[string]any{"result": "proposed; it waits for the user's approval on its card, then looks as it says. " +
		"A skill reacts to its events with when: " + evMonitorChanged + " key=" + name + " (or " + evMonitorNew + ", " + evMonitorCrossed + " holds=true, " + evMonitorFailed + ")",
		"monitor": name}, nil
}

// monitorProposal is a monitor's card, pending while the brain's proposal
// waits.
func (c *core) monitorProposal(p proposal) proposal {
	m, err := c.monitorByID(p.ID)
	if err != nil {
		return p
	}
	s := m.Spec
	switch {
	case m.Pending != nil:
		s = *m.Pending
	case m.State != monitorProposed:
		p.Outcome = cardApproved
		return p
	}
	p.Pending, p.Tainted = true, m.Tainted
	p.text = func(lang string) string {
		if m.State == monitorProposed {
			return fmt.Sprintf(tr(lang, "card.monitor.new"), m.Name)
		}
		return fmt.Sprintf(tr(lang, "card.monitor.update"), m.Name)
	}
	p.fields = func(lang string) []cardField {
		return []cardField{{Name: tr(lang, "card.monitor.what"), Value: s.Text(lang)},
			{Name: tr(lang, "card.monitor.events"), Value: fmt.Sprintf(tr(lang, "card.monitor.key"), m.Name)}}
	}
	p.Fields = p.fields("fr")
	return p
}

// proposedMonitors are the monitors whose card waits.
func (c *core) proposedMonitors() []int64 {
	rows, err := c.store.db.Query(`SELECT id FROM monitors WHERE state = ? OR pending != '' ORDER BY id`, monitorProposed)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// decideMonitor answers a monitor's card: yes turns on what it showed, from
// a new baseline; no drops it.
func (c *core) decideMonitor(id int64, approve bool) error {
	m, err := c.monitorByID(id)
	if err != nil || (m.State != monitorProposed && m.Pending == nil) {
		return errCardDecided
	}
	now := time.Now().Unix()
	switch {
	case !approve && m.State == monitorProposed:
		_, err = c.store.db.Exec(`DELETE FROM monitors WHERE id = ?`, id)
	case !approve:
		_, err = c.store.db.Exec(`UPDATE monitors SET pending = '', tainted = '', updated = ? WHERE id = ?`, now, id)
	case m.Pending != nil:
		raw, _ := json.Marshal(m.Pending)
		_, err = c.store.db.Exec(`UPDATE monitors SET spec = ?, pending = '', look = '{}', next_at = 0, tainted = '', updated = ? WHERE id = ?`,
			string(raw), now, id)
		c.freshStart(*m.Pending)
	default:
		_, err = c.store.db.Exec(`UPDATE monitors SET state = ?, look = '{}', next_at = 0, tainted = '', updated = ? WHERE id = ?`,
			monitorOn, now, id)
		c.freshStart(m.Spec)
	}
	return err
}

// monitorsSaid is the brain's view of the monitors.
func (c *core) monitorsSaid(lang string) []map[string]any {
	ms, _ := c.monitors()
	out := []map[string]any{}
	for _, m := range ms {
		e := map[string]any{"name": m.Name, "state": m.State, "does": m.Spec.Text(lang),
			"events": "key=" + m.Name}
		if m.Look.Checked > 0 {
			e["last_look"] = time.Unix(m.Look.Checked, 0).In(c.notifyTime().Location()).Format("2006-01-02 15:04")
		}
		if m.Look.Waiting != "" {
			e["waiting"] = m.Look.Waiting
		}
		if m.Look.Error != "" {
			e["error"] = m.Look.Error
		}
		if m.Look.Failed {
			e["failing"] = true
		}
		if m.Pending != nil {
			e["change_waiting"] = m.Pending.Text(lang)
		}
		out = append(out, e)
	}
	return out
}

type writeMonitorIn struct {
	Name   string         `json:"name" jsonschema:"lowercase letters, digits and dashes: getout-prod; the key of its events"`
	Way    string         `json:"way,omitempty" jsonschema:"poll (default): call a tool; follow: take the events a server publishes by itself (the mail server's mail.received), with server only"`
	Server string         `json:"server" jsonschema:"the catalog server (list_tools)"`
	Tool   string         `json:"tool,omitempty" jsonschema:"with poll: one of its read-only tools, a free read of yours"`
	Args   map[string]any `json:"args,omitempty" jsonschema:"its fixed arguments"`
	Every  string         `json:"every,omitempty" jsonschema:"how often it looks: 1m to 1d (2m, 15m, 1h); 5m if empty"`
	Cond   string         `json:"cond,omitempty" jsonschema:"changed (default): an event when the value at path changes; new: one event per item of the list at path not seen before; cross: an event when the number at path goes past above or below, and when it comes back (holds true or false)"`
	Path   string         `json:"path,omitempty" jsonschema:"what it compares in the result: fields joined by dots, * for every item of a list, [field=value] to keep some (data.rules[state=firing].name); the whole result if empty"`
	ID     string         `json:"id,omitempty" jsonschema:"with new: the field of an item that tells it from another (id, uid)"`
	Above  *float64       `json:"above,omitempty" jsonschema:"with cross: the limit the number goes above (90 for a CPU at 90 %)"`
	Below  *float64       `json:"below,omitempty" jsonschema:"with cross: the limit the number goes below (200 for a price under 200)"`
}

type nameIn struct {
	Name string `json:"name,omitempty"`
}

func registerMonitorTools(s *mcp.Server, c *core) {
	tool(s, "write_monitor", "Propose a monitor, or a change of one: eriphe-core itself calls one read-only tool of the catalog at an interval, without a model, and writes an event when what it watches changes (monitor.changed), when a list gets a new item (monitor.new), when a number crosses a limit either way (monitor.crossed, holds true or false), or when its looks keep failing (monitor.failed, then monitor.recovered); keyed by its name. It only detects: a skill reacts to its events (write_skill with when: monitor.changed key=<name>). Use it to watch a tool that does not tell by itself (« surveille les alertes Grafana de la prod »): never a time skill that calls the tool every few minutes. A server that publishes its own events (the mail server) is followed (way follow): its events keep their kind (mail.received), keyed by the monitor's name; nothing of it reaches a skill before a monitor follows it. Nothing looks before the user approves it on its card.",
		func(ctx context.Context, in writeMonitorIn) (map[string]any, error) {
			every := 0
			if e := strings.TrimSpace(in.Every); e != "" {
				d, err := parseMonitorEvery(e)
				if err != nil {
					return nil, err
				}
				every = int(d / time.Minute)
			}
			return c.writeMonitor(in.Name, monitorSpec{Way: cmpOr(strings.TrimSpace(in.Way), wayPoll), Server: in.Server, Tool: in.Tool, Args: in.Args, Every: every,
				Cond: strings.TrimSpace(in.Cond), Path: in.Path, ID: in.ID, Above: in.Above, Below: in.Below}, c.turnTaint())
		})
	tool(s, "monitors", "The user's monitors: each one's name (the key of its events), state (on, paused, proposed), what it does in words, its last look, why it waits or its last error, and a change waiting for the user's card.",
		func(ctx context.Context, in nameIn) (map[string]any, error) {
			all := c.monitorsSaid("fr")
			if in.Name != "" {
				all = slices.DeleteFunc(all, func(e map[string]any) bool { return e["name"] != in.Name })
			}
			return map[string]any{"monitors": all}, nil
		})
}

// parseMonitorEvery reads 2m, 1h, 1d, between a minute and a day.
func parseMonitorEvery(s string) (time.Duration, error) {
	m := skillEveryRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("every %q: a number and m, h or d", s)
	}
	n, _ := strconv.Atoi(m[1])
	d := time.Duration(n) * time.Minute
	switch m[2] {
	case "h":
		d = time.Duration(n) * time.Hour
	case "d", "j":
		d = time.Duration(n) * 24 * time.Hour
	}
	if d < monitorEveryMin || d > monitorEveryMax {
		return 0, fmt.Errorf("every %q: between 1m and 1d", s)
	}
	return d, nil
}

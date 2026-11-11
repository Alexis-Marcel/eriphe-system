package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The server says what each tool is, the user says what each kind of tool
// may do. A tool of the catalog falls in one category, by its server's MCP
// annotations (toolCategory); each category has a value, in a grid per
// server and per account; one tool may have its own value, an exception.
//
// A named level (access.go) reads as a grid (levelGrid): a server keeps its
// level until it is given a grid of its own.

// The categories, from the least to the most reaching.
const (
	catRead   = "read"
	catChange = "change"
	catDelete = "delete"
	catSend   = "send"
)

var categories = []string{catRead, catChange, catDelete, catSend}

// The values of a category.
const (
	gridFree     = "free"
	gridApproval = "approval"
	gridNever    = "never"
)

var gridValues = []string{gridFree, gridApproval, gridNever}

// accessGrid is the value of each category; a row left out has the default:
// reads free, the rest on approval.
type accessGrid struct {
	Read   string `json:"read,omitempty"`
	Change string `json:"change,omitempty"`
	Delete string `json:"delete,omitempty"`
	Send   string `json:"send,omitempty"`
}

// accountGrid is one account's grid, and its exceptions by tool name.
type accountGrid struct {
	Grid       accessGrid        `json:"grid"`
	Exceptions map[string]string `json:"exceptions,omitempty"`
}

func (g accessGrid) value(category string) string {
	v, fallback := g.Send, gridApproval
	switch category {
	case catRead:
		v, fallback = g.Read, gridFree
	case catChange:
		v = g.Change
	case catDelete:
		v = g.Delete
	}
	return cmpOr(v, fallback)
}

func (g accessGrid) check() error {
	for _, c := range categories {
		if !slices.Contains(gridValues, g.value(c)) {
			return fmt.Errorf("grid: %s must be free, approval or never", c)
		}
	}
	return nil
}

func checkExceptions(exceptions map[string]string) error {
	for tool, v := range exceptions {
		if !slices.Contains(gridValues, v) {
			return fmt.Errorf("exception %s must be free, approval or never", tool)
		}
	}
	return nil
}

// checkAccess refuses a grid or an exception that means nothing.
func (s Server) checkAccess() error {
	if s.Grid != nil {
		if err := s.Grid.check(); err != nil {
			return err
		}
	}
	if err := checkExceptions(s.Exceptions); err != nil {
		return err
	}
	for name, a := range s.AccountGrids {
		if err := a.Grid.check(); err != nil {
			return fmt.Errorf("account %s: %w", name, err)
		}
		if err := checkExceptions(a.Exceptions); err != nil {
			return fmt.Errorf("account %s: %w", name, err)
		}
	}
	return nil
}

// levelGrid is what a named level comes to.
func levelGrid(level string) accessGrid {
	switch level {
	case accessReadOnly:
		return accessGrid{gridFree, gridNever, gridNever, gridNever}
	case accessFree:
		return accessGrid{gridFree, gridFree, gridFree, gridFree}
	case accessAsk:
		return accessGrid{gridApproval, gridApproval, gridApproval, gridApproval}
	}
	return accessGrid{gridFree, gridApproval, gridApproval, gridApproval}
}

// gridLevel is the level closest to a grid, for what still speaks in levels
// (the workspace, a credential's scopes): read only when nothing but reads
// can happen, as the credential may then be read only.
func gridLevel(g accessGrid) string {
	above := []string{g.value(catChange), g.value(catDelete), g.value(catSend)}
	all := func(v string) bool {
		return !slices.ContainsFunc(above, func(x string) bool { return x != v })
	}
	switch {
	case all(gridNever):
		return accessReadOnly
	case all(gridFree) && g.value(catRead) == gridFree:
		return accessFree
	case all(gridApproval) && g.value(catRead) == gridApproval:
		return accessAsk
	}
	return accessReads
}

// toolCategory sorts a tool by its annotations, always read, with MCP's
// pessimistic defaults: what says nothing reaches outside, so an
// unannotated tool is Send. A server not to be believed is set to ask
// everything ("Tout demander"), or given exceptions per action.
func toolCategory(t *mcp.Tool) string {
	if t == nil || t.Annotations == nil {
		return catSend
	}
	a := t.Annotations
	switch {
	case a.ReadOnlyHint:
		return catRead
	case a.OpenWorldHint == nil || *a.OpenWorldHint:
		return catSend
	case a.DestructiveHint == nil || *a.DestructiveHint:
		return catDelete
	}
	return catChange
}

// serverAccess is what holds for a server, or for one of its accounts.
type serverAccess struct {
	grid       accessGrid
	exceptions map[string]string
	ceiling    bool // its credential reads only: no exception goes above reading
}

// of is a tool's category and value: its exception replaces its category's.
func (a serverAccess) of(t *mcp.Tool) (category, value string) {
	category = toolCategory(t)
	value = a.grid.value(category)
	if t != nil {
		if v, ok := a.exceptions[t.Name]; ok && (!a.ceiling || category == catRead) {
			value = v
		}
	}
	return category, value
}

// level is the level closest to it. An exception that lets a tool do more
// than read needs a credential that can: it is not read only. The level does
// not know the tools, so an exception on a read tool counts too: the grid
// still refuses the writes, the credential alone is wider than it could be.
func (a serverAccess) level() string {
	level := gridLevel(a.grid)
	if level == accessReadOnly && !a.ceiling {
		for _, v := range a.exceptions {
			if v != gridNever {
				return accessReads
			}
		}
	}
	return level
}

// baseAccess is a server's access, leaving its accounts aside.
func (s Server) baseAccess() serverAccess {
	if s.Grid != nil {
		return serverAccess{grid: *s.Grid, exceptions: s.Exceptions}
	}
	return serverAccess{grid: levelGrid(s.ownLevel()), exceptions: s.Exceptions}
}

// accountAccesses is the access of each account of a server of several; a
// server's own exceptions hold for them all, an account's replace them. An
// account whose credential cannot write (its ceiling) reads only, whatever
// its grid says.
func (s Server) accountAccesses() map[string]serverAccess {
	if len(s.AccountAccess) == 0 && len(s.AccountGrids) == 0 {
		return nil
	}
	out := map[string]serverAccess{}
	for name, level := range s.AccountAccess {
		out[name] = serverAccess{grid: levelGrid(level), exceptions: s.Exceptions}
	}
	for name, a := range s.AccountGrids {
		exceptions := maps.Clone(s.Exceptions)
		if exceptions == nil {
			exceptions = map[string]string{}
		}
		maps.Copy(exceptions, a.Exceptions)
		out[name] = serverAccess{grid: a.Grid, exceptions: exceptions}
	}
	for name := range s.readCeilings() {
		for k, a := range out {
			if strings.EqualFold(k, name) {
				out[k] = a.capped()
			}
		}
	}
	return out
}

// readCeilings are the accounts of a server whose credential reads only:
// signed in at a provider that was not granted the scopes that write (a
// Drive at drive.readonly). Above reading, their cases are greyed.
func (s Server) readCeilings() map[string]bool {
	svc, cfg, ok := accountsOf(s)
	if !ok || !svc.Scopes {
		return nil
	}
	out := map[string]bool{}
	for _, a := range cfg.Accounts {
		if t, ok := signInByAccount(svc.ID, a.Provider); ok && a.signedIn() {
			if _, write := t.granted(a.Scopes); !write {
				out[a.Name] = true
			}
		}
	}
	return out
}

// capped is an access that reads only: what its credential cannot do is
// never, whatever an exception says (of).
func (a serverAccess) capped() serverAccess {
	a.grid = accessGrid{Read: a.grid.Read, Change: gridNever, Delete: gridNever, Send: gridNever}
	a.ceiling = true
	return a
}

// with is the grid with one case set ("" says nothing).
func (g accessGrid) with(category, value string) accessGrid {
	switch category {
	case catRead:
		g.Read = value
	case catChange:
		g.Change = value
	case catDelete:
		g.Delete = value
	case catSend:
		g.Send = value
	}
	return g
}

// over is a grid with the cases another one sets: a place's exceptions
// over its account's grid.
func (g accessGrid) over(exceptions accessGrid) accessGrid {
	g.Read = cmpOr(exceptions.Read, g.Read)
	g.Change = cmpOr(exceptions.Change, g.Change)
	g.Delete = cmpOr(exceptions.Delete, g.Delete)
	g.Send = cmpOr(exceptions.Send, g.Send)
	return g
}

// placeAccess is what holds in one of a server's places: its account's
// grid with the place's own cases over it. An action's exception stays
// above both, and the account's ceiling above all.
func (s Server) placeAccess(p Place) serverAccess {
	a, ok := s.accountAccess(map[string]any{"account": p.Account})
	if !ok {
		a = s.baseAccess()
	}
	g, has := s.PlaceGrids[p.ID]
	if !has {
		return a
	}
	if a.ceiling {
		g.Change, g.Delete, g.Send = "", "", ""
	}
	a.grid = a.grid.over(g)
	return a
}

// namedPlace is the place a call of a server with PlaceArgs names: its
// arguments joined, lowercased; "" when one is missing.
func (s Server) namedPlace(input map[string]any) string {
	var parts []string
	for _, arg := range s.PlaceArgs {
		v, _ := input[arg].(string)
		if v = strings.TrimSpace(v); v == "" {
			return ""
		}
		parts = append(parts, v)
	}
	return strings.ToLower(strings.Join(parts, "/"))
}

// argPlaceAccess is what holds in a place a server names by its arguments
// (a repository): its grid with the place's cases over it. A server whose
// grid only reads is its places' ceiling: no case of theirs goes above.
func (s Server) argPlaceAccess(id string) serverAccess {
	a := s.baseAccess()
	if a.level() == accessReadOnly {
		return a
	}
	if g, ok := s.PlaceGrids[strings.ToLower(id)]; ok {
		a.grid = a.grid.over(g)
	}
	return a
}

// placeOf is the place a call names, if its server has places: by its
// argument, or the only one when it names none.
func (s Server) placeOf(input map[string]any) (Place, []Place, bool) {
	k, _, ok := keeperOf(s)
	if !ok {
		return Place{}, nil, false
	}
	list := placesIn("", s)
	named, _ := input[k.Arg].(string)
	named = strings.TrimSpace(named)
	for _, p := range list {
		if named != "" && strings.EqualFold(p.Name, named) || named == "" && len(list) == 1 {
			return p, list, true
		}
	}
	return Place{}, list, false
}

// accountAccess is the access of the account a call names, if the server
// has one by that name.
func (s Server) accountAccess(input map[string]any) (serverAccess, bool) {
	named, _ := input["account"].(string)
	named = strings.TrimSpace(named)
	if named == "" {
		return serverAccess{}, false
	}
	for name, a := range s.accountAccesses() {
		if strings.EqualFold(name, named) {
			return a, true
		}
	}
	return serverAccess{}, false
}

// toolFacts is what the grid says of a call: of the place it names, else
// of the account it names. A call that names neither takes the strictest
// of its places' values (of its accounts', without places); it is refused
// until it names one (noAccount) when they differ on anything but a read,
// or when one never reads.
func (s Server) toolFacts(t *mcp.Tool, input map[string]any) catalogFacts {
	p, places, hasPlaces := s.placeOf(input)
	if hasPlaces {
		return newFacts(s.placeAccess(p).of(t))
	}
	if id := s.namedPlace(input); id != "" {
		return newFacts(s.argPlaceAccess(id).of(t))
	}
	if a, ok := s.accountAccess(input); ok {
		return newFacts(a.of(t))
	}
	accounts := s.candidates(places)
	if len(accounts) == 0 {
		return newFacts(s.baseAccess().of(t))
	}
	var f catalogFacts
	differ, never := false, false
	for _, a := range accounts {
		category, value := a.of(t)
		differ = differ || (f.value != "" && f.value != value)
		never = never || value == gridNever
		if f.value == "" || slices.Index(gridValues, value) > slices.Index(gridValues, f.value) {
			f.value = value
		}
		if slices.Index(categories, category) > slices.Index(categories, f.category) {
			f.category = category
		}
	}
	f.read = f.category == catRead
	reads := t != nil && t.Annotations != nil && t.Annotations.ReadOnlyHint // by its own annotation
	f.noAccount = differ && (!reads || never)
	return f
}

// candidates are what a call naming nothing may reach: each place, or
// each account without places.
func (s Server) candidates(places []Place) []serverAccess {
	var out []serverAccess
	if len(places) > 0 {
		for _, p := range places {
			out = append(out, s.placeAccess(p))
		}
		return out
	}
	for _, a := range s.accountAccesses() {
		out = append(out, a)
	}
	return out
}

func newFacts(category, value string) catalogFacts {
	return catalogFacts{category: category, value: value, read: category == catRead}
}

// lends tells whether one of a server's tools is lent at all: Never in every
// account (in every place, with places) is neither listed nor called.
func lends(s Server, t *mcp.Tool) bool {
	_, places, _ := s.placeOf(nil)
	accounts := s.candidates(places)
	if len(s.PlaceArgs) > 0 {
		accounts = append(accounts, s.baseAccess())
		for id := range s.PlaceGrids {
			accounts = append(accounts, s.argPlaceAccess(id))
		}
	}
	if len(accounts) == 0 {
		_, v := s.baseAccess().of(t)
		return v != gridNever
	}
	for _, a := range accounts {
		if _, v := a.of(t); v != gridNever {
			return true
		}
	}
	return false
}

// legacyTrust moves the servers saved when a checkbox said whether their
// annotations were believed (the NAS, once): one not believed counted every
// tool as Send, so its grid, and each of its accounts' not believed, now
// says Send's value for every case, as before. Its exceptions stay.
func legacyTrust(store *Store) {
	const done = "legacy_trust_annotations"
	if store.get(done) != "" {
		return
	}
	list, err := store.catalogRows()
	if err != nil {
		return
	}
	flat := func(g accessGrid) accessGrid {
		v := g.value(catSend)
		return accessGrid{Read: v, Change: v, Delete: v, Send: v}
	}
	for _, r := range list {
		if r.Kind != kindServer {
			continue
		}
		var s Server
		var doc struct {
			Trust    bool `json:"trust_annotations"`
			Accounts map[string]struct {
				Trust *bool `json:"trust_annotations"`
			} `json:"account_grids"`
		}
		if json.Unmarshal(r.Spec, &s) != nil || json.Unmarshal(r.Spec, &doc) != nil {
			continue
		}
		changed := false
		if s.Grid != nil && !doc.Trust {
			g := flat(*s.Grid)
			s.Grid, changed = &g, true
		}
		for name, a := range s.AccountGrids {
			trusted := doc.Trust
			if t := doc.Accounts[name].Trust; t != nil {
				trusted = *t
			}
			if !trusted {
				a.Grid = flat(a.Grid)
				s.AccountGrids[name], changed = a, true
			}
		}
		if changed {
			if err := store.putCatalog(kindServer, r.Name, s); err != nil {
				log.Printf("catalog: %s: its annotations: %v", r.Name, err)
				return
			}
			log.Printf("catalog: %s: its annotations were not believed: every case is now what Send was", r.Name)
		}
	}
	store.set(done, "1")
}

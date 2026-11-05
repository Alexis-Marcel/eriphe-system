package main

import (
	"fmt"
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
	// Trust is whether this account believes the annotations; without it,
	// the server's trust_annotations says.
	Trust *bool `json:"trust_annotations,omitempty"`
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
func gridLevel(g accessGrid, trusted bool) string {
	above := []string{g.value(catChange), g.value(catDelete), g.value(catSend)}
	all := func(v string) bool {
		return !slices.ContainsFunc(above, func(x string) bool { return x != v })
	}
	switch {
	case !trusted:
		return accessAsk
	case all(gridNever):
		return accessReadOnly
	case all(gridFree) && g.value(catRead) == gridFree:
		return accessFree
	case all(gridApproval) && g.value(catRead) == gridApproval:
		return accessAsk
	}
	return accessReads
}

// toolCategory sorts a tool by its annotations, with MCP's pessimistic
// defaults: what says nothing reaches outside, so an unannotated tool, or
// any tool of a server whose annotations are not believed, is Send.
func toolCategory(t *mcp.Tool, trusted bool) string {
	if !trusted || t == nil || t.Annotations == nil {
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
	trusted    bool // the server's annotations are believed
}

// of is a tool's category and value: its exception replaces its category's.
func (a serverAccess) of(t *mcp.Tool) (category, value string) {
	category = toolCategory(t, a.trusted)
	value = a.grid.value(category)
	if t != nil {
		if v, ok := a.exceptions[t.Name]; ok {
			value = v
		}
	}
	return category, value
}

// level is the level closest to it. An exception that lets a tool do more
// than read needs a credential that can: it is not read only.
func (a serverAccess) level() string {
	level := gridLevel(a.grid, a.trusted)
	if level == accessReadOnly {
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
		return serverAccess{*s.Grid, s.Exceptions, s.TrustAnnotations}
	}
	level := s.ownLevel()
	return serverAccess{levelGrid(level), s.Exceptions, level != accessAsk}
}

// accountAccesses is the access of each account of a server of several; a
// server's own exceptions hold for them all, an account's replace them.
func (s Server) accountAccesses() map[string]serverAccess {
	if len(s.AccountAccess) == 0 && len(s.AccountGrids) == 0 {
		return nil
	}
	out := map[string]serverAccess{}
	for name, level := range s.AccountAccess {
		out[name] = serverAccess{levelGrid(level), s.Exceptions, level != accessAsk}
	}
	for name, a := range s.AccountGrids {
		exceptions := maps.Clone(s.Exceptions)
		if exceptions == nil {
			exceptions = map[string]string{}
		}
		maps.Copy(exceptions, a.Exceptions)
		trusted := s.TrustAnnotations
		if a.Trust != nil {
			trusted = *a.Trust
		}
		out[name] = serverAccess{a.Grid, exceptions, trusted}
	}
	return out
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

// toolFacts is what the grid says of a call. A call that names no account
// takes the strictest of its accounts' values; it is refused until it names
// one (noAccount) when they differ on anything but a read, or when one
// never reads.
func (s Server) toolFacts(t *mcp.Tool, input map[string]any) catalogFacts {
	if a, ok := s.accountAccess(input); ok {
		return newFacts(a.of(t))
	}
	accounts := s.accountAccesses()
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

func newFacts(category, value string) catalogFacts {
	return catalogFacts{category: category, value: value, read: category == catRead}
}

// lends tells whether one of a server's tools is lent at all: Never in every
// account is neither listed nor called.
func lends(s Server, t *mcp.Tool) bool {
	accounts := s.accountAccesses()
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

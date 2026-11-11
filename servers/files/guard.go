package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// The guard: the server reaches only the places it was configured with and
// what is inside them: folders of an account, or a whole account. Every id a
// tool is given is checked by walking its parents up through the provider (a
// file moved out of a place is out at once, within the cache's minute); a
// shortcut is judged by its target. Places may nest, and the deepest place
// holding an item decides: a call must name that place, so that a
// subfolder's own rules (eriphe-core's, by the place a call names) hold. A
// place itself is never modified nor trashed, only what is inside it.

// errOutside is the refusal of anything not in the place.
var errOutside = errors.New("this item is not in the folder: only the folders given to Eriphe can be reached")

// errDeeper is the refusal of an item of a nested place named through an
// ancestor: the call must name the deepest place holding it.
func errDeeper(name string) error {
	return fmt.Errorf("this item is in folder %q: name that folder", name)
}

const (
	parentsTTL = time.Minute
	maxDepth   = 64
	// myDrive is the provider's alias of the user's own drive: the top of a
	// whole account, where a file written at its top level goes.
	myDrive = "root"
)

// Files holds the accounts and places and serves the tools.
type Files struct {
	cfg      *Config
	accounts map[string]*account
	folders  []*Folder // every place: the whole accounts first, then the folders
}

type account struct {
	name     string
	p        Provider
	readOnly bool // the user lets Eriphe only read it

	places []*Folder // the account's folders given to Eriphe
	whole  *Folder   // the whole account, when it is given

	mu    sync.Mutex
	cache map[string]cachedItem
	now   func() time.Time
}

type cachedItem struct {
	it      Item
	expires time.Time
}

// Folder is one configured place, with its account: a folder, or the whole
// account (All), named after the account, whose ID is the provider's alias
// of the user's own drive.
type Folder struct {
	PlaceConfig
	acct *account
	All  bool
}

func NewFiles(cfg *Config) (*Files, error) {
	f := &Files{cfg: cfg, accounts: map[string]*account{}}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	for _, a := range cfg.Accounts {
		p, err := newProvider(a, timeout)
		if err != nil {
			return nil, fmt.Errorf("account %q: %w", a.Name, err)
		}
		acct := &account{name: a.Name, p: p, readOnly: a.ReadOnly, cache: map[string]cachedItem{}, now: time.Now}
		f.accounts[strings.ToLower(a.Name)] = acct
		if a.All {
			acct.whole = &Folder{PlaceConfig: PlaceConfig{Name: a.Name, Account: a.Name, ID: myDrive}, acct: acct, All: true}
			f.folders = append(f.folders, acct.whole)
		}
	}
	for _, fc := range cfg.Places {
		fo := &Folder{PlaceConfig: fc, acct: f.accounts[strings.ToLower(fc.Account)]}
		fo.acct.places = append(fo.acct.places, fo)
		f.folders = append(f.folders, fo)
	}
	return f, nil
}

// folder finds a place by name; empty means the only one.
func (f *Files) folder(name string) (*Folder, error) {
	name = strings.TrimSpace(name)
	if len(f.folders) == 0 {
		return nil, errors.New("no folder was given to Eriphe yet: the user picks them in the workspace")
	}
	if name == "" {
		if len(f.folders) == 1 {
			return f.folders[0], nil
		}
		return nil, fmt.Errorf("several folders: give folder (one of %s)", strings.Join(f.names(), ", "))
	}
	for _, fo := range f.folders {
		if strings.EqualFold(fo.Name, name) {
			return fo, nil
		}
	}
	return nil, fmt.Errorf("no folder %q (folders: %s)", name, strings.Join(f.names(), ", "))
}

func (f *Files) names() []string {
	var n []string
	for _, fo := range f.folders {
		n = append(n, fo.Name)
	}
	return n
}

func (f *Files) account(name string) (*account, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" && len(f.accounts) == 1 {
		for _, a := range f.accounts {
			return a, nil
		}
	}
	if a, ok := f.accounts[name]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("no account %q", name)
}

// get reads an item, from the cache when it is fresh.
func (a *account) get(ctx context.Context, id string) (Item, error) {
	a.mu.Lock()
	c, ok := a.cache[id]
	a.mu.Unlock()
	if ok && a.now().Before(c.expires) {
		return c.it, nil
	}
	it, err := a.p.Get(ctx, id)
	if err != nil {
		return Item{}, err
	}
	a.remember(it)
	return it, nil
}

// item reads an item like get; the provider's "Shared with me" is a folder
// of no parent, which only a whole account holds.
func (a *account) item(ctx context.Context, id string) (Item, error) {
	if id == sharedWithMe {
		return Item{ID: sharedWithMe, Name: "Shared with me", MimeType: mimeFolder, Folder: true}, nil
	}
	return a.get(ctx, id)
}

func (a *account) remember(items ...Item) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cache) > 50000 {
		clear(a.cache)
	}
	for _, it := range items {
		if it.ID != "" {
			a.cache[it.ID] = cachedItem{it, a.now().Add(parentsTTL)}
		}
	}
}

// ancestors walks id's parents up, breadth first, and gives the ids met,
// nearest first: id itself, then its parents, and so on (with the real id
// of an item named by an alias, such as "root"). found is false when id
// itself cannot be read. A parent the user cannot see ends that way up.
func (a *account) ancestors(ctx context.Context, id string) (ids []string, found bool, err error) {
	seen := map[string]bool{}
	todo := []string{id}
	for depth := 0; len(todo) > 0 && depth < maxDepth; depth++ {
		var next []string
		for _, cur := range todo {
			if seen[cur] {
				continue
			}
			seen[cur] = true
			ids = append(ids, cur)
			it, err := a.item(ctx, cur)
			if errors.Is(err, errNotFound) {
				continue // a parent the user cannot see: not this way
			}
			if err != nil {
				return nil, false, err
			}
			if depth == 0 {
				found = true
			}
			if it.ID != cur && !seen[it.ID] {
				seen[it.ID] = true
				ids = append(ids, it.ID)
			}
			next = append(next, it.Parents...)
		}
		todo = next
	}
	return ids, found, nil
}

// locate gives the places holding id, the deepest first: the folders met
// walking its parents up, then the whole account, which holds every item
// the provider can read. Nothing when no place holds it.
func (a *account) locate(ctx context.Context, id string) ([]*Folder, error) {
	ids, found, err := a.ancestors(ctx, id)
	if err != nil || !found {
		return nil, err
	}
	var out []*Folder
	for _, cur := range ids {
		for _, fo := range a.places {
			if fo.ID == cur && !slices.Contains(out, fo) {
				out = append(out, fo)
			}
		}
	}
	if a.whole != nil {
		out = append(out, a.whole)
	}
	return out, nil
}

// placeOf is the place of an item listed directly in place in: the place
// whose folder it is, or else in.
func (a *account) placeOf(id string, in *Folder) string {
	for _, fo := range a.places {
		if fo.ID == id {
			return fo.Name
		}
	}
	return in.Name
}

// check refuses id unless fo is the deepest place holding it.
func (fo *Folder) check(ctx context.Context, id string) error {
	places, err := fo.acct.locate(ctx, id)
	switch {
	case err != nil:
		return err
	case !slices.Contains(places, fo):
		return errOutside
	case places[0] != fo:
		return errDeeper(places[0].Name)
	}
	return nil
}

// top reads the place's own folder: for a whole account, the user's own
// drive.
func (fo *Folder) top(ctx context.Context) (Item, error) {
	it, err := fo.acct.get(ctx, fo.ID)
	if errors.Is(err, errNotFound) {
		return Item{}, fmt.Errorf("folder %q is gone or no longer shared with this account", fo.Name)
	}
	return it, err
}

// isTop tells whether id is the place's own folder; for a whole account,
// one of its top folders too (the user's own drive, Shared with me).
func (fo *Folder) isTop(ctx context.Context, id string) (bool, error) {
	if id == fo.ID {
		return true, nil
	}
	if !fo.All {
		return false, nil
	}
	if id == sharedWithMe {
		return true, nil
	}
	root, err := fo.acct.get(ctx, myDrive)
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	return root.ID == id, err
}

// item reads an item of the place, or refuses it: it must be in the place
// and in no deeper one. A shortcut is followed to its target, which must
// be held by the place the same way.
func (fo *Folder) item(ctx context.Context, id string) (Item, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == fo.ID {
		return fo.top(ctx)
	}
	if err := fo.check(ctx, id); err != nil {
		return Item{}, err
	}
	it, err := fo.acct.item(ctx, id)
	if errors.Is(err, errNotFound) {
		return Item{}, errOutside
	}
	if err != nil {
		return Item{}, err
	}
	if it.Trashed {
		return Item{}, errOutside
	}
	if it.Target != "" {
		return fo.item(ctx, it.Target)
	}
	return it, nil
}

// holdsPlace names a folder given to Eriphe inside the folder id, if any:
// trashing id would trash that place too, past its own rules.
func (fo *Folder) holdsPlace(ctx context.Context, id string) (string, error) {
	for _, p := range fo.acct.places {
		if p.ID == id {
			continue
		}
		ids, _, err := fo.acct.ancestors(ctx, p.ID)
		if err != nil {
			return "", err
		}
		if slices.Contains(ids, id) {
			return p.Name, nil
		}
	}
	return "", nil
}

// writable refuses a change in a place whose account may not change it.
func (fo *Folder) writable() error {
	switch {
	case fo.acct.readOnly:
		return fmt.Errorf("the account of folder %q is read only for Eriphe: the user can change that in the workspace", fo.Name)
	case !fo.acct.p.CanWrite():
		return errors.New("the account was connected for reading only: reconnect it in the workspace allowing Eriphe to change files")
	}
	return nil
}

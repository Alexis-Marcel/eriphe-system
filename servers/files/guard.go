package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The guard: the server reaches only the Folders it was configured with and
// what is inside them. Every id a tool is given is checked against the
// Folder it names by walking its parents up through the provider (a file
// moved out of the Folder is out at once, within the cache's minute); a
// shortcut is judged by its target. The server never modifies or deletes
// an item: it only creates new files, in the Eriphe/ subfolder of a Folder
// with the drop access.

// errOutside is the refusal of anything not in the Folder.
var errOutside = errors.New("this item is not in the folder: only the folders given to Eriphe can be reached")

const (
	parentsTTL = time.Minute
	maxDepth   = 64
)

// Files holds the accounts and Folders and serves the tools.
type Files struct {
	cfg      *Config
	accounts map[string]*account
	folders  []*Folder
}

type account struct {
	name string
	p    Provider

	mu    sync.Mutex
	cache map[string]cachedItem
	now   func() time.Time
}

type cachedItem struct {
	it      Item
	expires time.Time
}

// Folder is one configured Folder, with its account.
type Folder struct {
	FolderConfig
	acct *account

	mu      sync.Mutex
	results string // the id of its Eriphe/ subfolder, once found
}

func NewFiles(cfg *Config) (*Files, error) {
	f := &Files{cfg: cfg, accounts: map[string]*account{}}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	for _, a := range cfg.Accounts {
		p, err := newProvider(a, timeout)
		if err != nil {
			return nil, fmt.Errorf("account %q: %w", a.Name, err)
		}
		f.accounts[strings.ToLower(a.Name)] = &account{name: a.Name, p: p, cache: map[string]cachedItem{}, now: time.Now}
	}
	for _, fc := range cfg.Folders {
		f.folders = append(f.folders, &Folder{FolderConfig: fc, acct: f.accounts[strings.ToLower(fc.Account)]})
	}
	return f, nil
}

// folder finds a Folder by name; empty means the only one.
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

// within tells whether id is the Folder itself or inside it.
func (fo *Folder) within(ctx context.Context, id string) (bool, error) {
	seen := map[string]bool{}
	todo := []string{id}
	for depth := 0; len(todo) > 0 && depth < maxDepth; depth++ {
		var next []string
		for _, cur := range todo {
			if cur == fo.ID {
				return true, nil
			}
			if seen[cur] {
				continue
			}
			seen[cur] = true
			it, err := fo.acct.get(ctx, cur)
			if errors.Is(err, errNotFound) {
				continue // a parent the user cannot see: not this way
			}
			if err != nil {
				return false, err
			}
			if it.ID == fo.ID { // an alias, such as "root", read as the real id
				return true, nil
			}
			next = append(next, it.Parents...)
		}
		todo = next
	}
	return false, nil
}

// item reads an item of the Folder, or refuses it. A shortcut is followed
// to its target, which must be in the Folder too.
func (fo *Folder) item(ctx context.Context, id string) (Item, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == fo.ID {
		it, err := fo.acct.get(ctx, fo.ID)
		if errors.Is(err, errNotFound) {
			return Item{}, fmt.Errorf("folder %q is gone or no longer shared with this account", fo.Name)
		}
		return it, err
	}
	in, err := fo.within(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if !in {
		return Item{}, errOutside
	}
	it, err := fo.acct.get(ctx, id)
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

// resultsFolder finds or makes the Folder's Eriphe/ subfolder.
func (fo *Folder) resultsFolder(ctx context.Context) (string, error) {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	if fo.results != "" {
		return fo.results, nil
	}
	token := ""
	for range 20 {
		items, next, err := fo.acct.p.List(ctx, fo.ID, true, token)
		if err != nil {
			return "", err
		}
		for _, it := range items {
			if it.Folder && it.Name == resultsFolder {
				fo.results = it.ID
				return it.ID, nil
			}
		}
		if token = next; token == "" {
			break
		}
	}
	it, err := fo.acct.p.CreateFolder(ctx, fo.ID, resultsFolder)
	if err != nil {
		return "", err
	}
	fo.acct.remember(it)
	fo.results = it.ID
	return it.ID, nil
}

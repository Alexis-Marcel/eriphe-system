package main

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Resources: what a task is lent, as one typed list. Each entry is a kind,
// a reference to one thing the user opened to Eriphe, and an access on one
// scale for every kind:
//
//	read < drop < write
//
// read only reads; drop also creates new files in an Eriphe-owned place
// (a folder's results subfolder, results_folder.go), never changing the originals; write
// changes the thing itself (for a repository: push; for a folder shared to
// manage: create, change, rename and delete in it). Each kind takes the
// rights that make sense for it (resourceKinds), and an entry never gets
// more than the user opened (a folder's own access).
//
//	{"kind": "repo",         "ref": "owner/site", "access": "write", "branch": "eriphe/fix", "base": "main"}
//	{"kind": "folder",       "ref": "Clients",    "access": "drop"}
//	{"kind": "place",        "ref": "Factures",   "access": "read"}
//	{"kind": "tool",         "ref": "github.get_*"}
//	{"kind": "access",       "ref": "npm"}
//
// The list is kept with the task (tasks.resources), and the node gets its
// spec fields (repo, folders…), derived from it.
// The same list decides the task's credentials (repo_lend.go) and what
// a tainted turn may not lend (taintedResources).

// The kinds of resources.
const (
	resRepo   = "repo"
	resFolder = "folder"
	resTool   = "tool"
	resAccess = "access"
)

// accessDrop is the middle of the scale (accessRead and accessWrite are in
// repo_lend.go).
const accessDrop = "drop"

var accessRank = map[string]int{accessRead: 1, accessDrop: 2, accessWrite: 3}

// atLeast tells whether access a covers access b.
func atLeast(a, b string) bool { return accessRank[a] >= accessRank[b] && accessRank[b] > 0 }

// resourceKinds are the kinds and the rights each takes.
var resourceKinds = map[string][]string{
	resRepo:   {accessRead, accessWrite},
	resFolder: {accessRead, accessDrop, accessWrite},
	resPlace:  {accessRead, accessDrop},
	resTool:   {accessRead, accessWrite},
	resAccess: {}, // it has no scale: what it can do is the provider's (access.go)
}

// ResourceRef is one thing lent to a task. Branch, Base and Dir are a
// repository's only.
type ResourceRef struct {
	Kind   string `json:"kind" jsonschema:"repo, folder, place, tool, access or file"`
	Ref    string `json:"ref" jsonschema:"what it is: repo owner/name or a git URL, one of list_repos; folder or place its name in list_folders; tool a catalog server or server.tool with * globs (list_tools); access (a credential) its name in list_tools' accesses"`
	Access string `json:"access,omitempty" jsonschema:"read, drop or write (read < drop < write). repo: read, or write to push; folder: read, drop to add results in its results subfolder, named after the assistant, or write to change the folder itself (a folder shared to manage; its machine takes a snapshot first); place: read or drop; at most the place's own level; tool: read or write, from the tools' annotations when empty; file: read; access: none, leave it empty. Default read"`
	Branch string `json:"branch,omitempty" jsonschema:"repo: the work branch to create and push to; with access write, the user is asked on a card unless it is their own clean turn"`
	Base   string `json:"base,omitempty" jsonschema:"repo: the branch or tag to start from; the default branch if empty"`
	Dir    string `json:"dir,omitempty" jsonschema:"repo: the working directory inside the repository, if not its root"`
	// TrustSkills loads the agent skills a repository carries even when it
	// is not the user's own (repo_skills.go).
	TrustSkills bool `json:"trust_skills,omitempty" jsonschema:"repo: also load the agent skills the repository carries (.claude/skills, .agents/skills, .opencode/skills) when it is not the user's own; the user's own repositories load theirs anyway. Only for a repository the user said they trust"`
	// Hold lends a tool held back (lend.go): the task sees it, but none of
	// its calls is authorized until the user approves its release on a card.
	Hold bool `json:"hold,omitempty" jsonschema:"tool or access: lend it held back, for work the user validates before it acts: the task sees it, but nothing of it is authorized until the user approves its release on a card (answer_task with release). It may be lent on a tainted turn"`
}

// key tells two entries for the same thing apart from different ones.
func (r ResourceRef) key() string {
	switch r.Kind {
	case resFolder, resPlace:
		return r.Kind + "|" + strings.ToLower(r.Ref)
	}
	return r.Kind + "|" + r.Ref
}

func (r ResourceRef) String() string {
	s := r.Kind + " " + r.Ref
	if r.Access != "" {
		s += " (" + r.Access + ")"
	}
	if r.Hold {
		s += ", held"
	}
	return s
}

// normalizeResources reads a request's resources into one list, checked for
// its form only: what each entry names is checked by resolveResources. An
// assistant's tools get an empty access, filled there from their annotations.
func normalizeResources(r runRequest) ([]ResourceRef, error) {
	var out []ResourceRef
	for i, in := range r.Resources {
		e := ResourceRef{Kind: strings.ToLower(strings.TrimSpace(in.Kind)), Ref: strings.TrimSpace(in.Ref),
			Access: strings.ToLower(strings.TrimSpace(in.Access)),
			Branch: strings.TrimSpace(in.Branch), Base: strings.TrimSpace(in.Base), Dir: strings.TrimSpace(in.Dir),
			TrustSkills: in.TrustSkills, Hold: in.Hold}
		rights, ok := resourceKinds[e.Kind]
		if !ok {
			return nil, fmt.Errorf("resource %d: kind %q is not one of repo, folder, place, tool, access", i+1, in.Kind)
		}
		if e.Ref == "" {
			return nil, fmt.Errorf("resource %d (%s): ref is required", i+1, e.Kind)
		}
		if e.Access != "" && e.Kind == resAccess {
			return nil, fmt.Errorf("resource %s: an access takes no access level: what it can do is set at the provider", e.Ref)
		}
		if e.Access != "" && !slices.Contains(rights, e.Access) {
			return nil, fmt.Errorf("resource %s: a %s takes access %s, not %q", e.Ref, e.Kind, strings.Join(rights, " or "), e.Access)
		}
		if e.Kind != resRepo && (e.Branch != "" || e.Base != "" || e.Dir != "" || e.TrustSkills) {
			return nil, fmt.Errorf("resource %s: branch, base, dir and trust_skills are a repo's only", e.Ref)
		}
		if e.Hold && e.Kind != resTool && e.Kind != resAccess {
			return nil, fmt.Errorf("resource %s: only a tool or an access is lent held back (hold)", e.Ref)
		}
		if e.Access == "" && e.Kind != resTool && e.Kind != resAccess {
			e.Access = accessRead // least privilege; a tool's comes from its annotations
		}
		out = append(out, e)
	}
	for _, g := range r.Tools {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, ResourceRef{Kind: resTool, Ref: g})
		}
	}
	repos := 0
	for _, e := range out {
		if e.Kind == resRepo {
			repos++
			if err := checkRepoParams(e); err != nil {
				return nil, err
			}
		}
	}
	if repos > 1 {
		return nil, fmt.Errorf("one repository per task: start one task per repository")
	}
	return out, nil
}

func checkRepoParams(e ResourceRef) error {
	for what, v := range map[string]string{"branch": e.Branch, "base": e.Base} {
		if strings.HasPrefix(v, "-") || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == '~' || r == '^' || r == ':' || r == '\\' }) {
			return fmt.Errorf("resource %s: %s %q is not a branch name", e.Ref, what, v)
		}
	}
	if e.Dir != "" && (path.IsAbs(e.Dir) || slices.Contains(strings.Split(path.Clean(e.Dir), "/"), "..")) {
		return fmt.Errorf("resource %s: dir %q must be a path inside the repository", e.Ref, e.Dir)
	}
	return nil
}

// lentResources is a request's resources, checked against what the user
// opened, and what follows from them for the task.
type lentResources struct {
	List    []ResourceRef // normalized, as kept with the task
	Repo    *ResourceRef  // its repository, if any
	Folders []*Folder     // its machine's folders, each with the access lent (≤ the folder's own)
	Machine string        // the machine of its folders
	Grant   []string      // its catalog grant: the tools, and the tool of a place
}

// resolveResources checks each entry against what the user opened and
// fills the accesses left empty.
func (c *core) resolveResources(list []ResourceRef) (*lentResources, error) {
	out := &lentResources{}
	var names, grant []string
	lentAccess := map[string]string{} // a folder's access, by its lowercased name
	for _, e := range list {
		switch e.Kind {
		case resRepo:
			// A repository given to Eriphe: a place of a git
			// connection (connection_places.go), at most at its access.
			repo := repoOf(e.Ref)
			p := c.repoPlace(repo)
			if repo == "" || p == nil {
				return nil, fmt.Errorf("repository %q is not given to Eriphe: the user gives it in Settings → Tools (see list_repos)", e.Ref)
			}
			if !atLeast(p.Access, e.Access) {
				return nil, fmt.Errorf("repository %q is given to Eriphe for %s only: it cannot be lent %s", p.ID, p.Access, e.Access)
			}
		case resFolder:
			f, err := c.store.folder(e.Ref)
			if err != nil {
				if p := c.placeNamed(e.Ref); p != nil {
					return nil, fmt.Errorf("folder %q is a place of the tool %s: lend it as {kind: place, ref: %q}", e.Ref, p.Tool, e.Ref)
				}
				return nil, fmt.Errorf("folder %q: unknown (see list_folders)", e.Ref)
			}
			e.Ref = f.Name
			own := resourceAccess(f.Access)
			if !atLeast(own, e.Access) {
				return nil, fmt.Errorf("folder %q is opened to Eriphe for %s only: it cannot be lent %s", f.Name, f.Access, e.Access)
			}
			names = append(names, f.Name)
		case resPlace:
			p := c.placeNamed(e.Ref)
			if p == nil {
				if _, err := c.store.folder(e.Ref); err == nil {
					return nil, fmt.Errorf("folder %q is on a machine: lend it as {kind: folder, ref: %q}", e.Ref, e.Ref)
				}
				return nil, fmt.Errorf("place %q: unknown (see list_folders)", e.Ref)
			}
			e.Ref = p.Name
			if !atLeast(p.Access, e.Access) {
				return nil, fmt.Errorf("place %q is opened to Eriphe for %s only: it cannot be lent %s", p.Name, p.Access, e.Access)
			}
			// Its tool is lent whole: it reaches every place given to
			// Eriphe, and enforces each one's access itself.
			grant = append(grant, p.Tool)
		case resTool:
			if _, err := c.conf().checkGrant([]string{e.Ref}); err != nil {
				return nil, err
			}
			acts := len(c.actingGrant([]string{e.Ref})) > 0
			switch {
			case e.Access == "" && acts:
				e.Access = accessWrite
			case e.Access == "":
				e.Access = accessRead
			case e.Access == accessRead && acts:
				return nil, fmt.Errorf("tool %s: its tools may act (or are not listed yet): lend it with access write, or only its read-only tools", e.Ref)
			}
			grant = append(grant, e.Ref)
		case resAccess:
			if _, ok := c.conf().Accesses[e.Ref]; !ok {
				return nil, errNoAccess(e.Ref)
			}
		}
		// The same thing twice: once, with the higher access.
		if i := slices.IndexFunc(out.List, func(o ResourceRef) bool { return o.key() == e.key() }); i >= 0 {
			if accessRank[e.Access] > accessRank[out.List[i].Access] {
				out.List[i].Access = e.Access
			}
			out.List[i].TrustSkills = out.List[i].TrustSkills || e.TrustSkills
			out.List[i].Hold = out.List[i].Hold && e.Hold // lent outright once is lent outright
		} else {
			out.List = append(out.List, e)
		}
	}
	if err := c.checkLentAccesses(out.List); err != nil {
		return nil, err
	}
	for _, e := range out.List {
		if e.Kind == resFolder {
			lentAccess[strings.ToLower(e.Ref)] = e.Access
		}
		if e.Kind == resRepo {
			r := e
			out.Repo = &r
		}
	}
	fs, machine, err := c.folderGrant(names)
	if err != nil {
		return nil, err
	}
	for _, f := range fs {
		g := *f
		g.Access = folderAccess(lentAccess[strings.ToLower(f.Name)])
		out.Folders = append(out.Folders, &g)
	}
	out.Machine = machine
	if out.Grant, err = c.conf().checkGrant(grant); err != nil {
		return nil, err
	}
	if out.List == nil {
		out.List = []ResourceRef{} // kept as [], not empty: the task has resources, none
	}
	return out, nil
}

// folderAccess is the access a folder is lent with, as folders name it:
// write is manage.
func folderAccess(access string) string {
	switch access {
	case accessDrop:
		return folderDrop
	case accessWrite:
		return folderManage
	}
	return folderRead
}

// resourceAccess is a folder's level on the resources' scale: manage is
// write.
func resourceAccess(level string) string {
	switch level {
	case folderDrop:
		return accessDrop
	case folderManage:
		return accessWrite
	}
	return accessRead
}

// resolveRequest is resolveResources for a request.
func (c *core) resolveRequest(r runRequest) (*lentResources, error) {
	list, err := normalizeResources(r)
	if err != nil {
		return nil, err
	}
	return c.resolveResources(list)
}

func marshalResources(list []ResourceRef) string {
	if list == nil {
		return "" // a task from before resources
	}
	raw, _ := json.Marshal(list)
	return string(raw)
}

func unmarshalResources(raw string) []ResourceRef {
	if raw == "" {
		return nil
	}
	out := []ResourceRef{}
	json.Unmarshal([]byte(raw), &out)
	return out
}

// taintedResources refuses, during a tainted turn of the brain, a task
// lent a folder that writes, or a place (its tool is lent
// whole for now). Reads still go, and tools, accesses and a write to a GitHub
// repository: what they may do then is asked on a card at launch
// (access_card.go, task_start.go). My own message
// does not lift it: the brain asks me, and starts the task in the turn of
// my answer.
func (c *core) taintedResources(r runRequest) error {
	tainted := c.turnTaint()
	if len(tainted) == 0 {
		return nil
	}
	refuse := func(what string) error {
		return fmt.Errorf("refused: this turn read external content (%s), and the task would be lent %s: "+
			"ask %s first, and launch it in the turn of their answer", strings.Join(tainted, ", "), what, c.conf().User)
	}
	lent, err := c.resolveRequest(r)
	if err != nil {
		return err
	}
	var writes, acting []string
	for _, e := range lent.List {
		if e.Kind == resTool || e.Kind == resAccess {
			continue // what it may do is asked on a card (access_card.go)
		}
		if e.TrustSkills {
			// A repository's skills are instructions and scripts: trusting
			// them is the user's call, not a turn's that read someone else's words.
			return refuse("the skills of " + e.Ref + " (trust_skills)")
		}
		switch {
		case isRepoRef(e): // a write is asked on the start's card (task_start.go)
		case e.Kind == resFolder: // a write is asked on the start's card (folder_values.go)
		case e.Access == accessWrite || e.Access == accessDrop:
			writes = append(writes, e.String())
		case e.Kind == resPlace:
			if p := c.placeNamed(e.Ref); p != nil && len(c.actingGrant([]string{p.Tool})) > 0 {
				acting = append(acting, e.String()+" through "+p.Tool+", which can write in every place")
			}
		}
	}
	if len(writes) > 0 {
		return refuse("the right to write (" + strings.Join(writes, ", ") + ")")
	}
	if len(acting) > 0 {
		return refuse("tools that act (" + strings.Join(acting, ", ") + ")")
	}
	return nil
}

// folderNames are the names of a task's folders, as tasks.folders
// keeps them.
func (l *lentResources) folderNames() string { return strings.Join(folderNames(l.Folders), ",") }

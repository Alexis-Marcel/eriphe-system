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
// (a folder's Eriphe/ subfolder), never changing the originals; write
// changes the thing itself (for a repository: push). Each kind takes the
// rights that make sense for it (resourceKinds), and an entry never gets
// more than the user opened (a folder's own access).
//
//	{"kind": "repo",         "ref": "owner/site", "access": "write", "branch": "eriphe/fix", "base": "main"}
//	{"kind": "folder",       "ref": "Clients",    "access": "drop"}
//	{"kind": "drive-folder", "ref": "Factures",   "access": "read"}
//	{"kind": "tool",         "ref": "github.get_*"}
//	{"kind": "file",         "ref": "att_…"}
//
// The old fields of run_task (repo, ref, branch, dir; tools; folders) are
// read into the same list at the edge (normalizeResources). The list is
// kept with the task (tasks.resources); the old columns are still written,
// and the node still gets its spec fields (repo, folders…), derived from it.
// The same list decides the task's credentials (credentials.go) and what
// a tainted turn may not lend (taintedResources).

// The kinds of resources.
const (
	resRepo        = "repo"
	resFolder      = "folder"
	resDriveFolder = "drive-folder"
	resTool        = "tool"
	resFile        = "file"
)

// accessDrop is the middle of the scale (accessRead and accessWrite are in
// credentials.go).
const accessDrop = "drop"

var accessRank = map[string]int{accessRead: 1, accessDrop: 2, accessWrite: 3}

// atLeast tells whether access a covers access b.
func atLeast(a, b string) bool { return accessRank[a] >= accessRank[b] && accessRank[b] > 0 }

// resourceKinds are the kinds and the rights each takes.
var resourceKinds = map[string][]string{
	resRepo:        {accessRead, accessWrite},
	resFolder:      {accessRead, accessDrop},
	resDriveFolder: {accessRead, accessDrop},
	resTool:        {accessRead, accessWrite},
	resFile:        {accessRead},
}

// ResourceRef is one thing lent to a task. Branch, Base and Dir are a
// repository's only.
type ResourceRef struct {
	Kind   string `json:"kind" jsonschema:"repo, folder, drive-folder, tool or file"`
	Ref    string `json:"ref" jsonschema:"what it is: repo owner/name on GitHub or a git URL; folder or drive-folder its name in list_folders; tool a catalog server or server.tool with * globs (list_tools); file an attachment id (att_…)"`
	Access string `json:"access,omitempty" jsonschema:"read, drop or write (read < drop < write). repo: read, or write to push; folder and drive-folder: read, or drop to add results in its Eriphe/ subfolder (at most the folder's own access); tool: read or write, from the tools' annotations when empty; file: read. Default read"`
	Branch string `json:"branch,omitempty" jsonschema:"repo: the work branch to create; with access write, the first push to it needs no approval"`
	Base   string `json:"base,omitempty" jsonschema:"repo: the branch or tag to start from; the default branch if empty"`
	Dir    string `json:"dir,omitempty" jsonschema:"repo: the working directory inside the repository, if not its root"`
}

// key tells two entries for the same thing apart from different ones.
func (r ResourceRef) key() string {
	switch r.Kind {
	case resFolder, resDriveFolder:
		return r.Kind + "|" + strings.ToLower(r.Ref)
	}
	return r.Kind + "|" + r.Ref
}

func (r ResourceRef) String() string {
	s := r.Kind + " " + r.Ref
	if r.Access != "" {
		s += " (" + r.Access + ")"
	}
	return s
}

// normalizeResources reads a request's resources and its old fields into
// one list, checked for its form only: what each entry names is checked
// by resolveResources. Old folders and tools get an empty access, filled
// there: a folder's own access, the tools' annotations.
func normalizeResources(r runRequest) ([]ResourceRef, error) {
	var out []ResourceRef
	for i, in := range r.Resources {
		e := ResourceRef{Kind: strings.ToLower(strings.TrimSpace(in.Kind)), Ref: strings.TrimSpace(in.Ref),
			Access: strings.ToLower(strings.TrimSpace(in.Access)),
			Branch: strings.TrimSpace(in.Branch), Base: strings.TrimSpace(in.Base), Dir: strings.TrimSpace(in.Dir)}
		rights, ok := resourceKinds[e.Kind]
		if !ok {
			return nil, fmt.Errorf("resource %d: kind %q is not one of repo, folder, drive-folder, tool, file", i+1, in.Kind)
		}
		if e.Ref == "" {
			return nil, fmt.Errorf("resource %d (%s): ref is required", i+1, e.Kind)
		}
		if e.Access != "" && !slices.Contains(rights, e.Access) {
			return nil, fmt.Errorf("resource %s: a %s takes access %s, not %q", e.Ref, e.Kind, strings.Join(rights, " or "), e.Access)
		}
		if e.Kind != resRepo && (e.Branch != "" || e.Base != "" || e.Dir != "") {
			return nil, fmt.Errorf("resource %s: branch, base and dir are a repo's only", e.Ref)
		}
		if e.Access == "" && e.Kind != resTool {
			e.Access = accessRead // least privilege; a tool's comes from its annotations
		}
		out = append(out, e)
	}
	// The old fields. A repository lent through them was always "first
	// push asks": that is read here, whose writes ask the user.
	if repo := strings.TrimSpace(r.Repo); repo != "" {
		out = append(out, ResourceRef{Kind: resRepo, Ref: repo, Access: accessRead,
			Branch: strings.TrimSpace(r.Branch), Base: strings.TrimSpace(r.Ref), Dir: strings.TrimSpace(r.Dir)})
	}
	for _, g := range r.Tools {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, ResourceRef{Kind: resTool, Ref: g})
		}
	}
	for _, name := range r.Folders {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, ResourceRef{Kind: resFolder, Ref: name})
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
	Grant   []string      // its catalog grant: the tools, and the files server for a Drive folder
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
		case resFolder:
			f, err := c.store.folder(e.Ref)
			if err != nil {
				if c.driveFolder(e.Ref) != nil {
					return nil, fmt.Errorf("folder %q is in Google Drive: lend it as {kind: drive-folder, ref: %q}", e.Ref, e.Ref)
				}
				return nil, fmt.Errorf("folder %q: unknown (see list_folders)", e.Ref)
			}
			e.Ref = f.Name
			if e.Access == "" {
				e.Access = f.Access // the old folders field lent a folder with its own access
			}
			if !atLeast(f.Access, e.Access) {
				return nil, fmt.Errorf("folder %q is opened to Eriphe for %s only: it cannot be lent %s", f.Name, f.Access, e.Access)
			}
			names = append(names, f.Name)
		case resDriveFolder:
			d := c.driveFolder(e.Ref)
			if d == nil {
				if _, err := c.store.folder(e.Ref); err == nil {
					return nil, fmt.Errorf("folder %q is on a machine: lend it as {kind: folder, ref: %q}", e.Ref, e.Ref)
				}
				return nil, fmt.Errorf("Drive folder %q: unknown (see list_folders)", e.Ref)
			}
			e.Ref = d.Name
			if !atLeast(orStr(d.Access, accessRead), e.Access) {
				return nil, fmt.Errorf("Drive folder %q is opened to Eriphe for %s only: it cannot be lent %s", d.Name, d.Access, e.Access)
			}
			// For now the files server is lent whole: it reaches every Drive
			// folder given to Eriphe, and enforces each one's access itself.
			grant = append(grant, filesServer)
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
		case resFile:
			id := strings.TrimPrefix(e.Ref, "attachment:")
			if _, err := c.store.attachment(id); err != nil {
				return nil, fmt.Errorf("file %q: no such attachment in the conversation", e.Ref)
			}
			return nil, fmt.Errorf("file %s: attachments are not lent to tasks yet: read it with read_attachment and put what the task needs in its prompt", id)
		}
		// The same thing twice: once, with the higher access.
		if i := slices.IndexFunc(out.List, func(o ResourceRef) bool { return o.key() == e.key() }); i >= 0 {
			if accessRank[e.Access] > accessRank[out.List[i].Access] {
				out.List[i].Access = e.Access
			}
		} else {
			out.List = append(out.List, e)
		}
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

// folderAccess is the access a folder is lent with, as folders name it.
func folderAccess(access string) string {
	if access == accessDrop {
		return folderDrop
	}
	return folderRead
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
// lent anything that writes: a write or drop entry of any kind, tools that
// may act, a Drive folder (its files server is lent whole for now), and
// the old identities, long-lived secrets. Reads still go. My own message
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
	if len(r.Identities) > 0 {
		return refuse("long-lived credentials (identities " + strings.Join(r.Identities, ", ") + ")")
	}
	lent, err := c.resolveRequest(r)
	if err != nil {
		return err
	}
	var writes, acting []string
	for _, e := range lent.List {
		switch {
		case e.Access == accessWrite || e.Access == accessDrop:
			writes = append(writes, e.String())
		case e.Kind == resTool:
			acting = append(acting, c.actingGrant([]string{e.Ref})...)
		case e.Kind == resDriveFolder:
			if len(c.actingGrant([]string{filesServer})) > 0 {
				acting = append(acting, e.String()+" through "+filesServer+", which can write in every Drive folder")
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

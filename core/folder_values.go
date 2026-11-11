package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// A machine's folder's rights: what Eriphe may do there, as for any tool,
// two cases, Lire (read) and Modifier (change: create, change, rename and
// delete anywhere in it), each free, on approval or never. A machine has a
// default (every folder shared there, Free and Free until the user says
// otherwise), and a folder may set a case of its own, above or below it. A
// folder shared read-only on its machine greys Modifier: never, whatever
// is set. Free goes in the user's own clean turn; On approval, and a
// change on a start that is not clean, is asked on the task's start card;
// Never is refused when the task is lent.

// folderCases are the cases of a machine's folder.
var folderCases = []string{catRead, catChange}

// deviceGridKey is where a machine's default is kept; folderGridKey a
// folder's own cases, by its machine and id, which outlive its name.
func deviceGridKey(node string) string { return "folder_grid:" + node }

func folderGridKey(f *Folder) string { return "folder_case:" + f.Node + ":" + f.ID }

// deviceGrid is a machine's default for its folders: Free unless set.
func (c *core) deviceGrid(node string) accessGrid {
	var g accessGrid
	json.Unmarshal([]byte(c.store.get(deviceGridKey(node))), &g)
	g.Read, g.Change = cmpOr(g.Read, gridFree), cmpOr(g.Change, gridFree)
	return g
}

// folderOwn are a folder's cases of its own.
func (c *core) folderOwn(f *Folder) accessGrid {
	var g accessGrid
	json.Unmarshal([]byte(c.store.get(folderGridKey(f))), &g)
	return g
}

// folderCase is what holds for one case of a folder: its own, else its
// machine's; Modifier is never on a folder shared read-only.
func (c *core) folderCase(f *Folder, category string) string {
	if category == catChange && f.Access != accessWrite {
		return gridNever
	}
	return c.deviceGrid(f.Node).over(c.folderOwn(f)).value(category)
}

// folderRights are a folder's cases as they hold.
func (c *core) folderRights(f *Folder) map[string]string {
	out := map[string]string{}
	for _, cat := range folderCases {
		out[cat] = c.folderCase(f, cat)
	}
	return out
}

// cases is a grid's own values of some cases, those it sets.
func (g accessGrid) cases(cats []string) map[string]string {
	out := map[string]string{}
	for _, cat := range cats {
		if v := g.raw(cat); v != "" {
			out[cat] = v
		}
	}
	return out
}

func checkFolderCase(category, value string, follow bool) error {
	if !slices.Contains(folderCases, category) {
		return &folderError{Code: "invalid", Msg: fmt.Sprintf("case %q: read or change", category)}
	}
	if !slices.Contains(gridValues, value) && !(follow && value == "") {
		return &folderError{Code: "invalid", Msg: fmt.Sprintf("value %q: free, approval or never", value)}
	}
	return nil
}

// setDeviceCase sets one case of a machine's default.
func (c *core) setDeviceCase(node, category, value string) error {
	if err := checkFolderCase(category, value, false); err != nil {
		return err
	}
	raw, _ := json.Marshal(c.deviceGrid(node).with(category, value))
	return c.store.set(deviceGridKey(node), string(raw))
}

// setFolderCase gives one case of a folder its own value; "" has it follow
// its machine again.
func (c *core) setFolderCase(name, category, value string) error {
	if err := checkFolderCase(category, value, true); err != nil {
		return err
	}
	f, err := c.store.folder(name)
	if err != nil {
		return err
	}
	g := c.folderOwn(f).with(category, value)
	if g == (accessGrid{}) {
		return c.store.set(folderGridKey(f), "")
	}
	raw, _ := json.Marshal(g)
	return c.store.set(folderGridKey(f), string(raw))
}

// folderNeeds are the folders a task is lent that need the user's yes at
// its start: a case On approval, or a change on a start that is not clean.
func (c *core) folderNeeds(list []ResourceRef, clean bool) []startNeed {
	var out []startNeed
	for _, e := range list {
		if e.Kind != resFolder {
			continue
		}
		f, err := c.store.folder(e.Ref)
		if err != nil {
			continue // resolveRequest refuses it
		}
		cat := catRead
		if e.Access == accessWrite {
			cat = catChange
		}
		switch v := c.folderCase(f, cat); {
		case v == gridApproval:
			out = append(out, startNeed{Folder: f.Name, Access: e.Access, Category: cat, Reason: "folder " + cat + " on approval"})
		case v == gridFree && cat == catChange && !clean:
			out = append(out, startNeed{Folder: f.Name, Access: e.Access, Category: cat, Reason: "folder change lent on an unclean start"})
		}
	}
	return out
}

// folderStartText says, a sentence per folder, what a start asks of them.
func folderStartText(needs []startNeed, lang string) []string {
	var out []string
	for _, n := range needs {
		if n.Folder != "" {
			out = append(out, fmt.Sprintf(tr(lang, "taskstart.folder."+n.Category), n.Folder))
		}
	}
	return out
}

// legacyFolderValues moves the values set before folders had rights (the
// NAS, once): a folder whose manage was On approval keeps Modifier on
// approval; drop's values go with drop.
func legacyFolderValues(store *Store) {
	rows, err := store.db.Query(`SELECT key, value FROM kv WHERE key LIKE 'folder_value:%'`)
	if err != nil {
		return
	}
	type kept struct{ key, value string }
	var all []kept
	for rows.Next() {
		var k kept
		if rows.Scan(&k.key, &k.value) == nil {
			all = append(all, k)
		}
	}
	rows.Close()
	for _, k := range all {
		rest, ok := strings.CutSuffix(strings.TrimPrefix(k.key, "folder_value:"), ":manage")
		if ok && k.value == gridApproval {
			raw, _ := json.Marshal(accessGrid{Change: gridApproval})
			store.set("folder_case:"+rest, string(raw))
		}
		store.db.Exec(`DELETE FROM kv WHERE key = ?`, k.key)
	}
}

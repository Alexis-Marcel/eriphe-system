package main

import "fmt"

// Who decides a folder's writes (issue #210, step 7). A folder keeps its own
// scale (read, drop, manage, its machine's level as ceiling), and takes from
// the grid who decides: lending it at drop, and at manage (lent as write), is
// Free or On approval, set for each folder in Settings → Folders. On
// approval is asked on the task's start card, once per task; so is a Free
// write of a task that did not start cleanly (a tainted turn, a turn that is
// not the user's), as for a tool. Reads are always free.

// folderValueKey is where a folder's value for a level is kept: by its
// machine and id, which outlive its name.
func folderValueKey(f *Folder, level string) string {
	return "folder_value:" + f.Node + ":" + f.ID + ":" + level
}

// folderWriteLevel is the level a lend's access needs: drop, or manage for
// write; "" for a read.
func folderWriteLevel(access string) string {
	switch access {
	case accessDrop:
		return accessDrop
	case accessWrite:
		return folderManage
	}
	return ""
}

// folderValue is a folder's value for drop or manage: Free unless the user
// set it On approval.
func (c *core) folderValue(f *Folder, level string) string {
	if c.store.get(folderValueKey(f, level)) == gridApproval {
		return gridApproval
	}
	return gridFree
}

// setFolderValue sets a folder's value for drop or manage.
func (c *core) setFolderValue(name, level, value string) error {
	if level != accessDrop && level != folderManage {
		return &folderError{Code: "invalid", Msg: fmt.Sprintf("level %q: drop or manage", level)}
	}
	if value != gridFree && value != gridApproval {
		return &folderError{Code: "invalid", Msg: fmt.Sprintf("value %q: free or approval", value)}
	}
	f, err := c.store.folder(name)
	if err != nil {
		return err
	}
	if value == gridFree {
		value = "" // the default
	}
	return c.store.set(folderValueKey(f, level), value)
}

// folderNeeds are the folders a task is lent to write in that need the
// user's yes at its start: On approval, or any write on an unclean start.
func (c *core) folderNeeds(list []ResourceRef, clean bool) []startNeed {
	var out []startNeed
	for _, e := range list {
		level := folderWriteLevel(e.Access)
		if e.Kind != resFolder || level == "" {
			continue
		}
		f, err := c.store.folder(e.Ref)
		if err != nil {
			continue // resolveRequest refuses it
		}
		switch {
		case c.folderValue(f, level) == gridApproval:
			out = append(out, startNeed{Folder: f.Name, Access: level, Reason: "folder " + level + " on approval"})
		case !clean:
			out = append(out, startNeed{Folder: f.Name, Access: level, Reason: "folder " + level + " lent on an unclean start"})
		}
	}
	return out
}

// folderStartText says, a sentence per folder, what a start asks of them.
func folderStartText(needs []startNeed, lang string) []string {
	var out []string
	for _, n := range needs {
		if n.Folder != "" {
			out = append(out, fmt.Sprintf(tr(lang, "taskstart.folder."+n.Access), n.Folder))
		}
	}
	return out
}

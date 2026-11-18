package main

// How the user wants to see their work (« moins de lettres de news »): a
// preference memory about "topic:workspace", from the user's words (the
// nightly extraction never writes a preference). Whoever notes the items
// of a node (the brain, a skill that watches a source) reads it, and notes
// those items so. No setting of the page keeps it: the user says it to
// Eriphe.

const (
	workspaceEntity = "topic:workspace" // what a preference about the workspace is about

	seePrefsMax = 12  // the preferences about what the user sees a watcher reads, the latest first
	seePrefMax  = 300 // runes of each
)

// isWorkspaceEntity tells an about that is the workspace's.
func isWorkspaceEntity(s string) bool {
	k, _ := aliasKey(s)
	return k == workspaceEntity
}

// seePreferences are the user's preferences about what they see, the
// latest first: what a watcher reads before it notes items.
func (c *core) seePreferences() []string {
	if c.mem == nil {
		return nil
	}
	xs, _ := c.mem.list(`kind = ? AND about = ? AND status = ? AND (valid_to IS NULL OR valid_to > ?)`,
		[]any{kindPreference, workspaceEntity, memActive, c.mem.now().Unix()}, `updated_at DESC, id DESC`, seePrefsMax)
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, truncate(oneLine(x.Text), seePrefMax))
	}
	return out
}

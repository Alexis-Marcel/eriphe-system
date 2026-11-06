package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// No work lost on stop. Before a task's container is stopped (by the brain,
// a budget, or eriphe-core's "fails twice"), the node looks at its
// repository: commits that no remote has are pushed, if they are on the
// branch the task was lent, through the task's own credential helper, so
// eriphe-core's policy and credentials still decide. What cannot be pushed
// (no branch lent, the push refused, an approval the user would have to
// give) is kept on the node instead: /work/repo is moved to kept/<task>
// under -root once the container is stopped, kept -snapshot-days like the
// snapshots, then pruned. A "work" event says what was done.

const keptDir = "kept"

// saveWorkScript pushes the lent branch ($1) when it has commits no remote
// has, with git asking Eriphe for its credentials as in the task
// (entrypoint), and never a prompt. It prints ahead=N (commits of the local
// branches no remote has), pushed=1 when the push went through, left=N
// (those still not on a remote), and the push's own words when it failed.
const saveWorkScript = `cd /work/repo 2>/dev/null || { echo "ahead=0"; exit 0; }
if [ -z "$ERIPHE_GIT_OWN_TOKEN" ] && [ -n "$ERIPHE_CRED_URL" ] && [ -x ` + injectDir + `/bin/eriphe-cred ]; then
  export GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0=` + injectDir + `/bin/eriphe-cred \
    GIT_CONFIG_KEY_1=credential.useHttpPath GIT_CONFIG_VALUE_1=true
fi
export GIT_TERMINAL_PROMPT=0
ahead=$(git rev-list --count --branches --not --remotes 2>/dev/null) || ahead=0
echo "ahead=$ahead"
[ "$ahead" -gt 0 ] 2>/dev/null || exit 0
if [ -n "$1" ] && [ "$(git rev-list --count "refs/heads/$1" --not --remotes 2>/dev/null || echo 0)" -gt 0 ]; then
  if git push --quiet origin "refs/heads/$1:refs/heads/$1" 2>&1; then echo "pushed=1"; fi
fi
echo "left=$(git rev-list --count --branches --not --remotes 2>/dev/null || echo "$ahead")"`

// savedWork is what saveWork found and did.
type savedWork struct {
	ahead, left int
	pushed      bool
	output      string // the push's words, when it failed
}

// parseSavedWork reads saveWorkScript's output.
func parseSavedWork(out string) savedWork {
	var w savedWork
	left := -1
	var rest []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "ahead="):
			w.ahead, _ = strconv.Atoi(strings.TrimPrefix(l, "ahead="))
		case strings.HasPrefix(l, "left="):
			left, _ = strconv.Atoi(strings.TrimPrefix(l, "left="))
		case l == "pushed=1":
			w.pushed = true
		case l != "":
			rest = append(rest, l)
		}
	}
	w.left = left
	if left < 0 {
		w.left = w.ahead
	}
	w.output = strings.Join(rest, "\n")
	return w
}

// saveWork pushes or marks for keeping a task's commits before its
// container stops; it tells whether /work/repo is to be kept.
func (n *node) saveWork(t *Task) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, code, err := n.execTask(ctx, t, 8<<10, saveWorkScript, t.Spec.Branch)
	if err != nil || code != 0 {
		// It could not look: keep the repository rather than guess.
		reason := "could not read the repository before the stop"
		if err != nil {
			reason += ": " + err.Error()
		}
		t.emit("work", map[string]any{"kept": keptName(t), "reason": reason})
		return true
	}
	w := parseSavedWork(out)
	if w.ahead == 0 {
		return false
	}
	data := map[string]any{"commits": w.ahead}
	if t.Spec.Branch != "" {
		data["branch"] = t.Spec.Branch
	}
	if w.pushed {
		data["pushed"] = true
	}
	if w.left == 0 {
		t.emit("work", data)
		return false
	}
	data["kept"] = keptName(t)
	switch {
	case t.Spec.Branch == "":
		data["reason"] = "no branch was lent to push to"
	case w.pushed:
		data["reason"] = "commits on other branches than " + t.Spec.Branch
	default:
		data["reason"] = "the push was not possible: " + tail(strings.Join(strings.Fields(w.output), " "), 300)
	}
	t.emit("work", data)
	return true
}

// keptName is where a task's repository is kept, under -root.
func keptName(t *Task) string { return keptDir + "/" + t.ID }

// keptMeta is the meta.json of a kept repository.
type keptMeta struct {
	Task   string    `json:"task"`
	Branch string    `json:"branch,omitempty"`
	Time   time.Time `json:"time"`
}

// keepRepo moves a stopped task's /work/repo to kept/<task> under -root.
func (n *node) keepRepo(t *Task) {
	if !validName(t.ID) || t.dir == "" {
		return
	}
	src := filepath.Join(t.dir, "work", "repo")
	dst := filepath.Join(n.opt.root, keptDir, t.ID)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		log.Printf("task %s: keeping its repository: %v", t.ID, err)
		return
	}
	if err := os.Rename(src, filepath.Join(dst, "repo")); err != nil {
		log.Printf("task %s: keeping its repository: %v", t.ID, err)
		t.emit("work", map[string]any{"error": "the repository could not be kept: " + err.Error()})
		return
	}
	raw, _ := json.Marshal(keptMeta{Task: t.ID, Branch: t.Spec.Branch, Time: time.Now()})
	os.WriteFile(filepath.Join(dst, "meta.json"), raw, 0o600)
	log.Printf("task %s: its repository is kept in %s", t.ID, dst)
}

// pruneKept removes the kept repositories older than -snapshot-days.
func (n *node) pruneKept() {
	base := filepath.Join(n.opt.root, keptDir)
	entries, _ := os.ReadDir(base)
	keep := time.Duration(n.snapshotDays()) * 24 * time.Hour
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(base, e.Name())
		var m keptMeta
		raw, err := os.ReadFile(filepath.Join(p, "meta.json"))
		if err != nil || json.Unmarshal(raw, &m) != nil {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			m.Time = fi.ModTime()
		}
		if time.Since(m.Time) > keep {
			forceRemove(p)
		}
	}
}

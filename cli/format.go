package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Plain-text formatting shared by the commands and the TUI.

// Task states, as eriphe-core reports them.
const (
	statePending           = "pending"
	stateRunning           = "running"
	stateWaitingPermission = "waiting_permission"
	stateIdle              = "idle"
	stateStopped           = "stopped"
	stateFailed            = "failed"
	stateLost              = "lost"
)

func finished(state string) bool {
	return state == stateStopped || state == stateFailed || state == stateLost
}

// truncate cuts s to n runes, with an ellipsis.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// oneLine folds whitespace, newlines included, into single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// shortDir shortens a task's repository or directory for a narrow column:
// a git URL becomes owner/name, a long path keeps its end.
func shortDir(dir string, n int) string {
	d := dir
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
		if j := strings.Index(d, "/"); j >= 0 {
			d = d[j+1:] // drop the host
		}
	} else if strings.HasPrefix(d, "git@") {
		if j := strings.Index(d, ":"); j >= 0 {
			d = d[j+1:]
		}
	}
	d = strings.Replace(d, ".git@", "@", 1)
	d = strings.TrimSuffix(d, ".git")
	if d == "" {
		return "-"
	}
	if r := []rune(d); len(r) > n && n > 1 {
		return "…" + string(r[len(r)-n+1:])
	}
	return d
}

// clock shows a time as 15:04 today, Jan 2 15:04 otherwise.
func clock(t, now time.Time) string {
	t = t.Local()
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// roleLabel names who speaks in the conversation.
func roleLabel(role string) string {
	switch role {
	case "user":
		return "you"
	case "brain":
		return "eriphe"
	}
	return role
}

// compactJSON renders v on one line, cut to n runes.
func compactJSON(v any, n int) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return truncate(oneLine(s), n)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return truncate(fmt.Sprint(v), n)
	}
	return truncate(string(raw), n)
}

func str(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return s
}

// eventText summarises a task event: a short label and its text.
func eventText(e Event) (label, text string) {
	d := e.Data
	switch e.Type {
	case "text":
		return "agent", strings.TrimSpace(str(d, "text"))
	case "tool_use":
		return "tool", strings.TrimSpace(str(d, "name") + " " + compactJSON(d["input"], 300))
	case "permission":
		return "permission", "asks to use " + permissionText(d)
	case "permission_answer":
		verdict := "denied"
		if allow, _ := d["allow"].(bool); allow {
			verdict = "allowed"
		}
		if msg := strings.TrimSpace(str(d, "message")); msg != "" {
			verdict += ": " + msg
		}
		return "answer", verdict
	case "turn_end":
		text := "turn ended"
		if sub := str(d, "subtype"); sub != "" && sub != "success" {
			text += " (" + sub + ")"
		}
		if bad, _ := d["auth_error"].(bool); bad {
			text += ", authentication error"
		}
		return "turn", text
	case "error":
		text := str(d, "message")
		if tail := strings.TrimSpace(str(d, "stderr")); tail != "" {
			text += "\n" + tail
		}
		return "error", text
	case "state":
		return "state", str(d, "state")
	}
	return e.Type, compactJSON(d, 300)
}

// permissionText is the tool a pending permission is for, and its input.
func permissionText(p map[string]any) string {
	tool := str(p, "tool_name")
	if tool == "" {
		tool = "a tool"
	}
	if in := compactJSON(p["input"], 500); in != "" && in != "{}" && in != "null" {
		return tool + " " + in
	}
	return tool
}

// waitingCount counts the tasks that wait for a permission.
func waitingCount(tasks []Task) int {
	n := 0
	for _, t := range tasks {
		if t.State == stateWaitingPermission {
			n++
		}
	}
	return n
}

// taskWhere is a task's node, agent and short directory, for one line.
func taskWhere(t Task, dirWidth int) string {
	parts := []string{t.Node}
	if t.Agent != "" {
		parts = append(parts, t.Agent)
	}
	if t.Dir != "" {
		parts = append(parts, shortDir(t.Dir, dirWidth))
	}
	return strings.Join(parts, " · ")
}

package main

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// Rendering of the conversation and the task list: pure functions of the
// data and the width, so they can be tested.

// renderChat renders the conversation, oldest first, wrapped to width.
func renderChat(msgs []Message, width int, now time.Time) string {
	if len(msgs) == 0 {
		return styleDim.Render("No messages yet. Say something to Eriphe below.")
	}
	blocks := make([]string, len(msgs))
	for i, msg := range msgs {
		blocks[i] = renderMessage(msg, width, now)
	}
	return strings.Join(blocks, "\n\n")
}

// renderMessage is a header (who, when) and the text, indented under it.
func renderMessage(msg Message, width int, now time.Time) string {
	who := styleEvent
	switch msg.Role {
	case "user":
		who = styleUser
	case "brain":
		who = styleBrain
	}
	text := styleText
	if msg.Role == "event" {
		text = styleEvent
	}
	head := who.Render(roleLabel(msg.Role)) + styleDim.Render(" · "+clock(msg.Time, now))
	return head + "\n" + indent(wrap(strings.TrimSpace(msg.Text), width-2), "  ", text)
}

// wrap word-wraps plain text to width cells, breaking long words.
func wrap(s string, width int) string {
	return lipgloss.Wrap(s, max(width, 4), "")
}

// indent prefixes and styles each line of s.
func indent(s, prefix string, style lipgloss.Style) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + style.Render(l)
	}
	return strings.Join(lines, "\n")
}

// renderTaskList renders tasks two lines each, the selected one marked,
// within width cells and height lines.
func renderTaskList(tasks []Task, selected int, focused bool, width, height int) string {
	title := styleTitle.Render("Tasks")
	if len(tasks) == 0 {
		return title + "\n\n" + styleDim.Render("No active tasks.")
	}
	lines := []string{title, ""}
	// Scroll so that the selected task is visible.
	perPage := max((height-2)/2, 1)
	first := max(0, selected-perPage+1)
	for i := first; i < len(tasks) && i < first+perPage; i++ {
		lines = append(lines, renderTaskRow(tasks[i], i == selected, focused, width)...)
	}
	return strings.Join(lines, "\n")
}

// renderTaskRow: the id and the state, then where the task runs.
func renderTaskRow(t Task, selected, focused bool, width int) []string {
	mark := "  "
	if selected {
		mark = styleTitle.Render("▸ ")
		if !focused {
			mark = styleDim.Render("▸ ")
		}
	}
	state := stateStyle(t.State).Render(stateIcon(t.State) + " " + t.State)
	if t.State == stateWaitingPermission {
		state = styleBadge.Render("PERMISSION")
	}
	id := styleKey.Render(t.ID)
	gap := max(width-2-lipgloss.Width(id)-lipgloss.Width(state), 1)
	first := mark + id + strings.Repeat(" ", gap) + state
	second := "  " + styleDim.Render(truncate(taskWhere(t, max(width-8-len(t.Node)-len(t.Agent), 8)), width-2))
	if selected && focused {
		first = styleSelect.Width(width).Render(first)
		second = styleSelect.Width(width).Render(second)
	}
	return []string{first, second}
}

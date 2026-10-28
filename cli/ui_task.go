package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The task view: one task's events as they come, and the answers it needs:
// allow or deny a permission, reply when it is idle, or stop it.

// taskMode is what the keys of the task view do.
type taskMode int

const (
	modeNormal      taskMode = iota
	modeReply                // typing a reply to the idle task
	modeDenyReason           // typing the optional reason of a denial
	modeConfirmStop          // y/n to stop the task
)

type taskView struct {
	id      string
	gen     int
	status  *TaskStatus
	events  []Event
	lastSeq int
	polling bool // an events poll is in flight
	backoff time.Duration
	err     string // the last failure to reach the task

	vp    viewport.Model
	input textinput.Model
	mode  taskMode
	busy  bool   // an answer or a stop is in flight
	flash string // its outcome
}

type (
	taskStatusMsg struct {
		gen     int
		st      *TaskStatus
		err     error
		initial bool
	}
	taskEventsMsg struct {
		gen    int
		events []Event
		err    error
	}
	actionMsg struct {
		gen    int
		result string
		err    error
	}
)

func newTaskView(id string, gen int) *taskView {
	in := textinput.New()
	in.Prompt = "› "
	return &taskView{id: id, gen: gen, input: in, vp: viewport.New()}
}

// fetchStatus gets the task; with n > 0 it is the first load, with n events.
func (t *taskView) fetchStatus(c *Client, n int) tea.Cmd {
	id, gen := t.id, t.gen
	return func() tea.Msg {
		st, err := c.Task(context.Background(), id, n)
		return taskStatusMsg{gen: gen, st: st, err: err, initial: n > 0}
	}
}

func (t *taskView) pollEvents(c *Client, delay time.Duration) tea.Cmd {
	t.polling = true
	id, gen, after, wait := t.id, t.gen, t.lastSeq, pollWait
	if delay > 0 {
		wait = 0 // a retry answers at once once eriphe-core is back
	}
	return func() tea.Msg {
		time.Sleep(delay)
		events, err := c.Events(context.Background(), id, after, wait)
		return taskEventsMsg{gen: gen, events: events, err: err}
	}
}

func (t *taskView) action(fn func(ctx context.Context) (string, error)) tea.Cmd {
	t.busy, t.flash = true, ""
	gen := t.gen
	return func() tea.Msg {
		res, err := fn(context.Background())
		return actionMsg{gen: gen, result: res, err: err}
	}
}

// update applies an answer of eriphe-core to the view.
func (t *taskView) update(c *Client, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case taskStatusMsg:
		if msg.gen != t.gen {
			return nil
		}
		if msg.err != nil {
			t.err = errorText(msg.err)
			return nil
		}
		t.err = ""
		t.status = msg.st
		if msg.initial {
			t.addEvents(msg.st.Recent)
		}
		// A finished task answers its polls at once: no need to keep asking.
		if !t.polling && !finished(t.status.Task.State) {
			return t.pollEvents(c, 0)
		}
		if finished(t.status.Task.State) && t.mode != modeNormal {
			t.setMode(modeNormal)
		}
		return nil

	case taskEventsMsg:
		if msg.gen != t.gen {
			return nil
		}
		t.polling = false
		if msg.err != nil {
			t.err = errorText(msg.err)
			t.backoff = nextBackoff(t.backoff)
			return t.pollEvents(c, t.backoff)
		}
		t.err, t.backoff = "", 0
		t.addEvents(msg.events)
		// The state and the pending permission come with the status.
		cmds := []tea.Cmd{t.fetchStatus(c, 0)}
		if len(msg.events) > 0 || t.status == nil || !finished(t.status.Task.State) {
			cmds = append(cmds, t.pollEvents(c, 0))
		}
		return tea.Batch(cmds...)

	case actionMsg:
		if msg.gen != t.gen {
			return nil
		}
		t.busy = false
		if msg.err != nil {
			t.flash = "error: " + errorText(msg.err)
		} else {
			t.flash = msg.result
		}
		return t.fetchStatus(c, 0)
	}
	return nil
}

func (t *taskView) addEvents(events []Event) {
	bottom := t.vp.AtBottom() || t.vp.TotalLineCount() == 0
	for _, e := range events {
		if e.Seq > t.lastSeq {
			t.events = append(t.events, e)
			t.lastSeq = e.Seq
		}
	}
	t.refresh()
	if bottom {
		t.vp.GotoBottom()
	}
}

func (t *taskView) refresh() {
	t.vp.SetContent(renderEvents(t.events, t.vp.Width()))
}

func (t *taskView) state() string {
	if t.status == nil {
		return ""
	}
	return t.status.Task.State
}

func (t *taskView) setMode(mode taskMode) {
	t.mode = mode
	t.input.Reset()
	switch mode {
	case modeReply:
		t.input.Placeholder = "Reply to the task…"
		t.input.Focus()
	case modeDenyReason:
		t.input.Placeholder = "Reason for the agent (optional)"
		t.input.Focus()
	default:
		t.input.Blur()
	}
}

// key handles a key in the task view; back is true to close it.
func (t *taskView) key(c *Client, msg tea.KeyPressMsg) (back bool, cmd tea.Cmd) {
	key := msg.String()
	switch t.mode {
	case modeReply, modeDenyReason:
		switch key {
		case "esc":
			t.setMode(modeNormal)
			return false, nil
		case "enter":
			text := strings.TrimSpace(t.input.Value())
			if t.mode == modeReply && text == "" {
				return false, nil
			}
			var allow *bool
			if t.mode == modeDenyReason {
				no := false
				allow = &no
			}
			t.setMode(modeNormal)
			id, pid := t.id, t.pendingID()
			if allow == nil {
				pid = ""
			}
			return false, t.action(func(ctx context.Context) (string, error) { return c.Answer(ctx, id, pid, text, allow) })
		}
		t.input, cmd = t.input.Update(msg)
		return false, cmd

	case modeConfirmStop:
		t.setMode(modeNormal)
		if key == "y" {
			id := t.id
			return false, t.action(func(ctx context.Context) (string, error) { return "stopping", c.Stop(ctx, id) })
		}
		return false, nil
	}

	waiting := t.status != nil && t.status.PendingPermission != nil
	switch key {
	case "esc", "q":
		return true, nil
	case "y":
		if waiting && !t.busy {
			yes, id, pid := true, t.id, t.pendingID()
			return false, t.action(func(ctx context.Context) (string, error) { return c.Answer(ctx, id, pid, "", &yes) })
		}
	case "n":
		if waiting && !t.busy {
			t.setMode(modeDenyReason)
		}
	case "r":
		if t.state() == stateIdle && !t.busy {
			t.setMode(modeReply)
		}
	case "s":
		if t.status != nil && !finished(t.state()) && !t.busy {
			t.setMode(modeConfirmStop)
		}
	case "up", "k":
		t.vp.ScrollUp(1)
	case "down", "j":
		t.vp.ScrollDown(1)
	case "pgup":
		t.vp.PageUp()
	case "pgdown", "space":
		t.vp.PageDown()
	case "home", "g":
		t.vp.GotoTop()
	case "end", "G":
		t.vp.GotoBottom()
	}
	return false, nil
}

// bottomHeight is the height of the area under the events: the pending
// permission or the input.
func (t *taskView) bottomHeight(width int) int {
	if t.mode == modeReply || t.mode == modeDenyReason {
		h := inputHeight
		if t.mode == modeDenyReason {
			h += lipgloss.Height(t.permissionBox(width))
		}
		return h
	}
	if t.status != nil && t.status.PendingPermission != nil {
		return lipgloss.Height(t.permissionBox(width))
	}
	return 0
}

const taskHeaderHeight = 4 // the task's line, its prompt, the pane's top and bottom borders

// layout sizes the view; the bottom area grows and shrinks with the state.
func (t *taskView) layout(width, height int) {
	w := max(width-stylePane.GetHorizontalFrameSize(), 10)
	resized := w != t.vp.Width()
	t.vp.SetWidth(w)
	t.vp.SetHeight(max(height-taskHeaderHeight-1-t.bottomHeight(width), 1))
	t.input.SetWidth(max(w-3, 10))
	if resized {
		t.refresh()
	}
}

func (t *taskView) view(width, height int, now time.Time) string {
	if t.status == nil {
		msg := styleDim.Render("Loading " + t.id + "…")
		if t.err != "" {
			msg = styleError.Render(t.err)
		}
		return stylePane.Width(width).Height(height).Render(msg)
	}
	task := t.status.Task
	inner := width - stylePane.GetHorizontalFrameSize()
	head := styleTitle.Render(task.ID) + "  " + stateStyle(task.State).Render(stateIcon(task.State)+" "+task.State) +
		"  " + styleDim.Render(truncate(taskWhere(task, 40)+" · "+clock(task.Created, now), max(inner-30, 10)))
	prompt := styleText.Render(truncate(oneLine(task.Prompt), inner))
	parts := []string{head, prompt, "", t.vp.View()}
	box := styleFocused.Width(width).Height(height - t.bottomHeight(width)).Render(strings.Join(parts, "\n"))

	var bottom []string
	if t.status.PendingPermission != nil && t.mode != modeReply {
		bottom = append(bottom, t.permissionBox(width))
	}
	if t.mode == modeReply || t.mode == modeDenyReason {
		bottom = append(bottom, styleFocused.Width(width).Height(inputHeight).Render(t.input.View()))
	}
	return lipgloss.JoinVertical(lipgloss.Left, append([]string{box}, bottom...)...)
}

// permissionBox shows the pending permission, what it asks and how to answer.
func (t *taskView) permissionBox(width int) string {
	p := t.status.PendingPermission
	inner := width - stylePerm.GetHorizontalFrameSize()
	title := styleBadge.Render("PERMISSION") + " " + styleKey.Render(str(p, "tool_name"))
	input := styleText.Render(wrap(compactJSON(p["input"], 600), inner))
	hint := keys("y", "allow", "n", "deny (with an optional reason)")
	return stylePerm.Width(width).Render(title + "\n" + input + "\n" + hint)
}

func (t *taskView) statusLine() string {
	switch {
	case t.err != "":
		return styleError.Render(" " + truncate(t.err, 100) + ", retrying")
	case t.busy:
		return styleDim.Render(" sending…")
	case t.mode == modeConfirmStop:
		return styleBadge.Render("Stop "+t.id+"?") + keys("y", "stop", "any key", "cancel")
	case t.mode == modeReply:
		return keys("enter", "send", "esc", "cancel")
	case t.mode == modeDenyReason:
		return keys("enter", "deny", "esc", "cancel")
	}
	var hints []string
	if t.status != nil && t.status.PendingPermission != nil {
		hints = append(hints, "y", "allow", "n", "deny")
	}
	if t.state() == stateIdle {
		hints = append(hints, "r", "reply")
	}
	if t.status != nil && !finished(t.state()) {
		hints = append(hints, "s", "stop")
	}
	hints = append(hints, "↑↓ pgup pgdn", "scroll", "esc", "back")
	s := keys(hints...)
	if t.flash != "" {
		style := styleOK
		if strings.HasPrefix(t.flash, "error") {
			style = styleError
		}
		s = style.Render(" "+truncate(t.flash, 80)) + "  " + s
	}
	return s
}

// renderEvents renders task events, one block each, wrapped to width.
func renderEvents(events []Event, width int) string {
	if len(events) == 0 {
		return styleDim.Render("No events yet.")
	}
	lines := make([]string, 0, len(events))
	for _, e := range events {
		lines = append(lines, renderEvent(e, width))
	}
	return strings.Join(lines, "\n")
}

// renderEvent: the time, a label coloured by type, and the text under it
// when it does not fit on the line.
func renderEvent(e Event, width int) string {
	label, text := eventText(e)
	style := styleDim
	switch e.Type {
	case "text":
		style = styleBrain
	case "tool_use":
		style = styleUser
	case "permission":
		style = styleEvent
	case "error":
		style = styleError
	case "permission_answer", "turn_end":
		style = styleOK
	}
	gutter := styleDim.Render(e.Time.Local().Format("15:04:05")) + " " + style.Render(fmt.Sprintf("%-10s", label)) + " "
	const gutterWidth = 20
	body := wrap(text, max(width-gutterWidth, 10))
	lines := strings.Split(body, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = gutter + styleText.Render(lines[i])
		} else {
			lines[i] = strings.Repeat(" ", gutterWidth) + styleText.Render(lines[i])
		}
	}
	return strings.Join(lines, "\n")
}

// pendingID is the id of the permission shown, the one y and n answer.
func (t *taskView) pendingID() string {
	if t.status == nil || t.status.PendingPermission == nil {
		return ""
	}
	return fmt.Sprint(t.status.PendingPermission["id"])
}

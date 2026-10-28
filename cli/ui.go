package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The TUI: the conversation with Eriphe on the left, the worker tasks on the
// right, and a task view that opens over both. Everything eriphe-core says
// comes through long polls, one in flight per stream.

type focus int

const (
	focusChat focus = iota
	focusTasks
)

const (
	pollWait     = 50 // seconds of each long poll
	tasksEvery   = 3 * time.Second
	narrowWidth  = 72 // below, one pane at a time
	inputHeight  = 3
	maxMessages  = 1000
	firstBackoff = time.Second
	maxBackoff   = 15 * time.Second
)

type model struct {
	c     *Client
	now   func() time.Time
	width int
	high  int

	focus   focus
	input   textinput.Model
	chat    viewport.Model
	msgs    []Message
	lastSeq int
	loaded  bool // the first page of the conversation arrived

	tasks    []Task
	selected int

	online  bool
	connErr string
	backoff time.Duration // of the conversation poll, 0 while connected
	flash   string        // the last action's outcome, for the status line

	task    *taskView // the open task, nil on the main view
	taskGen int       // bumped on every open, to drop stale answers
}

// Messages from the commands.
type (
	messagesMsg struct {
		msgs []Message
		err  error
	}
	tasksMsg struct {
		tasks []Task
		err   error
	}
	tasksTickMsg struct{}
	sentMsg      struct {
		text string
		err  error
	}
)

func newModel(c *Client) model {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "Talk to Eriphe…"
	in.Focus()
	return model{c: c, now: time.Now, input: in, chat: viewport.New(), width: 80, high: 24}
}

func runTUI() error {
	c, err := client()
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(newModel(c)).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pollMessages(0), m.fetchTasks(), textinput.Blink)
}

// pollMessages waits for the conversation after the last message seen.
func (m model) pollMessages(delay time.Duration) tea.Cmd {
	c, after, wait := m.c, m.lastSeq, pollWait
	if !m.loaded || delay > 0 {
		wait = 0 // catching up, or checking that eriphe-core is back
	}
	return func() tea.Msg {
		time.Sleep(delay)
		msgs, err := c.Messages(context.Background(), after, wait)
		return messagesMsg{msgs, err}
	}
}

func (m model) fetchTasks() tea.Cmd {
	c := m.c
	return func() tea.Msg {
		tasks, err := c.Tasks(context.Background(), false)
		return tasksMsg{tasks, err}
	}
}

func (m model) send(text string) tea.Cmd {
	c := m.c
	return func() tea.Msg {
		_, err := c.Send(context.Background(), text)
		return sentMsg{text, err}
	}
}

// nextBackoff doubles a retry delay, from firstBackoff up to maxBackoff.
func nextBackoff(d time.Duration) time.Duration {
	if d < firstBackoff {
		return firstBackoff
	}
	return min(2*d, maxBackoff)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(model); ok && nm.task != nil {
		nm.task.layout(nm.width, nm.bodyHeight())
		return nm, cmd
	}
	return next, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.high = msg.Width, msg.Height
		m.layout()
		return m, nil

	case messagesMsg:
		if msg.err != nil {
			m.online, m.connErr = false, errorText(msg.err)
			m.backoff = nextBackoff(m.backoff)
			return m, m.pollMessages(m.backoff)
		}
		m.online, m.connErr, m.backoff = true, "", 0
		m.addMessages(msg.msgs)
		// The first page may not be all of it: catch up without waiting.
		if !m.loaded && len(msg.msgs) == 0 {
			m.loaded = true
		}
		return m, m.pollMessages(0)

	case tasksMsg:
		if msg.err == nil {
			m.tasks = msg.tasks
			m.selected = clampIndex(m.selected, len(m.tasks))
		}
		return m, tea.Tick(tasksEvery, func(time.Time) tea.Msg { return tasksTickMsg{} })

	case tasksTickMsg:
		cmds := []tea.Cmd{m.fetchTasks()}
		if m.task != nil {
			cmds = append(cmds, m.task.fetchStatus(m.c, 0))
		}
		return m, tea.Batch(cmds...)

	case sentMsg:
		if msg.err != nil {
			m.flash = "not sent: " + errorText(msg.err)
			if m.input.Value() == "" {
				m.input.SetValue(msg.text) // to send it again
			}
		}
		return m, nil

	case taskStatusMsg, taskEventsMsg, actionMsg:
		if m.task == nil {
			return m, nil
		}
		return m, m.task.update(m.c, msg)

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.task != nil {
			back, cmd := m.task.key(m.c, msg)
			if back {
				m.task = nil
			}
			return m, cmd
		}
		return m.mainKey(msg)
	}

	// Anything else (cursor blinks) goes to the focused input.
	var cmd tea.Cmd
	if m.task != nil {
		m.task.input, cmd = m.task.input.Update(msg)
	} else {
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

// mainKey handles a key on the main view.
func (m model) mainKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "tab" || key == "shift+tab" {
		m.setFocus(1 - m.focus)
		return m, nil
	}
	if m.focus == focusTasks {
		switch key {
		case "up", "k":
			m.selected = clampIndex(m.selected-1, len(m.tasks))
		case "down", "j":
			m.selected = clampIndex(m.selected+1, len(m.tasks))
		case "esc":
			m.setFocus(focusChat)
		case "q":
			return m, tea.Quit
		case "enter":
			if len(m.tasks) > 0 {
				return m, m.openTask(m.tasks[m.selected].ID)
			}
		}
		return m, nil
	}
	switch key {
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.Reset()
		m.flash = ""
		m.chat.GotoBottom()
		return m, m.send(text)
	case "pgup":
		m.chat.PageUp()
		return m, nil
	case "pgdown":
		m.chat.PageDown()
		return m, nil
	case "up":
		m.chat.ScrollUp(1)
		return m, nil
	case "down":
		m.chat.ScrollDown(1)
		return m, nil
	case "end":
		m.chat.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) setFocus(f focus) {
	m.focus = f
	if f == focusChat {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.layout()
}

func (m *model) openTask(id string) tea.Cmd {
	m.taskGen++
	m.task = newTaskView(id, m.taskGen)
	m.layout()
	return m.task.fetchStatus(m.c, 100)
}

// addMessages appends new messages, keeping the view at the bottom if it was.
func (m *model) addMessages(msgs []Message) {
	if len(msgs) == 0 {
		return
	}
	m.msgs, m.lastSeq = appendMessages(m.msgs, msgs, m.lastSeq)
	m.refreshChat()
}

// appendMessages adds the messages after seq last, at most maxMessages kept.
func appendMessages(have, msgs []Message, last int) ([]Message, int) {
	for _, msg := range msgs {
		if msg.Seq > last {
			have = append(have, msg)
			last = msg.Seq
		}
	}
	if len(have) > maxMessages {
		have = have[len(have)-maxMessages:]
	}
	return have, last
}

func (m *model) refreshChat() {
	bottom := m.chat.AtBottom() || m.chat.TotalLineCount() == 0
	m.chat.SetContent(renderChat(m.msgs, m.chat.Width(), m.now()))
	if bottom {
		m.chat.GotoBottom()
	}
}

// Layout: sizes of the panes for the current window.

func (m model) sideWidth() int {
	if m.width < narrowWidth {
		return m.width
	}
	return min(max(m.width*35/100, 30), 52)
}

func (m model) bodyHeight() int { return max(m.high-2, 3) } // header and status line

// layout resizes the components to the window.
func (m *model) layout() {
	chatW := m.width - m.sideWidth()
	if m.width < narrowWidth {
		chatW = m.width
	}
	frame := stylePane.GetHorizontalFrameSize()
	m.chat.SetWidth(max(chatW-frame, 10))
	m.chat.SetHeight(max(m.bodyHeight()-inputHeight-2, 1))
	m.input.SetWidth(max(chatW-frame-3, 10))
	if m.task != nil {
		m.task.layout(m.width, m.bodyHeight())
	}
	m.refreshChat()
}

func (m model) View() tea.View {
	var body string
	if m.task != nil {
		body = m.task.view(m.width, m.bodyHeight(), m.now())
	} else {
		body = m.mainView()
	}
	content := lipgloss.JoinVertical(lipgloss.Left, m.header(), body, m.statusLine())
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "eriphe"
	return v
}

func (m model) mainView() string {
	h := m.bodyHeight()
	side := m.sideWidth()
	chatW := m.width - side
	tasksPane := paneStyle(m.focus == focusTasks).Width(side).Height(h).
		Render(renderTaskList(m.tasks, m.selected, m.focus == focusTasks, side-stylePane.GetHorizontalFrameSize(), h-2))
	if m.width < narrowWidth {
		if m.focus == focusTasks {
			return tasksPane
		}
		chatW = m.width
	}
	chatPane := paneStyle(m.focus == focusChat).Width(chatW).Height(h - inputHeight).Render(m.chat.View())
	inputPane := paneStyle(m.focus == focusChat).Width(chatW).Height(inputHeight).Render(m.input.View())
	left := lipgloss.JoinVertical(lipgloss.Left, chatPane, inputPane)
	if m.width < narrowWidth {
		return left
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, tasksPane)
}

func paneStyle(focused bool) lipgloss.Style {
	if focused {
		return styleFocused
	}
	return stylePane
}

// header: the name, the connection, how many tasks wait for me, then the
// rest, cut first on a narrow window.
func (m model) header() string {
	parts := []string{styleTitle.Render(" eriphe")}
	switch {
	case m.online:
		parts = append(parts, styleOK.Render("● connected"))
	case m.connErr != "":
		parts = append(parts, styleError.Render("● disconnected"))
	default:
		parts = append(parts, styleDim.Render("● connecting"))
	}
	if n := waitingCount(m.tasks); n > 0 {
		parts = append(parts, styleBadge.Render(fmt.Sprintf("%d waiting", n)))
	}
	parts = append(parts, styleDim.Render(fmt.Sprintf("%d tasks", len(m.tasks))), styleDim.Render(m.c.URL))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(parts, "  "))
}

// statusLine: the connection error, the last outcome, or the keys.
func (m model) statusLine() string {
	var s string
	switch {
	case !m.online && m.connErr != "":
		s = styleError.Render(fmt.Sprintf(" disconnected: %s, retrying in %s", truncate(m.connErr, 80), m.backoff))
	case m.task != nil:
		s = m.task.statusLine()
	case m.flash != "":
		s = styleError.Render(" " + m.flash)
	case m.focus == focusTasks:
		s = keys("↑↓", "select", "enter", "open", "tab", "chat", "q", "quit")
	default:
		s = keys("enter", "send", "↑↓ pgup pgdn", "scroll", "tab", "tasks", "ctrl+c", "quit")
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(s)
}

// keys renders key hints: pairs of key and action.
func keys(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString(" " + styleKey.Render(pairs[i]) + " " + styleDim.Render(pairs[i+1]) + " ")
	}
	return b.String()
}

// errorText is an error without the request a network failure repeats.
func errorText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

func clampIndex(i, n int) int {
	return max(0, min(i, n-1))
}

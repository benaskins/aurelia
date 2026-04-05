package diagnose

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	loop "github.com/benaskins/axon-loop"
	talk "github.com/benaskins/axon-talk"
	tool "github.com/benaskins/axon-tool"
)

// Styles
var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	agentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	agentLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true)

	actionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true)

	approvedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	rejectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	modelLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true)
)

// chatEntry is a single item in the conversation view.
type chatEntry struct {
	role      string // "user", "agent", "tool", "action"
	content   string
	collapsed bool
}

// streamEvent wraps a loop.Event for the Bubble Tea update loop.
type streamEvent struct {
	token   string
	tool    *toolUseEvent
	done    bool
	err     error
	content string
}

type toolUseEvent struct {
	name string
	args map[string]any
}

// streamTickMsg carries stream events through Bubble Tea.
type streamTickMsg struct {
	event streamEvent
	ch    <-chan loop.Event
}

// actionConfirmMsg is sent when an action tool needs confirmation.
type actionConfirmMsg struct {
	action  string
	service string
	reason  string
	respond chan<- bool
}

// TUIModel is the Bubble Tea model for the diagnostic TUI.
type TUIModel struct {
	// Display state
	entries     []chatEntry
	streaming   string
	waiting     bool
	toolRunning bool // true while a tool is executing

	// Components
	input    textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	width    int
	height   int
	ready    bool

	// Engine
	engine   *Engine
	messages []talk.Message
	service  string // initial service focus (empty = all)

	// Action confirmation
	pendingAction *actionConfirmMsg
}

// NewTUIModel creates a TUI model for diagnostic conversation.
func NewTUIModel(engine *Engine, service string) TUIModel {
	ta := textarea.New()
	ta.Placeholder = "Ask about your services..."
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = toolStyle

	return TUIModel{
		engine:  engine,
		service: service,
		messages: []talk.Message{
			{Role: talk.RoleSystem, Content: systemPrompt},
			{Role: talk.RoleUser, Content: userMessage(service)},
		},
		input:   ta,
		spinner: sp,
	}
}

func (m TUIModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.startLLM(),
	)
}

func (m TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle action confirmation mode
		if m.pendingAction != nil {
			return m.handleConfirmKey(msg)
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.waiting {
				break
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				break
			}
			m.input.Reset()

			slog.Info("user message", "length", len(text))
			m.entries = append(m.entries, chatEntry{role: "user", content: text})
			m.messages = append(m.messages, talk.Message{Role: talk.RoleUser, Content: text})
			m.waiting = true
			m.streaming = ""
			m.refreshViewport()

			return m, m.startLLM()
		case "tab":
			m.toggleLastToolEntry()
			m.refreshViewport()
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		inputHeight := 5
		statusHeight := 1

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-inputHeight-statusHeight)
			m.viewport.YPosition = 0
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - inputHeight - statusHeight
		}
		m.input.SetWidth(msg.Width - 2)
		m.refreshViewport()

	case streamTickMsg:
		return m.handleStreamTick(msg)

	case actionConfirmMsg:
		m.pendingAction = &msg
		m.entries = append(m.entries, chatEntry{
			role:    "action",
			content: fmt.Sprintf("Propose: %s %s — %s  [y/n]", msg.action, msg.service, msg.reason),
		})
		m.refreshViewport()
		return m, nil
	}

	// Update sub-components
	var cmd tea.Cmd
	if m.pendingAction == nil {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	if m.toolRunning {
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		m.refreshViewport()
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m TUIModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		action := m.pendingAction
		slog.Info("action approved", "action", action.action, "service", action.service)
		m.entries = append(m.entries, chatEntry{
			role:    "action",
			content: approvedStyle.Render(fmt.Sprintf("Approved: %s %s", action.action, action.service)),
		})
		action.respond <- true
		m.pendingAction = nil
		m.refreshViewport()
		return m, nil
	case "n", "N":
		action := m.pendingAction
		slog.Info("action rejected", "action", action.action, "service", action.service)
		m.entries = append(m.entries, chatEntry{
			role:    "action",
			content: rejectedStyle.Render(fmt.Sprintf("Rejected: %s %s", action.action, action.service)),
		})
		action.respond <- false
		m.pendingAction = nil
		m.refreshViewport()
		return m, nil
	case "ctrl+c":
		if m.pendingAction != nil {
			m.pendingAction.respond <- false
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m TUIModel) handleStreamTick(msg streamTickMsg) (tea.Model, tea.Cmd) {
	ev := msg.event
	if ev.err != nil {
		slog.Error("LLM error", "error", ev.err)
		m.entries = append(m.entries, chatEntry{role: "agent", content: fmt.Sprintf("Error: %v", ev.err)})
		m.streaming = ""
		m.waiting = false
		m.toolRunning = false
		m.refreshViewport()
		return m, nil
	}
	if ev.tool != nil {
		slog.Info("tool use", "tool", ev.tool.name, "args", ev.tool.args)
		label := fmt.Sprintf("\u21b3 %s", ev.tool.name)
		if len(ev.tool.args) > 0 {
			var parts []string
			for k, v := range ev.tool.args {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
			label += " " + strings.Join(parts, ", ")
		}
		m.entries = append(m.entries, chatEntry{role: "tool", content: label, collapsed: true})
		// Inject inline marker into the streaming text
		m.streaming += fmt.Sprintf("[tool_use: %s] ", ev.tool.name)
		m.toolRunning = true
		m.refreshViewport()
		return m, tea.Batch(m.spinner.Tick, waitForStreamEvent(msg.ch))
	}
	if ev.done {
		content := ev.content
		if content == "" {
			content = m.streaming
		}
		if content != "" {
			slog.Info("agent response", "length", len(content))
			m.entries = append(m.entries, chatEntry{role: "agent", content: content})
			m.messages = append(m.messages, talk.Message{Role: talk.RoleAssistant, Content: content})
		}
		m.streaming = ""
		m.waiting = false
		m.toolRunning = false
		m.refreshViewport()
		return m, nil
	}
	if ev.token != "" {
		m.streaming += ev.token
		m.toolRunning = false
		m.refreshViewport()
	}
	return m, waitForStreamEvent(msg.ch)
}

func (m TUIModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	model := modelLabelStyle.Render(m.engine.Model())
	status := statusBarStyle.Render("ctrl+c quit | tab expand tools") + "  " + model
	if m.waiting {
		status = statusBarStyle.Render("thinking...") + "  " + model
	}
	if m.pendingAction != nil {
		status = actionStyle.Render("y to approve, n to reject")
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.viewport.View(),
		status,
		m.input.View(),
	)
}

func (m *TUIModel) refreshViewport() {
	var sb strings.Builder
	w := m.width
	if w <= 0 {
		w = 80
	}
	contentWidth := w - 2
	if contentWidth < 20 {
		contentWidth = 20
	}

	for _, e := range m.entries {
		switch e.role {
		case "user":
			sb.WriteString(userStyle.Render("you") + " ")
			sb.WriteString(wordWrap(e.content, contentWidth-5))
			sb.WriteString("\n\n")
		case "agent":
			sb.WriteString(agentLabelStyle.Render("aurelia") + " ")
			sb.WriteString(agentStyle.Width(contentWidth - 9).Render(e.content))
			sb.WriteString("\n\n")
		case "tool":
			sb.WriteString(toolStyle.Render(e.content))
			sb.WriteString("\n")
		case "action":
			sb.WriteString(actionStyle.Render(e.content))
			sb.WriteString("\n")
		}
	}

	if m.streaming != "" || m.toolRunning {
		sb.WriteString(agentLabelStyle.Render("aurelia") + " ")
		if m.streaming != "" {
			sb.WriteString(agentStyle.Width(contentWidth - 9).Render(m.streaming))
		}
		if m.toolRunning {
			sb.WriteString(m.spinner.View())
		} else {
			sb.WriteString("\u2588")
		}
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

func (m *TUIModel) toggleLastToolEntry() {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].role == "tool" {
			m.entries[i].collapsed = !m.entries[i].collapsed
			return
		}
	}
}

// startLLM launches the LLM stream and returns a command that reads events.
func (m TUIModel) startLLM() tea.Cmd {
	messages := make([]talk.Message, len(m.messages))
	copy(messages, m.messages)

	tools := m.engine.Tools()

	toolDefs := make([]tool.ToolDef, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, t)
	}

	req := &talk.Request{
		Model:         m.engine.Model(),
		Messages:      messages,
		Tools:         toolDefs,
		Stream:        true,
		MaxIterations: 50,
	}

	cfg := loop.RunConfig{
		Client:  m.engine.Client(),
		Request: req,
		Tools:   tools,
		ToolCtx: &tool.ToolContext{Ctx: context.Background()},
	}

	ch := loop.Stream(context.Background(), cfg)
	return waitForStreamEvent(ch)
}

// waitForStreamEvent reads the next loop.Event and converts it to a streamTickMsg.
func waitForStreamEvent(ch <-chan loop.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamTickMsg{event: streamEvent{done: true}}
		}
		se := streamEvent{}
		switch {
		case ev.Err != nil:
			se.err = ev.Err
		case ev.ToolUse != nil:
			se.tool = &toolUseEvent{name: ev.ToolUse.Name, args: ev.ToolUse.Args}
		case ev.Done != nil:
			se.done = true
			se.content = ev.Done.Content
		case ev.Token != "":
			se.token = ev.Token
		}
		return streamTickMsg{event: se, ch: ch}
	}
}

// wordWrap wraps text at the given width on word boundaries.
func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var sb strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(line)
			continue
		}
		words := strings.Fields(line)
		col := 0
		for i, w := range words {
			if i > 0 && col+1+len(w) > width {
				sb.WriteString("\n")
				col = 0
			} else if i > 0 {
				sb.WriteString(" ")
				col++
			}
			sb.WriteString(w)
			col += len(w)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

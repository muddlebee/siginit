package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muddlebee/siginit/internal/agent"
)

// Styles
var (
	styleHeader    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	styleToolCall  = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	styleToolResult = lipgloss.NewStyle().Foreground(lipgloss.Color("48"))
	styleFinal     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	styleError     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleBorder    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1)
)

// agentEventMsg wraps an agent.Event for the Bubble Tea message system.
type agentEventMsg agent.Event

// doneMsg signals the agent goroutine has exited.
type doneMsg struct{ err error }

// tickMsg is used for spinner animation.
type tickMsg time.Time

// Model is the Bubble Tea model for the siginit TUI.
type Model struct {
	spinner  spinner.Model
	viewport viewport.Model
	lines    []string
	done     bool
	err      error
	events   <-chan agent.Event
	width    int
	height   int
	title    string
}

// New creates a TUI model that reads from the given events channel.
func New(title string, events <-chan agent.Event) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	vp := viewport.New(80, 20)

	return Model{
		spinner: sp,
		viewport: vp,
		events:  events,
		title:   title,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		waitForEvent(m.events),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 6

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case agentEventMsg:
		e := agent.Event(msg)
		m.appendEvent(e)
		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		m.viewport.GotoBottom()

		if e.Kind == agent.EventFinal || e.Kind == agent.EventError {
			m.done = true
			if e.Kind == agent.EventError {
				m.err = fmt.Errorf("%s", e.Result)
			}
			cmds = append(cmds, tea.Quit)
		} else {
			cmds = append(cmds, waitForEvent(m.events))
		}

	case doneMsg:
		m.done = true
		m.err = msg.err
		cmds = append(cmds, tea.Quit)
	}

	vp, cmd := m.viewport.Update(msg)
	m.viewport = vp
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.width == 0 {
		return m.spinner.View() + " Starting…"
	}

	header := styleHeader.Render("  siginit — " + m.title)

	status := ""
	if m.done {
		if m.err != nil {
			status = styleError.Render(" ERROR: " + m.err.Error())
		} else {
			status = styleFinal.Render(" Done")
		}
	} else {
		status = m.spinner.View() + styleDim.Render(" working…  (q to quit)")
	}

	content := styleBorder.Width(m.width - 2).Render(m.viewport.View())
	return fmt.Sprintf("%s\n%s\n%s", header, content, status)
}

func (m *Model) appendEvent(e agent.Event) {
	var line string
	switch e.Kind {
	case agent.EventThinking:
		line = styleDim.Render("  ⋯ " + e.Message)
	case agent.EventToolCall:
		line = styleToolCall.Render("  → " + e.Message)
	case agent.EventToolResult:
		line = styleToolResult.Render("  ← " + e.Message)
	case agent.EventFinal:
		line = styleFinal.Render("\n  ✓ " + e.Message)
	case agent.EventError:
		line = styleError.Render("\n  ✗ " + e.Message)
	case agent.EventPermission:
		line = styleDim.Render("  ⚠ " + e.Message)
	default:
		line = "  " + e.Message
	}
	m.lines = append(m.lines, line)
}

// waitForEvent is the Bubble Tea async bridge: reads one event from the channel.
func waitForEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return doneMsg{}
		}
		return agentEventMsg(e)
	}
}

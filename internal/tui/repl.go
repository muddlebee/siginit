package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muddlebee/siginit/internal/agent"
)

// REPLHandlers are injected from main to avoid import cycles.
type REPLHandlers struct {
	Init     func(path string) <-chan agent.Event
	Verify   func(svc string) (string, error)
	Doctor   func(svc string) (string, error)
	Register func() error
}

// REPLConfig holds display metadata for the status bar.
type REPLConfig struct {
	SigNozURL string
	Provider  string
	Model     string
	Authed    bool
}

// ── message types ─────────────────────────────────────────────────────────────

type replLineMsg string
type replAgentEvent agent.Event
type replAgentDone struct{ err error }
type replSyncResult struct{ out string; err error }

// ── styles ────────────────────────────────────────────────────────────────────

var (
	stylePrompt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	styleCmd     = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	styleFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleSep     = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	styleStatus  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// ── model ─────────────────────────────────────────────────────────────────────

// REPLModel is the Bubble Tea model for the interactive siginit REPL.
type REPLModel struct {
	input    textinput.Model
	viewport viewport.Model
	lines    []string
	busy     bool   // true while agent or async op is running
	agEvents <-chan agent.Event

	cfg      REPLConfig
	handlers REPLHandlers

	width  int
	height int
}

// NewREPL constructs the REPL model.
func NewREPL(cfg REPLConfig, h REPLHandlers) REPLModel {
	ti := textinput.New()
	ti.Placeholder = "/init [path]  /verify <svc>  /doctor  /help"
	ti.Focus()
	ti.CharLimit = 256
	ti.PromptStyle = stylePrompt
	ti.Prompt = "siginit> "

	vp := viewport.New(80, 20)

	m := REPLModel{
		input:    ti,
		viewport: vp,
		cfg:      cfg,
		handlers: h,
	}

	m.log(styleHeader.Render("siginit") + styleInfo.Render("  agentic onboarding for SigNoz"))
	m.log(styleInfo.Render("type /help for commands, /quit to exit"))
	m.log("")
	if !cfg.Authed {
		m.log(styleFail.Render("  ✗  not authenticated — run /register on first use"))
	}
	return m
}

func (m *REPLModel) log(line string) {
	m.lines = append(m.lines, line)
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoBottom()
}

// ── Bubble Tea interface ───────────────────────────────────────────────────────

func (m REPLModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m REPLModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 4 // reserve 2 for input + 2 for status
		m.viewport.SetContent(strings.Join(m.lines, "\n"))

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if !m.busy {
				cmd := m.handleInput()
				cmds = append(cmds, cmd)
			}
		default:
			if !m.busy {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

	// streaming agent events during /init
	case replAgentEvent:
		e := agent.Event(msg)
		switch e.Kind {
		case agent.EventThinking:
			m.log(styleInfo.Render("  ⋯  " + e.Message))
		case agent.EventToolCall:
			m.log(styleCmd.Render("  →  " + e.Message))
		case agent.EventToolResult:
			m.log(styleInfo.Render("  ←  " + truncateLine(e.Message, 120)))
		case agent.EventFinal:
			m.log(styleOK.Render("  ✓  " + e.Message))
		case agent.EventError:
			m.log(styleFail.Render("  ✗  " + e.Message))
		case agent.EventPermission:
			m.log(styleInfo.Render("  ⚠  " + e.Message))
		}
		cmds = append(cmds, waitForReplEvent(m.agEvents))

	case replAgentDone:
		m.busy = false
		m.agEvents = nil
		if msg.err != nil {
			m.log(styleFail.Render("  ✗  " + msg.err.Error()))
		}
		m.log("")

	// result from synchronous commands (verify, doctor, register)
	case replSyncResult:
		m.busy = false
		if msg.err != nil {
			m.log(styleFail.Render("  ✗  " + msg.err.Error()))
		} else {
			for _, line := range strings.Split(strings.TrimSpace(msg.out), "\n") {
				m.log("  " + line)
			}
		}
		m.log("")
	}

	vp, cmd := m.viewport.Update(msg)
	m.viewport = vp
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m REPLModel) View() string {
	if m.width == 0 {
		return "starting…"
	}

	sep := styleSep.Render(strings.Repeat("─", m.width))

	inputView := m.input.View()
	if m.busy {
		inputView = styleInfo.Render("  working…")
	}

	authMark := styleOK.Render("✓ auth")
	if !m.cfg.Authed {
		authMark = styleFail.Render("✗ auth")
	}
	status := styleStatus.Render(fmt.Sprintf(
		"  ● %s   %s/%s   %s",
		m.cfg.SigNozURL, m.cfg.Provider, m.cfg.Model, authMark,
	))

	return fmt.Sprintf("%s\n%s\n%s\n%s",
		m.viewport.View(),
		sep,
		inputView,
		status,
	)
}

// ── command dispatch ──────────────────────────────────────────────────────────

func (m *REPLModel) handleInput() tea.Cmd {
	raw := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")
	if raw == "" {
		return nil
	}

	m.log(stylePrompt.Render("siginit> ") + raw)

	parts := strings.Fields(raw)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "/help", "/h":
		m.printHelp()
		return nil

	case "/clear":
		m.lines = nil
		m.viewport.SetContent("")
		return nil

	case "/quit", "/q", "quit", "exit":
		return tea.Quit

	case "/register", "/r":
		m.busy = true
		return func() tea.Msg {
			err := m.handlers.Register()
			if err != nil {
				return replSyncResult{err: err}
			}
			m.cfg.Authed = true
			return replSyncResult{out: "registered and authenticated"}
		}

	case "/verify", "/v":
		if len(args) == 0 {
			m.log(styleFail.Render("  usage: /verify <service-name>"))
			return nil
		}
		svc := args[0]
		m.busy = true
		return func() tea.Msg {
			out, err := m.handlers.Verify(svc)
			return replSyncResult{out: out, err: err}
		}

	case "/doctor", "/d":
		svc := ""
		if len(args) > 0 {
			svc = args[0]
		}
		m.busy = true
		return func() tea.Msg {
			out, err := m.handlers.Doctor(svc)
			return replSyncResult{out: out, err: err}
		}

	case "/init", "/i":
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		m.log(styleInfo.Render(fmt.Sprintf("  instrumenting %q…", path)))
		m.busy = true
		ch := m.handlers.Init(path)
		m.agEvents = ch
		return waitForReplEvent(ch)

	default:
		m.log(styleFail.Render(fmt.Sprintf("  unknown command %q — type /help", cmd)))
		return nil
	}
}

func (m *REPLModel) printHelp() {
	lines := []string{
		styleHeader.Render("  commands"),
		styleCmd.Render("  /init [path]") + styleInfo.Render("      instrument a project and verify traces"),
		styleCmd.Render("  /verify <svc>") + styleInfo.Render("     check if a service is visible in SigNoz"),
		styleCmd.Render("  /doctor [svc]") + styleInfo.Render("     diagnose why traces aren't flowing"),
		styleCmd.Render("  /register") + styleInfo.Render("         register admin account (first run only)"),
		styleCmd.Render("  /clear") + styleInfo.Render("            clear the log"),
		styleCmd.Render("  /quit") + styleInfo.Render("             exit"),
	}
	for _, l := range lines {
		m.log(l)
	}
	m.log("")
}

// waitForReplEvent reads one event from the agent channel.
func waitForReplEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return replAgentDone{}
		}
		return replAgentEvent(e)
	}
}

func truncateLine(s string, n int) string {
	// take just the first line if multi-line, then truncate
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"
	"github.com/muddlebee/siginit/internal/agent"
	"github.com/muddlebee/siginit/internal/provider"
	"github.com/muddlebee/siginit/internal/signoz"
	"github.com/muddlebee/siginit/internal/tools"
	"github.com/muddlebee/siginit/internal/tui"
	"github.com/spf13/cobra"
)

var (
	flagProvider  string
	flagAPIKey    string
	flagModel     string
	flagSigNozURL string
	flagEmail     string
	flagPassword  string
	flagCollector string
	flagDryRun    bool
	flagYes       bool
	flagService   string
)

func main() {
	_ = godotenv.Load()

	root := &cobra.Command{
		Use:          "siginit",
		Short:        "Agentic onboarding CLI for SigNoz",
		SilenceUsage: true,
		RunE:         func(_ *cobra.Command, _ []string) error { return runREPL() },
	}

	root.PersistentFlags().StringVar(&flagProvider, "provider", "deepseek", "LLM provider: deepseek, openai")
	root.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "Provider API key (overrides env)")
	root.PersistentFlags().StringVar(&flagModel, "model", "", "Model override")
	root.PersistentFlags().StringVar(&flagSigNozURL, "signoz-url", "http://localhost:8080", "SigNoz base URL")
	root.PersistentFlags().StringVar(&flagEmail, "email", "admin@siginit.local", "SigNoz admin email")
	root.PersistentFlags().StringVar(&flagPassword, "password", "Admin@12345678", "SigNoz admin password")
	root.PersistentFlags().StringVar(&flagCollector, "collector", "http://localhost:4318", "OTLP HTTP collector endpoint")

	root.AddCommand(initCmd(), doctorCmd(), verifyCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── REPL ──────────────────────────────────────────────────────────────────────

func runREPL() error {
	ctx := context.Background()

	llmClient, prov, err := provider.New(flagProvider, flagAPIKey, flagModel)
	if err != nil {
		return err
	}

	sc := signoz.New(flagSigNozURL)
	authed := false
	if tok, err := sc.Login(ctx, flagEmail, flagPassword); err == nil {
		sc.WithToken(tok)
		authed = true
	}

	cfg := tui.REPLConfig{
		SigNozURL: flagSigNozURL,
		Provider:  prov.Name,
		Model:     prov.DefaultModel,
		Authed:    authed,
	}

	handlers := tui.REPLHandlers{
		Register: func() error {
			if err := sc.Register(ctx, "Admin", flagEmail, flagPassword, "siginit-org"); err != nil {
				// ignore "already exists" errors
				if !strings.Contains(err.Error(), "already") && !strings.Contains(err.Error(), "disabled") {
					return err
				}
			}
			tok, err := sc.Login(ctx, flagEmail, flagPassword)
			if err != nil {
				return err
			}
			sc.WithToken(tok)
			return nil
		},

		Verify: func(svc string) (string, error) {
			args, _ := json.Marshal(map[string]any{
				"service_name":     svc,
				"lookback_minutes": 15,
			})
			t := &tools.QuerySigNoz{Client: sc}
			return t.Execute(ctx, args)
		},

		Doctor: func(svc string) (string, error) {
			collectorAddr := strings.TrimPrefix(flagCollector, "http://")
			collectorAddr = strings.TrimPrefix(collectorAddr, "https://")
			results := signoz.Doctor(ctx, sc, signoz.DoctorConfig{
				CollectorAddr: collectorAddr,
				ServiceName:   svc,
				Email:         flagEmail,
				Password:      flagPassword,
				LookbackMins:  15,
			})
			var sb strings.Builder
			allOK := true
			for _, r := range results {
				if r.OK {
					sb.WriteString(fmt.Sprintf("✓  [%s] %s\n", r.Layer, r.Message))
				} else {
					allOK = false
					sb.WriteString(fmt.Sprintf("✗  [%s] %s\n", r.Layer, r.Message))
					if r.Fix != "" {
						sb.WriteString(fmt.Sprintf("   → %s\n", r.Fix))
					}
				}
			}
			if !allOK {
				return sb.String(), fmt.Errorf("one or more checks failed")
			}
			return sb.String(), nil
		},

		Init: func(path string) <-chan agent.Event {
			events := make(chan agent.Event, 128)
			registry := agent.NewRegistry(
				&tools.InspectProject{},
				&tools.ReadFile{},
				&tools.EditFile{},
				&tools.RunCommand{},
				&tools.GenerateConfig{CollectorHTTP: flagCollector},
				&tools.QuerySigNoz{Client: sc},
			)
			agCfg := agent.Config{
				Model:       prov.DefaultModel,
				DryRun:      flagDryRun,
				AutoApprove: true,
				PermissionFn: func(_, _ string) bool { return true },
			}
			ag := agent.New(llmClient, registry, agCfg, events)
			systemPrompt := fmt.Sprintf(`You are siginit, an expert OpenTelemetry instrumentation agent.
Your goal: instrument the developer's project so traces flow into SigNoz, then verify it worked.

Rules:
- Call inspect_project FIRST before anything else.
- Prefer zero-code auto-instrumentation: NODE_OPTIONS for Node.js, opentelemetry-instrument for Python.
- OTel collector: %s  |  SigNoz: %s
- To start a background server: always use "nohup ... &>/tmp/app.log 2>&1 & disown; sleep 2" so the process survives after bash exits.
- After starting the server, make a test HTTP request (curl) to generate at least one trace.
- You MUST call query_signoz to verify traces — never declare success without it.
- On verify failure: fix the most likely issue, then call query_signoz once more.
- Be concise. Developers want commands and results, not paragraphs.`, flagCollector, flagSigNozURL)
			userMsg := fmt.Sprintf(`Instrument the project at %q and verify traces arrive in SigNoz.`, path)
			go func() {
				defer close(events)
				_ = ag.Run(ctx, systemPrompt, userMsg)
			}()
			return events
		},
	}

	m := tui.NewREPL(cfg, handlers)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

// ── siginit init (one-shot, for scripting/CI) ─────────────────────────────────

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [project-path]",
		Short: "Instrument a project and verify first trace (non-interactive)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			return runInit(path)
		},
	}
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview actions without executing")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Auto-approve all tool calls")
	cmd.Flags().StringVar(&flagService, "service", "", "OTEL service name override")
	return cmd
}

func runInit(projectPath string) error {
	ctx := context.Background()
	llmClient, prov, err := provider.New(flagProvider, flagAPIKey, flagModel)
	if err != nil {
		return err
	}

	sc := signoz.New(flagSigNozURL)
	if err := ensureAuth(ctx, sc, false); err != nil {
		return err
	}

	events := make(chan agent.Event, 128)
	registry := agent.NewRegistry(
		&tools.InspectProject{},
		&tools.ReadFile{},
		&tools.EditFile{},
		&tools.RunCommand{},
		&tools.GenerateConfig{CollectorHTTP: flagCollector},
		&tools.QuerySigNoz{Client: sc},
	)
	agCfg := agent.Config{
		Model:       prov.DefaultModel,
		DryRun:      flagDryRun,
		AutoApprove: flagYes,
	}
	if flagYes {
		agCfg.PermissionFn = func(_, _ string) bool { return true }
	}
	ag := agent.New(llmClient, registry, agCfg, events)

	svcHint := flagService
	if svcHint == "" {
		svcHint = "(derive from project name)"
	}
	systemPrompt := fmt.Sprintf(`You are siginit, an expert OpenTelemetry instrumentation agent.
Your goal: instrument the developer's project so traces flow into SigNoz, then verify it worked.

Rules:
- Call inspect_project FIRST before anything else.
- Prefer zero-code auto-instrumentation: NODE_OPTIONS for Node.js, opentelemetry-instrument for Python.
- OTel collector: %s  |  SigNoz: %s
- To start a background server: always use "nohup ... &>/tmp/app.log 2>&1 & disown; sleep 2" so the process survives after bash exits.
- After starting the server, make a test HTTP request (curl) to generate at least one trace.
- You MUST call query_signoz to verify traces — never declare success without it.
- On verify failure: fix the most likely issue, then call query_signoz once more.
- Be concise.`, flagCollector, flagSigNozURL)
	userMsg := fmt.Sprintf(`Instrument the project at %q and verify traces arrive in SigNoz. Service name: %s`, projectPath, svcHint)

	go func() {
		defer close(events)
		_ = ag.Run(ctx, systemPrompt, userMsg)
	}()

	if isTTY() {
		m := tui.New("init — "+projectPath, events)
		prog := tea.NewProgram(m, tea.WithAltScreen())
		_, err = prog.Run()
		return err
	}
	return runHeadless(events)
}

// ── siginit doctor ────────────────────────────────────────────────────────────

func doctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose why telemetry isn't flowing to SigNoz",
		RunE:  func(_ *cobra.Command, _ []string) error { return runDoctor() },
	}
	cmd.Flags().StringVar(&flagService, "service", "", "Service name to check specifically")
	return cmd
}

func runDoctor() error {
	ctx := context.Background()
	sc := signoz.New(flagSigNozURL)
	collectorAddr := strings.TrimPrefix(flagCollector, "http://")
	collectorAddr = strings.TrimPrefix(collectorAddr, "https://")

	fmt.Printf("\033[1;35msiginit doctor\033[0m  → %s\n\n", flagSigNozURL)
	results := signoz.Doctor(ctx, sc, signoz.DoctorConfig{
		CollectorAddr: collectorAddr,
		ServiceName:   flagService,
		Email:         flagEmail,
		Password:      flagPassword,
		LookbackMins:  15,
	})

	allOK := true
	for _, r := range results {
		if r.OK {
			fmt.Printf("  \033[32m✓\033[0m  [%s] %s\n", r.Layer, r.Message)
		} else {
			allOK = false
			fmt.Printf("  \033[31m✗\033[0m  [%s] %s\n", r.Layer, r.Message)
			if r.Fix != "" {
				fmt.Printf("     \033[33m→\033[0m %s\n", r.Fix)
			}
		}
	}
	fmt.Println()
	if allOK {
		fmt.Println("  \033[32mAll checks passed.\033[0m")
		return nil
	}
	fmt.Println("  \033[31mOne or more checks failed.\033[0m")
	return fmt.Errorf("doctor found issues")
}

// ── siginit verify ────────────────────────────────────────────────────────────

func verifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check whether a service is visible in SigNoz right now",
		RunE:  func(_ *cobra.Command, _ []string) error { return runVerify() },
	}
	cmd.Flags().StringVar(&flagService, "service", "", "Service name to check (required)")
	_ = cmd.MarkFlagRequired("service")
	return cmd
}

func runVerify() error {
	ctx := context.Background()
	sc := signoz.New(flagSigNozURL)
	if err := ensureAuth(ctx, sc, false); err != nil {
		return err
	}
	args, _ := json.Marshal(map[string]any{"service_name": flagService, "lookback_minutes": 15})
	t := &tools.QuerySigNoz{Client: sc}
	result, err := t.Execute(ctx, args)
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

// ── shared ────────────────────────────────────────────────────────────────────

func ensureAuth(ctx context.Context, sc *signoz.Client, register bool) error {
	if register {
		if err := sc.Register(ctx, "Admin", flagEmail, flagPassword, "siginit-org"); err != nil {
			fmt.Printf("  register: %v (continuing)\n", err)
		}
	}
	tok, err := sc.Login(ctx, flagEmail, flagPassword)
	if err != nil {
		return fmt.Errorf("SigNoz login failed: %w\n  hint: run /register in the REPL on first use", err)
	}
	sc.WithToken(tok)
	return nil
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func runHeadless(events <-chan agent.Event) error {
	for e := range events {
		switch e.Kind {
		case agent.EventToolCall:
			fmt.Printf("  →  %s\n", e.Message)
		case agent.EventToolResult:
			fmt.Printf("  ←  %s\n", e.Message)
		case agent.EventFinal:
			fmt.Printf("  ✓  %s\n", e.Message)
			return nil
		case agent.EventError:
			fmt.Printf("  ✗  %s\n", e.Message)
			return fmt.Errorf("%s", e.Message)
		}
	}
	return nil
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muddlebee/siginit/internal/agent"
	"github.com/muddlebee/siginit/internal/provider"
	"github.com/muddlebee/siginit/internal/signoz"
	"github.com/muddlebee/siginit/internal/tools"
	"github.com/muddlebee/siginit/internal/tui"
	"github.com/spf13/cobra"
)

var (
	flagProvider    string
	flagAPIKey      string
	flagModel       string
	flagSigNozURL   string
	flagEmail       string
	flagPassword    string
	flagRegister    bool
	flagCollector   string
	flagDryRun      bool
	flagAutoApprove bool
	flagServiceName string
)

func main() {
	root := &cobra.Command{
		Use:          "siginit",
		Short:        "Agentic onboarding CLI for SigNoz — collapse time-to-first-value",
		SilenceUsage: true,
		Long: `siginit detects your stack, generates OpenTelemetry instrumentation,
wires it to SigNoz, and verifies real telemetry arrived before declaring success.

  siginit init [project-path]   # instrument + verify
  siginit doctor                # diagnose why data isn't flowing
  siginit verify --service X    # check a service right now`,
	}

	root.PersistentFlags().StringVar(&flagProvider, "provider", "deepseek", "LLM provider: deepseek, openai")
	root.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "Provider API key (default: from env)")
	root.PersistentFlags().StringVar(&flagModel, "model", "", "Model override (default: provider default)")
	root.PersistentFlags().StringVar(&flagSigNozURL, "signoz-url", "http://localhost:8080", "SigNoz base URL")
	root.PersistentFlags().StringVar(&flagEmail, "email", "admin@siginit.local", "SigNoz admin email")
	root.PersistentFlags().StringVar(&flagPassword, "password", "Admin123!", "SigNoz admin password")
	root.PersistentFlags().BoolVar(&flagRegister, "register", false, "Register admin account on first run")
	root.PersistentFlags().StringVar(&flagCollector, "collector", "http://localhost:4318", "OTLP HTTP collector endpoint")

	root.AddCommand(initCmd(), doctorCmd(), verifyCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── siginit init ──────────────────────────────────────────────────────────────

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [project-path]",
		Short: "Instrument a project and verify first trace in SigNoz",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			return runInit(path)
		},
	}
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview agent actions without executing mutating tools")
	cmd.Flags().BoolVar(&flagAutoApprove, "yes", false, "Auto-approve all mutating tool calls")
	cmd.Flags().StringVar(&flagServiceName, "service", "", "OTEL service name override")
	return cmd
}

func runInit(projectPath string) error {
	ctx := context.Background()

	llmClient, prov, err := provider.New(flagProvider, flagAPIKey)
	if err != nil {
		return err
	}
	model := flagModel
	if model == "" {
		model = prov.DefaultModel
	}

	sc := signoz.New(flagSigNozURL)
	if err := ensureAuth(ctx, sc); err != nil {
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
		Model:       model,
		DryRun:      flagDryRun,
		AutoApprove: flagAutoApprove,
	}
	if flagAutoApprove {
		agCfg.PermissionFn = func(_, _ string) bool { return true }
	}

	ag := agent.New(llmClient, registry, agCfg, events)

	svcHint := flagServiceName
	if svcHint == "" {
		svcHint = "(derive from project name)"
	}

	systemPrompt := fmt.Sprintf(`You are siginit, an expert OpenTelemetry instrumentation agent.
Your goal: instrument the developer's project so traces flow into SigNoz, then verify it worked.

Rules:
- Call inspect_project FIRST before anything else.
- Prefer zero-code auto-instrumentation: NODE_OPTIONS for Node.js, opentelemetry-instrument for Python.
- OTel collector: %s  |  SigNoz: %s
- You MUST call query_signoz to verify traces — never declare success without it.
- On verify failure: fix the most likely issue, then call query_signoz once more.
- Be concise. Developers want commands and results, not paragraphs.`, flagCollector, flagSigNozURL)

	userMsg := fmt.Sprintf(`Instrument the project at %q and verify traces arrive in SigNoz.
Service name: %s
Collector: %s`, projectPath, svcHint, flagCollector)

	go func() {
		defer close(events)
		_ = ag.Run(ctx, systemPrompt, userMsg)
	}()

	m := tui.New("init — "+projectPath, events)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

// ── siginit doctor ────────────────────────────────────────────────────────────

func doctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose why telemetry isn't flowing to SigNoz",
		RunE:  func(_ *cobra.Command, _ []string) error { return runDoctor() },
	}
	cmd.Flags().StringVar(&flagServiceName, "service", "", "Service name to check specifically")
	return cmd
}

func runDoctor() error {
	ctx := context.Background()
	sc := signoz.New(flagSigNozURL)

	// Strip protocol for TCP dial.
	collectorAddr := strings.TrimPrefix(flagCollector, "http://")
	collectorAddr = strings.TrimPrefix(collectorAddr, "https://")

	cfg := signoz.DoctorConfig{
		CollectorAddr: collectorAddr,
		ServiceName:   flagServiceName,
		Email:         flagEmail,
		Password:      flagPassword,
		LookbackMins:  15,
	}

	fmt.Printf("\033[1;35msiginit doctor\033[0m  → %s\n\n", flagSigNozURL)

	results := signoz.Doctor(ctx, sc, cfg)

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
	fmt.Println("  \033[31mOne or more checks failed — see fixes above.\033[0m")
	return fmt.Errorf("doctor found issues")
}

// ── siginit verify ────────────────────────────────────────────────────────────

func verifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check whether a service is visible in SigNoz right now",
		RunE:  func(_ *cobra.Command, _ []string) error { return runVerify() },
	}
	cmd.Flags().StringVar(&flagServiceName, "service", "", "Service name to check (required)")
	_ = cmd.MarkFlagRequired("service")
	return cmd
}

func runVerify() error {
	ctx := context.Background()
	sc := signoz.New(flagSigNozURL)
	if err := ensureAuth(ctx, sc); err != nil {
		return err
	}

	args, _ := json.Marshal(map[string]any{
		"service_name":     flagServiceName,
		"lookback_minutes": 15,
	})

	t := &tools.QuerySigNoz{Client: sc}
	result, err := t.Execute(ctx, args)
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

// ── shared ────────────────────────────────────────────────────────────────────

func ensureAuth(ctx context.Context, sc *signoz.Client) error {
	if flagRegister {
		fmt.Printf("Registering admin (%s)…\n", flagEmail)
		if err := sc.Register(ctx, "Admin", flagEmail, flagPassword, "siginit-org"); err != nil {
			fmt.Printf("  register: %v (continuing — may already exist)\n", err)
		}
	}
	tok, err := sc.Login(ctx, flagEmail, flagPassword)
	if err != nil {
		return fmt.Errorf("SigNoz login failed: %w\n  hint: --register on first run; --email/--password to match your account", err)
	}
	sc.WithToken(tok)
	return nil
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
)

const (
	defaultMaxIterations = 20
	schemaVersion        = "v1"
)

// Config controls agent behaviour.
type Config struct {
	Model         string
	MaxIterations int
	DryRun        bool
	// AutoApprove skips the permission gate for mutating tools (use in tests).
	AutoApprove bool
	// PermissionFn is called before executing a mutating tool.
	// Return true to allow, false to deny.
	// If nil, the loop sends an EventPermission and waits for an approval message on ApproveCh.
	PermissionFn func(toolName, args string) bool
}

// Agent is the thin agentic loop on top of openai-go.
type Agent struct {
	client   *openai.Client
	tools    Registry
	cfg      Config
	events   chan<- Event
}

// New creates an Agent. events receives all agent events; callers must drain it.
func New(client *openai.Client, tools Registry, cfg Config, events chan<- Event) *Agent {
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = defaultMaxIterations
	}
	return &Agent{client: client, tools: tools, cfg: cfg, events: events}
}

// Run executes the agentic loop: system prompt + user message → iterate until done.
func (a *Agent) Run(ctx context.Context, systemPrompt, userMsg string) error {
	msgs := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userMsg),
	}

	toolDefs := a.buildToolDefs()

	for iter := range a.cfg.MaxIterations {
		_ = iter
		params := openai.ChatCompletionNewParams{
			Model:    a.cfg.Model,
			Messages: msgs,
		}
		if len(toolDefs) > 0 {
			params.Tools = toolDefs
		}

		resp, err := a.client.Chat.Completions.New(ctx, params)
		if err != nil {
			a.emit(Event{Kind: EventError, Result: err.Error(), Message: "LLM error"})
			return err
		}

		choice := resp.Choices[0]

		// If the model returned a final text response with no tool calls → done.
		if len(choice.Message.ToolCalls) == 0 {
			content := choice.Message.Content
			a.emit(Event{Kind: EventFinal, Result: content, Message: content})
			return nil
		}

		// Append the assistant message with its tool calls.
		msgs = append(msgs, choice.Message.ToParam())

		// Execute each tool call.
		for _, tc := range choice.Message.ToolCalls {
			name := tc.Function.Name
			rawArgs := tc.Function.Arguments

			a.emit(Event{Kind: EventToolCall, Tool: name, Args: rawArgs, Message: fmt.Sprintf("→ %s(%s)", name, rawArgs)})

			result, execErr := a.executeTool(ctx, name, rawArgs)
			if execErr != nil {
				result = fmt.Sprintf("error: %s", execErr.Error())
			}

			a.emit(Event{Kind: EventToolResult, Tool: name, Result: result, Message: fmt.Sprintf("← %s: %s", name, truncate(result, 120))})

			msgs = append(msgs, openai.ToolMessage(result, tc.ID))
		}
	}

	err := fmt.Errorf("exceeded max iterations (%d)", a.cfg.MaxIterations)
	a.emit(Event{Kind: EventError, Result: err.Error(), Message: err.Error()})
	return err
}

func (a *Agent) executeTool(ctx context.Context, name string, rawArgs string) (string, error) {
	t, ok := a.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}

	if !t.ReadOnly() {
		if a.cfg.DryRun {
			return fmt.Sprintf("[dry-run] would execute %s with args %s", name, rawArgs), nil
		}
		if a.cfg.PermissionFn != nil && !a.cfg.PermissionFn(name, rawArgs) {
			return "permission denied by user", nil
		}
	}

	return t.Execute(ctx, json.RawMessage(rawArgs))
}

func (a *Agent) buildToolDefs() []openai.ChatCompletionToolParam {
	defs := make([]openai.ChatCompletionToolParam, 0, len(a.tools))
	for _, t := range a.tools {
		var schema openai.FunctionParameters
		if err := json.Unmarshal(t.Schema(), &schema); err != nil {
			schema = openai.FunctionParameters{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, openai.ChatCompletionToolParam{
			Type: "function",
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name(),
				Description: openai.String(t.Description()),
				Parameters:  schema,
			},
		})
	}
	return defs
}

func (a *Agent) emit(e Event) {
	select {
	case a.events <- e:
	default:
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

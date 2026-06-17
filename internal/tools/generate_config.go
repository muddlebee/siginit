package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/muddlebee/siginit/internal/generate"
)

// GenerateConfig produces an OTel instrumentation config for a detected stack.
// ReadOnly — it only generates text, doesn't modify files.
type GenerateConfig struct {
	CollectorHTTP string
}

func (t *GenerateConfig) Name() string       { return "generate_otel_config" }
func (t *GenerateConfig) ReadOnly() bool      { return true }
func (t *GenerateConfig) Description() string {
	return "Generate OpenTelemetry instrumentation config (install command, start script, env vars, code snippet) for a given language and framework. Call this after inspect_project to get the exact commands to apply."
}
func (t *GenerateConfig) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["language", "service_name"],
		"properties": {
			"language":     {"type": "string", "description": "Language: javascript, typescript, python, go"},
			"framework":    {"type": "string", "description": "Framework: express, fastify, flask, django, fastapi (optional)"},
			"service_name": {"type": "string", "description": "OTEL service name to use (e.g. demo-express-app)"}
		}
	}`)
}

func (t *GenerateConfig) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Language    string `json:"language"`
		Framework   string `json:"framework"`
		ServiceName string `json:"service_name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Language == "" {
		return "", fmt.Errorf("language is required")
	}
	if params.ServiceName == "" {
		return "", fmt.Errorf("service_name is required")
	}

	cfg := generate.Generate(params.Language, params.Framework, params.ServiceName, t.CollectorHTTP)

	out := map[string]any{
		"service_name":       cfg.ServiceName,
		"collector_endpoint": cfg.CollectorEndpoint,
		"install_cmd":        cfg.InstallCmd,
		"start_script":       cfg.StartScript,
		"env_vars":           cfg.EnvVars,
		"code_snippet":       cfg.DocSnippet,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

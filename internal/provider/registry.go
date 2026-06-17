package provider

import (
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type Provider struct {
	Name          string
	BaseURL       string
	APIKey        string
	DefaultModel  string
	SupportsTools bool
}

var defaults = map[string]Provider{
	"deepseek": {
		Name:          "deepseek",
		BaseURL:       "https://api.deepseek.com/v1",
		DefaultModel:  "deepseek-v4-flash",
		SupportsTools: true,
	},
	"openai": {
		Name:          "openai",
		BaseURL:       "https://api.openai.com/v1",
		DefaultModel:  "gpt-4o-mini",
		SupportsTools: true,
	},
}

// New returns an openai-go client for the named provider.
// apiKey and model override defaults when non-empty.
func New(name, apiKey, model string) (*openai.Client, Provider, error) {
	p, ok := defaults[name]
	if !ok {
		return nil, p, fmt.Errorf("unknown provider %q; available: deepseek, openai", name)
	}

	if apiKey != "" {
		p.APIKey = apiKey
	}
	if p.APIKey == "" {
		// last-resort: check env
		p.APIKey = os.Getenv(envKeyFor(name))
	}
	if p.APIKey == "" {
		return nil, p, fmt.Errorf("no API key for provider %q", name)
	}

	if model != "" {
		p.DefaultModel = model
	}

	client := openai.NewClient(
		option.WithAPIKey(p.APIKey),
		option.WithBaseURL(p.BaseURL),
	)
	return &client, p, nil
}

func envKeyFor(name string) string {
	switch name {
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	}
	return ""
}

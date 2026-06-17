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
	APIKeyEnv     string
	DefaultModel  string
	SupportsTools bool
}

var registry = map[string]Provider{
	"deepseek": {
		Name:          "deepseek",
		BaseURL:       "https://api.deepseek.com/v1",
		APIKeyEnv:     "DEEPSEEK_API_KEY",
		DefaultModel:  "deepseek-chat",
		SupportsTools: true,
	},
	"openai": {
		Name:          "openai",
		BaseURL:       "https://api.openai.com/v1",
		APIKeyEnv:     "OPENAI_API_KEY",
		DefaultModel:  "gpt-4o-mini",
		SupportsTools: true,
	},
}

func Get(name string) (Provider, bool) {
	p, ok := registry[name]
	return p, ok
}

func Names() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	return names
}

// New returns an openai-go client configured for the named provider.
// apiKey overrides the env var if non-empty.
func New(name, apiKey string) (*openai.Client, Provider, error) {
	p, ok := registry[name]
	if !ok {
		return nil, p, fmt.Errorf("unknown provider %q; available: %v", name, Names())
	}
	if !p.SupportsTools {
		return nil, p, fmt.Errorf("provider %q does not support tool calling", name)
	}
	key := apiKey
	if key == "" {
		key = os.Getenv(p.APIKeyEnv)
	}
	if key == "" {
		return nil, p, fmt.Errorf("no API key: set %s or pass --api-key", p.APIKeyEnv)
	}
	client := openai.NewClient(
		option.WithAPIKey(key),
		option.WithBaseURL(p.BaseURL),
	)
	return &client, p, nil
}

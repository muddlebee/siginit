package agent

import (
	"context"
	"encoding/json"
)

// Tool is the interface every siginit tool must implement.
type Tool interface {
	Name() string
	Description() string
	// Schema returns the JSON Schema for the tool's parameters (object).
	Schema() json.RawMessage
	// ReadOnly returns true if the tool never modifies the filesystem or runs processes.
	ReadOnly() bool
	// Execute runs the tool and returns a result string (shown to the LLM).
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry holds all available tools keyed by name.
type Registry map[string]Tool

func NewRegistry(tools ...Tool) Registry {
	r := make(Registry, len(tools))
	for _, t := range tools {
		r[t.Name()] = t
	}
	return r
}

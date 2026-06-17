package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type ReadFile struct{}

func (t *ReadFile) Name() string        { return "read_file" }
func (t *ReadFile) ReadOnly() bool       { return true }
func (t *ReadFile) Description() string { return "Read the contents of a file." }
func (t *ReadFile) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Relative or absolute file path"}
		}
	}`)
}

func (t *ReadFile) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

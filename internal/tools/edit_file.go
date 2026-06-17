package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EditFile writes or patches a file. Mutating — subject to permission gate.
type EditFile struct{}

func (t *EditFile) Name() string        { return "edit_file" }
func (t *EditFile) ReadOnly() bool       { return false }
func (t *EditFile) Description() string {
	return "Write content to a file. Use mode=replace to overwrite, mode=patch to replace old_str with new_str."
}
func (t *EditFile) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "mode"],
		"properties": {
			"path":    {"type": "string", "description": "File path to write"},
			"mode":    {"type": "string", "enum": ["replace", "patch"], "description": "replace overwrites the file; patch replaces old_str with new_str"},
			"content": {"type": "string", "description": "Full file content (used with mode=replace)"},
			"old_str": {"type": "string", "description": "String to find and replace (used with mode=patch)"},
			"new_str": {"type": "string", "description": "Replacement string (used with mode=patch)"}
		}
	}`)
}

func (t *EditFile) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Content string `json:"content"`
		OldStr  string `json:"old_str"`
		NewStr  string `json:"new_str"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	switch params.Mode {
	case "replace":
		if err := os.WriteFile(params.Path, []byte(params.Content), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %d bytes to %s", len(params.Content), params.Path), nil

	case "patch":
		existing, err := os.ReadFile(params.Path)
		if err != nil {
			return "", err
		}
		patched := strings.Replace(string(existing), params.OldStr, params.NewStr, 1)
		if patched == string(existing) {
			return "", fmt.Errorf("old_str not found in %s", params.Path)
		}
		if err := os.WriteFile(params.Path, []byte(patched), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("patched %s", params.Path), nil

	default:
		return "", fmt.Errorf("unknown mode %q (use replace or patch)", params.Mode)
	}
}

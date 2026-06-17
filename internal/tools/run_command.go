package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const defaultCommandTimeout = 30 * time.Second

// RunCommand executes a shell command. Mutating — subject to permission gate.
type RunCommand struct{}

func (t *RunCommand) Name() string        { return "run_command" }
func (t *RunCommand) ReadOnly() bool       { return false }
func (t *RunCommand) Description() string {
	return "Run a shell command in the project directory and return its output. Use sparingly — prefer read_file and edit_file where possible."
}
func (t *RunCommand) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["command"],
		"properties": {
			"command":    {"type": "string", "description": "Shell command to run (passed to bash -c)"},
			"workdir":    {"type": "string", "description": "Working directory (default: current dir)"},
			"timeout_s":  {"type": "number", "description": "Timeout in seconds (default 30)"}
		}
	}`)
}

func (t *RunCommand) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Command   string  `json:"command"`
		Workdir   string  `json:"workdir"`
		TimeoutS  float64 `json:"timeout_s"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultCommandTimeout
	if params.TimeoutS > 0 {
		timeout = time.Duration(params.TimeoutS * float64(time.Second))
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", params.Command)
	if params.Workdir != "" {
		cmd.Dir = params.Workdir
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		combined := out.String() + errOut.String()
		return "", fmt.Errorf("command failed: %w\n%s", err, combined)
	}

	result := out.String()
	if errOut.Len() > 0 {
		result += "\nstderr:\n" + errOut.String()
	}
	return result, nil
}

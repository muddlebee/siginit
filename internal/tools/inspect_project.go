package tools

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InspectProject lists key project files and infers the stack.
type InspectProject struct{}

func (t *InspectProject) Name() string        { return "inspect_project" }
func (t *InspectProject) ReadOnly() bool       { return true }
func (t *InspectProject) Description() string {
	return "List the most relevant files in the project directory and infer the tech stack (language, framework, package manager). Always call this first."
}
func (t *InspectProject) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Project root directory (default: current dir)"}
		}
	}`)
}

func (t *InspectProject) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &params)
	root := params.Path
	if root == "" {
		root = "."
	}

	var interesting []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isInteresting(d.Name()) {
			rel, _ := filepath.Rel(root, path)
			interesting = append(interesting, rel)
		}
		return nil
	})

	stack := detectStack(root)

	out := map[string]any{
		"files": interesting,
		"stack": stack,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case "node_modules", ".git", "vendor", "dist", "build", ".next", "__pycache__", ".venv", "venv":
		return true
	}
	return false
}

func isInteresting(name string) bool {
	interesting := []string{
		"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
		"go.mod", "go.sum",
		"requirements.txt", "pyproject.toml", "setup.py", "Pipfile",
		"Cargo.toml",
		"Makefile", "Dockerfile", "docker-compose.yml", "docker-compose.yaml",
		".env", ".env.example",
		"tsconfig.json", "babel.config.js", "webpack.config.js",
		"server.js", "index.js", "app.js", "main.go", "main.py", "app.py",
	}
	for _, n := range interesting {
		if name == n {
			return true
		}
	}
	return false
}

type Stack struct {
	Language  string `json:"language"`
	Framework string `json:"framework"`
	Runtime   string `json:"runtime"`
}

func detectStack(root string) Stack {
	s := Stack{}

	if fileExists(filepath.Join(root, "package.json")) {
		s.Language = "javascript"
		s.Runtime = "node"
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err == nil {
			raw := string(data)
			switch {
			case strings.Contains(raw, `"express"`):
				s.Framework = "express"
			case strings.Contains(raw, `"fastify"`):
				s.Framework = "fastify"
			case strings.Contains(raw, `"koa"`):
				s.Framework = "koa"
			case strings.Contains(raw, `"next"`):
				s.Framework = "nextjs"
			case strings.Contains(raw, `"hapi"`):
				s.Framework = "hapi"
			}
		}
		if fileExists(filepath.Join(root, "tsconfig.json")) {
			s.Language = "typescript"
		}
		return s
	}

	if fileExists(filepath.Join(root, "go.mod")) {
		s.Language = "go"
		s.Runtime = "go"
		return s
	}

	if fileExists(filepath.Join(root, "requirements.txt")) || fileExists(filepath.Join(root, "pyproject.toml")) {
		s.Language = "python"
		s.Runtime = "python"
		if fileExists(filepath.Join(root, "requirements.txt")) {
			data, _ := os.ReadFile(filepath.Join(root, "requirements.txt"))
			raw := strings.ToLower(string(data))
			switch {
			case strings.Contains(raw, "flask"):
				s.Framework = "flask"
			case strings.Contains(raw, "django"):
				s.Framework = "django"
			case strings.Contains(raw, "fastapi"):
				s.Framework = "fastapi"
			}
		}
		return s
	}

	return s
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

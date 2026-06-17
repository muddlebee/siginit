# siginit — implementation progress

## What this is
Agentic onboarding CLI for SigNoz. Detects stack → generates OTel instrumentation →
wires to SigNoz → **verifies real traces arrived** (closed-loop, LLM cannot self-declare
success). See plan: `/home/muddles/.claude/plans/typed-wondering-dawn.md`

## Status: Phase 0–3 complete, build green

---

## Completed slices

| Slice | Feature | Files | Status |
|-------|---------|-------|--------|
| 1 | Scaffold, go.mod, deps | `go.mod`, `go.sum` | ✓ |
| 2 | F1 — Provider registry (DeepSeek + OpenAI, base-URL swap) | `internal/provider/registry.go` | ✓ |
| 3 | F1 — Agent event types + tool interface + loop | `internal/agent/{tool,event,loop}.go` | ✓ |
| 4 | F3 — SigNoz client (health, register, login, services, count_spans) | `internal/signoz/client.go` | ✓ |
| 5 | F3 — Built-in tools (inspect_project, read_file, edit_file, run_command, query_signoz) | `internal/tools/` | ✓ |
| 6 | F2 — OTel config generator (Node/Python/Go templates) | `internal/generate/otel.go` | ✓ |
| 7 | F4 — Charm TUI (Bubble Tea model, goroutine→channel bridge) | `internal/tui/model.go` | ✓ |
| 8 | F5 — Doctor diagnostic (5-layer: TCP, health, auth, services, spans) | `internal/signoz/doctor.go` | ✓ |
| 9 | Cobra CLI (init, doctor, verify) | `cmd/siginit/main.go` | ✓ |
| 10 | Fixture: Node Express demo app | `fixtures/node-express/` | ✓ |

**Build:** `go build ./...` → exit 0  
**Help check:** `go run ./cmd/siginit --help` → all 3 subcommands visible

---

## What's left

### F6 — Guardrails (partially done)
- [x] `--dry-run` flag (blocks mutating tools, wired in loop.go)
- [x] `--yes` auto-approve flag
- [x] Max-iterations cap (defaultMaxIterations=20 in loop.go)
- [ ] Permission prompt TUI (currently just a PermissionFn callback — needs interactive huh.Confirm wired into the TUI for when --yes is not set and a mutating tool is called)
- [ ] Edit-staleness check (read file → check sha before edit_file applies patch)

### Phase 0 — Live integration test (must be done manually, not coded)
1. `cd /home/muddles/Codes/signoz/deploy/docker && docker compose up -d`
2. Wait: `curl -s localhost:8080/api/v1/health`
3. Register: `go run ./cmd/siginit --register --email admin@siginit.local --password Admin123! verify --service demo` (expect login ok, no spans yet)
4. Emit test spans: `docker run --rm ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest traces --otlp-endpoint localhost:4318 --otlp-http --service-name demo --duration 5s`
5. Verify: `go run ./cmd/siginit verify --service demo`
6. Doctor: `go run ./cmd/siginit doctor --service demo`
7. Full init: `go run ./cmd/siginit init fixtures/node-express`

### Init TUI — generate.OTelConfig not wired to agent yet
- `internal/generate/otel.go` is implemented but not called in the agent loop.
- The agent currently generates the instrumentation itself via the LLM. The generate package should be exposed as an additional tool (`generate_otel_config`) so the agent can call it deterministically for the install/start commands, then have the LLM handle the nuances.
- Add `tools/generate_config.go` wrapping `generate.Generate(...)` as a ReadOnly tool.

### Tests
- [ ] Unit test: agent loop with mock LLM provider (inject stubbed tool_calls response)
- [ ] Unit test: signoz.Client against httptest.Server returning canned responses
- [ ] Unit test: generate.Generate outputs for node/python/go

### Stretch
- [ ] Streaming responses (openai-go supports SSE — wire into EventThinking events)
- [ ] `--provider claude` via openai-compatible Claude endpoint
- [ ] `siginit cloud` mode (ingestion key + cloud.signoz.io)
- [ ] Git: initial commit with passing build

---

## Repo layout (as built)

```
siginit/
  cmd/siginit/main.go           cobra root: init | doctor | verify
  internal/
    provider/registry.go        F1 — provider registry (deepseek, openai)
    agent/
      tool.go                   Tool interface + Registry
      event.go                  EventKind + Event types
      loop.go                   thin agent loop (send→tool→repeat)
    tools/
      inspect_project.go        list files + detect stack
      read_file.go
      edit_file.go              mutating (replace/patch modes)
      run_command.go            mutating (bash -c)
      query_signoz.go           verify telemetry (THE MOAT)
    signoz/
      client.go                 REST client: health/register/login/services/count_spans
      doctor.go                 5-layer diagnostic
    generate/
      otel.go                   OTel config templates (node/python/go)
    tui/
      model.go                  Bubble Tea model + goroutine→channel bridge
  fixtures/
    node-express/               demo Express app for init demo
  progress.md                   this file
```

---

## Key API facts (verified against upstream/main + openapi.yml)

| Surface | Endpoint | Auth |
|---------|----------|------|
| Health | `GET /api/v1/health` | Open |
| Register (first user) | `POST /api/v1/register` | Open |
| Login | `POST /api/v2/sessions/email_password` | Open → returns `accessToken` |
| Services list | `GET /api/v1/services/list?start=&end=` | Bearer token |
| Span count | `POST /api/v5/query_range` | Bearer token |
| Write path | OTLP HTTP `localhost:4318` / gRPC `localhost:4317` | None (local) |

---

## Resume instructions (for next session)

```
# verify build still green
cd /home/muddles/Codes/siginit && go build ./...

# pick up from "What's left" above
# highest priority next step:
# 1. Wire generate_config tool
# 2. Run Phase 0 live integration test
# 3. Add permission-gate TUI prompt (huh.Confirm)
# 4. Unit tests
```

# AGENTS.md — siginit agentic behavior reference

## What siginit is

siginit is an agentic CLI that instruments a developer's project for OpenTelemetry and
verifies that real traces arrive in SigNoz before declaring success. The agent loop is
deliberately thin: tool-use over the OpenAI-compatible API, no framework magic.

## Agent loop (`internal/agent/loop.go`)

```
user message
  └─▶ LLM (with tool schemas in system prompt)
        └─▶ tool_calls in response
              └─▶ execute via Registry
                    └─▶ append tool results
                          └─▶ repeat until EventFinal | EventError | max-iterations
```

- **Max iterations:** 20 (constant `defaultMaxIterations`)
- **Parallel tool calls:** supported — the loop executes all `tool_calls` in a single
  LLM response concurrently (one goroutine per call).
- **Permission gate:** `PermissionFn(toolName, input) bool` — called before any mutating
  tool. Returning `false` skips the tool and sends an `EventPermission` event.
- **`--dry-run`:** sets all mutating tools to no-op (returns a dry-run notice without
  executing).
- **`--yes`:** auto-approves all permission gates.

## Tools (`internal/tools/`)

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `inspect_project` | ✓ | List project files + detect stack (Node/Python/Go) |
| `read_file` | ✓ | Read a file from the project directory |
| `edit_file` | ✗ | Replace or patch content in a file |
| `run_command` | ✗ | Run a shell command (bash -c) with 30s timeout |
| `generate_config` | ✓ | Generate OTel SDK config for detected stack |
| `query_signoz` | ✓ | **The moat** — query SigNoz for real span counts |

### query_signoz — the verification engine

The agent MUST call `query_signoz` to verify traces rather than inferring success from
command output. This is the closed-loop guarantee: SigNoz query API is the ground
truth, not the LLM's self-assessment.

Input:
```json
{
  "service_name": "my-app",
  "lookback_minutes": 5
}
```

Output: `{ service_found, span_count, all_services, verdict, window }`

A `service_found: true` means the service appeared in `/api/v1/services/list`.
`span_count` is the count from `/api/v5/query_range` (may lag by up to 1 minute).

## System prompt contract (`runInit` in `cmd/siginit/main.go`)

The agent is told:
1. Call `inspect_project` FIRST — stack detection before any instrumentation
2. Prefer zero-code auto-instrumentation (NODE_OPTIONS, opentelemetry-instrument)
3. MUST call `query_signoz` to verify — never declare success without it
4. On verify failure: fix the most likely issue, then re-verify once more
5. Be concise — developers want commands and results

## Provider registry (`internal/provider/registry.go`)

| Provider | Base URL | Default model | Env var |
|----------|----------|---------------|---------|
| `deepseek` (default) | `https://api.deepseek.com/v1` | `deepseek-v4-flash` | `DEEPSEEK_API_KEY` |
| `openai` | `https://api.openai.com/v1` | `gpt-4o-mini` | `OPENAI_API_KEY` |

Switch provider: `siginit init --provider openai --model gpt-4o ...`

API keys are loaded from `.env` via `godotenv`. Never hardcoded.

## SigNoz integration (`internal/signoz/`)

### Auth flow
```
GET  /api/v2/sessions/context?email=X&ref=  →  orgId
POST /api/v2/sessions/email_password         →  accessToken (Bearer)
```

### Verified endpoints
| Endpoint | Auth | Used by |
|----------|------|---------|
| `GET /api/v1/health` | None | doctor layer 2 |
| `POST /api/v1/register` | None (first user only) | `--register` flag |
| `GET /api/v1/services/list` | Bearer | verify + doctor layer 4 |
| `POST /api/v5/query_range` | Bearer | CountSpans, doctor layer 5 |

### `/api/v1/services/list` response shape
Bare `[]string` (service names), NOT `{data: [{serviceName}]}`.

### `/api/v5/query_range` response traversal
```
data → data → results[] → aggregations[] → series[] → values[] → value (float64)
```

## Running siginit

```bash
# First run — register + verify (no data yet)
siginit --register --email admin@siginit.local --password 'Admin@12345678' verify --service demo

# Emit test spans
docker run --rm --network host ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest \
  traces --otlp-endpoint localhost:4318 --otlp-http --otlp-insecure --service demo --duration 5s

# Verify data arrived
siginit verify --service demo

# Diagnose (all 5 layers)
siginit doctor --service demo

# Full agentic instrument + verify (interactive TUI, needs a real terminal)
siginit init fixtures/node-express
siginit init --dry-run fixtures/node-express   # preview only
siginit init --yes fixtures/node-express        # auto-approve all actions
```

## Environment

Copy `.env.example` to `.env` and fill in your API key:
```
DEEPSEEK_API_KEY=sk-...
OPENAI_API_KEY=       # optional
```

`.env` is gitignored.

## Non-interactive / headless mode

When stdout is not a terminal (CI, pipes), `siginit init` automatically falls back from
the Bubble Tea TUI to plain stdout event printing. No flags needed.

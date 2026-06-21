# siginit

Agentic onboarding CLI for [SigNoz](https://signoz.io). Detects your stack, generates
OpenTelemetry instrumentation, wires it to a SigNoz instance, and **verifies real traces
arrived** before declaring success.

One command launches an interactive REPL — everything else is a slash command:

```
$ siginit
siginit> /register                     # first run only — create SigNoz admin
siginit> /init fixtures/node-express   # instrument a project + verify traces
siginit> /verify demo-express-app      # check a service is visible right now
siginit> /doctor                       # diagnose why data isn't flowing
siginit> /help                         # list commands
siginit> /quit
```

The status bar always shows where you're pointed:
`● localhost:8080  deepseek/deepseek-v4-flash  ✓ auth`.

Every slash command runs the same real integration (live SigNoz auth, `query_range`,
the full agent loop) — nothing is stubbed. The same actions exist as one-shot
subcommands for CI/scripting; see [Scripting & CI](#scripting--ci).

## Why

The biggest adoption bottleneck for any OTel-native observability product is
**time-to-first-value**: developers sign up, then get stuck wiring up OpenTelemetry —
wrong endpoint, missing SDK init, dropped spans. If they don't see their own data in
minutes, they churn.

The SigNoz UI can't fix this because the friction lives **on the developer's machine and
inside their codebase**. A CLI lives exactly there.

**The moat: closed-loop verification.** Most onboarding tools stop at "here's your
config, good luck." siginit doesn't let the LLM declare success — the SigNoz query API
is the ground truth. The agent edits → runs → queries SigNoz → sees whether real spans
landed → self-corrects.

## How it works

```
inspect_project → read files → generate OTel config → install SDK
    → start app → curl (generate traces) → query_signoz ✓
```

Every step is a tool call. The agent loop runs until `query_signoz` returns
`service_found: true` or max iterations is reached. In the REPL, `/init` auto-approves
tool calls so the run is hands-off; the `siginit init` subcommand gates mutating steps
behind approval unless `--yes` is passed.

## Quick start

### Prerequisites

- Go 1.21+
- A running SigNoz instance ([local Docker](https://signoz.io/docs/install/docker/))
- DeepSeek or OpenAI API key

### Install

```bash
git clone https://github.com/muddlebee/siginit
cd siginit
go build -o bin/siginit ./cmd/siginit
```

### Configure

```bash
cp .env.example .env
# edit .env and add your API key:
# DEEPSEEK_API_KEY=sk-...
# OPENAI_API_KEY=sk-...   (optional)
```

### Run

```bash
./bin/siginit
```

That's the whole thing — one command opens the REPL. Then drive it with slash commands.

## REPL commands

| Command | What it does |
|---------|--------------|
| `/init [path]` | Instrument the project at `path` (default `.`) and verify traces reach SigNoz. Streams every agent tool call live. |
| `/verify <svc>` | Instant check: is service `svc` visible in SigNoz right now? |
| `/doctor [svc]` | Five-layer diagnostic of why traces aren't flowing. |
| `/register` | Register the admin account in SigNoz (first run only), then authenticate. |
| `/help` | List commands. |
| `/clear` | Clear the log. |
| `/quit` | Exit. |

Pointing siginit somewhere else (different SigNoz URL, provider, collector) is done with
flags at launch — they apply to the whole REPL session:

```bash
./bin/siginit --provider openai --model gpt-4o-mini \
  --signoz-url http://localhost:8080 \
  --collector http://localhost:4318
```

### `/doctor` output

Five-layer diagnostic: TCP reachability → SigNoz health → auth → services list → span count.

```
  ✓  [collector_tcp]   collector reachable at localhost:4318
  ✓  [signoz_health]   SigNoz is healthy
  ✓  [signoz_auth]     authenticated
  ✓  [signoz_services] service "my-app" found
  ✓  [span_count]      42 spans from "my-app"
```

### `/verify` output

```json
{
  "service_found": true,
  "span_count": 42,
  "verdict": "SUCCESS: service \"my-app\" is visible in SigNoz with 42 spans",
  "window": "last 15 minutes"
}
```

## Scripting & CI

For non-interactive use the same actions are exposed as one-shot subcommands. They share
the agent loop and verify client with the REPL — they're just headless entry points (they
auto-detect a non-TTY and stream plain-text output).

```bash
# Instrument a project end-to-end, auto-approving all edits/commands
siginit init --yes ./my-app

# Diagnose why traces aren't flowing
siginit doctor

# Check a specific service
siginit verify --service my-app
```

```
Flags (init):
  --yes          Auto-approve all file edits and command runs
  --dry-run      Preview agent actions without executing anything
  --service      Override the OTel service name
  --provider     LLM provider: deepseek (default) | openai
  --model        Model override (default: provider's default)
  --collector    OTLP HTTP endpoint (default: http://localhost:4318)
  --signoz-url   SigNoz base URL (default: http://localhost:8080)
```

## Providers

| Provider | Model | Speed | Set via |
|----------|-------|-------|---------|
| `deepseek` (default) | `deepseek-v4-flash` | ~15–25s/call | `DEEPSEEK_API_KEY` |
| `openai` | `gpt-4o-mini` | ~2–4s/call | `OPENAI_API_KEY` |

Switch with `--provider openai --model gpt-4o-mini`. Any OpenAI-compatible endpoint works.

## Stack support

| Language | Framework | Instrumentation |
|----------|-----------|-----------------|
| Node.js | Express, Fastify, Koa | Zero-code via `NODE_OPTIONS` |
| Python | Flask, FastAPI, Django | Zero-code via `opentelemetry-instrument` |
| Go | Any | SDK init snippet |

## Tools the agent can call

| Tool | Description |
|------|-------------|
| `inspect_project` | Detect stack, list files |
| `read_file` | Read project files |
| `edit_file` | Patch or replace file content |
| `run_command` | Run shell commands (install, start, curl) |
| `generate_config` | Generate OTel SDK config for detected stack |
| `query_signoz` | **Verify traces in SigNoz** — the ground truth check |

See [AGENTS.md](AGENTS.md) for the full agentic behavior spec.

## Demo

```bash
# Start SigNoz locally
cd deploy/docker && docker compose up -d

# Launch the REPL
siginit
```

```
siginit> /register
  ✓  registered and authenticated

siginit> /init fixtures/node-express
  instrumenting "fixtures/node-express"…
  →  inspect_project({"path": "fixtures/node-express"})
  ←  stack: javascript / express
  →  generate_config({"language": "javascript", ...})
  →  run_command({"command": "npm install @opentelemetry/auto-instrumentations-node"})
  →  run_command({"command": "nohup node server.js ..."})
  →  query_signoz({"service_name": "demo-express-app"})
  ✓  SUCCESS: service "demo-express-app" is visible in SigNoz with 6 spans
```

## License

MIT

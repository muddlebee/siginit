# Plan: `signoz init` — an agentic onboarding REPL for SigNoz

## Context

**Why this exists.** For an OTel-native, open-source, PLG product like SigNoz, the
biggest adoption bottleneck is **time-to-first-value (TTFV)**: a developer signs up,
then has to wire up OpenTelemetry in their own codebase — wrong endpoint, missing
auth, dropped spans, SDK mismatch — and if they don't see their own data in minutes,
they churn. It is also the #1 support ticket ("why is no data showing up?"). The
SigNoz web UI cannot fix this because the friction lives **on the developer's machine
and inside their codebase**. A CLI/REPL lives exactly there — that's the wedge.

**What we're building (working name `siginit`).** A terminal app with two surfaces:
- `signoz init` — an interactive, **agentic** onboarding REPL that detects the stack,
  generates OTel instrumentation, wires it to a SigNoz endpoint, then **verifies real
  telemetry arrived** before declaring success.
- `signoz doctor` — diagnoses why data isn't flowing, mapping failures to stack layers.

**The moat = closed-loop verification.** Most onboarding tools stop at "here's your
config, good luck." Ours doesn't let the LLM declare success — the SigNoz **query API
is the ground truth**. The agent edits → runs → queries SigNoz → sees whether real
spans landed → self-corrects. This turns an unreliable LLM into a reliable onboarding
agent, and `doctor` falls out of the same engine for free.

**Decisions already locked (from prior discussion):**
- Language **Go** (matches SigNoz backend; credible to the founder).
- LLM client = official **`github.com/openai/openai-go`**. No official OpenAI *Agents*
  SDK exists for Go — we own a thin agent loop.
- **Multi-provider via OpenAI-compatible endpoints** (base URL + key swap): OpenAI and
  DeepSeek primary. BYO-model is part of the pitch.
- TUI via **Charm** (Bubble Tea / Lip Gloss / Huh / Bubbles).
- Tested practically against the **local SigNoz docker-compose** in this repo
  (`deploy/docker/docker-compose.yaml`).

## How the integration sits in (validated against this repo)

**Integration model:** `siginit` is a **standalone external client** — no SigNoz fork,
no binary changes, no plugin/IDE extension, no monorepo PR. The SigNoz binary is the
thing we *point at*, not modify. It integrates with a *running* SigNoz purely over the
network, via the same two public contracts the UI and every OTel SDK already use. We
need a SigNoz running (local compose for the demo; the user's Cloud/self-hosted in real
use), not its source. Upside: no fork to maintain, works against any version exposing
these endpoints, and can later be folded into the official CLI or an MCP server without
architectural change.

The harness touches exactly **two surfaces** and deliberately not the rest of the stack:

**Write path → OTel Collector** (`localhost:4317` gRPC / `4318` HTTP, exposed in compose).
The generated app config exports OTLP here. `doctor` does a TCP dial to catch
"endpoint unreachable" first.

**Read/verify path → SigNoz binary** (`localhost:8080`). *Re-verified against
`upstream/main` (local `main` was 800 commits / ~4 months stale — routing was since
refactored into `pkg/apiserver/signozapiserver/` with a new `router.Handle` + OpenAPI
handler framework; paths below are the current ones, confirmed in `docs/api/openapi.yml`):*
- Open (no auth): `GET /api/v1/health`, `GET /api/v1/version` — preflight.
- **Auth flow (corrected):** `POST /api/v1/register` (first user, open) → login is now
  **`POST /api/v2/sessions/email_password`** (open; body `AuthtypesPostableEmailPasswordSession`
  → returns `AuthtypesGettableToken`). Use that token as the bearer on `ViewAccess`
  calls. *(Old `/api/v1/login` is gone.)* Cloud later: ingestion key (write) + API key (read).
- Authed (`ViewAccess`, bearer token): `GET /api/v1/services/list` / `POST /api/v2/services`
  (did my service appear?), `POST /api/v5/query_range` (count spans for `service.name=X`
  in a window — the real proof). v5 is registered in
  `pkg/apiserver/signozapiserver/querier.go` → `querierHandler.QueryRange`.
- **Request shape:** `pkg/types/querybuildertypes/querybuildertypesv5/req.go`
  (`QueryRangeRequest`). The top-level envelope is unchanged (`schemaVersion`,
  `start`/`end` epoch-ms, `requestType`, `compositeQuery`, `noCache`), but the nested
  `CompositeQuery`/builder-spec types changed substantially upstream — so the
  count-spans query body must be derived from the *current* types or captured from the
  SigNoz UI network tab in Phase 0, not copied from memory.

**Build the SigNoz client from the spec, not by hand.** Upstream ships a generated
132-path OpenAPI spec at `docs/api/openapi.yml`. Generate the Go client for the verify
path with `oapi-codegen` against it — this makes the integration robust and
self-updating against SigNoz versions instead of a hand-written client that silently
rots (exactly the staleness this re-verification exposed).

We query through the **same API the UI uses** (not ClickHouse directly), so "the agent
confirmed it" and "the user sees it in the dashboard" always agree.

**Where the code lives:** a **standalone Go module outside the monorepo** (clean to
demo), pointed at the local compose for dev/test. No edits to the SigNoz repo itself.

## Deliverable features (each independently demoable on local SigNoz)

- **F1 — Multi-provider agent core.** Provider registry
  `{name, baseURL, apiKeyEnv, defaultModel, supportsTools}`; `openai-go` wrapper;
  thin tool-use loop (send → tool_calls → execute → repeat → terminate). Capability
  gate refuses providers without tool-calling.
- **F2 — Stack detection + OTel config generation.** Pluggable `Detector` registry
  (ship one stack deeply; interface proves extensibility); templates → OTel snippet +
  env vars + collector endpoint.
- **F3 — Verification engine (`query_signoz`) — THE MOAT.** Go client for the read
  path above (health/version + register/login + services/list + v5 query_range),
  returning structured `{received, dropped, auth_fail, endpoint_unreachable}`.
- **F4 — `signoz init` REPL (Charm TUI).** Goroutine→channel→`tea.Cmd` bridge streams
  agent events into the UI; Huh prompts for confirmations; the "✓ first trace from
  service X" moment.
- **F5 — `signoz doctor`.** Same engine run diagnostically; failure→layer mapping
  (collector unreachable / no data / dropped / 401).
- **F6 — Guardrails.** Permission gate on mutating tools (`edit_file`, `run_command`),
  edit-staleness check, command timeouts, max-iterations/token budget, `--dry-run`.

## Build phases (each ends with a live test)

- **Phase 0 — Substrate + validate surface by hand (no Go).** `docker compose up -d`
  (against `upstream/main`'s compose — signoz `v0.128.0`, otelcol `v0.144.5`); register
  via `/api/v1/register` then get a token via `POST /api/v2/sessions/email_password`;
  emit data with `telemetrygen` (decouples verify from
  codegen); capture the canonical `query_range` payload **from the SigNoz UI devtools
  network tab** and reuse it as the request template. *Test:* `services/list` shows the
  service and `query_range` returns non-zero. → F3 spec locked.
- **Phase 1 — F1.** Registry + wrapper + smoke loop. *Test:* a forced tool call in the
  REPL works on `--provider openai` and `--provider deepseek`.
- **Phase 2 — F3.** Port Phase-0 requests into Go. *Test:* with `telemetrygen` running,
  `signoz verify --service demo` returns the live count.
- **Phase 3 — F2 + F4.** Real tool set, the async bridge, a sample app to instrument.
  *Test:* `signoz init` in the sample app → first trace end-to-end.
- **Phase 4 — F5 + F6.** Guardrails, then break it on purpose (wrong port / bad token)
  → `doctor` names the exact layer.

## Proposed repo layout (standalone module)

```
siginit/
  cmd/siginit/main.go         # cobra root: init | doctor | verify
  internal/provider/          # F1 registry
  internal/llm/               # F1 openai-go wrapper + loop
  internal/agent/             # F1 tool-use loop + event types
  internal/tools/             # inspect_project, read_file, edit_file, run_command, query_signoz
  internal/signoz/            # F3 verify client (health/login/services/query_range)
  internal/detect/            # F2 Detector registry
  internal/generate/          # F2 templates
  internal/tui/               # F4 Bubble Tea model + bridge
  fixtures/                   # sample apps for the demo
```

## Verification (end-to-end)

1. `cd deploy/docker && docker compose up -d`; wait for `curl localhost:8080/api/v1/health`.
2. Register/login → JWT; confirm `telemetrygen` data is queryable via `query_range`.
3. `siginit` unit tests for the loop (mock provider) and the verify client (mock
   server returning known counts).
4. Live e2e: `signoz init` in `fixtures/<app>` → agent instruments → runs → "✓ first
   trace from `<app>`"; confirm the same service is visible in the SigNoz UI.
5. `doctor`: misconfigure the exporter port → assert it reports "collector unreachable
   on :4318", not a generic error.

## Decisions (locked)

- **Harness stack: Go** + `openai-go` + Charm. Single static binary (best embodies
  "ease of getting started"), Charm TUI polish, matches SigNoz's Go backend, trivial
  DeepSeek/OpenAI base-URL swap. We own a ~150-line agent loop + guardrails.
  *(TS + OpenAI Agents SDK considered and rejected: needs a Node runtime to install,
  Ink instead of Charm, OpenAI-centric multi-provider. App language ≠ harness language —
  a Go harness still instruments the Node demo app.)*
- **Repo location: standalone module** outside the monorepo.
- **Primary demo app: Node** — safe, fast activation wow via zero-code
  auto-instrumentation; point it at a slightly messy repo (e.g. Express/TS with a custom
  start script) so the agent's detection/config/verify reasoning is visibly non-trivial.
  A Go sample app is a stretch second demo. `telemetrygen` is the test fixture throughout.
- **Default provider: DeepSeek** (cheap iteration, BYO-model story); OpenAI selectable
  via `--provider`.
- **Build baseline: `upstream/main` (SigNoz/signoz), not local `main`.** Local was 800
  commits / ~4 months behind; the integration surface above was re-verified against
  `upstream/main` + `docs/api/openapi.yml`. Pull latest before Phase 0.

![Agentic Control banner](assets/banner.svg)

Agentic Control is a small Go control layer for products that need to run
local agent CLIs without hard-coding each runtime into the product itself.

It gives upstream services two surfaces:

- `agent_control` for app-owned sessions across Codex, Gemini, Claude,
  OpenCode, and pi.
- `agent_harness` for passive hook, plugin, and extension telemetry from
  sessions launched outside your app.

It contains Court as an internal subsystem for multi-agent workflow
orchestration.

For new integrations, start with `agent_control`. It discovers local installs,
reports auth state, exposes available models where the runtime has a stable
inventory surface, starts and resumes sessions, sends input, interrupts work,
answers runtime requests, and streams normalised events.

The passive harness remains available for diagnostics and unmanaged sessions,
while `agent_control` is the primary integration surface.

---

![Overview](assets/header-overview.svg)

## What This Repo Provides

Agentic Control keeps runtime-specific behaviour behind a small contract that
host applications can import or run over a Unix socket.

It is the native home for workflow orchestration primitives, with Court as the
advanced review-oriented subsystem on top.

| Need | Use | Primary files |
| --- | --- | --- |
| Discover installed runtimes, auth, capabilities, and models | `agent_control describe` or `embedded.ControlPlane.Describe()` | [`cmd/agent-control/main.go`](cmd/agent-control/main.go), [`pkg/controlplane/embedded/embedded.go`](pkg/controlplane/embedded/embedded.go) |
| Start, resume, send to, interrupt, respond to, stop, and list sessions | `agent_control serve` or `embedded.ControlPlane` | [`internal/controlplane/`](internal/controlplane), [`pkg/controlplane/types.go`](pkg/controlplane/types.go) |
| Track downstream session history and token economics | control-plane session ledger | [`internal/controlplane/sessionledger.go`](internal/controlplane/sessionledger.go), [`docs/session-ledger.md`](docs/session-ledger.md) |
| Own backend/provider/model definitions and validation | shared runtime target helpers | [`pkg/controlplane/runtime_targets.go`](pkg/controlplane/runtime_targets.go), [`docs/model-registry.md`](docs/model-registry.md) |
| Route model-backed text generation work in upstream services | `pkg/controlplane.TextGenerationRouter` | [`pkg/controlplane/textgen.go`](pkg/controlplane/textgen.go) |
| Generate synthetic data and run Sigma-backed LLM-as-a-judge evaluations | `agent_control dataset` | [`cmd/agent-control/dataset_cmds.go`](cmd/agent-control/dataset_cmds.go), [`internal/orchestration/dataset_eval.go`](internal/orchestration/dataset_eval.go) |
| Run Court workflow orchestration in process | `internal/court` | [`internal/court/`](internal/court), [`docs/court-subsystem.md`](docs/court-subsystem.md) |
| Capture unmanaged runtime telemetry | `agent_harness install`, `agent_harness listen`, and runtime bundles | [`cmd/agent-harness/main.go`](cmd/agent-harness/main.go), [`internal/harness/harness.go`](internal/harness/harness.go), [`internal/harness/install.go`](internal/harness/install.go), [`internal/harness/run.go`](internal/harness/run.go) |
| Consume stable JSON contracts | Go contract types | [`pkg/contract/controlplane.go`](pkg/contract/controlplane.go), [`pkg/contract/harness.go`](pkg/contract/harness.go) |

The runtime adapters are deliberately boring:

- Codex uses `codex app-server` for controlled sessions and native hooks for
  passive telemetry.
- Gemini uses `gemini --acp` for controlled sessions and native hooks for
  passive telemetry.
- Claude uses the official Agent SDK through a local bridge for controlled
  sessions and native hooks for passive telemetry.
- OpenCode uses `opencode serve` for controlled sessions and native plugins for
  passive telemetry.
- pi uses `pi --mode rpc` for controlled sessions and native extensions for
  passive telemetry.

Read these next:

- [Control-plane guide](docs/control-plane.md) for `system.describe`, session
  RPC, local probes, auth state, and model inventory.
- [Session ledger](docs/session-ledger.md) for downstream session tracking and
  token economics.
- [Model registry](docs/model-registry.md) for unified backend/provider/model
  sourcing, validation, and operator surfaces.
- [Smoke testing](docs/smoke-testing.md) for repeatable live runtime/orchestration
  verification through the CLI.
- [Orchestration surface](docs/orchestration-surface.md) for the first native
  non-Court workflow entry point.
- [Court subsystem](docs/court-subsystem.md) for the in-process workflow engine
  layering inside Agentic Control.
- [Integration guide](docs/integration.md) for embedding Agentic Control into a
  host service.
- [Event contract](docs/contract.md) for the passive harness event shape.
- Runtime guides for [Codex](docs/codex.md), [Gemini](docs/gemini.md),
  [Claude](docs/claude.md), [OpenCode](docs/opencode.md), and [pi](docs/pi.md).

---

![Install and run](assets/header-install.svg)

## Quick Start

Build the two CLI binaries:

```bash
mise trust
mise install
mise run build
```

Start the control-plane:

```bash
agent_control serve --socket-path /tmp/agentic-control.sock
agent_control wait-ready --socket-path /tmp/agentic-control.sock
```

In another terminal, ask it what the local machine can run:

```bash
agent_control describe --socket-path /tmp/agentic-control.sock
```

That call is the first useful integration point. It returns:

- the control-plane schema and wire protocol versions
- supported RPC methods
- one descriptor per runtime
- local binary install status
- runtime version and resolved binary path
- auth state where a runtime exposes a usable status command
- model inventory and model option metadata where the runtime exposes it

For typed model selection and orchestration, the most useful next commands are:

```bash
agent_control models --socket-path /tmp/agentic-control.sock --runtime opencode --provider google
agent_control smoke --socket-path /tmp/agentic-control.sock
agent_control court run --socket-path /tmp/agentic-control.sock --task "review this repo" --provider opencode --model-selection google/gemini-3-flash-preview --workspace . --watch
```

Run the local operator UI after the frontend bundle exists:

```bash
agent_control web --workspace . --backend opencode --open
```

The same React UI is embedded by the Wails desktop entry point in
`cmd/agent-control-desktop`. The root package does not launch the desktop app.
Build it explicitly when needed:

```bash
mise run desktop:build
```

It uses the shared app host so browser and desktop flows expose the same CLI
runtime controls, Court catalog, Court runs, speech routing, and Agentic
Interaction JSON-RPC calls.

The UI uses convention-first routes. `/agents` is the default path, while
specific resources are directly addressable as `/agents/{session_id}`,
`/court/{workflow_id}`, and `/runs/{run_id}`. Operational surfaces are also
stable paths: `/voices`, `/attention`, `/rpc`, and `/logs`. Workspace and
backend remain optional query parameters, so the default local path stays small
and overrides are shareable only when needed.

The UI host also owns agent voice allocation. Agents can claim a voice, and
operators can pin `project + agent -> voice` rules; the host refuses a second
live agent that tries to use an already reserved voice. Voice playback still
flows through Agentic Interaction TTS. Visual attention requests use Agentic
Interaction notification audio, so agents can distinguish "read this aloud"
from "get the user's eyes on this."

The same control-plane can be imported directly from Go:

```go
package main

import (
	"fmt"

	"github.com/benjaminwestern/agentic-control/pkg/controlplane/embedded"
)

func main() {
	cp := embedded.New()
	describe := cp.Describe()

	for _, runtime := range describe.Runtimes {
		status := "unknown"
		if runtime.Probe != nil {
			status = runtime.Probe.Status
		}
		fmt.Printf("%s: %s\n", runtime.Runtime, status)
	}
}
```

The build bootstraps the Claude SDK bridge dependency the first time
`agent_control` is compiled. That bridge is only for Claude's official Agent
SDK approval and input callback path; there is no extra bridge for pi model
inventory.

---

![Runtime support](assets/header-runtimes.svg)

## Runtime Matrix

The release was checked on April 24, 2026 against these CLI/package
versions:

| Runtime | Validated version | Controlled session transport | Passive telemetry | Model inventory |
| --- | --- | --- | --- | --- |
| Codex | `codex-cli 0.124.0` | `codex app-server` | native hooks | built-in catalogue; app-server `model/list` pending |
| Gemini | `0.39.0` via package version check; local smoke `0.38.2` | `gemini --acp` | native hooks | built-in catalogue |
| Claude | `2.1.104 (Claude Code)` local CLI; Agent SDK `0.2.119` package | Claude Agent SDK bridge | native hooks | built-in catalogue |
| OpenCode | `1.14.22` via package version check; local CLI `1.14.20` | `opencode serve` | native plugin | dynamic `/provider` inventory when the server exposes it |
| pi | `0.70.0` via package version check; local CLI `0.68.0` | `pi --mode rpc` | native extension | dynamic RPC `get_available_models` pending |

Install references:

| Runtime | Install guide | Native reference |
| --- | --- | --- |
| Codex | [Codex CLI quickstart and install](https://github.com/openai/codex#quickstart) | [Codex hooks](https://developers.openai.com/codex/hooks), [Codex plugins](https://developers.openai.com/codex/plugins), [Codex plugin packaging](https://developers.openai.com/codex/plugins/build) |
| Gemini | [Gemini CLI installation](https://geminicli.com/docs/get-started/installation/) | [Gemini CLI hooks reference](https://geminicli.com/docs/hooks/reference/) |
| Claude | [Claude Code setup](https://docs.claude.com/en/docs/claude-code/setup) | [Claude Code hooks reference](https://code.claude.com/docs/en/hooks) |
| OpenCode | [OpenCode install guide](https://opencode.ai/docs/) | [OpenCode plugins](https://opencode.ai/docs/plugins/), [OpenCode config](https://opencode.ai/docs/config/), [OpenCode permissions](https://opencode.ai/docs/permissions/), [OpenCode server](https://opencode.ai/docs/server/) |
| pi | [pi package install](https://www.npmjs.com/package/@mariozechner/pi-coding-agent) | [pi RPC mode](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md), [pi extensions](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md) |

pi documents a machine-readable RPC model inventory path through
`get_available_models`. Agentic Control reports pi install and version
state with an empty built-in model list until that RPC probe is implemented
and smoke-tested against the `0.70.0` package.

---

![Event contract](assets/header-contract.svg)

## Control-Plane Contract

`system.describe` is the bootstrap call every upstream service should make
before rendering a backend picker or starting work.

Minimal request:

```json
{"id":"describe-1","method":"system.describe"}
```

Response shape:

```json
{
  "schema_version": "agentic-control.control-plane.v1",
  "wire_protocol_version": "agentic-control.rpc.v1",
  "methods": [
    "system.ping",
    "system.describe",
    "events.subscribe",
    "events.unsubscribe",
    "session.start",
    "session.resume",
    "session.send",
    "session.interrupt",
    "session.respond",
    "session.stop",
    "session.list"
  ],
  "runtimes": [
    {
      "schema_version": "agentic-control.control-plane.v1",
      "runtime": "codex",
      "ownership": "controlled",
      "transport": "app_server",
      "capabilities": {
        "start_session": true,
        "resume_session": true,
        "send_input": true,
        "interrupt": true,
        "respond": true,
        "stop_session": true,
        "list_sessions": true,
        "stream_events": true,
        "approval_requests": true,
        "user_input_requests": true,
        "immediate_provider_session": true,
        "resume_by_provider_id": true,
        "adopt_external_sessions": false
      },
      "probe": {
        "installed": true,
        "status": "ready",
        "version": "codex-cli 0.124.0",
        "binary_path": "/opt/homebrew/bin/codex",
        "auth": {
          "status": "authenticated",
          "method": "login status"
        },
        "model_source": "built_in",
        "models": [
          {
            "id": "gpt-5.4",
            "label": "GPT-5.4",
            "provider": "codex",
            "default": true,
            "capabilities": {
              "reasoning_effort_levels": [
                {"value": "xhigh", "label": "Extra High"},
                {"value": "high", "label": "High", "is_default": true},
                {"value": "medium", "label": "Medium"},
                {"value": "low", "label": "Low"}
              ],
              "supports_fast_mode": true
            }
          }
        ],
        "message": "Runtime binary found",
        "probed_at_ms": 1775200000000
      }
    }
  ]
}
```

Use the probe for UX, not as the source of provider capability truth:

- `capabilities` says what the Agentic Control provider implementation can do.
- `probe.installed`, `probe.status`, `probe.version`, and
  `probe.binary_path` describe the current machine.
- `probe.auth` describes whether the local runtime appears authenticated.
- `probe.models` and `probe.model_source` describe the models available for
  picker UI and upstream routing.

Session and event types live in [pkg/contract/controlplane.go](pkg/contract/controlplane.go).
Provider request types live in [pkg/controlplane/types.go](pkg/controlplane/types.go).

---

![Bindings](assets/header-bindings.svg)

## Unified Text Router

Upstream services often need model-backed text generation without caring which
runtime should handle the request. The public router in
[pkg/controlplane/textgen.go](pkg/controlplane/textgen.go) centralises that
selection logic.

Supported generation surfaces:

- commit messages
- pull-request titles and bodies
- branch names
- thread titles

The router resolves providers in this order:

1. explicit provider
2. provider inferred from the model ID
3. fallback providers
4. router default provider

Built-in inference rules:

| Model shape | Provider |
| --- | --- |
| `claude-*` | Claude |
| `gemini-*` or `auto-gemini-*` | Gemini |
| `gpt-*`, `o1*`, `o3*`, or `o4*` | Codex |
| provider-scoped IDs like `anthropic/claude-sonnet-4-6` | OpenCode |

Example:

```go
router := controlplane.NewTextGenerationRouter("codex", map[string]controlplane.TextGenerationProvider{
	"codex":    codexProvider,
	"claude":   claudeProvider,
	"gemini":   geminiProvider,
	"opencode": openCodeProvider,
})

result, err := router.GenerateCommitMessageForSelection(ctx, controlplane.CommitMessageInput{
	ModelSelection: controlplane.TextGenerationModelSelection{
		Model: "claude-sonnet-4-6",
		Options: controlplane.ModelOptions{
			ReasoningEffort: "high",
		},
		Fallbacks: []string{"codex"},
	},
	Diff: diff,
})
```

That keeps product code focused on its own workflow model while preserving the
selected runtime, model, reasoning effort, thinking level, and thinking budget.

---

![How it works](assets/header-architecture.svg)

## Architecture

![Architecture overview](assets/architecture.svg)

The controlled-session path is:

1. The host app imports `pkg/controlplane/embedded` or starts
   `agent_control serve`.
2. The app calls `system.describe` and renders only the runtimes that are
   installed and usable.
3. The app starts or resumes a runtime session.
4. Agentic Control normalises runtime events, session state, approvals, and
   user-input requests.
5. The app subscribes to events and responds through one control-plane API.

The passive telemetry path is:

1. The host installs a runtime bundle with `agent_harness install`.
2. The runtime invokes `agent_harness` through its native hook, plugin, or
   extension surface.
3. The helper translates native payloads into the harness event contract.
4. The host consumes events from stdout or a local Unix socket listener.

Generated diagrams come from [assets/architecture.d2](assets/architecture.d2).

---

![Debug mode](assets/header-debug.svg)

## Passive Harness

Use `agent_harness` when your app does not own the runtime process or when you
need to inspect native runtime events during integration work.

Start a listener:

```bash
.artifacts/bin/agent_harness listen --socket-path /tmp/agent-harness.sock
```

Install repo-local bundles for hook-based runtimes:

```bash
.artifacts/bin/agent_harness install --runtime codex --scope repo --socket-env AGENT_HARNESS_SOCKET
.artifacts/bin/agent_harness install --runtime gemini --scope repo --socket-env AGENT_HARNESS_SOCKET
.artifacts/bin/agent_harness install --runtime claude --scope repo --socket-env AGENT_HARNESS_SOCKET
.artifacts/bin/agent_harness install --runtime pi --scope repo --socket-env AGENT_HARNESS_SOCKET
```

Install OpenCode globally when you want its normal plugin auto-load path:

```bash
.artifacts/bin/agent_harness install --runtime opencode --scope global --socket-env AGENT_HARNESS_SOCKET
```

Remove only Agentic Control-managed config later:

```bash
.artifacts/bin/agent_harness uninstall --runtime codex --scope repo
.artifacts/bin/agent_harness uninstall --runtime gemini --scope repo
.artifacts/bin/agent_harness uninstall --runtime claude --scope repo
.artifacts/bin/agent_harness uninstall --runtime opencode --scope global
.artifacts/bin/agent_harness uninstall --runtime pi --scope repo
```

Fixture replay and live smoke tasks:

```bash
mise run diag:fixtures:all
mise run diag:codex:smoke
mise run diag:gemini:bash
mise run diag:claude:approval
mise run diag:opencode:approval
mise run diag:pi:smoke
```

The normalised passive event contract is documented in
[docs/contract.md](docs/contract.md).

---

![Scripts and assets](assets/header-scripts.svg)

## Maintenance

The local automation entrypoints live in [mise.toml](mise.toml), and hook
checks live in [hk.pkl](hk.pkl).

Useful tasks:

| Command | Purpose |
| --- | --- |
| `mise run build` | Build the web UI bundle, `agent_harness`, and `agent_control`. |
| `mise run desktop:build` | Build the web UI bundle and `agent-control-desktop`. |
| `mise run control:serve` | Start the control-plane on `/tmp/agentic-control.sock` unless `SOCKET_PATH` is set. |
| `mise run validate-docs` | Validate README links and required generated assets. |
| `mise run generate-assets` | Regenerate README SVG assets and the architecture diagram. |
| `mise run hk:check` | Run the configured local checks. |

Documentation assets are generated by `scripts/generate_assets.py` and checked
by `scripts/validate_readme.py`. The generated files are:

- [assets/banner.svg](assets/banner.svg)
- [assets/header-overview.svg](assets/header-overview.svg)
- [assets/header-install.svg](assets/header-install.svg)
- [assets/header-runtimes.svg](assets/header-runtimes.svg)
- [assets/header-contract.svg](assets/header-contract.svg)
- [assets/header-bindings.svg](assets/header-bindings.svg)
- [assets/header-architecture.svg](assets/header-architecture.svg)
- [assets/header-debug.svg](assets/header-debug.svg)
- [assets/header-scripts.svg](assets/header-scripts.svg)
- [assets/header-repository.svg](assets/header-repository.svg)
- [assets/architecture.svg](assets/architecture.svg)

Regenerate and validate after README structure changes:

```bash
mise run generate-assets
mise run validate-docs
```

---

![Repository layout](assets/header-repository.svg)

## Repository Layout

| Path | Purpose |
| --- | --- |
| [cmd/](cmd) | Binary entrypoints for `agent_control` and `agent_harness`. |
| [internal/controlplane/](internal/controlplane) | Session registry, event bus, RPC server, probes, model catalogue, and provider implementations. |
| [internal/harness/](internal/harness) | Passive hook, plugin, and extension translation plus bundle install and live-run diagnostics. |
| [pkg/contract/](pkg/contract) | Public JSON contract types for controlled sessions and passive harness events. |
| [pkg/controlplane/](pkg/controlplane) | Public Go request types, session helpers, metadata helpers, and text-generation router. |
| [runtimes/](runtimes) | Runtime-specific fixtures, prompts, plugin source, extension notes, and passive harness README files. |
| [docs/](docs) | Durable integration, contract, control-plane, and runtime documentation. |
| [scripts/](scripts) | README asset generation and README validation. |
| [assets/](assets) | Generated README images and architecture source. |
| [go.mod](go.mod) | Go module definition. |

For a host integration, wire `system.describe` into your backend picker first,
then decide whether you need controlled sessions, passive telemetry, or both.

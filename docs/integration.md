# Integration guide

Agentic Control is built for host applications that want native runtime signals
and session control without turning this repository into a policy engine.

Choose your path first:

- Use `agent_control` when your application owns session lifecycle and needs to
  start, resume, interrupt, and answer runtime requests directly.
- Use `agent_harness` when your application only needs passive hook, plugin,
  or extension observation from sessions launched elsewhere.

The installation surface is intentionally small. Use the shared Go installer,
`agent_harness install`, with `--runtime` and, when needed, `--scope`, rather
than runtime-specific installer scripts.

## Recommended model

Use the repository as a translation layer only. Keep product state, policy,
and ownership in your application.

1. Your app launches the runtime.
2. Your app injects any app-owned identifiers it wants the helper to bind.
3. The runtime invokes `agent_harness` through hooks or plugins.
4. The helper emits normalised events to your local receiver.
5. Your app derives high-level state from that stream.

This keeps the helper portable and keeps product logic in your app.

## First success

Pick one of these starting points:

- If your app owns sessions, start with `docs/control-plane.md`, run
  `agent_control serve`, then `agent_control wait-ready`, then call
  `agent_control describe` before you do
  anything else.
- If your app only needs passive runtime events, install the runtime bundle,
  start `agent_harness listen`, and launch the runtime with your chosen socket
  path or socket environment variable.

## Pass correlation explicitly

Do not bake product semantics into the helper. Instead, pass correlation fields
through bindings.

For example:

```bash
--bind-env launch_id=APP_LAUNCH_ID \
--bind-env app_session_id=APP_SESSION_ID \
--bind-env actor_id=APP_ACTOR_ID \
--bind-env host_id=APP_HOST_ID
```

If your app does not need one of those fields, omit it.

The important design constraint is that the helper does not care what those
bindings mean. Your app may use them to correlate process state, UI sessions,
runtime resumes, worker identities, or any other ownership model. The helper
only preserves the values you asked it to attach.

## Choose a transport

The helper supports a local Unix domain socket listener out of the box. That is
the preferred transport for macOS and Linux desktop apps because it avoids port
management and makes local-only routing straightforward.

For manual debugging, you can also emit normalised events directly to stdout.

If your app has its own receiver lifecycle, prefer `--socket-path` and
own the path directly. If your app wants to inject the path dynamically per
launch, use `--socket-env` and set the environment variable at runtime.

Some runtimes also support launch-time settings injection. Claude is the
cleanest hook-based example in this repository: the bundle writes a sidecar
settings file and the runtime is launched with `--settings <path>`, so you can
keep hook configuration out of any existing user-managed settings file.

OpenCode is the cleanest plugin-based example. The bundle writes into the
runtime’s dedicated plugin directory, so the runtime auto-loads the adapter
without any `opencode.json` edit. When you need tighter control for testing,
you can use a repo-local `.opencode/plugins/` directory or override
runtime behaviour with `OPENCODE_CONFIG_CONTENT`. If both global and repo-local
OpenCode bundles are present, the repo-local bundle is the active bundle for
that repository.

The Go installer keeps each bundle inside the runtime’s own repo-local or
global config tree. That makes `agent_harness uninstall` safe because it can
remove only the Agentic Control content for that runtime without guessing about
shared global state.

## Provider offload APIs

Host applications that only need one-shot text generation or embeddings can use
`pkg/controlplane/builtin.NewOneShot()` or
`pkg/providers/openaicompatible.NewService()` without owning provider HTTP
details.

Use `openaicompatible.ResolveEndpointConfig`,
`ResolveTextGenerationSelection`, or `ResolveEmbeddingSelection` when building
OpenAI-compatible or Ollama selections from user configuration. The resolver
normalises `ollama`, `openai`, and `openai-compatible`, applies provider
defaults, honours `OLLAMA_HOST`, `OPENAI_COMPATIBLE_BASE_URL`, and
`OPENAI_API_KEY`, and returns `/v1`-normalised endpoint options.

`controlplane.Message.Parts` accepts first-class `contract.ContentPart` values,
so callers can send text and image inputs through the public control-plane API
without importing OpenAI-compatible HTTP content structs.

One-shot text and embedding calls return `ProviderResultMetadata` with provider,
model, base URL, request ID, request count, status code, nanosecond latency,
finish reason, output kind, usage, and provider error details when available.
Provider failures that still have useful metadata, such as an empty final
content response from a local model, are returned as
`*controlplane.ProviderResultError` so callers can inspect the same structured
provider result that would have appeared beside a successful output.

## Separate debug from product

Use two lanes:

- Product lane: a local receiver that updates your app state.
- Debug lane: the `listen` command plus raw stdout printing when you need to
  inspect exact hook, plugin, or extension payload translation.

The helper supports both without changing the runtime hook shape.

## Recommended host responsibilities

Keep these responsibilities in your application rather than in the helper:

- process ownership
- runtime IO ownership
- session persistence
- policy and approval state
- UI state derivation
- resume heuristics

That split keeps Agentic Control portable across products while letting
you build a runtime-aware host application around it.

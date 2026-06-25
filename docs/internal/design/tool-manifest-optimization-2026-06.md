# Tool Manifest Optimization (v0.1.0) — Design Spec

**Status:** Design → implementation. **Phase:** v0.1.0 tool optimization (NOT v0.3).
**Goal:** cut per-turn token cost by not sending every tool's full JSON schema each turn,
while keeping every tool reachable. Realizes the "Manifest" dimension of
`.preview-doc/tools-catalog.html` (target: ~13 full · ~65 compressed; ~7–11k tokens/turn saved).

## The three tiers (what "manifest" means)

1. **Full** — high-frequency tools. Sent to the LLM as normal callable tool defs every turn
   (name + description + full parameter schema). The agent calls them directly.
2. **Compressed (lazy)** — every other allowed tool. NOT sent as a callable def. Instead it
   appears in a compact **manifest** block in the system context as `name — one-line purpose`.
   To use one, the agent calls `load_tool` to make it callable (its full schema is fetched on
   demand). Once loaded it stays callable for the rest of the session.
3. **Search-discoverable** — for large/rare surfaces (esp. MCP). `search_tools_*` lets the agent
   find a tool by keyword/regex over the live surface, then `load_tool` it. (Gap 2: make search
   work over the whole surface, not only MCP.)

## Mechanism (keystone: `pkg/agent/loop.go` ~4871)

Today: `providerToolDefs := tools.ToolsToProviderDefs(policyFilteredTools)` — ALL tools, full schema.

New (when `cfg.Tools.Manifest.Compressed` is true):
1. Split `policyFilteredTools` into **full** and **lazy** by `tools.ManifestTier(name)`.
2. Compute the session's **loaded** lazy tools (per-session set, see State).
3. `providerToolDefs = ToolsToProviderDefs(full ∪ loaded ∪ {load_tool} ∪ {search_tools_* if registered})`.
4. Build a **compressed manifest string** from the lazy-but-not-loaded tools (grouped by category,
   `name — first line of Description()`), and inject it into the system context as an ephemeral
   block (rebuilt each turn, like the scratchpad re-injection) so it's never stale and never
   double-counted into the cached system prompt.
5. When `Compressed` is false → behave EXACTLY as today (all tools full). This is the kill-switch
   and the backward-compat contract.

## `load_tool` (new tool, pkg/tools)

- Name: `load_tool`. Category: `CategoryToolDiscovery`. Scope: `ScopeGeneral`.
- Params: `{ "names": ["string", ...] }` (1+ tool names).
- Execute: for each name — verify it is (a) a real tool registered for THIS agent, and (b) allowed
  by the agent's effective policy (not deny). Reject unknown/denied names with a clear error.
  Mark the valid names **loaded** for the current session, and return each loaded tool's full
  schema in the result so the model immediately sees the parameters. The tool becomes directly
  callable on the NEXT request (the loop includes its def). Idempotent: re-loading is a no-op.
- Always-on when compressed mode is enabled (registered per agent like spawn).

## State — loaded tools per session

- An in-memory, per-session set of loaded lazy-tool names (ephemeral; resets on a new chat/session).
- Stored on the AgentLoop keyed by session/transcript id (mirror how other per-session state is
  tracked), read at turn-build time. Concurrency-safe (mutex).
- The full tier is never "loaded"/"unloaded" — it is always callable. Only lazy tools toggle.

## Classification — `tools.ManifestTier(name) (full|lazy)`

`full` set (always callable; the high-frequency core — the agent only gets the ones it's allowed):
`read_file, write_file, edit_file, list_directory, exec, search_web, fetch_url, send_message,
hand_off, return_to_default, remember, recall_memory, set_todos`.
Everything else (browser_*, email, all management/agents/workspaces/channels/providers/mcp/platform,
skills, task CRUD, the `*_in_workspace` variants, spawn/run_subagent/check_spawn_status, serve_web,
workspace_shell*) is `lazy`. `load_tool` and `search_tools_*` are infrastructure — never in the
manifest, always callable when registered.

## Config — `pkg/config`

`cfg.Tools.Manifest.Compressed bool` (default ON — this is the delivered optimization; off = legacy
all-full behavior, the backward-compat path). One flag; no per-tool config.

## Gap 2 — tool search over the whole surface

`search_tools_bm25` / `search_tools_regex` already index the agent's full registry. Make them
register whenever compressed mode is on (not only when the MCP cache is enabled), so an agent can
search its lazy + MCP surface, then `load_tool` the hit.

## Gap 3 — `/agents/{id}/tools` endpoint

The per-agent tool view must enumerate the agent's COMPLETE allowed surface (full + lazy + the
dynamically-wired spawn/search/load tools), each tagged with its manifest tier (`full`/`compressed`).
Today it under-reports the dynamic tools. Add a `manifest_tier` field (contract change) and include
the dynamic tools.

## Gap 4 — docs

Update `.preview-doc/tools-catalog.html` + any reference: admin-ask fence is REMOVED (access =
policy + re-auth + delete-confirm); relabel the manifest model as the v0.1.0 optimization (not v0.3).

## Test plan (the mechanisms, very well tested)

- **Classification:** every full-set name → full; a sample of lazy names → lazy; load_tool/search
  → infra (not in manifest).
- **Manifest builder:** lazy tools grouped by category, `name — purpose`; full tools excluded;
  loaded tools excluded; deterministic ordering.
- **Loop split:** with Compressed on, providerToolDefs contains the full tools + load_tool, NOT the
  lazy ones; the system context contains the manifest block. With Compressed off, providerToolDefs
  == today's full set and no manifest block (golden/byte-for-byte backward-compat).
- **load_tool:** loads a valid lazy tool → it appears in the next turn's defs; unknown name →
  error; policy-denied name → error (cannot bypass policy via load); idempotent; multi-load.
- **Reachability invariant (critical):** for every agent, every tool that is policy-allowed is
  reachable — either full (in defs) or loadable (in the manifest + load_tool succeeds). No allowed
  tool is silently unreachable. Property-style test over all core agents.
- **Token win:** assert the compressed providerToolDefs JSON is materially smaller than the full set
  for a representative agent (sanity bound, not a brittle exact count).
- **Session state:** loaded set persists across turns in a session, resets across sessions,
  concurrency-safe.
- **Panel endpoint:** returns full + lazy + dynamic tools with correct manifest_tier.
- **Search:** registered under compressed mode; finds a lazy tool by keyword.
- **Exec-authorization (live-found, critical):** infra tools (load_tool/search_*) must be *executable*, not merely *visible*. Putting a tool in the provider defs (force-include) controls what the LLM sees; authorization is a SEPARATE gate (`resolveToolPolicyAtExec` → `filterTimePolicyMap` + `resolveSingleToolPolicy`). A deny-by-default agent never allow-lists load_tool, so it was shown the tool, called it, and the exec gate DENIED it → lazy tools unreachable. Both gates must force-allow registered `ManifestInfra` tools when compressed is on. Tests: `TestInfraToolsExecutable_DenyDefaultAgent` (ava/mia).

## Validation outcome (2026-06-25)

Built ON-by-default and validated live against the standard model **z-ai/glm-5.2** (Mia, deny-by-default): asked to create a task (lazy `create_task`). Audit recorded `load_tool → allow` then `create_task → allow`, and a real task ("manifest-check") was created — the full lazy-tool **load → call** round-trip works end-to-end. This answers the default-ON safety question with evidence: the standard model reliably emits `load_tool` then uses the loaded tool. The live run is what surfaced the exec-authorization bug above (unit tests + 4 reviewers all passed defs-visibility but missed exec-auth). Kill-switch (`compressed: false`) remains for fallback.

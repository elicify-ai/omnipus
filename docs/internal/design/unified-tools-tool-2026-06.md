# Unified `tools` discovery tool (v0.1.0) — design note

**Decision (operator, 2026-06-25):** collapse the three always-on infra tools —
`load_tool`, `search_tools_bm25`, `search_tools_regex` — into ONE multi-action
tool `tools`. Cuts per-turn infra defs 3→1 (serves the manifest token-saving
goal) and removes the confusing promote-vs-load duality.

## Shape

```
tools{ action: "search", query: string, mode?: "bm25"|"regex" }  → ranked/matched names (READ-ONLY)
tools{ action: "load",   names: [string] }                        → schemas + makes callable
```
- `action` required (enum search|load). `mode` defaults to `bm25`.
- Name: `tools`. Tier: `ManifestInfra` (always callable when registered, never in
  the manifest block). Category: `CategoryToolDiscovery`. Scope: `ScopeGeneral`.
- Description must be crisp so the model isn't confused by the generic name, e.g.:
  "Discover and load tools. Use action='search' to find tools by keyword/pattern;
  action='load' to fetch a tool's parameters and make it callable."

## Semantics — the make-callable reconciliation (the subtle part)

There are two underlying mechanisms for making a non-full tool callable, and the
unified `load` action must cover BOTH:

1. **In-process lazy tools** (e.g. create_workspace) — present in the agent's
   registry `GetAll()` but lazy-tier. Made callable via the per-session loaded
   set (`markToolsLoaded` → `buildCompressedToolDefs` includes them). Persists for
   the session.
2. **Hidden MCP tools** (`mcp_<server>_<tool>`, `RegisterHidden`) — NOT in
   `GetAll()` until promoted. Made callable via `PromoteTools(names, ttl)` which
   un-hides them in the registry (TTL-bounded).

**`load` action behavior** (per name): look the tool up including hidden; if found:
  - `PromoteTools([name], ttl)` — un-hides hidden/MCP tools so they enter `GetAll()`.
  - `markToolsLoaded(session, [name])` — records it in the session loaded set so
    `buildCompressedToolDefs` keeps sending its def for the rest of the session.
  - return the tool's schema (`ToolToSchema(t)["function"]`).
  Apply BOTH unconditionally — promote is a no-op for already-visible tools, and
  markLoaded is harmless for promoted ones; together they make in-process lazy AND
  hidden MCP tools reliably callable. Reject (in `rejected`) names that don't
  resolve or aren't policy-allowed (canLoad gate unchanged — no policy escalation).

**`search` action behavior:** run BM25 (default) or regex over the registry's
searchable (hidden + lazy) corpus and return matches (name + one-line purpose).
**READ-ONLY — does NOT promote.** The model then calls `load` for what it wants.
(This removes the old auto-promote-on-search; `load` is the single make-callable
path.) Keep the BM25 cache + regex engines (the data-race-fixed `getOrBuildEngine`)
on the registry; only the standalone tool wrappers are removed.

## Wiring

- `pkg/tools`: implement `tools` (multi-action), keep `ToolRegistry.SearchBM25/
  SearchRegex/PromoteTools` engine methods, remove the standalone `BM25SearchTool`/
  `RegexSearchTool`/`LoadTool` tool wrappers (or repoint them). Update
  `infraManifestToolNames` to `{ "tools" }` (single source — `InfraManifestTool-
  Names()` then auto-updates the loop force-include + gateway panel). Update the
  `BuildCompressedManifest` header text: "call tools with action 'load' and the
  name(s) to fetch parameters, then call them." Update `general_builtin_catalog.go`.
- `pkg/agent/loop.go` (`registerSharedTools`) + `loop_mcp.go`: register the single
  `tools` tool when `cfg.Tools.Manifest.Compressed` **OR** MCP discovery cache is
  enabled (union of the two old conditions). Wire its search (registry) + load
  (canLoad/markLoaded ctx-aware resolver + promote) deps. The infra force-include
  in `ensureInfraToolsExecutable`/`buildCompressedToolDefs` already reads
  `InfraManifestToolNames()`, so it follows automatically.
- `pkg/gateway/rest_tool_registry.go`: `manifest_tier` for `tools` = infra
  (via `ToolManifestTier`). No contract change (tool names are free-form).
- SPA: `src/lib/humanizeToolName.ts` (replace the two search entries + load_tool
  with `tools`), `src/test/canonicalToolNames.test.ts` (update canonical names).

## Tests to update (coupled)

`pkg/tools/{search_tools_test,search_tools_multi_mcp_test,load_tool_test,manifest_
test}.go`, `pkg/agent/tool_manifest_test.go` (TestSearchToolsRegistered →
assert `tools` present per agent; reachability/exec-auth use `tools`), `pkg/
gateway/rest_tool_registry_test.go`. Keep the multi-MCP search assertions (now via
`tools{action:search}`) and the `-race` concurrency test on the BM25 engine.

## Backward-compat

Tool names are free-form (no wire contract). Operators' `tool_policies` keyed on
the old names become inert (infra is force-allowed regardless). No migration
needed; note it in the changelog. The kill-switch (`compressed=false`) path is
unchanged except it now registers `tools` only when MCP cache is on.

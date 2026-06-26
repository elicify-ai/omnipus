# Unified `tools` discovery tool (v0.1.0) — design note

**Decision (operator, 2026-06-25):** collapse the three always-on infra tools —
`load_tool`, `search_tools_bm25`, `search_tools_regex` — into ONE multi-action
tool `tools`. Cuts per-turn infra defs 3→1 (serves the manifest token-saving
goal) and removes the confusing promote-vs-load duality.

**Update (operator, 2026-06-26):** Further simplified: dropped the explicit
`action`/`mode` parameters entirely. Intent is now inferred from which parameter
is present. Regex search is removed permanently.

## Shape

```
tools{ names: [string] }   → LOAD those tools: fetch schemas + make callable
tools{ query: string }     → SEARCH (BM25 only): find tools, AUTO-LOAD the top hit,
                              return schemas + full match list
```

- No `action` field. No `mode` field. Precedence: if `names` is present and
  non-empty → load path (ignore query). Else if `query` non-empty → search path.
  Else → error with guidance.
- Name: `tools`. Tier: `ManifestInfra` (always callable when registered, never in
  the manifest block). Category: `CategoryToolDiscovery`. Scope: `ScopeGeneral`.
- Description: "Find and load tools so you can call them. To load tools you know
  by name, pass 'names'. To find a tool by what it does, pass 'query' — the best
  match is loaded automatically. After loading, call the tool directly."

## Semantics — the make-callable reconciliation (the subtle part)

There are two underlying mechanisms for making a non-full tool callable, and the
unified `load` path must cover BOTH:

1. **In-process lazy tools** (e.g. create_workspace) — present in the agent's
   registry `GetAll()` but lazy-tier. Made callable via the per-session loaded
   set (`markToolsLoaded` → `buildCompressedToolDefs` includes them). Persists for
   the session.
2. **Hidden MCP tools** (`mcp_<server>_<tool>`, `RegisterHidden`) — NOT in
   `GetAll()` until promoted. Made callable via `PromoteTools(names, ttl)` which
   un-hides them in the registry (TTL-bounded).

**Load path behavior** (per name in `names`): look the tool up including hidden;
if found:
  - `PromoteTools([name], ttl)` — un-hides hidden/MCP tools so they enter `GetAll()`.
  - `markToolsLoaded(session, [name])` — records it in the session loaded set so
    `buildCompressedToolDefs` keeps sending its def for the rest of the session.
  - return the tool's schema.
  Apply BOTH unconditionally — promote is a no-op for already-visible tools, and
  markLoaded is harmless for promoted ones; together they make in-process lazy AND
  hidden MCP tools reliably callable. Reject (in `rejected`) names that don't
  resolve or aren't policy-allowed (canLoad gate unchanged — no policy escalation).

**Query/search path behavior:** run BM25 over the registry's hidden corpus and:
1. Return the full match list (name + description) for all ranked results.
2. **Auto-load the top hit** via the same load mechanism (canLoad gate →
   PromoteTools + markToolsLoaded → schema). If the top hit fails canLoad (e.g.
   policy-denied), skip it and try the next match until one loads or the list is
   exhausted.
3. Return a result the model can act on immediately:
   `{"loaded": ["create_task"], "schemas": {"create_task": <schema>}, "matches": [...]}`
   with a message: "Found N tools. Loaded the best match 'create_task' (schema
   included) — call it now, or load a different one by passing its name in 'names'."
4. When no resolver is wired (e.g. read-only callers), the query runs BM25 but
   skips auto-load; returns matches with instructions to use `names` to load.

**Regex removed:** `SearchRegex` and `MaxRegexPatternLength` are deleted from
`pkg/tools/search_tool.go`. No mode parameter exists. All search is BM25-only.

## Wiring

- `pkg/tools`: `ToolsTool` implements the param-inferred interface. `SearchBM25`
  and BM25 cache (`getOrBuildEngine`) are kept. `SearchRegex` is removed.
  `BuildCompressedManifest` header updated: "To use one, call `tools` with its
  exact name in `names` (or describe what you need in `query`) to load it, then
  call it."
- `pkg/agent/loop.go` (`registerSharedTools`) + `loop_mcp.go`: register the single
  `tools` tool when `cfg.Tools.Manifest.Compressed` **OR** MCP discovery cache is
  enabled. Wire its search (registry) + load (canLoad/markLoaded ctx-aware resolver
  + promote) deps. The infra force-include in `ensureInfraToolsExecutable`/
  `buildCompressedToolDefs` already reads `InfraManifestToolNames()`.
- `pkg/gateway/rest_tool_registry.go`: `manifest_tier` for `tools` = infra
  (via `ToolManifestTier`). No contract change (tool names are free-form).
- SPA: `src/lib/humanizeToolName.ts` (replace the two search entries + load_tool
  with `tools`), `src/test/canonicalToolNames.test.ts` (update canonical names).

## Tests to update (coupled)

`pkg/tools/{search_tools_test,search_tools_multi_mcp_test,load_tool_test,manifest_
test}.go`: rewritten to the new param-inferred shape. Regex tests deleted. BM25
concurrency tests kept (now `execBM25Query` calls `tools{query:...}`).
`pkg/agent/tool_manifest_test.go`: all `action='load'` Execute calls updated to
`names:[...]`. Search-then-load chain test comments updated.

## Backward-compat

Tool names are free-form (no wire contract). Operators' `tool_policies` keyed on
the old names (`load_tool`, `search_tools_bm25`, `search_tools_regex`) become inert
(infra is force-allowed regardless). No migration needed; note it in the changelog.
The kill-switch (`compressed=false`) path is unchanged except it now registers
`tools` only when MCP cache is on.

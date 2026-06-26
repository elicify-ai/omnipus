# Remove the agent enable/disable feature — impact assessment + plan

**Decision (operator, 2026-06-26):** remove the agent activate/deactivate (enable/disable) feature entirely — no use case. Remove cleanly, break nothing else.

## Headline findings

- **Backend-only feature.** There is **no wire-contract field** for it — the `Agent` schema (and `AgentUpdateRequest`) have NO `enabled` property (the `a.Enabled` in generated Go is an *MCP* type, not `Agent`). **No contract change, no `gen-contracts` regen, no generated-type edits.**
- **No UI control exists.** The SPA has zero toggle/switch/badge for agent enable/disable. The only SPA touch is two cosmetic entries in `humanizeToolName.ts` (chat-transcript labels) that fall back gracefully.
- It's a **system-agent tool that writes a config flag**, plus 5 runtime guards that read it. Self-contained.

## Scope boundary (do NOT touch)

Unrelated `Enabled`/`IsActive` that must stay: channel `Enabled`, cron `job.Enabled`, heartbeat `Enabled`/`HeartbeatIsEnabled()`, `SSRFPolicy.IsEnabled()`, god-mode `status.Enabled`, MCP-server `Enabled` (incl. the generated `a.Enabled` at openapi_types.gen.go:8773/8829), web-search provider `Enabled`. ONLY `AgentConfig.Enabled`/`IsActive()` and the two `*_agent` tools are in scope.

## What gets removed/changed

### A. Feature core — config (`pkg/config/config.go`)
- Delete `AgentConfig.Enabled *bool` field (~line 791) + its comment.
- Delete `AgentConfig.IsActive()` method (~lines 826–832). (Keep `HeartbeatIsEnabled()` — unrelated.)

### B. The 5 runtime guards (drop `IsActive()`; all agents become "active")
1. `pkg/routing/route.go:307` — `if a.Default && a.IsActive() && a.IsChatTarget()` → drop `.IsActive()`.
2. `pkg/routing/route.go:319` — `if a.IsActive() && a.IsChatTarget()` → drop `.IsActive()`. Update nearby "disabled agent" comments + the fallback log (`:325` "first enabled agent" → "first available agent").
3. `pkg/gateway/rest.go:786` — `firstEnabledAgentID`: drop `.IsActive()`; rename → `firstChatTargetAgentID` (optional) + fix comment.
4. `pkg/gateway/gateway.go:2285-2296` — cron `agentCheckerFunc`: simplify to `_, ok := registry.GetAgent(id); return ok` (drops `findAgentConfig` + `ac.IsActive()`; the `ac==nil` back-compat is preserved because registered==available). Update comments (`:774` "37 management tools", `:2281-2284`). Also `pkg/gateway/schedules.go:47-50` comment.
5. `pkg/agent/loop.go:3910-3914` — delete the FR-001 "disabled-owner guard" block + comment (`:3902-3905`); update `:3577` comment.

### C. The tools (`pkg/sysagent/tools/`)
- `agent.go` — delete `setAgentEnabled` (~69–103), `AgentActivateTool` (~642–667), `AgentDeactivateTool` (~669–694).
- `registry.go:22-23` — remove the two `New*` registrations; update the "7 → 5" agent-tool comment.
- `category.go:19-20` — remove the two `Category()` methods.

### D. Sysagent ancillary maps (remove the two `activate_agent`/`deactivate_agent` keys each)
- `rbac.go:50-51`, `confirmation.go:24-25`, `ratelimit.go:49-50`.
- `prompt.go` — drop the two names from the agent-tools line (`:116`) + update the "37 tools" count (`:112`).

### E. The tool-count cascade (37 → 35) — the main care-point
Every hardcoded "37" (and stale "41") must move to 35:
- `pkg/sysagent/tools/registry.go` (comment), `contract_test.go`, `deps_atomic_test.go`
- `pkg/sysagent/coverage_test.go`, `sysagent_test.go` (counts + remove the 2 names from `expectedTools`)
- `pkg/sysagent/handler.go`, `agent.go`, `tools/deps.go` (stale "41" comments → 35)
- `pkg/agent/loop.go:3577`, `pkg/gateway/gateway.go:774`, `pkg/gateway/builtin_registry_metadata_test.go`
- `pkg/tools/testdata/provider_defs.golden.json` — **regenerate**: `go test -tags goolm,stdjson ./pkg/tools/ -run TestProviderDefs_ShapeUnchanged -update`

### F. SPA
- `src/lib/humanizeToolName.ts:85-86` — remove the two entries (cosmetic; transcript labels fall back to the default formatter).

### G. Tests to delete (test the removed feature)
- `pkg/config/config_test.go` — `TestAgentConfig_IsActive_NilDefaultsToTrue`.
- `pkg/sysagent/tools/agent_test.go` — `TestAgentActivate_PersistsEnabled`, `TestAgentDeactivate_PersistsEnabled`, `TestAgentActivate_RoundTripDisk`, `TestAgentDeactivate_RefusesLockedAgent`, `TestAgentActivate_RollbackOnSaveFailure`.
- **Fixup:** `TestAgentDelete_RefusesLockedAgent` uses `Enabled: &enabled` in its fixture — remove that line (keep the test; `Locked:true` is enough). **Forgetting this = build failure.**

## Backward-compat
An existing `config.json` with `"enabled": false` on an agent: after the field is gone, `json.Unmarshal` silently ignores the unknown key (Go default) and the agent is treated as active. Intended, safe, no migration, no data loss.

## Risk assessment
1. **Count cascade (highest):** ~9 files assert 37. Miss one → CI red. Mitigation: the build hard-fails on any dangling `IsActive()`/`Enabled`/tool-constructor reference; the count tests catch the rest; golden `-update` handles the golden.
2. **`TestAgentDelete` fixture** still sets `Enabled:&enabled` → compile error if not cleaned. Mitigation: explicit step G fixup.
3. **Routing behavior:** dropping `IsActive()` means routing/fallback consider every registered chat-target agent. `IsChatTarget()` still excludes workers. Correct — no agent is ever "disabled" once the feature is gone.
4. **No contract/UI/import-cycle risk** — `IsActive()` is unexported and consumed only within pkg/routing, pkg/gateway, pkg/agent.

## Verification
`CGO_ENABLED=0 go build -tags goolm,stdjson ./...` (hard-fails on any dangling ref) · gofmt · `make verify-contracts` (must be a no-op — no contract change) · scoped tests: `./pkg/config ./pkg/sysagent/... ./pkg/routing ./pkg/gateway ./pkg/agent` (run the count/permission/routing tests) · golden regen + diff review · `npm run typecheck` + the humanizeToolName vitest · full CI go-test gate.

## Execution
Concentrated change; do it as ONE coordinated pass (the count cascade spans files and shouldn't be split across parallel agents racing the same test files): (1) remove core + guards + tools + ancillary; (2) update all counts + remove/fix tests; (3) regen golden; (4) SPA; (5) build/gofmt/scoped-tests green; (6) push + CI. Then the 7-reviewer gate per CLAUDE.md.

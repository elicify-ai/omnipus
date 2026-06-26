# Token-usage tracking (tokens only, no cost) — implementation spec

> **Status: DELIVERED (2026-06-26)** on `feat/0.1.0-uat-fixes` (commits `b8dc020f` W1 ·
> `e63b9319` W2 · `84afed66` W3 · `8b0942d5` reviewer-gate fixes · `0608ea70` polish).
> Decisions ratified in **ADR-023**. All three waves implemented, passed the 7-reviewer
> quality gate (twice — per-wave and whole-diff) with all CRITICAL findings fixed and
> regression-locked. Two non-blocking follow-ups tracked in **#449**. Key correction
> during review: the cache-token convention (`TokensIn+TokensOut==TokensTotal`; cache is a
> SUBSET of `TokensOut`, NOT additive) — bucket `Total` is the authoritative stored total,
> never `In+Out+cache`; the UI shows Total + Cached + Uncached (reconciling). See ADR-023 §D5.

**Decision:** report **token usage only** (no dollar cost). Deliver: a `get_usage` tool, a Usage analytics page, a per-session token counter (live + on reopening historic sessions), per-session totals in the Sessions panel. Full breakdown: **by agent · by session · by model · input/output/cache split**. **Out of scope:** external 3rd-party CLI subagents (`subagent_3p`) — they run on a separate engine and don't report usage through our provider layer; their usage is NOT tracked (show "not tracked", never a wrong 0). Keep the daily-cost **cap** (backend safety) — it's orthogonal to reporting.

## What already exists (don't rebuild)
- Per-session token totals persisted: `SessionStats.{TokensIn,TokensOut,TokensTotal}` in `meta.json` (`pkg/session/daypartition.go`), written on transcript append.
- `GET /api/v1/stats/tokens?period=month` aggregates per-agent token usage → `TokenUsageSummary` (`pkg/gateway/rest_stats.go`). `fetchTokenStats()` is wired in the SPA but unused.
- Live counter via WS `done` frame `stats.tokens` → `SessionBar`.
- `Session.total_tokens` already on the wire.

## Wave 1 — data model + contracts (foundational)
1. **`pkg/providers/protocoltypes` `UsageInfo`** — add `CacheReadTokens int json:"cache_read_tokens"`, `CacheWriteTokens int json:"cache_write_tokens"`. Update providers that currently COLLAPSE cache into prompt to populate them instead: claude_cli (`CacheCreationInputTokens`+`CacheReadInputTokens`), codex_cli (`CachedInputTokens`), anthropic/anthropic_messages (`cache_creation_input_tokens`/`cache_read_input_tokens`), openai_compat + gemini-compat (`prompt_tokens_details.cached_tokens` / `cachedContentTokenCount`). Keep `PromptTokens` as the *uncached* input (subtract cache so totals don't double-count) — document the convention.
2. **`pkg/session` `SessionStats`** — add `TokensCacheRead int`, `TokensCacheWrite int`, and `ByModel map[string]ModelTokens` (`ModelTokens{In,Out,CacheRead,CacheWrite,Total int}`). Accumulate on transcript append using the assistant entry's `Model` + token fields. The `TranscriptEntry` already has `Model`; ensure it also carries the cache split (add fields if missing).
3. **Scope guard:** only accumulate usage for native-engine turns. Skip `subagent_3p` (external CLI workers). Find where turn stats are recorded; gate on agent type ≠ external-CLI worker.
4. **Contracts (Constraint #8):** extend `SessionStats.yaml` (cache + by_model), `AgentTokenEntry.yaml` / `TokenUsageSummary.yaml` (cache + optional models[]), `/stats/tokens` `period` enum → `[day,week,month,all]`. Add a `models[]`/`by_model` to the usage summary. Regen via `scripts/gen-contracts.sh`.
5. Keep cost fields on the wire (`DoneStats.cost`, `SessionStats.cost`) — harmless; the SPA stops reading them.

## Wave 2 — aggregation + `get_usage`
6. **Shared aggregator** (refactor `HandleTokenStats`): `aggregateUsage(sessions, periodStart, periodEnd) → summary` supporting `period=day/week/month/all` and dimensions **by agent / by model / by session**, with in/out/cache split. Exclude `subagent_3p` sessions.
7. **`get_usage` tool:** repurpose the `query_cost` NOT_IMPLEMENTED stub (`pkg/sysagent/tools/diag.go`). Params: `period` (day/week/month/all, default month), optional `agent_id`, optional `session_id`, optional `by` (agent|model|session). Returns token breakdown (in/out/cache/total). No dollars. Rename the tool to `get_usage`; update registry/category/rbac/confirmation/ratelimit/prompt entries + the tool-count tests (35 stays 35 — rename, not add/remove). Update the golden + humanizeToolName.
8. Extend `/stats/tokens` handler to the new periods + dimensions + cache.

## Wave 3 — frontend (no new dep; CSS/Progress bars)
9. **Strip cost:** `SessionBar` ($ block, formatCost, CurrencyDollar import), `MessageItem` (per-msg cost), `AgentProfile` (Cost StatCard → token stat). Keep the daily-cost-cap **config** controls.
10. **Session counter:** seed `sessionTokens` from `session.total_tokens` on `attachToSession` so historic sessions show the total immediately; keep live accumulation.
11. **Sessions panel rows:** render a muted token chip per row (`Session.total_tokens`, formatted `44.0k`).
12. **Usage page** (`src/routes/_app/usage.tsx` + `LIBRARY_ITEMS` entry in `Sidebar.tsx`, `ChartBar` icon):
    - Hero stat row (StatCard pattern): **Total tokens (period)** ⭐ hero · Input/Output/Cached split · Sessions · Top agent.
    - Period anchor (Tabs/segmented: Day/Week/Month/All), top-right, persistent.
    - "Tokens over time" → per-period **CSS bar row** (no chart lib).
    - Tabs: **By agent / By model / By session** → horizontal **`Progress` bars** (name + bar + count, sorted desc); By session → sortable table linking to the session.
    - **Skeleton** loading; honest **empty** state ("No usage yet — start a chat"); "no data for this period" keeps the selector.
    - JetBrains Mono for numbers; human-format (`44.0k`/`1.2M`); one dominant hero number (Anchoring); accessible bars (≥3:1, labels, aria).

## Tests (production-grade)
- **Backend:** UsageInfo cache parsing per provider (table tests with sample responses incl. cache); SessionStats accumulation (in/out/cache/by-model) on append; the scope guard (subagent_3p excluded); the aggregator (periods, by-agent/model/session, cache split, empty); `get_usage` tool (params, output shape, no-dollars, period/dimension); contract_test (schema-valid JSON); count stays 35 (rename).
- **Frontend:** Usage page renders hero/bars/tabs/empty/loading (vitest + RTL); cost fully removed (no `$` in chat/agent UI — assert); session counter seeds from total on attach; sessions-row chip.
- **Contracts:** verify-contracts no-op after regen.
- **CI:** full go-test gate green; typecheck + vitest green.

## Review gates
Per CLAUDE.md: 7-reviewer gate after each wave + 14 on the whole diff before done. Fix all findings.

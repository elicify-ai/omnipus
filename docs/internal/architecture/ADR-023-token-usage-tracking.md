# ADR-023 — Token-Usage Tracking (tokens only, read-time aggregation)

**Status:** Accepted
**Date:** 2026-06-26
**Deciders:** operator, architect, backend-lead, frontend-lead

---

## Context

Omnipus persisted only a single in-memory running cost total for the current UTC day
(`pkg/security/ratelimit.go` `dailyCostUSD`, used by `CheckGlobalCostCap`). There was
no usage history to query, which is why the `query_cost` system tool was a
`NOT_IMPLEMENTED` stub. The operator wanted: a queryable usage tool, a Usage analytics
screen, a per-session usage counter (live + on reopening historic sessions), and a
per-session total in the Sessions panel.

Before building, we researched how the field tracks LLM spend
(`docs/internal/research/cost-tracking-opencode-openclaw-2026-06.md`): the dominant
pattern is a per-(provider, model) **price table** (input/output/cache_read/cache_write
per 1M tokens, e.g. models.dev) times token counts, with subscription plans handled by
tracking the provider's native unit. The hard part is that pricing is volatile,
per-model, context-tier-dependent, and subscription billing is fragmenting (Cursor,
Codex, Copilot all moved to credit/hybrid models in 2025–2026). Maintaining a correct
price table is an ongoing burden, and a stale table silently produces wrong dollar
figures.

This ADR records the decisions taken so a future contributor does not "fix" them by
adding a price table or a cache, or "correct" the cache-token accounting that looks
like an under-count but isn't.

## Decisions

### D1 — Report tokens only; no dollar cost. Keep the daily-cost cap.

We report **token usage only** — never a dollar estimate. Rationale: Omnipus ships no
pricing table and we will not take on maintaining one (it drifts, it's per-model and
per-context-tier, and subscription billing increasingly meters in provider-native
units a token×price figure misrepresents). Tokens are the honest, provider-agnostic,
zero-maintenance unit.

The daily-cost **cap** (`pkg/security/ratelimit.go`, SEC-26) is a separate **safety
control**, not reporting. It stays, denominated in dollars, because a runaway-spend
guardrail must be meaningful in money. Cost fields remain on the wire
(`SessionStats.cost`, `DoneStats.cost`) — harmless; the SPA simply stops reading them.
Dropping them would be a breaking contract change for no benefit.

### D2 — Read-time aggregation over `meta.json`; no usage store, no rollup.

Usage is aggregated **at request time** from the per-session `SessionStats` already
persisted in each session's `meta.json`, via a pure function
`session.AggregateUsage([]*UnifiedMeta, UsageOptions) UsageReport`
(`pkg/session/usage.go`). There is **no** separate usage log and **no** materialized
rollup (unlike the opencode append-per-request + rollup model in the research doc).

Rationale: it fits the file-based, no-DB constraint (Constraint #1/#2), it is strictly
correct with no cache-invalidation surface, and it is simple. The cost is **O(sessions)
file reads + unmarshals per request** (`AgentLoop.ListAllSessions`), and the REST
handler does a second pass to build the per-agent × per-model breakdown. For v0.1 scale
(single operator, hundreds of sessions, 90-day retention) this is fine behind an
authenticated, human-paced screen.

**Known scaling cliff (accepted for v0.1):** at thousands of sessions, or a
multi-tenant variant, the full scan per request becomes the bottleneck. The trigger to
revisit (a materialized rollup) is session-count / latency; it aligns naturally with
v0.3 per-workspace usage. Do **not** retrofit a stale cache before then.

### D3 — The aggregator lives in `pkg/session` (leaf), not a new package.

`AggregateUsage` is a pure function over `session.UnifiedMeta`/`SessionStats`/
`ModelTokens`. Both consumers — the gateway handler (`pkg/gateway/rest_stats.go`) and
the `get_usage` tool (`pkg/sysagent/tools/diag.go`) — already depend on `pkg/session`,
so co-locating adds no import edge and no cycle. Config-specific policy (the
`subagent_3p` exclusion, agent-name resolution) is injected via callbacks
(`Exclude func(agentID) bool`, `NameResolver func(agentID) string`), keeping the leaf
config-agnostic. A dedicated `pkg/usage` would earn its keep only once usage gains its
own persisted state (a rollup) or its own wire types — neither exists today.

### D4 — `subagent_3p` (external-CLI workers) are not tracked.

External-CLI subagents (`claude-code` / `codex` / `opencode`) run on a separate engine
we cannot meter, so their usage is **not tracked** — shown as "not tracked", never a
wrong `0`. Enforced belt-and-suspenders:
- **Write-time:** external-CLI dispatch (`pkg/agent/external_dispatch.go`) never calls
  `AddTurnStats`/`AddTurnCacheStats`, so those turns never accumulate `SessionStats`.
- **Read-time:** the aggregator's `Exclude` predicate skips any session owned by a
  `subagent_3p` agent, via the single authoritative
  `config.Config.IsExternalCLIWorkerID(agentID)` helper used by both callers.

The two gates guard different failure modes (accumulating wrong data vs. displaying a
3p-owned session that carries legacy native tokens); read-time exclusion is the
authoritative reporting filter.

### D5 — The cache-token accounting convention.

This is the most counter-intuitive decision and is documented on the source types
(`pkg/session/daypartition.go`, `pkg/session/usage.go`,
`contracts/components/schemas/ModelTokens.yaml`) as well as here:

- Every transcript entry adds its `Tokens` to **exactly one** of `TokensIn`
  (non-assistant roles) or `TokensOut` (assistant role), **and** to `TokensTotal`.
  Therefore **`TokensIn + TokensOut == TokensTotal`** always.
- An assistant turn's `entry.Tokens` is the **full** turn total (uncached input +
  cache_read + cache_write + completion), and it is added to `TokensOut`. The cache
  split (`TokensCacheRead`/`TokensCacheWrite`) is tracked **additionally** — so cache
  tokens are a **SUBSET of `TokensOut`**, already counted inside it, **NOT additive**.
- Consequently `Total` is the **authoritative recorded total**
  (`SessionStats.TokensTotal`, or `ModelTokens.Total` per model). It must **never** be
  reconstructed as `In + Out + CacheRead + CacheWrite` — that double-counts cache.
- `ByModel` records only `CacheRead`/`CacheWrite`/`Total` on the assistant write path
  (`In`/`Out` stay 0; they are only populated via the `UpdateStats` delta path). The
  model dimension therefore keys its bucket `Total` off `mt.Total`, and folds any
  unmodeled remainder (`TokensTotal − Σ mt.Total`, typically input-side tokens) into a
  `(unknown)` bucket so the bucket-Total sum reconciles exactly with the grand total.

**Reconciliation guarantee:** `Σ bucket.Total == report.Total.Total` across all three
dimensions (agent / model / session). The In/Out/Cache sub-fields reconcile exactly for
agent and session; for the model dimension only `Total` reconciles. The UI presents
**Total** + **Cached** (read+write, a subset) + **Uncached** (`Total − Cached`) — an
honest split where `Cached + Uncached == Total` — rather than a misleading
Input/Output/Cached triple (Input ≈ 0; cache already inside Output).

### D6 — Attribution: one session → its `ActiveAgentID`.

A session's tokens are charged to `sm.ActiveAgentID` (fallback `AgentIDs[0]`), not split
across `AgentIDs` — attributing to each entry would double-count on handoffs. This
preserves the prior `HandleTokenStats` behavior. **Known coarseness (v0.3 candidate):**
in a delegation-heavy Workspaces world (Orchestrator + Scout + Builder on one session)
this under-represents the agents that did the work; per-turn attribution
(`TranscriptEntry.AgentID` is already recorded) is the more accurate long-term model.

## Consequences

- `query_cost` (NOT_IMPLEMENTED stub) was **renamed** to `get_usage` (system-tool count
  unchanged at 35): a real tool returning a token breakdown by period
  (day/week/month/all) and dimension (agent/model/session), no dollars.
- New Usage screen (`/usage`), per-session token counter (live + seeded from
  `Session.total_tokens` on attach), and Sessions-panel token chips — all token-only,
  no chart library (CSS + `Progress` bars).
- Wire contract gained `ModelTokens`, cache + `by_model` on `SessionStats` /
  `AgentTokenEntry` / `TokenUsageSummary`, and the `/stats/tokens` period enum
  `[day,week,month,all]`. `get_usage` returns plain tool JSON (agent→LLM boundary, not a
  cross-gateway wire type — outside Constraint #8).
- The gateway response reuses generated `gen.*` element types where possible; the
  oapi-codegen-inlined `by_model` shape is mirrored by a `// not-wire-format`
  `byModelCell` helper, guarded against drift by
  `TestHandleTokenStats_WireShapeMatchesContract` (decodes the body into
  `gen.TokenUsageSummary` with `DisallowUnknownFields`).

## Deferred (tracked, v0.1 non-blocking) — issue #449

1. **REST partial-error surfacing.** `get_usage` flags `partial`/`partial_error_count`
   when some session stores fail to load; the REST `/stats/tokens` handler only logs
   them (the dashboard degrades silently). Add a `partial` field to `TokenUsageSummary`
   + a dashboard banner.
2. **External-CLI scope-guard production test (G2).** The "zero contribution" guarantee
   is enforced in code and locked by a session-side simulation; a build-tag-gated CI
   test asserting external-CLI dispatch never calls `AddTurnStats` would close the gap
   (deferred from the devpod due to OOM risk linking `pkg/agent`).

## References

- Spec: `docs/internal/specs/token-usage-tracking-2026-06.md`
- Research: `docs/internal/research/cost-tracking-opencode-openclaw-2026-06.md`
- Code: `pkg/session/usage.go`, `pkg/session/daypartition.go`,
  `pkg/gateway/rest_stats.go`, `pkg/sysagent/tools/diag.go`,
  `pkg/config/config.go` (`IsExternalCLIWorker[ID]`),
  `src/components/screens/UsageScreen.tsx`, `contracts/components/schemas/ModelTokens.yaml`
- Epic #440; follow-ups #449

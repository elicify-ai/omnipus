# e2e flake root-cause investigation — 2026-07-20

**Branch:** `release/v0.1.1` @ `54db5b20` (ADR-050 run-history merged in).
**Trigger for investigation:** ci-omnipus `e2e` gate flaked on different tests across runs
(T24b, then mcp-add, then T26). Operator directive: *these were green before — "contention"
is a trigger, not a root cause; find the real mechanism for each.*

**Verdict:** three **distinct, real** root causes. **None is a code regression from the
run-history feature.** Two are pre-existing latent bugs (one product, one test) that the
merge's test-suite growth newly *exposed*; one is a pre-existing marginal design gap that the
same growth + an infra oversubscription *tipped over*. Each is independently fixable.

The fixes below are captured as a re-appliable patch (`e2e-flake-fixes-2026-07-20.patch`) and
were **deliberately NOT committed** — held pending the operator's separate stability-issues
branch, which may address the same WS fragility.

---

## Flake 1 — T24b (`cancel-cross-channel.spec.ts:569`, WS code 1006)

**Symptom:** during the ~2.6-min *awaited* (async=false) delegation turn, the browser's
WebSocket drops with **code 1006** (abnormal close), the chat input goes disabled
("Reconnecting…"), the delegate message never sends, and `[data-testid="activity-bar"]` times
out at 150s. Passes solo in ~14s.

**Root cause — pre-existing WS keepalive gap (design), not a merge regression:**
- `pkg/gateway/websocket.go`: server pings every 30s; read deadline 60s, refreshed only by an
  inbound client message or gorilla's native PONG handler. During a *message-idle* long turn
  the only keepalive is a timely PONG, leaving only ~30s of round-trip slack per cycle before
  the server unilaterally `conn.Close()`s. **No WS Close control frame is written first**, so
  the browser sees exactly **1006**.
- The exit path that fires here logged at `slog.Debug`; the e2e shard config inherits the
  default `warn` level, so **the server-side close was invisible** — the earlier "no
  server-side WS error" was a *logging blind spot*, not proof of innocence. (The one visible
  line, `channels/manager.go:1227` "no active connection … send failed", is the downstream
  discovery after the WS was already gone.)

**Suspects ruled out with evidence** (all named hypotheses were wrong):
- MCP #520 reconciliation (`loop_mcp.go`): **byte-identical** a2eb507c↔54db5b20; its commit
  `f0d5ad70` is an *ancestor* of a2eb507c — not in the merge diff at all.
- run-history `websocket.go` change: only a non-blocking `select`-guarded
  `EventKindTaskRunStatus` case + an int64 cast; never touches read/ping/write pumps.
- `go1.26.5`: same on both sides (only the redundant `toolchain` line differs).
- New goroutines the merge actually adds: just a 5-min task-trigger recovery sweep and the
  existing 24h retention sweep — both low-frequency, neither hot.

**Why now / only under 6-shard concurrency:** the merge added `calendar-recurrence.spec.ts`
(656 lines) to the `ui` shard. All 6 shards run concurrently on one 8-vCPU worker with **no
`GOMAXPROCS` cap** (unlike the `go-test` gate, which explicitly caps it in `runci.sh`). The
extra CPU demand during T24b's window — the single longest-lived WS in the matrix — starved
the browser's prompt PONG and tipped the marginal keepalive budget over.

**Fix (`pkg/gateway/websocket.go`):**
1. Named constants `wsPingInterval = 30s`, `wsKeepAliveReadDeadline = 120s` (up from six
   scattered `60s` literals) → ~90s round-trip slack per cycle; a genuinely dead connection is
   still reclaimed within ~2 min.
2. Elevated the two `readLoop` exit logs (CloseAbnormal/GoingAway branch; ReadMessage-timeout
   branch) from `Debug` → `Warn` so a recurrence is diagnosable from `gateway.log` directly.

---

## Flake 2 — mcp-add (`mcp-add.spec.ts`, duplicate-409 conflict toast)

**Root cause — real PRODUCT bug (pre-existing), in `src/lib/queryClient.ts`:**
The `doAdd` mutation uses the global QueryClient. Its `mutations.retry` predicate excluded
401/403/404 but **not 409**. TanStack's default backoff retries a 409 three times
(1000+2000+4000ms = **~7s of unconditional client-side sleep**) before `onError`/the conflict
toast fires — leaving only ~3s against the test's 10s `toBeVisible()`. Any load consumes that
margin. The flow is fully mocked (`page.route()`), so no backend/reconciliation is involved —
MCP #520's earlier switch-to-mock merely removed the fast real-backend path that had *masked*
this pre-existing defect.

**Fix (`src/lib/queryClient.ts` + `queryClient.retry.test.ts`):** add `409` to the
retry-exclusion list for both queries and mutations (a conflict can't be resolved by retrying
identical data), mirroring 401/403/404; + 409 regression tests.

---

## Flake 3 — T26 (`cancel-cross-channel.spec.ts:635`, audit entries)

**Root cause — TEST bug (fixed-sleep-before-async-assert anti-pattern):**
The test used `page.waitForTimeout(2000)` then asserted the `turn_cancel_attempt`/`turn_canceled`
audit entries. But `turn_canceled` is written from `RequestCancel`'s `SetOnCancelFinish`
callback, gated behind the real LLM stream noticing context cancellation (up to 3s graceful →
hard-abort, then 5s → detached). The audit writes themselves are synchronous/flushed, but the
flat 2s sleep is provably too tight once any part of the async cancel cascade slows under load.
(Matches the operator's own documented anti-pattern: "fixed sleep before an async assert →
wait-on-condition".)

**Fix (`tests/e2e/cancel-cross-channel.spec.ts`):** replace the fixed 2s sleep + single read
with a poll loop (250ms interval, 20s deadline) that waits on the actual condition (both audit
entries present); original diagnostic messages preserved.

---

## Cross-cutting infra follow-up (out of the fixes' scope; recommended)

The common *trigger* is `deploy/ci-worker/runci.sh`'s `_e2e_run_shard`: 6 shards run fully
concurrently on 8 vCPU with **no `GOMAXPROCS` cap** on the per-shard gateway processes — the
exact oversubscription class the `go-test` gate already fixes (`-p2` + `GOMAXPROCS=4`).
Options: apply the same `GOMAXPROCS` discipline to `_e2e_run_shard`, and/or move the heavy
`calendar-recurrence.spec.ts` to the `solo`/`ui-heavy` shard so long-lived tests get a fair
scheduling shot. This would reduce the whole class of contention-tipped flakes, independent of
the three code fixes above.

## Verification (of the held fixes, before revert)
- `gofmt`/`go build`/`go vet` clean; `pkg/gateway` WS + forwarder scoped tests pass.
- `npm run typecheck` clean; `queryClient.*` + `McpServerModal` vitest 58/58 (incl. new 409 tests).

## Files (fixes, in the patch — NOT committed)
- `pkg/gateway/websocket.go` — WS keepalive widen + Debug→Warn logging
- `src/lib/queryClient.ts` + `src/lib/queryClient.retry.test.ts` — exclude 409 from retry
- `tests/e2e/cancel-cross-channel.spec.ts` — T26 poll-on-condition

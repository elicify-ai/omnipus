# Max-parallel-agents concurrency gap — live UAT finding, 2026-07-31

**Date:** 2026-07-31
**Found during:** live UAT testing on `omnipus-uat-swimlane.fly.dev` (1 vCPU / 512MB Fly machine), branch `feature/plan-swimlane-board`
**Method:** evidence-based — every claim below is cited to code (file:line) or to a directly-pulled artifact (session transcript, `session_lifecycle` on-disk record, `dmesg`, `free -m`). No claim in this document is inferred from timing correlation alone unless explicitly marked as such.

---

## Summary

| # | Finding | Severity | Layer |
|---|---------|----------|-------|
| G1 | `max_parallel_agents` / the concurrency semaphore is **never applied to delegate calls made directly from a top-level chat turn** — it only gates a sub-turn's *own* further nested delegation | **HIGH** | backend |
| G2 | `autoDetectMaxParallel()` sizes the cap from `NumCPU`/`RAM_GB` as if a SubTurn were a CPU-bound process (~1.5GB each); it's actually a cheap, netpoller-parked goroutine (tens of KB) — the formula reasons from the wrong resource entirely | MEDIUM | backend |
| G3 | An explicit `performance.max_parallel_agents` config value is hard-clamped to a ceiling of 16 (`clampParallelExplicit`), with no way to raise it without a code change + rebuild | MINOR | backend |
| G4 | Real memory pressure exists on small deployments independent of the above — confirmed via kernel-level allocation failures, not assumed | evidence, not a defect per se | infra |

---

## G1 — Concurrency cap doesn't apply to root-level delegation — **HIGH**

**What:** `turnState.concurrencySem` (`pkg/agent/turn.go:150`) is assigned in exactly one place in the entire codebase: `pkg/agent/subturn.go:1051`, inside `spawnSubTurn`, on the **child** turn state being constructed:
```go
childTS.concurrencySem = make(chan struct{}, rtCfg.maxConcurrent)
```
A root/top-level chat turn (e.g. Jim's own turn, receiving a user's chat message directly) is **not** created via `spawnSubTurn` — nothing else in the codebase ever initializes its `concurrencySem`. It stays at Go's zero-value, `nil`.

The acquire path (`subturn.go:598-629`) guards on that field:
```go
if parentTS.concurrencySem != nil {
    // ...acquire with timeout...
}
```
When `parentTS` is a root turn, this condition is false, and the **entire semaphore-acquisition block is skipped** — no wait, no timeout, no rejection. Every call succeeds immediately regardless of how many are already in flight.

**Consequence:** the cap only ever limits a sub-turn's *own* further delegation (e.g. Ray limiting how many children Ray itself can spawn) — never the first hop of delegation from the user-facing chat turn. Any workflow where the top-level agent fans out directly (exactly the "deploy N parallel research agents" pattern this UAT round tested) bypasses the cap entirely, no matter what `max_parallel_agents`/`OMNIPUS_MAX_PARALLEL_AGENTS` is set to.

**Evidence (live reproduction, not inferred):**
- Session `session_01KYV5AYDE6QNBRQDFSJPDVTC7` (agent: `jim`, title *"please run a research, deploy 24 subagents doing parallel..."*), transcript messages 2-25: 24 `delegate` calls issued directly by `jim` within a 0.29-second window (`2026-07-31T04:02:40.715Z` → `04:02:41.002Z`).
- Cross-referenced against `session_lifecycle/*.jsonl` (the authoritative per-child lifecycle record): all 24 children share `owner_scope_id` = empty string (i.e. no intermediate parent scope — direct children of Jim's root turn) and all 24 show a `queued`→`running` transition, meaning every one of them successfully acquired... nothing, because there was nothing to acquire.
- Peak-overlap computation (sweep over each child's `created_at`→terminal/`now`) for that group: **24 simultaneous**, against an effective configured cap of 16 (`performance.max_parallel_agents`, itself clamped — see G3).
- Contrast: the *same* session also shows nested delegation (`parent_agent_id: ray`, 44 children across several distinct `owner_scope_id` groups) where the largest single group peaked at 8 — consistent with a semaphore actually being present and simply not being exercised past its own cap in that instance, not proof the cap enforces globally.

**Fix direction (not yet implemented, no code changed for this finding):** either (a) initialize `concurrencySem` on root turn states too (at whatever point a root `turnState` is constructed for an incoming chat message), sized the same way `getSubTurnConfig()` already resolves it, so the very first hop of delegation is gated identically to nested hops; or (b) if root-level fan-out is intentionally meant to be uncapped by design, document that explicitly and rename/scope `max_parallel_agents` so its description doesn't imply a global ceiling it doesn't enforce.

---

## G2 — Auto-detect formula reasons from the wrong resource — MEDIUM

**What:** `autoDetectMaxParallel()` (`pkg/config/config.go:483-493`) computes `min(NumCPU-2, RAM_GB/1.5)`, floored at 2, capped at 16. This models a SubTurn as if it were a dedicated CPU-bound process costing ~1.5GB of RAM apiece.

**Evidence this doesn't match reality** (from a dedicated code-research pass, file:line cited throughout):
- A SubTurn is a goroutine, not a thread or process — confirmed at the actual async dispatch site, `pkg/tools/delegate.go:1282`'s `go func() { ... t.spawner.SpawnSubTurn(...) }()`, which calls straight through to `runTurn` synchronously on that same goroutine. No `exec.Command`, no OS thread spawn, no cgo in the native path.
- LLM calls are plain `net/http` (`pkg/providers/common/common.go:56-89`), so a goroutine blocked on an LLM response parks on Go's netpoller — it releases its OS thread back to the scheduler rather than pinning it.
- The actual per-SubTurn memory cost is a `turnState` struct plus an ephemeral session history capped at `maxEphemeralHistorySize = 50` messages (`pkg/tools/delegate.go:34`) — tens of KB, not ~1.5GB.
- GOMAXPROCS is never set anywhere in the codebase (`pkg/`/`cmd/` grep: zero hits outside a CI-runner test comment) — it's whatever `runtime.NumCPU()` auto-detects, which is 1 on the box this was tested on, collapsing `NumCPU-2` to a negative number that contributes no real signal (the formula just hits its floor constant).

**Consequence:** on a small box, the formula produces a number (2) that reflects neither the true CPU cost (near-zero, since work is I/O-bound) nor a validated memory ceiling (it wasn't derived from an actual per-turn memory measurement) — it's an artifact of the floor constant, not a calculated value.

**What a better estimate would need:** a per-turn marginal memory measurement (RSS delta from a controlled concurrency ramp), not a CPU-derived heuristic. Not yet measured — flagged as follow-up work, not fixed here.

---

## G3 — Explicit config value is capped at 16 regardless of what's requested — MINOR

**What:** `clampParallelExplicit` (`pkg/config/config.go:459-468`) hard-caps ANY explicit `performance.max_parallel_agents` value (including an operator-set 128) down to 16:
```go
func clampParallelExplicit(v int) int {
    const minPar, maxPar = 1, 16
    ...
}
```
**Consequence:** an operator cannot raise the ceiling above 16 via config/env var alone — it requires a code change (raising `maxPar`) and a rebuild/redeploy. Verified directly: patching `config.json` to `128` on the UAT box and confirming a live reload still only ever allows what G1 already made moot in practice (root-level fan-out ignores the cap regardless of its value).

---

## G4 — Real memory pressure exists (evidence, contextual)

Not itself a code defect, but material to sizing decisions: `dmesg` on the UAT box shows genuine kernel-level allocation failures during the 24-parallel test, not inferred from indirect signals:
```
__vm_enough_memory: pid: 824, comm: chromium, bytes: 536870912 not enough memory for the allocation
__vm_enough_memory: pid: 803, comm: chromium, bytes: 536870912 not enough memory for the allocation
... (7 total occurrences across the test window)
```
And at time of writing, `free -m` on the same box shows 6MB free / 28MB available out of 459MB total, with a 1-minute load average of 1.71 on a single vCPU. No visible crash resulted from this specific test, but the headroom is genuinely gone. This corroborates the user's own operational experience (memory, not CPU, is typically the real constraint for parallel-subagent workloads) independent of the G1/G2 findings above.

---

## Open items / not yet done

- G1's fix (initializing a root turn's `concurrencySem`) has **not** been implemented — this document records the finding only, per explicit instruction to document the gap, not patch it yet.
- G2's "better estimate" needs an actual controlled memory-ramp measurement before a replacement formula can be proposed with evidence rather than another guess.
- The separate, still-open "replay never reconciles an orphaned subagent span" gap (found earlier in this same UAT round) is tracked in conversation history, not yet in a standalone doc — candidate for a follow-up write-up if it recurs.

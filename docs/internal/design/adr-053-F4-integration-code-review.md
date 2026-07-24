# ADR-053 F4 Integration — Code Review

**Branch:** `feature/plan-swimlane-board`  
**Range:** `75ab6eb2..HEAD`  
**Reviewer:** read-only integration review (scope-limited files + commit log)  
**Date:** 2026-07-24  
**Scope:** F4 deferred security/integration items (#536–#542, D12/#540) plus follow-up e2e/conformance green-ups and `clearGoal` pill-state fix.

**Verified locally:**
```text
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestConformance_t0' -p 1 ./pkg/agent/
ok  	github.com/elicify-ai/omnipus/pkg/agent	0.086s
```

---

## Executive summary

F4 delivered the four deferred engine hardening pieces as real code (owner-authority on `AppendCorrection`, intent-log HMAC/0700/fsync, `TokenBudget.SetCap` one-shot, per-member Play checkout) plus D12 USD-cap retirement and §9.1 conformance/e2e scaffolding. Unit/integration tests for each type are solid; `TestConformance_t0_ChatGoal_Design` passes after the pill-enum fix.

The integration story is **not closed end-to-end**:

1. **Play-from-commit (`PlayPlan` + `ResetMemberCheckout`) is not on the REST ▶ Play path** — production `POST /plans/{id}/restart` reimplements member reset without calling `PlayPlan`, so generation mint, `ResumeFromCommit`, and isolated checkout never run in operator-facing Play.
2. **Owner-authority gate leaks the real OwnerAgentID** in denial errors, contradicting its own “non-owner learns nothing” contract.
3. **E2E correctness depended on product workarounds** (force members `inbox→next`, per-test Main agents) that paper over real gaps both the design gate and live gateway will keep hitting.

Secondary issues: SetCap freeze does not cover constructor-set caps; broken HMAC chains log but still replay; SPA/embed drift risk on rate-limit schemas if pack is not refreshed.

**Verdict: REVISE** — ship-ready only after Play is single-path through `PlayPlan` (or REST is explicitly accepted as non-D13) and owner denial messages stop leaking identity. No CRITICAL exploit in the in-process engine paths themselves, but F4’s advertised product surface is not what operators hit.

---

## CRITICAL

_None that hard-block the whole tree as unsafe-to-run. The Play path gap is graded MAJOR (product contract miss), not CRITICAL security-RCE, because REST still requires authenticated gateway access and only restarts plans the operator can already see._

---

## MAJOR

### M1 — REST ▶ Play does not call `PlayPlan` (D13 / #537 dead in production)

**Files:**  
- `pkg/agent/plan_engine.go:2575–2633` (`PlayPlan` + `recordMemberResumePoint`)  
- `pkg/agent/plan_engine_commit_resolver.go:130–188` (`ResetMemberCheckout`)  
- `pkg/gateway/rest_plans.go:1005–1114` (`handlePlanRestart`)  
- Gateway boots resolver: `pkg/gateway/gateway.go` (~`SetCommitResolver`)

**Evidence:**  
`rg PlayPlan` shows **only tests/conformance** call `PlayPlan`. Production Play is `handlePlanRestart`, which:

1. `RestartReset`s failed members  
2. `planStore.Update` → `approved`  
3. Returns wire plan  

It never:

- increments plan generation (D13/G-12)  
- resolves last boundary commit  
- persists `ResumeFromCommit`  
- materializes `workspaces/<ws>/resume/<taskID>` checkout  

**Impact:** F4 claim “per-member git checkout for Play-from-commit (#537)” is true only for the engine API tests exercise. Operator corner (SPA ▶ / `POST /plans/{id}/restart`) gets ADR-052 restart without D13 resume isolation. Conformance_t2 step (7) validating Playwright-grade Play is integration-fake relative to the HTTP surface.

**Required fix (choose one, document in ADR if deferred):**
- A) Make `handlePlanRestart` call `pe.PlayPlan(ctx, id)` (preferred — single chokepoint), or  
- B) Inline the same generation/commit/checkout steps in REST and delete duplicate logic, or  
- C) Explicit ADR amendment: D13 materialization is engine-only / future, and drop “Play” wording from REST docs.

---

### M2 — Owner-authority gate leaks plan owner identity

**File:** `pkg/agent/plan_engine.go:2261–2273`

```go
if caller.AgentID != p.OwnerAgentID {
    return nil, fmt.Errorf("%w: plan %q: caller agent %q is not owner %q",
        ErrCorrectionNotOwner, planID, caller.AgentID, p.OwnerAgentID)
}
```

**Comment contract (same function):** gate runs first so “a non-owner learns nothing about the plan.”

**Impact:** Denial includes the real `OwnerAgentID`. Optional session mismatch is properly opaque (`caller session does not match`). Agent mismatch is not. Once `AppendCorrection` is callable from a multi-agent or tool path, this is an info disclosure. Unit test `gate precedes phase check` only asserts residual error type, not message opacity.

**Required fix:** Uniform opaque denial, e.g.  
`fmt.Errorf("%w: plan %q", ErrCorrectionNotOwner, planID)`  
Log owner/caller detail server-side only.

---

### M3 — Plan member dispatch requires `status=next`; REST create lands `inbox` (e2e-only triage)

**Files:**  
- `pkg/agent/plan_engine.go:911–914` (`dispatchReadyMembers` only `StatusNext`)  
- E2E workaround: `tests/e2e/conformance-design-e2e.spec.ts` (`createPlanMember` PATCH → `next`, commit `58a2d29e`)  
- Go conformance seeds members directly with `StatusNext` (`conformance_design_test.go`)

**Impact:** A design-faithful “create plan + members via API + execute” path stalls forever in `running` unless something triages inbox→next. Integration tests hide this; e2e required a product-shaped PATCH. Without product auto-triage on plan Approve/Execute (or document owner must move cards to Next), real §9.1 e2e and operators share the same wedge.

**Required fix:** Approve/Execute (or plan member create under a running/approved plan) promotes free members inbox→next, **or** dispatch treats “inbox with deps satisfied” as ready. Track as product bug if deferred.

---

### M4 — Multi-workspace `find_for_agent` ambiguity survives outside test isolation

**Evidence:** commit `2358521c` — Jim in several core teams → wrong workspace → turn canceled.

**Impact:** F4 e2e invented per-test Main agents to avoid a real dispatcher routing bug. Any install with one agent on multiple workspace teams still races plan member turns. Out of pure F4 file list but blocks claiming e2e green proves the design diagram under multi-workspace product use.

**Required fix:** `find_for_agent` must prefer the **task’s** `workspace_id` (or hard-fail multi-membership) rather than “first sorted id.”

---

## MINOR

### m1 — `TokenBudget.SetCap` one-shot does not freeze constructor cap

**Files:** `pkg/agent/budget.go:70–128`, `pkg/agent/loop.go:890`, `pkg/agent/goal_compile_test.go:406–422`

Boot does `NewTokenBudget(cfg.Planning.EffectiveTokenBudget(), …)` and **never** calls `SetCap`. `capFrozen` stays false until the first `SetCap`. `goal_compile_test` still expects `NewTokenBudget(1000)` then `SetCap(500)` to apply. Unit one-shot tests always start from `NewTokenBudget(0)`.

**Impact:** FR-177 “once at boot” is only enforced on the SetCap API, not on the actual boot path. Today harmless (nothing double-SetCaps), but a future hot path that calls `SetCap` after construction can still retarget the live ceiling once. Prefer freeze constructor value (`capFrozen: true` in `NewTokenBudget`) or drop SetCap from production API surface.

---

### m2 — Intent-log HMAC break is soft (log + continue replay)

**File:** `pkg/plan/intent_log.go:441–455`

Broken chain → `slog.Error` then `Classify`/`ReplayForward` as usual. Matches audit-log precedent (commented), so intentional. Still weakens “tamper-evidence” for M4 corrections: a spliced committed intent replays.

**Suggestion:** Config flag or Doctor check that fails closed in enforce mode; and/or refuse `ReplayForward` past `BrokenAt`.

---

### m3 — `clearGoal` terminal-state string match is brittle (but currently single-site)

**File:** `pkg/agent/goal_loop.go:338–341`  
`note == "condition met"` → `done`; else `failed`.

 sole met path: `goal_triggers.go:372`. Works; prefer a typed enum / bool `met bool` so a reason wording change cannot regress t0 again.

User clear note `"cleared by user"` correctly maps to pill `failed` (enum has no `cleared`) — aligns with `GoalStatusFrame.yaml` R§8.10. Chat reply still says “Goal cleared (…)” which is user-facing language, not wire state — fine.

---

### m4 — `markLocked` re-appends full self-contained bodies on commit/done

**File:** `pkg/plan/intent_log.go:238–270`

Every status flip rewrites entire `IntentRecord` (including `Members []task.Task`) as a new JSONL line. Correct for chain + latest-wins, but O(n members) bloat per correction. Acceptable at current scale; consider status-only append shape later.

---

### m5 — Append JSONL Sync does file `Sync` only (no parent dir fsync)

**File:** `pkg/plan/intent_log.go:276–296`  
vs `pkg/fileutil` atomic write pattern.

Crash after file fsync but before dir entry durable is rare on modern ext4/XFS; still weaker than `fileutil.WriteFileAtomic` ethos.

---

### m6 — Owner session opens with synthetic ID, no auth binding check at ensure time

**File:** `pkg/agent/plan_engine.go:2148–2159`  
`sessionID := "plan:" + p.ID`

Gate later compares `caller.SessionID` to that field when set. Empty OwnerSessionID → agent-only gate. Timings where OwnerSessionID is still empty allow any process that can spoof `CorrectionCaller{AgentID: owner}` — defense depends entirely on REST/tool layers never constructing that struct from untrusted input. Document and keep “no trusted internal bypass” when wiring handlers.

---

### m7 — Contracts residual `cleared` / D12 messaging drift

- `GoalStatusFrame` enum correctly drops `cleared` (`contracts/components/schemas/GoalStatusFrame.yaml:85–93`).  
- Older docs/Goal record schemas may still mention `cleared` (`contracts/.../Goal.yaml` grep history).  
- D12 wire cleanup looks correct in `RateLimitConfig.yaml` + hot-reload e2e.  
- Embedded SPA under `pkg/gateway/spa/` may lag generated types until pack pipeline runs — verify on aritifact bake, not in this Go pass.

---

### m8 — T26 cancel e2e timing widen is flake mitigation, not product assert

**File:** `tests/e2e/cancel-cross-channel.spec.ts` (commits `07526b2a`, `82e58701`)

OR of composer-enabled / stop-btn-gone + 90s budget fixes suite-load flake. Valid. Does not assert cancel latency SC if SC still claims near-instant Stop — orthogonal to F4 but in scope file list.

---

### m9 — Conformance e2e partial drawn-path assertions

Several Conformance_*_E2E specs accept `done|failed|awaiting_owner_correction` as success buckets without requiring full SUPERSEDE/TARGETED-RETRY round-trip under real LLM (t3). Good progressive green strategy; do not treat green CI as full diagram proof yet.

---

## Positives (keep)

| Area | Assessment |
|------|------------|
| **#536 AppendCorrection owner gate** | Front of function, before phase; good unit matrix (wrong agent, empty agent, session mismatch, matching session, phase probe). |
| **#538 SetCap one-shot** | Mutex + freeze + warn; dedicated `budget_test.go` coverage. |
| **#539 intent log** | 0700 dir, 0600 files, `f.Sync` on every append, HMAC via shared `pkg/audit` helpers, VerifyChain at ReplayAtBoot, CommitCorrection 4-step ordering, gateway derives subkey. |
| **#537 checkout helper** | Path escape checks on task.id; replace semantics; degrade-not-fatal shared-tree path. |
| **D12 retire USD cap** | Registry stripped; REST 400 on retired field; hot-reload e2e updated. |
| **clearGoal pill** | `done`/`failed` only — fixes SPA zod drop that froze t0 pill on `active`. Conformance walk updated. |
| **F2 plan_phase poll** | E2e correctly distinguished top-level `state` vs hold sub-phase after wedge diagnosis. |
| **§9.1 Go conformance suite** | Full-path design proxies without real LLM; sensible e2e residue comments. |

---

## Test results (this review)

| Command | Result |
|---------|--------|
| `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestConformance_t0' -p 1 ./pkg/agent/` | **PASS** (`0.086s`) |

Full `./pkg/agent` / gateway suite and real-LLM e2e **not** re-run in-pod (project rule: CI is authority for heavy gateway).

---

## Verdict

# **REVISE**

Ship F4 engine pieces only after addressing at least **M1** (Play single-path) and **M2** (opaque owner denials). **M3/M4** should be tracked product bugs if not fixed in the same PR wave — otherwise e2e “passes” remain harness-shaped.

**Suggested merge gate before calling F4 closed:**

1. REST restart → `PlanEngine.PlayPlan` (or ADR amendment + tests that lock the chosen contract).  
2. Opaque `ErrCorrectionNotOwner` messages + unit assert on error text.  
3. (Strongly recommended) inbox→next on plan execute + task-workspace preference in `find_for_agent`.  
4. Re-run Conformance_t0 + Play/correction unit packs + project e2e gate on `ci-omnipus`.

---

## Commit map (F4 core → green-up)

| Commit | Theme |
|--------|--------|
| `78fc3bcc` | SetCap one-shot (#538) |
| `b300e8fd` | Intent-log HMAC/0700/fsync (#539) |
| `0350d2e5` | AppendCorrection owner gate (#536) |
| `ab8b981d` | Play-from-commit checkout (#537) |
| `f7d3318d` / `c9691445` | §9.1 Go conformance (#541) |
| `d14670c1` / `d05f277c` | §9.1 e2e specs (#542) |
| `eb50a957` | D12 USD cap retire (#540) |
| `18456a60`…`82e58701` | Integration compile fixes, e2e shape/fault diagnosis, clearGoal pill, T26 flake |

---

*Read-only review — no production code modified.*


## Post-review fixes (2026-07-24)

- **M1 FIXED:** `handlePlanRestart` now delegates to `PlanEngine.PlayPlan` (single chokepoint for D13 generation + ResumeFromCommit + checkout). Partial member-reset failures still surface as HTTP 500.
- **M2 FIXED:** owner-denial errors are opaque (no OwnerAgentID leak); detail logged server-side only.
- **M3/M4:** tracked as product follow-ups (inbox→next triage; multi-workspace find_for_agent) — e2e workarounds remain; not blocking this landing if operator accepts.

**Revised verdict after fixes: PASS with tracked follow-ups (M3/M4).**

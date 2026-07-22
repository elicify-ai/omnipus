# Grill Review (RE-GRILL) — Unified Goal / Plan / Subagent Spec (ADR-053)

**Reviewed file**: `docs/internal/specs/unified-goal-plan-subagent-spec.md`
**Inputs**: `docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md` (Accepted); `docs/internal/design/unified-goal-plan-subagent-target-design-v2.2.html`
**Mode**: plan-spec format · **RE-GRILL** after the prior FAIL (C1 CRITICAL + M1..M8 MAJOR + minors)
**Date**: 2026-07-22
**Scope note**: ADR-053 decisions D1–D17 are NOT re-opened. This pass (1) verifies each prior finding is resolved, (2) checks the fixes introduced no new contradictions, with special attention to the three called-out focus areas.

---

## Executive Summary

The revision **resolves C1 and all of M1–M8** substantively and correctly at the point of each fix. However, the C1 fix — making `awaiting_owner_correction` a **durable** plan condition so the `re-planning` pill reconstructs after restart — was propagated to the R§8.10 ledger and the Contract-Surface crosswalk but **not** to the realizing functional requirements (FR-185/FR-186), two BDD scenarios, or a Clarifications entry, all of which still describe `re-planning` as an **ephemeral engine-phase signal**. That is a genuine internal contradiction that, taken at the FR's word, would defeat the very restart-reconstruction guarantee C1 exists to provide.

- **Findings**: 0 CRITICAL · **1 MAJOR** · 6 MINOR · 2 OBSERVATION
- **Verdict**: **REVISE** (one unresolved MAJOR; PASS requires zero CRITICAL/MAJOR)

Focus-area answers:
- **(a) Does durable `plan_phase` fully close the F2-restart gap without contradicting G-9/G-13/INV-7/INV-9?** Yes at the state-machine/FR-147/FR-193/INV level — the mermaid, INV-2/7/9, FR-118/141/147/193 and the lifecycle-transition dataset are mutually consistent and the 8-state S2 enum is preserved. One implementation-identifiability gap remains (m-3: how the boot sweep *identifies* the paused owner session of an awaiting-correction plan).
- **(b) Is the intent-log consistent with the per-file JSONL model and boot recovery?** Yes — a WAL file written/committed via the same temp+rename primitive is consistent with the storage model. Two details are under-specified (m-2: intent-record self-containment for forward-replay; plan-record-patch rollback + replay idempotency).
- **(c) Any NEW contradiction the revision introduced?** Yes — **M-1 (MAJOR)**: FR-185/FR-186 (+ two BDDs + a Clarification) contradict the C1-updated crosswalk on whether `re-planning` is ephemeral or durable.

---

## Prior-Findings Resolution Ledger

| Prior | Verdict | Evidence in the revised spec |
|-------|---------|------------------------------|
| **C1** (durable `plan_phase=awaiting_owner_correction` + persisted `last_unmet_terminal_signature`; owner session durable `paused`; boot sweep exempts; S2 stays 8 states; mermaid/INV-2/7/9/FR-118/141/147/193 reconciled) | **RESOLVED** (with one residual, see m-3) | FR-147 (L1455) makes it a durable PLAN condition on the existing `Plan.PlanPhase`; FR-118 (L1419) + FR-193 (L1486) + INV-9 (L521) encode the two exemptions; INV-7 (L519) keys no-re-judge on the *persisted* signature; mermaid (L477-509) keeps exactly 8 session-state nodes with a `paused` boot-sweep-exempt edge; dataset rows 6–9 (L1341-1344) match; Explicit Non-Behavior L423 forbids a 9th state; L1717 asserts "EXACTLY 8 states". Code check: `pkg/plan/plan.go` confirms `State` is the closed 5-set (L58-67) and `PlanPhase` the 4-value set (L169-186) rendered only when `State==running`, so `awaiting_owner_correction` as a new PlanPhase with `State=running` + session `paused` is coherent. |
| **M1** (`classifyNonVerdict` predicate) | **RESOLVED** | FR-137 (L1413) names the function; predicate = "did the verification mechanism run to completion?"; BDD outline L694-706 keys on machine-observable outcome (blocked/unreadable→`unable_to_verify` re-run-never-scored; ran-no-judgment→`criterion_unjudgeable`); Test 50. |
| **M2** (honest `failed(judge_rounds_exhausted)`, no false no-round-burn claim) | **RESOLVED** | FR-138 (L1414) explicitly forbids describing escalate-once as preventing round-burn; remediation = owner re-statement (diffed amendment) or `/goal clear`; owner-inert → `failed(judge_rounds_exhausted)`. Explicit Non-Behavior L424; BDD L708-714; Test 51. |
| **M3** (`deriveQuestionAuthority` fail-closed default + runtime upgrade) | **RESOLVED** | FR-139 (L1444) + FR-131 (L1438): omitted→`owner_required`; upgrades `self_ok→owner_required` on credential/spend/irreversible/out-of-scope; child cannot downgrade; enforced before FR-132 respond-side reject. BDD outline L892-908 (7 rows); Test 52. |
| **M4** (write-ahead intent-log for atomic tail append) | **RESOLVED** (with residual m-2) | FR-148 (L1456) + INV-6 (L518): one intent record → per-file temp+rename writes → mark committed; boot rolls back uncommitted / replays committed-unapplied. BDD L1102-1108; Tests 29, 54. |
| **M5** (SC-008 bounded overshoot) | **RESOLVED** | SC-008 (L1517) drops "never negative"; states counter-never-corrupted + overshoot BOUNDED by in-flight turn costs. Consistent with FR-173 (L1474), INV-8 (L520), dataset row 4 (L1319). |
| **M6** (highest-available-isolation-rung wording) | **RESOLVED** | FR-157 (L1466) + FR-154 (L1463) + US-11 AS-3 (L275) + dataset row 3 (L1328): "highest available rung (system-git worktree → go-git clone → subdir); never assuming go-git `worktree add`, which does not exist". |
| **M7** (7 placeholder FRs) | **RESOLVED** | Behavioral: FR-128/130/146/178/188 each now carry a BDD (L860, L868, L979, L1086, L1133) + Tests 55-59. Downgraded: FR-158 (L1467) + FR-177 (L1478) flagged config/non-behavioral; completeness claim L1611 amended to name exactly those two. |
| **M8** (SessionMessage `direction` enum) | **RESOLVED** | L531 adds `engine`, drops `human`; the 12-kind variant table (L537-548) maps every kind to one of the 4 remaining directions (`child_to_parent`×8, `engine`×1, `session_to_ui`×1, `parent_to_child`×2). |

All nine prior findings are addressed at the point of fix. The residuals below are either propagation gaps left by an otherwise-correct fix (M-1) or newly-visible under-specifications the tightened text now exposes.

---

## New Findings

### M-1 — MAJOR — Inconsistency — FR-185/FR-186 (+ BDDs + Clarification) still call `re-planning` an *ephemeral* signal, contradicting the C1-durable crosswalk

**Lens**: Inconsistency (focus area c). **Sections**: FR-185 (L1490), FR-186 (L1491), US-14 AS-1 BDD (L320), Pill-crosswalk BDD (L1124), Clarification (L1702) — vs R§8.10 ledger (L94), crosswalk table (L585), crosswalk narrative (L590), FR-147 (L1455).

The C1 fix's stated benefit is that `re-planning` "reconstructs correctly after a restart" because it is now sourced from the **durable** `plan_phase=awaiting_owner_correction`, not an ephemeral overlay (L94, L585, L590). But the FRs that *realize* R§8.10 were not updated:

- **FR-186 (L1491)** states: "`judging`/`re-planning`/`judge_unavailable` MUST be sourced from **ephemeral engine phase signals** (verifier-in-flight / **awaiting-correction** / Judge-availability), **NOT the durable** lifecycle". This lists `re-planning`/`awaiting-correction` as ephemeral — the exact opposite of FR-147 ("durable PLAN condition") and crosswalk L590 ("`re-planning` is sourced from the **durable** `plan_phase`").
- **FR-185 (L1490)** gives the pill formula as `f(lifecycle, engine_phase_overlay)` — a 2-arg formula omitting `plan_phase`, contradicting crosswalk L590's `f(lifecycle_state, engine_phase_overlay, plan_phase)` (3 args).
- The BDDs at L320 and L1124, and the Clarifications entry at L1702, repeat the "re-planning comes from engine phase signals / 3 overlay states sourced from engine phase signals" framing.

**Failure scenario**: An engineer implementing FR-185/FR-186 verbatim sources `re-planning` from an ephemeral in-memory engine-phase overlay. On `kill -9` during `awaiting_owner_correction`, that overlay is lost; after restart the durable session is `paused` and the plan is `plan_phase=awaiting_owner_correction`, but because the pill derivation ignores `plan_phase` (2-arg formula, ephemeral source), the pill renders `waiting_on_user` (the default `paused` mapping) instead of `re-planning`. This is precisely the post-restart pill mis-render C1 was written to prevent — the fix's own acceptance guarantee (L94/L585/L590) would fail, silently.

**Recommended fix**: Rewrite FR-186 to split the three overlay states by source — `judging` and `judge_unavailable` from ephemeral engine-phase signals; `re-planning` from the **durable** `plan_phase=awaiting_owner_correction` on the plan record (survives restart). Update FR-185's formula to `pill = f(lifecycle, engine_phase_overlay, plan_phase)`. Fix the wording at L320, L1124, and L1702 to match L94/L585/L590 (do not lump `re-planning` under "engine phase signals" without qualifying it as the durable plan condition).

---

### m-2 — MINOR — Incompleteness — Intent-log record self-containment + plan-patch rollback/idempotency under-specified

**Lens**: Incompleteness (focus area b). **Sections**: FR-148 (L1456), INV-6 (L518), BDD L1102-1108.

The WAL is coherent and consistent with the per-file JSONL model (the intent file is written/committed via the same temp+rename primitive). Three details are not pinned down, and the "committed-but-unapplied → replay **forward**" clause is unimplementable without them:

1. **Self-containment.** Forward-replay of a committed-but-unapplied intent requires the intent record to carry the **full body** of each new member (not just ids), yet the related `revision_entry.tail_adds` is described as "member ids + edges" (L545/L558). An engineer who builds an ids-only intent can roll back but cannot replay forward — the member content is lost. State that the intent record is self-contained (full member bodies + edges + revision entry + plan-record patch).
2. **Plan-record patch on rollback.** FR-148 lists "plan-record patch" as part of the append but the rollback description (L1456: "delete partially-written members, wire no edges") does not say whether an already-applied plan-record patch (e.g. DAG edges / clearing `awaiting_owner_correction`) is reverted. Specify either that the plan-record patch is applied only in/after the commit phase, or that rollback reverts it.
3. **Replay idempotency.** Forward-replay may re-apply writes that partially landed; state that per-file writes and the plan-record patch are idempotent on replay.

**Recommended fix**: Add one clause to FR-148/INV-6 asserting the intent record is self-contained, the plan-record patch is committed-phase-only (or rollback-reverted), and replay is idempotent.

---

### m-3 — MINOR — Incompleteness — Boot-sweep exemption for the awaiting-correction owner session has no stated plan↔owner-session linkage

**Lens**: Incompleteness (focus area a). **Sections**: FR-118(b) (L1419), FR-147 (L1455), FR-193 (L1486), INV-9 (L521), durable session record (L554).

The exemption condition — "a `paused` **plan-owner session** whose plan is durably `awaiting_owner_correction`" — is a reverse lookup, but the field-level shapes do not name the linkage that makes it decidable at boot. The durable session record's `owner_scope` for a top-level plan-owner session resolves to `human`/chat-principal (L554), not `plan_id` (that is the *members'* owner_scope), so `owner_scope` alone cannot identify the owner session's plan. FR-140 (persistent owner session per plan) implies such a link must exist, but the spec — whose job #3 is to give field-level shape to every wire type — never states it (no `plan.owner_session_id`, and no requirement that the owner session carry `goal_ref` → goal with `binding=plan_id`).

**Failure scenario**: An implementer who resolves the exemption only via `owner_scope` finds the plan-owner session's scope is `human`, cannot match it to an `awaiting_owner_correction` plan, and sweeps it to `failed(interrupted)` — re-introducing CRIT-1 (the wedge) for exactly the C1 case.

**Recommended fix**: Name the linkage field explicitly — e.g. add `plan.owner_session_id` to the plan extensions, or require the plan-owner session's `goal_ref` to reference the plan-DoD goal (`binding=plan_id`) — and state that the boot sweep resolves the exemption by enumerating `awaiting_owner_correction` plans → their owner sessions.

---

### m-4 — MINOR — Incompleteness — `unable_to_verify` re-run has no bound and undefined round accounting (permanent-block livelock)

**Lens**: Incompleteness. **Sections**: FR-116 (L1412), FR-137 (L1413), classifier examples (L704-706), INV-1 (L513).

`unable_to_verify` → "re-run, **NEVER** scored". For a *permanently* unverifiable mechanism (e.g. a tool made out-of-policy after compile, or a command whose exit code is structurally unreadable), nothing terminates the loop: the criterion is never scored, so an AND-combine adjudication that includes it can never produce a final verdict. It is unspecified whether such an adjudication still consumes a round (INV-1 says an adjudication consumes exactly one round — but an all-`unable_to_verify` adjudication produces no verdict). If it consumes a round, rounds eventually exhaust (bounded, acceptable); if it does not, the goal stalls indefinitely with no terminal.

Mitigation: FR-111's compile gate rejects out-of-policy tools, so a *permanent* block is a narrow post-compile-policy-change edge — hence MINOR, not MAJOR.

**Recommended fix**: State a re-run bound for `unable_to_verify` (e.g. after K consecutive un-runnable adjudications, escalate-once and treat as a terminal `failed(unable_to_verify)` or fold into round accounting), and state explicitly whether an all-`unable_to_verify` adjudication consumes a round.

---

### m-5 — MINOR — Inconsistency — `SessionMessage.depth ≤ 5` cap does not track the delegation-depth backstop (3)

**Lens**: Inconsistency. **Sections**: envelope `depth` (L531), caps (L550), FR-135 (L1442, "default = delegation depth 3"), ADR D6 (backstop 3).

The newly-added `depth` field is hard-capped at `≤5` (schema-validated). Delegation depth is a *configurable* value with a shipped backstop of 3 (D6). The two are unlinked: an operator who raises delegation depth above 5 would have legitimate messages fail schema validation at depth 6, while the default (3) leaves the cap loosely over-provisioned. Not a live defect at defaults, but the constant should be derived from / consistent with the delegation-depth config rather than an independent literal.

**Recommended fix**: Tie the `depth` schema bound to the configured delegation depth (or document that `5` is the hard ceiling and delegation depth may not exceed it).

---

### m-6 — MINOR — Inconsistency — `session_messaging` key-count figures disagree and undercount

**Lens**: Inconsistency. **Sections**: US-15 (L331, "~14 keys"), FR-195 (L1497, "~15 keys" then enumerates ~20).

US-15 says "~14 keys"; FR-195 says "~15 keys" then lists roughly twenty (enabled, wake_enabled, adjudication_enabled, child_send_rate/body/depth, inbox_unacked_max/per_type_ceiling, steer_rate/steer_body, cancel_grace/needs_input_ttl, wake_debounce/wake_max_per_hour, idle_quiet_window, token_budget, attempts_max/judge_rounds_max, message_retention/audit_retention). The `adjudication_enabled` key folded in this revision (L1716) likely bumped the count without US-15 being updated.

**Recommended fix**: Make the two figures agree and match the enumerated list (or drop the count and reference the canonical enumeration in FR-195).

---

### OBS-1 — OBSERVATION — `revision_entry` (`direction=engine`) is not slotted into the ack-vs-event taxonomy

L550 partitions SessionMessages into acked inbox messages vs un-acked ring-buffered events, but `revision_entry` (engine-emitted, also committed transactionally with the tail) is neither a child→parent inbox message nor a `session.*`/`tool.*` fan-out event. Consider stating explicitly that engine-direction control kinds are exempt from ack/ceiling semantics.

### OBS-2 — OBSERVATION — Round-budget of a re-statement's new goal generation is unstated

FR-138's remediation mints "a new goal generation with the mis-compiled criterion corrected". Whether that new generation resets `judge_rounds`/`attempts` (analogous to Play's "JudgeRounds 0", L595) is not stated. Harmless if reset is intended; worth one sentence for parity with the Play/`resumed_from` path.

---

## Structural Integrity (plan-spec checks)

| Check | Result |
|-------|--------|
| Every user story has acceptance scenarios | PASS (US-1..US-17) |
| Every acceptance scenario has ≥1 BDD scenario | PASS |
| Every BDD scenario has `Traces to:` | PASS |
| Every behavioral FR in traceability matrix with a test | PASS (FR-158/FR-177 correctly excluded as config/non-behavioral, L1611) |
| Success criteria measurable | PASS (SC-001..SC-015 quantified; SC-008 corrected per M5) |
| Regression impact addressed | PASS (§Regression, L1358-1381) |
| Internal cross-references consistent | **FAIL** — FR-185/FR-186/L320/L1124/L1702 vs crosswalk L94/L585/L590 (M-1) |
| Enum-count invariants hold | PASS — S2 lifecycle exactly 8 (L1717); pill exactly 8; crosswalk complete both directions |

## Test-Coverage Assessment

Coverage is strong: 61 ordered tests spanning unit/integration/E2E, every G-1..G-16 and every §9.1 conformance diagram mapped, negative/boundary datasets present (budget race row 4, message caps, write-set disjointness, lifecycle transitions incl. the C1 boot-sweep-exempt rows 8). Gaps flow from the findings above: no test asserts the pill re-planning state **survives restart from `plan_phase`** (M-1 — `TestBootSweep_AwaitingCorrectionDurable` covers the engine no-re-judge but not the pill reconstruction); no test for intent forward-replay content-completeness (m-2 — Test 54 asserts replay-forward happens but not that member bodies survive); no permanent-`unable_to_verify` bound test (m-4).

---

## Unasked Questions

1. After restart, which component recomputes the `re-planning` pill from durable `plan_phase`, and is there a test that the pill (not just the engine's no-re-judge) reconstructs? (M-1)
2. Does the tail-append intent record carry full member bodies, and is the plan-record patch applied pre- or post-commit? (m-2)
3. What field links a plan to its owner session so the boot sweep can identify the exemption? (m-3)
4. Is a permanently-`unable_to_verify` criterion bounded, and does its adjudication burn a round? (m-4)
5. Does a re-statement's new goal generation reset the round/attempt budgets? (OBS-2)

---

## Verdict

**REVISE** — C1 and M1–M8 are resolved; one MAJOR contradiction (M-1) remains where the C1 "durable re-planning" fix was not propagated from the crosswalk into FR-185/FR-186 (and two BDDs + a Clarification), plus five MINOR/observation items. Fix M-1 (and ideally m-2/m-3, which harden the two focus-area mechanisms) and re-grill; the remaining minors can ride the same revision.

To address these findings, run:
  `/plan-spec --revise docs/internal/specs/unified-goal-plan-subagent-spec.md docs/internal/specs/unified-goal-plan-subagent-spec-review.md`

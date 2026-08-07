# Grill Review — Planning & Goals Spec (Round 3, FINAL)

**Spec**: `docs/internal/specs/planning-goals-spec.md`
**Mode**: plan-spec (three-Part composite)
**Round**: 3 of 3 (operator cap). Verifies closure of round-2 F1–F7 / O1–O2 and screens for new contradictions.

---

## Executive Summary

Round 2's fixes are **mostly** genuine: F3 (cap counted-set), F4 (`plan_status` rename — verified 0 `plan_status_changed`), F5 (attempt counter on `Task.attempt_count`), F6 (struct fields), and O2 (approval-only act) are cleanly closed. **However, two round-2 fixes left/introduced internal contradictions that survive into the final text:** F1 (`failed` terminal) was not propagated to SD-A2, which still calls `failed` "retryable/re-queuable"; and the F7 fix introduced a duplicate test-order numbering collision that makes FR-077's traceability ambiguous. Two further MINOR consistency defects (R1↔SD-C6 unknown-fallback; O2↔R5 cap interaction) remain.

**Findings**: 0 CRITICAL, 2 MAJOR, 2 MINOR, 3 OBSERVATION.
**Verdict**: **REVISE** — no design flaws or CRITICAL risks; all residuals are localized text edits (no rework), but two are genuine section-to-section contradictions that must be corrected before waves start.

---

## Findings

| ID | Sev | Lens | Section | Summary | Fix |
|----|-----|------|---------|---------|-----|
| F1R | MAJOR | Inconsistency | SD-A2 (L967) vs state table (L265), BDD outline (L589–590), SD-B5 (L1913) | **F1 not fully closed.** SD-A2 still summarizes the matrix as "failed retryable" and says it "mirrors the task lifecycle policy (`done` frozen, `failed` re-queuable)". Every other location makes plan-`failed` terminal/frozen (F1 r2). SD-A2 is the authoritative spec-decision that *rationalizes* the transition matrix, so it now contradicts the very table it names. | Rewrite SD-A2: parenthetical → "…done/failed; **done AND failed frozen/terminal**"; delete "`failed` re-queuable" and state plan-`failed` is terminal (F1 r2: not retried — author a new plan), noting plan diverges from the task-`failed` re-queue precedent by design. |
| F7R | MAJOR | Inconsistency / Traceability | Part B Test Order (L1635–1638), Traceability (L1901) | **F7 fix introduced a numbering collision.** The two inserted tests are numbered **35** (`TestRoleGating_SessionOwnership`) and **36** (`TestTaskExecutor_ScratchpadExemptFromGoalLoop`), but the pre-existing SEC-26 tests were **not** renumbered — so rows **35 and 36 each appear twice**. The traceability row `FR-077 → 35, 36` (L1901) now references bare numbers that each map to two different tests. (FR-074 and FR-048 disambiguate by naming their test; FR-077 does not.) | Renumber: keep 35/36 for the two new tests, shift the SEC-26 pair + everything after (37→…) down by 2, cascade the traceability numbers; and change FR-077's cell to name the tests: `TestSEC26_TypeSystemAgent_IsRateLimited`, `TestSEC26_IsPrivilegedAgent_CoreOnly`. |
| C6R | MINOR | Inconsistency | R1 (L2746) vs SD-C6 (L2649), dataset row 6 (L2476), SC-042 (L2602), test 1 (L2411) | R1 states "The SD-C6 'unknown → draft' fallback is **removed** (enum closed and total)," but SD-C6, badge dataset row 6, SC-042 ("6/6 dataset rows pass" incl. unknown-fallback), and `planStateColors.test.ts` all **retain and require** the fallback. An implementer cannot satisfy both (remove ⇒ SC-042 row 6 fails; keep ⇒ R1 violated). | Keep the defensive fallback (harmless, mirrors `statusColors.ts:83`) and soften R1 to: "the wire enum is closed; the SPA retains a defensive unknown→draft fallback." OR delete dataset row 6 + the SC-042 unknown clause + test-1 unknown case. Pick one; make all four agree. |
| O2R | MINOR | Incompleteness | R1 approve gating (L2747) vs R5 cap (L2784) | O2 calls `approved` "a **brief** transitional state" auto-advanced to `running` by the engine on its next tick. But R5 rejects a start when 16 loops are active. If the cap is full when the engine ticks, the `approved→running` admission is rejected and the plan is stuck in `approved` **indefinitely** — not "brief." Spec is silent on retry / user-surfacing / whether `approved` is a legitimate resting state. | Add: a plan blocked by the global cap remains in `approved` (a legitimate waiting state); the single engine retries admission each tick and starts it when a slot frees; the UI surfaces "waiting for a loop slot." |
| O1R | OBS | Incompleteness | R1 (L2746) vs Part C FRs/tests | O1's authorized secondary `failed_reason` chip ("renders a secondary chip from failed_reason") has **no** Part C FR (FR-082 only names "a PlanState badge") and **no** badge-dataset row (tests only the 5 states). Untraced nicety. | Add an FR-082 clause and a badge-dataset row for the `failed_reason` secondary chip, or explicitly mark it optional/out-of-test. |
| PBT | OBS | Inconsistency | Part B transitions outline (L1406) + dataset (L1754) | Illustrative Part B tables show `draft→active` directly, omitting the `approved` intermediate that R1/SD-B5 now mandate ("draft → approved → running"). R1 is authoritative so behaviour is defined, but the illustrations are stale and could mislead. | Insert `approved` into the two Part B illustrative rows (or add a note that `active` = post-`approved` `running`). |
| B4G | OBS | Ambiguity | SD-B4 (L1912) | SD-B4 still contains the garbled clause "counts as… **it does not**." R5 (L2784) explicitly supersedes it, so behaviour is defined, but the dangling sentence is sloppy for the source-of-truth register. | Rewrite SD-B4's standalone-task clause cleanly (or delete it and point to R5). |

---

## Round-2 Closure Verification (F1–F7, O1–O2)

| Item | Claimed fix | Verified? |
|------|-------------|-----------|
| **F1** — `failed` terminal/frozen | State table, BDD outline, SD-B5 all terminal | **PARTIAL** — SD-A2 (L967) still "retryable/re-queuable" → **F1R (MAJOR)** |
| **F2** — tiered approve DoD | R1 L2747 + SD-A7 L970 tiered; member-task ≥1 unconditional gate consistent with US-10 AS-5 + cross-cutting error flow | **CLOSED** |
| **F3** — cap counted-set = {running plans + active /goal + enabled /loop}; standalone task loops excluded | R5 L2784, FR-076, SC-033 all agree; SD-B4 garbled but superseded (see B4G) | **CLOSED** |
| **F4** — `plan_status` rename | grep: **0** `plan_status_changed`; `task_status_changed` intact (6) | **CLOSED** |
| **F5** — attempt counter on `Task.attempt_count` (C17) | SD-C12 L2655, FR-061 L1812, Ambiguity #1 all cite C17 not UnifiedMeta | **CLOSED** |
| **F6** — Plan gains plan_phase/failed_reason/progress; Task gains attempt_count/max_attempts | Struct L233–235, L276–277 present | **CLOSED** |
| **F7** — FR-048/FR-074/FR-090 test rows added | Rows added, but **duplicate numbering 35/36** introduced; FR-077 traceability now ambiguous | **PARTIAL** — **F7R (MAJOR)** |
| **O1** — failed_reason secondary chip authorized | R1 L2746 authorizes it | **CLOSED (auth), UNTRACED** — see O1R (OBS) |
| **O2** — approved→running engine-auto, approval the only explicit act | R1 L2747 | **CLOSED**, but interacts with cap → O2R (MINOR) |

Round-2 design decisions (failed terminal, tiered DoD, cap set) are internally consistent **except** for the two residual contradictions above (SD-A2 stale text; unknown-fallback wording).

---

## Structural Integrity (plan-spec checks)

- Per-Part traceability matrices present (A: R7; B: L1867; C: L2615). **Defect**: Part B FR-077 → bare "35, 36" is ambiguous under the duplicate numbering (F7R).
- Every FR appears in a matrix row; every BDD scenario has a `Traces to`. ✓
- Test datasets cover boundaries/edge/error. ✓
- Regression impact explicitly addressed per Part. ✓
- Success criteria measurable. ✓ (one internal conflict: SC-042 requires the unknown-fallback that R1 says is removed — C6R.)

## Other lenses (no new material findings)

- **Security/STRIDE**: origin gating (R6 fail-closed `UserInitiated`), evidence redaction-before-truncate (SD-A13), fail-closed judge, SEC-26 privilege narrowing, cross-agent check confirmation by identity (R6 m7) — all coherent and unchanged since r2. No new gap.
- **Overcomplexity**: no new abstractions introduced this round.
- **Infeasibility / Ambiguity / Incorrectness**: no new issues beyond those tabled.

---

## Verdict

**REVISE.** Fix F1R and F7R (both MAJOR, both single-location text edits), then reconcile C6R and O2R (MINOR). No design changes required; a quick spec-edit pass suffices before wave implementation. Given the 3-round cap, these are correction-level, not re-plan-level.

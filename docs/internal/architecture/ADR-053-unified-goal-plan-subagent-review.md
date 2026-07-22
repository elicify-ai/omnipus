# Grill Review — ADR-053: Unified goal / plan / subagent system (RE-GRILL, 2nd pass)

- **Input:** `docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md`
- **Authoritative design:** `docs/internal/design/unified-goal-plan-subagent-target-design-v2.2.html`
- **Mode:** generic-markdown (ADR ratification) — re-grill after revision.
- **This pass:** verify the six findings from the first grill (F-1..F-6, all recorded below) are resolved, and confirm §6.1 (spike GO resolution) and the §9 confidence updates are internally consistent. Locked decisions D1–D17 are NOT re-opened.
- **Prior verdict:** REVISE (0 CRITICAL · 3 MAJOR · 3 MINOR · 1 OBSERVATION).

---

## Executive Summary

The revision resolves all six carried findings. The three MAJORs (F-1 D6 backstop / §8 #7 mis-flag, F-2 ADR-049 under-scoped supersession, F-3 D12 budget sub-gaps) and the three MINORs (F-4 §5.2 header polarity, F-5 two-enum crosswalk, F-6 FR-5 cardinality) are each addressed in-text, and the fixes are internally consistent with §6.1 and §9. The one load-bearing code fact the fixes hinge on — the shipped delegation-depth backstop of 3 — is verified real and accurately cited (`pkg/agent/subturn.go:28` `defaultMaxSubTurnDepth = 3`, call site `:56`). No decision was re-opened or silently weakened by the revision. One residual consistency nit remains (OBSERVATION-level): the D3/D10/D13/D17 decision-row confidence cells still read a bare "Med" post-GO, reconciled only by the adjacent cross-group note and §9 — cosmetic, not misleading.

**Findings (this pass):** 0 CRITICAL · 0 MAJOR · 0 MINOR · 1 OBSERVATION.

**Verdict: PASS.**

---

## Resolution verification — F-1 .. F-6

| ID | Prior sev | Status | Evidence in revised ADR |
|----|-----------|--------|-------------------------|
| **F-1** | MAJOR | **RESOLVED** | D6 row (§4, Group D) now records *"delegation depth is configurable with a shipped backstop of 3 (`defaultMaxSubTurnDepth = 3`, `resolveEffectiveDelegationDepth` — `pkg/agent/subturn.go:28,56`)"* with rationale *"the default already exists in code."* §8 #7 rewritten: *"The default depth is not a gap — it is a shipped backstop of 3"*, keeping ONLY the still-valid latency concern (a question N hops deep reaches the human after N parent decisions; consider a direct-escalate shortcut). Code fact verified: `subturn.go:28 defaultMaxSubTurnDepth = 3`, `:56` = the `resolveEffectiveDelegationDepth(nil, cfg.MaxDepth)` call site; covered by `TestSpawnSubTurn_HonorsExplicitPerEdgeDepthOverDefaultBackstop`. The false "no default" premise is gone. |
| **F-2** | MAJOR | **RESOLVED** | Supersede header (line 7) broadened to name **D4**, **D7's round accounting** (round = one adjudication, not one turn), and **FR-5/D6's after-every-turn cadence + one-`/goal`-per-session** limit. §5.1 rewritten as *three* numbered supersessions (D4 owner-wake / D7 round accounting / FR-5·D6 cadence & cardinality), each stating the new semantics and deferring the `judge_rounds_max` reconciliation to `/plan-spec`. The prior "everything else stands" blanket is now a scoped enumeration (dispatch engine, boot reconciliation, overlap guard, 7-day sweeper) that excludes the superseded items. §8 #9 (round-accounting reconciliation) added, incl. in-flight round-count migration. |
| **F-3** | MAJOR | **RESOLVED** | §8 #3 expanded from 2 to **five** sub-gaps: (a) default value / unset-means-unbounded, (b) token≠cost across providers, (c) **brake timing** (hard-fail mid-turn vs ADR-049 NFR-3 graceful wind-down), (d) **atomic debit under concurrency** (owner+members+verifiers+Judge share one pool), (e) **no live brake** (`token_budget` restart-gated; `session_messaging.enabled` neuters messaging, not token burn). §8 Negative/tradeoffs and the STRIDE row also reflect the token-posture shift. |
| **F-4** | MINOR | **RESOLVED** | §5.2 header changed to *"add a NEW **inbound** 'Superseded in part by' header"* with an explicit polarity note: *"ADR-052 today carries only an outbound 'Supersedes (in part):' header … add a new header line, not append under the outbound one."* |
| **F-5** | MINOR | **RESOLVED** | §8 #10 added: the two distinct 8-state enums (S2 session-lifecycle vs D14 pill-state) require a specified crosswalk — which lifecycle state renders as which pill state, where `judge_unavailable`/`re-planning`/`judging` (no lifecycle counterpart) are sourced, and confirmation they are deliberately separate (DoD-11 anti-drift), not an accidental duplicate. |
| **F-6** | MINOR | **RESOLVED** | §5.1 note 3 supersedes FR-5's *"one active `/goal` per session"* (→ claim-or-idle per goal-id, multiple concurrent goals). §8 #11 added: multi-goal-per-session cardinality must define per-goal-id isolation and reconcile with ADR-049's global-cap accounting (does each goal consume a cap slot?). FR-5's status is now explicit. |

## §6.1 + §9 consistency check (per invocation)

- **§6.1 spike GO** is internally coherent: §6 states the gate + Go/No-go branches; §6.1 records **GO** (+4.40 MiB raw / +3.04 MiB stripped, no cgo) with three caveats (media size-guard, no `worktree add` / ff-only merge → shard/subdir joins, D17 needs a Landlock/bash `.git/` block as a security-lead Phase-1 dependency) and the OBS-3 NOTICE requirement. The §4 cross-group note (*"the spike has now resolved GO … residual is caveat-driven implementation detail"*) and §8 #4 (no-go degraded-contract residual retained as a defensive fallback) are consistent with GO.
- **§9 confidence** partitions all 17 decisions with no overlap or omission: 12 High (D1,D2,D4,D5,D6,D7,D8,D11,D12,D14,D15,D16) + 4 Med→High git-family (D3,D10,D13,D17, raised by the resolved spike) + 1 Med (D9). The four CONFIDENCE blocks match the roll-up. No decision is Low; none re-opened.

## Faithfulness — did the revision weaken or re-open any D1–D17?

No. The revision is additive record-correction only: it adds the missing supersession notes and residuals, corrects §8 #7 and §5.2 polarity, and annotates the D6 backstop already present in code. No decision text was softened; the D9 Med and git-family Med→High postures are unchanged in substance.

## Residual (OBSERVATION — non-blocking)

| ID | Sev | Section | Note |
|----|-----|---------|------|
| R-1 | OBSERVATION | §4 Groups B/C/E rows; §9 | The D3/D10/D13/D17 decision-row **confidence cells still literally read "Med"**, and D3's rationale (*"the mechanism branches on the spike"*) reads present-tense-contingent, though §6.1 has resolved the spike GO. The immediately-following cross-group note and §9 reconcile this to "Med→High", so no reader is misled — but annotating the four cells "Med→High (§6.1)" would remove the stale-at-a-glance read. Cosmetic; does not affect the verdict. |

Carried from the first pass, F-7 (glossary pinning "round", the two 8-state enums, and "goal-bearing scope") remains a sound `/plan-spec` opener and is now partially anchored by §8 #9/#10 — still an OBSERVATION, not a blocker.

---

**Verdict: PASS.** All three MAJOR and all three MINOR findings from the first grill are resolved; §6.1 and §9 are internally consistent; no D1–D17 decision was re-opened or weakened. The sole residual is a cosmetic confidence-cell annotation (R-1, OBSERVATION). The §8 residual set is the complete gap list a `/plan-spec` must close. Per delivery-brief DoD-1, the ADR status may flip Proposed → Accepted.

Next step:

```
Verdict: PASS

The ADR is ratified-ready. Proceed:
  /plan-spec docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md
Open §8 with a glossary pinning "round" (adjudication vs turn), the two 8-state enums, and "goal-bearing scope" (F-7 / R-1).
```

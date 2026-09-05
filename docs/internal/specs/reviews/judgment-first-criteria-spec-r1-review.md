# judgment-first-criteria-spec — grill review, round 1

- **Reviewed:** `docs/internal/specs/judgment-first-criteria-spec.md` Draft v1 (2026-09-05).
- **Reviewer:** grill-spec methodology (plan-spec mode detected), grounded against ADR-074 r3 and the working tree.
- **Verdict:** **REVISE** — 0 CRITICAL / 14 MAJOR / 8 MINOR / 2 OBSERVATION.
- **Disposition:** every finding addressed in Draft v2 (same day; F-nn markers in the spec cite this file).

## Executive summary

The spec is faithful to ADR-074 r3 on almost every transcribed decision — every code citation checked (tool-schema enums, gate ordering, `buildJudgeUserContent` sections, `formatGoalEcho` dead code, `core.go` seams, `queued` filter, contract shapes) is accurate. The problems concentrate in what the spec adds or compresses: SC-003 contradicts FR-007 and the clarifying-question flow outright; the clarifying-question mechanism is unspecifiable as written against the ADR's one-shot compile call; the fallback path's activation semantics are ambiguous in exactly the direction that would quietly reintroduce unconfirmed prose-goal activation; ADR-mandated surfaces (D5.4 confirmation flow, ADR required-test #5) are silently dropped; and test 21 is self-admittedly "holdout-adjacent" to H-1, compromising the holdout set.

## Structural integrity (plan-spec checks)

Every story has scenarios (PASS); Given/When/Then form (PASS); `Traces to:` back-references absent (FAIL); scenario→test coverage gaps: US-3 S2, US-5 S4, US-6 S3 partial, EC-2/4/5/7 (FAIL); FRs all matrixed (PASS); matrix rows FR-001/006/010/012 defective (FAIL); no test-dataset section (FAIL); regression list accurate (PASS); SC-003 internally contradictory (FAIL).

## Findings (numbered as cited by the spec's F-nn markers)

1. **MAJOR — SC-003 vs FR-007 vs EC-3.** "≤ 1 LLM call" is unachievable alongside exactly-one-repair (2 calls) and clarification resumes. Fix: per-phase budget.
2. **MAJOR — clarifying-question mechanism unspecified.** Needs a `oneOf {criteria[], clarifying_question}` response schema, a persisted pending-clarification record, handling for confirm-words/unrelated replies (which `confirmGoalAliases` would swallow), and a round bound.
3. **MAJOR — post-fallback activation ambiguous.** The worse reading (immediate activation) silently reintroduces unconfirmed prose-goal activation. Fix: every prose path ends pending+echo+confirm.
4. **MAJOR — "the goal still sets" false for a subset.** The deterministic fallback can itself reject (D9 gate, hedging veto, no-criteria); H-4 is exactly such an input and contradicts §3. Fix: rejection is legitimate; silent/unexplained failure is the prohibited outcome.
5. **MAJOR — mixed marker+prose goals untested.** "Markers never reinterpreted" through the LLM path is the trickiest D4a invariant; needs a byte-identity test.
6. **MAJOR — amendment path unspecified.** Prose restatement over an active goal takes `proposeGoalAmendment` (deterministic); decide whether the LLM rewrite applies. (v2 decided: deterministic this phase, tracked follow-up.)
7. **MAJOR — LLM-authored technical payloads unaddressed.** If the compiler may mint `check` commands, `/goal` becomes an indirect command-authoring channel steerable by pasted untrusted content. Fix: prose-only compiler output and/or a verbatim-display-before-activation invariant. (v2 adopted both: INV-1.)
8. **MAJOR — ADR required-test #5 dropped.** The per-tool behavior round-trip was decomposed away; `create_plan`/`plan_correct` route into different downstream validation. Restore.
9. **MAJOR — scope vs D5.4/D6/FR-001.** D5.4 (`CriteriaBreakdown` confirmation) had no story/FR/test; D6's calibration metrics had no instrumentation; REST update inference untested. 
10. **MAJOR — `Goal.yaml` ignored in §9.2.** The breakdown must `$ref AcceptanceCriterion` (no third criteria wire shape); `GoalStatusFrame`'s canonical copy is inline in asyncapi.yaml, hand-synced — a two-place edit.
11. **MAJOR — datasets and matrix.** No dataset section; EC-2/4/5/7 untested; US-5 S4 stranded; US-6 S3's frame emission unasserted; FR-012 row wrong.
12. **MAJOR — holdout integrity.** Test 21 was functionally H-1. Fix: stub the dev-loop e2e; reserve the real-LLM run for H-1.
13. **MAJOR — invisible fallback.** No log/counter/marker distinguishes a skill-governed compile from a fallback — the ADR-061 "undetectable fallback" anti-pattern verbatim. Fix: WARN + counter + echo note.
14. **MINOR — pending lifecycle/`queued` semantics.** Cap slot (double-Admit), expiry, and state-enum choice unpinned. (v2's `waiting_on_user` choice was itself overturned in round 2 — see the r2 review.)
15. **MINOR — `extraContext` position unspecified** (currently leads; the order guard would freeze an undecided choice).
16. **MINOR — "skill-loaded" mechanism ambiguous** (allowlist-resolved vs engine-injected; v2 pinned engine-injected).
17. **MINOR — truncation unit unspecified** (bytes vs runes; rune-safe ≤500 code points adopted).
18. **MINOR — `[kind]`-label prohibition untested** (echo negative assertion added).
19. **MINOR — untestable clauses:** FR-002's uniqueness unverified (and missing the `normalizeCriteria` call site); FR-010's re-emission MUST binds a deferred surface; FR-013 is process. 
20. **MINOR — migration marker mechanics:** atomicity of marker+append unstated; wire-visibility of `skills_migrations` unconsidered. (Round 2 subsequently proved the "internal-only" fix itself wrong against `GET /api/v1/config` — see the r2 review.)
21. **MINOR — quote emission side unowned** (who makes the Judge emit it; v2's answer was corrected again in round 2 — the rubric was an uncommitted working-tree edit).
22. **OBSERVATION** — test 19 needs no E2E; §9.2's frame-vs-field punt decidable now.
23. **OBSERVATION** — confirm vocabulary worth citing (`confirmGoalAliases`).

## Verdict

REVISE. The spec does not contradict ADR-074 r3 on any transcribed decision, but drops two ADR obligations, contains one hard internal contradiction, and leaves the clarifying-question and fallback-activation semantics ambiguous enough that two competent engineers would ship different `/goal` behavior.

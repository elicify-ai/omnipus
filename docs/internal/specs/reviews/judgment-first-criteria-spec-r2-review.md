# judgment-first-criteria-spec — grill review, round 2

- **Reviewed:** `docs/internal/specs/judgment-first-criteria-spec.md` Draft v2 (2026-09-05).
- **Reviewer:** grill-spec methodology, round-2 mode: verify v2's corrections against ADR-074 r3 and real code; find new defects in v2's additions; re-run structural checks; ask what's still undiscussed.
- **Verdict:** **REVISE** — 0 CRITICAL / 8 MAJOR / 7 MINOR / 2 OBSERVATION.
- **Disposition:** every finding addressed in Draft v3 (same day; R2-nn markers in the spec cite this file).

## Corrections verified REAL

`confirmGoalAliases` exact (`goal_compile.go:516-518`); `confirmPendingGoal` exists and re-Admits (`goal_loop.go:240`, `:280-286`), and "check before compile" is correctly framed as a change (today compile at `:102` precedes Admit at `:122`); `extraContext` genuinely leads `buildJudgeUserContent` (`judge.go:892-896`) and exactly three back-reference sites exist; pointer semantics/1000-rune bound/min>max ground DS-3/4/EC-2; the `GoalStatusFrame` inline-canonical + hand-sync obligation is real (its own header, asyncapi.yaml:1005); `Goal.yaml` carries `criteria: AcceptanceCriterion[]` with the DoD-11 note; `CriterionVerdict.yaml` is `additionalProperties: false` with no quote field; the `default:` codegen trap is documented where cited; update-request criteria fields exist; the gate exists on exactly the two task tools; all `core.go` seams at the cited locations; `SeedDefaults` copies missing dirs; `goalIdleExpirySweep` exists; nil-`agentInst` path grounded; `waiting_on_user` exists in the enum — but see R2-02/03.

## Findings

- **R2-01 MAJOR — the "2026-09-03 rubric" is an uncommitted working-tree edit, not a shipped fact.** `git log -S 'evidence_quote'` returns nothing on any ref; the quote-before-verdict rubric and its `{id, evidence_quote, met, reason}` response shape exist only in the dirty diff to `pkg/coreagent/core.go` (which also rewrites `PlanSupervisorDefaultRubric`); HEAD's rubric declares `{met, reason}` only. FR-010's parse half would wire to a rubric that never emits a quote, with no failing test. Fix: bring the rubric edit into delivery scope with a rollout slot before/with the parse half; drop the fabricated-precision date.
- **R2-02 MAJOR — the `waiting_on_user` pill choice contradicts ADR-074 r3** (which the spec declares authoritative and which explicitly gives the dead `queued` filter "its first real occupant"). A silent spec re-decision. Fix: revert to `queued` (amending its description) or obtain an ADR amendment.
- **R2-03 MAJOR — `waiting_on_user` already has a production emitter with different semantics:** the G-5 typed mid-run pause of an ACTIVE goal (`goal_loop.go:618-626`; enum description `GoalStatusFrame.yaml:103-105`). A card filter on it would match both a pending confirm and an active pause; no discriminator was specified. (Dissolves if `queued` is chosen — it was; the negative test is retained.)
- **R2-04 MAJOR — "`skills_migrations` never crosses the wire" is false as specified.** `GET /api/v1/config` marshals the entire config struct with only credential-shaped redaction (`rest.go:4180-4199`), and config-persistence JSON tags can't exclude the field. Fix: strip it from the response (with test), persist outside config, or give it a contract entry. (v3 chose: strip + test.)
- **R2-05 MAJOR — INV-1's display clause contradicted by the pinned marker-only path:** shorthand markers mint payloads the user never typed (`[tests]` → `go test ./...`, `goal_compile.go:213-217`) and marker-only goals activate with no confirmation surface. Fix: scope the clause to the LLM path or exempt user-typed markers with rationale. (v3: scoped + rationale.)
- **R2-06 MAJOR — confirm/answer routing unspecified.** Today confirm-words match only as `/goal <alias>` (`isGoalConfirmVerb`, `goal_loop.go:88`); bare "yes" is ordinary chat. The state machine presupposes interception of ordinary messages; unanswered: command-form vs bare-form confirm; what a bare non-confirm reply does during pending-confirm; `/goal confirm` during pending-clarification (would hit "No pending goal to confirm"). Fix: specify the interception point and full reply taxonomy. (v3: US-3 S9 + DS-8.)
- **R2-07 MAJOR — US-4 S5 and S6 have no test** (engine-side injection for curated-allowlist agents; marker wire posture); matrix claims coverage it lacks.
- **R2-08 MAJOR — FR-014's second counter has no observable event** (skill-load knowledge is what the ADR itself declared unverifiable), and its matrix row cites a test that asserts a description string, not a counter. Fix: define the seam or narrow to the fallback counter. (v3: narrowed.)
- **R2-09 MINOR** — pending states exempt from every brake (`goalIdleExpirySweep` skips `GoalCondition == ""`, `goal_loop.go:778`): no expiry, stale pill. **R2-10 MINOR** — `/goal clear`/restate vs the clarification record unspecified (`hadGoal`, `:368`). **R2-11 MINOR** — `/goal` status says "No active goal" during pending (`goalStatusReply`, `:317`). **R2-12 MINOR** — FR-002's call-site set vs ADR D2's "gateway converting via InferCriterionKind" — pin which. **R2-13 MINOR** — DS-3 lacks the 0-rune row (`criterion.go:270-272`); pin Input `minLength`. **R2-14 MINOR** — counters' exposure surface unnamed. **R2-15 MINOR** — `judge.go:973-974`'s parser-side contract comment stale vs the (uncommitted) rubric.
- **R2-16 OBS** — round-1 review not committed; F-nn markers unauditable. (Fixed: this reviews/ directory.) **R2-17 OBS** — pending-pill restart survival unspecified; boot-sweep reconstruction precedent exists (`GoalStatusFrame.yaml:108-111`).

## Unasked questions (v3 dispositions)

1. LLM-compiled prose criteria land in the Judge's non-UNTRUSTED criteria section — confirm-display + Judge skepticism accepted as the mitigation, now stated (INV-2). 2. Double-send during in-flight compile — turn serialization, stated. 3. Compile cost in `/goal` status — not surfaced this phase, noted. 4. Repair budget after a clarification resume — own single repair; episode ≤ 4 calls, pinned.

## Structural integrity

Every story ≥1 scenario PASS; scenario→test FAIL (US-4 S5/S6 — fixed in v3); back-references PASS; FRs matrixed PASS; matrix validity FAIL on FR-014's row (fixed); datasets PARTIAL (0-rune missing — fixed); regression list PASS (named files exist); measurable SCs PASS; holdout independence PASS (test 21 stubbed; H-8 covers D5.4).

## Verdict

REVISE — defects concentrated in four v2 corrections and the new state machine; the v2 structure is otherwise sound. A light round 3 after revision was recommended (optional).

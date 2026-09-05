# ADR-074 — adversarial review, round 1

- **Reviewed:** `docs/internal/architecture/ADR-074-judgment-first-criteria.md` at revision 1 (2026-09-05).
- **Reviewer:** architect subagent (adversarial "grill" round), grounded against the working tree at `release/v0.1.1`.
- **Verdict:** **REVISE** — 5 CRITICAL / 10 MAJOR / 7 MINOR / 4 OBSERVATION.
- **Disposition:** every required revision incorporated in ADR-074 revision 2 (same day). This file is the audit record; findings are summarized at full technical fidelity, evidence citations preserved.

## Executive summary

Three of the ADR's factual claims about the current UI are exactly right (the criteria editor defaults to `check` and cannot mint `behavior`; the Judge's per-criterion reason renders at 10px muted; `GoalStatusFrame` carries no criteria). Almost everything the ADR says about the *engine* is wrong or unverified. The headline decision D4 — "one skill governs every creation path" — is wired to a seam that does not exist (the `/goal` "compilation turn" contains no LLM at all) and to an allowlist function that governs the two agents which never author criteria. D2's `kind`-optional loosening contains a self-contradiction whose two readings differ on whether a security gate fires, and in one reading silently disarms the ADR-049 D2-rule-5 bash-policy check. D3 names two of four drift sites and is schema-only against parsers that physically discard the `behavior` payload. The design doc's own ship test is not answered by any decision, because the binding constraint is evidence assembly, not authoring friction.

## CRITICAL

- **CRIT-001 — D4(a) wired to a nonexistent seam.** `compileGoalIntent` (`pkg/agent/goal_compile.go:268-322`, sole non-test caller `goal_loop.go:102`) is a deterministic regex marker parser + prose lift + `NormalizeCriteria` + `feasibilityGate`. No model call, no prompt, no "compile turn" to attach a skill to. Making `/goal` skill-governed requires introducing an LLM compile turn — an ADR-sized decision colliding with ADR-053 D9, criteria immutability, and the parser's determinism.
- **CRIT-002 — wrong allowlist seam; stated pattern destructive.** `systemAgentSkills` (`core.go:1324-1336`) covers System Agents only (PlanSupervisor `["plan"]`; nil otherwise). The criteria-authoring agents are seeded by `coreAgentSkills` (`core.go:1520-1539`, consumed `:1902`) — never named by the ADR. Nil allowlist = unrestricted (`core.go:1309-1314`); "seed the way plan is" applied to a nil-allowlist agent would narrow it from every skill to one. User-created agents unaddressed.
- **CRIT-003 — D2 specifies two mutually exclusive defaults.** "default: prose" vs "a criterion carrying a check payload must be (or default to) kind check" produce opposite results for `{text, check:{...}}` with no kind: Reading A → confusing 400 from `validateCriterion` (`criterion.go:288-290`); Reading B → accepted, gate-relevant. Security-relevant guess left to the implementer.
- **CRIT-004 — the D2-rule-5 bash-policy gate bypassable by omitting `kind`.** Both gate copies run on the un-normalized slice before `normalizeCriteria` (`pkg/tools/task.go:644→654` via `allCheckCriteria` `:369-379`; `pkg/sysagent/tools/task.go:335→342`); parsers pass absent kind through as `""` (`task.go:347`; sysagent `:78`). Under Reading B, kind-omitted check-payload criteria are `""` at gate time → gate silent → unsatisfiable task persisted against `bash: deny` — exactly what the gate prevents (`task.go:649-653`). Today unreachable (validateCriterion rejects downstream); D2 opens it. No empty-kind test exists anywhere (`criterion_test.go:48-53` uses "bogus"; `task_test.go:508-566` hardcodes kind).
- **CRIT-005 — motivation misdescribes `JudgeDefaultRubric`; D1's delta unstated.** The shipped rubric is already prose-first ("You adjudicate PROSE criteria only", machine results "useful context ... not something you verdict yourself", `core.go:1371-1387`). The machine-first surface is `buildJudgeUserContent` (`judge.go:897`). D1 must name the target section order and state that worker's-claim-LAST (OBS-003/FR-053, `judge.go:871-877`) survives.

## MAJOR

- **MAJ-001** — drift is four sites, not two; `pkg/sysagent/tools/task.go` is `create_task_in_workspace`, not `create_task`; `pkg/tools/task.go:465` (real `create_task`) and `plan_correct.go:223` missed; each site's `required: ["kind","text"]` must relax in lockstep.
- **MAJ-002** — D3 schema-only: parsers discard `behavior` payloads (`task.go:354-361`; sysagent `:85-92`); executed literally, a correct behavior submission gets a worse 400 ("requires a behavior payload", `criterion.go:325-326`). Must name both parser functions + `MinCount`/`MaxCount` pointer semantics (`criterion.go:64-97`).
- **MAJ-003** — `plan_correct` is a fourth authoring path and the highest-stakes one (`plan_correct.go:216-247`, enforcement `:695-742`); PlanSupervisor's exactly-one-skill grant (FR-007/N3) makes the allowlist edit a spec amendment.
- **MAJ-004** — D2's contract change is ten hand-edited gateway sites (`rest_tasks.go:585-700`, `rest_plans.go:413-528`) with a second-defaulting-site divergence risk, not "run gen-contracts".
- **MAJ-005** — relaxing the shared schema weakens the response contract for a state the backend never emits; request-only relaxation (Input type) is the right modeling and must be argued.
- **MAJ-006** — the SPA edge discards the entire payload on one schema miss (`api.ts:932-946`; whole-array schemas `api.ts:2626`); regenerated zod must ship with the backend commit, not "UI last". (Verified: SPA always sends explicit kind; only reader is the editor — TS-safe.)
- **MAJ-007** — `evidence_quote` listed as a carried-over safeguard but discarded by the parser (`judgeCriterionResponse{ID,Met,Reason}`, `judge.go:975-979`; the constant's own doc admits it). Prompt-only mechanism with no artifact; D5.3 promotes `reason` while its anchor stays invisible.
- **MAJ-008** — the design doc's ship test unanswered: plan-scope window is `""` by construction (`judge.go:879-884`); diff is git/write-set-scoped; a no-file deliverable at plan scope scores unmet by starvation. Evidence assembly needs a decision or an explicit tracked non-goal.
- **MAJ-009** — D6's ordering contradicts D3's own content (D3's text contains D2's relaxation); step 1 as written advertises optional `kind` while `validateCriterion` still 400s empty. Split D3.
- **MAJ-010** — D5.2 half-verified: `formatGoalEcho` (`goal_compile.go:470-508`) already itemizes server-side and is where FR-113 lives per its comment; `Goal` REST schema already carries `criteria`. Reconcile before minting a new frame. (Note: r2's review subsequently found `formatGoalEcho` has no production caller — see the r2 review.)

## MINOR

MIN-001 non-verdict machinery load shift unnamed. MIN-002 `/goal` rejection text steers to technical markers (`goal_compile.go:312-314`); echo prints `[kind]` labels (`:484`). MIN-003 kind-keyed identity comparisons (`criterionKey` `plan_correct.go:870-891`; `sameShape` `goal_compile.go:628-644`) vs legacy empty-kind records. MIN-004 `CreatePlanSlideOver` deepEqual phantom-diff hazard (`:233`, `:65-80`). MIN-005 `IsValidCriterionKind` invariant comment (`criterion.go:30-35`) becomes conditional. MIN-006 ambiguous bare-number ADR citations. MIN-007 no upgrade-path statement for the new skill (`embed.go:16`,`:57`) and a stale failure-string in `contract_test.go:3720-3723`.

## OBSERVATION

OBS-001 no FR numbers/required tests in an ADR about definitions of done; no empty-kind test exists in either direction. OBS-002 three claims verified accurate (editor default/missing behavior; 10px reason; eval plan exists). OBS-003 `author`/`status` remain required with the asymmetry unexplained. OBS-004 verdict reasons match on optional `c.id` — id-less criteria silently show no reason, more visible after promotion.

## Required-before-ACCEPT list (all incorporated in r2)

1. D4(a): drop `/goal` or promote the LLM compile turn to a first-class decision. 2. Name `coreAgentSkills`; append-not-replace; nil/user-created behavior. 3. Resolve the D2 contradiction; default before the gate; two regression tests. 4. Correct D1's surface; state target order; claim-last unchanged; rubric delta. 5. Enumerate all four drift sites; schemas + parsers + pointer semantics. 6. Bring `plan_correct` in or exclude with rationale. 7. Request-only relaxation; enumerate gateway edits; single defaulting site; zod atomic. 8. Address or traceably defer non-file evidence. 9. Split D3. 10. Reconcile with `formatGoalEcho`.

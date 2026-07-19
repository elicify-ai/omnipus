# Adversarial Re-Review (r2): ADR-049 — Planning & Goals (amended r1)

**Spec reviewed**: `docs/internal/architecture/ADR-049-planning-goals-system-agents.md` (amended 2026-07-19 post grill-review r1)
**Prior review**: `docs/internal/architecture/ADR-049-planning-goals-system-agents-review.md` (r1, verdict BLOCK: 3 CRIT / 9 MAJ / 4 MIN / 3 OBS)
**Review date**: 2026-07-19
**Review mode**: generic-markdown (ADR, ratification format), re-review — closure verification + new-issue sweep
**Verdict**: REVISE

## Executive Summary

All 19 r1 findings verify as closed: every claimed closure was checked against both
the amended text and the working tree (branch `feature/planning-goals`, base
`96a1c1ab`), including the code-level premises — `forceCompression` is gone and
test-enforced absent, `IsPrivilegedAgent` currently returns true for `core`||`system`
exactly as the narrowing plan assumes, the CLAUDE.md Storage correction (OBS-001) is
present in the working tree, and no seeded agent uses type `system`. The amendments
are faithful to the codebase; no closure is cosmetic. However, the CRIT-002
resolution creates one NEW major interaction the ADR does not address — a Judge that
is now subject to SEC-26 throttles/caps has no stated loop semantics for the moment
those controls fire, which under NFR-2's fail-closed rule converts judge
*unavailability* into a correlated attempt-burn cascade across every active loop.
0 CRITICAL, 1 MAJOR, 5 MINOR, 3 OBSERVATION findings; all are targeted amendments.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 1 |
| MINOR | 5 |
| OBSERVATION | 3 |
| **Total** | **9** |

---

## Part A — r1 Closure Verification Matrix

Each r1 finding, the amendment's claim, and what this review verified in the working
tree (all citations re-checked at `feature/planning-goals` @ `96a1c1ab` + uncommitted
amendment change set).

| r1 ID | Claimed closure | Verification | Status |
|---|---|---|---|
| CRIT-001 | D3 re-grounded; Summarizer removed from epic (operator decision); §1 facts corrected | `forceCompression` absent from all non-test code; `window_trim_test.go:545-575` *test-enforces* its absence; `windowTrim` comment at `loop.go:8779` (func `:8796`), zero LLM calls; `maybeSummarize`/`summarizeSession` at `:8655`/`:9313`; inert legacy marker confirmed `context.go:882`. D3 now ships category + Judge only; §1 "corrected facts" block is accurate | **CLOSED** |
| CRIT-002 | System Agents non-privileged; `IsPrivilegedAgent` narrowed to core-only; SEC-26 tests extended | Premise verified: `ratelimit.go:21-23` returns `"core" \|\| "system"` today; enforcement seams verified `loop.go:6289`/`:6321`/`:7880`, caps `ratelimit.go:192`/`:269`. `SeedConfig` seeds no type-`system` agent (checked `pkg/coreagent/core.go` seeding paths) so the ADR's "no seeded-install impact" claim holds; legacy hand-crafted behavior change recorded as intentional | **CLOSED** — but see R2-MAJ-001 (new interaction) |
| CRIT-003 | Five ratified check-execution rules in D2 | All five present: bash-tool dispatch mandatory + parallel path forbidden; ask→deny (policy triad verified `validate.go:46-53`); criterion authorship; 60s timeout + output cap, timeout ⇒ fail closed + cannot hold idle clock; MIN-002 guard. Residual (cross-agent confirmation) properly carried as Gap #2 | **CLOSED** — but see R2-MIN-001 (guard omits `ask`) |
| MAJ-001 | Enumeration exclusions ratified in D3 | Exclusions enumerated: default-agent fallback, routing bindings (400), delegation enumeration, `list_agents` pickers, team rosters, Agents-screen System section; Constraint #6 via explicit all-deny seed; `ValidateToolPolicyCoverage` iterates `Agents.List` uniformly (`validate.go:491` verified). Codebase check confirms the work is real: `IsChatTarget()` = `!IsWorker()` (`config.go:919-921`) so a type-`system` agent IS a chat-routing/default-fallback candidate today (`route.go:339-361`) | **CLOSED** |
| MAJ-002 | Migration requirements ratified in D1 | All present: idempotent, task-store-load-time, logged, collision disambiguation keyed by milestone ID order, `due_date` → member tasks' empty `Due`, progress correctly described as computed read-time (verified `milestoneToWireWithProgress`, `rest_milestones.go:~120-130`, FR-L2-010), struct/contract removal, crash-safe re-run | **CLOSED** — see R2-MIN-002/003 (normalization collisions; empty milestones) |
| MAJ-003 | Evidence redaction/retention rules in D2 | Present: `RegisterSensitiveValues` redaction **before persistence**, per-attempt size cap + truncation marker, `$OMNIPUS_HOME` sessions posture, 90-day retention, delete-with-task. Mechanism exists (ADR-004 flow). Residual (unregistered secrets echoed by checks) is inherent to the r1-recommended mechanism — accepted | **CLOSED** |
| MAJ-004 | Durability/boot reconciliation/overlap guard/sweeper in D4 | Present: persisted counters (Plan + `UnifiedMeta` extension; `TaskID` precedent verified `unified.go:62`), boot-time reconciliation with task statuses as source of truth, exactly-one engine + overlap guard (cron precedent verified `service.go:140`), 7-day sweeper on cron service, handover destination ratified in NFR-3 | **CLOSED** |
| MAJ-005 | DoD bypass rules in D5 | Present: agent paths reject <1 criterion and reject edits below 1; Scratchpad exempt (field verified `task.go:197-203`); no-criteria-no-prompt human task → title+description; recurring-run criteria immutable per run | **CLOSED** |
| MAJ-006 | NFR-5 named mechanisms | Present: judge-verdict transcript entry type (wire contract), ActivityPanel span type **extension** (acknowledged as new work, shape = Gap #3), out-of-turn usage metering attributed to the System Agent's `agent_id` + plan/task/goal correlation IDs | **CLOSED** |
| MAJ-007 | Release placement ratified (§2 Constraints) | Present: ships in v0.1.1 (operator decision), PRs target release line, human-approved release→main PR, manual issue closure with PR citations, `.preview-doc/` reconciliation sentence (slice pulled forward; Workspaces re-cast stays v0.3). Branches verified: `feature/planning-goals` exists (checked out), `origin/release/v0.1.1` exists | **CLOSED** |
| MAJ-008 | Resolved via D3 split | Summarizer out of epic; category + Judge only; standing rule (FR-7) binds future out-of-turn features; Memory System Agent rides the first real tenant (v0.3) | **CLOSED** — see R2-OBS-001 (grandfathering wording) |
| MAJ-009 | Global active-loop cap + plan rounds | Present in D7: global cap on ACTIVE loops, default 16, configurable; plan judge 20 rounds (symmetric with /goal); `active loops: N/cap` in status output; plan-inner task loops bounded by the plan | **CLOSED** |
| MIN-001 | Round/idle definitions | Present: round = one worker turn + its judge evaluation; idle = no attempt/state transition/user interaction, hung checks time out so cannot hold the clock; "cheapest model" heuristic dropped (Gap #1 fallback = default agent's model); one /goal per session, replace-on-set (FR-5) | **CLOSED** |
| MIN-002 | Validation guard (D2 rule 5) | Present as ratified | **CLOSED** — see R2-MIN-001 (`ask` omission) |
| MIN-003 | Tag rules | Present: lowercase, trimmed, max 64, max 16/task, workspace-scoped, `prefix:value` convention-only | **CLOSED** |
| MIN-004 | FR-9 scoped | Present: global + per-entity (plan/task/goal/loop); workspace layer explicitly deferred | **CLOSED** |
| OBS-001 | CLAUDE.md corrected in same change set | **Verified in working tree**: CLAUDE.md line 66 now describes `windowTrim` (ADR-028), retired `forceCompression`, `maybeSummarize`/`summarizeSession` as inert reference-only — matches code. Uncommitted (see R2-OBS-003) | **CLOSED** |
| OBS-002 | Contract-surface table | Present in §6: Plan, PlanState, create/update requests, Task.tags, AcceptanceCriterion (kind/text/check/author/status), EvidenceRecord, JudgeVerdict + transcript entry, goal/loop AsyncAPI frames, Agent additions, command registry, Milestone removal diffs | **CLOSED** |
| OBS-003 | Evidence-first judge input | Present in D2: evidence records + workspace diffs primary, worker summary last, unevidenced claims score unmet | **CLOSED** |

**Citation accuracy note:** all re-checked file:line citations verify within ±1 line
(comment-line vs func-line) except one drift recorded as R2-OBS-002.

---

## Part B — New Findings (introduced or exposed by the amendments)

### MAJOR

#### [R2-MAJ-001] Judge starvation semantics: the CRIT-002 resolution gives the Judge brakes with no stated behavior when they engage

- **Lens**: Incompleteness / Inoperability (new interaction created by the amendment)
- **Affected section**: NFR-2, NFR-4, D3 privilege decision, D7.
- **Description**: The amendment (correctly) makes the Judge subject to per-agent
  LLM rate limits and the global daily cost cap
  (`MaxAgentLLMCallsPerHour` / `DailyCostCapUSD`, `pkg/config/sandbox.go:400-403`;
  both opt-in, 0 = off). The Judge is a *single seeded agent identity* serving up
  to 16 concurrently active loops (D7 global cap) — all judge calls share **one**
  per-agent rate bucket. NFR-2 ratifies "absence of evidence/verdict never defaults
  to success," explicitly extended to check timeouts and policy denials — but the
  ADR never distinguishes *verdict absent/unparseable* (attempt genuinely failed)
  from *judge temporarily unavailable* (throttled, cost-capped, provider 429/5xx).
  Read literally, a rate-limited judge call fails the attempt closed.
- **Impact**: On an installation where the operator has configured either limit
  (i.e., exactly the cost-conscious operator CRIT-002 protects), all active loops
  share the Judge's bucket: the moment it exhausts, every in-flight attempt across
  every loop "fails" simultaneously, burning the 3-attempt task budgets and waking
  owners in a correlated cascade — task state now records spurious failures caused
  by a throttle, not by the work. A tripped daily cost cap fails the entire active
  loop population at once. The brake designed to save money instead destroys loop
  state.
- **Recommendation**: One paragraph in D7 (or a new §3 Gap #9 with a stated
  default): judge-unavailable (rate-limited / cost-capped / provider transient
  error) is **not** a verdict — the loop pauses the attempt and retries with
  backoff (the D4 "loop pauses" state from Gap #6 is the natural home), the
  attempt counter is not consumed, and the idle-expiry clock (7d) remains the
  backstop so a permanently-capped judge still winds down gracefully per NFR-3.
  State explicitly that only an *evaluated* verdict or evidence-absence fails
  closed, never control-plane unavailability.

### MINOR

#### [R2-MIN-001] D2 rule-5 guard checks `deny` but not `ask`, which rule 2 itself resolves to deny

- **Lens**: Inconsistency
- **Affected section**: D2 ratified execution rules 2 and 5; FR-6.
- **Description**: Rule 2 makes `ask` resolve to deny in the unattended judge
  context. Rule 5 (the MIN-002 guard) rejects agent-path create/update only when
  the assignee's effective `bash` policy **is deny**. A task whose criteria are
  all machine-type, assigned to a `bash: ask` agent, is exactly as structurally
  unsatisfiable — every check will resolve ask→deny at judge time — yet passes
  the guard as written and fails 3 attempts deterministically.
- **Recommendation**: Reword rule 5 to trigger when the effective policy is
  **not `allow`** (deny or ask), with the same reject/warn split.

#### [R2-MIN-002] Migration collision detection is stated pre-normalization; tag length cap can overflow the `milestone:` form

- **Lens**: Incorrectness
- **Affected section**: D1 migration requirements × D1 tag rules.
- **Description**: (1) Tag rules force lowercase; the migration disambiguates
  "name collisions". Milestones named `Q3` and `q3` do not collide as *names*,
  but collapse into one `milestone:q3` tag after normalization — silently
  re-merging distinct groupings, the exact defect MAJ-002 closed. The collision
  check must operate on the **normalized final tag string**. (2) Tags cap at 64
  chars; `milestone:` consumes 10, so any milestone name over 54 chars (or a
  `-N` suffix landing at the boundary) produces an invalid tag — no
  truncation/overflow rule is stated, and naive truncation creates a *new*
  collision class.
- **Recommendation**: Amend D1: collisions are detected on the normalized final
  tag (post-lowercase/trim, prefix + suffix included); names exceeding the
  budget are truncated to fit **before** disambiguation suffixing, with the
  suffix guaranteeing uniqueness; migration log records every rename.

#### [R2-MIN-003] Empty milestones vanish without trace — `due_date` preservation only rides member tasks

- **Lens**: Incompleteness
- **Affected section**: D1 migration requirements; FR-2.
- **Description**: The ratified `due_date` preservation copies the date into
  member tasks' empty `Due` fields. A milestone with zero member tasks — the
  normal state of a *future* planned release bucket — produces no tag on any
  task, so its name and `due_date` are dropped entirely, unlogged. The r1
  requirement was "not discarded … or explicitly record the operator's decision";
  the empty-milestone case falls outside the adopted mechanism and no decision
  is recorded for it.
- **Recommendation**: One sentence in D1: empty milestones are (a) logged
  by name + due_date at migration as intentionally dropped, or (b) converted to
  a placeholder task carrying the tag + Due. Either is fine; record which.

#### [R2-MIN-004] D3 does not decide whether type-`system` agents are operator-creatable via REST

- **Lens**: Ambiguity
- **Affected section**: FR-7, D3; r1 Test Coverage item 3 (partially unadopted).
- **Description**: FR-7 says System Agents are "seeded, locked, visible, editable
  model + rubric prompt". "Locked" in this repo's existing sense (core roster)
  means identity-locked, not non-creatable. r1's test-coverage list explicitly
  asked "REST create of a System Agent type rejected/allowed per decision"; §9
  step 4 adopts the boot test and the SEC-26 test but not this decision. Whether
  `POST /api/v1/agents` accepts `type: system` (and DELETE on the seeded Judge)
  is left open — two implementers will answer differently, and the answer gates
  the MAJ-001 exclusion surface (each additional System Agent multiplies it).
- **Recommendation**: One line in D3: the `system` type is seed-only in this
  epic — REST create with `type: system` is rejected 400 (mirroring the
  `delegation_policy` body-sniff precedent), and the seeded Judge is
  non-deletable. (Or the contrary, recorded.)

#### [R2-MIN-005] Gap #6's stated default covers disable/re-enable but not deletion

- **Lens**: Incompleteness
- **Affected section**: §3 Gap #6.
- **Description**: The likely-assumption is "loop pauses, plan surfaces `blocked`;
  resume on re-enable". Deletion has no re-enable — a plan owned by a deleted
  agent blocks forever until the 7-day idle sweeper reaps it, which is a silent
  week-long stall with no owner to wake. The gap's title names "deleted" but the
  assumption only answers "disabled".
- **Recommendation**: Extend the assumption: on owner deletion, the plan takes a
  terminal wind-down (NFR-3 handover written to the plan record + owning
  session) instead of waiting out idle expiry. Plan-spec details the state.

### OBSERVATIONS

#### [R2-OBS-001] State the grandfathering of `summarizeSession` explicitly in FR-7

- **Lens**: Ambiguity
- **Description**: FR-7's standing rule — "every future *out-of-turn* LLM action
  MUST run as a System Agent" — coexists with `summarizeSession`, which runs in a
  goroutine spawned by `maybeSummarize` (`loop.go:8655+`, fired from `:8390`),
  i.e. a genuinely out-of-turn LLM call that survives this epic un-rerouted. The
  word "future" implicitly grandfathers it, but a reader auditing compliance on
  day one finds a violation unless the ADR says so.
- **Suggestion**: Add one clause to FR-7: "`summarizeSession` (legacy,
  reference-only output) is explicitly grandfathered until the v0.3 memory work
  lands the Memory System Agent."

#### [R2-OBS-002] Two citation drifts (non-substantive)

- **Lens**: Incorrectness (hygiene)
- **Description**: (1) D3 cites `applyMemoryCommandPrompt loop.go:9603`; it is at
  `loop.go:9770` (`handleCommand` `:9582` and `applyExplicitSkillCommand` `:9693`
  are exact). (2) D3/MAJ-001 describe `resolveDefaultAgentID` falling back to the
  "first ENABLED agent" (r1's phrasing, from CLAUDE.md); the actual fallback picks
  the first **chat-target** agent — `IsChatTarget()` = `!IsWorker()`
  (`config.go:919-921`, `route.go:339-361`). Substantively this *strengthens*
  MAJ-001's closure (type-`system` passes `IsChatTarget` today, so the exclusion
  is real work) and suggests the natural enforcement point: make
  `IsChatTarget()` (or an equivalent central predicate) return false for
  type-`system`, so every chat-routing surface inherits the exclusion at once.
- **Suggestion**: Fix the two citations; optionally name the central-predicate
  option for plan-spec.

#### [R2-OBS-003] The entire amendment change set is uncommitted on an ephemeral pod

- **Lens**: Inoperability
- **Description**: `git status`: the ADR and the r1 review are untracked; the
  CLAUDE.md OBS-001 correction is an uncommitted modification. §9 step 5 claims
  the CLAUDE.md fix "lands with this amendment's change set" — it exists but
  nothing has landed. This devpod is ephemeral and git is the source of truth;
  a pod recycle destroys the ADR, both reviews, and the correction.
- **Suggestion**: Commit ADR + r1 review + this r2 review + CLAUDE.md correction
  as one change set on `feature/planning-goals` and push, before plan-spec work
  begins (authorship per the repo's mandatory git-identity rules).

---

## Part C — Stress of the Remaining §3 Gaps (1–8)

| # | Gap | Assessment |
|---|---|---|
| 1 | Judge default model | Sound. The r1 "cheapest model" non-heuristic is gone; onboarding choice + default-agent's-model fallback is machine-derivable. Safe for plan-spec. |
| 2 | Cross-agent machine-check confirmation | Correctly scoped: CRIT-003's structural rules (bash machinery, ask→deny, authorship) bound the residual to the confirmation-flow choice; the stated default (assignee-owner confirmation) fails safe. Safe for plan-spec. |
| 3 | ActivityPanel span shape | Pure rendering shape; NFR-5 already commits the span-type extension as scoped work. Safe for plan-spec. |
| 4 | /goal,/loop on non-web channels | Plausible: `handleCommand` is channel-agnostic (bus-inbound text). One plan-spec must-cover: channel replies for `status` output formatting, and Gap #5 interplay (a channel peer = a loop starter). |
| 5 | Role gating (multi-user) | The v1 posture (session ownership/auth + routed-agent brakes + global cap 16) is a coherent interim answer; the global cap is the real backstop. The flagged "plan-spec + security review" routing is right. Watch: the global cap is shared — one channel peer can exhaust all 16 slots and starve the operator's own loops (fairness, not security; acceptable v1). |
| 6 | Owner agent disabled/deleted | Deletion path unanswered — promoted to R2-MIN-005. |
| 7 | Recurring Trigger × loop | Per-run loop + immutable per-run criteria composes cleanly with D5/D7. One plan-spec must-cover: a recurring fire while the previous run's loop is still active (overlap guard precedent exists in cron, `service.go:140`). |
| 8 | Loops spawning loops | The v1 enforcement claim (command surface not exposed to task runs / delegated sub-turns) has one side door plan-spec MUST close when it "confirms enforcement point": cron-injected prompts. A looping agent with cron tools can schedule a message whose text begins `/goal …`; if `handleCommand` parses cron-`continue` session injections identically to user chat, the loop has spawned a loop. The enforcement point must therefore discriminate on message *origin* (user-originated vs system-synthesized), not on surface. |

## Part D — Structural / Test / STRIDE Deltas

- **Structural**: The amended ADR resolves every r1 structural weakness (phase
  placement, on-call/runbook posture via D4 durability, gap-assumptions promoted
  to ratified text). The §3 table now contains genuinely open plan-spec items
  only — with the two exceptions promoted above (R2-MAJ-001 as a missing Gap #9
  or D7 paragraph; R2-MIN-005 inside Gap #6).
- **Test coverage**: §9 step 4 adopts r1's test list almost fully (check-execution
  security tests incl. ask→deny + timeout; Constraint #6 boot test; SEC-26
  non-privilege test; crash-recovery/reconciliation; migration idempotency;
  ADR-043 extension-not-fork). Missing per this review: a judge-starvation test
  (rate-limited judge ⇒ attempt NOT consumed, loop paused — per R2-MAJ-001's
  resolution), a normalized-tag collision test for the migration (R2-MIN-002),
  and the REST-create decision test (R2-MIN-004).
- **STRIDE delta vs r1**: the r1 threat table's Machine-check executor row is
  now structurally mitigated (rules 1–5); Judge row's E column (SEC-26
  exemption) flips from *exempt* to *subject* — which introduces the new D
  (denial-of-service-by-own-brake) entry R2-MAJ-001. Evidence-store I column
  mitigated by redaction rules; migration T column mitigated except the
  normalization case (R2-MIN-002). No new spoofing/repudiation surfaces
  introduced by the amendments.

## Unasked Questions (r2)

1. When the Judge is rate-limited or cost-capped mid-loop, does the attempt
   counter advance, and does the loop pause or fail? (R2-MAJ-001 — must be
   answered in the ADR or as Gap #9.)
2. Are additional type-`system` agents creatable via REST, and is the seeded
   Judge deletable? (R2-MIN-004.)
3. Is the enforcement point for Gap #8 message-origin-based (user vs
   synthesized), so cron-injected `/goal` text cannot start loops?
4. On owner-agent *deletion* (not disable), does the plan wind down terminally
   or wait out the 7-day idle sweeper? (R2-MIN-005.)

---

## Verdict

**REVISE.**

All 19 r1 findings are verifiably closed — the closures are substantive and
codebase-accurate, and the r1 BLOCK is fully discharged. No new CRITICAL exists.
One new MAJOR (R2-MAJ-001, judge-starvation semantics — a direct consequence of
the otherwise-correct CRIT-002 resolution) plus five MINORs and three
OBSERVATIONs remain; every one is a one-paragraph-or-one-sentence amendment, not
a direction change. D1–D8 directions stand.

Address R2-MAJ-001 (and ideally R2-MIN-001..005) in the ADR, then re-run:

```
/grill-spec docs/internal/architecture/ADR-049-planning-goals-system-agents.md
```

Given the findings' narrowness, a targeted amendment + focused re-check of the
amended paragraphs (rather than a fourth full pass) is a defensible fast path if
the operator prefers.

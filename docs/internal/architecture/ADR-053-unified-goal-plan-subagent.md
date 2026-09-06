# ADR-053: Unified goal / plan / subagent system — one goal core, three bindings

- **Status:** **Accepted** (2026-07-22), **superseded in part by [ADR-057](ADR-057-session-parent-child-parity.md)** (see banner below) — **amended 2026-08-30: `launch_profile` removed (see Amendment below)**. This ADR ratifies an interview-locked, twice-grilled design; it does not re-open it. The `/grill-spec` gate returned **PASS** (0 CRITICAL / 0 MAJOR / 0 MINOR after one REVISE cycle addressing F-1..F-6; review file adjacent), satisfying delivery-brief DoD-1.
- **Superseded in part by:** [ADR-057](ADR-057-session-parent-child-parity.md) §8 — **D1** (superseded outright), **D5**, **D15**, **D16** (each changed). Details in the banner immediately below.
- **Date:** 2026-07-22
- **Deciders:** Operator (Daniel Piatkowski); architect (ratification)
- **Evidence level (highest used):** 1 (operator-locked — 17 interview decisions D1–D17 + ~46 folded review findings) + design `[DESIGN]` grounding (the v2.2 target design) + codebase `[FACT]` grounding carried from ADR-049/052
- **Supersedes (in part):** [ADR-049](ADR-049-planning-goals-system-agents.md) — **D4** (hybrid coordinator — the one-shot owner wake), **D7's round accounting** (a round is no longer "one worker turn + its judge evaluation" but **one adjudication**), and **FR-5/D6's after-every-turn cadence + one-`/goal`-per-session** limit (→ claim-or-idle, per-goal-id, multiple goals per session) — and [ADR-052](ADR-052-autonomous-agent-plan-execution.md) **chat-goal triggers** (the after-every-turn goal adjudication it carried forward). Exact insertion text for both is in §5. Everything else in ADR-049 and ADR-052 — the deterministic dispatch engine, boot reconciliation, the verifier-as-real-agent architecture, the PUT-lockdown, the Stop fan-out — stands.
- **Authoritative design (the *what*):** [`unified-goal-plan-subagent-target-design-v2.2.html`](../design/unified-goal-plan-subagent-target-design-v2.2.html) — 13 sections, 8 diagrams (t0/t1/t2/t3/g4/g5/g6/g7), acceptance table G-1..G-16, BOM §9.
- **Delivery contract (the *how / order / proof*):** [`unified-goal-plan-subagent-DELIVERY-GOAL.md`](../design/unified-goal-plan-subagent-DELIVERY-GOAL.md).
- **Related:** ADR-034 (inline `oneOf` discriminated-union precedent — governs the `SessionMessage` contract), ADR-035/ADR-037 (raw-body-reject precedent for forbidden fields, no back-compat), ADR-043 (completion-signal marker protocol this extends), ADR-046 (System-Agent implicit workspace membership), ADR-028 (context paging).

> **Ratification mode.** The direction was decided across an extended operator design conversation, folded from a v2.1 assessment, and grilled twice (grill-31 · architect F1–F9 · third-pass N-1..N-15 — ~46 findings folded into v2.2). This ADR **records and grounds** the decision, records per-decision confidence, and names what it supersedes. It is deliberately concise: the design HTML holds the node-by-node detail; this ADR authorizes it as one system.

> ## ⚠️ SUPERSEDED IN PART BY [ADR-057](ADR-057-session-parent-child-parity.md) — read before implementing anything below
>
> **Do not implement D1 as written.** ADR-057 §8 changes four decisions. The table in **D1 (§4, "two identity namespaces")** describes a **retired** design, and its 2026-07-31 amendment (which added *"the transcript namespace IS shared — FR-6a retained, not dropped"*) is **exactly what ADR-057 deliberately supersedes**: it fixed the description; ADR-057 changed the system so D1's original "isolated-but-linked" intent is now literally true in code.
>
> | ADR-053 | Status under ADR-057 | What is now true |
> |---|---|---|
> | **D1** — dual namespace; child inherits parent's `transcriptSessionID` | **Superseded** | A delegated child gets its **OWN** `transcriptSessionID` (`pkg/agent/subturn.go:1113`, `TranscriptSessionID: childID`). The cascade-cancel key is a **separate, inherited** `routingSessionID` (`subturn.go:1130`). The transcript **visibility filter is deleted, not replaced** (FR-034), and FR-038 **forbids** reintroducing one at any read boundary. |
> | **D5** — ownership gate | **Changed** | Equality → depth-bounded ancestor walk (ADR-057 D7). |
> | **D15** — per-child message ceiling | **Changed** | Per-direct-parent, not per-chat-subtree. |
> | **D16** — ad-hoc inboxes keyed to the durable chat/plan id | **Changed** | `ParentDurableKey` now names the **immediate** parent. |
>
> **The four interrupt entry points D1 names no longer exist.** `InterruptSession`, `InterruptBySessionKey` and `InterruptBySessionKeyHard` are **retired** (`func InterruptSession` returns zero hits); ADR-057 FR-041 collapses them into **`Interrupt(id, scope, hint)`** (`pkg/agent/steering.go:667`, graceful) and **`InterruptSessionHard(id, scope, hint)`** (`:729`, hard) — both taking a **mandatory `InterruptScope`** (`ScopeSubtree` / `ScopeSelfOnly`). The per-delegation `delegate cancel` path D1 describes is now `ScopeSelfOnly`, not a separate function. (Not to be confused with the unrelated, still-live process-wide `InterruptGraceful(hint)` / `InterruptHard()`.)
>
> Everything else in this ADR — the S1–S6 spine, the evidence-ladder Judge, the goal core, the git evidence layer, the budget model — **stands unchanged**.

> **Amendment (2026-08-30, operator decision) — `launch_profile` removed; steering is always available.** §5.1's `launch_profile` field (`utility` vs. `specialist`, on both `DelegateRunAction` and `SessionLifecycleRecord`) is **deleted outright**, not just superseded. There is no longer a launch-time choice between a fire-and-collect delegation and a steerable one: every direct delegation now behaves the way `specialist` used to — `action="respond"` is always available on a non-terminal child session, gated only by ownership and state, never by a launch profile. The `utility` mode was never actually needed in practice, and having it unenforced by default (mint-time only, no real gate) was a footgun rather than a feature — a caller could believe steering was blocked when it never was. Keeping exactly one behavior removes that footgun instead of enforcing it. `launch_profile` is removed from both wire schemas (no back-compat, no deprecated-but-accepted field) and from `session.LifecycleRecord`; the `delegate` tool no longer accepts or documents the parameter. **Correction (2026-08-26 fix session): `action="steer"` is NOT covered by "gated only by ownership and state."** Steering has always been an orthogonal, D5-shaped gap the `launch_profile` removal never touched: every steering-queue drain site lives in the native turn engine (`pkg/agent/loop.go`, `pkg/agent/steering.go`), and `runExternalCLISubTurn` (`pkg/agent/external_dispatch.go`) never drains it — so a `steer` queued against a 3P (external-CLI) child session was silently orphaned forever, never a real capability the launch-flag removal could have unlocked or blocked. `executeSteer` (`pkg/tools/delegate.go`) now rejects `action="steer"` outright for `Is3P` sessions with a clear error, mirroring `message_parent`'s existing Is3P posture (D5); `action="respond"` remains the correct way to reach a parked 3P child (it redispatches a corrective session, per D5 below), and `action="follow_up"` remains available once the child is terminal.

---

## 1. Problem Understanding (context)

The Planning & Goals epic (ADR-049) and autonomous plan execution (ADR-052) shipped a working engine, an evidence-ladder Judge (real verifier agent, own session), and a plan+execute tool surface. **Live testing then exposed a family of gaps that are not five separate features — they are one missing spine.** Each has been observed on the running system:

1. **Blind / over-eager Judge triggering.** The shipped chat goal re-adjudicates after *every* completed worker turn; a turn that ends in a question to the user is judged as if it were a completion, and the claimless path judges nothing durable. There is no typed pause and no evidence substrate to judge against.
2. **Round-burn (F2, a live shipped defect).** On an all-terminal-but-unmet plan the engine re-judges the *unchanged* state on every ~30 s idle tick, burning a `JudgeRound` each time until rounds are exhausted while the owner deliberates.
3. **Cold-restart chains.** A `kill -9` mid-plan leaves persisted non-terminal sessions with no live turn; nothing sweeps them, so a plan can wedge forever.
4. **No child→parent inbox.** `delegate` today accepts only `run | status` (`pkg/tools/delegate.go` rejects anything else); a subagent cannot ask a question, report a checkpoint, hand back structured results, or be steered mid-run. Delegation is fire-and-collect only.
5. **Evidence poverty.** The Judge sees a transcript window and machine-check output but no per-attempt diffs; for a plan it has almost nothing durable to adjudicate against.

The through-line: **goals (chat `/goal`), standalone tasks, and plan Definitions-of-Done are the same object judged the same way** — but they were built as three code paths with three trigger models, three (or zero) evidence models, and no shared session-control plane. This ADR unifies them onto one core and adds the missing substrate (typed session messaging, git-backed evidence, boot recovery) once.

**Blast radius:** the goal-loop trigger path, the delegate tool surface + a new child tool, a new typed message transport over the existing `pkg/bus`, a spike-gated embedded-git dependency, new wire contracts (§7), the goal pill / ActivityPanel SPA surfaces, and a converted app-level budget. Security-relevant (agents gain a bidirectional control channel and a workspace git layer), so the retained guardrails — Constraint #6 tool policy, the kernel sandbox, fail-closed judging — remain load-bearing.

## 2. The unifying thesis — ONE goal core, THREE bindings

**Build the core once, bind it three times.** A goal is `prompt + goal definition + acceptance criteria` (criteria in three kinds: machine / behavior / prose). One core owns the goal shape, the claim-or-idle trigger discipline, the question→pause, the critic/evidence ladder, feedback steering, typed messaging, the cancel cascade, the count/token bounds, and the git-evidence feed. The **only legitimate differences** between the three bindings are:

1. the **deterministic composition engine** (plan-only — the DAG dispatch/cap/verdict-plumbing layer, reused unchanged from ADR-049/052), and
2. the **UI / data-model binding** (where the goal lives and how it is surfaced).

| Binding | Goal source | Surface |
|---|---|---|
| **Chat goal** | agent-compiled from user intent at `/goal set` | bottom-right goal pill |
| **Standalone task** | explicit in the task record at creation | Board / List / Graph, with ▶/■/Play |
| **Plan DoD** | the plan's Definition-of-Done over a member DAG | plan tile → Graph view |

Anything that is *not* one of those two differences is shared. The anti-parallel-systems discipline (design §9 BOM; delivery brief §3) is the enforcement: every mechanism names the existing component it extends or justifies a genuinely new one in place — "we built a second one" is a blocking review finding.

## 3. The shared spine — S1–S6 (build-once, first-class)

These six seams are **designed together and owned as single implementations**, each built once and consumed by multiple bindings. They are the anti-drift guarantee — a second goal store, a second messaging envelope, a second claim-marker parser, or a second budget path is a blocking finding (delivery brief DoD-11).

| # | Spine seam | Built once as | Consumed by |
|---|---|---|---|
| **S1** | **Unified goal / criteria record** | one schema: prompt + goal definition + acceptance criteria (machine / behavior / prose), authored two ways — chat = agent-compiled from intent; task = explicit at creation | chat goal · task · plan DoD |
| **S2** | **Durable session record + 8-state lifecycle enum** | one persisted per-entity JSONL record; enum `queued / running / needs_input / paused / completed / failed / cancelled / timed_out` | idle settlement · plan all-terminal detection · `blocked_by` · boot sweep · Agent-View |
| **S3** | **SessionMessage envelope family** | one inline `oneOf` + discriminator envelope (ADR-034), carrying revision-entry, handback, checkpoint, and goal-status as ONE family | child inbox · parent steer · goal-pill feed · plan tile · board aggregation · human parity |
| **S4** | **Owner ↔ Judge ↔ messaging ↔ plan-engine interlock** | the wiring contract: Judge feedback = a `steer`; member telemetry = the owner's inbox; waiting-on-owner = `question(wait=true)`; idle settlement reads S2 lifecycle states | plan correction loop · trigger detection · pause |
| **S5** | **Budget triple** | attempts (per member/task) + JudgeRounds (per goal/plan) + **one app-level OVERALL token budget** (D12) | every scope — owner loops, members, verifiers, Judge |
| **S6** | **Claim / marker family** | `[goal:evidence]` + `TASK_STATUS` + `GOAL_STATUS: met / waiting_on_user`, parser co-located with its teaching fragment | rung-0 gate · trigger detection · waiting-on-user pause |

**S4 is the highest-risk seam** (it composes four subsystems); the rest are additive schema/record work. The transactional-tail-append invariant (N-8), the awaiting-owner-correction gate (F2 fix), and the idle-settlement-vs-no-signal-penalty precedence (architect F5) all live at the S4 interlock and must be specified as a single state machine in `/plan-spec`.

## 4. The decisions — D1–D17 (ratified, with per-decision confidence)

Confidence is **High** where the decision is operator-locked *and* rides a proven substrate with no contingent dependency; **Med** where the decision is locked but its mechanism hangs on the go-git spike (§6) or carries a genuine under-specified predicate that `/grill-spec` must close. No decision below is Low — none is being re-opened.

### Group A — Goal core & triggers

| # | Decision | Conf. | One-line rationale |
|---|---|---|---|
| **D8** | Every idle-settled adjudication consumes a round (no free-first idle); the 1st bare-claim bounce is free (claiming stays strictly cheaper than idling). | **High** | Incentive gradient is sound (N-13): if idle were cheaper than claiming, workers learn to go silent and the evidence gate atrophies. G-2/G-4. |
| **D9** | Unverifiable criteria are caught by a **compile-time feasibility gate only** (no runtime `criterion_unverifiable` verdict); the gate vets semantic judgeability AND reachability. | **Med** | Locked, but "semantic judgeability" is itself an LLM determination made at compile time with **no runtime safety net** once removed — a false-accept has no fallback. Under-spec flagged (§8). G-7. |
| **D11** | Plan DoD + chat goal use **conversational confirmation** (LLM echoes/confirms in chat, no form/modal); re-statement diffs as an amendment, never a silent recompile. | **High** | Chat-first UI rule; FE-8; G-8; N-6. |
| **D12** | Budget = **no money caps, no per-plan/per-goal/Judge budgets**; convert SEC-26 app-level USD cap → **one app-level OVERALL token budget** (Usage screen) covering ALL workloads incl. core agents (ignores `IsPrivilegedAgent`). Brake = `failed(budget_exhausted)` in tokens. | **High** | Operator-locked; cost isn't reliably measurable so tokens are the honest proxy. Default value + the token≠cost semantic shift flagged (§8). G-14. |

### Group B — Judge & evidence

| # | Decision | Conf. | One-line rationale |
|---|---|---|---|
| **D3** | The **git evidence layer ships in-scope, spike-gated** (embedded go-git; real per-attempt write-set-scoped diffs). | **Med→High** (§6.1) | Footprint spike returned **GO** (+3.04 MiB stripped, no cgo). The *decision to include it* is locked; the *mechanism* is now implementable subject to the §6.1 caveats. |
| **D17** | `.git` is denied **by operation** (allow log/blame/show/diff; deny commit/amend/rebase/rm), not by path. | **Med→High** (§6.1) | Spike GO; but D17 needs a **Landlock/bash-policy `.git/` block** (a go-git repo is a byte-identical `.git` the real `git` CLI can `--amend`), not a tool-surface deny alone — a security-lead Phase-1 dependency. G-15. |

*(The Judge itself — real verifier agent, own session, three-rung AND-combined ladder, evidence-gated, fail-closed — is **not** re-decided here; it stands from ADR-052. This ADR changes only WHEN the Judge fires, per the verifier-trigger table in design §8, and adds the blocked-check honesty fix: a machine check that could not run returns "unable to verify" and is re-run, never scored as absent evidence — G-3.)*

### Group C — Owner loop & correction

| # | Decision | Conf. | One-line rationale |
|---|---|---|---|
| **D4** | Correction verbs: append-only tail + **SUPERSEDE** (marks a done member's outcome ignored-by-Judge; the record stays immutable) + **TARGETED RETRY** (retry a transient/frozen member without a full Stop/Play); each records a revision entry. | **High** | DoD immutability preserved; evidence trail preserved; transactional append (N-8). G-11. |
| **D7** | Plan-member tasks have **NO individual start/cancel/resume** (the plan owns lifecycle); standalone tasks keep them. Adjust a member = Stop plan → change → continue. Plan tile click → Graph view. | **High** | Prevents out-of-DAG-order member starts violating dependencies; FE-2/FE-4. |
| **D13** | Play resumes cancelled/failed members from the **last git commit** (not attempt 0); a no-commit member falls back to a fresh attempt; JudgeRounds reset to 0. | **Med→High** (§6.1) | Rides the D3 git layer (spike GO); degrades to fresh-attempt where no commit exists. G-12. |

### Group D — Session-control plane & delegation

| # | Decision | Conf. | One-line rationale |
|---|---|---|---|
| **D1** | Child sessions are **ISOLATED-but-LINKED** at the durable-SessionID layer: own durable `SessionID`; a typed `SessionMessage` channel is the only bridge; a curated context snapshot at spawn. **Two identity namespaces, two scopes** — (a) the durable `SessionID`/`sessionKey` and message history are isolated per child; (b) the **transcript namespace IS shared** (FR-6a **retained**, not dropped): a child inherits its parent's `transcriptSessionID` (`pkg/agent/subturn.go` FR-6a) so the chat-wide `/cancel` cascade (`InterruptSession`/`InterruptSessionHard`, filtering `activeTurnStates` by `transcriptSessionID`) can reach sub-turns — see `cancel-cross-channel-spec` FR-6a and ADR-056 D7. The per-delegation `delegate cancel` action uses a separate `sessionKey`-scoped interrupt (`InterruptBySessionKey`/`InterruptBySessionKeyHard`, direct `activeTurnStates.Load`) so it targets one delegation without cascading to siblings (#577). | **High** | Clean isolation on S2/S3 for ownership/messaging; shared transcript retained as the cascade-cancel key. *(Amended 2026-07-31: the original "FR-6a dropped" wording contradicted the code — `subturn.go` FR-6a is live and load-bearing for the cascade — and would have re-broken the chat-wide Stop if implemented in good faith.)* Snapshot contents under-spec flagged (§8). |
| **D2** | Questions: a child asks its **PARENT**; the parent answers-or-escalates; only a direct session/plan owner asks the **human**, conversationally in chat (no per-question reply card). **AMENDED 2026-09-05 by ADR-074 D4b:** for session OWNERS asking the human, the structured `AskUserQuestion` card supersedes the "no per-question reply card" clause (spec: `docs/internal/specs/askuserquestion-tool-spec.md`); the owner-only restriction and child-asks-parent routing survive unchanged. | **High** | Parent routes `correlation_id`; channel-portable; FE-3; g6. Answer-vs-escalate boundary flagged (§8). |
| **D5** | 3P (external-CLI) workers = **honest fire-and-collect**; `respond` = corrective re-dispatch (a NEW session, original prompt + answer folded in); `needs_input`/`question` never advertised to 3P. | **High** | External CLIs have no warm-resume/needs_input primitive; D5; g6. |
| **D6** | Deep chains are **one-hop-to-parent by design** (escalation = a parent decision); delegation depth is **configurable with a shipped backstop of 3** (`defaultMaxSubTurnDepth = 3`, `resolveEffectiveDelegationDepth` — `pkg/agent/subturn.go:28,56`). | **High** | Each hop is a real ownership decision; the default already exists in code. Only the deep-chain question latency is an open concern (§8). |
| **D14** | Pill states add `judge_unavailable` + `queued` — full **8-state** set: `queued / active / waiting_on_user / judge_unavailable / re-planning / judging / done / failed`. | **High** | One enum drives the pill and the feed; FE-1. |
| **D15** | Message ceiling is **PER-CHILD** (not per-parent) — 20 open question+blocker per child. | **High** | One noisy child cannot starve its siblings; config `per_type_ceiling`. |
| **D16** | Ad-hoc `delegate` inboxes are keyed to the **durable chat/plan id**. | **High** | Inbox survives a parent Stop/Play; g6. |

### Group E — Parallelism & git topology

| # | Decision | Conf. | One-line rationale |
|---|---|---|---|
| **D10** | Exploratory parallel work runs each in its **own git worktree**; merge at the join. | **Med→High** (§6.1) | Spike confirmed go-git has **no** `worktree add` and ff-only merge — so joins assemble via shard/subdir (D10/G-16 anticipate this); isolation ladder = system-git → go-git clone → subdir. G-16/g4/g5. |

**Cross-group note — the git family (D3, D10, D13, D17) was Med as a set** because all four hung on the Phase-0 go-git spike (§6). **The spike has now resolved GO (§6.1)**, so the mechanism is known-implementable subject to three caveats (media size-guard, ff-only-merge → shard/subdir joins, D17 needs a sandbox `.git/` block); the residual is caveat-driven implementation detail, a `/plan-spec` closure rather than a direction risk. Every non-git decision landed regardless of the spike.

## 5. Supersede-in-part

This ADR supersedes exactly two prior decisions; both prior ADRs otherwise stand. A docs pass should insert the following notes verbatim.

### 5.1 ADR-049 — add under the existing "Superseded in part" header

> **Superseded in part (2026-07-22) by [ADR-053](ADR-053-unified-goal-plan-subagent.md) — owner-wake mechanism, round accounting, and chat-goal cadence.** Three related supersessions:
> 1. **D4 owner-wake.** ADR-053 replaces D4's *one-shot* owner wake (the owner agent woken via the async-notifier seam only at decision points, `wakeOwner`) with a **persistent owner session, re-opened on purpose** — the plan/re-plan correction loop runs as an owner inbox (S4 interlock), gated by the awaiting-owner-correction state so an all-terminal-but-unmet plan consumes one round then waits.
> 2. **D7 round accounting.** A "round" is no longer "one worker turn plus its judge evaluation" (the definition tied to the `checkGoalLoopAfterTurn` hook). Under ADR-053's claim-or-idle model a **round is one adjudication** — fired by an explicit claim or by idle settlement, not by every turn — so `judge_rounds_max` now counts adjudications. The `/plan-spec` must state the reconciliation and its effect on the existing round-budget accounting.
> 3. **FR-5 / D6 chat-goal cadence & cardinality.** The `/goal` command's after-every-turn adjudication and the "one active `/goal` per session" limit are replaced by claim-or-idle triggering **per goal-id**, allowing multiple concurrent goals in one session (each with its own settlement, pill, and timers).
>
> D4's server-side deterministic dispatch engine, boot-time reconciliation, single-instance overlap guard, and the 7-day idle-expiry sweeper are **unchanged**.

### 5.2 ADR-052 — add a NEW **inbound** "Superseded in part by" header

*(ADR-052 today carries only an **outbound** "Supersedes (in part):" header — the wrong polarity for this note. This is an inbound supersession **of** ADR-052, so the docs pass must add a new header line, not append under the outbound one.)*

> **Superseded in part (2026-07-22) by [ADR-053](ADR-053-unified-goal-plan-subagent.md) — chat-goal triggers.** The shipped chat-goal trigger — the goal loop adjudicates after *every* completed worker turn — is replaced by ADR-053's **claim-or-idle** model: the Judge fires only on (a) an explicit completion claim (`[goal:evidence]` + `GOAL_STATUS: met`) or (b) event-driven idle settlement (claimless, quiet-window debounced, per goal-id), and a `GOAL_STATUS: waiting_on_user` turn **pauses with no verdict and no round**. ADR-052's autonomous plan+execute tool surface, the verifier-as-real-agent-in-its-own-session architecture, the PUT-lockdown, the Stop fan-out, and the restart/continuation model all **stand**; only the chat-goal after-every-turn trigger is superseded. (The after-every-turn behavior originated with the ADR-049 FR-5 `/goal` command and was carried forward through ADR-052; ADR-053 replaces the trigger semantics for both.)

## 6. The spike gate — go-git (Phase 0, long pole)

The git-evidence layer (D3) and everything that hangs off it (D10 worktrees, D13 Play-from-commit, D17 `.git` operation policy, G-15/G-16, the plan-Judge's real-diff evidence) is **gated on a Phase-0 spike** that measures embedded go-git against the Hard Constraints — **before** the git contract is frozen.

- **Go branch:** embedded go-git is adopted. The spike must show: binary-size delta within Constraint #1/#3 (`< 10 MB` security-feature overhead / single-binary footprint); acceptable auto-commit cost on a realistic media-heavy `work/`; an executable isolation-and-merge envelope **despite go-git having no `worktree add`** (the clone-fallback/subdir isolation ladder must be demonstrably executable); and the Apache-2.0 NOTICE ride-along (OBS-3).
- **No-go branch:** fall back to **system-git-when-present + subdir isolation** and re-scope the git layer accordingly. In this branch: D10 worktrees degrade to subdir isolation; D13 Play-from-commit degrades to fresh-attempt where no commit exists; D17 operation-policy applies to the agent's `bash git …` invocations rather than an embedded engine. **Everything else in this ADR lands regardless** — the trigger fix, the session-control plane, the boot sweep, the budget conversion, and the goal compiler do not depend on the spike.

The spike is a **hard gate on the git contract only**, never a gate on the ADR.

### 6.1 Spike resolution (2026-07-22): **GO**

The Phase-0 spike ran and returned **GO**, with three design caveats that shape the Phase-1 git layer (they do not change any decision):

- **Footprint passes.** Binary-size delta **+4.40 MiB raw / +3.04 MiB stripped** (well under the ~10 MB guideline); no cgo anywhere in the go-git dependency tree — Constraints #1/#2/#3 hold.
- **Caveat 1 — media bloat needs an app-level retention/size guard.** `.git` grows ~1:1 with committed media and `RepackObjects` (go-git's gc analogue) does not shrink it. The auto-commit path must carry the OBS-3 size guard (skip/warn above N files/MB) and an app-level retention policy — git-level compaction is not sufficient.
- **Caveat 2 — no `worktree add`, and merge is fast-forward-only.** Confirmed: go-git has no linked-worktree API and no 3-way content merge (only ff). So the isolation ladder's middle rung (go-git local clones) **isolates writes but cannot 3-way-merge them back** — joins assemble via the shard/subdir pattern (or system-git where present), exactly as D10/G-16 anticipate. The clone rung costs a full object-store copy (no shared inodes).
- **Caveat 3 — D17 needs a sandbox-level `.git/` block, not just tool-surface deny.** A go-git repo is a byte-identical standard `.git`; the real `git` CLI can `commit --amend` against it. So deny-by-operation (D17) must be paired with a Landlock / bash-policy control blocking agent shell access to `.git/` — a **security-lead dependency** for Phase 1, not a tool-schema-only gate.
- **NOTICE (OBS-3):** all 17 net-new modules are permissive (Apache-2.0 / MIT / BSD, zero copyleft); exactly one dep (`skeema/knownhosts`) ships a NOTICE requiring verbatim reproduction, and Omnipus has no NOTICE file today — adopting go-git triggers creating one.

With the spike resolved GO, the git-family decisions (D3/D10/D13/D17) move from "mechanism contingent on the spike" toward implementable, subject to the three caveats above; `/plan-spec` writes the git layer on the go-git substrate (no dual-branch carry needed) while still specifying the graceful degradation ladder (system-git → go-git clone → subdir) for runtimes where the middle/upper rungs are unavailable.

## 7. Contract-first surface this ADR authorizes (Constraint #8)

Every byte crossing the gateway/SPA boundary is defined in `contracts/*.yaml` **before** any Go/TS, generated types only, `make verify-contracts` green. Per the delivery brief §6, **all of these land in Phase 0**, before consuming code. The `SessionMessage` discriminated union is hosted **inline** in `openapi.yaml` per the ADR-034 precedent (external file refs inside a `oneOf` produce non-compiling `As*` accessors under oapi-codegen).

| Wire type | Shape | Spine |
|---|---|---|
| **SessionMessage** | inline `oneOf` + discriminator (ADR-034) — carries revision-entry, handback, checkpoint, goal-status | S3 |
| **Unified goal / criteria record** | schema: prompt + definition + criteria (machine/behavior/prose), two authors | S1 |
| **Durable session-lifecycle record** | schema — 8-state enum `queued/running/needs_input/paused/completed/failed/cancelled/timed_out` | S2 |
| **Revision entry** | schema — falsified assumption + what the tail adds (SessionMessage family member) | S3/S4 |
| **Goal-status WS frame** | asyncapi — `met` / `waiting_on_user`; rides the since-cursor replay | S6 |
| **Mid-span subagent frames** | asyncapi — `subagent_start`/`subagent_end` gain mid-span message/state events | S3 |
| **`write_sets` + `rationale` on `create_plan`** | schema fields — the planning discipline plan-lint + feasibility verify | S1 |
| **Budget / bounds** | schema + config — attempts + JudgeRounds on goal/plan records; app-level token budget wire | S5 |
| **Pill-state enum** | schema — the 8 states (D14) | S2 |
| **Cancel / restart** | request/response — plan/goal Stop (cancel → `approved` edge) + Play (`resumed_from` generation) | S2 |

The corrected `delegate` action set (`run | status | inbox | inbox_ack | steer | respond | cancel | follow_up | peek`) plus the child-side `message_parent` tool are each authorized as: contract-first schema → seeded Constraint-#6 policy entry (explicit, no wildcard) → handler → anti-pattern tests → a BOM row (delivery brief §5.1). Illegal launch-flag combinations are rejected at `delegate.run`, not silently accepted.

## 8. Consequences, risks & residual under-specification

### Positive
- One goal core removes three divergent trigger/evidence paths; the BOM review gate keeps it one.
- The F2 round-burn fix (awaiting-owner-correction gate) ships **standalone and immediately** (delivery brief §4.7) — a live defect fixed ahead of the integrated landing, with a regression test that fails pre-fix (G-9).
- Verification-first goals compiled from intent + an evidence-fed Judge + typed bidirectional session control with warm resume is, per the design's honest comparison (§ "best-in-class"), a combination no single competitor ships in one sovereign pure-Go binary.

### Negative / tradeoffs
- **New dependency risk** concentrated in go-git — mitigated by the §6 spike gate and the no-go fallback.
- **New attack surface**: a bidirectional agent↔agent control channel and a workspace git layer. Mitigated by per-child ceilings (D15), content-egress policy on boundary-crossing messages (N-10), untrusted-child-text sanitization (MAJ-12, FE-7), `.git` operation-deny (D17), and machine checks executing under the agent's own tool policy + kernel sandbox at runtime (MAJ-13, Constraint #6) — never a privileged bypass.
- **Security-posture shift (D12)**: converting SEC-26's USD cap to a token budget and ignoring `IsPrivilegedAgent` means core-agent turns now debit — a deliberate, operator-locked change of cost posture that removes the privileged exemption.

### Residual under-specification — pre-empting `/grill-spec`
These are genuine gaps a red-team will flag; none re-opens a decision, all are `/plan-spec` closures:

1. **D9 — no runtime net for a wrong compile-time gate.** The feasibility gate vets "semantic judgeability" (an LLM judgment) at compile time and D9 removed the runtime `criterion_unverifiable` verdict. Spec must define: what happens when the gate accepts a criterion the runtime Judge then genuinely cannot verify? (Fail-closed to unmet? Escalate to owner? There is currently no defined fallback.)
2. **D2 — the answer-vs-escalate boundary is undefined.** Nothing governs when a parent answers a child's question itself versus escalating to the human. Spec must define a policy (e.g. a class of questions that MUST escalate, or a confidence/authority test) so a parent cannot silently hallucinate an answer it should have escalated.
3. **D12 — budget mechanics (five sub-gaps).** (a) *Default value:* on a fresh install, what is the default `token_budget`, and does unset mean unbounded (no brake)? (b) *token≠cost:* a token cap does not bound spend uniformly across providers with different $/token — the SEC-26 cost-protection semantics shift silently and need an operator warning. (c) *Brake timing:* does `failed(budget_exhausted)` hard-fail mid-turn, or honor ADR-049 NFR-3's graceful wind-down (finish step + handover summary)? (d) *Atomic debit under concurrency:* owner loop, members, verifiers, and the Judge all debit ONE shared pool — the debit path must be atomic so concurrent turns cannot race the pool past the cap. (e) *No live brake:* the design marks `token_budget` restart-gated and the only live kill switch (`session_messaging.enabled`) neuters messaging, **not** goal-loop token burn — the spec must decide whether a live budget cut is needed.
4. **D3/D13/D10/D17 no-go semantics.** The spec must carry the exact degraded contract for each git-family decision under a spike no-go — not just "fall back to subdir", but what D13 Play-from-commit, D10 isolation, and D17 enforcement each *become*.
5. **D1 — curated context snapshot is named, not defined.** Contents, size bound, and selection policy are unspecified; too thin starves the child, too thick leaks the parent's context and defeats the isolation D1 is buying.
6. **§5.2 / D1 — needs_input warm-resume reconstructability predicate is undefined.** The design says a `needs_input` child is resumable "only if its context can be reconstructed, else swept identically" — the reconstructability test itself needs a definition.
7. **D6 — deep-chain question latency unbounded.** (The default depth is *not* a gap — it is a shipped backstop of 3, `defaultMaxSubTurnDepth`.) The residual is wall-clock: a question N hops deep reaches the human only after N parent decisions, so human-answer latency grows with depth. Spec should consider a direct-escalate shortcut / latency bound.
8. **S4 interlock ordering/atomicity.** The verdict-lands / awaiting-owner-correction-transition / concurrent-steer / boot-sweep interactions must be one specified state machine with named invariants (the transactional tail append N-8 is called out but the surrounding ordering guarantees are not).
9. **Round-accounting reconciliation (supersedes ADR-049 D7).** The round = one *adjudication* (not one turn) redefinition (§5.1 note 2) must be threaded through: the effect on `judge_rounds_max` budgeting, and any code/telemetry that today counts turns-as-rounds (`checkGoalLoopAfterTurn` and its round counters). Spec must state the migration for in-flight round counts.
10. **The two 8-state enums need a specified crosswalk (spine-drift guard).** S2's session-lifecycle enum (`queued/running/needs_input/paused/completed/failed/cancelled/timed_out`) and D14's pill-state enum (`queued/active/waiting_on_user/judge_unavailable/re-planning/judging/done/failed`) are **distinct** wire types with **different** members — one is durable session state, the other is UI display state. The spec must define the mapping (which lifecycle state renders as which pill state, and where `judge_unavailable`/`re-planning`/`judging` — which have no lifecycle counterpart — are sourced) and confirm they are deliberately separate, not an accidental duplicate (DoD-11 anti-drift).
11. **Multi-goal-per-session cardinality (supersedes ADR-049 FR-5).** The design allows a session to hold multiple concurrent goals, each with its own settlement/pill/timers (§5.1 note 3); this supersedes FR-5's "one active `/goal` per session". Spec must define per-goal-id isolation and reconcile with ADR-049's global-cap accounting (which counted "active goal" as one cap slot — does each goal now consume a slot?).

## 9. Confidence Assessment

Roll-up: **the unifying thesis and the S1–S6 spine are High**; **D1, D2, D4, D5, D6, D7, D8, D11, D12, D14, D15, D16 are High**; **the four git-family decisions D3/D10/D13/D17 are Med→High** (raised by the spike resolving GO, §6.1 — residual is caveat-driven implementation detail); **D9 is Med** (the compile-time-only-gate residual, §8.1). No decision is Low — none is being re-opened.

CONFIDENCE: **High** — the unifying thesis (one goal core, three bindings) and the S1–S6 build-once spine.
  Basis        : operator-locked over an extended design conversation; the design's BOM (§9) assigns every behavior a carrier and proves reuse over parallel infra.
  Evidence     : the twice-grilled v2.2 design; the folded findings; ADR-049/052 substrate ([FACT] carried).
  Missing      : the S4 interlock state machine (residual §8.8) — a plan-spec closure, not a direction gap.
  Would improve : a single S4 state-machine diagram with named ordering invariants.

CONFIDENCE: **High** — the goal-core, owner-loop, and session-control decisions (D1/D2/D4/D5/D6/D7/D8/D11/D12/D14/D15/D16).
  Basis        : operator-locked (evidence level 1); each rides a proven substrate (steering.go queues, async_notifier, bus, the shipped goal loop, the ADR-052 verifier).
  Evidence     : the acceptance table G-1..G-16; the BOM reuse rows; the verifier-trigger table.
  Missing      : the §8 residuals (D2 answer/escalate boundary, D12 default budget, D1 snapshot, D6 depth default) — plan-spec.
  Would improve : the goal-loop e2e conformance shard (delivery brief §9.1 t0) executed green.

CONFIDENCE: **Med→High** — the git-family decisions (D3/D10/D13/D17), raised by the resolved spike.
  Basis        : operator-locked as *decisions*; the *mechanism* was contingent on the Phase-0 go-git spike, which has now returned **GO** (§6.1, +4.40 MiB raw / +3.04 MiB stripped, no cgo).
  Evidence     : the spike report (binary-size delta, auto-commit cost, worktree/clone envelope confirmed, NOTICE requirement) — GO with three named caveats.
  Missing      : the caveat-driven detail — media size-guard thresholds, the ff-only-merge → shard/subdir join mechanics (D10/G-16), and the Landlock/bash-policy `.git/` block that D17 requires (a security-lead Phase-1 dependency).
  Would improve : the §9.1 g4/g5/g6 conformance shards executed green on the real go-git substrate.

CONFIDENCE: **Med** — D9 (compile-time feasibility gate only, no runtime criterion_unverifiable).
  Basis        : operator-locked; removes a runtime verdict class in favor of a mint-time gate.
  Evidence     : G-7 tests the reject-at-set behavior.
  Missing      : the fallback when the compile-time gate mis-accepts (§8.1) — no runtime net is defined today.
  Would improve : a spec-defined fail-closed-or-escalate path for a gate false-accept.

## 10. Validation / Next Steps

1. **Grill gate:** `/grill-spec` this ADR — the delivery brief DoD-1 requires a **PASS** before status flips to Accepted. This is a ratification, not a re-litigation; the grill should confirm the §8 residuals are the complete gap set and that no decision was silently weakened.
2. **Spec:** `/plan-spec docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md` — resolve the §8 residuals as spec decisions with BDD/TDD coverage, starting from the §7 contract-surface table (all wire types land in Phase 0, before consuming code).
3. **Phase 0 in parallel:** the go-git spike (§6, gates the git contract) and the F2 round-burn standalone fix (§4.7 of the brief — ships immediately, regression test fails pre-fix) run concurrently from day 0.
4. **Docs pass:** apply the §5.1 / §5.2 supersede notes to ADR-049 and ADR-052; mark the v2.2 design "Implemented" at landing; update AS-IS.
5. **Conformance:** every design diagram (t0/t1/t2/t3/g4/g5/g6/g7 + the §5 boot sweep) must have an executed end-to-end conformance test asserting the drawn path is the observed path (delivery brief §9.1) before the single human-gated `→ main` PR.

# Delivery Brief — Unified Goal/Plan/Subagent System (integrated layers)

**Delivery brief v2 (integrated-layers; completeness-audited) · 2026-07-22** · **Branch base:** `feature/plan-swimlane-board`
**Authoritative design (the *what*):** [`unified-goal-plan-subagent-target-design-v2.2.html`](./unified-goal-plan-subagent-target-design-v2.2.html)
**Assessment (context):** [`…-v2.1-assessment.html`](./unified-goal-plan-subagent-target-design-v2.1-assessment.html)
**Decision log:** memory `adr-052-autonomous-plan-execution.md` — 17 interview decisions D1–D17 + folded review findings (grill 31 · architect F1–F9 · third-pass N-1–N-15)

> **This brief is the delivery contract.** It does not restate the design — it sequences, integrates, and *proves* its delivery. The design is the *what*; this is the *how*, the *order*, and the *proof-of-done*. The v2.2 design ships as **one coherent system landed once**, not five ADRs dribbled to users over time.

---

## 1. The goal (one sentence)

Deliver the complete v2.2 unified goal/plan/subagent system — **verification-first goals compiled from intent, an evidence-fed Judge, an owner-loop planner/re-planner, a typed session-control plane, a git-backed workspace/evidence layer, and parallel-worktree execution** — as **one integrated, conformance-proven system built in dependency-ordered layers, integrated continuously onto one epic branch, and landed via a single human-gated `→ main` PR** with every quality gate green.

## 2. Definition of Done (the contract — immutable)

The delivery is done when **all** of the following hold:

- **DoD-1 — One ratified system, landed once.** The v2.2 design is ratified as **one ADR (ADR-053, the unified goal/plan/subagent ADR)** — grilled (`/grill-spec` verdict **PASS**), with **one contract set** — explicitly **superseding-in-part ADR-049 D4** (headless-hybrid owner) **and the ADR-052 chat-goal triggers**. The system integrates continuously across the layers of §4 and **lands as one complete `→ main` PR** — nothing ships to users layer-by-layer.
- **DoD-2 — Full traceability.** **Every** D1–D17 decision **AND every normative grill / architect / third-pass finding folded into v2.2** (~46 findings) is implemented as specified, each traceable to code + a test. The folded-findings appendix (§13) maps each still-homeless finding to its delivery layer.
- **DoD-3 — v2.1 blockers resolved in code (not doc):** isolated-but-linked sessions; parent-routed questions; git-backed Judge evidence (real diffs at member **and** plan scope); supersede + targeted-retry correction; conversational goal/DoD confirm.
- **DoD-4 — F2 round-burn fixed (the ONE true standalone, ships immediately).** The live bug — the engine re-judges an unchanged all-terminal-but-unmet state on every idle tick (~30 s), burning a `JudgeRound` each time — is fixed by the **awaiting-owner-correction gate**, with a regression test that **fails pre-fix**. This is the only piece that ships to users ahead of the integrated landing (§4.7).
- **DoD-5 — All quality gates green (§9)** on the ci-omnipus worker: `go-test`, `go-vet`, `gofmt`, `golangci-lint`, `govulncheck`, `contracts`, `spa` (typecheck + vitest), and the full `e2e` matrix **including the new goal-loop e2e** (set-goal → compile → confirm → work → claim/idle → verdict → done) on a fresh install.
- **DoD-6 — Fresh-install live E2E** on the internal port passes the goal/plan/subagent checklist (§9.1), verified in a real browser.
- **DoD-6b — Design-conformance gate (§9.1) green:** every v2.2 diagram (t0/t1/t2/t3/g4/g5/g6/g7 + §5 boot sweep) has an executed end-to-end test asserting the drawn path is the observed path — the system provably behaves *as designed*, node-by-node.
- **DoD-6c — Delegation/tool-surface (§5) delivered:** the corrected `delegate` action set (§5.1), the child `message_parent` tool, the typed `SessionMessage` transport with per-child caps + durable inboxes, checkpointing (message + git-commit boundary), and the 3P fire-and-collect contract — all contract-first, with tests.
- **DoD-7 — Review gates clean.** `/grill-code` on the full epic diff returns no unresolved BLOCKER/MAJOR; the 7-reviewer gate (per layer) + final 14-reviewer sign-off (whole epic) both clean, or every finding tracked with a dated issue.
- **DoD-8 — Hard Constraints hold** (single Go binary; pure Go / no CGo in security paths; <10 MB security-feature RAM overhead; contract-first wire formats; no default-policy fallback; graceful degradation). **The go-git footprint is measured against Constraint #3 and passes, or go-git is not adopted (§4.3 spike gate).**
- **DoD-9 — Docs synced:** ADR-049/052 superseded-in-part notes; AS-IS architecture updated; the v2.2 design marked "Implemented"; tracked issues for any accepted-with-issue deferrals.
- **DoD-10 — Operability delivered (§8):** the full `session_messaging` config schema (~14 keys) exists with the documented defaults + reload semantics; the **global kill switch** (`session_messaging.enabled` + `wake_enabled`) neuters messaging live without a rebuild; **retention** is explicit (message 90 d / audit-subsystem policy / 7-day undelivered) — nothing grows unbounded by omission.
- **DoD-11 — Shared spine is build-once (§4.1):** the unified goal record, the durable session record + 8-state enum, the SessionMessage family, the budget triple, and the claim/marker family are each **one implementation** consumed by multiple layers. The BOM review-gate (design §9) is clean — no PR introduces a parallel/duplicate of any spine artifact.

**The DoD is the user's contract. It is never weakened to pass — if a criterion proves unachievable, the plan fails honestly with the reason, or comes back to the operator.**

## 3. Non-negotiable guardrails (apply to every wave + every subagent)

- **Worktrees live on the workspace drive** (`/home/dev/worktrees/<agent>`), **never `/tmp`** (system disk is tight). Free disk before starting.
- **Never admin-merge / auto-merge to `main`.** A human reviews and approves the single `→ main` PR regardless of CI. Feature integration happens on `feature/plan-swimlane-board` (or a fresh epic branch); `→ main` is human-gated.
- **Authorship:** every commit authored + committed as the operator's GitHub identity (no-reply email); **no agent `Co-Authored-By` trailers.** Verify before every push.
- **CI is the authority for Go test/build** — run on ci-omnipus, never the full suite in the devpod (OOM). Parse the `RESULT` line + HEAD sha; distrust wrapper exit codes and flake-filter false-greens.
- **Contract-first (Constraint #8):** every new wire type (the §6 list) is defined in `contracts/*.yaml` **before** Go/TS; discriminated unions (SessionMessage) hosted **inline** in `openapi.yaml` (ADR-034 precedent). Regenerate + commit generated artifacts atomically. **All new wire schemas land in Phase 0** — before any consuming code.
- **No parallel systems (BOM discipline, design §9):** every mechanism reuses an existing component (steering.go queues, async_notifier, bus, criteria type, plan skill, verifier infra) or is a justified NEW row in the BOM table. A mechanism absent from the BOM is a review finding.
- **Shared-spine anti-drift:** the §4.1 build-once artifacts are owned as **single implementations**. A second goal store, a second messaging envelope, a second claim-marker parser, or a second budget path is a blocking finding — "which spine artifact is this?" and "none" is a finding.
- **The lead commits/merges; wave agents do not commit.**

---

## 4. Delivery model — integrated layers, one landing

We **build in dependency order** but **integrate continuously** onto one epic branch, and land the whole system as **one complete, conformance-proven `→ main` PR**. Nothing ships to users until the whole system is coherent. The five old "053a–053e" ADR slices are **retired as ship-units** — they map onto the four layers below (traceability note at §4.2). Only the **F2 round-burn fix** ships ahead, standalone (§4.7).

### 4.1 The shared spine — build-once artifacts (Phase 0, designed together)

The anti-parallel-systems guarantee. These six seams are **designed together in Phase 0 and owned as single artifacts**, each built ONCE and consumed by multiple layers. This is what stops the system from fragmenting into parallel implementations under parallel waves.

| # | Shared-spine seam | Built once as | Consumed by | Anti-drift note |
|---|-------------------|---------------|-------------|-----------------|
| S1 | **Unified goal record** | one schema: prompt + goal definition + acceptance criteria (3 kinds: machine / behavior / prose), **authored 2 ways** (chat = agent-compiled from intent; task = explicit at creation) | chat goal (§1) · task (§2) · plan DoD (§3) | ONE criteria model, two authors — never a separate goal store for tasks (goal lives in the task record) |
| S2 | **Durable session record + 8-state lifecycle enum** | one persisted per-entity JSONL record; enum = `queued / running / needs_input / paused / completed / failed / cancelled / timed_out` | idle settlement · plan all-terminal detection · `blocked_by` · boot sweep (§5) · Agent-View | one source of truth for "is anything still working?" — replaces the in-memory chat-scoped map |
| S3 | **SessionMessage envelope family** | one inline `oneOf`+discriminator envelope (ADR-034), incl. **revision-entry, handback, checkpoint, goal-status as ONE family** | child inbox · parent steer · goal pill event feed · plan tile · board aggregation · human parity | one steering/injection path, one envelope — never a second channel |
| S4 | **Owner ↔ Judge ↔ messaging ↔ plan-engine interlock** | the wiring contract: Judge feedback = a `steer`; member telemetry = the owner's inbox; waiting-on-owner = `question(wait=true)`; idle settlement reads session lifecycle states | plan correction loop · trigger detection · pause | designed as one interlock so behaviors in Phase 2 wire to real primitives, not stubs |
| S5 | **Budget triple** | attempts (per member/task) + JudgeRounds (per goal/plan) + **one app-level OVERALL token budget** (D12, no money caps, no per-plan budgets, no core-agent exemption) | every scope (owner loops, members, verifiers, Judge) | three axes, never conflated; one token debit path from provider usage |
| S6 | **Claim/marker family** | `[goal:evidence]` + `TASK_STATUS` + `GOAL_STATUS: met` / `waiting_on_user` — **parser co-located with its teaching fragment** (the gate-2 B2 lesson) | rung-0 gate · trigger detection · waiting-on-user pause | one marker family; adding a marker without amending its teaching fragment must be structurally awkward |

### 4.2 The layer dependency graph (layers, not ship-order)

```
                    ┌──────────────────────────────────────────────────────┐
  PHASE 3           │  WHOLE-SYSTEM CONFORMANCE → one human-gated → main PR  │
  (integrate+prove) │  §9.1 diagram gate · 14-reviewer · full CI · live E2E · docs
                    └───────────────────────▲──────────────────────────────┘
  PHASE 2           triggers/pause/pills · Judge evidence feed · owner loop +
  (behaviors, on    correction · goal compilation + budget · blocked-check fix
   REAL substrate)  — each wired to real messaging + real git evidence, NO stubs
                    ─────────────────────────▲──────────────────────────────
  PHASE 1           git evidence layer · session-message transport + durable
  (substrate,       records + tool surface · boot-sweep/crash-recovery ·
   no upward deps)  plan-lint / write-set enforcement  (parallel worktree waves,
                    integrated + gated TOGETHER)
                    ─────────────────────────▲──────────────────────────────
  PHASE 0           SHARED SPINE — one ADR + one contract set (§4.1):
  (design together, unified goal record · durable session record + 8-state enum ·
   contract-first)  SessionMessage family · budget triple · claim/marker family ·
                    ALL new wire schemas (§6)      ∥  go-git spike (gates §3d contract)

  ── standalone, ships immediately, independent of all of the above ──►  F2 round-burn fix
```

**Traceability — old ADR slice → layer:** 053a (triggers/pills/F2) → Phase 2 behaviors (+ F2 standalone §4.7); 053b (git layer) → Phase 1 substrate; 053c (session-control plane) → Phase 1 (transport/records/tools) + Phase 2 (semantics); 053d (owner loop + correction) → Phase 2 behaviors; 053e (goal compile + budget) → Phase 2 behaviors + §7 frontend (Usage screen).

**Critical path:** Phase 0 spine → Phase 1 substrate → Phase 2 behaviors → Phase 3 conformance. The go-git spike and the F2 fix hang off in parallel from day 0.

### 4.3 Phase 0 — Design the shared spine together (one ADR, one contract set)

Ratify the v2.2 design as one ADR and land the entire wire surface **before any consuming code**. The go-git spike runs in parallel and **gates the git-checkpoint / §3d contract**.

| Deliverable | DoD line | Notes |
|-------------|----------|-------|
| **ADR-053 ratification** (Albert, ratification mode) | one concise ADR, grilled **PASS**, per-decision confidence, superseding ADR-049 D4 + ADR-052 triggers; `/plan-spec` into a testable spec (US/FR/BDD/TDD/traceability) | design already grilled 2× + interview-locked → ratify, don't re-open |
| **Shared-spine schemas (§4.1 S1–S6)** contract-first | all six spine artifacts defined as schemas + regen committed atomically, **before** any Go/TS | anti-drift: one schema each |
| **All new wire types (§6)** land | every §6 schema in `contracts/`, generated types committed, `make verify-contracts` green | SessionMessage inline `oneOf` |
| **go-git spike** (long pole, day 0) | go/no-go report: binary-size delta vs Constraint #1/#3 (`<10 MB`); auto-commit cost on a realistic media-heavy `work/`; the merge / linked-worktree envelope (go-git has **no** `worktree add` — confirm the isolation-ladder + clone-fallback plan is executable); **Apache-2.0 NOTICE ride-along** (OBS-3) | **gates the §3d git contract**; fail → fall back to system-git-when-present + subdir isolation and re-scope the git layer, but the trigger fix (§1–2) and session plane (§4/§5-design) land regardless |

### 4.4 Phase 1 — Substrate (no upward deps; everything needs it)

Built in **parallel worktree waves, integrated + gated together**. The behaviors of Phase 2 must run on the REAL substrate — no stubs.

| Component | Design § | Deliverable / DoD line | Conformance / G |
|-----------|----------|------------------------|-----------------|
| **Git evidence layer** | §3d | embedded go-git (if spike passes): `work/` auto-init hidden repo, **write-set-scoped serialized boundary commits** (task·attempt·agent), per-attempt diff evidence, `.git` **deny-by-operation** (allow log/blame/show/diff; deny commit/amend/rebase/rm — D17), HEAD-divergence → "evidence integrity lost", size guard (skip/warn above N files/MB — OBS-3), **secrets-in-history purge/gc + sensitive-value scan on commit** (MIN-5), **nested-repo detection + signalled degraded contract** (MIN-6) | **G-15**; §9.1 g4 |
| **Session-message transport + durable records + tool surface** | §4 | typed `SessionMessage` over MessageBus (reuses steering.go queues + async_notifier wake + bus); per-child unacked ceilings (D15); durable inbox keyed to chat/plan id (D16); **durable session record + 8-state enum** persisted JSONL (S2); the corrected `delegate` action set + child `message_parent` (§5.1); **content-egress policy** on messages crossing agent/channel boundaries (N-10); **sync-delegate `wait=true` reject / human-route opt-in** (P2M-14/MIN-3); **needs_input TTL 24 h + escalation ladder** (MAJ-4/G-6); **board task ↔ session 1:N aggregation** (O-1) | **G-6**; §9.1 g6/g7 |
| **Boot-sweep / crash-recovery** | §5 | at boot, every persisted non-terminal session with no live turn → `failed(interrupted)` within N s (carries last checkpoint + undelivered messages); `session.failed` emitted → plan re-judges/re-dispatches, idle settlement fires; **live-upgrade re-baseline** of in-flight goals on a trigger-semantics change (N-15) — one sweep, two triggers | **G-13** (= A-17); §9.1 §5 boot sweep |
| **Plan-lint / write-set-disjointness enforcement** | §3c | `write_sets` + `rationale` schema on `create_plan`; plan-lint **REJECTS at approve** any two parallel streams with overlapping write paths, and any join point without an authored merge/assemble member; a conflict at merge → **plan-correction event**, never silent | **G-16**; §9.1 g4/g5 |

### 4.5 Phase 2 — Behaviors (on the real substrate, no stubs)

Each behavior is wired to **real messaging (Phase 1 transport)** and **real git evidence (Phase 1 git)** — never a mock.

| Component | Design § | Deliverable / DoD line | Conformance / G |
|-----------|----------|------------------------|-----------------|
| **Triggers / pause / pills** | §1, §2, "verifier triggers", "idle settlement" | claim-or-idle adjudication (event-driven timers + ~60 s quiet-window debounce, per goal-id); `GOAL_STATUS: waiting_on_user` pause (typed marker, no prose classifier) → no verdict/round; bounce economics (1st bare-claim free, 2nd costs); pill states drive the UI feed | **G-1/G-2/G-3/G-4/G-5** |
| **Judge evidence feed** | §2, §6, BOM | Judge fires per the trigger table; deterministic rungs first, AND-combine; **blocked-check fix — Judge returns "unable to verify" when a machine check could not run, NEVER scored as absent evidence**; idle/plan Judge reads real write-set-scoped diffs (Phase 1 git) | §9.1 t0/t2 |
| **Owner loop + correction** | §3, §3b | persistent owner session (supersedes ADR-049 D4 — re-opened on purpose); auto-reset + re-dispatch of live-round failed members (excludes frozen); **append-only correction + SUPERSEDE + TARGETED RETRY** (D4) with a revision entry; **transactional tail append** (all-or-nothing, N-8); **owner-gaming-DoD guards** (ladder weights deterministic rungs, post-unmet artifacts flagged post-hoc — N-2); Play = new `resumed_from` generation, resume failed/cancelled members from **last git commit** (D13); **no per-member start/cancel/resume** (D7) | **G-9/G-10/G-11/G-12** |
| **Goal compilation + budget** | §1, §6 | engine-invoked SMART compiler (schema-validated turn, co-located parser); **feasibility gate vets reachability AND semantic judgeability** (D9, no runtime `criterion_unverifiable`); echo-&-confirm in chat (D11); **re-statement = diffed amendment, never silent recompile** (N-6); `/goal clear` cancels compilation + verifier + inerts claim trigger (N-12); **app-level OVERALL token budget** debits all workloads, ignores `IsPrivilegedAgent`, brake = `failed(budget_exhausted)` (D12) | **G-7/G-8/G-14** |

### 4.6 Phase 3 — Whole-system conformance (integrate + prove + land)

| Deliverable | DoD line |
|-------------|----------|
| **§9.1 diagram-conformance gate on the integrated whole** | every diagram (t0/t1/t2/t3/g4/g5/g6/g7 + §5) has an executed conformance test asserting the drawn path is the observed path |
| **14-reviewer Opus sign-off** (`model: opus`) | whole epic diff clean, or every finding tracked with a dated issue |
| **Full CI + fresh-install live E2E** | all §9 gates green on ci-omnipus; §9.1 live checklist passes in a real browser on the internal port |
| **Docs sync** | ADR-049/052 superseded-in-part; AS-IS updated; v2.2 marked Implemented |
| **The single `→ main` PR** | human-gated, all issues closed via keywords in the PR body |

### 4.7 The one true standalone — F2 round-burn fix (ships immediately)

The only piece that ships ahead of the integrated landing. It is a **live, shipped defect** independent of the redesign: on an all-terminal-but-unmet plan the engine re-judges the unchanged state every ~30 s idle tick, burning a `JudgeRound` each time until rounds are exhausted while the owner deliberates. Fix = the **awaiting-owner-correction gate** (the verdict moves the plan to `awaiting-owner-correction`; the engine stops re-judging until the owner appends a correction or a budget is spent). Small standalone PR on the current branch, **regression test fails pre-fix** (DoD-4, acceptance **G-9**).

### 4.8 Self-containment note — integrate, not ship, incrementally

The former "053a self-contained" idea is honored as a **build-early, integrate-late** rule, not a ship-early one. Trigger and marker semantics (§1 claim-or-idle, `GOAL_STATUS` markers, pill taxonomy) **can be built early** — but their durable **persistence** completes with the Phase 1 durable session record, and their **real-diff evidence** completes with the Phase 1 git layer. So an early trigger build has nothing real to judge or persist until the substrate lands. We therefore **integrate continuously** (early code merges onto the epic branch behind the substrate as it arrives) and **do not ship** any layer to users on its own. The only exception is the F2 fix (§4.7), which needs neither.

---

## 5. The session-control & delegation epic (largest slice — Phase 1 substrate + Phase 2 semantics)

The biggest slice, its own requirement families (L-/M-/V-/P2M-/H-/S-, `subagent-session-control-visibility-requirements.md`). One primitive — session-scoped, typed, schema-validated `SessionMessage` over the existing `pkg/bus` — derives every control and visibility surface. The waiting-on-owner pause **is** one message type: `question(wait=true)`.

### 5.1 Tool surface — the CORRECTED delegate action set

**Repo today = `run | status` only** (`pkg/tools/delegate.go`: dispatch rejects anything else with `invalid action %q: must be "run" or "status"`). Target surface (design §8) is the real **~9 actions plus one child tool**:

| Action | Direction / role | Purpose |
|--------|------------------|---------|
| `run` | parent → new child | spawn (existing) |
| `status` | parent | event-driven V-1 payload from checkpoints + messages (was poll-scrape; `task_id` survives only as a deprecated compat alias) |
| `inbox` | parent | drain the child→parent typed inbox (progress · checkpoint · artifact · blocker · question/decision_request(`wait`) · error · handback) |
| `inbox_ack` | parent | explicit ack; runtime dedupes by `message_id` before surfacing; acked messages persist in the audit log |
| `steer` | parent → child | mid-run injection at the child's next tool boundary (skip-remaining-batch semantics identical to chat steering) |
| `respond` | parent → child | answer a `question` by `correlation_id`; out-of-order answers safe; **3P: spawns a NEW corrective session** (orig prompt + answer folded in, D5) |
| `cancel` (**soft**) | parent → child | cooperative stop at tool boundary + checkpoint flush inside grace; hard `RequestCancel` remains the backstop after grace |
| `follow_up` | parent → child | **warm resume** of the SAME session with retained context (native); 3P: cold — spawns a new session carrying the prior result |
| `peek` | human ↔ session | Agent-View parity read without attach |
| **`message_parent`** (child tool) | child → parent | first-class child-side tool: `progress`/`checkpoint`/`artifact`/`blocker`/`question`/`handback`; `question(wait=true)` parks the child in `needs_input` (native-only; 3P never advertises it) |

Launch flags collapse to two profiles with a published legality table (MAJ-7): **`utility`** (outcome / none / progress_only — fire-and-collect; maps today's one-shot) and **`specialist`** (checkpoints / parent_and_human / full — collaborating native worker; a 3P child degrades to fire-and-collect). Illegal combos are **rejected at `delegate.run`**, not silently accepted. Each new action/tool: contract-first schema → seeded tool-policy entry (Constraint #6, explicit, no wildcard) → handler → anti-pattern tests → a BOM row.

### 5.2 Transport, identity, checkpointing, degradation

- **Transport (Phase 1):** one `SessionMessage` envelope (inline `oneOf`+discriminator, ADR-034); per-type size/rate caps (child: 10 msgs/min, 32 KiB, depth ≤5; steer: 6/min, 16 KiB); **per-child unacked ceiling** (D15 — 20 open question+blocker per child, so one noisy child can't starve siblings); never-silent-drop back-pressure (fail to the child as a tool error); durable inbox keyed to chat/plan id (D16); dedupe by `message_id`; **events ≠ messages** (un-acked ring-buffered fan-out share the envelope, never the ack/ceiling semantics).
- **Identity & durability (Phase 1):** isolated-but-linked children (D1) — own durable `SessionID`, curated context snapshot at spawn, typed channel the only bridge (no shared transcript, FR-6a dropped); `DelegateTaskState` → durable session record (S2). Terminal states are **immutable result records**; `follow_up`/Play mint a new generation via `resumed_from` (MAJ-1/N-7).
- **Checkpointing (Phase 1/2):** the `checkpoint` message type + the **git-commit-at-attempt-boundary** (Phase 1 git) is the durable checkpoint that powers Play-from-commit (D13) and the §5 boot-sweep recover-to-checkpoint. Warm-resume-across-a-process-boundary is answered explicitly: `needs_input` is preserved as resumable **only if** its context can be reconstructed, else swept identically.
- **Control semantics (Phase 2):** parent-routed questions (D2 — child asks parent; parent answers-or-escalates; only the human's direct session/plan owner asks the human, **conversationally, no per-question reply card**); one-hop-to-parent correct by design, depth configurable (D6); **3P = honest fire-and-collect** (D5); human-surface **untrusted-child-text sanitization** (MAJ-12 — plain text / sanctioned markdown, no raw HTML, non-clickable links, untrusted-origin chrome); ownership derives from the spawn edge (union incl. the human for top-level sessions), least-privilege + audited.

---

## 6. New wire types — contract-first, all land in Phase 0

Every one is defined in `contracts/*.yaml`, regenerated, and committed **before** its consuming code (Constraint #8). State target for every row: **lands in Phase 0.**

| Wire type | Shape | Notes |
|-----------|-------|-------|
| **SessionMessage** | inline `oneOf` + discriminator in `openapi.yaml` | ADR-034 precedent; the envelope family carries revision-entry, handback, checkpoint, goal-status |
| **Unified goal / criteria record** | schema | prompt + goal definition + acceptance criteria (machine/behavior/prose); authored 2 ways (S1) |
| **Durable session-lifecycle record** | schema | **8-state enum** `queued/running/needs_input/paused/completed/failed/cancelled/timed_out` (S2) |
| **Revision entry** | schema | falsified assumption + what the tail adds; part of the SessionMessage family |
| **Goal-status WS frame** | asyncapi | `met` / `waiting_on_user` (drives the pill; rides since-cursor replay) |
| **Mid-span subagent frames** | asyncapi | `subagent_start`/`subagent_end` gain mid-span message/state events between the brackets |
| **`write_sets` + `rationale` on `create_plan`** | schema fields | the planning discipline the plan-lint + feasibility gates verify |
| **Budget / bounds** | schema + config | attempts + JudgeRounds fields on goal/plan records; app-level token budget wire for the Usage screen (D12) |
| **Pill-state enum** | schema | `queued / active / waiting_on_user / judge_unavailable / re-planning / judging / done / failed` (8 states) |
| **Cancel / restart** | request/response | plan/goal Stop (cancel → `approved` edge) + Play (`resumed_from` generation) |

---

## 7. Frontend-lead deliverables (its own list — Phase 2/3 UI, contract-first from §6)

| # | Deliverable | Decision |
|---|-------------|----------|
| FE-1 | **Goal pill relocation** — full-width box → **bottom-right** pill; **all 8 states** (incl. `re-planning`, `failed`), **per-goal-id** (a session with 2 goals shows 2 pills/timers); click expands criteria + latest per-criterion verdict | design §1 |
| FE-2 | **Plan tile → Graph view** — clicking a plan tile auto-switches to the Graph view (deselect leaves the view) | D7 |
| FE-3 | **In-chat question rendering** — the human answers in normal chat; **NO per-question reply card**, no approval/correlation UX — parent routes correlation | D2 |
| FE-4 | **No-per-member-controls button matrix** across Board / List / Graph — plan members show status only; ▶ Run / ■ Stop / Play exist **only** on standalone tasks | D7 |
| FE-5 | **ActivityPanel → Agent-View session list** — the open/close span brackets grow into a live session list (peek / reply / steer / stop, gated attach) | design §4 (H-1..H-6) |
| FE-6 | **Usage-screen token-budget setting** — operator-set app-level OVERALL token budget + verifier/owner/member spend accounting | D12 |
| FE-7 | **Untrusted-child-text sanitization** — child message bodies/subjects/artifact notes/previews render as plain text or sanctioned markdown, no raw HTML, links non-clickable/confirmation-gated, untrusted-origin chrome always visible | MAJ-12 |
| FE-8 | **Conversational goal/DoD confirm surface** — the compiled goal (incl. literal commands) is echoed in chat and confirmed by a chat reply; the re-statement amendment diff renders in chat — no form/modal | D11 |

---

## 8. Operability — config keys, kill switch, retention (its own deliverable, DoD-10)

Design §7. One `session_messaging` section in `config.json`; every numeric tunable maps to a key — nothing is a magic constant. **Global kill switch** so a misbehaving rollout is neutered without a rebuild.

| key | default | reload |
|-----|---------|--------|
| `session_messaging.enabled` | **true — global kill switch** (false neuters all messaging) | live |
| `session_messaging.wake_enabled` | **true — wake-path disable** ("wake storms" risk) | live |
| `child_send_rate / body / depth` | 10 msgs/min · 32 KiB · depth 5 | live |
| `inbox_unacked_max / per_type_ceiling` | 200 · 20 open question+blocker (per-child) | live |
| `steer_rate / steer_body` | 6/min · 16 KiB | live |
| `cancel_grace / needs_input_ttl` | 30 s · 24 h | live |
| `wake_debounce / wake_max_per_hour` | 15 s · 4/h | live |
| `idle_quiet_window` | ~60 s | live |
| `token_budget` (app-level OVERALL, D12) | operator-set in the Usage screen; covers ALL workloads, no core exemption; brake = `failed(budget_exhausted)` | restart-gated |
| `attempts_max / judge_rounds_max` | 3 native · 6 · rounds N | restart-gated |
| `message_retention / audit_retention` | inherits session retention (90 d) · audit-subsystem policy | restart-gated |

**Retention is stated, not implied:** message store inherits the 90-day session-retention (day-partitioned JSONL); the audit stream inherits the audit subsystem's policy; the **7-day undelivered-message** window (L-6) is a separate, shorter horizon. All three named so nothing grows unbounded by omission.

---

## 9. Quality gates (every gate, every layer)

**Per-layer (before its integration) AND on the whole epic diff (before `→ main`):**

```
# Backend (ci-omnipus worker — never full suite in devpod)
gofmt -l .                                   # 0
golangci-lint run --build-tags=goolm,stdjson # exit 0
go test -tags goolm,stdjson -count=1 ./...   # exit 0  (CI authority)
go build -tags goolm,stdjson ./...           # exit 0
govulncheck ./...                            # 0 vulns

# Frontend
npm run typecheck                            # tsc -b, exit 0
npx vitest run                               # exit 0

# Contracts
make verify-contracts                        # exit 0 (no drift)

# E2E (ci-omnipus e2e gate — sharded Playwright)
runci.sh <sha> e2e                           # ALL SHARDS GREEN
  + NEW goal-loop shard: set-goal → SMART compile → conversational confirm
    → worker turns → claim/idle trigger → Judge verdict → done; plus a
    plan: Execute → members → unmet DoD → owner re-plan (supersede/retry)
    → done; run against a fresh install.

# Review gates (MANDATORY, per CLAUDE.md — model: opus override)
7-reviewer Opus gate  after each LAYER (before its integration merge)
14-reviewer Opus gate on the whole epic diff (before the → main PR)
/grill-code on the epic diff — no unresolved BLOCKER/MAJOR
```

### 9.1 Design-conformance gate — prove the diagrams work AS DRAWN (mandatory)

The v2.2 diagrams **are** the behavioral spec. A separate, explicit gate proves the running system walks each target flow exactly as designed — not "units pass," but "the drawn path is the observed path." **Every design diagram → a named BDD scenario → an executed end-to-end conformance test that asserts each node/edge actually fired, in order.**

| Design diagram | Conformance scenario (must be an executed test, not prose) |
|----------------|------------------------------------------------------------|
| **t0 · chat goal** | set `/goal` → SMART compile → **conversational confirm in chat** → worker turn → **claim OR idle trigger** (assert *which*, and that non-claim question-turns **pause** without a verdict/round) → Judge verdict → done; assert pill walks active→judging→done and that `/goal clear` cancels the verifier **and** any in-flight compilation. |
| **t1 · standalone task** | ▶ Run → claim → evidence-gate (bare claim → free steer, 2nd → attempt) → ladder → done; ■ Stop cancels turn+verifier. |
| **t2 · plan lifecycle** | Execute → gated approve → members dispatch per DAG → all-terminal → plan Judge → unmet → **awaiting-owner-correction gate holds (no round burned on unchanged state — the F2 proof)** → owner appends → re-judge → done; **Play resumes a cancelled member from last git commit (D13)**; assert **no per-member start/cancel/resume exists (D7)**. |
| **t3 · planning & re-planning** | intent+refs → owner plans (checklist) → execute → unmet-all-done → owner re-plans → **supersede a done member (D4)** and **targeted-retry a frozen-transient member (D4)** → transactional append (kill mid-append → pre-append DAG) → done. |
| **g4 · parallel streams (lint)** | disjoint write-sets → lint passes; overlapping → lint **rejects at approve**; **exploratory member → own git worktree (D10)**, merge at join surfaces a real conflict as a plan-correction event. |
| **g5 · worked topologies (shard+assemble + software worktrees)** | run BOTH design topologies on ONE git-based model: (a) software plan — serial contract-first member → two lint-disjoint worktree streams → a merge member leaving one green tree; (b) report-with-workbook — serial shard-schema member → three disjoint-shard streams → ONE assemble member building the `.xlsx` from shards. Assert the join is a first-class member with its own criteria in both, and the isolation rung matches runtime capability (worktree → go-git clone → subdir). *(New row — was not covered; NOT foldable into g4, which only proves the lint. May share a shard with g4 but must add the assemble-topology assertions.)* |
| **g6 · session control** | spawn child (isolated-but-linked, own SessionID, context snapshot) → child `message_parent(question)` → **parent answers or escalates to human in chat (D2)** → `respond`/`steer` lands at the child's next tool boundary → `handback`; **3P child → fire-and-collect, respond = new corrective session (D5)**; **per-child ceiling (D15)** — one noisy child cannot starve a sibling; durable inbox survives a parent Stop/Play (D16). |
| **g7 · session round-trip (sequence)** | the design's "full round trip" diagram: **mid-run `steer` + a blocking `question(wait=true)` answered by `respond` without restarting the child + a clean `handback` into the evidence gate** — assert the child kept warm context (no cold restart), the answer routed by `correlation_id`, and the handback's `result_so_far/artifacts[]/open_questions[]` fed the rung-0 gate. *(New row — the bidirectional-control sequence; distinct from g6's control-surface enumeration.)* |
| **§5 · boot sweep** | kill -9 mid-plan → non-terminal sessions → `failed(interrupted)` within N s → plan re-judges/re-dispatches → idle settlement fires again → **no wedge** (the CRIT-1 proof); an in-flight goal predating the upgrade is re-baselined (N-15). |

**Rule:** a layer is not Done until its diagrams' conformance scenarios are executed green (BDD in the spec, realized as Go integration tests + at least one real-LLM e2e per user-facing flow). A diagram with no passing conformance test is an unproven claim, not a delivered behavior.

**Live E2E checklist (fresh install, internal port only — never `0.0.0.0:8080`):**
onboarding → set a chat goal (SMART compile shown, confirm in chat) → worker asks a question (parent-routed, answered in chat) → completion + verdict → **pill states (active / judging / waiting-on-user / queued / judge_unavailable / re-planning / failed / done)** → create a plan, Execute, watch members, unmet DoD → owner re-plans (supersede a done member / targeted-retry a frozen one) → Stop → Play resumes from last git commit → Usage screen shows the app-level token budget + verifier spend → zero console errors.

---

## 10. Execution model — maximum-parallelism worktree waves on ONE epic branch

**Per layer, run the wave pattern** (CLAUDE.md implementation rule), worktrees on the workspace drive, integrating onto the single epic branch:

1. **Contracts commit** (Phase 0) — the whole §6 wire surface lands first, once, for the whole epic.
2. **Dev wave:** up to **8 parallel dev agents** in isolated worktrees (`/home/dev/worktrees/<agent>`), decomposed by the plan-lint disjoint-write-set rule so branches merge cleanly. backend-lead / frontend-lead / security-lead / qa-lead per task type (dev = repo-default sonnet).
3. **Review wave:** **7 parallel Opus PR-reviewers** (`model: opus`) over the layer diff (correctness, silent-failure, tests, types, comments, architecture, security). All clean or each finding tracked.
4. **Fix wave:** parallel fix agents on reviewer findings; lead reconciles seams + commits.
5. **Integrate** the layer onto the epic branch; run the full §9 gate set on the worker. **Integrate — do not ship** (§4.8).

**Cross-layer parallelism (the speed win):** the go-git spike and the F2 fix run **concurrently from day 0**. Phase 0's contract set unblocks all of Phase 1. Phase 1's four substrate components run as **parallel worktree waves and are gated together**. Phase 2 behaviors start once their substrate seam is stable and wire to the REAL substrate. Critical path = **spine → substrate → behaviors → conformance**, with the spike/F2 hanging off in parallel.

**Final:** whole-epic **14-reviewer Opus sign-off** + `/grill-code` + all §9 gates green → open the single human-reviewed PR to `main` (never admin-merge).

## 11. Sequencing summary (what starts when)

| Phase | Runs in parallel |
|-------|------------------|
| **P0 (day 0)** | go-git spike · **ADR-053** ratify+grill(PASS)+spec · **all §6 contracts land** · **F2 round-burn fix PR** (standalone, ships immediately) |
| **P1** | spike report lands → git contract confirmed · **Phase 1 substrate** — 4 parallel worktree waves (git · session-transport+records+tools · boot-sweep · plan-lint), integrated + gated together |
| **P2** | **Phase 2 behaviors** on the real substrate — triggers/pause/pills · Judge evidence feed · owner loop + correction · goal compile + budget · blocked-check fix · frontend deliverables (§7) |
| **P3** | whole-epic 14-reviewer sign-off · `/grill-code` · full §9 gates + §9.1 conformance gate · fresh-install live E2E · docs sync |
| **P4** | single human-reviewed `→ main` PR (all issues closed via keywords) |

## 12. Acceptance-criteria coverage — G-1..G-16 → delivery home

Every design §8 acceptance criterion has a provable home. (Audit found G-6/G-8/G-15/G-16 only partially covered; all now anchored.)

| G | Criterion (abbrev.) | Delivery home / phase | Conformance |
|---|---------------------|-----------------------|-------------|
| **G-1** | claim invokes Judge exactly once; met→done, unmet→round+steer | Phase 2 triggers + Judge feed | t0 |
| **G-2** | idle settlement fires one adjudication, consumes a round, re-arms on new activity | Phase 2 triggers | t0 |
| **G-3** | claimless idle judging bypasses rung-0, judges persisted evidence (needs real diffs) | Phase 2 triggers **on** Phase 1 git | t0 |
| **G-4** | bounce economics — 1st bare claim free, 2nd costs | Phase 2 triggers (evidence gate) | t0/t1 |
| **G-5** | `waiting_on_user` pauses, no verdict/round; idle suppressed while waiting | Phase 2 triggers/pause | t0 |
| **G-6** | needs_input escalates at T1, auto-`handback(pause)` at TTL — never silent | **Phase 1 messaging** (TTL + escalation, MAJ-4) | g6/g7 |
| **G-7** | feasibility gate rejects out-of-policy criterion at `/goal set` | Phase 2 goal compilation | t0 |
| **G-8** | echo-confirm literal commands; re-statement diffs as amendment; `/goal clear` inerts trigger | Phase 2 compilation + **§7 FE-8** | t0 |
| **G-9** | awaiting-owner-correction — one round then wait; no re-judge of unchanged state | **F2 standalone (§4.7)** + Phase 2 owner loop | t2 |
| **G-10** | auto-reset excludes frozen; tails depend only on done; unreachable → honest exit | Phase 2 owner loop | t2/t3 |
| **G-11** | correction transactional; append + SUPERSEDE + TARGETED-RETRY each record a revision entry | Phase 2 owner loop **on** Phase 0 revision-entry + Phase 1 transactional append | t3 |
| **G-12** | Play mints `resumed_from` generation; failed/cancelled resume from last git commit | Phase 2 owner loop **on** Phase 1 git + §6 cancel/restart | t2 |
| **G-13** | boot sweep (= A-17): kill -9 → `failed(interrupted)` within N s; plan recovers; idle fires | **Phase 1 boot-sweep** | §5 boot sweep |
| **G-14** | app-level token budget debits owner+member+verifier+Judge; exhaustion brakes every scope | Phase 2 budget **on** §6 budget + **§7 FE-6** | live E2E |
| **G-15** | boundary commit write-set-scoped; `.git` deny by operation; HEAD-divergence → integrity lost | **Phase 1 git** | g4 |
| **G-16** | plan-lint rejects overlapping write-sets + join-less plans; merge conflict → correction event | **Phase 1 plan-lint** | g4/g5 |

## 13. Folded-findings traceability appendix — homeless findings → layer

DoD-2 requires **every** normative grill/architect/third-pass finding folded into v2.2 to have a delivery home. The still-homeless ones (beyond those already anchored by a G-row above):

| Finding | What it requires | Layer / home |
|---------|------------------|--------------|
| **N-2** | owner-gaming-DoD guards (ladder weights deterministic rungs; post-unmet artifacts flagged post-hoc; red-team acceptance case) | Phase 2 owner loop |
| **N-6** | re-statement = diffed + confirmed amendment, never silent recompile | Phase 2 goal compilation |
| **N-10** | content-egress policy for messages crossing agent/channel boundaries (a child can't exfiltrate through the parent's inbox) | Phase 1 messaging |
| **MAJ-4 / G-6** | needs_input TTL 24 h (configurable) + escalation ladder (T1 notify → TTL auto-handback) | Phase 1 messaging |
| **MIN-5** | secrets-in-history purge/gc procedure + sensitive-value registry scan on auto-commit paths | Phase 1 git |
| **MIN-6** | nested-repo detection + signalled degraded contract (user repo wins, ours skips; planner/Judge/owner all told) | Phase 1 git |
| **OBS-3** | go-git Apache-2.0 NOTICE ride-along + auto-commit size guard (skip/warn above N files/MB) | Phase 0 (NOTICE) / Phase 1 (size guard) |
| **P2M-14 / MIN-3** | sync-delegate `wait=true` question rejected with a clear tool error; human-route only via explicit launch-flag opt-in with a bounded wait | Phase 1 messaging |
| **O-1** | board task ↔ session is 1:N with aggregate status ("failed if any required session failed") | Phase 1 messaging (records) |

## 14. Risk register (mitigations baked in)

| Risk | Mitigation in this plan |
|------|------------------------|
| go-git footprint blows Constraint #3 | §4.3 spike is a hard gate; fallback to system-git + subdir isolation, re-scope the git layer; trigger/session/plane land regardless |
| Plan-Judge evidence poverty | Phase 1 git feeds real per-member write-set-scoped diffs (DoD-3); acceptance test for evidence composition (G-3/G-15) |
| Session plane is the biggest piece | Phase 1 substrate; reuses steering.go/async_notifier/bus (BOM); SessionMessage contract-first inline-oneOf |
| Warm-resume-cross-process unproven | boot-sweep §5 recover-to-checkpoint; the go-git commit is the checkpoint (Play-from-commit, D13); needs_input reconstructible-or-swept, answered explicitly (§5.2) |
| Regressing the just-shipped ADR-052 surfaces | every wave runs the existing suites; CI authority; 7+14 reviewer gates |
| **Shared-spine drift (NEW)** | §4.1 build-once artifacts + §3 shared-spine guardrail + BOM review-gate (DoD-11); "which spine artifact is this? / none" is a blocking finding |
| **Operability / kill-switch absence (NEW)** | §8 is its own deliverable (DoD-10) — full config schema, global kill switch, explicit retention; nothing a magic constant |
| **Folded-findings drop (NEW)** | DoD-2 widened to "every D1–D17 AND every normative finding"; §12 G-map + §13 appendix give every finding a provable home |
| Scope creep re-fragmenting into "5 shipped ADRs" | integrated-layers model (§4): build in dependency order, integrate continuously, land once; only F2 ships standalone |

## 15. First actions (start now, in parallel)

1. **go-git spike** → go/no-go report (the long pole; gates the §3d git contract).
2. **F2 round-burn fix** → small standalone PR on the current branch (a *shipped, live* defect; independent of the redesign; regression test fails pre-fix).
3. **ADR-053 (one ADR)** via Albert (ratification) → `/grill-spec` (PASS) → `/plan-spec` → **land the §6 contract set** → open the Phase 1 substrate waves.

---

*This brief is the delivery contract for the v2.2 design. The design is the what; this is the how, the order, and the proof-of-done. The system is built in dependency-ordered layers, integrated continuously onto one epic branch, and landed once — conformance-proven — via a single human-gated `→ main` PR.*

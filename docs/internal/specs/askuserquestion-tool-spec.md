# AskUserQuestion — structured clarification tool spec

- **Status:** Draft **v3** (2026-09-05). Two grill rounds complete per project standard: r1 (4C/11M — blocking model refuted, rebuilt on the durable park) corrected in v2; r2 (2C/8M — resume contract misdescribed, v1 channel hole) corrected here; the one flagged sign-off (§9.1, channel phasing) was RESOLVED by operator interview #5 — specs complete, zero open sign-offs.
- **Authoritative parents:** ADR-074 D4b (Accepted; all operator rulings FIXED; visual reference `docs/internal/design/askuserquestion-ui-mock.html` v2). **ADR-053 D2 amendment:** recorded at the ADR itself (D2 row annotation) per the design-first rule — the card supersedes D2's "no per-question reply card" for session owners; the owner-only restriction survives (children ask their parent via `message_parent`).
- **Role:** general-purpose agent tool; first consumer is ADR-074 D4a's goal-compilation clarifying question.

---

## 0. Execution model — durable park (verified precedents cited)

1. **Park.** `Execute()` validates, persists the pending question set (see §0.3), emits the card frame, and returns `ParksTurn` — the loop ends the turn `TurnEndStatusParked` (`loop.go:11759`, `:11583-11588` — verified). **The park-time tool_result is written immediately** (the transcript's tool_use/tool_result adjacency is never left dangling — C-R2-1): its content is `{status:"pending", card_id, question_count}`, mirroring the `MessageParentResponse` precedent.
2. **Resume.** The answer does NOT return as a tool result (refuted r2 — no late-fill mechanism exists). It returns exactly as the `message_parent` answer precedent does (`delegate.go:3520`, `:3562-3566`): a **correlated user-role message** starting the resume turn — `Answers to your questions (card_id=<id>): {"status":"answered"|"cancelled","answers":[...]}` — with each answered question's TEXT echoed alongside its answer (adopted o-R2-1: a days-late resume must not depend on the question surviving `windowTrim`'s eviction window). **Chat rendering:** the SPA renders this resume message AS the collapsed answer record (presentation rule), never as raw JSON; on channels it is not echoed back.
3. **Slot/timeout consequences (wording per mechanism — m-R2-7):** admission gates inbound dispatch per session worker and the slot releases at worker idle-exit (`session_worker.go:154`), shortly after the park — no indefinite pin; nothing is running, so sub-turn/scheduled/per-agent timeouts never engage; the steering queue is irrelevant.
4. **Durable store (M-R2-1, precise):** pending state lives in **UnifiedMeta (session meta) + the gateway-side pending registry** (`approvalRegistryV2` shape). Owner sessions have NO `LifecycleRecord` and this spec creates none — `ParksTurn` alone drives the loop seam (verified independent, `loop.go:11759`); the delegated-session boot sweep and `list_jobs` are untouched. The v2 "needs_input lifecycle" phrasing is struck.
5. **Timers** run server-side over the durable state, re-armed on boot from persisted timestamps.
6. **Collapsed-record reconstruction (C-R2-1 part 3):** on submission the registry/session-meta record is updated with the answers; the collapsed record (live and on history reload) renders from THAT record — not from the tool_call/tool_result pair (which holds only the park-time pending stub) and not from parsing the resume message.
7. **Goal-loop gate (M-R2-5):** `TurnEndStatusParked` is NOT a natural turn stop for `checkGoalLoopAfterTurn` — a parked compile/clarification never advances the goal round, invokes the Judge, or re-dispatches; the gate lives at that function's entry.
8. **Caller scope:** session owners only; delegated children rejected toward `message_parent(question:true)` (EC-9). The engine-invoked D4a goal-compile inside a DELEGATED child session (o-R2-3): falls back to `message_parent(question:true)` toward the delegating parent — never a card.
9. **Pending-key:** one set per **routing session** (`turn.go:322-364` verified — root turns self-key, children inherit; exactly "one card per chat surface").

## 1. User stories

### US-1 (P0) — Ask; answer on one structured surface
1. Owner-session call, valid → pending persists, card frame emits, turn parks, composer locks with reason. *(FR-1, FR-2)*
2. Selection on Q1 → tab marks, auto-advance to next unselected, n/M updates. *(FR-10)*
3. All questions selected/free-texted → Answer press → server-validated submission (FR-6) → registry record updated with answers → **resume turn starts with the correlated answers message (§0.2)** → card collapses to the record → composer unlocks. *(FR-3)*
4. Cancel at any point → submission-free resume with `{"status":"cancelled"}`, **no answers, selections discarded — uniform on every surface**; card collapses to a "cancelled" record; the agent decides next steps. *(FR-4)*
5. `multi_select: true` → list answers. (Multi-select visual: mock v3 required before implementation — flagged.)

### US-2 (P0) — Free text always, via the card
1. Free-text input under every question's options; typing deselects; **presence of `free_text` IS the flag**; re-select drops the text from the result (last interaction wins). *(FR-5)*
2. Composer stays locked; free-form answering happens only in the card.

### US-3 (P0) — Recommendation, timer, and the three answer states
States (o-R2-2 adopted): **selected** (client-only, unsubmitted) → **resolved-pending-submit** (server auto-resolved a default-safe question; not yet part of a submission) → **answered** (part of the accepted submission).
1. `recommended`: badge, listed first, never pre-selected.
2. `default_safe` (requires `recommended`): at its timer expiry (30:00 fixed) an un-answered such question becomes resolved-pending-submit, marked `auto_default`.
3. **Grace auto-submit — client-fired (M-R2-2):** when every question is selected/resolved and 5 minutes pass with no card interaction, the CLIENT submits (a normal `ask_user_answer`) including manual selections, each marked by origin. **Closed-tab outcome, stated:** client state is lost; the SERVER submits on its own only when EVERY question is default-safe-resolved and no client submission has landed — so a closed tab with manual selections on non-default-safe questions leaves the card pending (correct: those questions genuinely need the human), and a closed tab where all questions were default-safe yields the all-recommendation submission at 30:00.
4. Unanswered non-default-safe questions pend indefinitely — no other expiry exists; recovery = answer, Cancel, or session Stop affordances.

### US-4 (P1) — Rich context
Raw markdown/media-ref context; SPA-sanitized render (chat pipeline); display-only. Hostile-markdown inertness is TESTED (m-R2-3). *(FR-7)*

### US-5 (P1) — Text channels: the tool is BLOCKED there — permanent non-goal (operator ruling, interview #5, 2026-09-05; supersedes v2's v1.1 plan and resolves the §9.1 sign-off)
1. On any non-web-origin session the tool errors immediately (`no_human_surface`-class error naming the reason), and the agent asks its question **conversationally, in plain language** — ordinary chat, no machinery. This is the operator's chosen simplification: no sequential delivery, no reply parsing, no channel degradation subsystem, ever.
2. Consequences: the pre-turn answer seam, per-question channel clocks, both-surfaces races, and channel `/cancel`-as-card-cancel are all deleted from scope (the r2 findings M-R2-7 and parts of M-R2-4 dissolve with them). A session live on both the SPA and a channel is answerable via the SPA card only; channel messages during a pending set are ordinary turns (US-6 S6).

### US-6 (P1) — Lifecycle, liveness, inbound-while-pending
1. **Restart:** session-meta persistence + reconnect-snapshot hydration (`SessionStateFrame` precedent verified, `websocket.go:704-706`); timers re-arm; the resume path needs nothing alive to "survive". *(FR-9)*
2. **Session Stop / channel `/cancel`:** cancels the set, collapses the card, unlocks the composer.
3. **One set per routing session**; second call → tool error.
4. **Liveness, defined on origin — NOT connection state (M-R2-3):** answerable ⇔ the session's origin surface is the SPA (always answerable — a disconnected client's card hydrates on reconnect; parking makes waiting free). `no_human_surface` fires for: `AutoDenyAsk` contexts (scheduled/headless; ctx plumbing verified `base.go:123-152`, `loop.go:11278`; #659 status recorded: the inheritance CODE landed — `subturn.go:1428-1452`, ADR-075 FR-032 — the ISSUE remains open for its browser-tool remnant), and **every channel-origin session — permanently** (US-5 ruling): the agent asks conversationally instead; never a silent park a channel user can't see or answer. *(FR-12)*
5. Delegated-child calls rejected (owner-only).
6. **Ordinary inbound while pending, v1 (M-R2-4):** a plain message (second SPA client, stale tab) runs as a NORMAL turn; the pending set and card SURVIVE it untouched; that turn's own `AskUserQuestion` call errors (one-per-session); no server rejection notice (chat simply continues). Channel messages are likewise ordinary turns — permanently (US-5).

### US-7 (P1) — Registration, policy, visibility (M-R2-6 corrected)
1. **Constraint #6 seeding, all three sites enumerated:** (a) the tool joins `allStaticToolNames` (`core.go:625` region); (b) every agent's `denyAllThenOverride` override map gets an explicit entry — **allow** for every human-facing agent (core roster, subagent tier, customs' default allowlist), **deny for Judge and PlanSupervisor** (they can never be session owners; an advertised always-erroring tool violates their deliberately minimal seeds, `core.go:578-580`); (c) the global `sandbox.tool_policies` ceiling gains its literal entry. The "never harden asking into an ask-gate" rationale applies to the human-facing agents' allow.
2. Visibility: always visible (`toolVisibility.ts`) — asserted in Test 14.
3. Approval/card coexistence is **cross-session only** (m-R2-5 — the pending session is parked; nothing runs in it): an approval modal from ANOTHER session takes z-order precedence over this session's card. Asserted in Test 14.

## 2. Tool schema

As v2 (counts, caps, validation table incl. header ≤16, dup labels, empty strings, `default_safe`⇒`recommended`, one-element auto-default under `multi_select`). **The §2 result schema is the RESUME-MESSAGE payload, not a tool return** (C-R2-1): the tool's own Execute result is the park-time pending stub.

## 3. Wire & SPA

As v2 (WsFrameType both directions; inbound zod; session-scoped + FR-089 frame class; reconnect-snapshot hydration; server-side validation + first-valid-wins incl. auto-resolutions; size caps), with: **copy-set verification (m-R2-6):** the sync obligation may be TRIPLE (asyncapi inline + schema file + `pkg/gateway/inboundschemas/`) — the PR checklist enumerates the real set at implementation. Completed-card render sources from the registry/session-meta record (§0.6).

## 4. Non-behaviors

As v2, plus: the pending registry carries a global cap across sessions (DoS line item); a parked turn never advances a goal round; the server never submits while any non-default-safe question is unanswered.

## 5. Edge cases

EC-1 cancel discards selections (uniform). EC-2 → superseded by US-3 states + client-fired grace. EC-3 last-interaction-wins. EC-4 out-of-range number → free text. EC-5 struck (channel timers deleted with US-5's mechanism). EC-6 headless → `no_human_surface`. EC-7 two-client race → first valid wins. EC-8 stale card id → rejected. EC-9 delegated child → owner-only rejection. EC-10 channel-origin session → blocked tool + conversational ask, permanently (never a silent park). EC-11 (new) plain inbound during pending (v1) → normal turn, card survives.

## 6. Test plan

| # | Test | Level |
|---|------|-------|
| 1 | Validation table incl. caps/dup labels/header | Unit |
| 2 | **Park-time stub result; resume-message format incl. question-text echo; answer assembly (single/multi/free-text/EC-3)** | Unit |
| 3 | Park mechanics: ParksTurn, TurnEndStatusParked, worker idle slot release, no goal-round advance (M-R2-5) | Integration |
| 4 | Timer + states: per-question fire, resolved-pending-submit marking, client grace submit, server all-default submit, closed-tab outcomes, audit entries | Integration |
| 5 | One-per-routing-session; delegated-child rejection (EC-9) | Integration |
| 6 | Restart: persistence, snapshot hydration, timer re-arm, **post-restart resume via the answers message** | Integration |
| 7 | Session Stop + channel `/cancel` + **card Cancel button** (m-R2-2) all cancel correctly | Integration |
| 8 | Contracts: frame enums both directions, copy-set sync, inbound zod, snapshot field | Contract |
| 9 | Card component: tabs/advance/badge/underline/countdown/collapsed record from the registry record incl. history reload (§0.6) + **hostile-markdown context renders inert** (m-R2-3) | Component |
| 10 | Channel-origin session → immediate blocked-tool error + agent asks conversationally (US-5); channel messages during an SPA-pending set stay ordinary turns | Integration |
| 11 | Liveness: AutoDenyAsk; **channel-origin → blocked + conversational ask (EC-10)**; SPA disconnected-waits | Integration |
| 12 | E2E: goal clarifying question through the card, stubbed LLM; delegated-child compile falls back to message_parent (o-R2-3) | E2E |
| 13 | Submission validation + races: ownership, membership, arity, first-wins incl. auto-resolution contender, stale-card | Integration |
| 14 | **Policy seeding (3 sites; Judge/PlanSupervisor deny), visibility classification, cross-session approval z-order** (m-R2-4) | Unit+Component |
| 15 | Inbound-while-pending v1: normal turn, card survives, inner AskUserQuestion errors (EC-11) | Integration |

## 7. Traceability

| FR | Requirement | Stories | Tests |
|----|-------------|---------|-------|
| FR-1 | Park model; park-time stub; resume-message contract | US-1 S1/S3, §0.1-0.2 | 2,3 |
| FR-2 | Durable state (meta+registry, no LifecycleRecord); one per routing session; owner-only | §0.4/0.8-0.9, US-6 S3/S5 | 3,5 |
| FR-3 | Validated submission → registry update → resume | US-1 S3 | 2,13 |
| FR-4 | Cancel uniform, no answers (button/Stop//cancel) | US-1 S4, US-6 S2 | 7 |
| FR-5 | Free text semantics | US-2 | 2 |
| FR-6 | Server validation + first-valid-wins incl. auto-resolutions | US-5 S6, §3 | 13 |
| FR-7 | Raw context, sanitized render, inert hostile input | US-4 | 8,9 |
| FR-8 | Channels: tool blocked, conversational fallback (permanent) | US-5, EC-10 | 10 |
| FR-9 | Restart lifecycle | US-6 S1 | 6 |
| FR-10 | Approved visuals + record reconstruction | US-1 S2/S3, §0.6 | 9 |
| FR-11 | Seeding (3 sites, deny for system agents), visibility, coexistence | US-7 | 14 |
| FR-12 | Liveness by origin; channels permanently blocked | US-6 S4, EC-10 | 11 |

## 8. Open items
1. Liveness predicate's concrete interface (the `policyApproverAdapter`-style injection — implementation detail; behavior is now fully specified).
2. Mock v3: multi-select rows; also replace the mock's emoji glyphs with Phosphor icons at implementation (m-2/m-R2 carryover).
3. (Struck — the channel seam was deleted with US-5's ruling; the goal pending-confirm taxonomy stands alone.)

## 9. Operator sign-off register
**9.1 (RESOLVED — interview #5, 2026-09-05):** the operator superseded the channel-degradation ruling entirely: the tool is web-only and BLOCKED on other channels, where the agent asks conversationally in plain language; no channel mechanism is ever built. ADR-074 D4b carries the amended ruling. Also updates EC-10 to the permanent rule and deletes the v1.1 scope. No open sign-offs remain.

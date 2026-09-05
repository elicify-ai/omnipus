# AskUserQuestion — structured clarification tool spec

- **Status:** Draft **v3** (2026-09-05). Two grill rounds complete per project standard: r1 (4C/11M — blocking model refuted, rebuilt on the durable park) corrected in v2; r2 (2C/8M — resume contract misdescribed, v1 channel hole) corrected here. **One flagged item awaits explicit operator sign-off (§9.1).**
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

### US-5 (P1) — Text channels (v1.1; mechanism specified; deferral flagged §9.1)
1. **v1.1 mechanism (verified reachable):** the pre-turn seam (`handleCommand` path, `loop.go:7758` — runs for every bus inbound incl. channels) consumes an inbound message as the current question's answer when the routing key has a pending set. **Semantic flip acknowledged (M-R2-4):** on channels, v1.1 changes what a typed message means while pending (it becomes the answer); on the SPA it never does (see US-6 S6).
2. Sequential delivery ("2 of 5: …" + numbered options + "(recommended)" + reply guidance).
3. Parsing: in-range number → option; exact whole-message "cancel" (case-insensitive, trimmed; `IsCancelCommand` precedent, `cancelparse.go:29`) → cancels the set (no answers); else free text ("cancel" as literal free text: unreachable on channels — documented; use the app).
4. **Timers × sequencing (M-R2-7):** default-safe questions auto-resolve at **card-render + 30:00 on every surface, even if not yet delivered on the channel** — surface-independent terminal behavior; when the sequence reaches (or skips past) a resolved question the channel gets "resolved automatically: <label>". **Auto-resolutions are submissions participating in first-valid-wins.** Restart re-sends the current question (known, accepted re-delivery — m-R2-8) with its timer continuing from the ORIGINAL card-render-relative schedule.
5. Context on channels: text truncated to 500 chars; media omitted ("details in the app").
6. Both-surfaces sessions: first valid submission wins (auto-resolutions included); the loser surface gets "already answered".

### US-6 (P1) — Lifecycle, liveness, inbound-while-pending
1. **Restart:** session-meta persistence + reconnect-snapshot hydration (`SessionStateFrame` precedent verified, `websocket.go:704-706`); timers re-arm; the resume path needs nothing alive to "survive". *(FR-9)*
2. **Session Stop / channel `/cancel`:** cancels the set, collapses the card, unlocks the composer.
3. **One set per routing session**; second call → tool error.
4. **Liveness, defined on origin/binding — NOT connection state (M-R2-3):** answerable ⇔ the session's origin surface is the SPA (always answerable — a disconnected client's card hydrates on reconnect; parking makes waiting free) OR (v1.1+) the session is bound to a text channel. `no_human_surface` fires for: `AutoDenyAsk` contexts (scheduled/headless; ctx plumbing verified `base.go:123-152`, `loop.go:11278`; #659 status recorded: the inheritance CODE landed — `subturn.go:1428-1452`, ADR-075 FR-032 — the ISSUE remains open for its browser-tool remnant), and — **in v1 — channel-origin sessions** (C-R2-2): they get `no_human_surface` immediately and the calling agent uses ADR-074 D4b's sanctioned plain-chat-message fallback; never a silent park a channel user can't see or answer. *(FR-12)*
5. Delegated-child calls rejected (owner-only).
6. **Ordinary inbound while pending, v1 (M-R2-4):** a plain message (second SPA client, stale tab) runs as a NORMAL turn; the pending set and card SURVIVE it untouched; that turn's own `AskUserQuestion` call errors (one-per-session); no server rejection notice (chat simply continues). v1.1 changes this on channels only, per US-5 S1.

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

EC-1 cancel discards selections (uniform). EC-2 → superseded by US-3 states + client-fired grace. EC-3 last-interaction-wins. EC-4 out-of-range number → free text. EC-5 channel silence → per US-5 S4. EC-6 headless → `no_human_surface`. EC-7 two-client race → first valid wins. EC-8 stale card id → rejected. EC-9 delegated child → owner-only rejection. EC-10 (new) channel-origin session in v1 → `no_human_surface` + chat fallback (never a silent park). EC-11 (new) plain inbound during pending (v1) → normal turn, card survives.

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
| 10 | (v1.1) Channel: pre-turn seam consumption, parsing, surface-independent timer terminals, resolved-automatically messages, restart re-delivery, both-surfaces race | Integration |
| 11 | Liveness: AutoDenyAsk; **v1 channel-origin → no_human_surface + fallback (EC-10)**; SPA disconnected-waits | Integration |
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
| FR-8 | v1.1 channel mechanism | US-5 | 10 |
| FR-9 | Restart lifecycle | US-6 S1 | 6 |
| FR-10 | Approved visuals + record reconstruction | US-1 S2/S3, §0.6 | 9 |
| FR-11 | Seeding (3 sites, deny for system agents), visibility, coexistence | US-7 | 14 |
| FR-12 | Liveness by origin/binding; v1 channel fallback | US-6 S4, EC-10 | 11 |

## 8. Open items
1. Liveness predicate's concrete interface (the `policyApproverAdapter`-style injection — implementation detail; behavior is now fully specified).
2. Mock v3: multi-select rows; also replace the mock's emoji glyphs with Phosphor icons at implementation (m-2/m-R2 carryover).
3. Whether the v1.1 pre-turn seam and the goal pending-confirm taxonomy share one seam (both are pre-turn pending-state consumers).

## 9. Operator sign-off register
**9.1 (OPEN — M-R2-8): the v1/v1.1 channel phasing.** ADR-074 D4b's plain-text channel degradation is a fixed ruling with no phasing stated. This spec ships SPA-only in v1, with channel-origin sessions getting the D4b-sanctioned plain-chat fallback until v1.1 delivers the specified sequential mechanism. This phasing needs the operator's explicit yes/no; everything else in this spec stands regardless of the answer.

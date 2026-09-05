# AskUserQuestion — structured clarification tool spec

- **Status:** Draft **v2** (2026-09-05). v1's grill (4 CRITICAL / 11 MAJOR / 6 MINOR) found the blocking-execution model infeasible against the turn engine and every consequence undersold; v2 is rebuilt on the codebase's own sanctioned pattern (durable park + pending registry + reconnect snapshot) and addresses every finding (C-n/M-n/m-n markers cite them). Grill round 2 pending.
- **Authoritative parents:** ADR-074 D4b (Accepted — every operator ruling there is FIXED; see v1 header for the list; the approved visual reference is `docs/internal/design/askuserquestion-ui-mock.html` v2). **This spec records an amendment to ADR-053 D2** (C-4): D2 ruled "only a direct session/plan owner asks the human, conversationally in chat (no per-question reply card)" — D4b supersedes the no-card clause for session owners; the owner-only restriction SURVIVES (delegated children still ask their parent via `message_parent`, never the human).
- **Role:** general-purpose agent tool; first consumer is ADR-074 D4a's goal-compilation clarifying question.

---

## 0. Execution model — durable park, not an in-memory block (C-1, C-3, O-1)

`AskUserQuestion` NEVER blocks inside `Execute()`. It follows the `message_parent(question:true)` precedent exactly:

1. The tool validates its input, persists the pending question set as durable session state (session meta + a gateway-side pending registry shaped like `approvalRegistryV2` — O-2), emits the card frame, and returns a **`ParksTurn` result**: the turn ends parked with the `needs_input` lifecycle (`pkg/tools/message_parent.go`'s `LifecycleNeedsInput` pattern, honored at the loop's park seam).
2. The user's answer — SPA frame or (v1.1) channel reply — **resumes as a correlated new turn** carrying the tool result (answers array or `cancelled`), exactly as a `needs_input` park resumes today.
3. Because the turn ends at park time: the admission slot is released (no starvation), the steering queue is irrelevant (no mid-turn block for replies to divert around), per-agent/sub-turn/scheduled timeouts never race a human (nothing is running), and **restart survival is real by construction** — the pending state persists, the reconnect snapshot re-hydrates the card, and the answer starts the resume turn regardless of process lifetime. `RecoverOrphanedToolCalls`' cancel-on-restart semantic does not apply: there is no orphaned in-flight call to repair.
4. The 30-minute default-safe timer runs server-side over the durable state (armed from the persisted rendered-at; re-armed on boot from the same timestamp — continuity for free), never inside a tool goroutine.

**Caller scope (C-4):** v1 callers are **session owners only** — the agent driving a user-facing session. A delegated child's call is rejected with a clear error pointing at `message_parent(question:true)` (ADR-053 D2's surviving rule). Scheduled/headless runs (`AutoDenyAsk` contexts) get `no_human_surface` immediately.
**Pending-key (C-4):** one pending question set per **routing session** (`routingSessionID` — the cancel/reachability namespace), so parallel delegated children can never race two cards onto one chat surface (they can't call it at all in v1, but the key rules out the class).

## 1. User stories

### US-1 (P0) — An agent asks; the user answers on one structured surface
1. **Given** a call with 3 questions from a session owner, **When** validated, **Then** the pending set persists, the card frame emits, the turn parks (`needs_input`), and the composer locks with the reason shown. *(FR-1, FR-2)*
2. **Given** an option selected on Q1, **Then** its tab marks selected, the view auto-advances to the next question without a selection, and the n/M counter updates. *(FR-10)*
3. **Given** all questions have a selection or free text, **When** the user presses Answer, **Then** the submission is server-validated (FR-6), the resume turn carries `{status: "answered", answers: [...]}`, the card collapses to the flat record, and the composer unlocks. *(FR-3)*
4. **Given** Cancel pressed at any point, **Then** the resume turn carries `{status: "cancelled"}` with **no answers** — selections are discarded, uniformly on every surface (M-3 resolved; EC-1). The agent decides what to do next; the tool never re-asks by itself. *(FR-4)*
5. **Given** `multi_select: true`, **Then** several options are selectable and `selected` is a list. (Visual for multi-select rows is NOT yet in the approved mock — flagged for a mock v3 before implementation, m-3.)

### US-2 (P0) — Free text always, through the card only
1. **Given** any question, **Then** a free-text input renders under its options; typing deselects that question's options; **presence of `free_text` in the answer object IS the flag** (no boolean — M-10); a re-select clears the free text from the result even if text remains visible in the field (EC-3: last interaction wins). *(FR-5)*
2. The chat composer stays locked while pending; free-form answering happens only in the card (D4b).

### US-3 (P0) — Recommendation and the default-safe timer, with real definitions (M-4)
Definitions: a question is **selected** when the card holds a selection/free text for it (client state, unsubmitted); **answered** only when a submitted card (Answer press or auto-submit) carries it. The default-safe timer concerns *answered*, not *selected* — an unsubmitted selection does not stop the timer (a walked-away user's half-card must not block a background plan forever, per D4b's own rationale).
1. `recommended` renders first with the badge, never pre-selected.
2. **Given** `default_safe: true` (requires `recommended`; validated), **Then** at rendered-at + 30:00 (fixed global) an unanswered such question resolves to its recommendation, marked `auto_default: true` per answer and "· auto (30 min)" in the record.
3. **Given** timers have fired and every question now has an answer-or-auto-resolution BUT some are unsubmitted manual selections, **Then** the card auto-submits after a **5-minute grace period with no card interaction** (the concrete focus rule — v1 open-item 1 resolved: "focused" = any card interaction within the last 5 minutes defers auto-submit), including the manual selections, each answer marked by origin (`auto_default` true/false). A card where every answer is auto submits immediately.
4. **Given** unanswered NON-default-safe questions remain, **Then** the card pends indefinitely — there is **no other expiry** (M-11: the v1 "turn Stop/expiry" language is deleted; nothing is running to expire). Recovery is: answer, Cancel, or the session's Stop affordances.

### US-4 (P1) — Rich context
1. `context` (markdown; media refs from the session's own uploads) renders as the flat left-rule block. It is display-only, never parsed, and the frame carries it **raw with SPA-side sanitized rendering** (same pipeline as chat markdown — v1 open-item 3 resolved normatively; XSS posture identical to chat). *(FR-7)*

### US-5 (P1) — Text channels (v1.1, mechanism specified — C-2)
The D4b ruling (plain-text degradation, no native channel UI) stands; v1 ships SPA-only, and **v1.1 ships channels on this specified mechanism** — not a parser footnote:
1. **Why the park model makes this tractable:** with the turn parked, an inbound channel message is NOT mid-turn — it arrives between turns like any message. The mechanism is a **pre-turn pending-answer seam** in the loop's turn-start path (the `applyGoalCommandPrompt` precedent): before normal processing, if the session's routing key has a pending question set, the inbound text is consumed as the current question's answer instead of becoming a chat turn. No channel-adapter changes, no steering-queue surgery, no mid-turn interception (v1's deadlock, C-2, cannot occur — nothing is blocked).
2. Questions deliver **sequentially** — one message per question ("2 of 5: …"), each with numbered options, "(recommended)" inline, "reply with a number or your own answer"; the next question sends after the answer.
3. Reply parsing: an in-range number → that option; the exact whole-message keyword "cancel" (case-insensitive, trimmed — the `IsCancelCommand` precedent, m-1) → cancels the whole set (no answers, per FR-4); anything else → free text. A literal free-text answer "cancel" is unreachable on channels — documented limitation (use the app). *(FR-8)*
4. **Per-question clock (M-5):** each question's 30-minute default-safe timer starts at ITS delivery, not card render. On restart, the current question re-sends with its timer continuing from persisted delivery time. Cancel mid-sequence returns `cancelled` with no answers (uniform rule).
5. **Context on channels (M-6):** text context is included truncated to 500 chars; media context is omitted with "(details in the app)". Nothing else about the context leaves the session's own surfaces.
6. A session live on BOTH SPA and a channel: the card and the sequential messages both present; **first valid submission wins** (FR-6's race rule); the loser surface gets a "already answered" notice.

### US-6 (P1) — Lifecycle, liveness, callers
1. **Restart:** pending state persists in session meta; the SPA re-hydrates via the **reconnect snapshot** (the `pending_approvals`-in-`session_state` precedent — NOT a boot-time frame push; M-2); timers re-arm from persisted timestamps. The answer resumes a new turn — nothing needed to "survive". *(FR-9)*
2. **Session Stop** (user hits Stop / `/cancel` on a channel): the pending set cancels (resume turn `cancelled`), card collapses, composer unlocks — no zombie cards. *(FR-4)*
3. **One pending set per routing session**; a second call while one pends → tool error to the caller. *(FR-2)*
4. **`no_human_surface` (M-1), operationally defined:** the tool errors immediately when (a) the turn context carries `AutoDenyAsk` (scheduled/headless — the existing seam; its non-inheritance defect #659 is a named dependency to verify for delegated contexts), or (b) a new **inverted gateway-liveness predicate** (the `policyApproverAdapter` pattern: gateway registers a "has any answerable surface" check into the loop) reports no SPA client AND no bound channel for the session. A session whose SPA client is merely disconnected but exists **waits** (the card hydrates on reconnect; parking makes waiting free).
5. Delegated-child calls rejected (owner-only, §0).

### US-7 (P1) — Registration, policy, visibility (M-9)
1. **Constraint #6 seeding:** the tool joins the static builtin catalog with an explicit, literal policy entry for every agent — seeded **allow** for all (core roster, subagent tier, system agents, new customs): asking the user is the safety-increasing direction, and an `ask`-gate on asking (a question requiring approval to ask a question) is absurd — stated here so nobody "hardens" it later. Boot coverage validation passes by seed, per the constraint's mechanism.
2. **Visibility:** always visible in the thread (`toolVisibility.ts` classification: never hidden — it IS user-facing content).
3. **Approval-surface interplay:** a tool-approval modal and a question card never contend — approvals gate dispatch pre-execution; the card exists only after this tool has executed and parked. If an unrelated later turn triggers an approval while a card pends, both render; the approval modal takes z-order precedence (it has its own 600s timeout; the card has none).

## 2. Tool schema (agent-facing)

As v1 (`questions[1..10]`, options 2..6, `recommended` by label, `multi_select`, `default_safe` ⇒ requires `recommended`, `context`), plus (M-8) size caps — question ≤500 chars, header ≤16 (enforced — in the rejection table now), option label ≤80, description ≤200, context ≤4000, free-text answer ≤2000 — and a completed validation table: 0/>10 questions; <2/>6 options; empty any required string; duplicate headers; **duplicate option labels within a question** (recommended matches by label); `default_safe` without `recommended`; `recommended` naming no option. `recommended` + `multi_select`: the auto-default resolves to a one-element list. Result schema: `{status: "answered"|"cancelled", answers?: [{header, selected?: [string], free_text?: string, auto_default: bool}]}` — `answers` present iff `answered` (M-3).

## 3. Wire & SPA (Constraint #8 — rewritten per M-2)

- **Frames:** `ask_user_question` (server→client: card payload, card id, per-question rendered/delivered timestamps) and `ask_user_answer` (client→server: card id + answers | cancel). BOTH added to `WsFrameType.yaml`'s respective direction enums; `ask_user_answer` joins the inbound zod validation set.
- **Dual-copy obligation:** each frame's canonical copy lives inline in `asyncapi.yaml` components.schemas AND as the schema file, kept in sync by hand — the documented `GoalStatusFrame.yaml` convention, named here so the PR checklist carries it.
- **Session scoping:** both frames are session-scoped (`SESSION_SCOPED_FRAME_TYPES`, `src/store/chat.ts`) and carry the ADR-057 FR-089 frame-class/`producing_session_id` decision like their siblings.
- **Hydration:** the pending card joins the **reconnect snapshot** (`session_state`, alongside `pending_approvals`) — attach-time hydration, no boot-time push.
- **Server-side submission validation (M-7):** card id matches the session's pending set; submitting user owns the session (multi-user installs); every `selected` label ∈ that question's options; arity respects `multi_select`; free-text within cap. **First valid submission wins**; later ones are rejected with an "already answered" frame. Invalid submissions are rejected without consuming the pending set.
- **Completed-card rendering on history load (m-5):** the collapsed record re-renders from the persisted transcript (tool_call + tool_result pair), not from live frames — the answers array is sufficient to reconstruct it.
- SPA component per the approved mock; implementation note (m-2): the mock's 🔒/✓ glyphs are placeholders — production uses Phosphor icons per the no-emoji-in-chrome rule. Multi-select visual needs a mock v3 (m-3).

## 4. Non-behaviors

As v1, plus: the tool never blocks a goroutine awaiting a human; never re-asks after Cancel without the agent explicitly calling again; auto-default never fires on a question lacking `default_safe`; a cancelled set never yields partial answers; the pending registry never accepts a second set per routing session; an audit-log entry records every auto-default resolution (STRIDE note adopted).

## 5. Edge cases

EC-1 Cancel with selections → `cancelled`, no answers (uniform). EC-2 → superseded by US-3's definitions and 5-minute grace rule. EC-3 free-text + re-select → last interaction wins; re-select drops the text from the result. EC-4 out-of-range/zero number on channel → free text. EC-5 channel silence: default-safe questions resolve on their per-question timers; non-default-safe pend indefinitely (no expiry — nothing is running). EC-6 headless/scheduled → `no_human_surface` immediately. EC-7 (new) two SPA clients race → first valid wins, loser notified. EC-8 (new) answer arrives for an already-cancelled/answered card id → rejected, no effect. EC-9 (new) delegated child calls the tool → owner-only rejection naming `message_parent`.

## 6. Test plan (v1 tests repaired; the review's seven gaps added)

| # | Test | Level |
|---|------|-------|
| 1 | Schema validation table incl. size caps, dup labels, header cap (M-8) | Unit |
| 2 | Answer assembly: single/multi/free-text/mixed; free-text-presence flag; EC-3 last-interaction-wins | Unit |
| 3 | Park mechanics: Execute returns ParksTurn; needs_input lifecycle; admission slot released (assert no slot held while pending) | Integration |
| 4 | Timer semantics per US-3 definitions: per-question fire; unsubmitted-selection doesn't stop it; 5-min grace; all-auto immediate submit; audit entry | Integration |
| 5 | One-pending-per-routing-session: serial second call rejected; **parallel delegated child rejected (EC-9)** | Integration |
| 6 | Restart: pending persists; reconnect snapshot hydrates; timers continue from persisted timestamps; answer resumes correctly post-restart | Integration |
| 7 | Session Stop / channel `/cancel` cancels the set, unlocks composer | Integration |
| 8 | Contracts: WsFrameType both directions, dual-copy sync, inbound zod, snapshot field; round-trip | Contract |
| 9 | Card component: tabs/auto-advance/progress/badge/underline/countdown/collapsed record incl. origin markers; composer lock; **history-load reconstruction (m-5)** | Component |
| 10 | (v1.1) Channel sequence: pre-turn seam consumes replies; numbered/free/cancel parsing; per-question clocks; restart mid-sequence; both-surfaces race (EC-7) | Integration |
| 11 | `no_human_surface`: AutoDenyAsk path; liveness-predicate path; disconnected-but-exists waits | Integration |
| 12 | E2E: goal-compilation clarifying question through the real card, stubbed LLM | E2E |
| 13 | Server-side submission validation + race: ownership, label membership, arity, first-wins, stale-card rejection (M-7, EC-8) | Integration |

## 7. Traceability (m-4)

| FR | Requirement | Stories | Tests |
|----|-------------|---------|-------|
| FR-1 | Park execution model, no in-memory block | US-1 S1, §0 | 3 |
| FR-2 | Durable pending state, one per routing session, owner-only | US-1 S1, US-6 S3/S5 | 3,5 |
| FR-3 | Answer resume with validated answers | US-1 S3 | 2,13 |
| FR-4 | Cancel/Stop → cancelled, no answers, uniform | US-1 S4, US-6 S2 | 7 |
| FR-5 | Free text always, presence-as-flag, last-interaction-wins | US-2 | 2 |
| FR-6 | Server-side validation + first-valid-wins | US-5 S6, §3 | 13 |
| FR-7 | Raw context, SPA-sanitized render | US-4 | 8,9 |
| FR-8 | Channel mechanism (v1.1): pre-turn seam, sequential, parsing, per-question clocks | US-5 | 10 |
| FR-9 | Restart hydration via reconnect snapshot; timer continuity | US-6 S1 | 6 |
| FR-10 | Approved visuals: flat, tabbed, auto-advance, collapsed record | US-1 S2/S3 | 9 |
| FR-11 | Seeded allow for all agents; always visible; approval interplay | US-7 | 3 (policy assert), 9 |

## 8. Open items (for grill round 2)

1. The inverted liveness predicate's exact interface and its #659 dependency status.
2. Whether the v1.1 pre-turn seam should also serve the goal flow's pending-confirm taxonomy (two pre-turn pending-state consumers — one shared seam?).
3. Mock v3 for multi-select rows (m-3) before implementation.

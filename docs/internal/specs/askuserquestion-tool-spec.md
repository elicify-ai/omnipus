# AskUserQuestion — structured clarification tool spec

- **Status:** Draft v1 (2026-09-05). Grill rounds pending (project standard: two).
- **Authoritative parent:** ADR-074 D4b (Accepted) — every operator ruling recorded there is a FIXED requirement here, not open design: tool name `AskUserQuestion`; up to 10 questions per call; blocked chat input with an always-present Cancel; a free-text option on every question; recommendation = badge + listed first, never pre-selected; rich context previews; fixed global 30-minute timeout for default-safe questions only; plain-text numbered-options degradation on non-SPA channels (no per-channel UI); no answer memory; flat hairline-delimited visual (no card box) matching the chat's transparent assistant messages; tabbed one-question-at-a-time with auto-advance. **Approved visual reference:** `docs/internal/design/askuserquestion-ui-mock.html` (v2, operator-approved 2026-09-05 — "lock that design in").
- **Role in the system:** general-purpose agent tool — any agent, any clarification. First consumer: ADR-074 D4a's goal-compilation clarifying question (which specifies plain-chat fallback until this ships). Goal-specific reply rules (US-3 S9 of the judgment-first spec) apply only when the goal flow uses it; this tool itself is flow-agnostic.

---

## 1. User stories

### US-1 (P0) — An agent asks; the user answers on one structured surface
As an agent mid-turn, I call `AskUserQuestion` with 1–10 questions; my turn blocks until the user answers (or cancels, or default-safe timeouts resolve); the result gives me one answer per question.

Acceptance scenarios:
1. **Given** a call with 3 questions, **When** the card renders, **Then** chat input locks, one question is visible with tabs for the rest, and the composer shows why it's locked.
2. **Given** the user selects an option on question 1, **Then** the tab marks answered, the view auto-advances to the next unanswered question, and the progress counter updates (n/M).
3. **Given** all questions answered, **When** the user presses Answer, **Then** the tool result carries each question's answer (option label, or free text verbatim), the card collapses to the flat question → answer record, and chat unlocks.
4. **Given** the user presses Cancel at any point, **Then** the tool returns `cancelled` (with any partial answers marked as such, unsubmitted), the card collapses to a "cancelled" record, and chat unlocks. The agent decides how to proceed — the tool never retries itself.
5. **Given** a question with `multi_select: true`, **Then** multiple options can be selected and the answer is a list. (The one flagged-assumption from D4b, hereby specified: supported.)

### US-2 (P0) — Free text is always available, through the card only
1. **Given** any question, **Then** a free-text input renders beneath its options ("Something else…"); typing in it deselects options for that question; the free text is returned verbatim, flagged `free_text: true`.
2. **Given** a pending card, **Then** the chat composer stays locked — free-form answering happens in the card's field, never the chat box (D4b ruling; prevents ambiguity about what a typed chat message means).

### US-3 (P0) — Recommendation and default-safe timeout
1. **Given** an option marked `recommended`, **Then** it renders first with the badge and is NOT pre-selected.
2. **Given** a question marked `default_safe: true` (requires a `recommended` option — a call marking default-safe without one is rejected at validation), **Then** a quiet countdown renders; **When** 30 minutes elapse from card render with that question unanswered, **Then** it resolves to the recommended option, marked `auto_default: true` in both the tool result and the collapsed record ("· auto (30 min)").
3. **Given** the card still has unanswered NON-default-safe questions after any timeouts fire, **Then** the card keeps waiting (timeouts never submit past a question that genuinely needs a human).
4. **Given** every remaining unanswered question was default-safe and timed out, **Then** the card auto-submits, each auto answer marked.
5. The 30 minutes is a fixed global constant — not per-call tunable (D4b interview #3 ruling).

### US-4 (P1) — Rich context in a question
1. **Given** a question carrying `context` (markdown: lists, tables, fenced code/diff; media refs from the session's own uploads), **Then** it renders between the question text and the options as a thin left-rule block (flat, per the visual ruling) — so the user decides while looking at the thing.
2. Context is display-only — never parsed for answers, never executable.

### US-5 (P1) — Non-SPA channels degrade to numbered text
1. **Given** the session's surface is a text channel (WhatsApp, Telegram, etc.), **Then** each question is sent as a plain message: question text, numbered options with "(recommended)" inline, and "reply with a number or your own answer."
2. **Given** 2+ questions on a text channel, **Then** they are delivered **sequentially** — one message, wait for the reply, then the next — never a 10-question wall (decision: the SPA's tabs have no text analogue; sequential is the text-native equivalent).
3. **Given** a numeric reply matching an option index, **Then** it resolves to that option; any other reply is the free-text answer. A reply of "cancel" cancels.
4. Default-safe timeout behaves identically (the pending question resolves to the recommendation after 30 minutes, and the channel gets a short "went with: X (no reply in 30 min)" message).

### US-6 (P1) — Persistence and lifecycle
1. **Given** a gateway restart with a card pending, **Then** the pending state (persisted in session meta) re-renders on reconnect; countdown resumes from original render time (not reset).
2. **Given** the session's turn is stopped/cancelled (chat Stop), **Then** the tool returns `cancelled` and the card collapses — a dead turn never leaves a zombie card locking the composer.
3. One pending `AskUserQuestion` card per session at a time (the calling turn is blocked anyway); a second call while one is pending is rejected to the calling agent with a clear error.
4. **Given** the answering context is a delegating AGENT rather than a human (delegated work, ADR-074 D4a's "delegating agent answers"), **Then** v1 scope: the tool is human-surface only — an agent-to-agent clarification uses the existing delegation channels (`message_parent`); the tool returns a `no_human_surface` error if invoked where no human surface exists. (Explicit scope line; extending to agent-answerable is future work.)

## 2. Tool schema (agent-facing)

```
AskUserQuestion({
  questions: [                     // 1..10
    {
      question: string,            // the full question
      header: string,              // short tab label, <=16 chars
      options: [                   // 2..6
        { label: string, description: string }
      ],
      recommended: string?,        // label of the recommended option
      multi_select: bool?,         // default false
      default_safe: bool?,         // default false; requires `recommended`
      context: string?             // markdown preview block
    }
  ]
}) -> {
  status: "answered" | "cancelled",
  answers?: [                      // present when answered; one per question, in order
    { header, selected: [string]?, free_text: string?, auto_default: bool }
  ]
}
```

Validation (400-equivalent tool error, nothing renders): 0 or >10 questions; <2 or >6 options; `default_safe` without `recommended`; `recommended` naming no option; duplicate headers.

## 3. Wire & SPA (Constraint #8 — contract-first)

- New AsyncAPI frames: `ask_user_question` (card payload: questions as authored + card id + rendered-at timestamp) and `ask_user_answer` (submission: card id + answers | cancel). Schemas in `contracts/`, generated Go + zod committed atomically; the SPA edge validates inbound frames like every other frame.
- Pending card state persists in session meta (survives restart, US-6 S1); the boot sweep re-emits the frame (mirrors the goal-pill reconstruction precedent).
- SPA component: `AskUserQuestionCard` per the approved mock — flat hairline zone in the thread, tabs, auto-advance, flat option rows, underline free-text, left-rule context, countdown line, Answer/Cancel footer, collapsed record on completion; composer lock driven by the pending-card store state.

## 4. Non-behaviors

- Never pre-select anything; never auto-default without `default_safe` + `recommended`; never unlock the composer while pending except via Cancel/completion/turn-death; never render per-channel native UI on text channels; never store or reuse past answers (no memory, per ruling); context blocks never influence parsing.

## 5. Edge cases

EC-1 Cancel with some questions already selected → `cancelled`, selections discarded (not partial-submitted). EC-2 default-safe fires while user is mid-card on another tab → the auto answer lands, tab marks, counter updates; if that completes the card it does NOT auto-submit while the user has the card focused with unsubmitted manual selections — Answer stays theirs (auto-submit only fires when ALL answers are auto). EC-3 free text + selection both present on one question → free text wins is WRONG: last interaction wins (typing deselects; re-selecting clears the flag but keeps the typed text visible). EC-4 text-channel reply "0" or out-of-range number → treated as free text. EC-5 10 questions on a text channel, user goes silent mid-sequence → per-question default-safe timeouts apply; non-default-safe silence leaves the card pending (turn blocked) until turn Stop/expiry. EC-6 session with no live human surface at render time (e.g. headless run) → `no_human_surface` error immediately (US-6 S4), never an invisible pending lock.

## 6. Test plan

| # | Test | Level |
|---|------|-------|
| 1 | Schema validation table (counts, default_safe w/o recommended, dup headers) | Unit |
| 2 | Answer assembly: single, multi_select, free-text, mixed; order preserved | Unit |
| 3 | Cancel: partial selections discarded; turn unblocks; result `cancelled` | Integration |
| 4 | Default-safe: fires at 30:00 from render; marks auto; never fires on non-default-safe; all-auto card auto-submits; EC-2 focus rule | Integration |
| 5 | One-pending-per-session; second call rejected | Integration |
| 6 | Restart re-render with countdown continuity | Integration |
| 7 | Turn Stop kills the card, unlocks composer | Integration |
| 8 | Contract round-trip: frames schema-valid, generated types, SPA zod accepts | Contract |
| 9 | Card component: tabs, auto-advance, progress, badge-not-preselected, underline free text, countdown, collapsed record incl. auto marker, composer lock | Component |
| 10 | Text-channel degradation: sequential delivery, numeric/free/cancel parsing, timeout message | Integration |
| 11 | `no_human_surface` on headless | Unit |
| 12 | E2E: goal-compilation clarifying question through the real card (ADR-074 D4a consumer), stubbed LLM | E2E |

## 7. Open items (for the grill rounds to pressure-test)

1. EC-2's focus rule (auto answers never auto-submit past a user mid-card) — needs a concrete "focused" definition (card visible in viewport? any interaction in last N minutes?).
2. Countdown display cadence (live tick vs minute granularity) — cosmetic, component-level.
3. Whether the `ask_user_question` frame carries the context markdown pre-rendered or raw (XSS surface → raw + SPA-side sanitized render, same as chat markdown, is the presumed answer).

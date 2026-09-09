# ADR-078 — Goal confirmation gets a Confirm button, and a pending goal is injected into the agent's context

- **Status:** **Proposed** — amends [ADR-074] D4a (two-phase `/goal`). Awaiting operator ratification.
- **Deciders:** architect (proposing); Daniel Piatkowski (operator, ratifies). Operator chose the "Confirm button + goal in context" option verbatim.
- **Extends / amends:** [ADR-074-judgment-first-criteria.md] D4a (prose `/goal` compile → echo → confirm → activate) and D5.2 (`GoalEchoCard` / `GoalStatusFrame.criteria`). Design source for the underlying flow: `docs/internal/specs/judgment-first-criteria-spec.md` US-3, US-6.
- **Code cited:** `pkg/agent/goal_loop.go::applyGoalPendingReply`, `::confirmPendingGoal`, `::applyGoalCompileOutcome`; `pkg/agent/goal_compile.go::IsGoalConfirm` / `confirmGoalAliases` / `formatGoalEcho`; `pkg/agent/loop.go` runTurn ephemeral-note injection block (~9305–9351) and `::ephemeralSystemNoteTokens` (`pkg/agent/midturn_budget.go:128`); `src/components/chat/GoalEchoCard.tsx`; `src/components/chat/GoalThreadTailCards.tsx`; `src/store/chat.ts::sendMessage`.

## Context

ADR-074 D4a introduced a two-phase `/goal` for prose goals: the engine compiles the intent, stores it as a **pending goal** (`meta.GoalPendingJSON`), echoes it, and activates only when the user confirms. Two defects, verified on a live long-running session:

1. **No affordance to confirm.** `GoalEchoCard` (`GoalEchoCard.tsx:72-76`) renders only the prose "Reply to confirm, or restate to amend." — no button. Activation requires the reply to match `IsGoalConfirm` (`goal_compile.go:560`), i.e. one of the exact tokens `{confirm, yes, ok, okay, activate, y}` (`confirmGoalAliases`). A natural-language reply ("yeah let's do it", "go ahead and also add X") is **not** recognized, so the user "couldn't confirm."

2. **The pending goal is decoupled from the agent's context.** In `applyGoalPendingReply` (`goal_loop.go:359-406`), any reply that is not an exact confirm token — and is not a clarification answer — hits the terminal `return false, ""` (`goal_loop.go:405`). The turn then proceeds as an **ordinary LLM turn that never sees the pending goal**. On the live session the goal stayed `goal_pending` forever and the model answered about the *previous* task, with no idea a goal was pending.

Both are the same root problem from two sides: the only channel between a pending goal and the user is a single exact-token string match, and the model behind the turn is blind to the pending state.

**Operator's ratified decision (verbatim option): "Confirm button + goal in context."** Add a Confirm (and Cancel/Amend) affordance so the user clicks to activate — no guessing the confirm word. AND while a goal is pending, inject it into the agent's context so any typed reply is handled *with* the goal in view (a tweak amends it, a question is answered about it, a confirmation activates it), never silently dropped to the prior conversation.

This ADR does not touch the marker-only single-phase path, the clarifying-question path, or the amendment-of-an-active-goal path — all three already work and are pinned unchanged (see D3).

## Decisions

### D1 — `GoalEchoCard` gains Confirm / Cancel / Amend buttons; Confirm and Cancel reuse the existing chat-message path (NO contract change)

**Send mechanism — decided: reuse `sendMessage`; no new wire type, no store action, no REST/WS frame.** The pending-goal reply router (`applyGoalPendingReply`, called from `processMessage` at `loop.go:7789`, before `runAgentLoop`) already classifies a bare chat message against the pending state. Clicking a button therefore sends an ordinary chat message and the existing deterministic router handles it unchanged:

- **Confirm** → `sendMessage("confirm")`. `applyGoalPendingReply` sees a bare message, `GoalPendingJSON != ""`, `IsGoalConfirm("confirm") == true`, calls `confirmPendingGoal` → fresh activation rewrites the turn into round 1 (`goal_loop.go:391-402`). No code path in the backend changes for Confirm.
- **Cancel/Dismiss** → `sendMessage("/goal clear")`. This is a slash command, so it is caught earlier by `handleCommand` → `applyGoalCommandPrompt` → `isGoalClearVerb` → `clearGoal` (`goal_loop.go:85-87`, `:603`), which clears every goal field including `GoalPendingJSON` (`clearGoal`'s `hadGoal` explicitly covers a pending-but-unconfirmed goal, `goal_loop.go:610-612`) and emits the terminal `cleared` pill. **A dedicated `/goal cancel` already exists** as a clear alias (`GoalClearAliases`, referenced in the router's `isGoalClearVerb`) — but Cancel sends `/goal clear` because the whole clear family lands in the same `clearGoal` body; the alias choice is cosmetic. Decision: send `/goal clear`.
- **Amend** → **no message sent.** The button focuses the composer and pre-fills `"/goal "` via the AssistantUI composer runtime (`useComposerRuntime().setText("/goal ")`, precedent: the composer-runtime usage already mocked across `ChatScreen.*.test.tsx`). The user completes their restatement; `/goal <new intent>` over a pending goal replaces the pending compile (spec US-3 S8, EC-5). Amend deliberately does **not** mutate goal state on click — restatement stays an explicit user action, honoring INV "a routine chat action never silently mutates goal state."

**Why not a dedicated store action / WS frame / REST call:** the reply router is the single, tested authority on pending-state transitions (`goal_two_phase_test.go::TestGoalTwoPhase_PendingConfirmReplyTaxonomy`). A parallel activation path (store action calling a new REST endpoint) would duplicate the confirm/clear semantics, require a new contract surface under Constraint #8, and create a second place where the `queued → active` transition can drift. Routing the button through `sendMessage` keeps exactly one activation path. **Constraint #8 is not engaged: the buttons send existing chat messages over the existing `chat` WS frame; `GoalStatusFrame.criteria` (the data the card renders) already shipped with ADR-074 D5.2. No schema changes.**

**Card wiring.** `GoalEchoCard` stays presentational; it gains three optional callback props (`onConfirm`, `onCancel`, `onAmend`). `GoalThreadTailCards` (the container that already reads `goalPills` from the store, `GoalThreadTailCards.tsx:27-52`) supplies them: `onConfirm = () => sendMessage('confirm')`, `onCancel = () => sendMessage('/goal clear')`, `onAmend = () => composer.setText('/goal ')`. The existing prose line ("Reply to confirm, or restate to amend.") stays as a secondary hint below the buttons — a channel user with no card can still confirm by typing.

**Chat is not blocked** while the goal-echo card is showing (unlike the `AskUserQuestion` card, ADR-074 D4b, which blocks input). A pending goal is non-modal: the user may keep chatting, and D2 guarantees those messages are handled with the goal in view.

### D2 — While a goal is pending, inject it into the turn's context as an ephemeral system note

**Seam — decided:** the per-turn ephemeral system-note injection block in `runTurn` (`pkg/agent/loop.go`, the block at ~9305–9351 that splices `buildScratchpadNote`, `injectWorkspaceInstructions`, `injectWebRenderingNote`, `injectManifestNote` at index 1 of `callMessages`). Add one more injector, `injectGoalPendingNote(callMessages, al.buildGoalPendingNote(ts.opts.TranscriptStore, ts.opts.TranscriptSessionID))`, alongside the others. This is the correct seam because:

- It is **rebuilt fresh every turn from session meta** and **never persisted to history** (identical lifecycle to the scratchpad note, `loop.go:9306-9309`) — so it appears exactly on the turns where `GoalPendingJSON != ""` and vanishes the moment the goal activates or is cleared, with zero on-disk state.
- It sits **after the system prompt, before conversation history** (index 1), where the model reliably reads standing per-turn instructions.
- `ts.opts.TranscriptStore` / `ts.opts.TranscriptSessionID` are already in scope here (used pervasively in `runTurn`), and `al.ResolveSessionStore` is the fallback if the store pointer is nil.

**What the note contains** (`buildGoalPendingNote`, a new function co-located with the other note builders):

1. A statement that a goal is **compiled and awaiting the user's confirmation** — not yet active.
2. The goal **intent/condition** and its **itemized acceptance criteria in plain language** (reuse `formatGoalEcho`'s per-criterion rendering, or the shared `criterionEchoLine` helper — `goal_compile.go:534`, so the note and the card and the chat echo all read identically). Technical payloads are included verbatim as they already are in the echo.
3. An **instruction on how to treat the user's current message** while pending:
   - If it expresses confirmation intent → tell the user to reply **confirm** or click **Confirm**. (The model does **not** activate the goal itself; activation stays deterministic in `applyGoalPendingReply`/`confirmPendingGoal`. This keeps a near-miss like "sounds good" from silently mutating state while still guiding the user to the deterministic gate.)
   - If it asks to **change** the goal → answer helpfully and tell the user to restate with `/goal <new intent>` (or click **Amend**); do not assume the change is applied.
   - Otherwise (a question, or unrelated) → **answer it with the pending goal in view**; the goal remains pending.

**`buildGoalPendingNote` gates itself to nil/empty:** it returns `""` when the store/session is nil, when `meta.GoalPendingJSON == ""`, or when a **clarification** is pending (`meta.GoalClarificationJSON != ""`) — a clarification already has its own conversational surface and its own reply routing (`goal_loop.go:381-389`); double-injecting would confuse the model about whether it is answering a question or reviewing a compiled goal. An empty note is a no-op in the injector (matching `injectWorkspaceInstructions`' empty-note contract).

**Token accounting — MUST update `ephemeralSystemNoteTokens`** (`midturn_budget.go:128-142`): add `add(al.buildGoalPendingNote(ts.opts.TranscriptStore, ts.opts.TranscriptSessionID))` next to the scratchpad/workspace/web-rendering `add(...)` calls. The pre-turn window-budget check (`loop.go:8975`) sums this into `nonMessageTokens`; omitting it would let the pending note push the assembled request past the budget the check believed it was protecting (the exact failure the C1 comment at `loop.go:8965` warns about). One criteria ladder is small (tens to low-hundreds of tokens), but correctness, not size, is the reason.

### D3 — Fate of `applyGoalPendingReply`'s `return false, ""` fall-through: it stays, and D2 makes it context-aware

The blind `return false, ""` at `goal_loop.go:405` is **kept** — the router must not itself route a non-confirm reply through the goal-compile or amendment path (spec US-3 S9 / the behavioral contract §3: "a routine chat message never silently mutates goal state"; `TestGoalTwoPhase_PendingConfirmReplyTaxonomy` pins bare-non-confirm passthrough leaving pending intact). Changing the router to recompile on bare text would violate INV-1b and re-open a silent-mutation hole.

Instead, the operator's requirement — the fall-through must **not proceed context-blind** — is satisfied by D2: when the router returns `false` with `GoalPendingJSON` still set, the turn continues into `runAgentLoop → runTurn`, where the D2 injector reads that same still-set `GoalPendingJSON` and injects the pending goal. The two mechanisms compose: the router owns deterministic state transitions (confirm/clarify only), the injector owns the model's awareness. This is the "lets the turn continue but guarantees the context injection happens" option, chosen over re-routing.

**Behavioral guarantee:** for every reply while `GoalPendingJSON != ""`, exactly one of: (a) an exact confirm token → deterministic activation (router); (b) a clarification answer → deterministic resumed compile (router); (c) anything else → the turn runs with the pending goal injected (D2). Case (c) is the previously-blind path, now context-aware.

### D4 — Interaction with the existing flows (confirmed non-breaking)

- **`/goal confirm` command path** (`isGoalConfirmVerb` → `applyGoalCommandPrompt`, `goal_loop.go:88-110`): untouched. The Confirm button sends the bare token `confirm`, which the router already treats identically to `/goal confirm` for a fresh pending goal.
- **Fresh-activation turn rewrite** (`opts.UserMessage = startCondition`, `goal_loop.go:399` and `:106`): untouched. The Confirm button reaches `confirmPendingGoal` by exactly the existing bare-token branch, which already performs the rewrite.
- **`GoalClarification` (clarifying-question) path** (`goal_loop.go:381-389`): untouched, and explicitly **excluded** from D2 injection (a clarification is pending, not a compiled goal). The router still routes the next bare message as the answer. The Confirm button is not rendered during a clarification because the card renders only on the `queued` pill (`GoalThreadTailCards.tsx:39`), and a clarification does not emit a `queued` criteria frame.
- **Judge/verifier goal-window feeds** (`checkGoalLoopAfterTurn`, `runGoalAdjudication`): untouched. Those fire only for an **active** goal (`meta.GoalCondition != ""`, `goal_loop.go:838`); the D2 note exists only while the goal is **pending** (`GoalCondition == ""`, `GoalPendingJSON != ""`), so the two never overlap. The pending note is never seen by the Judge (it is a per-turn ephemeral note, never persisted to the transcript the Judge reads).
- **Marker-only single-phase path** (`goal_loop.go:158-236`): untouched — it never sets `GoalPendingJSON` (it activates immediately), so `buildGoalPendingNote` returns `""` for it.
- **Amendment of an active goal** (`proposeGoalAmendment`, `goal_loop.go:413`): sets `GoalPendingJSON` **while `GoalCondition != ""`**. Note the nuance: the active goal is running, and a pending *amendment* is parked. `buildGoalPendingNote` should therefore key on `GoalPendingJSON != "" && GoalCondition == ""` (fresh pending only), NOT on `GoalPendingJSON != ""` alone — otherwise it would inject a "goal awaiting confirmation" note during an active goal's amendment window, contradicting the running goal. **Decision: the D2 note fires only for a fresh pending goal (`GoalCondition == ""`).** An active-goal amendment keeps its existing deterministic `/goal confirm` flow and is out of scope for this ADR.

### D5 — Width fix (context only, not redesigned)

The already-applied container width fix — `GoalThreadTailCards` root is now `w-full max-w-3xl mx-auto px-4 pb-2` (`GoalThreadTailCards.tsx:44`) — stands as-is. The buttons render inside this container; no layout change beyond adding the button row to the card body.

## Test plan

**Backend (`pkg/agent`):**

1. `TestGoalPendingContext_InjectedWhileFreshPending` (new): with `GoalPendingJSON` set and `GoalCondition == ""`, `buildGoalPendingNote` returns a non-empty note containing the intent and each criterion's plain-language text; assert the note is present in `callMessages` at index 1 for that turn (or unit-test `buildGoalPendingNote` directly + a thin injector test mirroring `injectWorkspaceInstructions`' tests).
2. `TestGoalPendingContext_EmptyWhenNoPendingOrClarificationOrActive` (new): returns `""` when `GoalPendingJSON == ""`; when `GoalClarificationJSON != ""`; and when `GoalCondition != ""` (active goal, incl. active-goal amendment window) — the three gates in D2/D4.
3. `TestGoalPendingContext_BudgetAccounted` (new): `ephemeralSystemNoteTokens` includes the pending-note tokens when a fresh pending goal exists (guards the `loop.go:8975` C1 contract).
4. Extend `TestGoalTwoPhase_PendingConfirmReplyTaxonomy` (`goal_two_phase_test.go:796`): the bare token `"confirm"` (the exact string the Confirm button sends) activates and rewrites into round 1; a bare non-confirm reply still leaves the pending goal intact **and** (new assertion) the subsequent turn carries the pending note. `"/goal clear"` (the Cancel button) clears the pending goal to the `cleared` pill.
5. No new test asserts the router recompiles on bare text — that would be a regression; the existing passthrough pin stays green.

**Frontend (`src/components/chat`):**

6. `GoalEchoCard.test.tsx`: add cases — renders Confirm, Cancel, Amend buttons; `onConfirm`/`onCancel`/`onAmend` fire on click; the "no [kind] tokens" and criteria-render assertions stay green (buttons don't change criteria rendering). Update the existing "shows the conversational confirmation prompt (no buttons/form)" case (`GoalEchoCard.test.tsx:113`) — that assertion is now inverted (buttons ARE present); keep the secondary prose hint assertion.
7. `GoalThreadTailCards.test.tsx`: clicking Confirm calls `sendMessage('confirm')`; Cancel calls `sendMessage('/goal clear')`; Amend calls the composer runtime's `setText('/goal ')` and does NOT call `sendMessage` (mock `useChatStore.sendMessage` and `useComposerRuntime`). The `queued`-only render gate and the G-5 `waiting_on_user` negative (does not render the card) stay green.

**No contract test changes** — no wire type changes (D1).

## Consequences

**Positive**
- The user can confirm with one click; a natural-language reply no longer strands the goal, because the model now answers with the goal in view and steers the user to Confirm.
- Symptom (3) is fixed at the root: the pending goal is coupled to the model's context on every pending turn.
- Zero new wire surface, zero new state store, one new activation path avoided — the deterministic router stays the single authority.

**Negative / risks**
- **Prompt-size on pending turns:** every turn while a fresh goal is pending carries the extra note. Bounded — one criteria ladder, tens-to-low-hundreds of tokens, and it exists only in the (typically short) pending window before confirm/clear. Accounted for in the budget check (D2).
- **Confirm-button race with an already-answered goal:** if the user types `confirm` (or the goal is cleared) and then clicks the stale Confirm button, the click sends a second `confirm`. By then `GoalPendingJSON == ""`, so `applyGoalPendingReply` returns `false` and `confirm` falls through as ordinary chat — harmless (the model sees a bare "confirm" with no pending note). No double-activation: the second `confirmPendingGoal` early-returns "No pending goal to confirm" (`goal_loop.go:456-458`). Accepted; no debounce required, though the card MAY disable its buttons once it observes the pill leave `queued`.
- **Model does not auto-activate on natural-language confirmation** by design (activation stays deterministic). A user who writes "yes do it exactly, thanks" gets a model reply asking them to click Confirm / reply `confirm`, rather than instant activation. This is the deliberate trade for not letting an LLM mutate goal state. `IsGoalConfirm` already covers the common `{confirm, yes, ok, okay, activate, y}` tokens, so the friction is limited to longer phrasings.
- **`[INFERRED]` composer prefill API:** the Amend button assumes `useComposerRuntime().setText(...)` is available on the AssistantUI composer runtime used by this chat surface. Evidence: `useComposerRuntime` is imported/mocked across `ChatScreen.*.test.tsx`. The implementer must confirm the exact method name (`setText` vs `setValue`) against the installed AssistantUI version; if unavailable, fall back to a chat-store composer-draft setter. This does not affect Confirm/Cancel, which need no composer access.

## Affected files

**Backend**
- `pkg/agent/goal_compile.go` (or a new `goal_pending_note.go`): add `buildGoalPendingNote` (reusing `criterionEchoLine`/`formatGoalEcho` rendering).
- `pkg/agent/loop.go`: add `injectGoalPendingNote` call in the runTurn ephemeral-note block (~9337–9350); add the `injectGoalPendingNote` helper (mirror `injectWorkspaceInstructions`).
- `pkg/agent/midturn_budget.go`: add the pending-note `add(...)` in `ephemeralSystemNoteTokens`.
- `pkg/agent/goal_loop.go`: **no logic change** — `applyGoalPendingReply`'s `return false, ""` is retained (D3); a doc-comment note pointing at the D2 injector is the only edit.

**Frontend**
- `src/components/chat/GoalEchoCard.tsx`: add Confirm/Cancel/Amend buttons + `onConfirm`/`onCancel`/`onAmend` props.
- `src/components/chat/GoalThreadTailCards.tsx`: wire the three callbacks to `sendMessage` / composer `setText`.
- Tests: `GoalEchoCard.test.tsx`, `GoalThreadTailCards.test.tsx`, `pkg/agent/goal_two_phase_test.go`, plus new backend note tests.

**Contracts:** none.
</content>

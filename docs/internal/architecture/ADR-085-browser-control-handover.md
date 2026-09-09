# ADR-085 — Taking the browser wheel parks the turn; it does not cancel it

- **Status:** Proposed (awaiting operator ratification) — 2026-09-09
- **Relates to:** ADR-039/ADR-040/ADR-041 (live browser view, implicit control), ADR-053 §5.1 (turn parking), ADR-082 (UI-independent turns)
- **Spec:** `docs/internal/specs/browser-control-handover-spec.md`

## 1. Operator direction (verbatim, 2026-09-09)

> the cancel is not good, we need a more elegant solution, like the browser tool signals the agent that the user took control, but does not cancel the turn

and, on handing back:

> we do not need the hand back button, the user has to prompt again

and, on what the agent does meanwhile:

> [continue other work] yes it should continue other work

## 2. Evidence

Operator report, 2026-09-09: a turn ended with "This turn was stopped before it finished." The operator had not clicked Stop — they took control of the live browser while the agent was driving it.

The transcript is unambiguous about the mechanism:

```
type: turn_canceled, canceled_by_user: admin, canceled_by_channel: web,
cancel_method: graceful, descendants_canceled: [jim-turn-6]
```

This is by design today, and documented at the top of `src/components/browser/BrowserLiveView.tsx`:

> the FIRST interactive action — the "Take over" button, a frame click, a tab-strip click, or a URL submit — pauses the agent (reuses the chat store's existing `cancelStream` — the same action the chat Stop button calls) AND acquires the lock AND … dispatches that same action, all in ONE take

So "pause the agent" was implemented as "cancel the turn". Three consequences:

| # | Consequence | Evidence |
|---|---|---|
| E1 | The turn is destroyed, not paused. | `RequestCancel` is the sole gateway cancel entry point; the turn ends as `turn_canceled`. |
| E2 | The message misattributes it to the user aborting. | "This turn was stopped before it finished." — indistinguishable from a Stop click. |
| E3 | There is no way back. | An earlier decision (ADR-040 D1) deliberately removed the explicit toggle; `BrowserLiveView.controlToggle.test.tsx` asserts no "Take control" / "Release control" / "Hand to agent" button exists. |

Escape inside the frame is a deliberate local no-op (WCAG 2.1.2, no keyboard trap) and does **not** cancel — that part is correct and stays.

## 3. Precedent

ChatGPT's agent parks rather than cancels: it pauses for a takeover (typically at a login the agent should not perform), the operator acts, control returns, and the agent resumes from its prior state; it also suspends screenshot capture while the human drives, so credentials do not enter the record. Claude Cowork addresses the same problem upstream, via permission modes and confirmation before consequential actions, and documents no takeover/hand-back cycle. Our current behaviour is the outlier.

## 4. Decisions

### D1 — Taking the wheel parks the turn

The first interactive action while the agent is working acquires the control lock and **parks** the enclosing turn instead of cancelling it. The mechanism already exists and is proven: a tool result flagged `ParksTurn` (`pkg/tools/result.go`) makes `runTurn` end the turn cleanly — not an error, not an abort — with the pending state recorded durably. `AskUserQuestion` and `message_parent(kind=question, wait=true)` already use it.

No `turn_canceled` entry, no interrupted status, no `RequestCancel`.

### D2 — Resume is a prompt, not a button (operator decision)

No hand-back control is added; ADR-040 D1's removal of the explicit toggle stands. The operator's next message resumes the work and returns the wheel to the agent. The resume dispatch carries the current page state (URL, title) so the agent re-orients rather than assuming its pre-handover view.

### D3 — Browser actions are refused politely while the operator holds the wheel

The lock is tracked already (`LiveViewRegistry`, `controlledByOther`) but the action tools never consult it. They will: an attempted navigate/click/type while the operator drives returns an ordinary tool result stating control is with the operator and the action was not performed — not an error, not a retry-able failure. The result says explicitly not to retry until control returns; after a bounded number of attempts the agent is told to state what it is waiting for and stop trying.

### D4 — The agent continues non-browser work (operator decision)

Parking is scoped to the browser, not the agent. Work that does not need the browser proceeds.

### D5 — The agent can hand over deliberately

A new **action on the existing browser tool family** (not a new tool, no new policy entry): hand the browser to the operator, flagged `ParksTurn`, with a reason. Used when the agent reaches a sign-in or anything it should not do on the operator's behalf. This is the ChatGPT precedent and it also gives the operator a natural moment to act.

### D6 — Capture is suspended while the operator drives

Screenshot capture and frame persistence stop while the lock is held by the operator, resuming when control returns. Today an operator who takes the wheel to log in has their credentials screenshotted into the transcript. This is a live privacy defect independent of everything else here.

### D7 — Waiting is visible

The chat states that the agent is paused because the operator took the browser, and what it is waiting for. Silence that implies progress is the same defect the goal acknowledgement line fixed.

### D8 — One park per chat surface

`AskUserQuestion` refuses a second card while one is pending ("one card per chat surface"). Browser parking respects the same single-park invariant: the two must not collide, and the failure mode when they would must be explicit rather than a silent overwrite.

### D9 — Stop still cancels

The Stop button and `/cancel` keep their current meaning. Only the take-the-wheel path changes.

## 5. Consequences

- A turn survives an operator taking the browser; nothing is destroyed.
- The operator can be away indefinitely without a wedged turn: the turn already ended cleanly and the pending state is durable.
- The transcript stops recording an operator's steering action as an abort.
- Credentials typed during a takeover no longer reach the record.
- The agent may occasionally have nothing non-browser to do and will end its turn saying it is waiting — which is correct, and visible per D7.

## 6. Out of scope

- Reinstating an explicit control toggle (ADR-040 D1 stands; operator reconfirmed).
- Changing Escape-in-frame behaviour (WCAG local release, correct today).
- The Judge's stance (ADR-084).

# WU-9 draft — `AskUserQuestion` description text (FR-014) and God Mode note (FR-015)

Spec basis: FR-014 ("`AskUserQuestion`'s description MUST permit one plan-confirmation use; the Go question MUST never set `default_safe`"), FR-015 (one-line God Mode mode note), ADR OSA-2 item 5 ("`AskUserQuestion`'s description must allow exactly one use as a plan confirmation (C11); Ava's 'never as permission' line disappears with Ava"), ADR OSA-3 (the gate; C24: a `default_safe` card auto-resolves with no human).

**Target when wired:** `pkg/tools/ask_user_question.go::AskUserQuestionTool.Description()` (backend-lead edits the Go string; WU-10's SPA already renders the card). Until wired, this file is the text of record.

## Change 1 — the one permitted plan-confirmation use

Current text forbids permission-seeking outright:

> … never a placeholder, a test question, or a request for permission to proceed.

Replace that clause with:

> never a placeholder or a test question. The one exception is an agent following a plan-and-confirm workflow: it may use exactly one card as its plan confirmation — one question presenting the plan summary with options such as Go, Change and Cancel — before it runs its first change. That single card is the workflow's gate; anything beyond it (approving an individual tool call, confirming another plan, or asking again after Go) is not a clarification and must not use this tool.

## Change 2 — `default_safe` never on the plan-confirmation question

Current text offers `default_safe` generally:

> Add `default_safe: true` on a question (requires that question's `recommended`) to auto-resolve it to the recommendation after 30 minutes without an answer.

Append to that sentence:

> Never set `default_safe` on a plan-confirmation question (a Go / Change / Cancel card): a plan may start only from a real human answer, so that question must always wait for one, however long it takes.

Rationale on the wire: `default_safe` auto-resolves with no human (C24), so a Go card carrying it would let changes start unattended — the exact behaviour amendment §1.2 R1/R4 refuse for unattended roots.

## Change 3 — God Mode mode note (FR-015, separate carrier)

Not part of this description. One line injected into Omnipus's per-turn context only when God Mode is active (`GodModeActive(cfg)`), full text in `core-prompt.md`:

> God Mode is active: post the plan summary as an ordinary message and proceed without the Go question and without tool approvals — including changes another agent asked for. Clarification questions for genuinely missing information and secret cards still appear.

## Untouched on purpose

- Web-only gating, delegated-session relay, `recommended` semantics, option schema, park/resume mechanics: unchanged — this draft edits only the two clauses above.
- Wording tests that pin this text: spec test 51 (`TestOmnipusPrompt_GateRulesAndRoutingTable`, wording only) asserts the gate rules; BDD-3.1 asserts a Go/Change/Cancel card **without** `default_safe` precedes `create_agent`.

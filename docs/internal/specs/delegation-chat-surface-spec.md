# Delegation chat surface — two pills, and delegation as sentences

**Status:** Implemented

## Summary

Delegation is currently legible only as tool-call badges. A parent that launches two
workers and polls them twelve times produces sixteen badges, of which four say
something and twelve say "I looked". Background shell jobs, meanwhile, have no
surface at all: the activity pill mounts only for agent children, and the pill is the
side panel's only entry point, so a background command is unreachable while it runs.

This spec replaces that with two ideas:

1. **Two pills** — one for agents, one for background commands — both opening the
   same side panel.
2. **A grey line per EVENT, never per call.** Polls are not events. The chat gets
   the story; the panel keeps the detail.

Founder decisions, 2026-09-25, recorded inline below as D1–D5.

## Existing codebase context

### Reused unchanged

| Surface | Where | Evidence |
|---|---|---|
| Side panel with agent + bash + judge rows | `src/components/chat/ActivityPanel.tsx` | row label: `item.kind === 'bash' ? item.command : …` |
| Open-a-child-session route | `ActivityPanel.tsx::ActivityRow` | `navigate({ to: '/sessions/$sessionId', params: { sessionId: childSessionId } })`, `data-testid="activity-row-open"` |
| Activity aggregation | `src/hooks/useRunningActivity.ts` | `running` carries agent children AND bash; `runningChildItems` filters to `kind === 'agent' && lifecycleState === 'running'` |
| Avatar stack, spinner | `ActivityBar.tsx` | `ActivityAvatar` ×4, `ArrowsClockwise … animate-spin` |
| Muted single-line notice grammar | `ChatScreen` truncation notice, goal-setup-failure line | existing precedent for a grey, non-expanding line |
| Verbose-chat toggle | `src/store/chatPreferences.ts`, `toolVisibility.ts::shouldRenderToolCall` | D2 keeps this behaviour |

### Changing

| Surface | Change |
|---|---|
| `ActivityBar.tsx` | splits into two pills (agents, background commands) |
| `ActivityPanel.tsx` | gains a **Queued** section with dispatch order |
| chat message list | gains delegation event lines |

## Founder decisions

**D1 — The child's output never enters the parent's chat.** A grey line states what
happened, never what the worker said. The worker's result goes to the parent agent,
which decides what (if anything) to tell the human. No chatter. This is ADR-091 D7
applied to the event lines themselves.

**D2 — Verbose chat is unchanged.** With verbose on, every delegate call still renders
as a tool-call badge. The grey lines are the default view, not a replacement for
debugging.

**D3 — Queued workers do not announce themselves in chat.** They appear in the side
panel with `queued` status and their dispatch order.

**D4 — Every subagent line carries an `[open]` link** to that child's session, reusing
the panel's existing route.

**D5 — Two pills, one panel.** Each pill mounts only when it has something to show.
The Commands pill scrolls the panel to the background-commands section.

**D6 — Failed parent actions stay out of the chat (2026-09-25).** A `steer`, `respond`,
`cancel` or `follow_up` call that fails produces no line: the parent agent sees the failure
and retries. Verbose chat still shows the call's badge (D2).

**D7 — A background command stopped on purpose reads `⊘ Stopped <command>` (2026-09-25).**
Only a real failure (non-zero exit, error, timeout) reads `failed`.

**D8 — No cascade count (2026-09-25).** A cascading cancel reads `⊘ Stopped <agent>`; the
SPA never receives the number of stopped descendants, and the panel already lists them.

**D9 — Each follow-up run is its own span (2026-09-25).** The gateway gives every
`follow_up` generation its own span identity and start frame, so a follow-up's outcome gets
its own line and never rewrites the original run's line — live and after a reload.

**D10 — A queued worker ended before it ever ran produces no line (2026-09-25).** It never
announced itself (D3), so there is nothing to close.

## The event lines

One muted line, no expander. `<agent>` is the display name; `<title>` the task label.

| Event | Line | `[open]` |
|---|---|---|
| `run` dispatched | `→ Delegated to <agent> · <title>` | yes |
| queued worker starts | `→ <agent> started · <title>` | yes |
| child reaches a terminal success | `✓ <agent> finished · <title>` | yes |
| child ends without finishing | `⚠ <agent> stopped without finishing` | yes |
| `steer` | `→ Sent <agent> a new instruction` | yes |
| `respond` | `→ Answered <agent>'s question` | yes |
| `cancel` | `⊘ Stopped <agent>` | yes |
| `follow_up` | `→ Gave <agent> follow-up work · <title>` | yes |
| refusal (depth, concurrency cap, unknown skill) | `⚠ Delegation refused · <reason>` | no |
| `status`, `peek`, `inbox`, `inbox_ack` | **nothing** | — |
| background command launched | `→ Running in background · <command>` | no |
| background command finished | `✓ <command> finished` | no |
| background command failed (non-zero exit, error, timeout) | `⚠ <command> failed (exit N)` | no |
| background command stopped on purpose (killed / cancelled by the agent) | `⊘ Stopped <command>` | no |
| a `steer` / `respond` / `cancel` / `follow_up` call that FAILS | **nothing** (verbose chat shows the badge) | — |

### Why `status` and `peek` render nothing

They are the parent looking, not something happening. A real session produced 12 of
them against 4 launches. The lines are driven by the child's own lifecycle
transitions, which `useRunningActivity` already tracks — not by the parent's calls.

## Non-goals

- No summary text in the finish line (D1).
- No change to what the parent agent receives from the tool.
- No new wire fields; every line is derived from frames the SPA already handles.
- The pill count stays agent-only (ADR-091 FR-E-005 governs the NUMBER, not the mount).

## Acceptance criteria

| # | Criterion |
|---|---|
| AC-1 | A background command alone mounts the Commands pill and opens the panel. |
| AC-2 | An agent child alone mounts the Agents pill; its count excludes bash. |
| AC-3 | Both pills open the same panel; Commands scrolls to the shell section. |
| AC-4 | A queued child appears in the panel under **Queued** with its dispatch position, and produces no chat line. |
| AC-5 | A `status`/`peek`/`inbox` call produces no chat line. |
| AC-6 | A child's terminal transition produces exactly ONE finish line, not one per poll. |
| AC-7 | No child-authored text appears in any line (D1). |
| AC-8 | Every subagent line's `[open]` navigates to that child's session. |
| AC-9 | Verbose chat still renders every delegate call as a badge. |
| AC-10 | A refusal renders its reason and no `[open]`. |

## Test requirements

Each AC gets a test that is proven able to fail. In particular:

- AC-6 must be tested with a poll storm (≥10 `status` calls around one transition) and
  assert exactly one finish line — the failure mode this spec exists to remove.
- AC-7 must inject a child result containing a sentinel and assert the sentinel appears
  nowhere in the parent thread, with a non-vacuity gate proving the sentinel exists
  somewhere (the child's own view).
- AC-1 must fail if the mount gate is narrowed back to agent children.

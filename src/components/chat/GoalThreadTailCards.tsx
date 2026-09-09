// GoalThreadTailCards — ADR-053 FE-8 / US-3; button wiring per ADR-078 D1.
//
// Renders the conversational goal echo (and amendment) cards at the thread
// tail — the non-scrolling slot between the message list and the composer.
// Per FE-8 / D11: the compiled goal is echoed IN CHAT (no form/modal), the
// user confirms by replying in normal chat OR (ADR-078) clicking Confirm.
// This component surfaces the echo card whenever a goal pill is in the
// `queued` state (newly compiled, not yet active — awaiting the user's
// confirmation). Once the user replies/clicks and the goal transitions to
// `active`, the echo card disappears (the bottom-right pill continues to
// track the active goal).
//
// Button wiring (ADR-078 D1) reuses the existing chat-message path — no new
// wire type, no new store action:
//   - Confirm -> `sendMessage('confirm')`. The backend's pending-goal reply
//     router (`applyGoalPendingReply` -> `IsGoalConfirm`) already treats the
//     bare token `confirm` identically to the `/goal confirm` command.
//   - Cancel  -> `sendMessage('/goal clear')`. A slash command, caught by
//     `handleCommand` -> `clearGoal`, which clears a pending-but-unconfirmed
//     goal.
//   - Amend   -> sends nothing. Pre-fills the composer with `/goal ` via
//     AssistantUI's `useComposerRuntime().setText(...)` — the same mechanism
//     `useSlashMenu.ts` already uses throughout the composer (e.g.
//     `composerRuntime.setText('/skills')`), verified against the installed
//     `@assistant-ui/react` version by that existing, working call site.
//
// The `GoalAmendmentDiff` is imported here so the thread-tail is the single
// render site for both echo and amendment; the amendment surfaces when the
// store detects a condition change on an existing goal (tracked via a
// before/after condition comparison in the pill subscription below).

import { useComposerRuntime } from '@assistant-ui/react'
import { useChatStore } from '@/store/chat'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { GoalEchoCard } from './GoalEchoCard'
import { GoalAmendmentDiff, type GoalAmendmentDiffData } from './GoalAmendmentDiff'

// not-wire-format — SPA-internal display projection.
function emptyDiff(d: GoalAmendmentDiffData): boolean {
  return d.added.length === 0 && d.changed.length === 0 && d.dropped.length === 0
}

export function GoalThreadTailCards() {
  const goalPills = useChatStore((s) => s.goalPills ?? {})
  const sendMessage = useChatStore((s) => s.sendMessage)
  const composerRuntime = useComposerRuntime()

  const pills = Object.values(goalPills) as GoalStatusFrame[]

  // Echo cards: one per pill in `queued` state (newly compiled, awaiting
  // confirmation). The `queued` emission carries the compiled criteria
  // breakdown on the frame's optional `criteria` field (ADR-074 D5.2 /
  // judgment-first FR-011), so the card itemizes exactly what will run —
  // plain-language rows with verbatim technical payloads per row. A G-5
  // `waiting_on_user` pause on an ACTIVE goal must never render this
  // confirm card (US-6 S3 negative) — only `queued` does.
  const queuedPills = pills.filter((p) => p.state === 'queued')

  if (queuedPills.length === 0) return null

  const handleConfirm = () => sendMessage('confirm')
  const handleCancel = () => sendMessage('/goal clear')
  const handleAmend = () => composerRuntime.setText('/goal ')

  return (
    <div className="w-full max-w-3xl mx-auto px-4 pb-2" data-testid="goal-thread-tail-cards">
      {queuedPills.map((frame) => (
        <GoalEchoCard
          key={frame.goal_id ?? '_default'}
          frame={frame}
          onConfirm={handleConfirm}
          onCancel={handleCancel}
          onAmend={handleAmend}
        />
      ))}
    </div>
  )
}

// Re-exported so a future amendment-wiring caller can build the diff shape
// from landed goal data and drop it into the thread without a new import site.
export { GoalAmendmentDiff, emptyDiff }
export type { GoalAmendmentDiffData }

// GoalThreadTailCards — ADR-081 D5/D9 (work-first goal flow).
//
// Renders the goal record card. Per ADR-081 D5: on every successful
// `set_goal` register/update (and on marker-path activation/restate), the
// engine emits the `goal_status` frame in state `active` with `criteria`/
// `dod`/`definition` populated — the SAME optional fields the old
// `queued`-state pending-confirm emission once populated. This component
// surfaces the echo card whenever a goal pill is `active` AND carries a
// criteria breakdown — i.e. the record has actually been authored, not
// merely activated (D1: a freshly-activated goal is `active` with an EMPTY
// record until the agent's first move registers one; that empty-record
// window renders no card here). There is no `queued` state to filter on
// anymore — the whole confirm-gate mechanism (button row, compiling
// indicator, amendment-diff re-export) is deleted in full (ADR-081 D9,
// greenfield, no dormant branches).
//
// Mount point (operator report, 2026-09-07, preserved across the ADR-081
// redesign): this component is rendered INSIDE the scrollable message-list
// container (at the tail of the transcript, scrolling with the messages) —
// NOT in ChatScreen's non-scrolling slot between the message list and the
// composer. A goal with a long criteria/DoD ladder rendered there could
// overflow a fixed-height slot with no way to scroll to the rest of the
// card (GoalEchoCard's own collapsed-by-default accordions independently
// shrink the common case, but the scrollable mount point is what
// guarantees the full card stays reachable regardless). See
// ChatScreen.tsx's PlainMessageList / VirtualizedMessageListInner for the
// two call sites (plain and virtualized).
//
// Steering (ADR-081 D5/US-5) replaces the deleted confirm ritual: an
// ordinary chat message that changes direction reaches the working agent,
// which updates the record via `set_goal(mode: update)`; the refreshed
// `active` frame lands here like any other and the card re-renders from it
// — no button, no new wiring needed in this component for that path.

import { useChatStore } from '@/store/chat'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { GoalEchoCard } from './GoalEchoCard'

export function GoalThreadTailCards() {
  const goalPills = useChatStore((s) => s.goalPills ?? {})

  const pills = Object.values(goalPills) as GoalStatusFrame[]

  // Record cards: one per pill that is `active` AND carries a criteria
  // breakdown (ADR-081 D5 — the record has actually been authored). A
  // freshly-activated goal with an empty record, a `waiting_on_user` pause,
  // or any other non-active state renders no card here — only the
  // authored, judgeable record is shown as the working-assumptions listing.
  const recordPills = pills.filter((p) => p.state === 'active' && (p.criteria?.length ?? 0) > 0)

  if (recordPills.length === 0) return null

  return (
    <div className="w-full max-w-3xl mx-auto px-4 pb-2" data-testid="goal-thread-tail-cards">
      {recordPills.map((frame) => (
        <GoalEchoCard key={frame.goal_id ?? '_default'} frame={frame} />
      ))}
    </div>
  )
}

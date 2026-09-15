// Goal outcome line — founder decision 2026-09-14.
//
// When a goal ENDS, the chat thread shows one lasting line saying how it
// ended. Before this, the only signals were a goal pill deliberately hidden
// 4 seconds after turning terminal (GoalPillTray's TERMINAL_PILL_DISPLAY_MS)
// and the Judge's verdict card, which renders only under Verbose chat — so a
// user who looked away, or reloaded, had no trace of the outcome at all.
//
// Data source (contract-first, Constraint #8): the structured `GoalOutcome`
// (contracts/components/schemas/GoalOutcome.yaml), written once per ending by
// the goal's terminal transition and delivered two ways that share one shape:
//   - the persisted `system_subtype: goal_outcome` transcript entry, read on a
//     cold REST load (src/lib/api.ts's rawToMessage stamps `goalOutcome`), and
//   - the `goal_outcome` WS frame, sent live at the ending and re-sent by
//     replay from the same persisted entry (store/chat.ts's
//     `case 'goal_outcome'` → buildGoalOutcomeInsertion below).
// All three carry the same id, so they converge on ONE thread line.
//
// An intermediate UNMET Judge round (rounds remaining) is not an ending — the
// worker is steered and keeps going — and never produces a GoalOutcome.

import type {
  GoalOutcomeFrame,
  GoalOutcomeFrameOutcome,
} from '@/lib/api/generated/asyncapi-types'
import type { ChatMessage } from '@/store/chat'

/**
 * How a goal ended. The WS copy (`GoalOutcomeFrameOutcome`) and the REST copy
 * (`GoalOutcome` in openapi-types) are hand-synced duplicates of one contract
 * shape — structurally identical, so either is assignable to this alias.
 */
export type GoalOutcome = GoalOutcomeFrameOutcome

/** Visual tone: Forge Gold is reserved for `met`. */
export type GoalOutcomeTone = 'met' | 'not_met' | 'stopped'

export interface GoalOutcomeCopy { // not-wire-format: SPA-only display copy derived from GoalOutcome for the thread row; never sent or received
  tone: GoalOutcomeTone
  /** The outcome in plain words, e.g. "Goal not met after 5 tries". */
  label: string
  /** Optional second line, always visible (truncated to one line until expanded). */
  summary?: string
  /** Optional extra line, visible only when the row is expanded. */
  detail?: string
}

function tries(n: number): string {
  return n === 1 ? '1 try' : `${n} tries`
}

function judgeLine(reason: string | undefined): string | undefined {
  const trimmed = reason?.trim()
  return trimmed ? `Judge: ${trimmed}` : undefined
}

/**
 * The user-facing copy for one outcome. Every count comes from the outcome
 * data itself (`rounds_used` / `max_rounds`) — never a hardcoded limit.
 * Users see "tries", never "rounds".
 *
 * `other` (today: the idle-expiry sweep, or the working agent being deleted;
 * any future terminal brake too) is deliberately a neutral "not met" with the
 * tries count only — the founder-specified variants are exactly met, not met
 * after N tries, and stopped by you.
 */
export function describeGoalOutcome(outcome: GoalOutcome): GoalOutcomeCopy {
  const judge = judgeLine(outcome.judge_reason)
  switch (outcome.ending) {
    case 'met': {
      const total = outcome.criteria_total
      if (total !== undefined && total > 0) {
        return {
          tone: 'met',
          label: 'Goal met',
          summary: total === 1
            ? 'The Judge confirmed the one criterion.'
            : `The Judge confirmed all ${total} criteria.`,
          detail: judge,
        }
      }
      return { tone: 'met', label: 'Goal met', summary: judge }
    }
    case 'rounds_exhausted':
      return {
        tone: 'not_met',
        label: `Goal not met after ${tries(outcome.rounds_used)}`,
        summary: judge,
      }
    case 'stopped_by_user':
      return { tone: 'stopped', label: 'Goal stopped by you' }
    case 'other':
      return {
        tone: 'not_met',
        label: 'Goal not met',
        summary: `Ended after ${outcome.rounds_used} of ${tries(outcome.max_rounds)}.`,
        detail: judge,
      }
  }
}

/** Single-line text form of the outcome (used as the message's `content`). */
export function goalOutcomeHeadline(outcome: GoalOutcome): string {
  return `${describeGoalOutcome(outcome).label} — ${outcome.goal_text}`
}

/**
 * Builds the {messagesById, messageOrder} patch that inserts the outcome line
 * for `frame`, or returns null when a message with the frame's id is already
 * in the bucket. That id is minted once per ending and stamped on the
 * persisted transcript entry too, so a live push, a replay of the same entry,
 * and a cold REST load of it can never produce a second line.
 *
 * Appends at the current tail: live, the goal has just ended now; on replay,
 * frames arrive in transcript order, so the tail IS the place the goal ended.
 * (Mirrors store/chat.ts's buildBrowserHandoverInsertion.)
 */
export function buildGoalOutcomeInsertion(
  b: { messagesById: Record<string, ChatMessage>; messageOrder: string[] },
  frame: GoalOutcomeFrame,
): { messagesById: Record<string, ChatMessage>; messageOrder: string[] } | null {
  if (b.messagesById[frame.message_id]) return null

  const message: ChatMessage = {
    id: frame.message_id,
    role: 'system',
    status: 'done',
    content: goalOutcomeHeadline(frame.outcome),
    timestamp: frame.outcome.ended_at,
    goalOutcome: frame.outcome,
  }
  return {
    messagesById: { ...b.messagesById, [frame.message_id]: message },
    messageOrder: [...b.messageOrder, frame.message_id],
  }
}

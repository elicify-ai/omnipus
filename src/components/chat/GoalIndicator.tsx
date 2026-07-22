// GoalIndicator — ADR-049 D6/US-12/FR-094/SD-C9.
//
// Persistent chat indicator for an active `/goal` session, driven entirely
// by `goal_status` WS frames (the chat store just stores the frame
// verbatim — see chat.ts's `case 'goal_status'`). Renders in the SAME
// non-scrolling composer slot as RateLimitIndicator (SD-C9), directly above
// the composer: persistent (no dismiss control) while the session bucket
// holds a frame, collapses to nothing once the bucket goes back to null
// (session switch/reset) — mirroring ActivityBar's `empty:hidden` precedent
// rather than inventing a new one.
//
// Also renders a compact `/loop` status line (mode/run/next-delay) when a
// `loop_status` frame has arrived for the session — LoopStatusFrame.state is
// an UNCONSTRAINED string on the wire (unlike GoalStatusFrame.state's closed
// enum), so there is no literal this component can compare against to
// auto-hide a "stopped" loop the way it once did for a goal; it simply
// renders whenever `loopStatus` is non-null. Flagged in this wave's report
// as a contract asymmetry for Wave 2 backend to reconcile.
//
// ADR-053 §Contract Surface — "Pill-state enum"/R§8.10: `GoalStatusFrame.state`
// is now an 8-value pill enum (queued/active/waiting_on_user/
// judge_unavailable/re-planning/judging/done/failed), superseding the
// original 4-value active/paused_judge_unavailable/brake_fired/cleared set
// — NO back-compat, and no `cleared` literal survives. This is a MINIMAL
// compat fix to render all 8 states sanely; it deliberately does NOT do the
// FE-1 pill redesign (bottom-right relocation, per-goal-id multi-pill,
// click-to-expand criteria) — that remains a Phase-2 deliverable. Since
// there is no `cleared` state to compare against any more, this component
// renders whenever a non-null `goalStatus` frame is present; the store
// clears the bucket (session switch / reset) rather than a state literal.
//
// `aria-live="polite"` on the root announces state transitions to screen
// readers without stealing focus.

import { Target, Repeat } from '@phosphor-icons/react'
import type { GoalStatusFrame, LoopStatusFrame } from '@/lib/api/generated/asyncapi-types'

export interface GoalIndicatorProps {
  goalStatus: GoalStatusFrame | null
  loopStatus?: LoopStatusFrame | null
}

// Display cap for the goal condition — the wire allows up to 4000 chars
// (Edge Case: "Very long goal condition (4000 chars): indicator truncates
// with title tooltip"). Grapheme-safe (Array.from) so combining marks/CJK/
// astral characters truncate cleanly, mirroring SubagentBlock.tsx's
// `truncateLabel` convention.
const CONDITION_DISPLAY_CAP = 140

function truncateCondition(raw: string): string {
  const clusters = Array.from(raw)
  if (clusters.length <= CONDITION_DISPLAY_CAP) return raw
  return clusters.slice(0, CONDITION_DISPLAY_CAP).join('') + '…'
}

function formatNextDelay(seconds: number): string {
  if (seconds <= 0) return '0s'
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  const rem = seconds % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}

type GoalPillState = GoalStatusFrame['state']

interface StatusLineConfig {
  testId: string
  text: string
  className: string
}

// Non-`active` states each render a single summary line (no condition/round
// detail) — mirrors the prior paused/brake-fired convention. Exhaustive
// switch with a `never` default so a future 9th enum value fails typecheck
// here instead of silently rendering nothing.
function describeNonActiveState(state: Exclude<GoalPillState, 'active'>): StatusLineConfig {
  switch (state) {
    case 'queued':
      return {
        testId: 'goal-indicator-queued',
        text: 'queued — waiting for a free loop slot',
        className: 'text-[var(--color-muted)]',
      }
    case 'waiting_on_user':
      return {
        testId: 'goal-indicator-waiting',
        text: 'waiting on you',
        className: 'text-[color:var(--color-warning)]',
      }
    case 'judge_unavailable':
      return {
        testId: 'goal-indicator-paused',
        text: 'paused — waiting on judge',
        className: 'text-[color:var(--color-warning)]',
      }
    case 're-planning':
      return {
        testId: 'goal-indicator-replanning',
        text: 're-planning — awaiting your correction',
        className: 'text-[color:var(--color-warning)]',
      }
    case 'judging':
      return {
        testId: 'goal-indicator-judging',
        text: 'judging…',
        className: 'text-[var(--color-muted)]',
      }
    case 'done':
      return {
        testId: 'goal-indicator-done',
        text: 'done',
        className: 'text-[color:var(--color-success)]',
      }
    case 'failed':
      return {
        testId: 'goal-indicator-failed',
        text: 'failed',
        className: 'text-[color:var(--color-error)]',
      }
    default: {
      const exhaustiveCheck: never = state
      throw new Error(`GoalIndicator: unhandled goal pill state ${String(exhaustiveCheck)}`)
    }
  }
}

export function GoalIndicator({ goalStatus, loopStatus }: GoalIndicatorProps) {
  // No `cleared` literal survives in the 8-value pill enum (ADR-053) — the
  // store clears the session bucket rather than this component matching a
  // state value, so any non-null frame renders.
  const showGoal = !!goalStatus
  const showLoop = !!loopStatus

  if (!showGoal && !showLoop) return null

  return (
    <div
      data-testid="goal-indicator"
      role="status"
      aria-live="polite"
      className="flex flex-col gap-1.5 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs"
    >
      {showGoal && goalStatus && (
        <div className="flex items-start gap-2">
          <Target size={13} weight="fill" className="mt-0.5 shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
          <div className="flex-1 min-w-0">
            {goalStatus.state === 'active' && (
              <>
                <p
                  className="text-[var(--color-secondary)] truncate"
                  title={goalStatus.condition}
                  data-testid="goal-indicator-condition"
                >
                  {truncateCondition(goalStatus.condition)}
                </p>
                <p className="text-[var(--color-muted)] mt-0.5" data-testid="goal-indicator-round">
                  round {goalStatus.round}/{goalStatus.max_rounds} · active loops {goalStatus.active_loops}/{goalStatus.cap}
                </p>
                {goalStatus.latest_reason && (
                  <p className="text-[var(--color-muted)] mt-0.5 italic truncate" title={goalStatus.latest_reason}>
                    {goalStatus.latest_reason}
                  </p>
                )}
              </>
            )}
            {goalStatus.state !== 'active' && (() => {
              const { testId, text, className } = describeNonActiveState(goalStatus.state)
              return (
                <>
                  <p className={className} data-testid={testId}>
                    {text}
                  </p>
                  {goalStatus.latest_reason && (
                    <p className="text-[var(--color-muted)] mt-0.5 italic truncate" title={goalStatus.latest_reason}>
                      {goalStatus.latest_reason}
                    </p>
                  )}
                </>
              )
            })()}
          </div>
        </div>
      )}
      {showLoop && loopStatus && (
        <div className="flex items-center gap-2" data-testid="loop-status-line">
          <Repeat size={12} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
          <p className="text-[var(--color-muted)] truncate">
            Loop: {loopStatus.mode === 'interval' ? 'every' : 'self-paced'} · run {loopStatus.run}/{loopStatus.max_runs}
            {typeof loopStatus.next_delay === 'number' && <> · next in {formatNextDelay(loopStatus.next_delay)}</>}
          </p>
        </div>
      )}
    </div>
  )
}

// GoalIndicator — ADR-049 D6/US-12/FR-094/SD-C9.
//
// Persistent chat indicator for an active `/goal` session, driven entirely
// by `goal_status` WS frames (the chat store just stores the frame
// verbatim — see chat.ts's `case 'goal_status'`). Renders in the SAME
// non-scrolling composer slot as RateLimitIndicator (SD-C9), directly above
// the composer: persistent (no dismiss control) while a goal is active,
// collapses to nothing when cleared — mirroring ActivityBar's
// `empty:hidden` precedent rather than inventing a new one.
//
// Also renders a compact `/loop` status line (mode/run/next-delay) when a
// `loop_status` frame has arrived for the session — LoopStatusFrame.state is
// an UNCONSTRAINED string on the wire (unlike GoalStatusFrame.state's closed
// 4-value enum), so there is no literal this component can compare against
// to auto-hide a "stopped"/cleared loop the way it does for a goal; it
// simply renders whenever `loopStatus` is non-null. Flagged in this wave's
// report as a contract asymmetry for Wave 2 backend to reconcile.
//
// `aria-live="polite"` on the root announces state transitions (active →
// paused → brake-fired → cleared) to screen readers without stealing focus.

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

export function GoalIndicator({ goalStatus, loopStatus }: GoalIndicatorProps) {
  // BDD "Goal indicator states": `cleared` → indicator removed. The store
  // keeps the frame verbatim (see chat.ts) so a later re-render still has
  // access to the last-known reason etc.; this component is the one place
  // that decides 'cleared' means "render nothing".
  const showGoal = !!goalStatus && goalStatus.state !== 'cleared'
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
            {goalStatus.state === 'paused_judge_unavailable' && (
              <p className="text-[color:var(--color-warning)]" data-testid="goal-indicator-paused">
                paused — waiting on judge
              </p>
            )}
            {goalStatus.state === 'brake_fired' && (
              <p className="text-[var(--color-muted)]" data-testid="goal-indicator-brake">
                winding down (bound reached)
              </p>
            )}
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

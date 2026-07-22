// GoalPillTray — ADR-053 FE-1 / US-14 / design §1.
//
// Relocates the goal indicator from the full-width above-composer box to a
// BOTTOM-RIGHT pill tray (mirroring the subagent pill placement grammar),
// renders ONE pill per goal-id (a session carrying 2 goals shows 2 pills +
// 2 timers), and click-expands a pill to reveal the full condition + round
// accounting + the latest per-criterion verdict.
//
// Driven entirely by the chat store's per-goal-id `goalPills` map (populated
// by `case 'goal_status'` in chat.ts — one entry per GoalStatusFrame.goal_id,
// falling back to '_default'). The latest goal-scoped JudgeVerdict (for the
// expanded per-criterion view) is read from the global judgeActivity store.
//
// All 8 pill states render with distinct colour/icon grammar per the design:
// queued (muted) / active (gold target) / waiting_on_user (amber) /
// judge_unavailable (amber) / re-planning (amber) / judging (muted pulse) /
// done (green) / failed (red). The pill-state→render mapping lives in
// `describePillState` below — an exhaustive switch with a `never` default so
// a future 9th enum value fails typecheck.
//
// `aria-live="polite"` on the tray root announces state transitions to screen
// readers without stealing focus.

import { useState } from 'react'
import { Target, CaretDown, CaretUp, CheckCircle, XCircle, Spinner, Hourglass, ChatCircleDots, FlagBannerFold, Pencil } from '@phosphor-icons/react'
import type { GoalStatusFrame, JudgeVerdictFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import { cn } from '@/lib/utils'

// ── Display cap for the goal condition (grapheme-safe, mirrors GoalIndicator) ─
const CONDITION_DISPLAY_CAP = 80

function truncateCondition(raw: string): string {
  const clusters = Array.from(raw)
  if (clusters.length <= CONDITION_DISPLAY_CAP) return raw
  return clusters.slice(0, CONDITION_DISPLAY_CAP).join('') + '…'
}

// ── Pill-state → render config (exhaustive, 8 values) ─────────────────────────
//
// Each state maps to a testId, icon, accent class, and human label. The
// exhaustive switch with a `never` default ensures a future 9th enum value
// fails typecheck here instead of silently rendering nothing.

interface PillStateConfig {
  testId: string
  label: string
  /** Tailwind text-colour class for the icon + label. */
  accentClass: string
  /** True for the pulse animation (judging). */
  pulse?: boolean
  /** Phosphor icon component. */
  Icon: typeof Target
}

function describePillState(state: GoalStatusFrame['state']): PillStateConfig {
  switch (state) {
    case 'queued':
      return { testId: 'goal-pill-queued', label: 'queued', accentClass: 'text-[var(--color-muted)]', Icon: Hourglass }
    case 'active':
      return { testId: 'goal-pill-active', label: 'active', accentClass: 'text-[var(--color-accent)]', Icon: Target }
    case 'waiting_on_user':
      return { testId: 'goal-pill-waiting', label: 'waiting on you', accentClass: 'text-[color:var(--color-warning)]', Icon: ChatCircleDots }
    case 'judge_unavailable':
      return { testId: 'goal-pill-judge-unavailable', label: 'judge unavailable', accentClass: 'text-[color:var(--color-warning)]', Icon: FlagBannerFold }
    case 're-planning':
      return { testId: 'goal-pill-replanning', label: 're-planning', accentClass: 'text-[color:var(--color-warning)]', Icon: Pencil }
    case 'judging':
      return { testId: 'goal-pill-judging', label: 'judging', accentClass: 'text-[var(--color-muted)]', Icon: Spinner, pulse: true }
    case 'done':
      return { testId: 'goal-pill-done', label: 'done', accentClass: 'text-[color:var(--color-success)]', Icon: CheckCircle }
    case 'failed':
      return { testId: 'goal-pill-failed', label: 'failed', accentClass: 'text-[color:var(--color-error)]', Icon: XCircle }
    default: {
      const exhaustiveCheck: never = state
      throw new Error(`GoalPillTray: unhandled goal pill state ${String(exhaustiveCheck)}`)
    }
  }
}

// ── GoalPill — one pill for one goal-id ───────────────────────────────────────

interface GoalPillProps {
  goalId: string
  frame: GoalStatusFrame
  /** Latest goal-scoped verdict for the expanded per-criterion view, or null. */
  latestVerdict: JudgeVerdictFrame | null
}

function GoalPill({ goalId, frame, latestVerdict }: GoalPillProps) {
  const [expanded, setExpanded] = useState(false)
  const config = describePillState(frame.state)
  const { Icon } = config

  return (
    <div className="flex flex-col items-end" data-testid="goal-pill-wrapper">
      <button
        type="button"
        data-testid={config.testId}
        data-goal-id={goalId}
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        aria-label={`Goal: ${truncateCondition(frame.condition)}, state ${config.label}. Click to ${expanded ? 'collapse' : 'expand'}.`}
        className={cn(
          'flex items-center gap-1.5 rounded-full border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-xs shadow-md transition-colors hover:bg-[var(--color-surface-2)] cursor-pointer',
          config.accentClass,
        )}
      >
        <Icon
          size={13}
          weight="fill"
          className={cn('shrink-0', config.pulse && 'animate-pulse')}
          aria-hidden="true"
        />
        <span className="font-medium max-w-[220px] truncate">
          {truncateCondition(frame.condition)}
        </span>
        <span className="shrink-0 tabular-nums opacity-80">
          {frame.round}/{frame.max_rounds}
        </span>
        {expanded ? <CaretUp size={11} aria-hidden="true" /> : <CaretDown size={11} aria-hidden="true" />}
      </button>

      {expanded && (
        <div
          data-testid="goal-pill-expanded"
          className="mt-1 w-[320px] max-w-[calc(100vw-2rem)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5 text-xs shadow-lg"
        >
          {/* Condition (full, not truncated) */}
          <div className="flex items-start gap-2">
            <Target size={12} weight="fill" className="mt-0.5 shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
            <div className="flex-1 min-w-0">
              <p className="text-[var(--color-secondary)] break-words" data-testid="goal-pill-condition">
                {frame.condition}
              </p>
              <p className="text-[var(--color-muted)] mt-1 tabular-nums" data-testid="goal-pill-round">
                round {frame.round}/{frame.max_rounds} · active loops {frame.active_loops}/{frame.cap}
              </p>
            </div>
          </div>

          {/* Latest judge reason */}
          {frame.latest_reason && (
            <p className="text-[var(--color-muted)] mt-1.5 italic break-words" title={frame.latest_reason}>
              {frame.latest_reason}
            </p>
          )}

          {/* Latest per-criterion verdict (from judgeActivity, goal-scoped) */}
          {latestVerdict && (
            <div className="mt-2 border-t border-[var(--color-border)] pt-2">
              <p className="text-[var(--color-muted)] uppercase tracking-wide text-[10px] font-sans mb-1">
                Latest verdict — {latestVerdict.met ? 'met' : 'not met'} (round {latestVerdict.round})
              </p>
              <ul className="space-y-0.5" data-testid="goal-pill-verdict-criteria">
                {latestVerdict.per_criterion.map((c) => (
                  <li key={c.criterion_id} className="flex items-start gap-1.5 text-[11px]">
                    {c.met ? (
                      <CheckCircle size={11} className="mt-0.5 shrink-0 text-[color:var(--color-success)]" aria-hidden="true" />
                    ) : (
                      <XCircle size={11} className="mt-0.5 shrink-0 text-[color:var(--color-error)]" aria-hidden="true" />
                    )}
                    <span className="text-[var(--color-secondary)] break-words">{c.reason}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── GoalPillTray — bottom-right floating tray ─────────────────────────────────

export function GoalPillTray() {
  const goalPills = useChatStore((s) => s.goalPills ?? {})
  const verdicts = useJudgeActivityStore((s) => s.verdicts)

  const entries = Object.entries(goalPills)
  if (entries.length === 0) return null

  // Find the latest goal-scoped verdict for correlation in the expanded view.
  // JudgeVerdictFrame carries no goal_id (correlated by scope='goal'); the
  // most-recent goal-scoped verdict is the best available correlation. This
  // is display-only enrichment — the pill renders correctly without it.
  const latestGoalVerdict: JudgeVerdictFrame | null =
    [...verdicts].reverse().find((v) => v.scope === 'goal') ?? null

  return (
    <div
      data-testid="goal-pill-tray"
      role="status"
      aria-live="polite"
      className="pointer-events-none absolute bottom-[calc(100%+0.5rem)] right-4 z-20 flex flex-col items-end gap-1.5"
    >
      {entries.map(([goalId, frame]) => (
        <div key={goalId} className="pointer-events-auto">
          <GoalPill goalId={goalId} frame={frame} latestVerdict={latestGoalVerdict} />
        </div>
      ))}
    </div>
  )
}

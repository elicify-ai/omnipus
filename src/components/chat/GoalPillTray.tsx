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
// All 9 pill states render with distinct colour/icon grammar per the design:
// queued (muted) / active (gold target) / waiting_on_user (amber) /
// judge_unavailable (amber) / re-planning (amber) / judging (muted pulse) /
// done (green) / failed (red) / cleared (muted — a deliberate user stop is
// neither success nor failure, UAT S3 fix). The pill-state→render mapping
// lives in `describePillState` below — an exhaustive switch with a `never`
// default so a future 10th enum value fails typecheck.
//
// TERMINAL-PILL LIFETIME (regression fix, bc66345f follow-up): a terminal
// pill (done/failed/cleared) stays visible for `TERMINAL_PILL_DISPLAY_MS` —
// long enough to be the confirmation the user needs that a goal actually
// finished — then this component stops rendering it (`useVisibleGoalPills`
// below). It is NOT deleted from the store the instant it terminates (that
// would deny the user the "goal met"/"cleared" confirmation), and the
// underlying `goalPills` map is separately capped in chat.ts
// (`evictGoalPillsOverCap`) so it can never grow unbounded even though this
// component may still be showing (or have already hidden) any given entry.
// This keeps the render-timing decision entirely in this component, per
// chat.ts's stated design ("components decide whether/how to render each
// state — no special-casing here") — the store still just stores frames
// verbatim, forever, up to its own cap; only THIS file decides how long a
// terminal one stays on screen.
//
// `aria-live="polite"` on the tray root announces state transitions to screen
// readers without stealing focus.

import { useEffect, useRef, useState } from 'react'
import { Target, CaretDown, CaretUp, CheckCircle, XCircle, Spinner, Hourglass, ChatCircleDots, FlagBannerFold, Pencil, MinusCircle } from '@phosphor-icons/react'
import type { GoalStatusFrame, JudgeVerdictFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore, GOAL_TERMINAL_STATES } from '@/store/chat'
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
    case 'cleared':
      // UAT S3 fix: a user-initiated `/goal clear` is a deliberate, successful
      // stop — NOT a failure. Neutral/muted, distinct from both `done`
      // (green success) and `failed` (red error).
      return { testId: 'goal-pill-cleared', label: 'cleared', accentClass: 'text-[var(--color-muted)]', Icon: MinusCircle }
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
        tabIndex={0}
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

// ── Terminal-pill display window ──────────────────────────────────────────
//
// A terminal goal (done/failed/cleared) is the user's confirmation that
// something just happened, so it must render for a beat — but the
// regression this fixes was exactly that a terminal pill never went away
// (chat.ts's `goalPills` map only grows; nothing ever deleted an entry).
// This hook is the render-layer answer: after `TERMINAL_PILL_DISPLAY_MS`,
// it drops a terminal goal_id out of what gets rendered — it does NOT
// touch the store, so `goalPills` itself is unaffected (that map's own
// bound is chat.ts's `evictGoalPillsOverCap`, a separate, size-based
// concern — see that function's comment for why the two are independent).
//
// Mirrors the existing `useCancelState` house pattern (a
// `MIN_STOPPING_DISPLAY_MS`-style minimum/brief display before a state
// transition takes visible effect) rather than inventing a new idiom.
const TERMINAL_PILL_DISPLAY_MS = 4_000

function useVisibleGoalPills(goalPills: Record<string, GoalStatusFrame>): Record<string, GoalStatusFrame> {
  const [hiddenIds, setHiddenIds] = useState<ReadonlySet<string>>(() => new Set())
  const timersRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map())

  useEffect(() => {
    const timers = timersRef.current

    // Arm a hide timer for every terminal goal_id that doesn't have one yet
    // (a brand-new terminal frame, or one that arrived before this effect
    // last ran). Once a goal_id is terminal it can never become non-terminal
    // again (goal_id is unique per generation, ADR-053) — no branch here
    // needs to reverse a hide once armed.
    for (const [goalId, frame] of Object.entries(goalPills)) {
      if (GOAL_TERMINAL_STATES.has(frame.state) && !timers.has(goalId) && !hiddenIds.has(goalId)) {
        const timer = setTimeout(() => {
          timers.delete(goalId)
          setHiddenIds((prev) => {
            const next = new Set(prev)
            next.add(goalId)
            return next
          })
        }, TERMINAL_PILL_DISPLAY_MS)
        timers.set(goalId, timer)
      }
    }

    // Reconcile against the CURRENT goalPills map: a goal_id can disappear
    // from the store (evicted by `evictGoalPillsOverCap`'s own cap) without
    // this component's timer ever having fired — drop its timer/hidden
    // bookkeeping too, so this hook's local state never outlives the data
    // it describes (otherwise hiddenIds would itself grow unbounded across
    // a long session, reintroducing the exact class of leak this fix
    // closes, just one layer up).
    for (const [goalId, timer] of timers) {
      if (!(goalId in goalPills)) {
        clearTimeout(timer)
        timers.delete(goalId)
      }
    }
    setHiddenIds((prev) => {
      let changed = false
      const next = new Set(prev)
      for (const goalId of prev) {
        if (!(goalId in goalPills)) {
          next.delete(goalId)
          changed = true
        }
      }
      return changed ? next : prev
    })
  }, [goalPills, hiddenIds])

  // Unmount: clear every outstanding timer so none fire (and call
  // setState) after this component is gone.
  useEffect(() => {
    const timers = timersRef.current
    return () => {
      for (const timer of timers.values()) clearTimeout(timer)
      timers.clear()
    }
  }, [])

  if (hiddenIds.size === 0) return goalPills
  const visible: Record<string, GoalStatusFrame> = {}
  for (const [goalId, frame] of Object.entries(goalPills)) {
    if (!hiddenIds.has(goalId)) visible[goalId] = frame
  }
  return visible
}

// ── GoalPillTray — bottom-right floating tray ─────────────────────────────────

export function GoalPillTray() {
  const goalPills = useChatStore((s) => s.goalPills ?? {})
  const verdicts = useJudgeActivityStore((s) => s.verdicts)
  const visiblePills = useVisibleGoalPills(goalPills)

  const entries = Object.entries(visiblePills)
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

// Shared subagent-span interrupt-reason formatting (W1-9).
//
// Originally extracted from SubagentBlock.tsx (Fix 2, 2026-07-16); that
// component is deleted (ADR-091 D7/D10 — a child's own frames never arrive
// in the parent's bucket any more, so there is no thread-side span card
// left to render), leaving ActivityPanel.tsx / useRunningActivity.ts as the
// sole consumers of this formatting for the same field
// (SubagentSpanTerminal.reason).

import type { SubagentSpan, SubagentSpanTerminal } from '@/store/chat'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { statusDot } from '@/lib/toolStatusConfig'
import type { SpanStatusConfigOptions, SpanStatusDotConfig } from '@/lib/toolStatusConfig'

export type SubagentInterruptReason = NonNullable<SubagentSpanTerminal['reason']>

/** Human-readable label for the interrupted reason field (W1-9). */
export function formatInterruptReason(reason: SubagentInterruptReason | undefined): string {
  switch (reason) {
    case 'parent_timeout': return 'parent timed out'
    case 'parent_cancelled': return 'parent cancelled'
    case 'parent_done_early': return 'parent completed early'
    case 'unknown': return 'unknown reason'
    default: return reason ?? ''
  }
}

/**
 * ADR-091 D7's status-line fallback ("last update N s ago") — the side
 * panel row's status line before the child's first `subagent_message`
 * arrives (SubagentSpanBase.statusLine/lastUpdateAt doc comments). Mirrors
 * `TaskActivityChip.tsx::formatActivityAge`'s exact wording ("just now",
 * "N s ago", "N min ago", "N h ago", "N d ago") rather than importing that
 * workspaces-module component into chat — same relative-age shape, no
 * cross-module coupling.
 */
export function formatLastUpdateAge(ageMs: number): string {
  const seconds = Math.floor(Math.max(0, ageMs) / 1000)
  if (seconds < 1) return 'just now'
  if (seconds < 60) return `${seconds} s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} h ago`
  return `${Math.floor(hours / 24)} d ago`
}

export type SubagentLifecycleState = NonNullable<SubagentSpan['lifecycleState']>

/**
 * ADR-091 D7/FR-E-004 (cross-family review finding 21): the eight-state
 * `lifecycleState` domain (`subagent_state`, ADR-053) mapped onto the
 * existing dot vocabulary (`toolStatusConfig.tsx::getSpanStatusDot`'s
 * `statusDot`/spinner) — ActivityPanel's row must prefer THIS whenever
 * `lifecycleState` is present, with the span's own `status` (the parent's
 * "still open" flag, a different axis — see `SubagentSpanBase.lifecycleState`'s
 * doc comment) only as a legacy fallback for a span with no lifecycleState
 * yet (an old transcript pre-dating this delivery, or the brief window
 * before a span's first `subagent_state` arrives).
 *
 * `'running'` builds the same spinner `getSpanStatusDot('running', opts)`
 * builds, inline rather than by delegating to it: the design-system status
 * analyzer (scripts/design-system-locks/status.mjs) resolves each case's
 * colour statically, and a cross-function hand-off is a shape it cannot
 * follow — it reports `design-system/status-unsupported`, which is
 * hard-blocking and cannot be baselined. Keep every case in this switch
 * self-contained and its token literal in the source. Every other state
 * gets its own dot color + label, none of them the spinner (only a
 * genuinely running child spins; queued, waiting-on-a-human, and every
 * terminal state get a static dot).
 */
export function getLifecycleStatusDot(
  state: SubagentLifecycleState,
  opts: SpanStatusConfigOptions = {},
): SpanStatusDotConfig {
  switch (state) {
    case 'running': {
      const { size = 13, runningLabel = 'working' } = opts
      return {
        indicator: <ArrowsClockwise size={size} className="animate-spin text-[var(--color-accent)]" aria-hidden="true" />,
        label: runningLabel,
      }
    }
    case 'queued':
      // Distinct from 'running' — a queued launch stamps subagent_start (so
      // the SPAN is already status: 'running') before the child has
      // actually started executing (D7 table). Must NOT reuse the spinner.
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'queued' }
    case 'needs_input':
      // Same warning color as `getSpanStatusDot`'s 'parked' case (the
      // pre-ADR-091 span-status equivalent of "waiting on a human") — own
      // label, since this is the precise lifecycle signal, not a fallback.
      return { indicator: statusDot('bg-[var(--color-warning)]'), label: 'needs input' }
    case 'paused':
      return { indicator: statusDot('bg-[var(--color-warning)]'), label: 'paused' }
    case 'completed':
      return { indicator: statusDot('bg-[var(--color-success)]'), label: 'done' }
    case 'failed':
      return { indicator: statusDot('bg-[var(--color-error)]'), label: 'failed' }
    case 'cancelled':
      return { indicator: statusDot('bg-[var(--color-cancelled)]'), label: 'cancelled' }
    case 'timed_out':
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'timed out' }
    default: {
      // Safe fallback for any unexpected state value arriving from the wire.
      const _exhaustive: never = state
      void _exhaustive
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'unknown' }
    }
  }
}

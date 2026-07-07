// Shared status → icon/label/color mapping for tool-call and subagent-span
// status indicators. Previously each of 4 files (SubagentBlock, ActivityPanel,
// ToolCallBadge, GenericToolCall) reimplemented its own status→config
// switch/lookup independently. Consolidated here as TWO helpers — not one —
// because the two families are genuinely different visual languages, not
// incidental duplication:
//
//   - getSpanStatusConfig — the "pill" family (SubagentBlock, ActivityPanel):
//     lowercase labels, colored pill background, full 6-value status domain
//     (running/success/error/cancelled/interrupted/timeout).
//   - getToolBadgeStatusConfig — the "inline" family (ToolCallBadge,
//     GenericToolCall): Capitalized labels, border only (no pill), duration
//     folded into the success label, narrower 4-value status domain
//     (running/success/error/cancelled — these callers' status types have no
//     interrupted/timeout case).
//
// Real per-caller differences are preserved via options, never dropped:
//   - ActivityPanel deliberately omits the `border` field from its own local
//     type and labels the 'running' case "running" (vs SubagentBlock's
//     "working") — see ActivityPanel.tsx's file header for why. Both are
//     expressed here as options rather than silently unified.
//   - GenericToolCall's `cancelled` case is muted-colored with a generic
//     border (derived from AssistantUI's incomplete/cancelled reason) while
//     ToolCallBadge's dedicated `cancelled` status uses the cancelled color —
//     both preserved via `cancelledVariant`.
//   - GenericToolCall's extra `delegationFailure` case has no equivalent in
//     the other 3 callers, so it is intentionally NOT modeled here — it stays
//     as GenericToolCall's own local branch, composed alongside calls to
//     getToolBadgeStatusConfig for the other 4 statuses.

import type { ReactNode } from 'react'
import { ArrowsClockwise, CheckCircle, XCircle, Prohibit, Clock } from '@phosphor-icons/react'
import { formatDuration } from './formatDuration'

// ── Family A: "pill" style — SubagentBlock, ActivityPanel ────────────────────

export type SpanLikeStatus = 'running' | 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout'

export interface SpanStatusConfig { // not-wire-format: SPA-internal render config for the "pill" status indicator (icon node, label text, border/pill CSS classes) consumed only by SubagentBlock/ActivityPanel — never serialized across the gateway/SPA boundary
  icon: ReactNode
  label: string
  /** Colored border class. SubagentBlock renders this; ActivityPanel's rows don't reference it. */
  border: string
  pill: string
}

export interface SpanStatusConfigOptions { // not-wire-format: SPA-internal call-site options bag (icon pixel size, running-label override) for getSpanStatusConfig()'s local rendering behavior — a function parameter shape, never serialized
  /** Icon pixel size. SubagentBlock uses 13 (default); ActivityPanel uses 12. */
  size?: number
  /** Label for the 'running' case. SubagentBlock: "working" (default); ActivityPanel: "running". */
  runningLabel?: string
}

export function getSpanStatusConfig(
  status: SpanLikeStatus,
  opts: SpanStatusConfigOptions = {},
): SpanStatusConfig {
  const { size = 13, runningLabel = 'working' } = opts
  switch (status) {
    case 'running':
      return {
        icon: <ArrowsClockwise size={size} className="animate-spin text-[var(--color-accent)]" aria-hidden="true" />,
        label: runningLabel,
        border: 'border-[var(--color-border)]',
        pill: 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
      }
    case 'success':
      return {
        icon: <CheckCircle size={size} className="text-[var(--color-success)]" weight="fill" aria-hidden="true" />,
        label: 'done',
        border: 'border-[var(--color-success)]/20',
        pill: 'bg-[var(--color-success)]/10 text-[var(--color-success)]',
      }
    case 'error':
      return {
        icon: <XCircle size={size} className="text-[var(--color-error)]" weight="fill" aria-hidden="true" />,
        label: 'failed',
        border: 'border-[var(--color-error)]/20',
        pill: 'bg-[var(--color-error)]/10 text-[var(--color-error)]',
      }
    case 'cancelled':
      return {
        icon: <Prohibit size={size} className="text-[var(--color-cancelled)]" weight="fill" aria-hidden="true" />,
        label: 'cancelled',
        border: 'border-[var(--color-cancelled)]/20',
        pill: 'bg-[var(--color-cancelled)]/10 text-[var(--color-cancelled)]',
      }
    case 'interrupted':
      return {
        icon: <Prohibit size={size} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'interrupted',
        border: 'border-[var(--color-muted)]/20',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    case 'timeout':
      // Timeout is treated like interrupted but with a Clock icon.
      return {
        icon: <Clock size={size} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'timed out',
        border: 'border-[var(--color-muted)]/20',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    default: {
      // Safe fallback for any unexpected status value arriving from the wire.
      const _exhaustive: never = status
      void _exhaustive
      return {
        icon: <Prohibit size={size} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'unknown',
        border: 'border-[var(--color-muted)]/20',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    }
  }
}

// ── Family B: "inline" style — ToolCallBadge, GenericToolCall ────────────────

export type ToolBadgeStatus = 'running' | 'success' | 'error' | 'cancelled'

export interface ToolBadgeStatusConfig { // not-wire-format: SPA-internal render config for the "inline" tool-badge status indicator (icon node, label text, border CSS class) consumed only by ToolCallBadge/GenericToolCall — never serialized
  icon: ReactNode
  label: string
  border: string
}

export interface ToolBadgeStatusOptions { // not-wire-format: SPA-internal call-site options bag (icon size, duration folding, cancelled-variant styling) for getToolBadgeStatusConfig()'s local rendering behavior — never serialized
  /** Icon pixel size. ToolCallBadge uses 13 (default); GenericToolCall uses 12. */
  size?: number
  /** Duration folded into the 'success' label as `formatDuration(durationMs) || 'Done'`. */
  durationMs?: number
  /**
   * Visual treatment for the 'cancelled' case:
   *  - 'cancelled' (default) — dedicated cancelled color (ToolCallBadge).
   *  - 'muted' — muted color + generic border (GenericToolCall, whose
   *    'cancelled' is derived from AssistantUI's incomplete/cancelled reason,
   *    not a dedicated cancelled status field).
   */
  cancelledVariant?: 'cancelled' | 'muted'
}

export function getToolBadgeStatusConfig(
  status: ToolBadgeStatus,
  opts: ToolBadgeStatusOptions = {},
): ToolBadgeStatusConfig {
  const { size = 13, durationMs, cancelledVariant = 'cancelled' } = opts
  switch (status) {
    case 'running':
      return {
        icon: <ArrowsClockwise size={size} className="animate-spin text-[var(--color-accent)]" />,
        label: 'Running...',
        border: 'border-[var(--color-border)]',
      }
    case 'success':
      return {
        icon: <CheckCircle size={size} className="text-[var(--color-success)]" weight="fill" />,
        label: formatDuration(durationMs) || 'Done',
        border: 'border-[var(--color-success)]/20',
      }
    case 'error':
      return {
        icon: <XCircle size={size} className="text-[var(--color-error)]" weight="fill" />,
        label: 'Failed',
        border: 'border-[var(--color-error)]/20',
      }
    case 'cancelled':
      return cancelledVariant === 'muted'
        ? {
            icon: <Prohibit size={size} className="text-[var(--color-muted)]" weight="fill" />,
            label: 'Cancelled',
            border: 'border-[var(--color-border)]',
          }
        : {
            icon: <Prohibit size={size} className="text-[var(--color-cancelled)]" weight="fill" />,
            label: 'Cancelled',
            border: 'border-[var(--color-cancelled)]/20',
          }
  }
}

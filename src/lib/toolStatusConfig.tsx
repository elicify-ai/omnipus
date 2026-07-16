// Shared status → icon/label/color mapping for tool-call and subagent-span
// status indicators. Previously each of 4 files (SubagentBlock, ActivityPanel,
// ToolCallBadge, GenericToolCall) reimplemented its own status→config
// switch/lookup independently. Consolidated here as THREE helpers — not one —
// because the underlying visual languages are genuinely different, not
// incidental duplication:
//
//   - getSpanStatusConfig — Family A, the "pill" style (SubagentBlock,
//     ActivityPanel): lowercase labels, colored pill background, full
//     6-value status domain (running/success/error/cancelled/interrupted/
//     timeout). UNCHANGED by the flat text-line restyle below — SubagentBlock
//     and ActivityPanel migrate to getSpanStatusDot (below) in a later stage,
//     so this pill API must keep working exactly as-is until then.
//
//   - getToolBadgeStatusConfig — Family B, the "inline" style (ToolCallBadge,
//     GenericToolCall). REWORKED for ticket "Tool components in chat" (P2):
//     tool calls moved from a bordered/backgrounded card to a flat text-line
//     row, so the returned shape drops `border` entirely (no caller tints a
//     card border anymore) in favor of `indicator` — an 8px status dot for
//     terminal states, or the spinning ArrowsClockwise icon (same slot) for
//     'running', since a static dot can't communicate "in progress". Dot =
//     status. Narrower 4-value status domain unchanged (running/success/
//     error/cancelled — these callers' status types have no
//     interrupted/timeout case).
//
//   - getSpanStatusDot — NEW. A dot-shaped sibling to getSpanStatusConfig,
//     ahead of the span/pill family's own migration to the flat-line
//     language Family B just adopted. Same 6-value status domain and options
//     as getSpanStatusConfig (reused directly — the eventual callers are the
//     same SubagentBlock/ActivityPanel), but returns Family B's dot-based
//     shape. SubagentBlock/ActivityPanel adopt this in the next stage; until
//     then getSpanStatusConfig keeps serving them untouched.
//
// Real per-caller differences are preserved via options, never dropped:
//   - ActivityPanel deliberately omits the `border` field from its own local
//     type (Family A only — Family B has no `border` field at all anymore)
//     and labels the 'running' case "running" (vs SubagentBlock's "working")
//     — see ActivityPanel.tsx's file header for why. Both are expressed here
//     as options rather than silently unified.
//   - GenericToolCall's `cancelled` case is muted-colored (derived from
//     AssistantUI's incomplete/cancelled reason, not a dedicated cancelled
//     status field) while ToolCallBadge's dedicated `cancelled` status uses
//     the cancelled color — both preserved via `cancelledVariant`, which now
//     selects the dot's fill color instead of a border color.
//   - GenericToolCall's extra `delegationFailure` case has no equivalent in
//     the other 3 callers, so it is intentionally NOT modeled here — it stays
//     as GenericToolCall's own local branch, composed alongside calls to
//     getToolBadgeStatusConfig for the other 4 statuses (reusing the shared
//     `statusDot` helper below so its dot matches the other four exactly).

import type { ReactNode } from 'react'
import { ArrowsClockwise, CheckCircle, XCircle, Prohibit, Clock } from '@phosphor-icons/react'
import { formatDuration } from './formatDuration'

// ── Family A: "pill" style — SubagentBlock, ActivityPanel ────────────────────
// UNCHANGED by this restyle — see file header. Do not edit until the
// SubagentBlock/ActivityPanel migration stage lands.

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

// ── Shared dot indicator — flat text-line design (ticket "Tool components in
// chat", P2) ─────────────────────────────────────────────────────────────────
// One 8px filled dot renders any terminal status (success/error/cancelled/
// interrupted/timeout/delegation-denied) — dot = status. 'running' never uses
// this: it keeps the spinning ArrowsClockwise icon in the same indicator slot
// instead, since a static dot cannot show "in progress". Shared by
// getToolBadgeStatusConfig, getSpanStatusDot, and GenericToolCall's own local
// delegationFailure branch (which has no equivalent status in either family)
// so all three draw pixel-identical dots instead of three near-duplicates.
export function statusDot(colorClass: string): ReactNode {
  return <span aria-hidden="true" className={`w-2 h-2 rounded-full shrink-0 ${colorClass}`} />
}

// ── Family B: "inline" style — ToolCallBadge, GenericToolCall ────────────────

export type ToolBadgeStatus = 'running' | 'success' | 'error' | 'cancelled'

export interface ToolBadgeStatusConfig { // not-wire-format: SPA-internal render config for the "inline" tool-badge status indicator (indicator node, label text) consumed only by ToolCallBadge/GenericToolCall — never serialized
  /** An 8px status dot (via statusDot) for terminal states, or the spinning
   * icon for 'running'. Replaces the old `icon` + `border` pair — flat
   * text-line rows have no card border to tint, and the dot itself already
   * carries the status color. */
  indicator: ReactNode
  label: string
  /** Optional label text-color override. Omitted means the caller applies
   * its own default muted status-text color (`text-[var(--color-muted)]`). */
  textClass?: string
}

export interface ToolBadgeStatusOptions { // not-wire-format: SPA-internal call-site options bag (icon size, duration folding, cancelled-variant styling) for getToolBadgeStatusConfig()'s local rendering behavior — never serialized
  /** Icon pixel size — affects only the 'running' spinner; the terminal-state
   * dots are a fixed 8px per the flat text-line spec regardless of this
   * option. ToolCallBadge uses 13 (default); GenericToolCall uses 12. */
  size?: number
  /** Duration folded into the 'success' label as `formatDuration(durationMs) || 'Done'`. */
  durationMs?: number
  /**
   * Visual treatment for the 'cancelled' case's dot color:
   *  - 'cancelled' (default) — dedicated cancelled color (ToolCallBadge).
   *  - 'muted' — muted color (GenericToolCall, whose 'cancelled' is derived
   *    from AssistantUI's incomplete/cancelled reason, not a dedicated
   *    cancelled status field).
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
        indicator: <ArrowsClockwise size={size} className="animate-spin text-[var(--color-accent)]" />,
        label: 'Running...',
      }
    case 'success':
      return {
        indicator: statusDot('bg-[var(--color-success)]'),
        label: formatDuration(durationMs) || 'Done',
      }
    case 'error':
      return {
        indicator: statusDot('bg-[var(--color-error)]'),
        label: 'Failed',
      }
    case 'cancelled':
      return cancelledVariant === 'muted'
        ? {
            indicator: statusDot('bg-[var(--color-muted)]'),
            label: 'Cancelled',
          }
        : {
            indicator: statusDot('bg-[var(--color-cancelled)]'),
            label: 'Cancelled',
          }
  }
}

// ── Family A→dot migration target: getSpanStatusDot ─────────────────────────
// Same 6-value status domain + options as getSpanStatusConfig (Family A,
// above) — reused directly, since the eventual callers are the same
// SubagentBlock/ActivityPanel — but returns Family B's dot-based shape.
// SubagentBlock/ActivityPanel migrate to this in a later stage; until then
// they keep calling getSpanStatusConfig, untouched by this restyle.

export interface SpanStatusDotConfig { // not-wire-format: dot-based render config for the span family's future flat-line indicator. Same shape as ToolBadgeStatusConfig but kept as its own type (not aliased) — Family A, Family B, and this migration-target type are documented and evolve independently per this file's header
  indicator: ReactNode
  label: string
  textClass?: string
}

export function getSpanStatusDot(
  status: SpanLikeStatus,
  opts: SpanStatusConfigOptions = {},
): SpanStatusDotConfig {
  const { size = 13, runningLabel = 'working' } = opts
  switch (status) {
    case 'running':
      return {
        indicator: <ArrowsClockwise size={size} className="animate-spin text-[var(--color-accent)]" aria-hidden="true" />,
        label: runningLabel,
      }
    case 'success':
      return { indicator: statusDot('bg-[var(--color-success)]'), label: 'done' }
    case 'error':
      return { indicator: statusDot('bg-[var(--color-error)]'), label: 'failed' }
    case 'cancelled':
      return { indicator: statusDot('bg-[var(--color-cancelled)]'), label: 'cancelled' }
    case 'interrupted':
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'interrupted' }
    case 'timeout':
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'timed out' }
    default: {
      // Safe fallback for any unexpected status value arriving from the wire.
      const _exhaustive: never = status
      void _exhaustive
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'unknown' }
    }
  }
}

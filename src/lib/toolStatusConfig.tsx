// Shared status → icon/label/color mapping for tool-call and subagent-span
// status indicators. Previously each of 4 files (SubagentBlock, ActivityPanel,
// ToolCallBadge, GenericToolCall) reimplemented its own status→config
// switch/lookup independently. Consolidated here as TWO helpers — not one —
// because the underlying visual languages serve genuinely different status
// domains, not incidental duplication:
//
//   - getSpanStatusDot — the span-status style (SubagentBlock, ActivityPanel):
//     lowercase labels, full 6-value status domain (running/success/error/
//     cancelled/interrupted/timeout). Flat text-line design (ticket "Tool
//     components in chat", P2): an 8px status dot for terminal states, or
//     the spinning ArrowsClockwise icon (same slot) for 'running'. This
//     replaced the earlier pill-shaped `getSpanStatusConfig` (colored pill
//     background, per-status border) once SubagentBlock/ActivityPanel
//     finished migrating to it — that pill API had zero remaining
//     consumers and was deleted rather than kept as dead code.
//
//   - getToolBadgeStatusConfig — the "inline" style (ToolCallBadge,
//     GenericToolCall): same flat dot indicator, narrower 4-value status
//     domain (running/success/error/cancelled — these callers' status types
//     have no interrupted/timeout case), duration folded into the 'success'
//     label.
//
// Real per-caller differences are preserved via options, never dropped:
//   - ActivityPanel labels the 'running' case "running" (vs SubagentBlock's
//     "working") — see ActivityPanel.tsx's file header for why. Expressed
//     here as an option (`runningLabel`) rather than silently unified.
//   - GenericToolCall's `cancelled` case is muted-colored (derived from
//     AssistantUI's incomplete/cancelled reason, not a dedicated cancelled
//     status field) while ToolCallBadge's dedicated `cancelled` status uses
//     the cancelled color — both preserved via `cancelledVariant`, which
//     selects the dot's fill color.
//   - GenericToolCall's extra `delegationFailure` case has no equivalent in
//     the other 3 callers, so it is intentionally NOT modeled here — it stays
//     as GenericToolCall's own local branch, composed alongside calls to
//     getToolBadgeStatusConfig for the other 4 statuses (reusing the shared
//     `statusDot` helper below so its dot matches the other four exactly).
//   - getSpanStatusDot's 'interrupted' and 'timeout' cases share the exact
//     same muted dot color (the old pill API distinguished them with
//     different icon glyphs — Prohibit vs Clock — but the flat-dot design
//     has no icon slot for terminal states); the label text ("interrupted"
//     vs "timed out") is the only discriminator between them now.

import type { ReactNode } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { formatDuration } from './formatDuration'

// ── Shared span-status domain ─────────────────────────────────────────────────

export type SpanLikeStatus = 'running' | 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout' | 'parked'

export interface SpanStatusConfigOptions { // not-wire-format: SPA-internal call-site options bag (icon pixel size, running-label override) for getSpanStatusDot()'s local rendering behavior — a function parameter shape, never serialized
  /** Icon pixel size — affects only the 'running' spinner. SubagentBlock uses 13 (default); ActivityPanel uses 12. */
  size?: number
  /** Label for the 'running' case. SubagentBlock: "working" (default); ActivityPanel: "running". */
  runningLabel?: string
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

// ── Inline style — ToolCallBadge, GenericToolCall ─────────────────────────────

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
        indicator: (
          <ArrowsClockwise
            size={size}
            className="animate-spin text-[var(--color-accent)]"
            aria-hidden="true"
          />
        ),
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

/**
 * True when a MessagePartStatus (or any status-like object shaped `{ type:
 * string; reason?: string }`) is the user-cancelled variant of AssistantUI's
 * `incomplete` status. `reason` is declared optional here rather than
 * required — AssistantUI's own `incomplete` variant type doesn't always
 * carry it in its public surface, so a plain `{ type: 'incomplete' }` (no
 * `reason` at all) is still a valid, well-typed argument, not just a
 * generic-failure fallthrough. Mirrors GenericToolCall.tsx's original inline
 * detector (see its `isCancelled` computation). Centralized here so every
 * assistant-ui-driven tool-call renderer that needs to distinguish
 * "cancelled" from a generic failure (BashOutput, BrowserTool,
 * BrowserNavigate, FileWriteConfirm, WebFetchPreview, FileReadPreview,
 * FileTreeView, WebSearchResult, GenericToolCall) uses identical detection
 * logic instead of re-deriving it per file.
 */
export function isCancelledStatus(status: { type: string; reason?: string }): boolean {
  return status.type === 'incomplete' && status.reason === 'cancelled'
}

// ── Span-status style — SubagentBlock, ActivityPanel ──────────────────────────

export interface SpanStatusDotConfig { // not-wire-format: dot-based render config for the span family's flat-line indicator, consumed only by SubagentBlock/ActivityPanel — never serialized. Same shape as ToolBadgeStatusConfig but kept as its own type (not aliased) since the two families evolve independently per this file's header
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
    case 'parked':
      // ADR-057 UAT defect C2 fix: the child parked awaiting the parent's
      // answer (message_parent(kind="question", wait=true)) — not a
      // failure. Reuses --color-warning, the same amber the D14 goal/plan
      // pill uses for its analogous 'waiting_on_user' state (planStateColors.ts/
      // statusColors.ts), so "needs a human" reads consistently app-wide.
      return { indicator: statusDot('bg-[var(--color-warning)]'), label: 'paused' }
    default: {
      // Safe fallback for any unexpected status value arriving from the wire.
      const _exhaustive: never = status
      void _exhaustive
      return { indicator: statusDot('bg-[var(--color-muted)]'), label: 'unknown' }
    }
  }
}

// Shared subagent-span interrupt-reason formatting (W1-9).
//
// Originally extracted from SubagentBlock.tsx (Fix 2, 2026-07-16); that
// component is deleted (ADR-091 D7/D10 — a child's own frames never arrive
// in the parent's bucket any more, so there is no thread-side span card
// left to render), leaving ActivityPanel.tsx / useRunningActivity.ts as the
// sole consumers of this formatting for the same field
// (SubagentSpanTerminal.reason).

import type { SubagentSpanTerminal } from '@/store/chat'

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

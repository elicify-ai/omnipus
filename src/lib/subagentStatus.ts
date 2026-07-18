// Shared subagent-span interrupt-reason formatting (W1-9).
//
// Extracted from SubagentBlock.tsx (Fix 2, 2026-07-16) so ActivityPanel /
// useRunningActivity.ts can render the exact same human-readable reason text
// for the SAME field (SubagentSpanTerminal.reason) — the panel is now the
// durable surface for this detail once the thread hides delegation cards by
// default (toolVisibility.ts's shouldRenderSubagentSpan), so the two
// surfaces must not drift into two different phrasings for one field.

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

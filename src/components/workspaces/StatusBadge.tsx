// StatusBadge — a status-tinted pill built on the catalogued `Badge`.
//
// A literal `switch` per branch — the same shape as `PriorityBadge.tsx` —
// is what the design-system static scanners
// (`scripts/design-system-locks/{typography,spacing,ts-colors}.mjs`) can
// verify: they cannot resolve a class list read out of a record via a
// runtime key. Wrapping the switch in its own component (rather than an
// exported function returning a class string) also means a caller never
// needs its own className expression resolved — it just renders
// `<StatusBadge status=.../>`.
//
// Every branch uses the matching `--status-<name>-foreground` /
// `--status-<name>-background` token pair (`src/styles/tokens.generated.css`)
// — the dedicated status-chip token family, all ultimately reading
// `src/design-system/status.ts`'s `statusContract`.
//
// `skipped` (a `TaskRun`-only outcome — the overlap guard's declined-fire
// result, never a `Task['status']` member) has no dedicated
// `--status-skipped-*` token, so it reuses the `cancelled` family's orange —
// still visually distinct from `blocked`/`in_progress` (yellow, their own
// `--status-*` families).

import type { HTMLAttributes } from 'react'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { Task, TaskRun } from '@/lib/api'

/** A status value drawn either from `Task['status']` (6-state task lifecycle)
 * or `TaskRun['status']` (run-outcome vocabulary, including `skipped`). */
export type StatusBadgeKey = Task['status'] | TaskRun['status']

export interface StatusBadgeProps extends Omit<HTMLAttributes<HTMLDivElement>, 'className'> {
  status: StatusBadgeKey
  /** Merged with (after) the token-driven colour classes via `cn` — sizing, padding, and border stay owned by each call site, matching `PriorityBadge`'s contract. */
  className?: string
}

export function StatusBadge({ status, className, ...rest }: StatusBadgeProps) {
  switch (status) {
    case 'inbox':
      return <Badge className={cn('text-[color:var(--status-inbox-foreground)] bg-[var(--status-inbox-background)]', className)} {...rest} />
    case 'next':
      return <Badge className={cn('text-[color:var(--status-next-foreground)] bg-[var(--status-next-background)]', className)} {...rest} />
    case 'in_progress':
      return <Badge className={cn('text-[color:var(--status-in-progress-foreground)] bg-[var(--status-in-progress-background)]', className)} {...rest} />
    case 'blocked':
      return <Badge className={cn('text-[color:var(--status-blocked-foreground)] bg-[var(--status-blocked-background)]', className)} {...rest} />
    case 'done':
      return <Badge className={cn('text-[color:var(--status-done-foreground)] bg-[var(--status-done-background)]', className)} {...rest} />
    case 'failed':
      return <Badge className={cn('text-[color:var(--status-failed-foreground)] bg-[var(--status-failed-background)]', className)} {...rest} />
    case 'skipped':
      return <Badge className={cn('text-[color:var(--status-cancelled-foreground)] bg-[var(--status-cancelled-background)]', className)} {...rest} />
    default:
      return <Badge className={cn('text-[color:var(--status-inbox-foreground)] bg-[var(--status-inbox-background)]', className)} {...rest} />
  }
}

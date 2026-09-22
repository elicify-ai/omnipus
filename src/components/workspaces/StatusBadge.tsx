// StatusBadge — a status-tinted pill built on the catalogued `Badge`.
//
// The status→colour mapping is a plain object literal, `STATUS_BADGE_CLASSES`
// below, read by a DYNAMIC key (`STATUS_BADGE_CLASSES[status]`) — the same
// "governed record" shape `scripts/design-system-locks/status.mjs` already
// recognises for `taskStatusConfig.ts`'s own `STATUS_BADGE` map (see that
// file's header comment) and that `typography.mjs`/`spacing.mjs`/
// `ts-colors.mjs` independently prove by enumerating every value the record
// can hold. This is NOT the shape the file used to have (a `switch` inlined
// directly in the component's JSX return, one `<Badge>` per branch): a
// `switch` whose case value folds to a canonical status makes every `cn()`
// argument inside that case status-governed paint the STATUS lock must
// statically prove — including the unrelated pass-through `className` prop,
// which it cannot prove and so reported `design-system/status-unsupported`
// (a finding that can never be baselined). A plain record, read once by a
// runtime key outside any switch, keeps the colour classes fully checked at
// their own declaration (each property is verified against its key's
// canonical `--status-<name>-*` token pair) while the component's own
// `cn(STATUS_BADGE_CLASSES[status], className)` merge is recognised as an
// ordinary registrable extension boundary instead.
//
// Every entry uses the matching `--status-<name>-foreground` /
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

/** Token-driven text/background colour classes, one entry per `StatusBadgeKey`
 * — see this file's header comment for why this is a plain record rather
 * than a `switch` inlined in the component. */
const STATUS_BADGE_CLASSES: Record<StatusBadgeKey, string> = {
  inbox: 'text-[color:var(--status-inbox-foreground)] bg-[var(--status-inbox-background)]',
  next: 'text-[color:var(--status-next-foreground)] bg-[var(--status-next-background)]',
  in_progress: 'text-[color:var(--status-in-progress-foreground)] bg-[var(--status-in-progress-background)]',
  blocked: 'text-[color:var(--status-blocked-foreground)] bg-[var(--status-blocked-background)]',
  done: 'text-[color:var(--status-done-foreground)] bg-[var(--status-done-background)]',
  failed: 'text-[color:var(--status-failed-foreground)] bg-[var(--status-failed-background)]',
  skipped: 'text-[color:var(--status-cancelled-foreground)] bg-[var(--status-cancelled-background)]',
}

export function StatusBadge({ status, className, ...rest }: StatusBadgeProps) {
  return <Badge className={cn(STATUS_BADGE_CLASSES[status] ?? STATUS_BADGE_CLASSES.inbox, className)} {...rest} />
}

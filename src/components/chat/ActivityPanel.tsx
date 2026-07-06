// ActivityPanel — slide-out detail view for the Activity Bar (Sheet-based,
// mirrors SessionPanel.tsx's Sheet/SheetContent/SheetHeader/SheetTitle usage).
//
// Two sections — "Running now" / "Recently finished" — each hidden entirely
// when empty. Status badges follow SubagentBlock.tsx's status→style mapping
// (getStatusConfig there isn't exported, so the same theme tokens are
// reproduced here rather than inventing new ones — not exported/shared
// because two things intentionally differ: this row has no per-status
// border, and the "running" label reads "running" here vs "working" there
// since a row can be a bash call as well as an agent span).

import { useState } from 'react'
import type { ReactNode } from 'react'
import {
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  Prohibit,
  Clock,
  CaretDown,
  CaretUp,
} from '@phosphor-icons/react'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { ActivityAvatar } from './ActivityAvatar'
import { ToolCallBadge } from './ToolCallBadge'
import type { ActivityItem, ActivityStatus } from '@/hooks/useRunningActivity'
import { cn } from '@/lib/utils'

export interface ActivityPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  running: ActivityItem[]
  recentlyFinished: ActivityItem[]
}

interface StatusPillConfig {
  icon: ReactNode
  label: string
  pill: string
}

/** Same theme tokens as SubagentBlock.tsx's getStatusConfig (not exported there) — see file header for the two deliberate differences. */
function getActivityStatusConfig(status: ActivityStatus): StatusPillConfig {
  switch (status) {
    case 'running':
      return {
        icon: <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" aria-hidden="true" />,
        label: 'running',
        pill: 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
      }
    case 'success':
      return {
        icon: <CheckCircle size={12} className="text-[var(--color-success)]" weight="fill" aria-hidden="true" />,
        label: 'done',
        pill: 'bg-[var(--color-success)]/10 text-[var(--color-success)]',
      }
    case 'error':
      return {
        icon: <XCircle size={12} className="text-[var(--color-error)]" weight="fill" aria-hidden="true" />,
        label: 'failed',
        pill: 'bg-[var(--color-error)]/10 text-[var(--color-error)]',
      }
    case 'cancelled':
      return {
        icon: <Prohibit size={12} className="text-[var(--color-cancelled)]" weight="fill" aria-hidden="true" />,
        label: 'cancelled',
        pill: 'bg-[var(--color-cancelled)]/10 text-[var(--color-cancelled)]',
      }
    case 'interrupted':
      return {
        icon: <Prohibit size={12} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'interrupted',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    case 'timeout':
      return {
        icon: <Clock size={12} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'timed out',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    default: {
      const _exhaustive: never = status
      void _exhaustive
      return {
        icon: <Prohibit size={12} className="text-[var(--color-muted)]" weight="fill" aria-hidden="true" />,
        label: 'unknown',
        pill: 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]',
      }
    }
  }
}

function formatDuration(ms?: number): string | null {
  if (ms == null) return null
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function ActivityRow({ item }: { item: ActivityItem }) {
  const [expanded, setExpanded] = useState(false)
  const config = getActivityStatusConfig(item.status)
  const duration = formatDuration(item.durationMs)
  const label = item.kind === 'bash' ? item.command : item.taskLabel
  // Narrowed inline (not via a stored boolean) so `steps` stays typed without a cast.
  const steps = item.kind === 'agent' && item.agentType !== '3p' ? item.steps : null
  const show3pNotice = item.kind === 'agent' && item.agentType === '3p'
  const canExpand = steps != null && steps.length > 0

  return (
    <div
      data-testid="activity-row"
      data-status={item.status}
      className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] text-xs overflow-hidden"
    >
      <button
        type="button"
        onClick={() => canExpand && setExpanded((e) => !e)}
        aria-expanded={canExpand ? expanded : undefined}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 text-left transition-colors',
          canExpand ? 'hover:bg-[var(--color-surface-2)] cursor-pointer' : 'cursor-default',
        )}
      >
        <ActivityAvatar item={item} size="sm" />
        <span className="flex-1 min-w-0 truncate text-[var(--color-secondary)] font-medium font-mono">
          {label}
        </span>
        <span className={cn('flex items-center gap-1 shrink-0 rounded px-1.5 py-0.5', config.pill)}>
          {config.icon}
          <span>{config.label}</span>
        </span>
        {duration && <span className="text-[var(--color-muted)] shrink-0 tabular-nums">{duration}</span>}
        {canExpand && (
          <span className="text-[var(--color-muted)] shrink-0">
            {expanded ? <CaretUp size={11} aria-hidden="true" /> : <CaretDown size={11} aria-hidden="true" />}
          </span>
        )}
      </button>

      {canExpand && expanded && steps && (
        <div className="border-t border-[var(--color-border)] px-3 py-2 space-y-1">
          {steps.map((step, idx) =>
            step.kind === 'tool' ? (
              <ToolCallBadge key={step.tool.call_id} toolCall={step.tool} />
            ) : (
              <p key={idx} className="text-[10px] text-[var(--color-secondary)] font-sans py-0.5">
                {step.text}
              </p>
            ),
          )}
        </div>
      )}

      {show3pNotice && (
        <div className="border-t border-[var(--color-border)] px-3 py-1.5">
          <p className="text-[10px] text-[var(--color-muted)] italic">No live step detail yet</p>
        </div>
      )}
    </div>
  )
}

export function ActivityPanel({ open, onOpenChange, running, recentlyFinished }: ActivityPanelProps) {
  const isEmpty = running.length === 0 && recentlyFinished.length === 0

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-[90vw] sm:w-[22.5rem] p-0 flex flex-col" overlay={false}>
        <SheetHeader className="px-4 pt-5 pb-3 border-b border-[var(--color-border)]">
          <div className="flex items-center gap-2">
            <SheetTitle>Activity</SheetTitle>
            <Badge variant={running.length > 0 ? 'default' : 'muted'}>{running.length} running</Badge>
          </div>
        </SheetHeader>

        <div className="flex-1 overflow-y-auto px-3 py-3 space-y-4">
          {isEmpty && (
            <p className="text-xs text-[var(--color-muted)] text-center py-6">No background activity yet.</p>
          )}

          {running.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] px-1">
                Running now
              </h3>
              <div className="space-y-2">
                {running.map((item) => (
                  <ActivityRow key={item.key} item={item} />
                ))}
              </div>
            </div>
          )}

          {recentlyFinished.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)] px-1">
                Recently finished
              </h3>
              <div className="space-y-2">
                {recentlyFinished.map((item) => (
                  <ActivityRow key={item.key} item={item} />
                ))}
              </div>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

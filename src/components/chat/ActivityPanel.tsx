// ActivityPanel — slide-out detail view for the Activity Bar (Sheet-based,
// mirroring the Sheet/SheetContent/SheetHeader/SheetTitle usage pattern
// SessionPanel.tsx once used — since-deleted; session-list UI now lives in
// SearchModal + the sidebar accordion).
//
// Two sections — "Running now" / "Recently finished" — each hidden entirely
// when empty. Status badges follow SubagentBlock.tsx's status→style mapping
// via the shared getSpanStatusConfig helper (src/lib/toolStatusConfig.tsx) —
// two things intentionally differ here vs SubagentBlock, expressed as options
// rather than a separate reimplementation: this row doesn't use the `border`
// field (icon size 12 + no per-status border), and the "running" label reads
// "running" here vs "working" there, since a row can be a bash call as well
// as an agent span.

import { useState } from 'react'
import { CaretDown, CaretUp } from '@phosphor-icons/react'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Badge } from '@/components/ui/badge'
import { ActivityAvatar } from './ActivityAvatar'
import { ToolCallBadge } from './ToolCallBadge'
import type { ActivityItem } from '@/hooks/useRunningActivity'
import { cn } from '@/lib/utils'
import { formatDuration } from '@/lib/formatDuration'
import { getSpanStatusConfig } from '@/lib/toolStatusConfig'

export interface ActivityPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  running: ActivityItem[]
  recentlyFinished: ActivityItem[]
}

function ActivityRow({ item }: { item: ActivityItem }) {
  const [expanded, setExpanded] = useState(false)
  const config = getSpanStatusConfig(item.status, { size: 12, runningLabel: 'running' })
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
      {/* Mirrors GenericToolCall.tsx's `disabled={!hasDetail}` gate: a
          non-expandable row (bash calls, 3p-agent rows, or a native row with
          zero steps) has nothing to expand — disable it natively so it
          drops out of the tab order and Enter/Space can't no-op on it,
          rather than leaving a focusable dead button whose aria-expanded is
          already (correctly) omitted below. */}
      <button tabIndex={0}
        type="button"
        onClick={() => canExpand && setExpanded((e) => !e)}
        disabled={!canExpand}
        aria-expanded={canExpand ? expanded : undefined}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 text-left transition-colors',
          canExpand ? 'hover:bg-[var(--color-surface-3)] cursor-pointer' : 'cursor-default',
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
        <SheetHeader>
          <SheetTitle>Activity</SheetTitle>
          <Badge variant={running.length > 0 ? 'default' : 'muted'}>{running.length} running</Badge>
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

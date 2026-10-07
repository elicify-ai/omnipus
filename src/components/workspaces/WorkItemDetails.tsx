import { useState, type ReactNode } from 'react'
import { Info } from '@phosphor-icons/react'
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover'
import { IconButton } from '@/components/ui/icon-button'
import { Button } from '@/components/ui/button'
import { WordBoundaryText } from '@/components/ui/word-boundary-text'
import { taskDisplayLabel } from '@/lib/statusColors'
import { planDisplayLabel } from '@/lib/planStateColors'
import type { Agent, Plan, Task } from '@/lib/api'

// Domain composition, not a new primitive: both entities use the catalogued
// Popover/IconButton. Keep the info action isolated from task open/drag and
// plan selection, including clicks on the SVG and portalled content.
function WorkItemDetails({ title, kind, children, onOpen }: { title: string; kind: 'Task' | 'Plan'; children: ReactNode; onOpen?: () => void }) {
  const [open, setOpen] = useState(false)
  return <span className="inline-flex shrink-0" onClick={(event) => event.stopPropagation()} onPointerDown={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <IconButton size="sm" variant="ghost" aria-label={`${kind} details: ${title}`} className="shrink-0 text-[var(--color-muted)]">
          <Info size={14} aria-hidden="true" />
        </IconButton>
      </PopoverTrigger>
      <PopoverContent align="start" role="dialog" aria-label={`${kind} details`} className="z-[100] w-max max-w-[min(28rem,calc(100vw-var(--space-4)))] max-h-[var(--radix-popover-content-available-height)] overflow-y-auto text-[length:var(--type-body-compact-size)]">
        <WordBoundaryText as="h3" text={title} className="whitespace-normal break-normal wrap-break-word font-headline font-bold" />
        <dl className="mt-[var(--space-2)] grid grid-cols-[auto_minmax(0,1fr)] gap-x-[var(--space-3)] gap-y-[var(--space-1)] [&>dt]:text-[var(--color-muted)] [&>dd]:min-w-0 [&>dd]:break-normal [&>dd]:wrap-break-word">{children}</dl>
        {onOpen && <Button variant="ghost" className="mt-[var(--space-2)] h-auto p-0 text-[var(--color-accent)]" onClick={() => { setOpen(false); onOpen() }}>Open task</Button>}
      </PopoverContent>
    </Popover>
  </span>
}

function UpdatedTime({ value }: { value: string }) {
  const date = new Date(value)
  return <time dateTime={value}>{Number.isNaN(date.getTime()) ? 'Unavailable' : date.toLocaleString()}</time>
}

export function TaskDetailsPopover({ task, plans = [], agents = [], onOpenTask }: { task: Task; plans?: Plan[]; agents?: (Pick<Agent, 'id'> & Partial<Pick<Agent, 'name'>>)[]; onOpenTask: () => void }) {
  const agent = task.agent_name ?? agents.find((value) => value.id === task.agent_id)?.name ?? task.agent_id ?? 'Unassigned'
  const plan = plans.find((value) => value.id === task.plan_id)?.title ?? task.plan_id ?? 'Unplanned'
  return <WorkItemDetails title={task.title} kind="Task" onOpen={onOpenTask}>
    <dt>Status</dt><dd>{taskDisplayLabel(task)}</dd>
    <dt>Agent</dt><dd>{agent}</dd>
    <dt>Tags</dt><dd>{task.tags?.length ? task.tags.map((tag, index) => <span key={tag}>{index > 0 && ' · '}<span>{tag}</span></span>) : 'None'}</dd>
    <dt>Plan</dt><dd>{plan}</dd>
    <dt>Updated</dt><dd><UpdatedTime value={task.updated_at} /></dd>
  </WorkItemDetails>
}

export function PlanDetailsPopover({ plan, owner, done, total }: { plan: Plan; owner?: Pick<Agent, 'id' | 'name'>; done: number; total: number }) {
  return <WorkItemDetails title={plan.title} kind="Plan">
    <dt>Status</dt><dd>{planDisplayLabel(plan)}</dd>
    <dt>Progress</dt><dd>{done}/{total}</dd>
    <dt>Owner agent</dt><dd>{owner?.name ?? plan.owner_agent_id ?? 'Unassigned'}</dd>
    <dt>Updated</dt><dd><UpdatedTime value={plan.updated_at} /></dd>
  </WorkItemDetails>
}

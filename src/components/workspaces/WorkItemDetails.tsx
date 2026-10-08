import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Info } from '@phosphor-icons/react'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
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
  const closeTimer = useRef<number | undefined>(undefined)
  const keyboardFocus = useRef(false)
  const touch = useRef(false)
  const infoRef = useRef<HTMLButtonElement>(null)
  const detailsId = useId()
  const cancelClose = () => { window.clearTimeout(closeTimer.current); closeTimer.current = undefined }
  const dismiss = () => { cancelClose(); keyboardFocus.current = false; setOpen(false) }
  const reveal = () => { cancelClose(); setOpen(true) }
  const leave = () => { cancelClose(); closeTimer.current = window.setTimeout(dismiss, 200) }
  useEffect(() => {
    const keyboard = (event: KeyboardEvent) => { keyboardFocus.current = event.key === 'Tab' || event.key.startsWith('Arrow'); touch.current = false }
    const pointer = () => { keyboardFocus.current = false }
    document.addEventListener('keydown', keyboard, true)
    document.addEventListener('pointerdown', pointer, true)
    return () => { document.removeEventListener('keydown', keyboard, true); document.removeEventListener('pointerdown', pointer, true); window.clearTimeout(closeTimer.current) }
  }, [])
  return <span className="inline-flex shrink-0" onClick={(event) => event.stopPropagation()} onPointerDown={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
    <Popover open={open} onOpenChange={(next) => { if (!next) dismiss() }}>
      <PopoverAnchor asChild>
        <IconButton ref={infoRef} size="sm" variant="ghost" aria-label={`${kind} details: ${title}`} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? detailsId : undefined} className="shrink-0 text-[var(--color-muted)]"
          onPointerEnter={(event) => { if (event.pointerType !== 'touch') reveal() }}
          onPointerLeave={(event) => { if (event.pointerType !== 'touch') leave() }}
          onPointerDown={(event) => { touch.current = event.pointerType === 'touch' }}
          onClick={(event) => { event.preventDefault(); if (touch.current) reveal() }}
          onFocus={() => { if (keyboardFocus.current) reveal(); keyboardFocus.current = false }}
          onBlur={leave}
          onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); reveal() } }}>
          <Info size={14} aria-hidden="true" />
        </IconButton>
      </PopoverAnchor>
      <PopoverContent id={detailsId} align="start" role="dialog" aria-label={`${kind} details`} onPointerEnter={cancelClose} onPointerLeave={leave} onFocus={cancelClose}
        onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) leave() }}
        onOpenAutoFocus={(event) => event.preventDefault()} onCloseAutoFocus={(event) => { event.preventDefault() }}
        onEscapeKeyDown={() => { dismiss(); infoRef.current?.focus() }}
        onInteractOutside={(event) => {
          const target = event.detail.originalEvent.target
          if (target instanceof Node && infoRef.current?.contains(target)) event.preventDefault()
          else dismiss()
        }}
        className="z-[100] w-max max-w-[min(28rem,calc(100vw-var(--space-4)))] max-h-[var(--radix-popover-content-available-height)] overflow-y-auto text-[length:var(--type-body-compact-size)]">
        <WordBoundaryText as="h3" text={title} className="whitespace-normal break-normal wrap-break-word font-headline font-bold" />
        <dl className="mt-[var(--space-2)] grid grid-cols-[auto_minmax(0,1fr)] gap-x-[var(--space-3)] gap-y-[var(--space-1)] [&>dt]:text-[var(--color-muted)] [&>dd]:min-w-0 [&>dd]:break-normal [&>dd]:wrap-break-word">{children}</dl>
        {onOpen && <Button variant="ghost" className="mt-[var(--space-2)] h-auto p-0 text-[var(--color-accent)]" onClick={() => { dismiss(); infoRef.current?.focus(); onOpen() }}>Open task</Button>}
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
    <dt>Tags</dt><dd className="flex flex-wrap gap-[var(--space-1)]">{task.tags?.length ? task.tags.map((tag, index) => <span key={tag} className="inline-flex min-w-0 max-w-full items-center gap-[var(--space-1)]"><WordBoundaryText text={tag} className="break-normal wrap-break-word" />{index < task.tags!.length - 1 && <span aria-hidden="true">·</span>}</span>) : 'None'}</dd>
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

import { cloneElement, useEffect, useId, useRef, useState, type HTMLAttributes, type ReactElement } from 'react'
import { HoverCard, HoverCardTrigger, HoverCardContent } from '@/components/ui/hover-card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { taskDisplayLabel } from '@/lib/statusColors'
import type { Agent, Plan, Task } from '@/lib/api'

interface TaskHoverDetailsProps {
  task: Task
  plans?: Plan[]
  agents?: (Pick<Agent, 'id'> & Partial<Pick<Agent, 'name'>>)[]
  children: ReactElement<HTMLAttributes<HTMLElement>>
  onOpenTask: () => void
  enabled?: boolean
}

/** Domain composition of the portalled kit Hover Card. Mouse hover and focus
 * preview; a touch tap previews only, with an explicit Open task action inside.
 * No mutation, no tooltip internals and no replacement of drag descriptions.
 */
export function TaskHoverDetails({ task, plans = [], agents = [], children, onOpenTask, enabled = true }: TaskHoverDetailsProps) {
  const [open, setOpen] = useState(false)
  const previewId = useId()
  const touch = useRef(false)
  const props = children.props
  useEffect(() => {
    if (!open) return
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.stopPropagation()
      setOpen(false)
    }
    document.addEventListener('keydown', escape, true)
    return () => document.removeEventListener('keydown', escape, true)
  }, [open])
  if (!enabled) return children

  const trigger = cloneElement(children, {
    'aria-describedby': [props['aria-describedby'], open ? previewId : undefined].filter(Boolean).join(' ') || undefined,
    onPointerDown: (event) => {
      touch.current = event.pointerType === 'touch'
      props.onPointerDown?.(event)
    },
    onClickCapture: (event) => {
      props.onClickCapture?.(event)
      if (event.defaultPrevented) return
      if (!touch.current) { setOpen(false); return }
      const button = (event.target as HTMLElement).closest('button')
      // Run/Stop and other independent nested actions retain their own behavior.
      if (button && !button.hasAttribute('data-task-open')) return
      event.preventDefault()
      event.stopPropagation()
      setOpen(true)
    },
    onFocus: (event) => { props.onFocus?.(event); if (!event.defaultPrevented) setOpen(true) },
    onKeyDown: (event) => { touch.current = false; if (event.key === 'Enter' || event.key === ' ') setOpen(false); props.onKeyDown?.(event) },
  })
  const agent = task.agent_name ?? agents.find((value) => value.id === task.agent_id)?.name ?? task.agent_id ?? 'Unassigned'
  const plan = plans.find((value) => value.id === task.plan_id)?.title ?? task.plan_id ?? 'Unplanned'
  const updated = new Date(task.updated_at)

  return (
    <HoverCard open={open} onOpenChange={setOpen} openDelay={100} closeDelay={200}>
      <HoverCardTrigger asChild>{trigger}</HoverCardTrigger>
      <HoverCardContent id={previewId} role="dialog" aria-label="Task preview" data-testid={`task-preview-${task.id}`}>
        <h3 className="max-w-full whitespace-normal break-normal wrap-break-word font-headline font-bold text-[var(--color-secondary)]">{task.title}</h3>
        <dl className="mt-[var(--space-2)] grid grid-cols-[auto_minmax(0,1fr)] gap-x-[var(--space-3)] gap-y-[var(--space-1)]">
          <dt className="text-[var(--color-muted)]">Status</dt><dd className="break-normal wrap-break-word">{taskDisplayLabel(task)}</dd>
          <dt className="text-[var(--color-muted)]">Agent</dt><dd className="break-normal wrap-break-word">{agent}</dd>
          <dt className="text-[var(--color-muted)]">Tags</dt><dd className="flex min-w-0 flex-wrap gap-[var(--space-1)]">{task.tags?.length ? task.tags.map((tag) => <Badge key={tag} variant="outline" className="min-w-0 max-w-full"><span className="min-w-0 break-normal wrap-break-word">{tag}</span></Badge>) : 'None'}</dd>
          <dt className="text-[var(--color-muted)]">Plan</dt><dd className="break-normal wrap-break-word">{plan}</dd>
          <dt className="text-[var(--color-muted)]">Updated</dt><dd><time dateTime={task.updated_at}>{Number.isNaN(updated.getTime()) ? 'Unavailable' : updated.toLocaleString()}</time></dd>
        </dl>
        <Button variant="ghost" onClick={(event) => { event.stopPropagation(); setOpen(false); onOpenTask() }} className="mt-[var(--space-2)] h-auto p-0 text-[var(--color-accent)]">Open task</Button>
      </HoverCardContent>
    </HoverCard>
  )
}

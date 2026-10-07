import { cloneElement, useEffect, useId, useRef, useState, type HTMLAttributes, type ReactElement } from 'react'
import { HoverCard, HoverCardTrigger, HoverCardContent } from '@/components/ui/hover-card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { WordBoundaryText } from '@/components/ui/word-boundary-text'
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
  const dismissed = useRef(false)
  const keyboardFocus = useRef(false)
  const props = children.props
  useEffect(() => {
    if (!enabled) return
    // A fresh keyboard navigation event may preview on focus. Focus restored
    // by a task/confirmation dialog is not a new keyboard visit.
    const keyboard = (event: KeyboardEvent) => {
      keyboardFocus.current = event.key === 'Tab' || event.key.startsWith('Arrow')
    }
    const pointer = () => { keyboardFocus.current = false }
    document.addEventListener('keydown', keyboard, true)
    document.addEventListener('pointerdown', pointer, true)
    return () => {
      document.removeEventListener('keydown', keyboard, true)
      document.removeEventListener('pointerdown', pointer, true)
    }
  }, [enabled])
  useEffect(() => {
    if (!open) return
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.stopPropagation()
      dismissed.current = true
      setOpen(false)
    }
    document.addEventListener('keydown', escape, true)
    return () => document.removeEventListener('keydown', escape, true)
  }, [open])
  if (!enabled) return children

  const independentAction = (target: EventTarget, owner: HTMLElement) => {
    const action = target instanceof Element ? target.closest('button,a,input,select,textarea,[role="button"]') : null
    return Boolean(action && action !== owner && !action.hasAttribute('data-task-open'))
  }
  const dismiss = () => { dismissed.current = true; keyboardFocus.current = false; setOpen(false) }
  const trigger = cloneElement(children, {
    'aria-describedby': [props['aria-describedby'], open ? previewId : undefined].filter(Boolean).join(' ') || undefined,
    onPointerDownCapture: (event) => {
      touch.current = event.pointerType === 'touch'
      if (independentAction(event.target, event.currentTarget)) dismiss()
      props.onPointerDownCapture?.(event)
    },
    onClickCapture: (event) => {
      props.onClickCapture?.(event)
      if (event.defaultPrevented) return
      // Capture runs even when the nested action isolates its bubble handlers.
      if (independentAction(event.target, event.currentTarget) || !touch.current) { dismiss(); return }
      event.preventDefault()
      event.stopPropagation()
      dismissed.current = false
      setOpen(true)
    },
    onPointerLeave: (event) => { props.onPointerLeave?.(event); dismissed.current = false },
    onFocus: (event) => {
      props.onFocus?.(event)
      if (!event.defaultPrevented && keyboardFocus.current && !dismissed.current && !independentAction(event.target, event.currentTarget)) setOpen(true)
      keyboardFocus.current = false
    },
    onBlur: (event) => {
      props.onBlur?.(event)
      if (!event.currentTarget.contains(event.relatedTarget)) dismissed.current = false
    },
    onKeyDown: (event) => {
      touch.current = false
      if (event.key === 'Enter' || event.key === ' ') dismiss()
      props.onKeyDown?.(event)
    },
  })
  const agent = task.agent_name ?? agents.find((value) => value.id === task.agent_id)?.name ?? task.agent_id ?? 'Unassigned'
  const plan = plans.find((value) => value.id === task.plan_id)?.title ?? task.plan_id ?? 'Unplanned'
  const updated = new Date(task.updated_at)

  return (
    <HoverCard open={open} onOpenChange={(next) => { if (!next || !dismissed.current) setOpen(next) }} openDelay={100} closeDelay={200}>
      {/* Focus is owned above; suppress Radix's unconditional focus-open timer. */}
      <HoverCardTrigger asChild onFocus={(event) => event.preventDefault()}>{trigger}</HoverCardTrigger>
      <HoverCardContent id={previewId} role="dialog" aria-label="Task preview" data-testid={`task-preview-${task.id}`}>
        <WordBoundaryText as="h3" text={task.title} className="max-w-full whitespace-normal break-normal wrap-break-word font-headline font-bold text-[var(--color-secondary)]" />
        <dl className="mt-[var(--space-2)] grid grid-cols-[auto_minmax(0,1fr)] gap-x-[var(--space-3)] gap-y-[var(--space-1)]">
          <dt className="text-[var(--color-muted)]">Status</dt><dd className="break-normal wrap-break-word">{taskDisplayLabel(task)}</dd>
          <dt className="text-[var(--color-muted)]">Agent</dt><dd className="break-normal wrap-break-word">{agent}</dd>
          <dt className="text-[var(--color-muted)]">Tags</dt><dd className="flex min-w-0 flex-wrap gap-[var(--space-1)]">{task.tags?.length ? task.tags.map((tag) => <Badge key={tag} variant="outline" className="min-w-0 max-w-full"><span className="min-w-0 break-normal wrap-break-word">{tag}</span></Badge>) : 'None'}</dd>
          <dt className="text-[var(--color-muted)]">Plan</dt><dd className="break-normal wrap-break-word">{plan}</dd>
          <dt className="text-[var(--color-muted)]">Updated</dt><dd><time dateTime={task.updated_at}>{Number.isNaN(updated.getTime()) ? 'Unavailable' : updated.toLocaleString()}</time></dd>
        </dl>
        <Button variant="ghost" onClick={(event) => { event.stopPropagation(); dismiss(); onOpenTask() }} className="mt-[var(--space-2)] h-auto p-0 text-[var(--color-accent)]">Open task</Button>
      </HoverCardContent>
    </HoverCard>
  )
}

/**
 * One muted, single-line delegation event. Not a published control: it
 * composes the catalogued Button for [open] and the existing grey-line
 * grammar (caption size, muted colour). The leading mark is a Phosphor
 * icon, not an emoji glyph.
 */
import { Children, Fragment, createContext, useContext, type ReactNode } from 'react'
import { ArrowRight, Check, Prohibit, Warning } from '@phosphor-icons/react'
import { useNavigate } from '@tanstack/react-router'
import { useMessage } from '@assistant-ui/react'
import { Button } from '@/components/ui/button'
import { delegationEventLineText, delegationEventShowsOpen } from '@/lib/delegationEventLine'
import type { DelegationEvent, DelegationEventKind } from '@/lib/delegationEvents.types'

function LineMark({ kind }: { kind: DelegationEventKind }) {
  const common = { size: 12, 'aria-hidden': true as const, className: 'shrink-0' }
  switch (kind) {
    case 'finished':
    case 'bash_finished':
      return <Check weight="bold" {...common} />
    case 'stopped':
    case 'refused':
    case 'bash_failed':
      return <Warning {...common} />
    case 'cancelled':
    case 'bash_stopped':
      return <Prohibit {...common} />
    default:
      return <ArrowRight {...common} />
  }
}

export function DelegationEventLine({ event }: { event: DelegationEvent }) {
  const navigate = useNavigate()
  const text = delegationEventLineText(event)
  const childSessionId = event.childSessionId
  const showOpen = delegationEventShowsOpen(event) && typeof childSessionId === 'string'
  return (
    <div
      data-testid="delegation-event-line"
      data-event-kind={event.kind}
      data-event-id={event.id}
      className="flex min-w-0 items-center gap-[var(--space-1)] px-[var(--space-3)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
    >
      <LineMark kind={event.kind} />
      <span className="min-w-0 truncate">{text}</span>
      {showOpen && (
        <Button
          type="button"
          variant="link"
          size="sm"
          data-testid="delegation-event-open"
          className="h-auto shrink-0 px-0 py-0 text-[length:var(--type-caption-size)]"
          onClick={() => {
            void navigate({ to: '/sessions/$sessionId', params: { sessionId: childSessionId } })
          }}
        >
          [open]
        </Button>
      )}
    </div>
  )
}

export function DelegationEventLineList({ events }: { events: readonly DelegationEvent[] }) {
  if (events.length === 0) return null
  return (
    <>
      {events.map((event) => (
        <DelegationEventLine key={event.id} event={event} />
      ))}
    </>
  )
}

/** Keeps a replayed tool row as-is when it has no lines. Otherwise the lines follow that row. */
export function delegationSlotted(
  callId: string,
  events: readonly DelegationEvent[] | undefined,
  node: ReactNode,
): ReactNode {
  if (!events || events.length === 0) return node
  return (
    <Fragment key={callId}>
      {node}
      <DelegationEventLineList events={events} />
    </Fragment>
  )
}

const EMPTY_INLINE = new Map<string, readonly DelegationEvent[]>()
const InlineEvents = createContext<ReadonlyMap<string, readonly DelegationEvent[]>>(EMPTY_INLINE)

export function DelegationInlineProvider({
  byCall,
  children,
}: {
  byCall: ReadonlyMap<string, readonly DelegationEvent[]>
  children: ReactNode
}) {
  return <InlineEvents.Provider value={byCall}>{children}</InlineEvents.Provider>
}

/**
 * Live bubble: assistant-ui groups tool parts, and this slot draws each
 * call's lines directly after that part — the same place the replay path
 * uses. An empty map returns the group unchanged.
 */
export function DelegationToolGroup({
  children,
  startIndex,
}: {
  children?: ReactNode
  startIndex: number
  endIndex: number
}) {
  const byCall = useContext(InlineEvents)
  const message = useMessage()
  if (byCall.size === 0) return <>{children}</>
  const parts = message.content
  return (
    <>
      {Children.toArray(children).map((node, index) => {
        const part = parts?.[startIndex + index] as { type?: string; toolCallId?: string } | undefined
        const callId = part?.type === 'tool-call' && typeof part.toolCallId === 'string' ? part.toolCallId : undefined
        const events = callId ? byCall.get(callId) : undefined
        return (
          <Fragment key={callId ?? index}>
            {node}
            {events && events.length > 0 ? <DelegationEventLineList events={events} /> : null}
          </Fragment>
        )
      })}
    </>
  )
}

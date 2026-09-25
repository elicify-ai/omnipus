/**
 * One muted, single-line delegation event. Not a published control: it
 * composes the catalogued Button for [open] and the existing grey-line
 * grammar (caption size, muted colour). The leading mark is a Phosphor
 * icon, not an emoji glyph.
 */
import { Children, Fragment, createContext, useCallback, useContext, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowRight, Check, Prohibit, Warning } from '@phosphor-icons/react'
import { useNavigate } from '@tanstack/react-router'
import { useMessage } from '@assistant-ui/react'
import { Button } from '@/components/ui/button'
import { delegationEventLineText, delegationEventShowsOpen } from '@/lib/delegationEventLine'
import type { DelegationEvent, DelegationEventKind } from '@/lib/delegationEvents.types'
import { appendUnclaimedCalls } from '@/lib/delegationEventPlacement'

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
const MatchReporter = createContext<((groupKey: string, ids: ReadonlySet<string>) => void) | null>(null)

export function DelegationInlineProvider({
  byCall,
  reportMatched,
  children,
}: {
  byCall: ReadonlyMap<string, readonly DelegationEvent[]>
  /** Live path only: which call ids this bubble's tool groups actually drew. */
  reportMatched?: (groupKey: string, ids: ReadonlySet<string>) => void
  children: ReactNode
}) {
  return (
    <InlineEvents.Provider value={byCall}>
      <MatchReporter.Provider value={reportMatched ?? null}>{children}</MatchReporter.Provider>
    </InlineEvents.Provider>
  )
}

type ToolPart = { type?: string; toolCallId?: string }

function callIdAt(parts: readonly ToolPart[] | undefined, index: number): string | undefined {
  const part = parts?.[index]
  return part?.type === 'tool-call' && typeof part.toolCallId === 'string' ? part.toolCallId : undefined
}

/**
 * Call ids in this group that have lines. The parent renders any other
 * positioned line after the bubble, so a snapshot with no part is not lost.
 */
function matchedCallIds(
  children: ReactNode,
  parts: readonly ToolPart[] | undefined,
  startIndex: number,
  byCall: ReadonlyMap<string, readonly DelegationEvent[]>,
): ReadonlySet<string> {
  const ids = new Set<string>()
  if (byCall.size === 0) return ids
  Children.toArray(children).forEach((_, index) => {
    const callId = callIdAt(parts, startIndex + index)
    if (callId && byCall.has(callId)) ids.add(callId)
  })
  return ids
}

/** Collects the call ids each live tool group drew, so the rest can trail the bubble. */
export function useClaimedCallIds(): {
  claimed: ReadonlySet<string>
  reportMatched: (groupKey: string, ids: ReadonlySet<string>) => void
} {
  const groups = useRef(new Map<string, ReadonlySet<string>>())
  const claimedRef = useRef<ReadonlySet<string>>(new Set())
  const [claimed, setClaimed] = useState<ReadonlySet<string>>(claimedRef.current)
  const reportMatched = useCallback((groupKey: string, ids: ReadonlySet<string>) => {
    if (ids.size === 0) groups.current.delete(groupKey)
    else groups.current.set(groupKey, ids)
    const next = new Set<string>()
    for (const group of groups.current.values()) {
      for (const id of group) next.add(id)
    }
    // Compare to the last set we published, not the state updater's prev.
    // A group's cleanup and its next report land in one flush; chaining off
    // prev would treat the cleanup's empty set as current and re-render forever.
    if (sameIds(claimedRef.current, next)) return
    claimedRef.current = next
    setClaimed(next)
  }, [])
  return { claimed, reportMatched }
}

function sameIds(prev: ReadonlySet<string>, next: ReadonlySet<string>): boolean {
  if (prev.size !== next.size) return false
  for (const id of next) if (!prev.has(id)) return false
  return true
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
  const reportMatched = useContext(MatchReporter)
  const message = useMessage()
  const parts = message.content as readonly ToolPart[] | undefined
  const matchedKey = useMemo(() => {
    const ids = matchedCallIds(children, parts, startIndex, byCall)
    return [...ids].sort().join('\0')
  }, [byCall, children, parts, startIndex])
  const groupKey = String(startIndex)
  useLayoutEffect(() => {
    if (!reportMatched) return undefined
    reportMatched(groupKey, new Set(matchedKey ? matchedKey.split('\0') : []))
    return () => reportMatched(groupKey, new Set())
  }, [groupKey, matchedKey, reportMatched])
  if (byCall.size === 0) return <>{children}</>
  return (
    <>
      {Children.toArray(children).map((node, index) => {
        const callId = callIdAt(parts, startIndex + index)
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

/** Lines for this live message: the unplaced ones, plus any placed call the tool group never drew. */
export function DelegationLiveTail({
  byCall,
  trailing,
  claimed,
}: {
  byCall: ReadonlyMap<string, readonly DelegationEvent[]>
  trailing: readonly DelegationEvent[]
  claimed: ReadonlySet<string>
}) {
  return <DelegationEventLineList events={appendUnclaimedCalls(trailing, byCall, claimed)} />
}

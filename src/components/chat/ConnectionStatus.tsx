import React, { useEffect, useId, useState } from 'react'
import {
  ArrowClockwise,
  Check,
  CheckCircle,
  Checks,
  Clock,
  CloudSlash,
  PauseCircle,
  WifiSlash,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useConnectionStore } from '@/store/connection'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { cn } from '@/lib/utils'

export type UserDeliveryState = 'queued' | 'received' | 'working' | 'failed'
export type AssistantConnectionState = 'paused' | 'unfinished'
export type ChatConnectionState = 'offline' | 'unreachable' | 'back'

const QUIET_DROP_MS = 15_000
const CHAT_NOTICE_MS = 120_000
const RECOVERY_NOTICE_MS = 2_000

// Review finding 15 (WCAG 1.4.13 — Content on Hover or Focus): the tooltip
// must be DISMISSABLE (Escape closes it), HOVERABLE (the pointer can move
// from the trigger onto the tooltip itself without it disappearing), and
// PERSISTENT (stays open until the trigger loses focus/hover, or Escape).
// Fixed by:
//   - moving the open/close hover handlers to the OUTER wrapper (so hovering
//     either the trigger or the tooltip body keeps it open — before this,
//     `pointer-events-none` also meant the tooltip couldn't receive hover at
//     all, so ANY movement toward it crossed dead space and closed it);
//   - an Escape keydown handler that force-closes it;
//   - dropping `pointer-events-none` now that the tooltip is a legitimate
//     hover target.
function tooltipWrapperHandlers(setOpen: (open: boolean) => void) {
  return {
    onMouseEnter: () => setOpen(true),
    onMouseLeave: () => setOpen(false),
    onKeyDown: (event: React.KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    },
  }
}

function StatusTooltip({
  label,
  tooltip,
  children,
  latest = true,
}: {
  label: string
  tooltip: string
  children: React.ReactNode
  latest?: boolean
}) {
  const [open, setOpen] = useState(false)
  const tooltipId = useId()
  return (
    <span className="relative inline-flex" {...tooltipWrapperHandlers(setOpen)}>
      <IconButton
        size="sm"
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onClick={() => setOpen((value) => !value)}
        className={cn(
          'text-[var(--color-secondary)] transition-opacity motion-reduce:transition-none',
          latest ? 'opacity-100' : 'opacity-40 group-hover:opacity-100 group-focus-within:opacity-100',
        )}
      >
        {children}
      </IconButton>
      {open && (
        <span
          id={tooltipId}
          role="tooltip"
          className="absolute bottom-full right-0 z-50 mb-[var(--space-1)] w-64 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-lg"
        >
          {tooltip}
        </span>
      )}
    </span>
  )
}

function StatusActionButton({
  label,
  tooltip,
  onClick,
  testId,
  children,
}: {
  label: string
  tooltip: string
  onClick?: () => void
  /**
   * Optional stable hook for e2e specs. The failed-send retry keeps the
   * pre-#823 id `user-message-retry` so `tests/e2e/open-in-chat.spec.ts`'s
   * §253(c) resend path keeps testing the same affordance across the
   * banner-to-message-level redesign.
   */
  testId?: string
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const tooltipId = useId()
  return (
    <span className="relative inline-flex" {...tooltipWrapperHandlers(setOpen)}>
      <Button
        variant="ghost"
        size="sm"
        onClick={onClick}
        data-testid={testId}
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onPointerDown={() => setOpen(true)}
        className="h-8 gap-[var(--space-1)] px-[var(--space-2)] font-[var(--font-weight-regular)] text-[var(--color-secondary)]"
      >
        {children}
      </Button>
      {open && (
        <span
          id={tooltipId}
          role="tooltip"
          className="absolute bottom-full right-0 z-50 mb-[var(--space-1)] w-64 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-lg"
        >
          {tooltip}
        </span>
      )}
    </span>
  )
}

export function UserMessageDeliveryStatus({
  state,
  agentName,
  latest = false,
  onRetry,
}: {
  state: UserDeliveryState
  agentName: string
  latest?: boolean
  onRetry?: () => void
}) {
  if (state === 'failed') {
    return (
      // Review finding 15 (contrast): fading was designed for the passive
      // ticks below (queued/received/working) — an actionable "failed,
      // needs a retry" control must NEVER fade to 40% opacity regardless of
      // `latest`, or it falls below the spec's own 4.5:1 contrast
      // requirement on an older message.
      <div
        role="status"
        aria-live="polite"
        data-testid="user-message-delivery-status"
        className="flex min-h-8 items-center justify-end text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
      >
        <StatusActionButton
          onClick={onRetry}
          testId="user-message-retry"
          label="Couldn't be sent. Try again"
          tooltip="This message couldn't be delivered. Your text is kept — click to try again."
        >
          <ArrowClockwise size={14} aria-hidden="true" />
          <span>Couldn't be sent</span>
          <span aria-hidden="true">{' · '}</span>
          <span>Try again</span>
        </StatusActionButton>
      </div>
    )
  }

  const copy = state === 'queued'
    ? {
        label: 'Not sent yet, will be sent automatically',
        tooltip: "Not sent yet — it will be sent automatically as soon as you're back online.",
        icon: <Clock size={14} aria-hidden="true" />,
      }
    : state === 'received'
      ? {
          label: 'Received',
          tooltip: `Received by Omnipus. ${agentName} will pick it up after finishing the current step.`,
          icon: <Check size={14} aria-hidden="true" />,
        }
      : {
          label: `${agentName} is working on it`,
          tooltip: `${agentName} is working on this.`,
          icon: <Checks size={14} aria-hidden="true" />,
        }

  return (
    <div role="status" aria-live="polite" data-testid="user-message-delivery-status" className="flex min-h-8 items-center justify-end">
      <StatusTooltip label={copy.label} tooltip={copy.tooltip} latest={latest}>
        {copy.icon}
      </StatusTooltip>
    </div>
  )
}

export function AssistantConnectionStatus({
  state,
  agentName,
  onGenerateAgain,
}: {
  state: AssistantConnectionState
  agentName: string
  onGenerateAgain?: () => void
}) {
  if (state === 'paused') {
    return (
      <div
        role="status"
        aria-live="polite"
        data-testid="assistant-connection-status"
        className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
      >
        <StatusTooltip
          label={`${agentName} is still working. The rest appears when the connection is back.`}
          tooltip={`Your connection dropped. ${agentName} keeps working in the background; nothing is lost.`}
        >
          <PauseCircle size={15} aria-hidden="true" />
        </StatusTooltip>
        <span>{agentName} is still working on this — the rest appears when you're connected again.</span>
      </div>
    )
  }

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="assistant-connection-status"
      className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
    >
      <StatusActionButton
        onClick={onGenerateAgain}
        label="Answer couldn't be finished. Generate again"
        tooltip="The connection was gone for too long to continue this answer. Generate it again to get a complete reply."
      >
        <ArrowClockwise size={14} aria-hidden="true" />
        <span>This answer couldn't be finished</span>
        <span aria-hidden="true">{' · '}</span>
        <span>Generate again</span>
      </StatusActionButton>
    </div>
  )
}

interface ConnectionDisplayInput {
  isConnected: boolean
  reconnectPhase: 'reconnecting' | 'slow' | 'gave_up' | null
  disconnectedAt: number | null
  reconnectedAt: number | null
  lastDisconnectDurationMs: number | null
  lastDisconnectWasTerminal: boolean
  hasInterruptedAnswer: boolean
  deviceOnline: boolean
  now: number
  /**
   * Review finding 14 / ADR-082: true when the session's own `session_state`
   * frame has confirmed (most recently) that a turn is still running for
   * this session — i.e. `ChatStore.activeTurnId` is non-null. This is the
   * ONLY thing that can honestly answer "did the turn finish" — a turn
   * never ends just because the browser gave up reconnecting (ADR-082 P1),
   * so the client cannot claim "couldn't be finished" from local reconnect
   * state alone. It can only make that claim once RECONNECTED and the
   * server's own signal says the turn is no longer in flight.
   */
  sessionHasActiveTurn: boolean
  /**
   * #823 catch-up redesign (BE-DESIGN.md §6.5) — true while this session's
   * reconnect is still mid catch-up (between `session_snapshot`/the
   * incremental journal tail and the matching `catch_up_complete`, or
   * before any attach has resolved at all — SessionChatState.awaitingCatchUp,
   * set true by the `session_snapshot` case and cleared by
   * `catch_up_complete`, see src/store/chat/slices/catchup-frames.ts).
   * `session_state` (sessionHasActiveTurn's own source) arrives BEFORE
   * catch_up_complete in the real attach sequence (§4.1 A6), so treating it
   * as authoritative before the catch-up it belongs to has actually
   * finished being applied risks flashing "couldn't be finished" mid
   * catch-up. This is the ONLY input change this lane makes to this
   * function (BE-DESIGN.md §9's Lane C DoD: "phase-1 components untouched
   * apart from the unfinished input").
   */
  awaitingCatchUp: boolean
}

export function deriveConnectionDisplay(input: ConnectionDisplayInput): {
  answer: 'hidden' | AssistantConnectionState
  chat: 'hidden' | ChatConnectionState
} {
  if (input.isConnected) {
    const showedProblem = (input.lastDisconnectDurationMs ?? 0) >= QUIET_DROP_MS || input.lastDisconnectWasTerminal
    const showRecovery = showedProblem && input.reconnectedAt !== null && input.now - input.reconnectedAt < RECOVERY_NOTICE_MS
    // Review finding 14: reconnected AND the server confirms no turn is
    // in flight for this session is the only honest "couldn't be
    // finished" signal — see sessionHasActiveTurn's doc comment.
    // #823 catch-up redesign (§6.5): also gated on the catch-up for THIS
    // attach having actually finished — see awaitingCatchUp's doc comment.
    const answer: 'hidden' | AssistantConnectionState =
      input.hasInterruptedAnswer && !input.sessionHasActiveTurn && !input.awaitingCatchUp
        ? 'unfinished'
        : 'hidden'
    return { answer, chat: showRecovery ? 'back' : 'hidden' }
  }

  const elapsed = input.disconnectedAt === null ? 0 : Math.max(0, input.now - input.disconnectedAt)
  const terminal = input.reconnectPhase === 'gave_up'
  // ADR-082 P1: a turn never ends just because the UI gave up reconnecting
  // — the server keeps working regardless of connection state. While still
  // disconnected there is no server signal available at all, so the only
  // honest state is the same quiet "still working" continuation as any
  // other drop — NEVER 'unfinished', which this client cannot yet know.
  const answer = input.hasInterruptedAnswer && (terminal || elapsed >= QUIET_DROP_MS) ? 'paused' : 'hidden'
  const chat = terminal || elapsed >= CHAT_NOTICE_MS
    ? (input.deviceOnline ? 'unreachable' : 'offline')
    : 'hidden'
  return { answer, chat }
}

function useConnectionNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return undefined
    const timer = window.setInterval(() => setNow(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [active])
  return now
}

export function ChatConnectionStatusLine({ state, onRetry }: { state: ChatConnectionState; onRetry?: () => void }) {
  if (state === 'back') {
    return (
      <div role="status" aria-live="polite" data-testid="connection-status-line" className="flex min-h-8 items-center justify-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
        <CheckCircle size={15} aria-hidden="true" />
        <span>Up to date</span>
      </div>
    )
  }

  const offline = state === 'offline'
  const copy = offline
    ? 'No internet connection. Your agents keep working.'
    : "Can't reach Omnipus right now. Your agents keep working."
  const tooltip = offline
    ? "Your device isn't connected to the internet. Omnipus reconnects on its own when it's back — your messages and your agents' work are kept."
    : "Omnipus isn't responding at the moment. It will keep trying on its own — your messages and your agents' work are kept."
  const label = offline ? 'No internet connection. Your agents keep working.' : "Can't reach Omnipus right now. Your agents keep working."

  return (
    <div role="status" aria-live="polite" data-testid="connection-status-line" className="flex min-h-8 items-center justify-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
      <StatusTooltip label={label} tooltip={tooltip}>
        {offline ? <WifiSlash size={15} aria-hidden="true" /> : <CloudSlash size={15} aria-hidden="true" />}
      </StatusTooltip>
      <span>{copy}</span>
      <span aria-hidden="true">{' · '}</span>
      <Button variant="ghost" size="sm" onClick={onRetry} className="h-8 px-[var(--space-2)] text-[var(--color-secondary)]">
        Try again
      </Button>
    </div>
  )
}

export function ChatConnectionNotice() {
  const connection = useConnectionStore((state) => state)
  const active = !connection.isConnected || connection.reconnectedAt !== null
  const now = useConnectionNow(active)
  // This line only ever renders `display.chat` (reachability), never
  // `display.answer` — sessionHasActiveTurn only affects the latter, so the
  // active session's own value is a safe, sufficient input here.
  // activeTurnId lives on the per-session bucket (SessionChatState), not on
  // the flat foreground ChatStore, so it is read via sessionsById keyed by
  // the session store's own activeSessionId.
  const activeSessionId = useSessionStore((state) => state.activeSessionId)
  const sessionHasActiveTurn = useChatStore((state) =>
    activeSessionId != null && state.sessionsById[activeSessionId]?.activeTurnId != null,
  )
  // #823 catch-up redesign (§6.5) — only affects `display.answer`, never
  // `display.chat` (the only field this line renders), same as
  // sessionHasActiveTurn above; read here purely so this call site stays a
  // valid, honest ConnectionDisplayInput.
  const awaitingCatchUp = useChatStore((state) =>
    activeSessionId != null && !!state.sessionsById[activeSessionId]?.awaitingCatchUp,
  )
  const display = deriveConnectionDisplay({
    ...connection,
    hasInterruptedAnswer: connection.disconnectedAssistantMessageId !== null,
    deviceOnline: typeof navigator === 'undefined' ? true : navigator.onLine,
    sessionHasActiveTurn,
    awaitingCatchUp,
    now,
  })
  if (display.chat === 'hidden') return null
  return <ChatConnectionStatusLine state={display.chat} onRetry={connection.reconnect} />
}

// Review finding 13: this renders once PER assistant message row in the
// thread, so subscribing to `(state) => state` (the whole connection store)
// re-rendered EVERY row on ANY connection-store change, and starting
// useConnectionNow's 1-second timer before checking whether this row was
// even the disconnected one meant every row ran a polling timer for the
// life of the tab. Fixed by:
//   - selecting only the individual primitive fields deriveConnectionDisplay
//     needs, instead of the whole state object, so an unrelated field (e.g.
//     liteMode) flipping never re-renders a row that doesn't care about it;
//   - gating `active` on `disconnectedHere` FIRST, so useConnectionNow's
//     setInterval never starts at all for a row that isn't the disconnected
//     one, regardless of the raw isConnected/reconnectedAt values.
// (reconnectedAt itself no longer sticks forever either — see
// connection.ts::setConnected's auto-clear.)
export function AssistantMessageConnectionStatus({ messageId, agentName }: { messageId: string; agentName: string }) {
  const disconnectedHere = useConnectionStore((state) => state.disconnectedAssistantMessageId === messageId)
  const isConnected = useConnectionStore((state) => state.isConnected)
  const reconnectPhase = useConnectionStore((state) => state.reconnectPhase)
  const disconnectedAt = useConnectionStore((state) => state.disconnectedAt)
  const reconnectedAt = useConnectionStore((state) => state.reconnectedAt)
  const lastDisconnectDurationMs = useConnectionStore((state) => state.lastDisconnectDurationMs)
  const lastDisconnectWasTerminal = useConnectionStore((state) => state.lastDisconnectWasTerminal)
  // #823 catch-up redesign, Opus review round 2 item 5 (BE-DESIGN.md §6.5):
  // `disconnectedHere` alone cannot detect "unfinished" once reconnected —
  // connection.ts::setConnected(true) clears disconnectedAssistantMessageId
  // the INSTANT the socket reconnects, before catch-up (and therefore
  // awaitingCatchUp) could possibly have resolved. Gating this component's
  // very existence on that flag made the awaitingCatchUp check further down
  // unreachable dead code. §6.5's rule is fully derivable from the bucket
  // instead, with no dependency on the connection store's transient flag.
  //
  // Opus review round 3 item N1 (real-browser regression, browser-confirmed
  // at t006 of a plain, never-disconnected live turn): this used to compare
  // `msg.turnId !== bucket.activeTurnId` reactively, on every render.
  // `activeTurnId` is ONLY ever populated by a `session_state.active_turn`
  // frame, and for an ordinary live turn that frame never arrives mid-turn
  // (only the turn-less `session_state{}` at connection bind does) — so
  // `activeTurnId` stays `null` for the entire duration of a normal live
  // turn, making that comparison true from the FIRST token onward and
  // showing "couldn't be finished" under every streaming answer. Fixed by
  // reading `ChatMessage.confirmedUnfinished` — a flag the
  // `catch_up_complete` reducer sets ONCE, only when session_state's
  // active_turn is actually authoritative (see that field's own doc
  // comment) — instead of re-deriving the same judgment reactively here,
  // where a live turn and a genuinely-ended one are indistinguishable.
  const activeSessionId = useSessionStore((state) => state.activeSessionId)
  const unfinishedHere = useChatStore((state) => {
    if (activeSessionId == null) return false
    const bucket = state.sessionsById[activeSessionId]
    const msg = bucket?.messagesById?.[messageId]
    return !!msg?.confirmedUnfinished
  })
  const active = (disconnectedHere || unfinishedHere) && (!isConnected || reconnectedAt !== null || unfinishedHere)
  const now = useConnectionNow(active)
  // Review finding 14 / ADR-082: the server-confirmed signal that gates
  // 'unfinished' — see ConnectionDisplayInput.sessionHasActiveTurn's doc
  // comment. activeTurnId lives on the per-session bucket, not the flat
  // foreground ChatStore — see ChatConnectionNotice's identical read above.
  const sessionHasActiveTurn = useChatStore((state) =>
    activeSessionId != null && state.sessionsById[activeSessionId]?.activeTurnId != null,
  )
  // #823 catch-up redesign (BE-DESIGN.md §6.5) — see
  // ConnectionDisplayInput.awaitingCatchUp's doc comment.
  const awaitingCatchUp = useChatStore((state) =>
    activeSessionId != null && !!state.sessionsById[activeSessionId]?.awaitingCatchUp,
  )
  if (!disconnectedHere && !unfinishedHere) return null
  const display = deriveConnectionDisplay({
    isConnected,
    reconnectPhase,
    disconnectedAt,
    reconnectedAt,
    lastDisconnectDurationMs,
    lastDisconnectWasTerminal,
    hasInterruptedAnswer: true,
    deviceOnline: typeof navigator === 'undefined' ? true : navigator.onLine,
    sessionHasActiveTurn,
    awaitingCatchUp,
    now,
  })
  if (display.answer === 'hidden') return null
  const generateAgain = () => {
    const chat = useChatStore.getState()
    const lastUser = [...chat.messages].reverse().find((message) => message.role === 'user')
    // Review finding 17/14: resend BY ID (in place, with attachments) —
    // not a fresh sendMessage(content) call, which used to drop media and
    // duplicate the bubble.
    if (lastUser) chat.resendMessage(lastUser.id)
  }
  return <AssistantConnectionStatus state={display.answer} agentName={agentName} onGenerateAgain={generateAgain} />
}

// #823 review round 9 (real-browser evidence, CI run 36081327151, scenario
// h): a turn killed before any token streamed leaves no assistant message
// at all for AssistantMessageConnectionStatus above to attach to —
// catch_up_complete's sweep (catchup-frames.ts) can only mark an EXISTING
// bubble unfinished. This sibling component renders the identical
// "couldn't be finished · Generate again" state for the exchange itself,
// keyed off the user message rather than a (nonexistent) reply, driven by
// SessionChatState.unansweredLastUserMessageId — that field's own doc
// comment covers the exact scope (boot_mismatch specifically, not
// retention_exceeded, and only when the exchange truly has no reply and no
// turn is running).
export function UnansweredUserMessageStatus({ messageId, agentName }: { messageId: string; agentName: string }) {
  const activeSessionId = useSessionStore((state) => state.activeSessionId)
  const unanswered = useChatStore((state) => {
    if (activeSessionId == null) return false
    return state.sessionsById[activeSessionId]?.unansweredLastUserMessageId === messageId
  })
  if (!unanswered) return null
  const generateAgain = () => {
    useChatStore.getState().resendMessage(messageId)
  }
  return <AssistantConnectionStatus state="unfinished" agentName={agentName} onGenerateAgain={generateAgain} />
}

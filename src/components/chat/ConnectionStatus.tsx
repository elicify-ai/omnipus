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
import { cn } from '@/lib/utils'

export type UserDeliveryState = 'queued' | 'received' | 'working' | 'failed'
export type AssistantConnectionState = 'paused' | 'unfinished'
export type ChatConnectionState = 'offline' | 'unreachable' | 'back'

const QUIET_DROP_MS = 15_000
const CHAT_NOTICE_MS = 120_000
const RECOVERY_NOTICE_MS = 2_000

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
    <span className="relative inline-flex">
      <IconButton
        size="sm"
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
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
          className="pointer-events-none absolute bottom-full right-0 z-50 mb-[var(--space-1)] w-64 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-lg"
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
    <span className="relative inline-flex">
      <Button
        variant="ghost"
        size="sm"
        onClick={onClick}
        data-testid={testId}
        aria-label={label}
        aria-describedby={open ? tooltipId : undefined}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
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
          className="pointer-events-none absolute bottom-full right-0 z-50 mb-[var(--space-1)] w-64 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-3)] px-[var(--space-2)] py-[var(--space-1)] text-left text-[length:var(--type-caption-size)] text-[var(--color-secondary)] shadow-lg"
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
      <div
        role="status"
        aria-live="polite"
        data-testid="user-message-delivery-status"
        className={cn(
          'flex min-h-8 items-center justify-end text-[length:var(--type-caption-size)] text-[var(--color-secondary)] transition-opacity motion-reduce:transition-none',
          latest ? 'opacity-100' : 'opacity-40 group-hover:opacity-100 group-focus-within:opacity-100',
        )}
      >
        <StatusActionButton
          onClick={onRetry}
          testId="user-message-retry"
          label="Couldn't be sent. Try again, button"
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
        label="Answer couldn't be finished. Generate again, button"
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
}

export function deriveConnectionDisplay(input: ConnectionDisplayInput): {
  answer: 'hidden' | AssistantConnectionState
  chat: 'hidden' | ChatConnectionState
} {
  if (input.isConnected) {
    const showedProblem = (input.lastDisconnectDurationMs ?? 0) >= QUIET_DROP_MS || input.lastDisconnectWasTerminal
    const showRecovery = showedProblem && input.reconnectedAt !== null && input.now - input.reconnectedAt < RECOVERY_NOTICE_MS
    return { answer: 'hidden', chat: showRecovery ? 'back' : 'hidden' }
  }

  const elapsed = input.disconnectedAt === null ? 0 : Math.max(0, input.now - input.disconnectedAt)
  const terminal = input.reconnectPhase === 'gave_up'
  const answer = input.hasInterruptedAnswer && (terminal || elapsed >= QUIET_DROP_MS)
    ? (terminal ? 'unfinished' : 'paused')
    : 'hidden'
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
  const display = deriveConnectionDisplay({
    ...connection,
    hasInterruptedAnswer: connection.disconnectedAssistantMessageId !== null,
    deviceOnline: typeof navigator === 'undefined' ? true : navigator.onLine,
    now,
  })
  if (display.chat === 'hidden') return null
  return <ChatConnectionStatusLine state={display.chat} onRetry={connection.reconnect} />
}

export function AssistantMessageConnectionStatus({ messageId, agentName }: { messageId: string; agentName: string }) {
  const connection = useConnectionStore((state) => state)
  const active = !connection.isConnected || connection.reconnectedAt !== null
  const now = useConnectionNow(active)
  if (connection.disconnectedAssistantMessageId !== messageId) return null
  const display = deriveConnectionDisplay({
    ...connection,
    hasInterruptedAnswer: true,
    deviceOnline: typeof navigator === 'undefined' ? true : navigator.onLine,
    now,
  })
  if (display.answer === 'hidden') return null
  const generateAgain = () => {
    const chat = useChatStore.getState()
    const lastUser = [...chat.messages].reverse().find((message) => message.role === 'user')
    if (lastUser) chat.sendMessage(lastUser.content)
  }
  return <AssistantConnectionStatus state={display.answer} agentName={agentName} onGenerateAgain={generateAgain} />
}

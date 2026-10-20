import { MessagePrimitive, useMessage } from '@assistant-ui/react'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat/types'
import { isClearContextMarker } from '@/lib/clearMarker'
import { ClearContextDivider } from './ClearContextDivider'
import { ProviderFallbackNoteLine } from './ProviderFallbackNoteLine'
import { GoalOutcomeRow } from './GoalOutcomeRow'
import { HelperEventRow } from './HelperEventRow'

/** Shared by the live, virtualized and standalone system-message paths. */
export function OutcomeMessageContent({ message }: { message: ChatMessage }) {
  if (message.goalOutcome) return <GoalOutcomeRow outcome={message.goalOutcome} />
  if (message.helperEvent) {
    return (
      <HelperEventRow
        event={message.helperEvent}
        announce={message.announceHelperEvent}
        pending={message.id.startsWith('repair-pending-')}
      />
    )
  }
  return null
}

/** Live AssistantUI path; structured payloads remain in the session store. */
export function SystemMessage() {
  const message = useMessage()
  const storeMsg = useChatStore((s) => s.messagesById[message.id])
  if (storeMsg?.goalOutcome || storeMsg?.helperEvent) {
    return (
      <MessagePrimitive.Root className="flex justify-center px-[var(--space-3)] py-[var(--space-2)]">
        <OutcomeMessageContent message={storeMsg} />
      </MessagePrimitive.Root>
    )
  }
  if (storeMsg?.providerFallbackNote) {
    return (
      <MessagePrimitive.Root>
        <ProviderFallbackNoteLine
          message={storeMsg.providerFallbackNote.message}
          pickNewModelHint={storeMsg.providerFallbackNote.pickNewModelHint}
        />
      </MessagePrimitive.Root>
    )
  }
  if (storeMsg && isClearContextMarker(storeMsg)) {
    return (
      <MessagePrimitive.Root className="flex justify-center px-[var(--space-3)] py-[var(--space-2)]">
        <ClearContextDivider messageId={storeMsg.id} content={storeMsg.content ?? ''} />
      </MessagePrimitive.Root>
    )
  }
  return (
    <MessagePrimitive.Root
      className="flex justify-center py-[var(--space-2)]"
      data-testid={storeMsg?.goalAckGoalId ? 'goal-ack-line' : storeMsg?.browserHandoverNoticeId ? 'browser-handover-notice' : undefined}
    >
      <div className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] bg-[var(--color-surface-2)] px-[var(--space-2-5)] py-[var(--space-1)] rounded-full">
        <MessagePrimitive.Parts>
          {({ part }) => part.type === 'text' ? <span>{part.text}</span> : null}
        </MessagePrimitive.Parts>
      </div>
    </MessagePrimitive.Root>
  )
}

/** Historical/virtualized path, also used when ResizeObserver is unavailable. */
export function VirtualSystemMessageRow({ message }: { message: ChatMessage }) {
  if (message.goalOutcome || message.helperEvent) {
    return (
      <div data-message-role="system" data-message-id={message.id} className="flex justify-center px-[var(--space-3)] py-[var(--space-2)]">
        <OutcomeMessageContent message={message} />
      </div>
    )
  }
  if (message.providerFallbackNote) {
    return (
      <div data-message-role="system" data-message-id={message.id}>
        <ProviderFallbackNoteLine
          message={message.providerFallbackNote.message}
          pickNewModelHint={message.providerFallbackNote.pickNewModelHint}
        />
      </div>
    )
  }
  if (isClearContextMarker(message)) {
    return <ClearContextDivider messageId={message.id} content={message.content ?? ''} />
  }
  return (
    <div
      data-message-role="system"
      data-message-id={message.id}
      data-testid={message.goalAckGoalId ? 'goal-ack-line' : message.browserHandoverNoticeId ? 'browser-handover-notice' : undefined}
      className="flex justify-center py-[var(--space-2)]"
    >
      <div className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] bg-[var(--color-surface-2)] px-[var(--space-2-5)] py-[var(--space-1)] rounded-full">
        <span>{message.content}</span>
      </div>
    </div>
  )
}

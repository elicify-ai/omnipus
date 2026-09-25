/**
 * Events for the open chat.
 *
 * Thin seam over `useDelegationEvents` (`src/hooks/useDelegationEvents.ts`).
 * Chat-screen tests inject fixtures by mocking this module; the derivation
 * itself is not reimplemented here.
 */
import { useDelegationEvents } from '@/hooks/useDelegationEvents'
import type { DelegationEvent } from '@/lib/delegationEvents.types'

export function useChatDelegationEvents(sessionId: string | null | undefined): DelegationEvent[] {
  return useDelegationEvents(sessionId)
}

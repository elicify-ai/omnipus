/**
 * Events for the open chat.
 *
 * Lane 2 owns `src/hooks/useDelegationEvents.ts` (`useDelegationEvents`).
 * That file is not in this worktree yet, and this lane must not ship a
 * second derivation. The glob binds that exact path when the file exists
 * and yields nothing when it does not, so the thread still compiles.
 * Tests inject fixtures by mocking this module.
 */
import type { DelegationEvent, UseDelegationEvents } from '@/lib/delegationEvents.types'

const loaded = import.meta.glob<{ useDelegationEvents: UseDelegationEvents }>(
  '../../hooks/useDelegationEvents.ts',
  { eager: true },
)

const useDelegationEvents: UseDelegationEvents =
  Object.values(loaded)[0]?.useDelegationEvents ?? (() => [])

export function useChatDelegationEvents(sessionId: string | null | undefined): DelegationEvent[] {
  return useDelegationEvents(sessionId)
}

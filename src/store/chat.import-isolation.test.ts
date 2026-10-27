// The chat store must import cleanly when '@/store/session' is replaced by a
// plain selector hook (as many component tests do): no module-load read of
// useSessionStore.getState().
import { describe, expect, it, vi } from 'vitest'

vi.mock('@/store/session', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session')>()
  // A selector-only hook with NO getState, exactly like those component mocks.
  const useSessionStore = (selector: (state: { activeSessionId: string | null }) => unknown) => selector({ activeSessionId: null })
  return { ...actual, useSessionStore }
})

describe('chat store module load', () => {
  it('imports without reading the session store', async () => {
    const chat = await import('@/store/chat')
    expect(typeof chat.useChatStore.getState).toBe('function')
  })
})

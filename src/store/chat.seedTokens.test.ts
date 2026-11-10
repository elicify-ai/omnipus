/**
 * chat.seedSessionTokens tests
 *
 * Verifies:
 *   1. Missing aggregate stays unknown; authoritative zero is a known value.
 *   2. D16 merges newer absolute observations without lowering a known total.
 *   3. The explicit legacy updateSessionStats helper remains compatible;
 *      addressed live done frames use absolute observations, tested separately.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'

// Mock dependent stores
vi.mock('@/store/session', () => ({
  useSessionStore: { getState: () => ({ activeSessionId: 'sess-1' }) },
  registerChatSetReplaying: vi.fn(),
  registerChatResetForReplay: vi.fn(),
  registerChatClearPendingAutoApprove: vi.fn(),
  registerChatAbandonPendingFirstSend: vi.fn(),
  // Wave-2's + New chat guard registers this getter when chat.ts loads.
  // Token seeding does not use the guard, but the module must still collect.
  registerReadPendingFirstSend: vi.fn(),
  registerSyncChatForeground: vi.fn(),
  // chat.ts registers the in-flight-attach reader at module load (single-attach guard).
  registerIsSessionReplaying: vi.fn(),
  // #823 catch-up redesign (BE-DESIGN.md §6.1) — chat.ts registers this at
  // module load; a mock missing it crashes the import (see registerGetSessionCursor's
  // own doc comment, src/store/session.ts).
  registerGetSessionCursor: vi.fn(),
}))
vi.mock('@/store/connection', () => ({
  useConnectionStore: { getState: () => ({ isConnected: false, connection: null }) },
}))
vi.mock('@/store/ui', () => ({
  useUiStore: { getState: () => ({ addToast: vi.fn() }) },
}))
vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: { getState: () => ({ activeWorkspaceId: null }) },
}))
vi.mock('@/store/notifications', () => ({
  useNotificationsStore: { getState: () => ({ add: vi.fn() }) },
}))
vi.mock('@/store/toolApproval', () => ({
  useToolApprovalStore: { getState: () => ({ addApproval: vi.fn() }) },
}))
vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: { getState: () => ({ onPairingFrame: vi.fn() }) },
}))
vi.mock('@/lib/queryClient', () => ({
  queryClient: { invalidateQueries: vi.fn() },
}))

import { useChatStore } from './chat'

describe('useChatStore.seedSessionTokens', () => {
  beforeEach(() => {
    // Reset store to empty state
    useChatStore.setState({ sessionsById: {}, sessionTokens: undefined })
  })

  it('seeds sessionTokens for a fresh session bucket whose aggregate was unknown', () => {
    // Set active session via session store mock side-channel
    // We patch the active session id directly on the store for the test.
    // The store reads activeSessionId from useSessionStore.getState()
    // which is mocked to return 'sess-1'.

    // D16: a fresh bucket without aggregate evidence is exactly unknown.
    const { seedSessionTokens, sessionsById } = useChatStore.getState()
    expect(sessionsById['sess-1']?.sessionTokens).toBeUndefined()

    seedSessionTokens(44000)

    const updated = useChatStore.getState()
    // The seeded value propagates to the foreground sessionTokens
    expect(updated.sessionTokens).toBe(44000)
    expect(updated.sessionsById['sess-1']?.sessionTokens).toBe(44000)
  })

  it('merges a newer absolute total and never lowers it for a stale observation', () => {
    // First authoritative absolute observation.
    useChatStore.getState().seedSessionTokens(44000)
    expect(useChatStore.getState().sessionTokens).toBe(44000)

    // D16: later authoritative usage is nondecreasing, not first-seed-wins.
    useChatStore.getState().seedSessionTokens(99000)
    expect(useChatStore.getState().sessionTokens).toBe(99000)
    useChatStore.getState().seedSessionTokens(44000)
    expect(useChatStore.getState().sessionTokens, 'stale lower evidence cannot erase the newer total').toBe(99000)
  })

  it('a verified recorded zero is distinct from the fresh unknown observation', () => {
    expect(useChatStore.getState().sessionTokens).toBeUndefined()
    useChatStore.getState().seedSessionTokens(0)
    expect(useChatStore.getState().sessionTokens).toBe(0)
    expect(useChatStore.getState().sessionsById['sess-1'].sessionTokens).toBe(0)
  })

  it('accumulates live delta on top of seeded value via updateSessionStats', () => {
    useChatStore.getState().seedSessionTokens(10000)
    expect(useChatStore.getState().sessionTokens).toBe(10000)

    // A new turn comes in with 500 tokens
    useChatStore.getState().updateSessionStats(500, 0)
    expect(useChatStore.getState().sessionTokens).toBe(10500)
  })
})

/**
 * chat.seedSessionTokens tests
 *
 * Verifies:
 *   1. Seeds sessionTokens when bucket is at 0 (fresh attach).
 *   2. Does NOT overwrite when sessionTokens is already > 0 (live accumulation).
 *   3. updateSessionStats still accumulates on top of seeded value.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'

// Mock dependent stores
vi.mock('@/store/session', () => ({
  useSessionStore: { getState: () => ({ activeSessionId: 'sess-1' }) },
  registerChatSetReplaying: vi.fn(),
  registerChatResetForReplay: vi.fn(),
  registerSyncChatForeground: vi.fn(),
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
    useChatStore.setState({ sessionsById: {}, sessionTokens: 0 })
  })

  it('seeds sessionTokens for a fresh session bucket (starts at 0)', () => {
    // Set active session via session store mock side-channel
    // We patch the active session id directly on the store for the test.
    // The store reads activeSessionId from useSessionStore.getState()
    // which is mocked to return 'sess-1'.

    // Ensure the bucket starts at 0
    const { seedSessionTokens, sessionsById } = useChatStore.getState()
    expect(sessionsById['sess-1']?.sessionTokens ?? 0).toBe(0)

    seedSessionTokens(44000)

    const updated = useChatStore.getState()
    // The seeded value propagates to the foreground sessionTokens
    expect(updated.sessionTokens).toBe(44000)
    expect(updated.sessionsById['sess-1']?.sessionTokens).toBe(44000)
  })

  it('does NOT overwrite sessionTokens when bucket already has accumulated tokens', () => {
    // First seed
    useChatStore.getState().seedSessionTokens(44000)
    expect(useChatStore.getState().sessionTokens).toBe(44000)

    // Try to seed again — should be a no-op because bucket is no longer at 0
    useChatStore.getState().seedSessionTokens(99000)
    // Still 44000, not 99000
    expect(useChatStore.getState().sessionTokens).toBe(44000)
  })

  it('accumulates live delta on top of seeded value via updateSessionStats', () => {
    useChatStore.getState().seedSessionTokens(10000)
    expect(useChatStore.getState().sessionTokens).toBe(10000)

    // A new turn comes in with 500 tokens
    useChatStore.getState().updateSessionStats(500, 0)
    expect(useChatStore.getState().sessionTokens).toBe(10500)
  })
})

// A failed restore must offer Retry, and that action must run workspace entry again.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchSessions, fetchWorkspace } from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { runWorkspaceEntry } from './workspaceEntryFlow'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSessions: vi.fn(),
    fetchWorkspace: vi.fn(),
  }
})

beforeEach(() => {
  localStorage.clear()
  useUiStore.setState({ toasts: [] })
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    sessionByWorkspace: {},
    workspaceEntry: null,
  })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-product' })
  vi.mocked(fetchSessions).mockRejectedValue(new Error('offline'))
  vi.mocked(fetchWorkspace).mockResolvedValue({ member_configs: {} } as Awaited<ReturnType<typeof fetchWorkspace>>)
})

afterEach(() => {
  for (const toast of useUiStore.getState().toasts) {
    useUiStore.getState().removeToast(toast.id)
  }
})

describe('workspace restore warning', () => {
  it('includes a Retry action that runs entry again', async () => {
    await runWorkspaceEntry('ws-product')
    const toast = useUiStore.getState().toasts[0]
    expect(toast?.message).toBe('Could not restore your last conversation. Retry to try again.')
    expect(toast?.variant).toBe('warning')
    expect(toast?.action?.label).toBe('Retry')

    vi.mocked(fetchSessions).mockClear()
    toast?.action?.onClick()
    await vi.waitFor(() => {
      expect(fetchSessions).toHaveBeenCalled()
    })
  })
})

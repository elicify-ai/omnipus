import { afterEach, describe, expect, it, vi } from 'vitest'

class NoopBroadcastChannel {
  addEventListener() {}
  removeEventListener() {}
  postMessage() {}
  close() {}
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('policy-driven pop-out workspace following', () => {
  it('re-keys an owned Mail pop-out when its workspace changes', async () => {
    vi.stubGlobal('BroadcastChannel', NoopBroadcastChannel)
    const lifecycle = await import('./panelPopoutLifecycle')
    const presence = await import('./panelTabPresence')
    const stopOwner = lifecycle.startPanelPopoutLifecycleOwner()
    const handle = {
      closed: false,
      focus: vi.fn(),
      close: vi.fn(),
    } as unknown as Window

    try {
      lifecycle.registerPanelPopout({
        popoutId: 'mail-popout-1',
        identity: { panelId: 'mail', workspaceId: 'workspace-a' },
        handle,
        onClosed: vi.fn(),
      })

      expect(typeof lifecycle.updatePanelPopoutWorkspace).toBe('function')
      lifecycle.updatePanelPopoutWorkspace('mail', 'mail-popout-1', 'workspace-b')

      expect(presence.focusPanelTab({ panelId: 'mail', workspaceId: 'workspace-a' })).toBe(false)
      expect(presence.focusPanelTab({ panelId: 'mail', workspaceId: 'workspace-b' })).toBe(true)
      expect(handle.focus).toHaveBeenCalledOnce()
    } finally {
      stopOwner()
    }
  })

  it('keeps an unscoped Library pop-out in its app bucket under when-scoped policy', async () => {
    vi.stubGlobal('BroadcastChannel', NoopBroadcastChannel)
    const lifecycle = await import('./panelPopoutLifecycle')
    const presence = await import('./panelTabPresence')
    const stopOwner = lifecycle.startPanelPopoutLifecycleOwner()
    const handle = {
      closed: false,
      focus: vi.fn(),
      close: vi.fn(),
    } as unknown as Window

    try {
      lifecycle.registerPanelPopout({
        popoutId: 'library-popout-app',
        identity: { panelId: 'library' },
        handle,
        onClosed: vi.fn(),
      })

      lifecycle.updatePanelPopoutWorkspace('library', 'library-popout-app', 'workspace-b')

      expect(presence.focusPanelTab({ panelId: 'library' })).toBe(true)
      expect(presence.focusPanelTab({ panelId: 'library', workspaceId: 'workspace-b' })).toBe(false)
    } finally {
      stopOwner()
    }
  })
})

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
    }

    try {
      lifecycle.registerPanelPopout({
        popoutId: 'mail-popout-1',
        identity: { panelId: 'mail', workspaceId: 'workspace-a' },
        context: { workspaceId: 'workspace-a' },
        handle: handle as unknown as Window,
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

  it('re-keys an unscoped Library pop-out to its current workspace and closes in that scope', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('BroadcastChannel', NoopBroadcastChannel)
    const lifecycle = await import('./panelPopoutLifecycle')
    const presence = await import('./panelTabPresence')
    const stopOwner = lifecycle.startPanelPopoutLifecycleOwner()
    const onClosed = vi.fn()
    const handle = {
      closed: false,
      focus: vi.fn(),
      close: vi.fn(),
    }

    try {
      lifecycle.registerPanelPopout({
        popoutId: 'library-popout-app',
        identity: { panelId: 'library' },
        context: {},
        handle: handle as unknown as Window,
        onClosed,
      })

      lifecycle.updatePanelPopoutWorkspace('library', 'library-popout-app', 'workspace-b')

      expect(presence.focusPanelTab({ panelId: 'library' })).toBe(false)
      expect(presence.focusPanelTab({ panelId: 'library', workspaceId: 'workspace-b' })).toBe(true)
      handle.closed = true
      vi.advanceTimersByTime(250)
      expect(onClosed).toHaveBeenCalledWith({
        panelId: 'library',
        workspaceId: 'workspace-b',
      }, { workspaceId: 'workspace-b' })
    } finally {
      stopOwner()
      vi.useRealTimers()
    }
  })
})

import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  announcePanelTabPresence,
  focusPanelTab,
  forgetPanelTabHandle,
  getPanelTabPresence,
  registerPanelTabHandle,
  startPanelTabPresenceMonitor,
  type PanelIdentity,
} from './panelTabPresence'

describe('panel tab presence lifecycle', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('tracks a manual tab, focuses it on request, and clears it on navigation away', async () => {
    const identity = { panelId: 'library', workspaceId: 'ws-1' } satisfies PanelIdentity
    const stopMonitor = startPanelTabPresenceMonitor()
    const announcement = announcePanelTabPresence(identity)
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})

    try {
      await vi.waitFor(() => {
        expect(getPanelTabPresence()).toContainEqual(identity)
      })

      expect(focusPanelTab(identity)).toBe(true)
      await vi.waitFor(() => expect(focus).toHaveBeenCalledOnce())

      announcement.stop()
      await vi.waitFor(() => expect(getPanelTabPresence()).not.toContainEqual(identity))
    } finally {
      announcement.stop()
      stopMonitor()
    }
  })

  it('focuses an app-opened handle directly without navigating it', () => {
    const identity = { panelId: 'library', workspaceId: 'ws-1' } satisfies PanelIdentity
    const handle = { closed: false, focus: vi.fn() } as unknown as Window
    registerPanelTabHandle(identity, handle)

    try {
      expect(focusPanelTab(identity)).toBe(true)
      expect(handle.focus).toHaveBeenCalledOnce()
    } finally {
      forgetPanelTabHandle(identity, handle)
    }
  })
})

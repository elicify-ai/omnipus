import { afterEach, describe, expect, it, vi } from 'vitest'

type PresenceApi = typeof import('./panelTabPresence') & {
  acceptPanelPresenceMessage?: (value: unknown) => boolean
}

class CapturingBroadcastChannel {
  static messages: unknown[] = []
  name: string

  constructor(name: string) {
    this.name = name
  }

  addEventListener() {}
  removeEventListener() {}
  close() {}
  postMessage(value: unknown) {
    CapturingBroadcastChannel.messages.push(value)
  }
}

async function loadFresh(): Promise<PresenceApi> {
  vi.resetModules()
  return import('./panelTabPresence')
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  CapturingBroadcastChannel.messages = []
})

describe('panel-tab presence security boundaries', () => {
  it('broadcasts only an opaque digest, never raw Browser session or agent ids', async () => {
    vi.stubGlobal('BroadcastChannel', CapturingBroadcastChannel)
    const api = await loadFresh()
    const announcement = api.announcePanelTabPresence({
      panelId: 'browser',
      sessionId: 'session-secret',
      agentId: 'agent-secret',
    })

    try {
      await vi.waitFor(() => expect(CapturingBroadcastChannel.messages.length).toBeGreaterThan(0))
      const serialized = JSON.stringify(CapturingBroadcastChannel.messages)
      expect(serialized).not.toContain('session-secret')
      expect(serialized).not.toContain('agent-secret')
      expect(serialized).not.toContain('sessionId')
      expect(serialized).not.toContain('agentId')
      expect(serialized).toMatch(/[a-f0-9]{64}/)
    } finally {
      announcement.stop()
    }
  })

  it('strictly accepts the opaque envelope and rejects raw, malformed, or extended announcements', async () => {
    const api = await loadFresh()
    expect(typeof api.acceptPanelPresenceMessage).toBe('function')
    const accept = api.acceptPanelPresenceMessage!
    const valid = {
      type: 'presence',
      tabId: '8e51c73e-7a88-4acc-a779-cd09cc949f11',
      identityKey: 'a'.repeat(64),
      focusNonce: '90d82f08-ff9e-4de6-b7f7-bf08b27c4971',
      sentAt: Date.now(),
    }
    expect(accept(valid)).toBe(true)
    expect(accept({ ...valid, identity: { panelId: 'library', workspaceId: 'secret' } })).toBe(false)
    expect(accept({ ...valid, identityKey: 'not-a-digest' })).toBe(false)
    expect(accept({ ...valid, sentAt: Number.POSITIVE_INFINITY })).toBe(false)
    expect(accept({ ...valid, extra: true })).toBe(false)
  })

  it('uses the SHA-256 digest of the local identity key', async () => {
    const api = await loadFresh()
    expect(api.panelPresenceKey({ panelId: 'library', workspaceId: 'ws-1' })).toBe(
      '5a23e89edbf04a20244e17e4345e38099a3367b9b9a035d685a9cd401440ddbb',
    )
  })

  it('returns false and forgets an app handle whose focus throws', async () => {
    const api = await loadFresh()
    const identity = { panelId: 'library', workspaceId: 'ws-1' } as const
    const handle = {
      closed: false,
      focus: vi.fn(() => {
        throw new Error('focus denied')
      }),
    } as unknown as Window
    api.registerPanelTabHandle(identity, handle)

    expect(api.focusPanelTab(identity)).toBe(false)
    expect(api.focusPanelTab(identity)).toBe(false)
    expect(handle.focus).toHaveBeenCalledTimes(1)
  })

  it('warns once when BroadcastChannel is unavailable', async () => {
    vi.stubGlobal('BroadcastChannel', undefined)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const api = await loadFresh()

    const stop = api.startPanelTabPresenceMonitor()
    const first = api.announcePanelTabPresence({ panelId: 'library', workspaceId: 'ws-1' })
    const second = api.announcePanelTabPresence({ panelId: 'library', workspaceId: 'ws-2' })
    first.stop()
    second.stop()
    stop()

    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('rejects an invalid identity before registering its handle', async () => {
    const api = await loadFresh()
    const invalid = { panelId: 'settings' } as unknown as Parameters<typeof api.registerPanelTabHandle>[0]
    expect(() => api.registerPanelTabHandle(invalid, window)).toThrow(TypeError)
  })

  it('returns a detached read-only registry view rather than the live Map', async () => {
    const api = await loadFresh()
    const identity = { panelId: 'library', workspaceId: 'ws-1' } as const
    const handle = { closed: false, focus: vi.fn() } as unknown as Window
    api.registerPanelTabHandle(identity, handle)

    const view = api.getPanelTabHandleRegistry() as Map<string, Window>
    view.clear()

    expect(api.focusPanelTab(identity)).toBe(true)
    api.forgetPanelTabHandle(identity, handle)
  })
})

import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PanelIdentity } from './panelTabPresence'

class UnexpectedBroadcastChannel {
  constructor() {
    throw new Error('invalid identities must be rejected before opening a channel')
  }
}

class NoopBroadcastChannel {
  postMessage() {}
  addEventListener() {}
  removeEventListener() {}
  close() {}
}

const validBrowser: PanelIdentity = {
  panelId: 'browser',
  sessionId: 'session-1',
  agentId: 'agent-1',
}
void validBrowser

// @ts-expect-error Browser identities require both sessionId and agentId.
const invalidBrowser: PanelIdentity = { panelId: 'browser', sessionId: 'session-1' }
void invalidBrowser

// @ts-expect-error Workspace-panel identities cannot carry Browser session context.
const invalidWorkspace: PanelIdentity = { panelId: 'library', sessionId: 'session-1', agentId: 'agent-1' }
void invalidWorkspace

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.resetModules()
})

describe('panel presence identity validation', () => {
  it('warns once and returns inert announcements for invalid identities', async () => {
    vi.stubGlobal('BroadcastChannel', UnexpectedBroadcastChannel)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const api = await import('./panelTabPresence')
    const invalid = { panelId: 'browser', sessionId: 'session-only' } as unknown as PanelIdentity

    const first = api.announcePanelTabPresence(invalid)
    const second = api.announcePanelTabPresence(invalid)

    expect(() => first.update(invalid)).not.toThrow()
    expect(() => first.stop()).not.toThrow()
    expect(() => second.stop()).not.toThrow()
    expect(warn).toHaveBeenCalledTimes(1)
    expect(warn).toHaveBeenCalledWith('Panel tab presence ignored an invalid panel identity.')
  })

  it('warns once when an active announcement is updated with an invalid identity', async () => {
    vi.stubGlobal('BroadcastChannel', NoopBroadcastChannel)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const api = await import('./panelTabPresence')
    const announcement = api.announcePanelTabPresence({ panelId: 'library', workspaceId: 'ws-1' })
    const invalid = { panelId: 'browser', sessionId: 'session-only' } as unknown as PanelIdentity

    announcement.update(invalid)
    announcement.update(invalid)

    expect(warn).toHaveBeenCalledTimes(1)
    expect(warn).toHaveBeenCalledWith('Panel tab presence ignored an invalid panel identity.')
    announcement.stop()
  })
})

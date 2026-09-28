import { afterEach, describe, expect, it, vi } from 'vitest'

type Listener = (event: MessageEvent<unknown>) => void

class InspectableBroadcastChannel {
  static instances: InspectableBroadcastChannel[] = []
  readonly messages: unknown[] = []
  readonly listeners = new Set<Listener>()

  constructor(readonly name: string) {
    InspectableBroadcastChannel.instances.push(this)
  }

  addEventListener(_type: string, listener: Listener) {
    this.listeners.add(listener)
  }

  removeEventListener(_type: string, listener: Listener) {
    this.listeners.delete(listener)
  }

  postMessage(message: unknown) {
    this.messages.push(message)
  }

  emit(message: unknown) {
    for (const listener of this.listeners) listener({ data: message } as MessageEvent<unknown>)
  }

  close() {}
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.resetModules()
  InspectableBroadcastChannel.instances = []
})

describe('presence focus authorization', () => {
  it('focuses only this tab when the request carries its self-generated nonce', async () => {
    vi.stubGlobal('BroadcastChannel', InspectableBroadcastChannel)
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})
    const api = await import('./panelTabPresence')
    const announcement = api.announcePanelTabPresence({ panelId: 'library', workspaceId: 'ws-1' })
    const channel = InspectableBroadcastChannel.instances[0]!

    try {
      const presence = channel.messages.find(
        (message): message is { type: 'presence'; tabId: string; focusNonce: string } =>
          typeof message === 'object' && message !== null &&
          (message as { type?: string }).type === 'presence',
      )
      expect(presence?.focusNonce).toMatch(/^[A-Za-z0-9-]{8,128}$/)

      channel.emit({ type: 'focus', tabId: presence!.tabId })
      channel.emit({ type: 'focus', tabId: presence!.tabId, focusNonce: 'wrong-nonce' })
      expect(focus).not.toHaveBeenCalled()

      channel.emit({
        type: 'focus',
        tabId: presence!.tabId,
        focusNonce: presence!.focusNonce,
      })
      expect(focus).toHaveBeenCalledOnce()
    } finally {
      announcement.stop()
    }
  })
})

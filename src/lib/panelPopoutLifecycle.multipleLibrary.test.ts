import { afterEach, describe, expect, it, vi } from 'vitest'

type Listener = (event: MessageEvent<unknown>) => void

class LinkedBroadcastChannel {
  static channels = new Set<LinkedBroadcastChannel>()
  readonly listeners = new Set<Listener>()

  constructor(readonly name: string) {
    LinkedBroadcastChannel.channels.add(this)
  }

  addEventListener(_type: string, listener: Listener) {
    this.listeners.add(listener)
  }

  removeEventListener(_type: string, listener: Listener) {
    this.listeners.delete(listener)
  }

  postMessage(data: unknown) {
    for (const channel of LinkedBroadcastChannel.channels) {
      if (channel !== this && channel.name === this.name) {
        for (const listener of channel.listeners) listener({ data } as MessageEvent<unknown>)
      }
    }
  }

  close() {
    LinkedBroadcastChannel.channels.delete(this)
  }
}

function popup(): Window {
  return {
    closed: false,
    close: vi.fn(),
    focus: vi.fn(),
  } as unknown as Window
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  LinkedBroadcastChannel.channels.clear()
})

describe('multiple owned Library pop-outs', () => {
  it('routes workspace changes and close signals by opener-generated pop-out id', async () => {
    vi.stubGlobal('BroadcastChannel', LinkedBroadcastChannel)
    const lifecycle = await import('./panelPopoutLifecycle')
    const handoff = await import('./libraryHandoff')
    const stopOwner = lifecycle.startPanelPopoutLifecycleOwner()
    const closedA = vi.fn()
    const closedB = vi.fn()

    try {
      lifecycle.registerPanelPopout({
        popoutId: 'library-popout-a',
        identity: { panelId: 'library', workspaceId: 'workspace-a' },
        handle: popup(),
        onClosed: closedA,
      })
      lifecycle.registerPanelPopout({
        popoutId: 'library-popout-b',
        identity: { panelId: 'library', workspaceId: 'workspace-b' },
        handle: popup(),
        onClosed: closedB,
      })

      handoff.announceLibraryWorkspaceChanged('library-popout-a', 'workspace-a-next')
      handoff.announceLibraryPopoutClosed('library-popout-a', 'workspace-a-stale')

      expect(closedA).toHaveBeenCalledWith({
        panelId: 'library',
        workspaceId: 'workspace-a-next',
      })
      expect(closedB).not.toHaveBeenCalled()

      handoff.announceLibraryPopoutClosed('library-popout-b', 'workspace-b')
      expect(closedB).toHaveBeenCalledWith({ panelId: 'library', workspaceId: 'workspace-b' })
    } finally {
      stopOwner()
    }
  })
})

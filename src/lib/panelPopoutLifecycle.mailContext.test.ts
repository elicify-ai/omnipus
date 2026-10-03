import { afterEach, describe, expect, it, vi } from 'vitest'

type Listener = (event: MessageEvent<unknown>) => void

class LinkedBroadcastChannel {
  static channels = new Set<LinkedBroadcastChannel>()
  private readonly listeners = new Set<Listener>()

  constructor(private readonly name: string) {
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

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  LinkedBroadcastChannel.channels.clear()
})

describe('Mail pop-out context validation', () => {
  it('keeps mailbox ids non-empty while accepting null and opaque message references', async () => {
    vi.stubGlobal('BroadcastChannel', LinkedBroadcastChannel)
    const lifecycle = await import('./panelPopoutLifecycle')
    const onClosed = vi.fn()
    const stopOwner = lifecycle.startPanelPopoutLifecycleOwner()

    try {
      lifecycle.registerPanelPopout({
        popoutId: 'mail-popout-1',
        identity: { panelId: 'mail', workspaceId: 'workspace-a' },
        context: { workspaceId: 'workspace-a', mailboxId: 'mailbox-a' },
        handle: { closed: false, close: vi.fn(), focus: vi.fn() } as unknown as Window,
        onClosed,
      })

      lifecycle.announcePanelPopoutClosed('mail', 'mail-popout-1', {
        workspaceId: 'workspace-a',
        mailboxId: '',
        folder: 'inbox',
        messageRef: 'message-a',
      })
      expect(onClosed).not.toHaveBeenCalled()

      lifecycle.announcePanelPopoutClosed('mail', 'mail-popout-1', {
        workspaceId: 'workspace-a',
        mailboxId: null,
        folder: 'drafts',
        messageRef: '',
      })
      expect(onClosed).toHaveBeenCalledWith(
        { panelId: 'mail', workspaceId: 'workspace-a' },
        {
          workspaceId: 'workspace-a',
          mailboxId: null,
          folder: 'drafts',
          messageRef: '',
        },
      )
    } finally {
      stopOwner()
    }
  })
})

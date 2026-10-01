import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelDefinition, WorkspacePanelContext } from './types'
import { usePanelShell } from './usePanelShell'
import { usePanelShellStore } from './panelShellStore'

const { registerPanelPopout } = vi.hoisted(() => ({
  registerPanelPopout: vi.fn(),
}))

vi.mock('@/lib/panelPopoutLifecycle', () => ({
  registerPanelPopout,
  discardPanelPopout: vi.fn(),
}))

vi.mock('@/lib/panelTabPresence', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/panelTabPresence')>()),
  resolveRegisteredPanelOpen: ({ open }: { open: () => Window | null }) => {
    open()
    return { kind: 'opened' as const }
  },
}))

function Probe() {
  return null
}

const mail: PanelDefinition = {
  id: 'mail',
  title: 'Mail',
  content: Probe,
  fullScreen: {
    toSearch: (context) => {
      const workspace = context as WorkspacePanelContext
      return {
        workspace: workspace.workspaceId ?? '',
        mailbox: workspace.mailboxId ?? '',
        folder: workspace.folder ?? '',
        message: workspace.messageRef ?? '',
      }
    },
    fromSearch: () => null,
  },
}

beforeEach(() => {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: null,
    guardPending: false,
    historyPushed: false,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  usePanelShellStore.getState().closePanel()
})

describe('shell-owned full-screen URL', () => {
  it('preserves opaque context, warns above 8 KB, and still opens the full URL', async () => {
    const messageRef = `message /?&=${'π'.repeat(4_100)}`
    const context: WorkspacePanelContext = {
      workspaceId: 'workspace A',
      mailboxId: 'mailbox/B',
      folder: 'drafts',
      messageRef,
    }
    const replace = vi.fn()
    vi.spyOn(window, 'open').mockReturnValue({
      closed: false,
      opener: window,
      location: { replace },
      close: vi.fn(),
      focus: vi.fn(),
    } as unknown as Window)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel('mail', context)
    const { result } = renderHook(() => usePanelShell([mail], 'dana'))

    await act(async () => {
      await expect(result.current.requestExpand()).resolves.toBe('opened')
    })

    const fullUrl = replace.mock.calls[0]?.[0] as string
    const search = new URL(fullUrl, window.location.origin).hash.split('?')[1]
    const params = new URLSearchParams(search)
    expect(fullUrl.length).toBeGreaterThan(8 * 1024)
    expect(params.get('workspace')).toBe(context.workspaceId)
    expect(params.get('mailbox')).toBe(context.mailboxId)
    expect(params.get('folder')).toBe(context.folder)
    expect(params.get('message')).toBe(messageRef)
    expect(warn).toHaveBeenCalledWith(
      '[side-panel] Full-screen panel URL exceeds 8 KB; opening it without truncation.',
      expect.objectContaining({ panelId: 'mail' }),
    )
  })
})

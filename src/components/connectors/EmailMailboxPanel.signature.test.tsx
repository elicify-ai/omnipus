/**
 * RED — signature editor. Oracles: spec §16, US-1, MC-1, MC-10 iframe block, D27.
 * The editor is a section of the existing EmailMailboxPanel, not a new screen.
 * 16,384 characters is the maximum (MC-1). One past that is blocked in the panel
 * and is not sent. The live preview is a sandboxed frame with the MC-10 tokens.
 * There is no "let the agent handle new mail" switch (D27).
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/store/ui', () => ({ useUiStore: vi.fn() }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: vi.fn(),
    fetchMailboxes: vi.fn(),
    saveAgentMailbox: vi.fn(),
    deleteAgentMailbox: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchWorkspaces: vi.fn(),
    fetchChannelRouting: vi.fn(),
    isApiError: vi.fn(() => false),
  }
})

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, prop: string) =>
      ({ children, ...props }: Record<string, unknown>) =>
        React.createElement(prop as string, props, children as React.ReactNode),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useUiStore } from '@/store/ui'
import { fetchWorkspace, saveAgentMailbox } from '@/lib/api'
import { EmailMailboxPanel } from './EmailMailboxPanel'

const SIGNATURE_MAX = 16_384 // MC-1

function renderPanel() {
  vi.mocked(useUiStore).mockReturnValue({ addToast: vi.fn() } as never)
  vi.mocked(fetchWorkspace).mockResolvedValue({
    id: 'ws-1', name: 'My Workspace', status: 'active', core_team: ['mia'],
  } as never)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(['agents'], [{ id: 'mia', name: 'Mia', type: 'core', locked: true }])
  client.setQueryData(['workspaces'], [{ id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 }])
  return render(
    <QueryClientProvider client={client}>
      <EmailMailboxPanel open mailbox={null} mailboxes={[]} onOpenChange={vi.fn()} />
    </QueryClientProvider>,
  )
}

describe('EmailMailboxPanel signature editor (US-1, MC-1, MC-10, D27)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(saveAgentMailbox).mockResolvedValue({} as never)
  })

  it('shows a signature field and a sandboxed live preview', () => {
    renderPanel()
    expect(screen.getByRole('textbox', { name: /signature/i })).toBeInTheDocument()
    const frame = screen.getByTitle(/signature preview/i)
    expect(frame.tagName).toBe('IFRAME')
    expect(frame).toHaveAttribute('sandbox', 'allow-popups allow-popups-to-escape-sandbox')
    expect(frame).toHaveAttribute('referrerPolicy', 'no-referrer')
    expect(frame.getAttribute('allow')).toBe('')
    expect(frame).not.toHaveAttribute('srcdoc')
    const sandbox = frame.getAttribute('sandbox') ?? ''
    for (const banned of ['allow-scripts', 'allow-same-origin', 'allow-forms', 'allow-top-navigation', 'allow-downloads', 'allow-modals', 'allow-pointer-lock']) {
      expect(sandbox, banned).not.toContain(banned)
    }
  })

  it('blocks a signature longer than 16384 characters and does not save it', () => {
    renderPanel()
    const field = screen.getByRole('textbox', { name: /signature/i })
    fireEvent.change(field, { target: { value: 'x'.repeat(SIGNATURE_MAX + 1) } })
    fireEvent.click(screen.getByRole('button', { name: /save mailbox/i }))
    expect(saveAgentMailbox).not.toHaveBeenCalled()
    expect(screen.getByText(/16,?384/)).toBeInTheDocument()
  })

  it('does not offer a switch that lets mail start an agent (D27)', () => {
    renderPanel()
    expect(screen.queryByText(/let the agent handle new mail/i)).not.toBeInTheDocument()
  })
})

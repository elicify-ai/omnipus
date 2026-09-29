import { Suspense } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelContentProps } from '@/components/panel-shell/types'
import { mailPanelDefinition } from './mailPanelDefinition'

const { fetchAgents, fetchMailboxes, fetchMailSummary } = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAgents,
  fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api/mail')>(),
  fetchMailSummary,
}))

function renderMail(presentation: PanelContentProps['presentation'], close: () => void) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const Content = mailPanelDefinition.content
  return render(
    <QueryClientProvider client={client}>
      <Suspense fallback={<span>Loading Mail…</span>}>
        <Content
          context={{ workspaceId: 'ws-1' }}
          presentation={presentation}
          close={close}
          expand={vi.fn()}
          registerExpandContext={vi.fn()}
          onWidthSettle={vi.fn()}
        />
      </Suspense>
    </QueryClientProvider>,
  )
}

describe('F1 — fullscreen Mail exit', () => {
  beforeEach(() => {
    fetchAgents.mockReset().mockResolvedValue([])
    fetchMailboxes.mockReset().mockResolvedValue([])
    fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  })
  afterEach(() => cleanup())

  it('shows Return to chat only in fullscreen and delegates the exit to the shell close callback', async () => {
    const close = vi.fn()
    const view = renderMail('fullscreen', close)
    const returnToChat = await screen.findByRole('button', { name: 'Return to chat' }, { timeout: 30_000 })
    fireEvent.click(returnToChat)
    expect(close).toHaveBeenCalledExactlyOnceWith()
    view.unmount()

    renderMail('docked', close)
    await waitFor(() => expect(screen.getByTestId('mail-panel')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Return to chat' })).not.toBeInTheDocument()
    expect(close).toHaveBeenCalledTimes(1)
  })
})

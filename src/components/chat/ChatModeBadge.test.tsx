/**
 * ChatModeBadge tests (ADR-092).
 *
 * Three renderings per the founder's ruling ("safe" = what never leaves the
 * kernel sandbox, judged per call): Auto off -> "Ask"; Auto on + kernel
 * sandbox active -> "Auto"; Auto on + no kernel sandbox -> "Auto -> Ask"
 * with an explanatory tooltip (the Tooltip primitive's actual production
 * use case).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchSandboxStatus: vi.fn(),
  }
})

import * as api from '@/lib/api'
import { ChatModeBadge } from './ChatModeBadge'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderBadge() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ChatModeBadge />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1', activeAgentType: 'core' })
    useChatStore.setState({ autoApproveEffective: undefined, sessionsById: {} })
  })
  vi.mocked(api.fetchAgents).mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', status: 'active' }] as never)
})

describe('ChatModeBadge — three states', () => {
  it('reads "Ask" when Auto-approve is off', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ auto_approve_effective: false, kernel_sandbox_active: true } as never)
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Ask')
    })
  })

  it('reads "Auto" when Auto-approve is on and the kernel sandbox is active', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ auto_approve_effective: true, kernel_sandbox_active: true } as never)
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Auto')
      expect(screen.getByTestId('chat-mode-badge')).not.toHaveTextContent('Auto →')
    })
  })

  it('reads "Auto → Ask" with an explanatory tooltip when Auto-approve is on but no kernel sandbox is active', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ auto_approve_effective: true, kernel_sandbox_active: false } as never)
    renderBadge()
    const trigger = await screen.findByTestId('chat-mode-badge-trigger')
    expect(trigger).toHaveTextContent('Auto → Ask')

    // The tooltip primitive's actual production use case — reveal on focus.
    fireEvent.focus(trigger)
    await waitFor(() => {
      expect(screen.getByRole('tooltip')).toHaveTextContent(/no active kernel sandbox/i)
    })
  })

  it('a per-agent auto_approve_disabled floors the badge to "Ask" even when global is on', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', auto_approve_disabled: true },
    ] as never)
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ auto_approve_effective: true, kernel_sandbox_active: true } as never)
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Ask')
    })
  })

  it('a resolved per-chat override can loosen past a globally-off default', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ auto_approve_effective: false, kernel_sandbox_active: true } as never)
    act(() => {
      // The hook reads the foreground-synced field, set directly here — see
      // AutoApprovePicker.test.tsx's identical comment for why.
      useChatStore.setState({ autoApproveEffective: true })
    })
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Auto')
    })
  })
})

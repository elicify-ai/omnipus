/**
 * ChatModeBadge tests (ADR-092).
 *
 * Founder decision (2026-09-24): Auto-approve no longer requires an
 * enforcing kernel sandbox — Auto works for every tool, shell included, on
 * every platform, whether or not the sandbox is enforcing.
 * `kernel_sandbox_active` is WARNING-ONLY now. Three renderings: Auto off ->
 * "Ask"; Auto on + kernel sandbox active -> "Auto"; Auto on + no kernel
 * sandbox -> "Auto — no sandbox" (still Auto, a caution) with an explanatory
 * tooltip (the Tooltip primitive's actual production use case). The old
 * "Auto → Ask" text (Auto silently falling back to Ask with no sandbox) is
 * gone.
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
import type { SandboxStatus } from '@/lib/api'
import { ChatModeBadge } from './ChatModeBadge'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

// A full, realistic SandboxStatus — every REQUIRED field
// (contracts/components/schemas/SandboxStatus.yaml: backend, available,
// kernel_level, policy_applied, seccomp_enabled, bind_ports_count) present,
// not just the three optional Auto-approve-related fields this suite cares
// about. Fixtures that only ever mocked the optional fields, cast through
// `as never`, are exactly the pattern that hid ADR-092 review finding A: the
// real GET /security/sandbox-status handler omitted kernel_sandbox_active/
// auto_approve_effective entirely until the gateway lane's fix landed, and a
// type-erased fixture never would have caught that mismatch.
function sandboxStatus(overrides: Partial<SandboxStatus> = {}): SandboxStatus {
  return {
    backend: 'landlock',
    available: true,
    kernel_level: true,
    policy_applied: true,
    seccomp_enabled: true,
    bind_ports_count: 0,
    ...overrides,
  }
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
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true }))
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Ask')
    })
  })

  it('reads "Auto" when Auto-approve is on and the kernel sandbox is active', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }))
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Auto')
      expect(screen.getByTestId('chat-mode-badge')).not.toHaveTextContent('no sandbox')
    })
  })

  it('reads "Auto — no sandbox" (still Auto, a caution) with an explanatory tooltip when Auto-approve is on but no kernel sandbox is enforcing', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: false }))
    renderBadge()
    const trigger = await screen.findByTestId('chat-mode-badge-trigger')
    expect(trigger).toHaveTextContent('Auto — no sandbox')
    // The old "Auto → Ask" wording (Auto silently degrading to Ask with no
    // sandbox) must be gone — Auto is still active, this is a caution.
    expect(trigger).not.toHaveTextContent('Auto → Ask')
    expect(trigger).not.toHaveTextContent('→')

    // The tooltip primitive's actual production use case — reveal on focus.
    fireEvent.focus(trigger)
    await waitFor(() => {
      const tooltip = screen.getByRole('tooltip')
      expect(tooltip).toHaveTextContent(
        'No kernel sandbox is enforcing. Safe tool calls still run without asking, but shell commands are checked by reading the command text only.',
      )
    })
  })

  it('a per-agent auto_approve_disabled floors the badge to "Ask" even when global is on', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', auto_approve_disabled: true },
    ] as never)
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }))
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Ask')
    })
  })

  it('a resolved per-chat override can loosen past a globally-off default', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true }))
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

// ADR-092 review finding A: SandboxStatus.god_mode_active is a stronger
// floor than Auto — checked first, whatever auto_approve_effective and
// kernel_sandbox_active say (contracts/components/schemas/SandboxStatus.yaml).
describe('ChatModeBadge — God Mode floor (item 1)', () => {
  it('reads "God Mode" when god_mode_active is true, even though auto_approve_effective is false', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({
      auto_approve_effective: false,
      kernel_sandbox_active: true,
      god_mode_active: true,
    }))
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('God Mode')
    })
    expect(screen.queryByTestId('chat-mode-badge')).not.toHaveTextContent('Ask')
  })

  it('reads "God Mode" even when auto_approve_effective is true and the kernel sandbox is active (not "Auto")', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({
      auto_approve_effective: true,
      kernel_sandbox_active: true,
      god_mode_active: true,
    }))
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('God Mode')
    })
  })

  it('does not read "God Mode" when god_mode_active is false or absent', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({
      auto_approve_effective: false,
      kernel_sandbox_active: true,
      god_mode_active: false,
    }))
    renderBadge()
    await waitFor(() => {
      expect(screen.getByTestId('chat-mode-badge')).toHaveTextContent('Ask')
    })
    expect(screen.queryByTestId('chat-mode-badge')).not.toHaveTextContent('God Mode')
  })
})

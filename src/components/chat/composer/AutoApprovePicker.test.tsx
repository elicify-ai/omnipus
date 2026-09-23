/**
 * AutoApprovePicker tests (ADR-092).
 *
 * Scaffolding (QueryClient wrapper, store reset) mirrors ModelPicker.test.tsx
 * — same composer, same pattern.
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
import { AutoApprovePicker } from './AutoApprovePicker'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

// A full, realistic SandboxStatus (every required field present, not just
// the Auto-approve-related optional ones) — see ChatModeBadge.test.tsx's
// identical helper for why: a fixture that only ever mocked the optional
// fields, cast through `as never`, is exactly what hid ADR-092 review
// finding A (the real handler omitted these fields entirely).
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

function renderPicker(props: { disabled?: boolean } = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AutoApprovePicker {...props} />
    </QueryClientProvider>,
  )
}

const AGENT_NO_OVERRIDE = { id: 'mia', name: 'Mia', type: 'core', status: 'active' }
const AGENT_FORCES_OFF = { id: 'mia', name: 'Mia', type: 'core', status: 'active', auto_approve_disabled: true }

beforeEach(() => {
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1', activeAgentType: 'core' })
    useChatStore.setState({ autoApproveEffective: undefined, sessionsById: {} })
  })
  vi.mocked(api.fetchAgents).mockResolvedValue([AGENT_NO_OVERRIDE] as never)
  vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true }))
})

describe('AutoApprovePicker — resolution', () => {
  it('reflects global on, no per-agent/per-chat override', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }))
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'true')
    })
  })

  it('reflects global off', async () => {
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'false')
    })
  })

  it('the per-agent auto_approve_disabled floors it off even when global is on', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValue([AGENT_FORCES_OFF] as never)
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }))
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'false')
    })
  })

  it('a resolved session override wins over the agent/global resolution — including loosening past both being off', async () => {
    act(() => {
      // The hook reads the foreground-synced field (matches every other
      // ModelPicker-style consumer) — set it directly rather than
      // sessionsById, which only syncs to the foreground via withBucket
      // (exercised for real in chat.sessionModeUpdate.test.ts).
      useChatStore.setState({ autoApproveEffective: true })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'true')
    })
  })
})

describe('AutoApprovePicker — sends the human-only per-chat toggle', () => {
  it('toggling on sends sendSessionModeUpdate(sessionId, true)', async () => {
    const spy = vi.fn()
    act(() => {
      useChatStore.setState({ sendSessionModeUpdate: spy })
    })
    renderPicker()
    const toggle = await screen.findByTestId('composer-auto-approve-toggle')
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    fireEvent.click(toggle)
    expect(spy).toHaveBeenCalledWith('sess_1', true)
  })

  it('is disabled with no real session (no session_id to target yet)', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
  })

  it('is disabled while the session is the transient "__pending" placeholder', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: '__pending' })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
  })

  it('is disabled when the disabled prop is set (matches AgentPicker/ModelPicker agentRemoved gating)', async () => {
    renderPicker({ disabled: true })
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
  })
})

describe('AutoApprovePicker — explains itself via the catalogued Tooltip, not a native title= (item 9)', () => {
  it('carries no native title attribute anywhere in the control', async () => {
    renderPicker()
    await screen.findByTestId('composer-auto-approve-toggle')
    expect(document.querySelector('[title]')).toBeNull()
  })

  it('the disabled reason is reachable by keyboard focus alone, with no pointer involved', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })

    // The disabled Switch is pulled out of the tab order — a native title=
    // was never reachable by keyboard for it anyway (title only shows on
    // mouse hover). Tooltip's own trigger picks up that tab stop instead.
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    expect(trigger).toHaveAttribute('tabIndex', '0')
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent('Send a message first to enable per-chat Auto-approve')
  })

  it('the enabled-state explanation is also reachable via the same Tooltip trigger', async () => {
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).not.toBeDisabled()
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'Auto-approve for this chat — turns on or off for this conversation only',
    )
  })
})

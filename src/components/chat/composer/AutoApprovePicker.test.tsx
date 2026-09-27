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
    useChatStore.setState({ autoApproveEffective: undefined, sessionsById: {}, pendingAutoApproveChoice: null })
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

  it('is disabled when the disabled prop is set (matches AgentPicker/ModelPicker agentRemoved gating)', async () => {
    renderPicker({ disabled: true })
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
  })

  it('is disabled with no active agent — the only remaining disable condition', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
  })

  // Founder ruling (2026-09-24): a flip in a running chat applies from the
  // very next tool call, not merely the next message — so the switch must
  // stay enabled AND still send immediately while a turn is streaming; the
  // frame must never be queued or held back until the turn finishes.
  it('is NOT disabled while a turn is streaming, and toggling still sends session_mode_update immediately', async () => {
    const spy = vi.fn()
    act(() => {
      useChatStore.setState({ sendSessionModeUpdate: spy, isStreaming: true })
    })
    renderPicker()
    const toggle = await screen.findByTestId('composer-auto-approve-toggle')
    expect(toggle).not.toBeDisabled()
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    fireEvent.click(toggle)
    expect(spy).toHaveBeenCalledWith('sess_1', true)
  })
})

describe('AutoApprovePicker — usable in a brand-new chat with no real session yet (founder-reported UX fix)', () => {
  it('is NOT disabled with no active session — a fresh chat before the first message', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).not.toBeDisabled()
    })
  })

  it('is NOT disabled while the session is the transient "__pending" placeholder', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: '__pending' })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).not.toBeDisabled()
    })
  })

  it('toggling with no real session records a PENDING choice instead of sending session_mode_update', async () => {
    const spy = vi.fn()
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
      useChatStore.setState({ sendSessionModeUpdate: spy })
    })
    renderPicker()
    const toggle = await screen.findByTestId('composer-auto-approve-toggle')
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    fireEvent.click(toggle)
    expect(spy).not.toHaveBeenCalled()
    expect(useChatStore.getState().pendingAutoApproveChoice).toBe(true)
  })

  it('reflects the pending choice immediately — the switch flips without waiting for a server ack', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    renderPicker()
    const toggle = await screen.findByTestId('composer-auto-approve-toggle')
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    fireEvent.click(toggle)
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'))
  })
})

describe('AutoApprovePicker — explains itself via the catalogued Tooltip, not a native title= (item 9)', () => {
  it('carries no native title attribute anywhere in the control', async () => {
    renderPicker()
    await screen.findByTestId('composer-auto-approve-toggle')
    expect(document.querySelector('[title]')).toBeNull()
  })

  it('the explanation is reachable by keyboard focus alone, with no pointer involved, while genuinely disabled (no active agent)', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null })
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
    // Design-system fix: the tooltip explains what the switch DOES, never
    // why it happens to be disabled right now. This case has a real session
    // ('sess_1', from beforeEach), so the running-chat wording applies.
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'Auto-approve for this chat — applies from the next step',
    )
  })

  it('the same explanation is reachable via the Tooltip trigger while enabled (running chat)', async () => {
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).not.toBeDisabled()
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'Auto-approve for this chat — applies from the next step',
    )
  })

  // Founder ruling (2026-09-24): the wording differs by whether this chat
  // has a real session yet — never a "disabled reason" message either way.
  it('names "your first message" with no real session yet', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
    })
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).not.toBeDisabled()
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'Auto-approve for this chat — applies from your first message',
    )
  })

  it('names "the next step" — not "the next message" — once the chat has a real session', async () => {
    renderPicker() // beforeEach seeds a real session ('sess_1')
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).not.toBeDisabled()
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip).toHaveTextContent('applies from the next step')
    expect(tooltip).not.toHaveTextContent('next message')
  })
})

// Founder decision (2026-09-24): Auto works without an enforcing kernel
// sandbox — it must never flip back to disabled/off, but the tooltip adds a
// calm caution matching ChatModeBadge's "Auto — no sandbox" wording.
describe('AutoApprovePicker — no-sandbox caution (2026-09-24)', () => {
  it('is still checked (Auto stays on) when resolved true and no kernel sandbox is enforcing', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: false }),
    )
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'true')
    })
  })

  it('appends the no-sandbox caution to the tooltip when Auto is on and no kernel sandbox is enforcing', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: false }),
    )
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'true')
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip).toHaveTextContent('applies from the next step')
    expect(tooltip).toHaveTextContent(
      'No kernel sandbox — shell commands ask first unless read-only or operator-allowed.',
    )
    // The old "checked by reading the command text only" wording described
    // stale behaviour (2026-09-24 shell-no-sandbox-gate rewording) and must
    // never reappear.
    expect(tooltip).not.toHaveTextContent('checked by reading the command text only')
  })

  it('does NOT show the caution when Auto is off, even with no kernel sandbox enforcing', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: false }),
    )
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toHaveAttribute('aria-checked', 'false')
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip).not.toHaveTextContent('kernel sandbox')
  })
})

// Code-review finding 3: ChatModeBadge checks `godModeActive` FIRST and
// renders "God Mode" whatever `resolved` says (it's a STRONGER floor than
// Auto, not the absence of it — useResolvedAutoApprove's own contract). This
// picker used to ignore `godModeActive` entirely and just render `resolved`
// — so with God Mode on and this chat's own Auto explicitly false, the badge
// said "God Mode" while the switch showed off, and flipping the switch (it
// sent session_mode_update, which the server accepts but which changes
// nothing real under the God Mode floor) looked like it did something when
// it didn't.
describe('AutoApprovePicker — God Mode is a stronger floor than this chat’s Auto (code-review finding 3)', () => {
  it('is disabled under God Mode even though there IS an active agent and a real session (the only two conditions that used to control disabled)', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true, god_mode_active: true }),
    )
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
  })

  it('the disabled tooltip mentions God Mode and says this chat’s Auto choice does not apply', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true, god_mode_active: true }),
    )
    renderPicker()
    await waitFor(() => {
      expect(screen.getByTestId('composer-auto-approve-toggle')).toBeDisabled()
    })
    const trigger = screen.getByTestId('composer-auto-approve-tooltip-trigger')
    fireEvent.focus(trigger)
    const tooltip = screen.getByRole('tooltip')
    expect(tooltip).toHaveTextContent(/god mode/i)
    expect(tooltip).toHaveTextContent(/does not apply/i)
  })

  it('flipping does nothing under God Mode — the disabled Switch never fires sendSessionModeUpdate or records a pending choice', async () => {
    const spy = vi.fn()
    act(() => {
      useChatStore.setState({ sendSessionModeUpdate: spy })
    })
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true, god_mode_active: true }),
    )
    renderPicker()
    const toggle = await screen.findByTestId('composer-auto-approve-toggle')
    await waitFor(() => expect(toggle).toBeDisabled())
    fireEvent.click(toggle)
    expect(spy).not.toHaveBeenCalled()
    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
  })
})

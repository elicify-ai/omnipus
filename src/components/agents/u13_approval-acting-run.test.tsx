// RED pack — WC-RESUME RED·U13/U14, unit U13 (FR-034; BDD-10.2).
//
// Spec source: docs/internal/specs/session-core-spec.md
//   FR-034 — "one native approval ID/modal names acting helper/run."
//   BDD-10.2 — "One ID/current scoped actions/target restrictions, acting
//            execution name/Open; no duplicate queue/card."
//
// Oracle provenance: the spec's FR-034/BDD-10.2 clauses. An approval raised by
// a helper/run must let the human (a) see WHICH execution is acting and (b)
// reach that execution ("acting execution name/Open"). The current modal names
// only the AGENT (`resolvedAgentName`) and offers no way to open the acting
// run's own session — E-APPROVAL's `one acting approval modal/store` is not
// yet widened to the run identity.
//
// FIELD-NAME CONTRACT (stated, not hidden): the Open control carries
// `data-testid="approval-open-run"` and targets the acting run's session
// (`PendingToolApproval.sessionId`). The spec names this by meaning
// ("acting execution name/Open"); if GREEN lands a differently-named control,
// update the testid here, not the assertions.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { act } from 'react'

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return { ...actual, submitToolApproval: vi.fn(), fetchAgents: vi.fn().mockResolvedValue([]) }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn((selector) => {
    const state = { addToast: vi.fn(), toasts: [], removeToast: vi.fn() }
    return selector ? selector(state) : state
  }),
}))

vi.mock('@/lib/authLogout', () => ({ forceLogout: vi.fn() }))

import { useToolApprovalStore } from '@/store/toolApproval'
import { ToolApprovalModal } from './ToolApprovalModal'

const HELPER_APPROVAL = {
  approvalId: 'appr-helper-001',
  toolCallId: 'call-helper-001',
  toolName: 'bash',
  args: { command: 'echo hi' },
  agentId: 'agent-helper',
  sessionId: 'sess-helper-run-7',
  turnId: 'turn-001',
  expiresAt: Date.now() + 300_000,
}

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
  vi.clearAllMocks()
})

describe('U13/FR-034 — the approval modal attributes the acting helper/run', () => {
  it('names the acting execution and exposes an Open control to its run session', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [HELPER_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    // One approval → exactly one card (BDD-10.2 "no duplicate queue/card").
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    // The acting execution is reachable from the modal (FR-034
    // "acting execution name/Open").
    const open = screen.getByTestId('approval-open-run')
    expect(open).toBeInTheDocument()
    expect(open).toHaveAttribute('data-session-id', 'sess-helper-run-7')
  })

  it('still names the acting agent (positive control — unchanged behaviour)', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [HELPER_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    const dialog = screen.getAllByRole('dialog')[0]
    // The agent id is the only name available on the wire today; the modal
    // falls back to it when no cached/fetched agent name resolves.
    expect(within(dialog).getByText('agent-helper')).toBeInTheDocument()
  })
})

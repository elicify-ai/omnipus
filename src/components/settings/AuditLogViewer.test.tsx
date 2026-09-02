// AuditLogViewer — G4 chain-integrity badge tests.
//
// The viewer wraps GET /api/v1/audit-log (now an AuditLogResponse envelope with
// a chain_status). These tests assert the header badge reflects the HMAC chain
// verification result: valid / broken (+ index) / unknown.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { AuditLogViewer } from './AuditLogViewer'
import { fetchAuditLog } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAuditLog: vi.fn() }
})

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

// ResizeObserver / scrollIntoView stubs — required by cmdk inside the
// SmartSelect popover (event-type filter has >5 items, so it uses the
// searchable cmdk-based variant). Same pattern as ModelPicker.test.tsx.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

function renderViewer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AuditLogViewer open onOpenChange={() => {}} />
    </QueryClientProvider>,
  )
}

describe('AuditLogViewer — chain-integrity badge (G4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows "Chain verified" when chain_status is valid', async () => {
    vi.mocked(fetchAuditLog).mockResolvedValue({ entries: [], chain_status: 'valid' } as never)
    renderViewer()
    await waitFor(() => {
      expect(screen.getByTestId('audit-chain-status')).toHaveTextContent(/verified/i)
    })
  })

  it('shows "Chain broken" with the broken index when chain_status is broken', async () => {
    vi.mocked(fetchAuditLog).mockResolvedValue({
      entries: [],
      chain_status: 'broken',
      chain_broken_index: 7,
    } as never)
    renderViewer()
    await waitFor(() => {
      const badge = screen.getByTestId('audit-chain-status')
      expect(badge).toHaveTextContent(/broken/i)
      expect(badge).toHaveTextContent(/7/)
    })
  })

  it('shows "Chain not checked" when chain_status is unknown', async () => {
    vi.mocked(fetchAuditLog).mockResolvedValue({ entries: [], chain_status: 'unknown' } as never)
    renderViewer()
    await waitFor(() => {
      expect(screen.getByTestId('audit-chain-status')).toHaveTextContent(/not checked/i)
    })
  })
})

// D20: security_setting_change rows (pkg/audit.SecurityChangeRecord —
// actor/resource/old_value/new_value) were previously invisible: no expand
// arrow, no rendered detail, and no filter option. Write side (audit) is
// already correct — this is purely a read-side rendering gap.
describe('AuditLogViewer — security_setting_change rendering (D20)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  const securityChangeEntry = {
    timestamp: '2026-07-28T00:01:00Z',
    event: 'security_setting_change',
    actor: 'admin',
    resource: 'gateway.god_mode',
    old_value: { enabled: false, god_mode_allowed: false },
    new_value: { enabled: true, god_mode_allowed: true, restart_required: true },
  }

  it('renders an expand arrow for a security_setting_change record even with no parameters/details/command/policy_rule', async () => {
    vi.mocked(fetchAuditLog).mockResolvedValue({
      entries: [securityChangeEntry],
      chain_status: 'unknown',
    } as never)
    renderViewer()
    await waitFor(() => screen.getByText('security_setting_change'))

    // Must be expandable purely because actor/resource are present — none of
    // parameters/details/command/policy_rule are set on this entry.
    expect(
      screen.getByRole('button', { name: /show details for this security_setting_change entry/i }),
    ).toBeInTheDocument()
  })

  it('expands to show actor, resource, old_value, and new_value', async () => {
    vi.mocked(fetchAuditLog).mockResolvedValue({
      entries: [securityChangeEntry],
      chain_status: 'unknown',
    } as never)
    renderViewer()
    await waitFor(() => screen.getByText('security_setting_change'))

    fireEvent.click(screen.getByRole('button', { name: /show details/i }))

    await waitFor(() => {
      expect(screen.getByText('Actor')).toBeInTheDocument()
    })
    expect(screen.getByText('admin')).toBeInTheDocument()
    expect(screen.getByText('Resource')).toBeInTheDocument()
    expect(screen.getByText('gateway.god_mode')).toBeInTheDocument()
    expect(screen.getByText('Old Value')).toBeInTheDocument()
    expect(screen.getByText('New Value')).toBeInTheDocument()
    // JsonBlock stringifies the redacted old/new value payloads.
    expect(screen.getByText(/"god_mode_allowed": false/)).toBeInTheDocument()
    expect(screen.getByText(/"restart_required": true/)).toBeInTheDocument()
  })

  it('includes security_setting_change in the event-type filter and filters by it', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchAuditLog).mockResolvedValue({
      entries: [
        { timestamp: '2026-07-28T00:00:00Z', event: 'tool_call', decision: 'allow', tool: 'bash' },
        securityChangeEntry,
      ],
      chain_status: 'unknown',
    } as never)
    renderViewer()
    await waitFor(() => screen.getByText('tool_call'))

    await user.click(screen.getByRole('button', { name: 'Event type filter' }))
    const option = await screen.findByRole('option', { name: 'security_setting_change' })
    await user.click(option)

    await waitFor(() => {
      expect(screen.queryByText('tool_call')).not.toBeInTheDocument()
    })
    // "security_setting_change" now appears twice: the filter trigger's own
    // selected-value label, and the surviving row's event badge.
    expect(screen.getAllByText('security_setting_change').length).toBeGreaterThanOrEqual(1)
  })

  // The event-type filter used to be a hardcoded ten-name list that predates
  // every dot-separated event name pkg/audit emits (over fifty of them). An
  // operator could therefore SEE a browser record in the table and have no way
  // to isolate it — a filter that silently omits most of the vocabulary is a
  // control that lies about its own coverage. The option list is now derived
  // from the records actually loaded, unioned with the original flat families.
  it('offers a filter option for a dotted event name that is present in the log', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchAuditLog).mockResolvedValue({
      entries: [
        { timestamp: '2026-09-02T10:00:00Z', event: 'tool_call', decision: 'allow', tool: 'bash' },
        { timestamp: '2026-09-02T10:00:03Z', event: 'browser.live.control_taken', decision: 'allow', agent_id: 'ray' },
      ],
      chain_status: 'unknown',
    } as never)
    renderViewer()
    await waitFor(() => screen.getByText('tool_call'))

    await user.click(screen.getByRole('button', { name: 'Event type filter' }))
    const option = await screen.findByRole('option', { name: 'browser.live.control_taken' })
    await user.click(option)

    // Selecting it must actually isolate the record, not just exist.
    await waitFor(() => {
      expect(screen.queryByText('tool_call')).not.toBeInTheDocument()
    })
    expect(screen.getAllByText('browser.live.control_taken').length).toBeGreaterThanOrEqual(1)
  })
})

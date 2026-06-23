// AuditLogViewer — G4 chain-integrity badge tests.
//
// The viewer wraps GET /api/v1/audit-log (now an AuditLogResponse envelope with
// a chain_status). These tests assert the header badge reflects the HMAC chain
// verification result: valid / broken (+ index) / unknown.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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

/**
 * ExecProxyStatusCard — F2 reconciliation, Settings FR-010 and FR-020.
 * The proxy is now a draft on the common Save bar, not an independent
 * confirmation/toast mutation. Preserve the three original failure inputs,
 * but assert their specified outcomes and retained drafts at the new boundary.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchExecProxyStatus: vi.fn(), updateConfig: vi.fn() }
})
const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({ useUiStore: vi.fn(() => ({ addToast: mockAddToast })) }))

import { ApiError, fetchExecProxyStatus, updateConfig } from '@/lib/api'
import type { ExecProxyStatus } from '@/lib/api'
import { ExecProxyStatusCard } from './ExecProxyStatusCard'

function renderCard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><ExecProxyStatusCard /></QueryClientProvider>)
}
const baseStatus: ExecProxyStatus = { enabled: false, running: false }

async function toggleAndSave() {
  await waitFor(() => expect(screen.getByRole('switch')).toBeEnabled(), { timeout: 5000 })
  fireEvent.click(screen.getByRole('switch'))
  expect(screen.queryByRole('dialog'), 'F2 / FR-010: no separate proxy confirmation').not.toBeInTheDocument()
  expect(updateConfig, 'F2 / FR-010: the switch alone never writes').not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(updateConfig).toHaveBeenCalledExactlyOnceWith({ tools: { exec: { enable_proxy: true } } }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchExecProxyStatus).mockResolvedValue(baseStatus)
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), window.location.origin).pathname
    if (path !== '/api/v1/config/pending-restart') throw new Error(`Unexpected proxy reconciliation read: ${path}`)
    return new Response('[]', { headers: { 'Content-Type': 'application/json' } })
  }))
})
afterEach(() => { vi.unstubAllGlobals() })

describe('ExecProxyStatusCard — common Save failure outcomes (F2 / FR-020)', () => {
  it('a 500 with unreadable saved state stays unknown with Retry and keeps the checked draft', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new ApiError(500, undefined))
    renderCard()
    await waitFor(() => expect(screen.getByRole('switch')).toBeEnabled())
    vi.mocked(fetchExecProxyStatus).mockRejectedValue(new Error('proxy saved-state read unavailable'))
    await toggleAndSave()
    expect(await screen.findByText('1 change may not have been saved')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(screen.getByRole('switch')).toBeChecked()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('a 403 refusal shows ApiError.userMessage under the setting and retains the draft', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new ApiError(403, undefined))
    renderCard()
    await toggleAndSave()
    expect(await screen.findByText("You don't have permission to perform this action.")).toBeInTheDocument()
    expect(screen.getByText('1 change could not be saved')).toBeInTheDocument()
    expect(screen.getByRole('switch')).toBeChecked()
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('a plain Error is reconciled against saved state, never a false success or a reset draft', async () => {
    vi.mocked(updateConfig).mockRejectedValue(new Error('proxy bind failed: address already in use'))
    renderCard()
    await toggleAndSave()
    expect(await screen.findByText('These changes were not saved. Save them again.')).toBeInTheDocument()
    expect(screen.getByText('1 change could not be saved')).toBeInTheDocument()
    expect(screen.getByRole('switch')).toBeChecked()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(mockAddToast).not.toHaveBeenCalled()
  })
})

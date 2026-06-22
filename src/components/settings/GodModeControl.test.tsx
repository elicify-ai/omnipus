/**
 * GodModeControl.test.tsx — O14 god-mode switch.
 *
 * Covers:
 *  - the toggle reflects live state from GodModeStatus ({ enabled, available })
 *  - flipping the toggle opens the step-up (re-auth) dialog and does NOT call
 *    the god-mode endpoint until the password is confirmed
 *  - confirming the password replays the minted consent token into setGodMode
 *  - the unavailable build disables the toggle with an explanatory note
 *  - the active-state banner renders only when god-mode is on
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchGodMode: vi.fn(),
    setGodMode: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { GodModeControl, GodModeActiveBanner } from './GodModeControl'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderControl() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <GodModeControl />
    </QueryClientProvider>,
  )
}

function renderBanner() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <GodModeActiveBanner />
    </QueryClientProvider>,
  )
}

// GodModeStatus = { enabled, available }
const STATE_OFF = { enabled: false, available: true }
const STATE_ON = { enabled: true, available: true }
const STATE_UNAVAILABLE = { enabled: false, available: false }

beforeEach(() => {
  vi.clearAllMocks()
})

describe('GodModeControl', () => {
  it('renders the toggle off when god-mode is disabled', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-toggle')).toHaveAttribute('aria-checked', 'false')
    })
  })

  it('renders the toggle on when god-mode is enabled', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_ON)
    renderControl()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-toggle')).toHaveAttribute('aria-checked', 'true')
    })
  })

  it('flipping the toggle opens the step-up dialog and does NOT call setGodMode yet', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(api.setGodMode).not.toHaveBeenCalled()
  })

  it('replays the minted consent token into setGodMode after password confirm', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.setGodMode).mockResolvedValue({ enabled: true, available: true })

    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.setGodMode).toHaveBeenCalledWith(true, 'reauth_tok')
    })
  })

  it('disables the toggle and shows a note when god-mode is unavailable in the build', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_UNAVAILABLE)
    renderControl()
    // The note only renders once the query has settled (available=false &&
    // !isLoading); wait for it, then assert the toggle is disabled.
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-unavailable-note')).toBeInTheDocument()
    })
    expect(screen.getByTestId('god-mode-toggle')).toBeDisabled()
  })

  it('does not call setGodMode if the step-up dialog is cancelled', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    renderControl()
    await waitFor(() => expect(screen.getByTestId('god-mode-toggle')).toBeEnabled())

    fireEvent.click(screen.getByTestId('god-mode-toggle'))
    await waitFor(() => screen.getByTestId('reauth-confirm'))

    fireEvent.click(screen.getByRole('button', { name: /Cancel/i }))
    await waitFor(() => {
      expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    })
    expect(api.setGodMode).not.toHaveBeenCalled()
  })
})

describe('GodModeActiveBanner', () => {
  it('renders the banner when god-mode is active', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_ON)
    renderBanner()
    await waitFor(() => {
      expect(screen.getByTestId('god-mode-active-banner')).toBeInTheDocument()
    })
    expect(screen.getByText(/God-mode is active/i)).toBeInTheDocument()
  })

  it('renders nothing when god-mode is off', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(STATE_OFF)
    const { container } = renderBanner()
    // Give the query a tick to settle.
    await waitFor(() => {
      expect(api.fetchGodMode).toHaveBeenCalled()
    })
    expect(screen.queryByTestId('god-mode-active-banner')).not.toBeInTheDocument()
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

// Mock the sidebar store so we can verify toggle is called and control isOpen
// (aria-expanded reflects it — see the aria-expanded describe block below)
// and isPinned (the God Mode dot's sidebar-hidden condition).
const mockToggle = vi.fn()
let mockIsOpen = false
let mockIsPinned = false
vi.mock('@/store/sidebar', () => ({
  useSidebarStore: vi.fn((selector: (s: { toggle: () => void; isOpen: boolean; isPinned: boolean; close: () => void }) => unknown) =>
    selector({ toggle: mockToggle, isOpen: mockIsOpen, isPinned: mockIsPinned, close: vi.fn() })
  ),
  SIDEBAR_PIN_BREAKPOINT: 1024,
}))

// jsdom ships no window.matchMedia — useGodModeSidebarDot needs it for the
// pin breakpoint. Controllable per test via mockMatchMediaMatches (default
// false: viewport too narrow to pin, so only isOpen decides visibility).
let mockMatchMediaMatches = false
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: mockMatchMediaMatches,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

// The God Mode dot's status hook queries god-mode (gated on app-state) —
// mock both so no real network call is attempted from jsdom and each test
// can pin the live status it needs.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    fetchGodMode: vi.fn(),
  }
})

import * as api from '@/lib/api'

// Import after mocks are in place
import { ScreenHeader } from './ScreenHeader'

function makeWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

function renderHeader(props: { title: string }) {
  return render(<ScreenHeader {...props} />, { wrapper: makeWrapper() })
}

const APP_STATE_OK = {
  onboarding_complete: true,
  dev_mode_bypass: false,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as never

const GOD_MODE_OFF = { enabled: false, available: true, supported: true, persisted: false }
const GOD_MODE_ON = { enabled: true, available: true, supported: true, persisted: true }

beforeEach(() => {
  vi.clearAllMocks()
  mockIsOpen = false
  mockIsPinned = false
  mockMatchMediaMatches = false
  // Re-pin defaults: an override from an earlier test would otherwise leak
  // in (clearAllMocks clears calls, not implementations).
  vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE_OK)
  vi.mocked(api.fetchGodMode).mockResolvedValue(GOD_MODE_OFF)
})

describe('ScreenHeader', () => {
  it('renders the title', () => {
    renderHeader({ title: 'Agents' })
    expect(screen.getByText('Agents')).toBeTruthy()
  })

  it('renders the hamburger button with accessible label', () => {
    renderHeader({ title: 'Settings' })
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    expect(btn).toBeTruthy()
  })

  it('calls sidebar toggle when hamburger is clicked', () => {
    renderHeader({ title: 'Skills & Tools' })
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    fireEvent.click(btn)
    expect(mockToggle).toHaveBeenCalledTimes(1)
  })

  it('renders optional actions slot when provided', () => {
    render(
      <ScreenHeader
        title="Usage"
        actions={<button type="button">Export</button>}
      />,
      { wrapper: makeWrapper() },
    )
    expect(screen.getByRole('button', { name: 'Export' })).toBeTruthy()
  })

  it('does not render actions slot when omitted', () => {
    renderHeader({ title: 'Profile' })
    expect(screen.queryByTestId('screen-header-actions')).toBeNull()
  })

  it('renders title prop faithfully for multiple invocations', () => {
    const { rerender } = renderHeader({ title: 'Connectors' })
    expect(screen.getByText('Connectors')).toBeTruthy()
    rerender(<ScreenHeader title="Profile" />)
    expect(screen.getByText('Profile')).toBeTruthy()
  })
})

// BDD: Given the sidebar store's isOpen state, When ScreenHeader renders its
// hamburger, Then aria-expanded reflects it — screen reader users otherwise
// have no way to know whether activating the button opens or closes the
// drawer. Traces to: src/components/layout/ScreenHeader.tsx hamburger button.
describe('ScreenHeader — hamburger aria-expanded', () => {
  it('is "false" when the sidebar is closed', () => {
    mockIsOpen = false
    renderHeader({ title: 'Agents' })
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    expect(btn.getAttribute('aria-expanded')).toBe('false')
  })

  it('is "true" when the sidebar is open — differentiation from the closed state', () => {
    mockIsOpen = true
    renderHeader({ title: 'Agents' })
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    expect(btn.getAttribute('aria-expanded')).toBe('true')
  })
})

// ── God Mode dot (founder decision 2026-09-25) ───────────────────────────────
//
// While god-mode is on AND the sidebar (and its pill) is off screen, the
// hamburger that opens the sidebar carries a small red dot, and its
// accessible name says why. The dot is the pill's stand-in for the hidden
// sidebar state only — it must NOT render when god-mode is off, when the
// status is merely unknown (the pill's warning variant owns that signal),
// or when the sidebar is on screen (overlay open, or pinned at a wide
// enough viewport). See src/components/layout/GodModeIndicators.tsx.
describe('ScreenHeader — God Mode dot (2026-09-25)', () => {
  it('shows the dot and a God Mode accessible name when god-mode is on and the sidebar is hidden', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(GOD_MODE_ON)
    renderHeader({ title: 'Agents' })

    await waitFor(() => {
      expect(screen.getByTestId('sidebar-god-mode-dot')).toBeInTheDocument()
    })
    expect(
      screen.getByRole('button', { name: 'Toggle navigation sidebar — God Mode is on' })
    ).toBeInTheDocument()
  })

  it('shows no dot when god-mode is off', async () => {
    renderHeader({ title: 'Agents' })
    await waitFor(() => expect(api.fetchGodMode).toHaveBeenCalled())
    expect(screen.queryByTestId('sidebar-god-mode-dot')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Toggle navigation sidebar — God Mode is on' })
    ).not.toBeInTheDocument()
  })

  it('shows no dot when the status is unknown — the pill\'s warning variant owns that signal, not the dot', async () => {
    vi.mocked(api.fetchGodMode).mockRejectedValue(new Error('network error'))
    renderHeader({ title: 'Agents' })
    await waitFor(() => expect(api.fetchGodMode).toHaveBeenCalled())
    expect(screen.queryByTestId('sidebar-god-mode-dot')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Toggle navigation sidebar — God Mode is on' })
    ).not.toBeInTheDocument()
  })

  it('shows no dot while the sidebar overlay is open — the pill is already on screen', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(GOD_MODE_ON)
    mockIsOpen = true
    renderHeader({ title: 'Agents' })

    await waitFor(() => expect(api.fetchGodMode).toHaveBeenCalled())
    expect(screen.queryByTestId('sidebar-god-mode-dot')).not.toBeInTheDocument()
  })

  it('shows no dot when the sidebar is pinned and the viewport allows pinning', async () => {
    vi.mocked(api.fetchGodMode).mockResolvedValue(GOD_MODE_ON)
    mockIsPinned = true
    mockMatchMediaMatches = true
    renderHeader({ title: 'Agents' })

    await waitFor(() => expect(api.fetchGodMode).toHaveBeenCalled())
    expect(screen.queryByTestId('sidebar-god-mode-dot')).not.toBeInTheDocument()
  })
})

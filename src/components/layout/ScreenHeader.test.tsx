import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

// Mock the sidebar store so we can verify toggle is called and control isOpen
// (aria-expanded reflects it — see the aria-expanded describe block below).
// The God Mode dot that used to mark this hamburger is deleted (founder
// decision 2026-09-25 revision 2 — one app-wide corner dot from AppShell
// replaces the per-button dots); its matchMedia/api mocks went with it.
const mockOpen = vi.fn()
let mockIsOpen = false
vi.mock('@/store/sidebar', () => ({
  useSidebarStore: vi.fn((selector: (s: { open: () => void; pin: () => void; isOpen: boolean; isPinned: boolean }) => unknown) =>
    selector({ open: mockOpen, pin: vi.fn(), isOpen: mockIsOpen, isPinned: false })
  ),
  SIDEBAR_PIN_BREAKPOINT: 1024,
}))

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

beforeEach(() => {
  vi.clearAllMocks()
  mockIsOpen = false
})

describe('ScreenHeader', () => {
  it('renders the title', () => {
    renderHeader({ title: 'Agents' })
    expect(screen.getByText('Agents')).toBeTruthy()
  })

  it('renders Show sidebar while the sidebar is hidden', () => {
    renderHeader({ title: 'Settings' })
    expect(screen.getByRole('button', { name: 'Show sidebar' })).toBeTruthy()
  })

  it('hides the header button while the sidebar is open', () => {
    mockIsOpen = true
    renderHeader({ title: 'Settings' })
    expect(screen.queryByRole('button', { name: 'Show sidebar' })).toBeNull()
  })

  it('opens the sidebar when Show sidebar is clicked', () => {
    renderHeader({ title: 'Skills & Tools' })
    fireEvent.click(screen.getByRole('button', { name: 'Show sidebar' }))
    expect(mockOpen).toHaveBeenCalledTimes(1)
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

  // Founder decision 2026-09-25 revision 2: the per-hamburger God Mode dot
  // is deleted — AppShell renders ONE corner indicator app-wide instead (see
  // AppShell.test.tsx). Pin the removal: even with god-mode on and the
  // sidebar hidden, this header renders no dot and its hamburger keeps the
  // plain accessible name.
  it('renders no God Mode dot on the hamburger — replaced by the app-wide corner dot', async () => {
    renderHeader({ title: 'Agents' })
    expect(screen.queryByTestId('sidebar-god-mode-dot')).toBeNull()
    expect(
      screen.queryByRole('button', { name: 'Toggle navigation sidebar — God Mode is on' })
    ).toBeNull()
  })
})

// BDD: While the sidebar is hidden, Show sidebar reports aria-expanded="false".
// While the sidebar is on screen, that button is not in the header — Hide
// sidebar lives inside the sidebar. Traces to: SidebarShowButton.
describe('ScreenHeader — hamburger aria-expanded', () => {
  it('is "false" when the sidebar is closed', () => {
    mockIsOpen = false
    renderHeader({ title: 'Agents' })
    const btn = screen.getByRole('button', { name: 'Show sidebar' })
    expect(btn.getAttribute('aria-expanded')).toBe('false')
  })

  it('is absent from the header when the sidebar is open — the Hide control lives in the sidebar', () => {
    mockIsOpen = true
    renderHeader({ title: 'Agents' })
    expect(screen.queryByRole('button', { name: 'Show sidebar' })).toBeNull()
  })
})

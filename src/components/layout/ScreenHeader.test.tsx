import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

// Mock the sidebar store so we can verify toggle is called and control isOpen
// (aria-expanded reflects it — see the aria-expanded describe block below).
const mockToggle = vi.fn()
let mockIsOpen = false
vi.mock('@/store/sidebar', () => ({
  useSidebarStore: vi.fn((selector: (s: { toggle: () => void; isOpen: boolean }) => unknown) =>
    selector({ toggle: mockToggle, isOpen: mockIsOpen })
  ),
}))

// Import after mocks are in place
import { ScreenHeader } from './ScreenHeader'

beforeEach(() => {
  vi.clearAllMocks()
  mockIsOpen = false
})

describe('ScreenHeader', () => {
  it('renders the title', () => {
    render(<ScreenHeader title="Agents" />)
    expect(screen.getByText('Agents')).toBeTruthy()
  })

  it('renders the hamburger button with accessible label', () => {
    render(<ScreenHeader title="Settings" />)
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    expect(btn).toBeTruthy()
  })

  it('calls sidebar toggle when hamburger is clicked', () => {
    render(<ScreenHeader title="Skills & Tools" />)
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
    )
    expect(screen.getByRole('button', { name: 'Export' })).toBeTruthy()
  })

  it('does not render actions slot when omitted', () => {
    render(<ScreenHeader title="Profile" />)
    expect(screen.queryByTestId('screen-header-actions')).toBeNull()
  })

  it('renders title prop faithfully for multiple invocations', () => {
    const { rerender } = render(<ScreenHeader title="Connectors" />)
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
    render(<ScreenHeader title="Agents" />)
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    expect(btn.getAttribute('aria-expanded')).toBe('false')
  })

  it('is "true" when the sidebar is open — differentiation from the closed state', () => {
    mockIsOpen = true
    render(<ScreenHeader title="Agents" />)
    const btn = screen.getByRole('button', { name: /toggle navigation sidebar/i })
    expect(btn.getAttribute('aria-expanded')).toBe('true')
  })
})

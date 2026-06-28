import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

// Mock the sidebar store so we can verify toggle is called
const mockToggle = vi.fn()
vi.mock('@/store/sidebar', () => ({
  useSidebarStore: vi.fn((selector: (s: { toggle: () => void }) => unknown) =>
    selector({ toggle: mockToggle })
  ),
}))

// Import after mocks are in place
import { ScreenHeader } from './ScreenHeader'

beforeEach(() => {
  vi.clearAllMocks()
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

/**
 * MediaActionToolbar.test.tsx — unit tests for the reusable media action toolbar.
 *
 * Covers:
 *   - Renders one button per action with the correct aria-label.
 *   - Clicking an action calls its onClick handler.
 *   - A successful async action with transientLabel shows the Check/done state.
 *   - A failing action (throws) calls addToast with variant:'error'.
 *   - Renders nothing when actions array is empty.
 *   - Both variants render without error (smoke tests).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { Camera } from '@phosphor-icons/react'

// vi.hoisted runs before vi.mock factories — it is the correct way to define
// shared mocks that a hoisted factory closure needs to capture.
const { mockAddToast } = vi.hoisted(() => ({
  mockAddToast: vi.fn(),
}))

// Mock the ui store so addToast is observable. The factory closes over
// mockAddToast which is guaranteed to be defined (hoisted above this call).
vi.mock('@/store/ui', () => ({
  useUiStore: {
    getState: () => ({ addToast: mockAddToast }),
  },
}))

// Import AFTER vi.mock (hoisting ensures the mock is in place)
import { MediaActionToolbar, type MediaAction } from './MediaActionToolbar'

function makeAction(overrides: Partial<MediaAction> = {}): MediaAction {
  return {
    icon: <Camera size={14} />,
    label: 'Copy image',
    onClick: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  }
}

beforeEach(() => {
  mockAddToast.mockReset()
})

// ── Renders buttons ───────────────────────────────────────────────────────────

describe('MediaActionToolbar — button rendering', () => {
  it('renders one button per action with the correct aria-label', () => {
    const actions = [
      makeAction({ label: 'Copy image' }),
      makeAction({ label: 'Download image' }),
      makeAction({ label: 'Share image' }),
    ]
    render(<MediaActionToolbar actions={actions} />)

    expect(screen.getByRole('button', { name: 'Copy image' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download image' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Share image' })).toBeInTheDocument()
  })

  it('renders nothing when actions array is empty', () => {
    const { container } = render(<MediaActionToolbar actions={[]} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders group role (not toolbar — no roving-tabindex is implemented) with aria-label', () => {
    render(<MediaActionToolbar actions={[makeAction()]} />)
    expect(screen.getByRole('group', { name: 'Media actions' })).toBeInTheDocument()
    expect(screen.queryByRole('toolbar', { name: 'Media actions' })).toBeNull()
  })
})

// ── onClick invocation ────────────────────────────────────────────────────────

describe('MediaActionToolbar — onClick', () => {
  it('calls the action onClick when button is clicked', async () => {
    const onClick = vi.fn().mockResolvedValue(undefined)
    render(<MediaActionToolbar actions={[makeAction({ label: 'Copy image', onClick })]} />)

    fireEvent.click(screen.getByRole('button', { name: 'Copy image' }))
    await waitFor(() => expect(onClick).toHaveBeenCalledTimes(1))
  })

  it('calls onClick for each of multiple distinct buttons', async () => {
    const onClick1 = vi.fn().mockResolvedValue(undefined)
    const onClick2 = vi.fn().mockResolvedValue(undefined)
    render(
      <MediaActionToolbar
        actions={[
          makeAction({ label: 'Action A', onClick: onClick1 }),
          makeAction({ label: 'Action B', onClick: onClick2 }),
        ]}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Action A' }))
    await waitFor(() => expect(onClick1).toHaveBeenCalledTimes(1))
    expect(onClick2).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Action B' }))
    await waitFor(() => expect(onClick2).toHaveBeenCalledTimes(1))
  })
})

// ── Transient "done" state ────────────────────────────────────────────────────

describe('MediaActionToolbar — transient done state', () => {
  it('shows done state after a successful action with transientLabel', async () => {
    vi.useFakeTimers()

    const action = makeAction({
      label: 'Copy image',
      transientLabel: 'Copied!',
      onClick: vi.fn().mockResolvedValue(undefined),
    })
    render(<MediaActionToolbar actions={[action]} />)

    const btn = screen.getByRole('button', { name: 'Copy image' })

    // Click and let the promise resolve
    await act(async () => {
      fireEvent.click(btn)
      // Drain microtasks so the async handleClick can run to completion
      await Promise.resolve()
      await Promise.resolve()
    })

    // Button should now show the transient "done" state
    expect(screen.getByRole('button', { name: 'Copied!' })).toBeInTheDocument()

    // Advance fake timers past the 1.5 s reset
    await act(async () => {
      vi.advanceTimersByTime(1600)
    })

    expect(screen.getByRole('button', { name: 'Copy image' })).toBeInTheDocument()

    vi.useRealTimers()
  })
})

// ── Error toast on failure ────────────────────────────────────────────────────

describe('MediaActionToolbar — error toast', () => {
  it('calls addToast with variant:error when the action throws', async () => {
    const onClick = vi.fn().mockRejectedValue(new Error('clipboard denied'))
    render(<MediaActionToolbar actions={[makeAction({ label: 'Copy image', onClick })]} />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy image' }))
    })

    expect(mockAddToast).toHaveBeenCalledTimes(1)
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error', message: 'clipboard denied' }),
    )
  })

  it('calls addToast with a fallback message for non-Error throws', async () => {
    const onClick = vi.fn().mockRejectedValue('some string error')
    render(<MediaActionToolbar actions={[makeAction({ label: 'Fail', onClick })]} />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Fail' }))
    })

    expect(mockAddToast).toHaveBeenCalledTimes(1)
    const [toast] = mockAddToast.mock.calls[0] as [{ message: string; variant: string }]
    expect(toast.variant).toBe('error')
    expect(typeof toast.message).toBe('string')
    expect(toast.message.length).toBeGreaterThan(0)
  })
})

// ── Variant smoke tests ───────────────────────────────────────────────────────

describe('MediaActionToolbar — variants', () => {
  it("renders without crashing in 'overlay' variant", () => {
    render(<MediaActionToolbar actions={[makeAction()]} variant="overlay" />)
    expect(screen.getByRole('group')).toBeInTheDocument()
  })

  it("renders without crashing in 'bar' variant", () => {
    render(<MediaActionToolbar actions={[makeAction()]} variant="bar" />)
    expect(screen.getByRole('group')).toBeInTheDocument()
  })
})

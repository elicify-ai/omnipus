/**
 * ListGroup — W2-1 contract §2.2 (R35).
 *
 * RED on 69aa07d9a: `src/components/ui/list-group.tsx` does not exist yet
 * (contract §2.2 names it as a new file) — fails at import for the right
 * reason.
 *
 * jsdom notes: glyph identity (CaretRight vs CaretDown) is pixel data — the
 * screenshot gate (contract G8) owns it; here the caret is asserted as an
 * aria-hidden svg whose presence flips with expansion. The 44px coarse
 * growth lives on DisclosureRow (already published and tested) and the
 * pointer manifest kind.
 */
import userEvent from '@testing-library/user-event'
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Button } from './button'
import { ListGroup } from './list-group'

// Overrides are Partial: `label` and `children` stay REQUIRED on
// ListGroupProps (weakening them would loosen the contract API — K6 says
// fix the test-side type, never the component prop).
function renderGroup(overrides: Partial<Parameters<typeof ListGroup>[0]> = {}) {
  const props = {
    label: 'Telegram',
    children: <div data-testid="group-child">Channel one</div>,
    ...overrides,
  }
  return render(<ListGroup {...props} />)
}

describe('ListGroup — collapsed by default (R35 "all groups start collapsed")', () => {
  it('renders a list-group section whose toggle starts aria-expanded=false with the body hidden and children unmounted', () => {
    const { container } = renderGroup()
    expect(container.querySelector('section[data-slot="list-group"]')).not.toBeNull()
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    const body = document.getElementById(toggle.getAttribute('aria-controls')!)
    expect(body).not.toBeNull()
    expect(body).toHaveAttribute('hidden')
    expect(screen.queryByTestId('group-child')).not.toBeInTheDocument()
  })

  it('expands on click: aria-expanded=true, body un-hidden, children mounted', async () => {
    const user = userEvent.setup()
    renderGroup()
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const body = document.getElementById(toggle.getAttribute('aria-controls')!)
    expect(body).not.toHaveAttribute('hidden')
    expect(screen.getByTestId('group-child')).toBeInTheDocument()
  })

  it('collapses again: children are unmounted, not merely hidden', async () => {
    const user = userEvent.setup()
    renderGroup({ defaultExpanded: true })
    expect(screen.getByTestId('group-child')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Telegram' }))
    expect(screen.queryByTestId('group-child')).not.toBeInTheDocument()
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    const body = document.getElementById(toggle.getAttribute('aria-controls')!)
    expect(body).toHaveAttribute('hidden')
  })

  it('toggles with Enter and Space (native button keyboard activation)', async () => {
    const user = userEvent.setup()
    renderGroup()
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    toggle.focus()
    await user.keyboard('{Enter}')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await user.keyboard(' ')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })
})

describe('ListGroup — header contract (R35)', () => {
  it('the toggle’s accessible name is the label plus the count, e.g. "Telegram 3"', () => {
    renderGroup({ count: 3 })
    expect(screen.getByRole('button', { name: 'Telegram 3' })).toBeInTheDocument()
  })

  it('without a count the accessible name is exactly the label (no count text renders)', () => {
    const { container } = renderGroup()
    // Exact string match: any appended count text would break the name.
    expect(screen.getByRole('button', { name: 'Telegram' })).toBeInTheDocument()
    expect(container.textContent).not.toContain('3')
  })

  it('the label is uppercase-styled secondary semibold with label tracking; the count is muted and not uppercased', () => {
    renderGroup({ count: 3 })
    const toggle = screen.getByRole('button', { name: 'Telegram 3' })
    const label = within(toggle).getByText('Telegram')
    expect(label).toHaveClass('uppercase')
    expect(label).toHaveClass('font-semibold')
    expect(label).toHaveClass('tracking-[var(--type-label-letter-spacing)]')
    expect(label).toHaveClass('text-[var(--color-secondary)]')
    const count = within(toggle).getByText('3')
    expect(count).not.toHaveClass('uppercase')
    expect(count).toHaveClass('text-[var(--color-muted)]')
  })

  it('has no "Show all" text anywhere in the header', () => {
    const { container } = renderGroup({ count: 3 })
    expect(container.textContent).not.toMatch(/show all/i)
  })

  it('carries an aria-hidden leading caret in both states', async () => {
    const user = userEvent.setup()
    renderGroup()
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    const caretWhenCollapsed = toggle.querySelector('svg[aria-hidden="true"]')
    expect(caretWhenCollapsed).not.toBeNull()
    await user.click(toggle)
    const caretWhenExpanded = toggle.querySelector('svg[aria-hidden="true"]')
    expect(caretWhenExpanded).not.toBeNull()
    // Glyph direction (CaretRight/CaretDown) is the screenshot gate's job.
  })
})

describe('ListGroup — body identity', () => {
  it('uses "<id>-body" for the body and aria-controls when an id is given', () => {
    renderGroup({ id: 'groups-x' })
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    expect(toggle).toHaveAttribute('aria-controls', 'groups-x-body')
    expect(document.getElementById('groups-x-body')).not.toBeNull()
  })

  it('generates the body id with useId and wires aria-controls to it when no id is given', () => {
    renderGroup()
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    const controls = toggle.getAttribute('aria-controls')
    expect(controls).toBeTruthy()
    expect(document.getElementById(controls!)).not.toBeNull()
  })
})

describe('ListGroup — actions are a sibling of the toggle, never inside it', () => {
  it('renders the action outside the toggle button and keeps a click on it from toggling the group', async () => {
    const user = userEvent.setup()
    const onExpandedChange = vi.fn()
    render(
      <ListGroup label="Telegram" onExpandedChange={onExpandedChange} actions={<Button type="button" data-testid="group-action">Rename</Button>}>
        <div data-testid="group-child">Channel one</div>
      </ListGroup>,
    )
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    const action = screen.getByTestId('group-action')
    expect(toggle.contains(action)).toBe(false)
    await user.click(action)
    expect(onExpandedChange).not.toHaveBeenCalled()
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })
})

describe('ListGroup — controlled expanded', () => {
  it('a user toggle only reports onExpandedChange; rendered state stays under the caller’s control', async () => {
    const user = userEvent.setup()
    const onExpandedChange = vi.fn()
    const { rerender } = renderGroup({ expanded: false, onExpandedChange })
    const toggle = screen.getByRole('button', { name: 'Telegram' })
    await user.click(toggle)
    expect(onExpandedChange).toHaveBeenCalledTimes(1)
    expect(onExpandedChange).toHaveBeenCalledWith(true)
    // Controlled override: still closed.
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('group-child')).not.toBeInTheDocument()
    // Caller opens it: children mount.
    rerender(
      <ListGroup label="Telegram" expanded onExpandedChange={onExpandedChange}>
        <div data-testid="group-child">Channel one</div>
      </ListGroup>,
    )
    expect(screen.getByRole('button', { name: 'Telegram' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('group-child')).toBeInTheDocument()
  })

  it('defaultExpanded opens the group initially when uncontrolled', () => {
    renderGroup({ defaultExpanded: true })
    expect(screen.getByRole('button', { name: 'Telegram' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('group-child')).toBeInTheDocument()
  })
})

describe('ListGroup — empty groups', () => {
  it('count === 0 still renders the group (hiding empty groups is the caller’s choice)', () => {
    const { container } = renderGroup({ count: 0 })
    expect(container.querySelector('section[data-slot="list-group"]')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Telegram' })).toBeInTheDocument()
  })
})

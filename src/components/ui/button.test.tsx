import type { FormEvent } from 'react'
import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Button } from './button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './dialog'

// test_button_default_variant_colors
// Traces to: wave0-brand-design-spec.md Scenario: Default button uses Forge Gold (US-2 AC1, FR-004)
describe('Button — default variant (Forge Gold)', () => {
  it('renders with Forge Gold background CSS variable class', () => {
    const { container } = render(<Button>Click me</Button>)
    const btn = container.querySelector('button')
    expect(btn).not.toBeNull()
    // The default variant uses bg-[var(--color-accent)] which maps to Forge Gold.
    expect(btn!.className).toContain('var(--color-accent)')
  })

  it('renders with Deep Space Black text class', () => {
    const { container } = render(<Button>Click me</Button>)
    const btn = container.querySelector('button')
    expect(btn!.className).toContain('var(--color-primary)')
  })

  it('renders button text', () => {
    render(<Button>Click me</Button>)
    expect(screen.getByRole('button')).toHaveTextContent('Click me')
  })

  it('default variant has no explicit variant class for destructive', () => {
    const { container } = render(<Button>Default</Button>)
    // Must NOT use the error color for default variant.
    expect(container.querySelector('button')!.className).not.toContain('var(--color-error)')
  })
})

// test_button_destructive_variant
// Traces to: wave0-brand-design-spec.md Scenario: Destructive button uses Ruby (US-2 AC2, FR-004)
describe('Button — destructive variant (Ruby / #EF4444)', () => {
  const luminance = (hex: string) => {
    const channels = [1, 3, 5].map((index) => Number.parseInt(hex.slice(index, index + 2), 16) / 255)
    const linear = channels.map((channel) => channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4)
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]
  }
  const contrast = (first: string, second: string) => {
    const values = [luminance(first), luminance(second)].sort((a, b) => b - a)
    return (values[0] + 0.05) / (values[1] + 0.05)
  }

  it('renders with error color CSS variable class', () => {
    const { container } = render(<Button variant="destructive">Delete</Button>)
    const btn = container.querySelector('button')
    expect(btn).not.toBeNull()
    // Destructive variant uses bg-[var(--color-error)] which maps to Ruby (#EF4444).
    expect(btn!.className).toContain('var(--color-error)')
  })

  it('does not use Forge Gold class for destructive variant', () => {
    const { container } = render(<Button variant="destructive">Delete</Button>)
    // Destructive must NOT use the accent (Forge Gold) background.
    expect(container.querySelector('button')!.className).not.toContain(
      'bg-[var(--color-accent)]'
    )
  })

  it('uses the accessible dark text and the accessible destructive hover token', () => {
    render(<Button variant="destructive">Delete</Button>)
    const button = screen.getByRole('button', { name: 'Delete' })
    expect(button.className).toContain('text-[var(--color-primary)]')
    expect(button.className).toContain('hover:bg-[var(--color-destructive-action-hover)]')
    expect(contrast('#EF4444', '#0A0A0B')).toBeCloseTo(5.25886, 5)
    expect(contrast('#EA2626', '#0A0A0B')).toBeCloseTo(4.52688, 5)
  })
})

describe('Button — disabled state', () => {
  it('is disabled when disabled prop is set', () => {
    render(<Button disabled>Disabled</Button>)
    expect(screen.getByRole('button')).toBeDisabled()
  })
})

describe('Button — forced-colors contract', () => {
  it('adds a system-color boundary without changing the ordinary variant colors', () => {
    render(<Button>Continue</Button>)
    const button = screen.getByRole('button', { name: 'Continue' })
    expect(button).toHaveClass(
      'forced-colors:border',
      'forced-colors:border-[ButtonText]',
      'forced-colors:bg-[ButtonFace]',
      'forced-colors:text-[ButtonText]',
      'forced-colors:[forced-color-adjust:none]',
    )
    expect(button).toHaveClass('bg-[var(--color-accent)]', 'text-[var(--color-primary)]')
  })
})

describe('Button — action contract', () => {
  it('defaults a native button to type=button without changing explicit submit', () => {
    const { rerender } = render(<form><Button>Cancel</Button></form>)
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveAttribute('type', 'button')
    rerender(<form><Button type="submit">Save</Button></form>)
    expect(screen.getByRole('button', { name: 'Save' })).toHaveAttribute('type', 'submit')
  })

  it('marks the action for a CSS hit region without changing its rendered dimensions', () => {
    render(<Button className="h-8 w-8">Action</Button>)
    const button = screen.getByRole('button', { name: 'Action' })
    expect(button).toHaveAttribute('data-ds-action')
    expect(button.className).not.toContain('min-h-[44px]')
    expect(button.className).not.toContain('min-w-[44px]')
  })

  it('marks pending actions busy and suppresses activation', () => {
    let activations = 0
    render(<Button actionState="pending" onClick={() => { activations += 1 }}>Save</Button>)
    const button = screen.getByRole('button', { name: 'Save' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(button).toHaveAttribute('data-action-state', 'pending')
    fireEvent.click(button)
    expect(activations).toBe(0)
  })

  it.each(['idle', 'success', 'error'] as const)('exposes the controlled %s action state', (actionState) => {
    render(<Button actionState={actionState}>Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toHaveAttribute('data-action-state', actionState)
  })

  // Button's live-announcement region is nested INSIDE the <button>, not a
  // DOM sibling — so a Button placed inside a role that restricts its
  // direct children (radiogroup, tablist, toolbar, menu, listbox —
  // RadioGroupItem and SegmentedControlItem are both built on Button) never
  // gets an illegal second child (axe aria-required-children). Asserted
  // across idle AND every action state, since the announcement region is
  // always present, not conditionally omitted.
  it.each(['idle', 'pending', 'success', 'error'] as const)(
    'renders exactly one DOM child into a role-restrictive parent in the %s action state',
    (actionState) => {
      const { container } = render(
        <div role="radiogroup" aria-label="Choice">
          <Button role="radio" aria-checked actionState={actionState}>A</Button>
        </div>,
      )
      const group = container.querySelector('[role="radiogroup"]')!
      expect(group.children).toHaveLength(1)
      expect(group.children[0].tagName).toBe('BUTTON')
    },
  )
})

// The name shield (aria-labelledby={contentId}) engages ONLY while an
// announcement is actually present — engaging it unconditionally overrides
// a `title`-based accessible name (the content span it references contains
// no text for an icon-only Button), un-naming every idle icon-only Button.
describe('Button — accessible name from a non-content source (e.g. title)', () => {
  it('keeps a title-only icon Button named by its title, idle and after an action completes', () => {
    const { rerender } = render(<Button title="Close panel"><svg aria-hidden="true" /></Button>)
    expect(screen.getByRole('button', { name: 'Close panel' })).toBeInTheDocument()
    rerender(<Button title="Close panel" actionState="success"><svg aria-hidden="true" /></Button>)
    expect(screen.getByRole('button', { name: 'Close panel' })).toBeInTheDocument()
    rerender(<Button title="Close panel" actionState="idle"><svg aria-hidden="true" /></Button>)
    expect(screen.getByRole('button', { name: 'Close panel' })).toBeInTheDocument()
  })

  it('keeps a text Button named by its content during success and error, not just idle', () => {
    const { rerender } = render(<Button>Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
    rerender(<Button actionState="success">Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
    rerender(<Button actionState="error">Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
    rerender(<Button actionState="idle">Save</Button>)
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
  })
})

describe('Button — action feedback timing', () => {
  beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date', 'performance'] }))
  afterEach(() => vi.useRealTimers())

  it('reserves feedback geometry immediately and reveals pending at the 400ms boundary', () => {
    render(<Button actionState="pending">Save</Button>)
    const button = screen.getByRole('button', { name: 'Save' })
    const announcement = screen.getByRole('status')
    expect(announcement).toBeEmptyDOMElement()
    expect(button.querySelector('[data-action-indicator]')).toBeInTheDocument()
    expect(button.querySelector('[data-action-feedback]')).not.toBeInTheDocument()
    act(() => vi.advanceTimersByTime(399))
    expect(button.querySelector('[data-action-feedback="pending"]')).not.toBeInTheDocument()
    expect(announcement).toBeEmptyDOMElement()
    act(() => vi.advanceTimersByTime(1))
    expect(button.querySelector('[data-action-feedback="pending"]')).toBeInTheDocument()
    expect(screen.getByRole('status')).toBe(announcement)
    expect(announcement).toHaveTextContent('Action in progress')
  })

  it('keeps visible pending feedback for 300ms before presenting success', () => {
    const { rerender } = render(<Button actionState="pending">Save</Button>)
    act(() => vi.advanceTimersByTime(400))
    rerender(<Button actionState="success">Save</Button>)
    const button = screen.getByRole('button', { name: 'Save' })
    expect(button.querySelector('[data-action-feedback="pending"]')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(299))
    expect(button.querySelector('[data-action-feedback="success"]')).not.toBeInTheDocument()
    act(() => vi.advanceTimersByTime(1))
    expect(button.querySelector('[data-action-feedback="success"]')).toBeInTheDocument()
    expect(button).toHaveAccessibleName('Save')
    expect(button).toHaveAttribute('aria-description', 'Action succeeded')
    expect(screen.getByRole('status')).toHaveTextContent('Action succeeded')
  })

  it('announces an error before a retry even while the pending spinner completes its dwell', () => {
    const { rerender } = render(<Button actionState="pending">Retry</Button>)
    const announcement = screen.getByRole('status')
    act(() => vi.advanceTimersByTime(400))
    expect(announcement).toHaveTextContent('Action in progress')
    rerender(<Button actionState="error">Retry</Button>)
    const button = screen.getByRole('button', { name: 'Retry' })
    expect(button.querySelector('[data-action-feedback="pending"]')).toBeInTheDocument()
    expect(button).not.toBeDisabled()
    expect(button).toHaveAttribute('aria-description', 'Action failed')
    expect(announcement).toHaveTextContent('Action failed')
    act(() => vi.advanceTimersByTime(100))
    rerender(<Button actionState="pending">Retry</Button>)
    expect(screen.getByRole('status')).toBe(announcement)
    expect(announcement).toHaveTextContent('Action in progress')
    expect(button).toBeDisabled()
  })

  it('presents a quick failure immediately and allows a retry without rewriting content', () => {
    const retry = vi.fn()
    const { rerender } = render(<Button actionState="pending" onClick={retry}><span>Try again</span></Button>)
    act(() => vi.advanceTimersByTime(399))
    rerender(<Button actionState="error" onClick={retry}><span>Try again</span></Button>)
    const button = screen.getByRole('button', { name: 'Try again' })
    expect(button.querySelector('[data-action-feedback="error"]')).toBeInTheDocument()
    expect(button).toHaveAttribute('aria-description', 'Action failed')
    expect(screen.getByRole('status')).toHaveTextContent('Action failed')
    fireEvent.click(button)
    expect(retry).toHaveBeenCalledOnce()
  })

  it('preserves an asChild accessible name and native link contract with success feedback', () => {
    render(<Button asChild actionState="success"><a href="/done">Continue</a></Button>)
    const link = screen.getByRole('link', { name: 'Continue' })
    expect(link).toHaveAttribute('href', '/done')
    expect(link).toHaveAttribute('aria-description', 'Action succeeded')
    expect(link.querySelector('[data-action-feedback="success"]')).toBeInTheDocument()
  })
})

describe('Button — asChild capture and activation', () => {
  it('does not invent native button attributes for an asChild link', () => {
    render(<Button asChild><a href="/settings">Settings</a></Button>)
    const link = screen.getByRole('link', { name: 'Settings' })
    expect(link).not.toHaveAttribute('type')
    expect(link).not.toHaveAttribute('disabled')
  })

  it('preserves enabled asChild capture ordering exactly once', () => {
    const order: string[] = []
    render(
      <Button asChild onClickCapture={() => order.push('button-capture')}>
        <a href="#target" onClickCapture={() => order.push('child-capture')} onClick={() => order.push('click')}>Target</a>
      </Button>,
    )
    fireEvent.click(screen.getByRole('link', { name: 'Target' }))
    expect(order).toEqual(['child-capture', 'button-capture', 'click'])
  })

  it('preserves enabled asChild auxiliary capture ordering and href', () => {
    const order: string[] = []
    render(<Button asChild onAuxClickCapture={() => order.push('button-aux')}><a href="#target" onAuxClickCapture={() => order.push('child-aux')}>Target</a></Button>)
    const link = screen.getByRole('link', { name: 'Target' })
    expect(fireEvent(link, new MouseEvent('auxclick', { button: 1, bubbles: true, cancelable: true }))).toBe(true)
    expect(order).toEqual(['child-aux', 'button-aux'])
    expect(link).toHaveAttribute('href', '#target')
  })

  it.each(['pending', 'disabled'] as const)('suppresses %s asChild auxiliary activation before child or caller handlers', (state) => {
    const child = vi.fn(); const caller = vi.fn()
    render(<Button asChild actionState={state === 'pending' ? 'pending' : 'idle'} disabled={state === 'disabled'} onAuxClickCapture={caller}><a href="/settings" onAuxClickCapture={child}>Settings</a></Button>)
    const link = screen.getByRole('link', { name: 'Settings' })
    expect(fireEvent(link, new MouseEvent('auxclick', { button: 1, bubbles: true, cancelable: true }))).toBe(false)
    expect(child).not.toHaveBeenCalled(); expect(caller).not.toHaveBeenCalled()
    expect(link).toHaveAttribute('href', '/settings')
  })

  it('preserves both enabled capture callbacks when the child prevents the default action', () => {
    const order: string[] = []
    render(
      <Button asChild onClickCapture={() => order.push('button-capture')}>
        <a href="#target" onClickCapture={(event) => { event.preventDefault(); order.push('child-capture') }}>Target</a>
      </Button>,
    )
    fireEvent.click(screen.getByRole('link', { name: 'Target' }))
    expect(order).toEqual(['child-capture', 'button-capture'])
  })

  it('suppresses pending asChild link navigation and activation', () => {
    let activations = 0
    render(<Button asChild actionState="pending"><a href="/settings" onClick={() => { activations += 1 }}>Settings</a></Button>)
    const link = screen.getByRole('link', { name: 'Settings' })
    expect(link).toHaveAttribute('aria-disabled', 'true')
    expect(link).toHaveAttribute('aria-busy', 'true')
    expect(link).toHaveAttribute('href', '/settings')
    expect(link).toHaveClass('aria-disabled:cursor-not-allowed', 'aria-disabled:opacity-50')
    expect(fireEvent.click(link)).toBe(false)
    expect(activations).toBe(0)
  })

  it.each(['pending', 'disabled'] as const)('suppresses keyboard activation for a %s asChild link', async (state) => {
    const user = userEvent.setup()
    let activations = 0
    render(
      <Button asChild actionState={state === 'pending' ? 'pending' : 'idle'} disabled={state === 'disabled'}>
        <a href="/settings" onClick={() => { activations += 1 }}>Settings</a>
      </Button>
    )
    const link = screen.getByRole('link', { name: 'Settings' })
    link.focus()
    await user.keyboard('{Enter}')
    expect(activations).toBe(0)
    expect(link).toHaveAttribute('href', '/settings')
  })

  it('suppresses disabled asChild activation while preserving its URL', () => {
    let activations = 0
    render(<Button asChild disabled><a href="/settings" onClick={() => { activations += 1 }}>Settings</a></Button>)
    const link = screen.getByRole('link', { name: 'Settings' })
    expect(link).toHaveAttribute('aria-disabled', 'true')
    expect(link).toHaveAttribute('href', '/settings')
    expect(link).toHaveClass('aria-disabled:cursor-not-allowed', 'aria-disabled:opacity-50')
    expect(fireEvent.click(link)).toBe(false)
    expect(activations).toBe(0)
  })
})

describe('Button — form submission', () => {
  it('submits a real form only when an enabled submit button activates', () => {
    const submitted = vi.fn((event: FormEvent) => event.preventDefault())
    const { rerender } = render(<form onSubmit={submitted}><Button type="submit">Save</Button></form>)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(submitted).toHaveBeenCalledOnce()
    rerender(<form onSubmit={submitted}><Button type="submit" actionState="pending">Save</Button></form>)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    rerender(<form onSubmit={submitted}><Button type="submit" disabled>Save</Button></form>)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(submitted).toHaveBeenCalledOnce()
  })
})

describe('Button — aria state contract', () => {
  it('does not allow callers to override enforced pending accessibility state', () => {
    render(<Button actionState="pending" aria-busy={false} aria-disabled={false}>Save</Button>)
    const button = screen.getByRole('button', { name: 'Save' })
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(button).toBeDisabled()
  })

  it('does not let child capture handlers or false ARIA props defeat a blocked asChild action', () => {
    let captures = 0
    let activations = 0
    render(
      <Button asChild actionState="pending">
        <a
          href="/settings"
          aria-busy={false}
          aria-disabled={false}
          onClickCapture={() => { captures += 1 }}
          onClick={() => { activations += 1 }}
        >Settings</a>
      </Button>
    )
    const link = screen.getByRole('link', { name: 'Settings' })
    fireEvent.click(link)
    expect(captures).toBe(0)
    expect(activations).toBe(0)
    expect(link).toHaveAttribute('aria-busy', 'true')
    expect(link).toHaveAttribute('aria-disabled', 'true')
  })

  it('preserves caller ARIA state when the action contract does not enforce it', () => {
    render(<Button aria-busy={false} aria-disabled="true">Queued elsewhere</Button>)
    const button = screen.getByRole('button', { name: 'Queued elsewhere' })
    expect(button).toHaveAttribute('aria-busy', 'false')
    expect(button).toHaveAttribute('aria-disabled', 'true')
  })

  it('preserves Button-level ARIA state on an unblocked asChild action', () => {
    const { rerender } = render(<Button asChild aria-disabled="true" aria-busy="true"><a href="/queue">Queue</a></Button>)
    const link = screen.getByRole('link', { name: 'Queue' })
    expect(link).toHaveAttribute('aria-disabled', 'true')
    expect(link).toHaveAttribute('aria-busy', 'true')

    rerender(<Button asChild aria-disabled="true" aria-busy="true"><a href="/queue" aria-disabled="false" aria-busy="false">Queue</a></Button>)
    expect(link).toHaveAttribute('aria-disabled', 'false')
    expect(link).toHaveAttribute('aria-busy', 'false')
  })
})


describe('Button persistent action announcements', () => {
  it('updates the same initially empty live region through action states and clears idle feedback', () => {
    const { rerender } = render(<Button>Save</Button>)
    const region = screen.getByRole('status')
    expect(region).toBeEmptyDOMElement()
    expect(region).toHaveAttribute('aria-live', 'polite')
    expect(region).toHaveAttribute('aria-atomic', 'true')
    rerender(<Button actionState="success">Save</Button>)
    expect(screen.getByRole('status')).toBe(region)
    expect(region).toHaveTextContent('Action succeeded')
    rerender(<Button actionState="error">Save</Button>)
    expect(screen.getByRole('status')).toBe(region)
    expect(region).toHaveTextContent('Action failed')
    rerender(<Button>Save</Button>)
    expect(screen.getByRole('status')).toBe(region)
    expect(region).toBeEmptyDOMElement()
    expect(screen.getByRole('button', { name: /^Save$/ })).toBeInTheDocument()
  })

  it('keeps action announcements inside their modal and outside hidden content', () => {
    render(<Dialog open><DialogContent><DialogTitle>Edit</DialogTitle><DialogDescription>Save your changes</DialogDescription><Button actionState="success">Save</Button></DialogContent></Dialog>)
    const region = screen.getByRole('status')
    expect(screen.getByRole('dialog')).toContainElement(region)
    expect(region.closest('[aria-hidden="true"]')).toBeNull()
    expect(region).toHaveTextContent('Action succeeded')
    expect(screen.getByRole('button', { name: /^Save$/ })).toBeInTheDocument()
  })
})

// Explicit native form behavior must survive Slot composition.
describe('Button — composed form semantics', () => {
  it.each(['button', 'reset', 'submit'] as const)('preserves explicit %s behavior through asChild', (type) => {
    const onSubmit = vi.fn((event: FormEvent) => event.preventDefault())
    const onReset = vi.fn()
    render(
      <form onSubmit={onSubmit} onReset={onReset}>
        <input aria-label="Name" defaultValue="Initial" />
        <Button asChild type={type}><button>Action</button></Button>
      </form>,
    )
    const input = screen.getByRole('textbox', { name: 'Name' })
    fireEvent.change(input, { target: { value: 'Edited' } })
    const action = screen.getByRole('button', { name: 'Action' })
    expect(action).toHaveAttribute('type', type)
    fireEvent.click(action)
    expect(onSubmit).toHaveBeenCalledTimes(type === 'submit' ? 1 : 0)
    expect(onReset).toHaveBeenCalledTimes(type === 'reset' ? 1 : 0)
    expect(input).toHaveValue(type === 'reset' ? 'Initial' : 'Edited')
  })

  it('preserves an explicit child type over a Slot type and leaves links without an invented button type', () => {
    const { rerender } = render(<Button asChild type="submit"><button type="button">Action</button></Button>)
    expect(screen.getByRole('button', { name: 'Action' })).toHaveAttribute('type', 'button')
    rerender(<Button asChild><a href="#details">Details</a></Button>)
    expect(screen.getByRole('link', { name: 'Details' })).not.toHaveAttribute('type')
  })
})

/**
 * SearchField + SearchToolbar — W2-1 contract §2.1 (R33).
 *
 * RED on 69aa07d9a: `src/components/ui/search-field.tsx` does not exist yet
 * (contract §2.1 names it as a new file), so this file fails at import for
 * the right reason: the component is not implemented.
 *
 * Escape/clear behaviour follows the contract's Q1 RECOMMENDED option
 * (founder question Q1: A — clear button only when text is present; Escape
 * clears text, and on an empty field lets Escape reach the surrounding
 * pop-up). If the founder picks B or C, the Escape/clear cases marked [Q1]
 * change with the answer.
 *
 * jsdom computes no layout: the 36px/44px size rows and the 1024px/320px
 * reflow rows of §2.1 "Assertable" are class/token proxies here; their real
 * checks are the manifest's browser/pointer/reflow kinds (§2.10).
 */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import * as React from 'react'
import { createRef } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { Dialog, DialogContent, DialogTitle } from './dialog'
import { SearchField, SearchToolbar } from './search-field'

// Fresh props per test — no mock state leaks across tests (order independence).
function baseProps(
  overrides: {
    value?: string
    onValueChange?: (v: string) => void
    clearable?: boolean
    disabled?: boolean
  } = {},
) {
  return {
    value: '',
    onValueChange: vi.fn(),
    placeholder: 'Search connectors…',
    'aria-label': 'Search connectors',
    ...overrides,
  }
}

describe('SearchField — identity and controlled value', () => {
  it('renders a searchbox whose accessible name is exactly the required aria-label', () => {
    render(<SearchField {...baseProps()} />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    expect(input.tagName).toBe('INPUT')
    expect(input).toHaveAttribute('type', 'search')
    expect(input).toHaveAttribute('placeholder', 'Search connectors…')
  })

  it('the magnifier icon is aria-hidden and outside the accessibility tree', () => {
    const { container } = render(<SearchField {...baseProps()} />)
    const wrapper = container.querySelector('[data-slot="search-field"]')
    expect(wrapper).not.toBeNull()
    const svg = wrapper!.querySelector('svg')
    expect(svg).not.toBeNull()
    expect(svg).toHaveAttribute('aria-hidden', 'true')
    // Not part of the announced name: the searchbox name is the aria-label only.
    expect(screen.getByRole('searchbox', { name: 'Search connectors' })).toBeTruthy()
  })

  it('typing calls onValueChange with the exact typed string (controlled)', () => {
    const onValueChange = vi.fn()
    render(<SearchField {...baseProps({ onValueChange })} />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    fireEvent.change(input, { target: { value: 'lo' } })
    expect(onValueChange).toHaveBeenCalledTimes(1)
    expect(onValueChange).toHaveBeenCalledWith('lo')
    fireEvent.change(input, { target: { value: 'lo and behold' } })
    expect(onValueChange).toHaveBeenLastCalledWith('lo and behold')
  })

  it('forwards its ref to the underlying <input>', () => {
    const ref = createRef<HTMLInputElement>()
    render(<SearchField {...baseProps()} ref={ref} />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    expect(ref.current).toBe(input)
  })

  it('id and autoFocus land on the input', () => {
    render(<SearchField {...baseProps()} id="connector-search" autoFocus />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    expect(input).toHaveAttribute('id', 'connector-search')
    expect(input).toHaveFocus()
  })
})

describe('SearchField — clear button [Q1]', () => {
  it('shows no clear button while the value is empty', () => {
    render(<SearchField {...baseProps({ value: '' })} />)
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument()
  })

  it('shows a "Clear search" button once the value is non-empty', () => {
    render(<SearchField {...baseProps({ value: 'webhook' })} />)
    expect(screen.getByRole('button', { name: 'Clear search' })).toBeInTheDocument()
  })

  it('clicking the clear button calls onValueChange(\'\') and returns focus to the input', () => {
    const onValueChange = vi.fn()
    render(<SearchField {...baseProps({ value: 'webhook', onValueChange })} />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }))
    expect(onValueChange).toHaveBeenCalledWith('')
    expect(input).toHaveFocus()
  })

  it('renders no clear button when clearable={false} even with text', () => {
    render(<SearchField {...baseProps({ value: 'webhook', clearable: false })} />)
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument()
  })
})

describe('SearchField — Escape inside the real Radix Dialog (§2.1, CHECK finding 1)', () => {
  // The round-1 tests here used a BUBBLE-phase document listener and claimed
  // "the enclosing dialog stays open". That was weaker than its title: Radix
  // dismissable layers (Dialog, Sheet, Popover) listen for Escape on
  // `document` in the CAPTURE phase (react-dismissable-layer dist), so they
  // see the key before any input-level handler runs, and a bubble listener
  // proves nothing about them (CHECK finding 1, team-lead round 4: contract
  // §2.1 stands — Escape with text clears the field AND the dialog stays
  // open; Escape on an empty field lets the dialog close). These cases
  // render the real kit Dialog — real Radix, no mock — and assert the
  // CONTRACT outcome, replacing the old listener tests.
  function DialogWithSearchField({ initial, clearable = true }: { initial: string; clearable?: boolean }) {
    const [open, setOpen] = React.useState(true)
    const [value, setValue] = React.useState(initial)
    return (
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogTitle>Filter</DialogTitle>
          <SearchField
            value={value}
            onValueChange={setValue}
            placeholder="Search connectors…"
            aria-label="Search connectors"
            clearable={clearable}
          />
        </DialogContent>
      </Dialog>
    )
  }

  async function openAndFocus(initial: string, clearable?: boolean) {
    render(<DialogWithSearchField initial={initial} clearable={clearable} />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    act(() => input.focus())
    // Instrument check: the dialog is open BEFORE the key, so a pass can
    // never come from "it never rendered".
    expect(screen.getByRole('dialog', { name: 'Filter' })).toBeInTheDocument()
    return input
  }

  it('with clearable and non-empty text, Escape clears the field AND the dialog stays open', async () => {
    const input = await openAndFocus('webhook')
    fireEvent.keyDown(input, { key: 'Escape', code: 'Escape' })
    // The clear happens (controlled value flows back through the harness).
    expect(input).toHaveValue('')
    // Let any dismissal that is going to happen happen (the alert-dialog
    // suite's close idiom shows dismissal lands within this window).
    await act(async () => { await Promise.resolve() })
    // THE CONTRACT: the enclosing dialog is still open.
    expect(screen.getByRole('dialog', { name: 'Filter' })).toBeInTheDocument()
  })

  it('with an empty field, Escape is not handled and the dialog closes as usual', async () => {
    const input = await openAndFocus('')
    fireEvent.keyDown(input, { key: 'Escape', code: 'Escape' })
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Filter' })).not.toBeInTheDocument(),
    )
    expect(input).toHaveValue('')
  })

  it('with clearable={false} and text, Escape is never handled and the dialog closes as usual', async () => {
    const input = await openAndFocus('webhook', false)
    fireEvent.keyDown(input, { key: 'Escape', code: 'Escape' })
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Filter' })).not.toBeInTheDocument(),
    )
    expect(input).toHaveValue('webhook')
  })
})

describe('SearchField — states', () => {
  it('disabled disables the input, drops the clear button, and carries opacity-50', () => {
    const { container } = render(<SearchField {...baseProps({ value: 'webhook', disabled: true })} />)
    const input = screen.getByRole('searchbox', { name: 'Search connectors' })
    expect(input).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Clear search' })).not.toBeInTheDocument()
    // Contract §2.1 States: "Disabled: input disabled, no clear button,
    // opacity-50" — the token must appear on the field (wrapper or input).
    const field = container.querySelector('[data-slot="search-field"]')
    const opacityCarrier = [field, ...(field ? Array.from(field.children) : [])].find((el) =>
      el?.classList.contains('opacity-50'),
    )
    expect(opacityCarrier).not.toBeUndefined()
  })

  it('the wrapper owns the search-field slot and adds no focus ring of its own (skill rule 7)', () => {
    const { container } = render(<SearchField {...baseProps()} />)
    const wrapper = container.querySelector('div[data-slot="search-field"]')
    expect(wrapper).not.toBeNull()
    expect(wrapper!.className).not.toMatch(/focus-visible:(ring|outline)/)
  })
})

describe('SearchToolbar', () => {
  it('renders the search-toolbar slot with the contract’s wrapping flex layout', () => {
    const { container } = render(
      <SearchToolbar>
        <SearchField {...baseProps()} />
        <div data-testid="filter-a">A</div>
      </SearchToolbar>,
    )
    const toolbar = container.querySelector('div[data-slot="search-toolbar"]')
    expect(toolbar).not.toBeNull()
    expect(toolbar).toHaveClass(
      'flex',
      'flex-wrap',
      'items-center',
      'gap-[var(--space-2)]',
    )
  })

  it('is a role="search" landmark only when an aria-label is given', () => {
    const { rerender } = render(
      <SearchToolbar aria-label="Connector filters">
        <SearchField {...baseProps()} />
      </SearchToolbar>,
    )
    expect(screen.getByRole('search', { name: 'Connector filters' })).toBeInTheDocument()
    rerender(
      <SearchToolbar>
        <SearchField {...baseProps()} />
      </SearchToolbar>,
    )
    expect(screen.queryByRole('search')).not.toBeInTheDocument()
  })

  it('sizes the SearchField child to grow and to hold a 192px minimum (flex-[1_1_auto] + min-w 3×--space-8)', () => {
    const { container } = render(
      <SearchToolbar>
        <SearchField {...baseProps()} />
      </SearchToolbar>,
    )
    const field = container.querySelector('[data-slot="search-field"]')
    expect(field).not.toBeNull()
    // 3 × --space-8 (64px) = 192px, the contract's minimum before filters wrap.
    expect(field).toHaveClass('flex-[1_1_auto]')
    expect(field).toHaveClass('min-w-[calc(var(--space-8)*3)]')
  })

  // Contract §2.1 Toolbar layout: "Filters are `shrink-0`." Read as the
  // toolbar's own layout duty toward its non-SearchField children (it keeps
  // them at content width so only the field flexes). Flagged in the report
  // as an ambiguity: if the intended reading is that callers pass shrink-0
  // themselves, this assertion moves to the caller's contract instead.
  it('keeps non-SearchField children at content width via shrink-0', () => {
    const { container } = render(
      <SearchToolbar>
        <SearchField {...baseProps()} />
        <div data-testid="filter-a">A</div>
      </SearchToolbar>,
    )
    const filter = container.querySelector('[data-testid="filter-a"]')!
    const carrier =
      filter.classList.contains('shrink-0') ? filter : filter.closest('.shrink-0')
    expect(carrier).not.toBeNull()
  })
})

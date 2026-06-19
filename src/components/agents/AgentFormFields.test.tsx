/**
 * AgentFormFields.test.tsx
 *
 * Tests for the three primitives lifted from AgentProfile.tsx so the
 * wizard and the edit slide-over share one widget:
 *   - <AvatarColorPicker>  — 8-swatch palette with semantic aria-labels
 *   - <IconPicker>         — SmartSelect over ICON_OPTIONS
 *   - <AvatarHeader>       — 48-px circle with bg color + icon
 *
 * Traces:
 *   - wave5a-wire-ui-spec.md — Scenario: wizard and profile share identity widgets
 *   - wave5a-wire-ui-spec.md — US-7 AC1: Agent profile renders identity section
 */

// jsdom does not implement ResizeObserver (used by cmdk inside SmartSelect's
// searchable popover branch). Polyfill a noop — the ICON_OPTIONS list is 10
// entries, which crosses SmartSelect's SEARCHABLE_THRESHOLD of 5, so the
// popover+Command path is exercised and would otherwise throw on render.
import { describe, it, expect, vi } from 'vitest'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { render, screen, fireEvent, within } from '@testing-library/react'
import { AvatarColorPicker, IconPicker, AvatarHeader } from './AgentFormFields'
import { AVATAR_COLORS, AVATAR_COLORS_BY_NAME } from '@/lib/constants'
import { ICON_OPTIONS } from '@/lib/agentIcons'

describe('AvatarColorPicker', () => {
  it('renders one button per AVATAR_COLORS entry (8 swatches)', () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — Identity section exposes
    // every brand-palette color.
    const onChange = vi.fn()
    render(<AvatarColorPicker value={AVATAR_COLORS[0]} onChange={onChange} />)
    for (const color of AVATAR_COLORS) {
      const name = AVATAR_COLORS_BY_NAME[color] ?? color
      expect(
        screen.getByRole('button', { name }),
        `missing swatch for ${name} (${color})`,
      ).toBeInTheDocument()
    }
    // Sanity: exactly 8 buttons (no extras, no duplicates).
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(AVATAR_COLORS.length)
  })

  it('calls onChange with the chosen hex when a swatch is clicked', () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — clicking a swatch commits
    // the choice back to the parent.
    const onChange = vi.fn()
    render(<AvatarColorPicker value={AVATAR_COLORS[0]} onChange={onChange} />)
    // Pick the second swatch ("Azure" = '#3B82F6') and click it.
    const target = AVATAR_COLORS[1]
    const name = AVATAR_COLORS_BY_NAME[target]
    fireEvent.click(screen.getByRole('button', { name }))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith(target)
  })

  it('marks the selected swatch with aria-pressed=true (and others false)', () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — selected swatch is
    // distinguishable for screen readers. aria-pressed is the canonical
    // signal for a toggle button.
    const onChange = vi.fn()
    const selected = AVATAR_COLORS[3] // Saffron '#EAB308'
    render(<AvatarColorPicker value={selected} onChange={onChange} />)
    for (const color of AVATAR_COLORS) {
      const name = AVATAR_COLORS_BY_NAME[color] ?? color
      const btn = screen.getByRole('button', { name })
      if (color === selected) {
        expect(btn).toHaveAttribute('aria-pressed', 'true')
      } else {
        expect(btn).toHaveAttribute('aria-pressed', 'false')
      }
    }
  })

  it('honours the testIdPrefix prop when generating data-testid values', () => {
    // Traces: wave5a-wire-ui-spec.md — wizard and profile can share the
    // component without colliding on test ids. The profile uses
    // "avatar-color" (default), so we pick a different prefix here to
    // assert the prefix actually flows through.
    const onChange = vi.fn()
    render(
      <AvatarColorPicker
        value={AVATAR_COLORS[0]}
        onChange={onChange}
        testIdPrefix="wizard-color"
      />,
    )
    const firstName = AVATAR_COLORS_BY_NAME[AVATAR_COLORS[0]]
    expect(
      screen.getByTestId(`wizard-color-${firstName}`),
    ).toBeInTheDocument()
  })
})

describe('IconPicker', () => {
  it('renders the trigger button with the current value as its visible label', () => {
    // SmartSelect renders the chosen item's label as the trigger's
    // visible text. The trigger is a Radix popover trigger button with
    // aria-haspopup="listbox". We target it via the visible label rather
    // than a testid because the IconPicker → SmartSelect pass-through
    // does not currently attach a data-testid attribute to the trigger.
    const onChange = vi.fn()
    render(<IconPicker value="Robot" onChange={onChange} />)
    const trigger = screen.getByRole('button', { name: /Robot/ })
    expect(trigger).toBeInTheDocument()
    // Selected label must be visible to the user (sighted + screen-reader
    // via the trigger's text content).
    expect(within(trigger).getByText('Robot')).toBeInTheDocument()
  })

  it('exposes one option per ICON_OPTIONS entry (10 entries)', async () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — every ICON_OPTIONS entry
    // is available from the picker. SmartSelect renders the options inside
    // a Radix popover; opening it (click the trigger) materialises the
    // items as cmdk CommandItem nodes. The trigger button also carries the
    // selected label as visible text, so use findAllByText and assert
    // >= 1 (the popover's CommandItem).
    const onChange = vi.fn()
    render(<IconPicker value="Robot" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: /Robot/ }))
    for (const { name } of ICON_OPTIONS) {
      // cmdk renders each CommandItem with the item label as text content.
      // With 10 items (>= SmartSelect's SEARCHABLE_THRESHOLD of 5), the
      // searchable popover+Command branch is used.
      const matches = await screen.findAllByText(name)
      expect(matches.length).toBeGreaterThanOrEqual(1)
    }
  })

  it('calls onChange with the picked icon name', async () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — picking an option commits
    // the wire-shape IconName back to the parent.
    const onChange = vi.fn()
    render(<IconPicker value="Robot" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: /Robot/ }))
    // Pick a non-default option to prove the change is observed.
    const target = 'Lightbulb'
    fireEvent.click(await screen.findByText(target))
    expect(onChange).toHaveBeenCalledWith(target)
  })
})

describe('AvatarHeader', () => {
  it('renders a circular background tinted with the given color', () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — header circle reflects the
    // agent's chosen brand color. The wrapper is the first <div> rendered
    // by the component. jsdom normalizes inline `backgroundColor: hex`
    // to its `rgb(...)` form, so we compare on the canonical rgb string.
    const color = AVATAR_COLORS[7] // Forge Gold '#D4AF37'
    const { container } = render(<AvatarHeader color={color} />)
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper).not.toBeNull()
    expect(wrapper.style.backgroundColor).toBe('rgb(212, 175, 55)')
  })

  it('falls back to a surface token when color is missing', () => {
    // Defense for null / undefined color values — the component renders
    // an inert circle instead of crashing. The fall-back is a CSS var
    // rather than a hex (kept token-driven).
    const { container } = render(<AvatarHeader color={null} />)
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper).not.toBeNull()
    expect(wrapper.style.backgroundColor).toBe('var(--color-surface-3)')
  })

  it('honours the className override for the wrapper', () => {
    // The component accepts a className prop so consumers (e.g. the
    // wizard) can size the circle without forking the implementation.
    const { container } = render(
      <AvatarHeader color={AVATAR_COLORS[0]} className="w-20 h-20" />,
    )
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper).toHaveClass('w-20')
    expect(wrapper).toHaveClass('h-20')
  })
})
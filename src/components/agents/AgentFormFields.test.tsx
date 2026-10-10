/**
 * AgentFormFields.test.tsx
 *
 * Tests for the primitives lifted from AgentProfile.tsx so the
 * wizard and the edit slide-over share one widget:
 *   - <AvatarColorPicker>  — 10-colour identity palette (Azure..Grey) with semantic aria-labels
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

import { render, screen, fireEvent } from '@testing-library/react'
import { AvatarColorPicker, AvatarHeader } from './AgentFormFields'

// SPEC "Locked identity vocabulary" ordered palette (Azure..Grey).
// Not src/lib/constants.ts AVATAR_COLORS (the retired eight brand swatches)
// and not a value read off the picker.
const IDENTITY_PALETTE = [
  { name: 'Azure', hex: '#3B82F6' },
  { name: 'Sky', hex: '#38BDF8' },
  { name: 'Cyan', hex: '#22D3EE' },
  { name: 'Indigo', hex: '#818CF8' },
  { name: 'Violet', hex: '#A78BFA' },
  { name: 'Purple', hex: '#C084FC' },
  { name: 'Fuchsia', hex: '#E879F9' },
  { name: 'Pink', hex: '#F472B6' },
  { name: 'Orange', hex: '#FB923C' },
  { name: 'Grey', hex: '#9CA3AF' },
] as const

const RETIRED_SWATCH_NAMES = ['Verdant', 'Amethyst', 'Saffron', 'Ember', 'Crimson', 'Slate', 'Forge Gold'] as const

describe('AvatarColorPicker', () => {
  it('renders one button per identity palette colour (Azure through Grey) and not the retired swatches', () => {
    // Traces: navigation spec FR-017 / BDD-05.2 — the editor offers the ten
    // ordered palette colours, not the previous eight brand swatches.
    const onChange = vi.fn()
    render(<AvatarColorPicker value={IDENTITY_PALETTE[0].hex} onChange={onChange} />)
    for (const { name, hex } of IDENTITY_PALETTE) {
      expect(
        screen.getByRole('button', { name }),
        `missing swatch for ${name} (${hex})`,
      ).toBeInTheDocument()
    }
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(IDENTITY_PALETTE.length)
    for (const retired of RETIRED_SWATCH_NAMES) {
      expect(screen.queryByRole('button', { name: retired }), retired).not.toBeInTheDocument()
    }
  })

  it('calls onChange with the canonical Sky hex when Sky is clicked', () => {
    // Traces: FR-017 — the second palette entry is Sky #38BDF8, uppercase.
    const onChange = vi.fn()
    const sky = IDENTITY_PALETTE[1]
    render(<AvatarColorPicker value={IDENTITY_PALETTE[0].hex} onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: sky.name }))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith(sky.hex)
  })

  it('marks the selected swatch with aria-pressed=true (and others false)', () => {
    // Traces: the selected palette swatch is distinguishable. aria-pressed
    // is the signal for a toggle button. Fuchsia is palette entry 7.
    const onChange = vi.fn()
    const selected = IDENTITY_PALETTE[6]
    render(<AvatarColorPicker value={selected.hex} onChange={onChange} />)
    for (const { name, hex } of IDENTITY_PALETTE) {
      const btn = screen.getByRole('button', { name })
      expect(btn).toHaveAttribute('aria-pressed', hex === selected.hex ? 'true' : 'false')
    }
  })

  it('honours the testIdPrefix prop when generating data-testid values', () => {
    // Traces: wave5a-wire-ui-spec.md — wizard and profile can share the
    // component without colliding on test ids. The profile uses
    // "avatar-color" (default), so we pick a different prefix here to
    // assert the prefix actually flows through. Azure is palette entry 1.
    const onChange = vi.fn()
    const azure = IDENTITY_PALETTE[0]
    render(
      <AvatarColorPicker
        value={azure.hex}
        onChange={onChange}
        testIdPrefix="wizard-color"
      />,
    )
    expect(screen.getByTestId(`wizard-color-${azure.name}`)).toBeInTheDocument()
  })
})

describe('AvatarHeader', () => {
  it('renders a circular background tinted with the given color', () => {
    // Traces: wave5a-wire-ui-spec.md US-7 AC1 — header circle reflects the
    // agent's chosen brand color. The wrapper is the first <div> rendered
    // by the component. jsdom normalizes inline `backgroundColor: hex`
    // to its `rgb(...)` form, so we compare on the canonical rgb string.
    // Azure #3B82F6 → rgb(59, 130, 246). 0x3B = 59, 0x82 = 130, 0xF6 = 246.
    const color = IDENTITY_PALETTE[0].hex
    const { container } = render(<AvatarHeader color={color} />)
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper).not.toBeNull()
    expect(wrapper.style.backgroundColor).toBe('rgb(59, 130, 246)')
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
      <AvatarHeader color={IDENTITY_PALETTE[0].hex} className="w-20 h-20" />,
    )
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper).toHaveClass('w-20')
    expect(wrapper).toHaveClass('h-20')
  })
})
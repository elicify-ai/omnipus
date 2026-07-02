// brand-icon.test.tsx — Unit tests for <BrandIcon> and <BrandDisclaimer>.
//
// Test #9  — brandicon.knownSlug.test   — Known slug renders an inline SVG.
// Test #10 — brandicon.missingSlug.test — Unknown slug renders a lettermark, no throw.
// (Disclaimer assertion is included in the BrandDisclaimer suite below.)
//
// Traces to: connectors-providers-redesign-spec.md §7 tests #9/#10;
//            US-2 Acceptance Scenarios AS-1/AS-2/AS-3.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BrandIcon } from './brand-icon'
import { BrandDisclaimer, BRAND_DISCLAIMER_TEXT } from './brand-disclaimer'

// ---------------------------------------------------------------------------
// Test #9 — brandicon.knownSlug.test
// ---------------------------------------------------------------------------
// "openai" is a vendored slug (p_openai.svg). The component must render an
// inline SVG element themed with currentColor.
describe('BrandIcon — known slug renders the real mark (test #9)', () => {
  it('renders a span wrapper with role="img"', () => {
    const { container } = render(<BrandIcon slug="openai" />)
    const wrapper = container.querySelector('span[role="img"]')
    expect(wrapper).not.toBeNull()
  })

  it('contains an inline <svg> element in the DOM', () => {
    const { container } = render(<BrandIcon slug="openai" />)
    // The sanitized SVG is injected via dangerouslySetInnerHTML
    const svg = container.querySelector('svg')
    expect(svg).not.toBeNull()
  })

  it('sets aria-label to the slug by default', () => {
    render(<BrandIcon slug="openai" />)
    expect(screen.getByRole('img', { name: 'openai' })).toBeTruthy()
  })

  it('honours a custom label prop', () => {
    render(<BrandIcon slug="openai" label="OpenAI" />)
    expect(screen.getByRole('img', { name: 'OpenAI' })).toBeTruthy()
  })

  it('is aria-hidden when decorative=true', () => {
    const { container } = render(<BrandIcon slug="openai" decorative />)
    const wrapper = container.querySelector('[aria-hidden="true"]')
    expect(wrapper).not.toBeNull()
    // No role="img" when decorative
    expect(container.querySelector('[role="img"]')).toBeNull()
  })

  it('applies a custom size via inline style', () => {
    const { container } = render(<BrandIcon slug="openai" size={48} />)
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper.style.width).toBe('48px')
    expect(wrapper.style.height).toBe('48px')
  })

  it('telegram (channel slug) also resolves to an inline SVG', () => {
    const { container } = render(<BrandIcon slug="telegram" />)
    expect(container.querySelector('svg')).not.toBeNull()
  })

  it('anthropic (provider slug) also resolves to an inline SVG', () => {
    const { container } = render(<BrandIcon slug="anthropic" />)
    expect(container.querySelector('svg')).not.toBeNull()
  })
})

// ---------------------------------------------------------------------------
// Test #10 — brandicon.missingSlug.test
// ---------------------------------------------------------------------------
// "__missing__" has no vendored asset. The component must render a lettermark
// chip and must not throw.
describe('BrandIcon — unknown slug falls back to a lettermark (test #10)', () => {
  it('does not throw for an unknown slug', () => {
    expect(() => render(<BrandIcon slug="__missing__" />)).not.toThrow()
  })

  it('renders a lettermark chip (span, not an svg)', () => {
    const { container } = render(<BrandIcon slug="__missing__" />)
    // Lettermark chip: no svg, but a span with text
    const svg = container.querySelector('svg')
    expect(svg).toBeNull()
    const chip = container.querySelector('span')
    expect(chip).not.toBeNull()
  })

  it('shows the first two uppercase letters of the slug', () => {
    const { container } = render(<BrandIcon slug="__missing__" />)
    // slug "__missing__" → strip non-alphanum → "missing" → first 2 upper → "MI"
    expect(container.textContent).toBe('MI')
  })

  it('does not throw for an empty string slug', () => {
    expect(() => render(<BrandIcon slug="" />)).not.toThrow()
  })

  it('shows "?" when the slug yields no letters', () => {
    const { container } = render(<BrandIcon slug="___" />)
    expect(container.textContent).toBe('?')
  })

  it('does not throw for a numeric-only slug', () => {
    expect(() => render(<BrandIcon slug="123" />)).not.toThrow()
  })
})

// ---------------------------------------------------------------------------
// BrandDisclaimer — renders the exact trademark notice
// ---------------------------------------------------------------------------
describe('BrandDisclaimer — trademark notice', () => {
  it('renders without throwing', () => {
    expect(() => render(<BrandDisclaimer />)).not.toThrow()
  })

  it('contains the exact disclaimer text', () => {
    const { container } = render(<BrandDisclaimer />)
    expect(container.textContent).toBe(BRAND_DISCLAIMER_TEXT)
  })

  it('renders a <p> element', () => {
    const { container } = render(<BrandDisclaimer />)
    expect(container.querySelector('p')).not.toBeNull()
  })

  it('BRAND_DISCLAIMER_TEXT is the required notice', () => {
    expect(BRAND_DISCLAIMER_TEXT).toContain('trademarks of their respective owners')
    expect(BRAND_DISCLAIMER_TEXT).toContain('identification only')
    expect(BRAND_DISCLAIMER_TEXT).toContain('no affiliation or endorsement implied')
  })

  it('accepts a custom className', () => {
    const { container } = render(<BrandDisclaimer className="custom-footer" />)
    expect(container.querySelector('.custom-footer')).not.toBeNull()
  })
})

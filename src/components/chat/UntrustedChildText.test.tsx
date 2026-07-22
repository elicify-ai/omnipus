// UntrustedChildText.test.tsx — FE-7 / MAJ-12 React rendering guarantees.
//
// MAJ-12 acceptance (spec US-14 AS-3 / BDD "Untrusted child text renders
// safely"): child-originated text renders as plain text / sanctioned
// markdown, NO raw HTML, links NON-CLICKABLE, untrusted-origin chrome ALWAYS
// visible. This spec covers the render layer; the pure-utility DOMPurify
// pre-clean is covered in src/lib/sanitizeChildText.test.ts.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { UntrustedChildText, UntrustedOriginBadge } from './UntrustedChildText'

describe('UntrustedChildText — untrusted-origin chrome (MAJ-12)', () => {
  it('shows the untrusted-origin badge by default', () => {
    render(<UntrustedChildText text="scanning files" />)
    expect(screen.getByTestId('untrusted-origin-badge')).toBeInTheDocument()
  })

  it('hides the badge when untrustedOrigin=false (trusted reuse path)', () => {
    const { container } = render(<UntrustedChildText text="engine text" untrustedOrigin={false} />)
    expect(container.querySelector('[data-testid="untrusted-origin-badge"]')).toBeNull()
  })

  it('renders nothing for empty/null text', () => {
    const { container } = render(<UntrustedChildText text="" />)
    expect(container.firstChild).toBeNull()
  })
})

describe('UntrustedChildText — no raw HTML ever reaches the DOM', () => {
  it('strips a <script> block (no <script> node, no alert payload)', () => {
    const { container } = render(
      <UntrustedChildText text={'hi <script>alert("xss")</script> there'} />,
    )
    expect(container.querySelector('script')).toBeNull()
    expect(container.textContent).not.toContain('alert')
    expect(container.textContent).toContain('hi')
  })

  it('strips <img onerror=...> entirely (no img node, no onerror attribute)', () => {
    const { container } = render(
      <UntrustedChildText text={'<img src=x onerror=alert(1)>'} />,
    )
    expect(container.querySelector('img')).toBeNull()
    expect(container.innerHTML).not.toContain('onerror')
  })

  it('drops raw <a href="javascript:..."> to plain text, never an anchor', () => {
    const { container } = render(
      <UntrustedChildText text={'<a href="javascript:alert(1)">click</a>'} />,
    )
    // Raw HTML anchor is stripped by sanitize; "click" text survives.
    expect(container.querySelector('a')).toBeNull()
    expect(container.textContent).toContain('click')
    expect(container.innerHTML).not.toContain('javascript')
  })
})

describe('UntrustedChildText — links are non-clickable (MAJ-12 render layer)', () => {
  it('renders a markdown link as INERT text (no <a href>, no clickable anchor)', () => {
    const { container } = render(
      <UntrustedChildText text={'see [docs](https://example.com) here'} />,
    )
    // No real anchor with href anywhere.
    const anchors = container.querySelectorAll('a')
    expect(anchors).toHaveLength(0)
    // The link text + the URL are both shown (URL muted, non-clickable).
    expect(container.textContent).toContain('docs')
    expect(container.textContent).toContain('https://example.com')
  })

  it('a markdown javascript: link renders as INERT text (never an active anchor)', () => {
    const { container } = render(
      <UntrustedChildText text={'[click me](javascript:alert(1))'} />,
    )
    expect(container.querySelectorAll('a')).toHaveLength(0)
    expect(container.innerHTML).not.toContain('href')
    expect(container.textContent).toContain('click me')
  })

  it('an inert link click does NOT navigate (no anchor to click)', () => {
    const { container } = render(
      <UntrustedChildText text={'[x](https://example.com)'} />,
    )
    // There is no <a> to fire a click on — clicking the span is a no-op nav.
    const span = container.querySelector('[data-testid="child-inert-link"]')
    expect(span).not.toBeNull()
    expect(span?.tagName).toBe('SPAN') // not an <a>
    // verify no navigation handler is attached: no onClick spy needed, the
    // element is a <span> with no href, so it cannot navigate by construction.
    expect((span as HTMLElement).tagName).not.toBe('A')
  })
})

describe('UntrustedChildText — sanctioned markdown', () => {
  it('renders bold markdown as <strong>', () => {
    const { container } = render(<UntrustedChildText text={'**bold text**'} />)
    expect(container.querySelector('strong')).not.toBeNull()
    expect(container.textContent).toContain('bold text')
  })

  it('renders inline code markdown as <code>', () => {
    const { container } = render(<UntrustedChildText text={'see `code` here'} />)
    expect(container.querySelector('code')).not.toBeNull()
    expect(container.textContent).toContain('code')
  })

  it('drops images to alt text (never fetches a src)', () => {
    const { container } = render(
      <UntrustedChildText text={'![alt text](https://tracker.example/pixel.png)'} />,
    )
    expect(container.querySelector('img')).toBeNull()
    expect(container.textContent).toContain('image: alt text')
  })
})

describe('UntrustedOriginBadge', () => {
  it('renders standalone with a customizable label', () => {
    render(<UntrustedOriginBadge label="ray (child)" testId="badge-x" />)
    const badge = screen.getByTestId('badge-x')
    expect(badge).toBeInTheDocument()
    expect(badge.textContent).toContain('untrusted')
    expect(badge.getAttribute('title')).toContain('ray (child)')
  })
})

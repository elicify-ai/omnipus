/**
 * markdown-shared.test.tsx — Direct tests for the shared markdown element renderers.
 *
 * markdown-shared.tsx is the single source of truth backing BOTH chat renderers:
 *   • markdown-text.tsx   — live AssistantUI streaming renderer
 *   • historical-markdown.tsx — finalized / reloaded renderer
 *
 * This suite tests the REAL exports directly. A one-line edit to markdown-shared
 * could silently break the live renderer while the existing suite stays green
 * (the live-path tests mock AssistantUI's MarkdownTextPrimitive to null).
 * These tests close that gap.
 *
 * Mocking strategy:
 *   MOCK:  './mermaid-renderer' (MermaidDiagram — avoids loading real mermaid in vitest)
 *   MOCK:  './image-lightbox'   (ImageLightbox sentinel — avoids portal complexity)
 *   REAL:  '@/lib/preview-url'  (resolveEffectivePreview, rewriteLegacyURL — pure functions)
 *   REAL:  '@/lib/url-safe'     (isSafeHref — pure function)
 *   REAL:  '@/lib/phosphor-emoji-icons' (PHOSPHOR_EMOJI_ICONS — the allow-list itself)
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { createElement } from 'react'
import type { ReactNode } from 'react'

// Sentinel mocks — avoids heavy deps, lets us assert routing decisions
vi.mock('./mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => (
    <div data-testid="mermaid-diagram">{code}</div>
  ),
}))

vi.mock('./image-lightbox', () => ({
  ImageLightbox: ({ src, alt }: { src: string; alt?: string }) => (
    <div data-testid="lightbox-sentinel" data-src={src} data-alt={alt ?? ''} />
  ),
}))

// These imports are intentionally AFTER the vi.mock calls (vitest hoists vi.mock above imports)
import {
  codeText,
  classifyFence,
  createLinkRenderer,
  commonMarkdownComponents,
  InlineCode,
  MarkdownImage,
  PhosphorEmojiSpan,
} from './markdown-shared'
import { PHOSPHOR_EMOJI_ICONS } from '@/lib/phosphor-emoji-icons'

// ── codeText ─────────────────────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — codeText (parity-critical text extractor)

describe('codeText — robust text extraction from ReactNode', () => {
  it("plain string 'abc' → 'abc'", () => {
    expect(codeText('abc')).toBe('abc')
  })

  it("array ['a','b','c'] → 'abc' (concatenated, no separator)", () => {
    expect(codeText(['a', 'b', 'c'])).toBe('abc')
  })

  it("nested array ['a',['b','c'],'d'] → 'abcd' (recursive)", () => {
    expect(codeText(['a', ['b', 'c'], 'd'])).toBe('abcd')
  })

  it('React element (e.g. <span>x</span>) → empty string, NOT "[object Object]"', () => {
    const el = createElement('span', null, 'x')
    const result = codeText(el as unknown as ReactNode)
    expect(result).toBe('')
    expect(result).not.toContain('[object Object]')
  })

  it('null → empty string', () => {
    expect(codeText(null as unknown as ReactNode)).toBe('')
  })

  it('undefined → empty string', () => {
    expect(codeText(undefined as unknown as ReactNode)).toBe('')
  })

  it('number → empty string (not stringified)', () => {
    expect(codeText(42 as unknown as ReactNode)).toBe('')
  })

  it("mixed array ['t', <em>x</em>, 'm'] → 'tm' (element children ignored)", () => {
    const em = createElement('em', null, 'x')
    const result = codeText(['t', em as unknown as ReactNode, 'm'])
    expect(result).toBe('tm')
  })

  it('differentiation: two different strings produce two different outputs', () => {
    expect(codeText('const x = 1')).not.toBe(codeText('const y = 2'))
  })
})

// ── classifyFence ─────────────────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — classifyFence (isBlock / language / text routing)

describe('classifyFence — block vs inline, language extraction', () => {
  it("('const x=1','language-ts') → isBlock=true, language='ts'", () => {
    const result = classifyFence('const x=1', 'language-ts')
    expect(result.isBlock).toBe(true)
    expect(result.language).toBe('ts')
    expect(result.text).toBe('const x=1')
  })

  it("('a\\nb', undefined) → isBlock=true, language=undefined (bare multi-line fence)", () => {
    const result = classifyFence('a\nb', undefined)
    expect(result.isBlock).toBe(true)
    expect(result.language).toBeUndefined()
    expect(result.text).toBe('a\nb')
  })

  it("('graph TD','language-mermaid') → isBlock=true, language='mermaid'", () => {
    const result = classifyFence('graph TD', 'language-mermaid')
    expect(result.isBlock).toBe(true)
    expect(result.language).toBe('mermaid')
  })

  it("('inline', undefined) → isBlock=false, language=undefined (single-line, no class)", () => {
    const result = classifyFence('inline', undefined)
    expect(result.isBlock).toBe(false)
    expect(result.language).toBeUndefined()
  })

  it("('x','language-') → language=undefined (empty-lang boundary — regex requires at least one char)", () => {
    const result = classifyFence('x', 'language-')
    expect(result.language).toBeUndefined()
    // Still a block because of the className containing 'language-'
    expect(result.isBlock).toBe(true)
  })

  it('differentiation: ts vs python className → different language values', () => {
    const ts = classifyFence('code', 'language-ts')
    const py = classifyFence('code', 'language-python')
    expect(ts.language).toBe('ts')
    expect(py.language).toBe('python')
    expect(ts.language).not.toBe(py.language)
  })
})

// ── createLinkRenderer ────────────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — createLinkRenderer (rewrite + scheme allow-list)

describe('createLinkRenderer — legacy URL rewrite + scheme sanitization', () => {
  it('rewrites a 0.0.0.0:5000/serve URL to the effectivePreview host:port', () => {
    const MarkdownLink = createLinkRenderer({ hostname: '10.0.0.5', port: 5001 })
    render(
      <MarkdownLink href="http://0.0.0.0:5000/serve/x/y/">link text</MarkdownLink>
    )
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    // Proves rewriteLegacyURL is applied — 0.0.0.0 → 10.0.0.5, port 5001
    expect(link).toHaveAttribute('href', 'http://10.0.0.5:5001/serve/x/y/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('javascript: href → <span> with line-through, NO href, NO anchor, title present', () => {
    const MarkdownLink = createLinkRenderer({ hostname: '10.0.0.5', port: 5001 })
    render(<MarkdownLink href="javascript:alert(1)">evil link</MarkdownLink>)
    const el = screen.getByTestId('markdown-link')
    expect(el.tagName).toBe('SPAN')
    expect(el).not.toHaveAttribute('href')
    expect(el).toHaveAttribute('title', 'Link removed: unsafe URL scheme')
    // line-through class must be present (catches the copy-test drift)
    expect(el.className).toContain('line-through')
    expect(el).toHaveTextContent('evil link')
  })

  it('createLinkRenderer(null) on a legacy href → passes through unchanged (no crash)', () => {
    const MarkdownLink = createLinkRenderer(null)
    render(<MarkdownLink href="http://0.0.0.0:5000/serve/x/y/">pass</MarkdownLink>)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    // No rewrite when effectivePreview is null
    expect(link).toHaveAttribute('href', 'http://0.0.0.0:5000/serve/x/y/')
  })

  it('href undefined → renders without throwing', () => {
    const MarkdownLink = createLinkRenderer({ hostname: 'host', port: 5001 })
    expect(() => {
      render(<MarkdownLink href={undefined}>text</MarkdownLink>)
    }).not.toThrow()
  })

  it('differentiation: two different effectivePreview hosts produce two different rewritten hrefs', () => {
    // Proves createLinkRenderer is not hardcoded — different preview hosts → different output
    const Link1 = createLinkRenderer({ hostname: 'host1.local', port: 5001 })
    const Link2 = createLinkRenderer({ hostname: 'host2.local', port: 5001 })

    const { unmount: u1, getByTestId: g1 } = render(
      <Link1 href="http://0.0.0.0:5000/serve/a/b/">link</Link1>
    )
    const href1 = g1('markdown-link').getAttribute('href')
    u1()

    const { getByTestId: g2 } = render(
      <Link2 href="http://0.0.0.0:5000/serve/a/b/">link</Link2>
    )
    const href2 = g2('markdown-link').getAttribute('href')

    expect(href1).toBe('http://host1.local:5001/serve/a/b/')
    expect(href2).toBe('http://host2.local:5001/serve/a/b/')
    expect(href1).not.toBe(href2)
  })

  it('safe https link → <a> with correct href, target=_blank, rel=noopener noreferrer', () => {
    const MarkdownLink = createLinkRenderer({ hostname: '10.0.0.5', port: 5001 })
    render(<MarkdownLink href="https://github.com/user/repo">GitHub</MarkdownLink>)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link).toHaveAttribute('href', 'https://github.com/user/repo')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })
})

// ── commonMarkdownComponents ──────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — commonMarkdownComponents (parity-critical styles)

describe('commonMarkdownComponents — canonical element renderers', () => {
  const P = commonMarkdownComponents.p
  const H2 = commonMarkdownComponents.h2
  const H3 = commonMarkdownComponents.h3
  const Strong = commonMarkdownComponents.strong
  const Em = commonMarkdownComponents.em
  const Ul = commonMarkdownComponents.ul
  const Ol = commonMarkdownComponents.ol
  const Li = commonMarkdownComponents.li
  const Th = commonMarkdownComponents.th
  const Td = commonMarkdownComponents.td
  const Hr = commonMarkdownComponents.hr

  // p — the drift that bit before: whitespace-pre-wrap AND secondary text color
  it('p → className includes whitespace-pre-wrap AND text-[var(--color-secondary)]', () => {
    const { container } = render(<P>text</P>)
    const p = container.querySelector('p')!
    expect(p.className).toContain('whitespace-pre-wrap')
    expect(p.className).toContain('text-[var(--color-secondary)]')
  })

  it('h2 → renders as <h2> element', () => {
    const { container } = render(<H2>Heading Two</H2>)
    expect(container.querySelector('h2')).toBeInTheDocument()
    expect(container.querySelector('h2')).toHaveTextContent('Heading Two')
  })

  it('h3 → renders as <h3> element', () => {
    const { container } = render(<H3>Heading Three</H3>)
    expect(container.querySelector('h3')).toBeInTheDocument()
  })

  it('strong → <strong> with font-semibold class', () => {
    const { container } = render(<Strong>bold</Strong>)
    const el = container.querySelector('strong')!
    expect(el).toBeInTheDocument()
    expect(el.className).toContain('font-semibold')
  })

  it('em → <em> with italic class', () => {
    const { container } = render(<Em>italic</Em>)
    const el = container.querySelector('em')!
    expect(el).toBeInTheDocument()
    expect(el.className).toContain('italic')
  })

  it('ul → inline style listStyleType disc', () => {
    const { container } = render(<Ul>items</Ul>)
    const ul = container.querySelector('ul')!
    expect(ul.style.listStyleType).toBe('disc')
  })

  it('ol → inline style listStyleType decimal', () => {
    const { container } = render(<Ol>items</Ol>)
    const ol = container.querySelector('ol')!
    expect(ol.style.listStyleType).toBe('decimal')
  })

  it('ol with start={7} → rendered <ol> has start=7 (forwarded prop — regression guard)', () => {
    const { container } = render(<Ol start={7}>items</Ol>)
    const ol = container.querySelector('ol')!
    expect(ol).toHaveAttribute('start', '7')
  })

  it('li → inline style display list-item', () => {
    const { container } = render(<Li>item</Li>)
    const li = container.querySelector('li')!
    expect(li.style.display).toBe('list-item')
  })

  it('th with style={{textAlign:"center"}} → <th> carries that style (GFM alignment forwarding)', () => {
    const { container } = render(<Th style={{ textAlign: 'center' }}>H</Th>)
    const th = container.querySelector('th')!
    expect(th.style.textAlign).toBe('center')
  })

  it('td with style={{textAlign:"right"}} → <td> carries that style (GFM alignment forwarding)', () => {
    const { container } = render(<Td style={{ textAlign: 'right' }}>D</Td>)
    const td = container.querySelector('td')!
    expect(td.style.textAlign).toBe('right')
  })

  it('hr → renders as <hr>', () => {
    const { container } = render(<Hr />)
    expect(container.querySelector('hr')).toBeInTheDocument()
  })
})

// ── InlineCode ────────────────────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — InlineCode

describe('InlineCode — styled inline code element', () => {
  it('renders as <code> with font-mono and bg-[var(--color-surface-2)] classes', () => {
    const { container } = render(<InlineCode>const x = 1</InlineCode>)
    const code = container.querySelector('code')!
    expect(code).toBeInTheDocument()
    expect(code.className).toContain('font-mono')
    expect(code.className).toContain('bg-[var(--color-surface-2)]')
    expect(code).toHaveTextContent('const x = 1')
  })

  it('renders children correctly', () => {
    const { container } = render(<InlineCode>npm install</InlineCode>)
    expect(container.querySelector('code')).toHaveTextContent('npm install')
  })
})

// ── MarkdownImage ─────────────────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — MarkdownImage

describe('MarkdownImage — safe src, unsafe rejection, lightbox', () => {
  it('safe src → <img> with loading=lazy, role=button, aria-label', () => {
    const { container } = render(
      <MarkdownImage src="https://example.com/photo.png" alt="a photo" />
    )
    const img = container.querySelector('img')!
    expect(img).toBeInTheDocument()
    expect(img).toHaveAttribute('loading', 'lazy')
    expect(img).toHaveAttribute('role', 'button')
    expect(img).toHaveAttribute('aria-label', 'View: a photo')
  })

  it('clicking the image mounts the mocked ImageLightbox sentinel', () => {
    const { container } = render(
      <MarkdownImage src="https://example.com/photo.png" alt="a photo" />
    )
    const img = container.querySelector('img')!
    fireEvent.click(img)
    const lightbox = screen.getByTestId('lightbox-sentinel')
    expect(lightbox).toBeInTheDocument()
    expect(lightbox).toHaveAttribute('data-src', 'https://example.com/photo.png')
  })

  it('pressing Enter on the image mounts the lightbox (keyboard accessible)', () => {
    const { container } = render(
      <MarkdownImage src="https://example.com/photo.png" alt="kbtest" />
    )
    const img = container.querySelector('img')!
    fireEvent.keyDown(img, { key: 'Enter' })
    expect(screen.getByTestId('lightbox-sentinel')).toBeInTheDocument()
  })

  it('unsafe src (javascript:x) with alt → muted "[image: alt]" span, no <img>', () => {
    const { container } = render(
      <MarkdownImage src="javascript:x" alt="bad image" />
    )
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('[image: bad image]')).toBeInTheDocument()
  })

  it('unsafe src (javascript:x) without alt → null (nothing rendered)', () => {
    const { container } = render(<MarkdownImage src="javascript:x" />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.firstChild).toBeNull()
  })

  it('src undefined → null (nothing rendered)', () => {
    const { container } = render(<MarkdownImage src={undefined} />)
    expect(container.firstChild).toBeNull()
  })

  it('differentiation: two different safe srcs → two different img[src] attributes', () => {
    const { unmount } = render(<MarkdownImage src="https://a.com/1.png" alt="img1" />)
    const img1 = document.querySelector('img')
    const src1 = img1?.getAttribute('src')
    unmount()

    render(<MarkdownImage src="https://b.com/2.png" alt="img2" />)
    const img2 = document.querySelector('img')
    const src2 = img2?.getAttribute('src')

    expect(src1).toBe('https://a.com/1.png')
    expect(src2).toBe('https://b.com/2.png')
    expect(src1).not.toBe(src2)
  })
})

// ── PhosphorEmojiSpan ─────────────────────────────────────────────────────────
// Traces to: markdown-shared.tsx — PhosphorEmojiSpan (PHOSPHOR_EMOJI_ICONS allow-list)

describe('PhosphorEmojiSpan — icon rendering from allow-list', () => {
  // Pick a REAL key from the actual allow-list so we guard against list changes
  const realIconName = Object.keys(PHOSPHOR_EMOJI_ICONS)[0] as string // e.g. 'CheckCircle'

  it('known icon name via data-phosphor-icon → renders an SVG (not a plain span)', () => {
    const { container } = render(
      <PhosphorEmojiSpan data-phosphor-icon={realIconName}>fallback</PhosphorEmojiSpan>
    )
    // Phosphor icons render as <svg> elements
    const svg = container.querySelector('svg')
    expect(svg).toBeInTheDocument()
    // Must NOT render as plain text span with children
    expect(container.querySelector('span')).toBeNull()
  })

  it('unknown icon name → renders as <span> with children', () => {
    const { container } = render(
      <PhosphorEmojiSpan data-phosphor-icon="NonExistentIcon9999">child text</PhosphorEmojiSpan>
    )
    const span = container.querySelector('span')!
    expect(span).toBeInTheDocument()
    expect(span).toHaveTextContent('child text')
    expect(container.querySelector('svg')).toBeNull()
  })

  it('no data-phosphor-icon attr → plain <span> with children', () => {
    const { container } = render(<PhosphorEmojiSpan>plain</PhosphorEmojiSpan>)
    const span = container.querySelector('span')!
    expect(span).toBeInTheDocument()
    expect(span).toHaveTextContent('plain')
  })

  it('differentiation: two different known icon names render different SVGs', () => {
    const keys = Object.keys(PHOSPHOR_EMOJI_ICONS)
    // Need at least two icons to differentiate
    expect(keys.length).toBeGreaterThanOrEqual(2)

    const icon1 = keys[0]
    const icon2 = keys[1]

    const { container: c1, unmount: u1 } = render(
      <PhosphorEmojiSpan data-phosphor-icon={icon1} />
    )
    const svg1 = c1.querySelector('svg')?.outerHTML
    u1()

    const { container: c2 } = render(
      <PhosphorEmojiSpan data-phosphor-icon={icon2} />
    )
    const svg2 = c2.querySelector('svg')?.outerHTML

    // Both render SVGs (both are valid icons)
    expect(svg1).toBeTruthy()
    expect(svg2).toBeTruthy()
    // Different icons → different SVG markup (aria-label or path data differs)
    expect(svg1).not.toBe(svg2)
  })
})

/**
 * Dependency-upgrade smoke tests for the real finalized-chat markdown pipeline.
 * Mermaid, remark-math, rehype-katex, KaTeX and DOMPurify are deliberately NOT mocked.
 * Expected diagram labels/edges and formula annotations come from the supplied source
 * (upgrade requirement R2), not a snapshot of either dependency version's output.
 * SVG measurement shims bridge jsdom's missing browser geometry only; these tests
 * verify generated content and routing, not pixel layout or font measurements.
 */
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { HistoricalMessageMarkdown } from './historical-markdown'

const measurements = {
  getBBox: () => new DOMRect(0, 0, 120, 24),
  getComputedTextLength: () => 120,
}
const installedMeasurements: string[] = []

beforeAll(() => {
  for (const [name, measure] of Object.entries(measurements)) {
    if (!(name in SVGElement.prototype)) {
      Object.defineProperty(SVGElement.prototype, name, { configurable: true, value: measure })
      installedMeasurements.push(name)
    }
  }
})

afterAll(() => {
  for (const name of installedMeasurements) Reflect.deleteProperty(SVGElement.prototype, name)
})

describe('chat markdown with real diagram and math dependencies', () => {
  it('renders a Mermaid fence as a two-node SVG and keeps the legacy layout and look', async () => {
    const { container } = render(
      <HistoricalMessageMarkdown content={'```mermaid\nflowchart TD\n  A[Start] --> B[Done]\n```'} />,
    )

    const svg = await waitFor(() => {
      const diagrams = container.querySelectorAll<SVGSVGElement>('svg[id^="mermaid-"]')
      expect(diagrams).toHaveLength(1)
      return diagrams[0]
    })
    expect(svg.querySelectorAll('g.node')).toHaveLength(2)
    expect(Array.from(svg.querySelectorAll('g.node'), (node) => node.textContent?.trim())).toEqual([
      'Start',
      'Done',
    ])
    expect(svg.querySelectorAll('path.flowchart-link')).toHaveLength(1)
    expect(container.querySelector('pre')).toBeNull()
    expect(container.querySelector('[role="status"]')).toBeNull()

    // Inspect the real initialized dependency, without replacing initialize/render.
    const { default: mermaid } = await import('mermaid')
    expect(mermaid.mermaidAPI.getConfig()).toMatchObject({
      layout: 'dagre',
      look: 'classic',
      theme: 'base',
      securityLevel: 'strict',
      suppressErrorRendering: true,
    })
  })

  it('renders inline and display math with the exact source in MathML annotations', () => {
    const { container } = render(
      <HistoricalMessageMarkdown content={'Inline $x^2$.\n\n$$\n\\frac{1}{2}\n$$'} />,
    )

    expect(container.querySelectorAll('.katex')).toHaveLength(2)
    expect(container.querySelectorAll('.katex-display')).toHaveLength(1)
    expect(container.querySelectorAll('.katex-html')).toHaveLength(2)
    expect(Array.from(container.querySelectorAll('annotation[encoding="application/x-tex"]'),
      (annotation) => annotation.textContent)).toEqual(['x^2', '\\frac{1}{2}'])
    expect(container.querySelector('.katex-error')).toBeNull()
  })

  it('leaves escaped math delimiters and dollar signs inside inline code literal', () => {
    const { container } = render(
      <HistoricalMessageMarkdown content={'Escaped \\$x^2\\$ and code `$y^2$`.'} />,
    )

    expect(container.querySelectorAll('.katex')).toHaveLength(0)
    expect(container.querySelector('code')?.textContent).toBe('$y^2$')
    expect(container.textContent).toBe('Escaped $x^2$ and code $y^2$.')
  })

  it('retains an unclosed math group with its specific parse error instead of dropping it', () => {
    // Characterization of the existing missing-group error, not KaTeX's macro-argument path.
    const { container } = render(
      <HistoricalMessageMarkdown content={'${x$'} />,
    )

    const error = container.querySelector('.katex-error')
    expect(error).toHaveTextContent('{x')
    expect(error).toHaveAttribute('title', expect.stringContaining("Expected '}', got 'EOF'"))
    expect(container.querySelector('math')).toBeNull()
  })
})

/**
 * Dependency-upgrade smoke tests for the real finalized-chat markdown pipeline.
 * Mermaid, remark-math, rehype-katex, KaTeX and DOMPurify are deliberately NOT mocked.
 * Expected diagram labels/edges and formula annotations come from the supplied source
 * (upgrade requirement R2), not a snapshot of either dependency version's output.
 * SVG/Canvas platform stand-ins bridge jsdom's missing browser APIs; these tests
 * verify real diagram content and layout selection, not pixels or font metrics.
 * Cytoscape and the Cose-Bilkent algorithm are not replaced or mocked.
 */
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import type { FlowDB } from 'mermaid/dist/diagrams/flowchart/flowDb.js'
import type { MindmapDB } from 'mermaid/dist/diagrams/mindmap/mindmapDb.js'
import { HistoricalMessageMarkdown } from './historical-markdown'

const measurements = {
  getBBox: () => new DOMRect(0, 0, 120, 24),
  getComputedTextLength: () => 120,
}
const installedMeasurements: string[] = []
const originalCanvasContext = Object.getOwnPropertyDescriptor(HTMLCanvasElement.prototype, 'getContext')
const canvasContexts = new WeakMap<HTMLCanvasElement, CanvasRenderingContext2D>()
const cytoscapeInstances = new Set<{ destroy(): void }>()

function canvasContext(canvas: HTMLCanvasElement): CanvasRenderingContext2D {
  // Cose's real Cytoscape instance uses an off-screen Canvas renderer to measure
  // text and calculate edge endpoints before Mermaid produces the actual SVG.
  // Supply only the browser API boundary: paint operations do not draw pixels.
  const paint = () => {}
  const paintMethods = [
    'arc', 'arcTo', 'beginPath', 'clearRect', 'clip', 'closePath', 'drawImage',
    'ellipse', 'fill', 'fillRect', 'fillText', 'lineTo', 'moveTo', 'quadraticCurveTo',
    'rect', 'restore', 'rotate', 'save', 'scale', 'setLineDash', 'setTransform',
    'stroke', 'strokeRect', 'strokeText', 'translate',
  ]
  const gradient = () => ({ addColorStop: paint })
  return {
    ...Object.fromEntries(paintMethods.map((name) => [name, paint])),
    canvas,
    measureText: (text: string) => ({ width: text.length * 8 }),
    createLinearGradient: gradient,
    createRadialGradient: gradient,
    createPattern: () => null,
  } as unknown as CanvasRenderingContext2D
}

beforeAll(() => {
  for (const [name, measure] of Object.entries(measurements)) {
    if (!(name in SVGElement.prototype)) {
      Object.defineProperty(SVGElement.prototype, name, { configurable: true, value: measure })
      installedMeasurements.push(name)
    }
  }
  Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', {
    configurable: true,
    value(this: HTMLCanvasElement, kind: string) {
      if (kind !== '2d') return null
      // Retain the real instance solely to destroy its timers after each test.
      const container = document.getElementById('cy') as
        (HTMLElement & { _cyreg?: { cy?: { destroy(): void } } }) | null
      if (container?._cyreg?.cy) cytoscapeInstances.add(container._cyreg.cy)
      let context = canvasContexts.get(this)
      if (!context) {
        context = canvasContext(this)
        canvasContexts.set(this, context)
      }
      return context
    },
  })
})

afterEach(() => {
  for (const instance of cytoscapeInstances) instance.destroy()
  cytoscapeInstances.clear()
})

afterAll(() => {
  for (const name of installedMeasurements) Reflect.deleteProperty(SVGElement.prototype, name)
  if (originalCanvasContext) {
    Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', originalCanvasContext)
  } else {
    Reflect.deleteProperty(HTMLCanvasElement.prototype, 'getContext')
  }
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

    // Inspect the flowchart-specific config: Mermaid resets global scope after render.
    const { default: mermaid } = await import('mermaid')
    const diagram = await mermaid.mermaidAPI.getDiagramFromText('flowchart TD\n  A[Start] --> B[Done]')
    expect(diagram.type).toBe('flowchart-v2')
    expect((diagram.db as FlowDB).getData().config.flowchart?.layout).toBe('dagre')
    expect(mermaid.mermaidAPI.getConfig()).toMatchObject({
      look: 'classic',
      theme: 'base',
      securityLevel: 'strict',
      suppressErrorRendering: true,
    })
    expect(mermaid.mermaidAPI.getSiteConfig()).toMatchObject({
      flowchart: { layout: 'dagre' },
      state: { layout: 'dagre' },
      class: { layout: 'dagre' },
      er: { layout: 'dagre' },
      requirement: { layout: 'dagre' },
      usecase: { layout: 'dagre' },
      agentflow: { layout: 'dagre' },
    })
  })

  it('renders an unconfigured mindmap with its original cose-bilkent layout', async () => {
    const source = 'mindmap\n  root((MindmapRoot))\n    MindmapBranch\n      MindmapLeaf'
    const { container } = render(
      <HistoricalMessageMarkdown content={`\`\`\`mermaid\n${source}\n\`\`\``} />,
    )

    const svg = await waitFor(() => {
      const diagrams = container.querySelectorAll<SVGSVGElement>('svg[id^="mermaid-"]')
      expect(diagrams).toHaveLength(1)
      return diagrams[0]
    })
    expect(svg).toHaveTextContent('MindmapRoot')
    expect(svg).toHaveTextContent('MindmapBranch')
    expect(svg).toHaveTextContent('MindmapLeaf')
    expect(container.querySelector('pre')).toBeNull()
    expect(container.querySelector('[role="status"]')).toBeNull()

    // The documented mindmap exception selects this real layout input, not the
    // global config's default. Merely getting an SVG would also pass with Dagre.
    const { default: mermaid } = await import('mermaid')
    const diagram = await mermaid.mermaidAPI.getDiagramFromText(source)
    expect(diagram.type).toBe('mindmap')
    const data = (diagram.db as MindmapDB).getData()
    expect(data.nodes).toHaveLength(3)
    expect(data.edges).toHaveLength(2)
    expect(data.config.layout).toBe('cose-bilkent')
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

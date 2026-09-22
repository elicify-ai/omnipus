/**
 * zoomable-view-canvas.test.tsx — unit coverage for the React Flow canvas
 * preset's DYNAMIC zoom floor (2026-09-21 fix, the "Canvas preset,
 * effective-floor gap" note in
 * docs/internal/design/components/zoomable-view.md): `useZoomableCanvasPill`'s
 * `min` (passed to `<ReactFlow minZoom>` and the pill's own zoom-out
 * disable) and `useZoomableCanvasOpeningFit` (the imperative opening fit
 * that replaces the declarative `fitView` prop) must both reach BELOW the
 * default 25% floor for an oversized graph — one with more nodes than fit
 * at 25% — and stay at the default 25% for one that comfortably fits.
 *
 * jsdom has no real layout, so — mirroring
 * `src/components/workspaces/graph/GraphView.test.tsx`'s own approach — the
 * frame size is stubbed via offsetWidth/offsetHeight/clientWidth/
 * clientHeight (React Flow measures its wrapper element on mount) and node
 * geometry via `getBoundingClientRect`. A node's POSITION (not its
 * per-node measured size, which is the same stub for every node) is what
 * spreads a "huge" graph's bounding box past the stubbed 800x600 frame.
 */

import { render, screen } from '@testing-library/react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { FitViewOptions, Node } from '@xyflow/react'
import { ReactFlow, ReactFlowProvider } from '@xyflow/react'
import { useZoomableCanvasOpeningFit, useZoomableCanvasPill } from './zoomable-view-canvas'

// Wraps the REAL `useReactFlow`/`fitView` (mirrors GraphView.test.tsx's own
// mock) so `useZoomableCanvasOpeningFit`'s imperative calls can be counted
// and inspected without needing real pixel measurement.
const fitViewCalls: unknown[] = []
vi.mock('@xyflow/react', async () => {
  const actual = await vi.importActual<typeof import('@xyflow/react')>('@xyflow/react')
  return {
    ...actual,
    useReactFlow: (...args: Parameters<typeof actual.useReactFlow>) => {
      const real = actual.useReactFlow(...args)
      return {
        ...real,
        fitView: (options?: Parameters<typeof real.fitView>[0]) => {
          fitViewCalls.push(options)
          return real.fitView(options)
        },
      }
    },
  }
})

beforeAll(() => {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  const g = globalThis as unknown as Record<string, unknown>
  g.ResizeObserver = ResizeObserverStub
  g.DOMMatrixReadOnly = class {
    m22 = 1
  }
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, value: 600 })
  if (!Element.prototype.getBoundingClientRect) return
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 150, height: 60, top: 0, left: 0, right: 150, bottom: 60, toJSON: () => ({}),
  } as DOMRect)
})

beforeEach(() => {
  fitViewCalls.length = 0
})

/** Two nodes, 200px apart — comfortably inside the stubbed 800x600 frame at
 *  any scale >= 25%, so the dynamic floor stays at the default. */
function smallNodes(): Node[] {
  return [
    { id: 'a', position: { x: 0, y: 0 }, data: {} },
    { id: 'b', position: { x: 200, y: 0 }, data: {} },
  ]
}

/** 60 nodes on a 10-wide grid, 600px apart — a ~5400x3000 bounding box, far
 *  larger than the stubbed 800x600 frame even at the 25% floor (which would
 *  still need a 2160x1200 canvas to hold it). Forces the true fit-to-frame
 *  scale below 0.25. */
function hugeNodes(): Node[] {
  const nodes: Node[] = []
  for (let i = 0; i < 60; i++) {
    nodes.push({
      id: `n${i}`,
      position: { x: (i % 10) * 600, y: Math.floor(i / 10) * 600 },
      data: {},
    })
  }
  return nodes
}

function PillProbe({ nodes, fitViewOptions }: { nodes: Node[]; fitViewOptions?: FitViewOptions }) {
  const pill = useZoomableCanvasPill({ fitViewOptions })
  return (
    <div>
      <ReactFlow nodes={nodes} edges={[]} minZoom={pill.min} maxZoom={pill.max} />
      <span data-testid="pill-min">{pill.min}</span>
      <span data-testid="pill-zoom-out-disabled">{String(pill.zoom <= pill.min)}</span>
    </div>
  )
}

function renderPill(nodes: Node[], fitViewOptions?: FitViewOptions) {
  return render(
    <ReactFlowProvider>
      <PillProbe nodes={nodes} fitViewOptions={fitViewOptions} />
    </ReactFlowProvider>,
  )
}

describe('useZoomableCanvasPill — the dynamic interactive zoom floor (2026-09-21 fix)', () => {
  it('stays at the default 25% floor for content that comfortably fits the frame', () => {
    renderPill(smallNodes())
    expect(Number(screen.getByTestId('pill-min').textContent)).toBe(0.25)
  })

  it('drops BELOW 25% for an oversized graph — Fit must always be reachable', () => {
    renderPill(hugeNodes())
    const min = Number(screen.getByTestId('pill-min').textContent)
    expect(min).toBeLessThan(0.25)
    expect(min).toBeGreaterThan(0)
  })

  it('recomputes when the NODE SET changes without the frame resizing — the pre-fix bug: a `useMemo` keyed on `getNodes` (a stable function identity) never re-ran on a node-only change', () => {
    const { rerender } = render(
      <ReactFlowProvider>
        <PillProbe nodes={smallNodes()} />
      </ReactFlowProvider>,
    )
    expect(Number(screen.getByTestId('pill-min').textContent)).toBe(0.25)

    rerender(
      <ReactFlowProvider>
        <PillProbe nodes={hugeNodes()} />
      </ReactFlowProvider>,
    )

    const min = Number(screen.getByTestId('pill-min').textContent)
    expect(min).toBeLessThan(0.25)
  })
})

function OpeningFitProbe({ nodes, fitViewOptions }: { nodes: Node[]; fitViewOptions?: FitViewOptions }) {
  useZoomableCanvasOpeningFit(fitViewOptions)
  return <ReactFlow nodes={nodes} edges={[]} />
}

describe('useZoomableCanvasOpeningFit — drives the opening fit imperatively (replaces the declarative `fitView` prop)', () => {
  it("calls fitView exactly once, with the caller's own fitViewOptions unmodified, once the frame and nodes are known", () => {
    const options: FitViewOptions = { padding: 0.25, maxZoom: 1.1, minZoom: 0.8 }
    render(
      <ReactFlowProvider>
        <OpeningFitProbe nodes={smallNodes()} fitViewOptions={options} />
      </ReactFlowProvider>,
    )
    expect(fitViewCalls).toHaveLength(1)
    expect(fitViewCalls[0]).toBe(options)
  })

  it('does not fire again on an unrelated re-render — one-shot, same semantics as the removed declarative `fitView` prop', () => {
    const options: FitViewOptions = { padding: 0.25, maxZoom: 1.1 }
    const { rerender } = render(
      <ReactFlowProvider>
        <OpeningFitProbe nodes={smallNodes()} fitViewOptions={options} />
      </ReactFlowProvider>,
    )
    expect(fitViewCalls).toHaveLength(1)

    rerender(
      <ReactFlowProvider>
        <OpeningFitProbe nodes={smallNodes()} fitViewOptions={options} />
      </ReactFlowProvider>,
    )

    expect(fitViewCalls).toHaveLength(1)
  })

  it('drives the opening fit for an oversized graph too — the exact gap this fix closes: the declarative prop clipped at 25%, this fires imperatively with the caller\'s own (possibly sub-25%-reaching) options', () => {
    const options: FitViewOptions = { padding: 0.25, maxZoom: 1.1 }
    render(
      <ReactFlowProvider>
        <OpeningFitProbe nodes={hugeNodes()} fitViewOptions={options} />
      </ReactFlowProvider>,
    )
    expect(fitViewCalls).toHaveLength(1)
    expect(fitViewCalls[0]).toBe(options)
  })
})

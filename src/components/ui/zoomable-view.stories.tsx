import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, waitFor, within } from 'storybook/test'
import { Panel, ReactFlow, ReactFlowProvider, type Node } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { ZoomPill, ZoomableMediaSurface, clampZoomScale, useZoomableViewKeyboard } from './zoomable-view'
import {
  useZoomableCanvasOpeningFit,
  useZoomableCanvasPill,
  zoomableCanvasFlowProps,
} from '@/lib/zoomable-view-canvas'

// Fixture mirrors the shape a Phase-2 consumer will actually wire: a
// focusable frame owning the `+ − 0 1` shortcuts (D18: "while the view is
// focused"), and a controlled `zoom` fraction driving the pill. `Fit`
// resolves to 75% here (not 100%) specifically so the Fit/reset stories can
// tell "reset happened" apart from "already at 100%".
function Fixture({ withSelection = false }: { withSelection?: boolean }) {
  const [zoom, setZoom] = useState(0.75)
  const [selectionCount, setSelectionCount] = useState(0)
  const handlers = {
    onZoomIn: () => setZoom((z) => clampZoomScale(z * 1.25)),
    onZoomOut: () => setZoom((z) => clampZoomScale(z / 1.25)),
    onFit: () => setZoom(0.75),
    onZoomTo100: () => setZoom(1),
  }
  const onKeyDown = useZoomableViewKeyboard(handlers)
  return (
    <div tabIndex={0} data-testid="zoomable-view-frame" onKeyDown={onKeyDown}>
      <ZoomPill
        {...handlers}
        zoom={zoom}
        onZoomToSelection={withSelection ? () => setSelectionCount((n) => n + 1) : undefined}
      />
      <span data-testid="zoomable-view-selection-count">{selectionCount}</span>
    </div>
  )
}

const meta = {
  title: 'Design System/ZoomableView',
  component: ZoomPill,
  args: {
    zoom: 0.75,
    onZoomIn: () => {},
    onZoomOut: () => {},
    onFit: () => {},
    onZoomTo100: () => {},
  },
  render: () => <Fixture />,
  parameters: {
    designSystem: {
      keyboard: [
        {
          trigger: '[data-testid="zoomable-view-zoom-out"]',
          key: 'Tab',
          expectFocus: '[data-testid="zoomable-view-percent"]',
        },
      ],
      pointerTargets: [
        '[data-testid="zoomable-view-zoom-out"]',
        '[data-testid="zoomable-view-percent"]',
        '[data-testid="zoomable-view-zoom-in"]',
      ],
      motionTargets: ['[data-testid="zoomable-view-percent"]'],
      forcedColorTargets: ['[data-testid="zoomable-view-zoom-out"]'],
      forcedColors: {
        differences: [
          {
            cue: 'foreground',
            selector: '[data-testid="zoomable-view-zoom-out"]',
            property: 'color',
            againstSelector: '[data-testid="zoomable-view-zoom-out"]',
            againstProperty: 'backgroundColor',
          },
        ],
        focus: ['[data-testid="zoomable-view-zoom-out"]'],
        states: [],
      },
      reflowExemptions: [],
      browserAssertions: [
        { selector: '[data-testid="zoomable-view-zoom-out"]', attribute: 'aria-label', value: 'Zoom out' },
      ],
    },
  },
} satisfies Meta<typeof ZoomPill>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}

export const AccessibleNames: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await expect(canvas.getByRole('group', { name: 'Zoom' })).toBeInTheDocument()
    await expect(canvas.getByRole('button', { name: 'Zoom out' })).toBeInTheDocument()
    await expect(canvas.getByRole('button', { name: 'Zoom in' })).toBeInTheDocument()
    await expect(canvas.getByRole('button', { name: 'Zoom 75 percent — open zoom menu' })).toBeInTheDocument()
  },
}

export const ZoomInOut: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByTestId('zoomable-view-zoom-in'))
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('94%')
    await userEvent.click(canvas.getByTestId('zoomable-view-zoom-out'))
    await userEvent.click(canvas.getByTestId('zoomable-view-zoom-out'))
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('60%')
  },
}

export const OpenMenu: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByTestId('zoomable-view-percent'))
    const menu = within(document.body)
    await expect(menu.getByRole('menuitem', { name: /Fit/ })).toBeVisible()
    await expect(menu.getByRole('menuitem', { name: /100%/ })).toBeVisible()
  },
}

export const SelectFit: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    // Move off the Fit scale first so "Fit" is observably a reset, not a no-op.
    await userEvent.click(canvas.getByTestId('zoomable-view-zoom-in'))
    await userEvent.click(canvas.getByTestId('zoomable-view-zoom-in'))
    await userEvent.click(canvas.getByTestId('zoomable-view-percent'))
    await userEvent.click(within(document.body).getByRole('menuitem', { name: /Fit/ }))
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('75%')
  },
}

export const ZoomToSelection: Story = {
  render: () => <Fixture withSelection />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByTestId('zoomable-view-percent'))
    await userEvent.click(within(document.body).getByRole('menuitem', { name: 'Zoom to selection' }))
    await expect(canvas.getByTestId('zoomable-view-selection-count')).toHaveTextContent('1')
  },
}

export const KeyboardShortcuts: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const frame = canvas.getByTestId('zoomable-view-frame')
    frame.focus()
    await userEvent.keyboard('+')
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('94%')
    await userEvent.keyboard('1')
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('100%')
    await userEvent.keyboard('-')
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('80%')
    await userEvent.keyboard('0')
    await expect(canvas.getByTestId('zoomable-view-percent')).toHaveTextContent('75%')
  },
}

export const NarrowViewport: Story = { parameters: { viewport: { defaultViewport: 'mobile1' } } }

/** REAL-BROWSER regression guard (founder-approved clarification, 2026-09-21):
 *  very tall/large content must still open FITTED, never overflowing its
 *  frame. Before the effective-floor fix, `clampZoomScale` raised any fit
 *  below 25% up to the 25% floor, so content whose fit is under 25% (this
 *  portrait 1500x5000 into a viewport-sized frame fits at roughly 17%)
 *  opened too large and overflowed the frame — reproduced live in Chromium,
 *  Firefox and WebKit (`docs/internal/design/components/zoomable-view.md`,
 *  "Range"). Modelled on the lead's throwaway probe
 *  (`src/components/chat/zz-probe-lightbox.stories.tsx`, since deleted): a
 *  fixed-size `<div>` (not an `<img>`) as content, so this check measures
 *  ONLY `ZoomableMediaSurface`'s own fit math — never Tailwind's global
 *  `img { max-width: 100%; height: auto }` preflight rule, which is a
 *  SEPARATE, consumer-side factor (see the root-cause writeup) that a plain
 *  `<img>` with no explicit size would additionally compound. */
export const TallContentFitsFrame: Story = {
  parameters: { layout: 'fullscreen' },
  render: () => (
    <div style={{ width: '100vw', height: '100vh' }}>
      <ZoomableMediaSurface contentSize={{ width: 1500, height: 5000 }} aria-label="Tall content">
        <div
          data-testid="tall-content"
          style={{
            width: 1500,
            height: 5000,
            background: 'repeating-linear-gradient(45deg, #1a2b4a, #1a2b4a 20px, #2c4a7c 20px, #2c4a7c 40px)',
          }}
        />
      </ZoomableMediaSurface>
    </div>
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const surface = await canvas.findByTestId('zoomable-media-surface')
    // The frame's ResizeObserver measurement and the D18 opening-scale
    // effect are async — jsdom has no ResizeObserver (`useZoomableMedia`'s
    // own comment), but this story runs in a REAL browser
    // (`.storybook/vitest.config.ts`), where it settles within a frame or two.
    await waitFor(() => expect(surface.getAttribute('data-fit')).toBe('true'))
    const content = canvas.getByTestId('tall-content')
    const frameRect = surface.getBoundingClientRect()
    const contentRect = content.getBoundingClientRect()
    const evidence = JSON.stringify({
      frame: `${Math.round(frameRect.width)}x${Math.round(frameRect.height)}`,
      content: `${Math.round(contentRect.width)}x${Math.round(contentRect.height)}`,
      contentTop: Math.round(contentRect.top),
      contentLeft: Math.round(contentRect.left),
      scale: surface.getAttribute('data-scale'),
    })
    expect(contentRect.left, evidence).toBeGreaterThanOrEqual(frameRect.left - 1)
    expect(contentRect.top, evidence).toBeGreaterThanOrEqual(frameRect.top - 1)
    expect(contentRect.right, evidence).toBeLessThanOrEqual(frameRect.right + 1)
    expect(contentRect.bottom, evidence).toBeLessThanOrEqual(frameRect.bottom + 1)
  },
}

// ── Canvas preset (React Flow face) — dynamic zoom floor (2026-09-21) ──────

// 126 nodes on a 14x9 grid, 700px apart: a ~9250x5640 bounding box. Even
// against the widest realistic browser-test viewport (1920x1080) with the
// fitViewOptions 10% padding shrinking the usable frame further, the TRUE
// fit-to-frame scale here is well under the shared 25% floor — the exact
// "more nodes than fit at 25%" case the gap closed below.
function hugeGraphNodes(): Node[] {
  const nodes: Node[] = []
  const cols = 14
  const rows = 9
  const spacing = 700
  for (let row = 0; row < rows; row++) {
    for (let col = 0; col < cols; col++) {
      nodes.push({ id: `n${row}-${col}`, position: { x: col * spacing, y: row * spacing }, data: {} })
    }
  }
  return nodes
}
const HUGE_GRAPH_NODES = hugeGraphNodes()
const HUGE_GRAPH_FIT_OPTIONS = { padding: 0.1 }

function HugeGraphFixture() {
  const pill = useZoomableCanvasPill({ fitViewOptions: HUGE_GRAPH_FIT_OPTIONS })
  useZoomableCanvasOpeningFit(HUGE_GRAPH_FIT_OPTIONS)
  return (
    <div data-testid="huge-graph-frame" style={{ width: '100%', height: '100%' }}>
      <ReactFlow
        nodes={HUGE_GRAPH_NODES}
        edges={[]}
        {...zoomableCanvasFlowProps}
        minZoom={pill.min}
        // Hides React Flow's own "React Flow" watermark link — its default
        // styling fails axe color-contrast on its own (a third-party
        // stylesheet this story doesn't control), unrelated to the fix this
        // story proves. `WorkspaceTeamGraph.tsx` makes the same choice.
        proOptions={{ hideAttribution: true }}
      >
        <Panel position="bottom-left">
          <ZoomPill {...pill} aria-label="Zoom huge graph" />
        </Panel>
      </ReactFlow>
    </div>
  )
}

/** REAL-BROWSER regression guard for the canvas preset's own effective-floor
 *  gap (2026-09-21, `docs/internal/design/components/zoomable-view.md`'s
 *  "Canvas preset, effective-floor gap" note): React Flow's declarative
 *  `fitView` boolean prop and a STATIC `minZoom` prop both read the
 *  scaleExtent BEFORE any per-graph measurement is possible, so a graph
 *  needing less than 25% to fit opened clipped at 25% (content cut off) and
 *  could not be zoomed out any further with the pill, wheel, or pinch. Fixed
 *  by `useZoomableCanvasOpeningFit` (the imperative opening fit) and passing
 *  the reactive `useZoomableCanvasPill().min` as `<ReactFlow minZoom>`
 *  instead of a static value. Before the fix (verified by temporarily
 *  reverting to the pre-fix `zoomable-view-canvas.tsx` — static
 *  `minZoom: 0.25` in `zoomableCanvasFlowProps`, the declarative `fitView`
 *  boolean prop, no `useZoomableCanvasOpeningFit`): the opening percent
 *  read 25% with nodes visibly clipped past the frame, and the zoom-out
 *  button disabled at 25% instead of the true fit. */
export const HugeGraphOpensFittedAndZoomsBelowFloor: Story = {
  parameters: { layout: 'fullscreen' },
  render: () => (
    <div style={{ width: '100vw', height: '100vh' }}>
      <ReactFlowProvider>
        <HugeGraphFixture />
      </ReactFlowProvider>
    </div>
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const frame = await canvas.findByTestId('huge-graph-frame')
    const percentEl = canvas.getByTestId('zoomable-view-percent')

    // (a) Fit shows every node inside the frame — the opening-fit fix. Wait
    // for the opening fit to settle (async: frame/node measurement, then
    // useZoomableCanvasOpeningFit's imperative fitView call).
    await waitFor(() => {
      const nodeEls = frame.querySelectorAll<HTMLElement>('.react-flow__node')
      expect(nodeEls.length).toBe(HUGE_GRAPH_NODES.length)
      const openingPercent = Number(percentEl.textContent?.replace('%', ''))
      expect(openingPercent).toBeGreaterThan(0)
    })

    const nodeEls = Array.from(frame.querySelectorAll<HTMLElement>('.react-flow__node'))
    const frameRect = frame.getBoundingClientRect()
    const contentBox = nodeEls.reduce(
      (box, el) => {
        const r = el.getBoundingClientRect()
        return {
          left: Math.min(box.left, r.left),
          top: Math.min(box.top, r.top),
          right: Math.max(box.right, r.right),
          bottom: Math.max(box.bottom, r.bottom),
        }
      },
      { left: Infinity, top: Infinity, right: -Infinity, bottom: -Infinity },
    )
    const openingPercent = Number(percentEl.textContent?.replace('%', ''))
    const evidence = JSON.stringify({ frameRect, contentBox, openingPercent })
    expect(contentBox.left, evidence).toBeGreaterThanOrEqual(frameRect.left - 1)
    expect(contentBox.top, evidence).toBeGreaterThanOrEqual(frameRect.top - 1)
    expect(contentBox.right, evidence).toBeLessThanOrEqual(frameRect.right + 1)
    expect(contentBox.bottom, evidence).toBeLessThanOrEqual(frameRect.bottom + 1)
    // The true fit for content this large needs LESS than the shared 25%
    // floor — this is the case the fix exists for.
    expect(openingPercent, evidence).toBeLessThan(25)

    // (b) Zoom in first (100%) so the zoom-out walk below is a genuine
    // descent, not a no-op already sitting at the floor.
    await userEvent.click(percentEl)
    await userEvent.click(within(document.body).getByRole('menuitem', { name: /100%/ }))
    await waitFor(() => expect(percentEl).toHaveTextContent('100%'))

    // Manual zoom-out (the pill's own stepper — the same scaleExtent wheel
    // and pinch read) must be able to reach BELOW the static 25% floor, all
    // the way down to the true fit.
    const zoomOutButton = canvas.getByTestId('zoomable-view-zoom-out')
    for (let i = 0; i < 40 && !zoomOutButton.hasAttribute('disabled'); i += 1) {
      await userEvent.click(zoomOutButton)
    }
    await waitFor(() => expect(zoomOutButton).toBeDisabled())
    const floorPercent = Number(percentEl.textContent?.replace('%', ''))
    const floorEvidence = JSON.stringify({ openingPercent, floorPercent })
    expect(floorPercent, floorEvidence).toBeLessThan(25)
    expect(floorPercent, floorEvidence).toBe(openingPercent)

    // The explicit "Fit" action reaches the SAME floor from any zoom level.
    await userEvent.click(percentEl)
    await userEvent.click(within(document.body).getByRole('menuitem', { name: /Fit/ }))
    await waitFor(() => expect(percentEl).toHaveTextContent(`${floorPercent}%`))
  },
}

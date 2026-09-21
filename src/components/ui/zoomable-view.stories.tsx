import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { ZoomPill, clampZoomScale, useZoomableViewKeyboard } from './zoomable-view'

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

// resize-separator.stories.tsx — the published ResizeSeparator control's
// stories (four-part contract, part 4): the interaction check drives the
// `Default` story's play function; the browser checks read this file's
// designSystem parameters. The parent box gives the separator a height and
// a panel-ish neighbor so the drag has something to move.

import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { useState } from 'react'
import { ResizeSeparator } from './resize-separator'

function SeparatorStage() {
  const [width, setWidth] = useState(640)
  return (
    // Fluid stage (w-full, not a fixed 900px): the zoom (640px @ zoom 2) and
    // reflow (320px) checks require the stage to reflow — the columns shrink
    // via min-w-0/flex so nothing overflows, while `value` stays the state
    // truth the assertions read.
    <div className="flex h-48 w-full max-w-[900px] overflow-hidden">
      {/* Coarse-pointer floor on the neighbor column (SegmentedControl's
          pattern, skill rule 12): without it the neighbor collapses to ~16px
          at the 390px viewport and the stage's overflow-hidden clips the
          separator's 44px touch hit region (the clip is why the tap misses,
          not the region's size). */}
      <div className="min-w-0 flex-1 bg-[var(--color-surface-1)] p-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] pointer-coarse:min-w-[var(--target-touch-minimum)]">chat column</div>
      <ResizeSeparator
        label="Resize Library panel"
        value={width}
        min={320}
        max={720}
        testId="resize-separator-demo"
        onValueChange={setWidth}
        onCommit={(px) => setWidth(px)}
      />
      <div style={{ width }} className="min-w-0 bg-[var(--color-surface-2)] p-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]" data-testid="resize-separator-panel">
        panel column
      </div>
    </div>
  )
}

const meta = {
  title: 'Design System/ResizeSeparator',
  // The stage (not the bare control) is the story unit: the separator's
  // required onValueChange prop is wired to local state inside the stage,
  // so the stories need no args of their own.
  component: SeparatorStage,
  parameters: {
    designSystem: {
      keyboard: [
        { trigger: '[data-testid="resize-separator-demo"]', key: 'ArrowRight', expectFocus: '[data-testid="resize-separator-demo"]' },
      ],
      pointerTargets: ['[data-testid="resize-separator-demo"]'],
      motionTargets: ['[data-testid="resize-separator-demo"]'],
      forcedColorTargets: ['[data-testid="resize-separator-demo"]'],
      forcedColors: {
        differences: [
          {
            cue: 'resize rail',
            selector: '[data-testid="resize-separator-demo-rail"]',
            property: 'borderLeftColor',
            againstSelector: '[data-testid="resize-separator-demo"]',
            againstProperty: 'backgroundColor',
            // The rail's border settles to the system border color in forced
            // colors — verified ButtonBorder rgb(0,0,0) on Chromium by direct
            // probe — but the check probes border system colors through the
            // `color` property, which Firefox resolves differently (its real
            // ButtonBorder is rgb(143,143,157); the probe returns
            // rgb(0,0,0)), so the contract declares only the a11y requirement
            // itself: rail must differ from its surface. The wrapper paints
            // no fill (background stays transparent in forced colors).
          },
        ],
        states: [
          { selector: '[data-testid="resize-separator-demo"]', attribute: 'aria-valuenow', value: '640' },
        ],
      },
      reflowExemptions: [],
      browserAssertions: [
        { selector: '[data-testid="resize-separator-demo"]', attribute: 'aria-valuenow', value: '640' },
      ],
    },
  },
} satisfies Meta<typeof ResizeSeparator>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {
  play: async ({ canvasElement }) => {
    const separator = within(canvasElement).getByRole('separator')
    expect(separator).toHaveAttribute('aria-valuenow', '640')
    separator.focus()
    // US-9 AS-2: a 16px keyboard step, mirrored — ArrowRight narrows a
    // right-side panel 640→624, ArrowLeft widens it back. The play ends at
    // 640 because the generated browser check runs this play first and then
    // reads browserAssertions against the END state.
    await userEvent.keyboard('{ArrowRight}')
    await expect(separator).toHaveAttribute('aria-valuenow', '624')
    await userEvent.keyboard('{ArrowLeft}')
    await expect(separator).toHaveAttribute('aria-valuenow', '640')
  },
}

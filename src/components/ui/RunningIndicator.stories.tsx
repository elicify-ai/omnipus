import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, within } from 'storybook/test'
import { RunningIndicator } from './RunningIndicator'

const statusSelector = '[role="status"]'
const meta = {
  title: 'Design System/RunningIndicator',
  component: RunningIndicator,
  parameters: {
    designSystem: {
      motionTargets: [`${statusSelector} svg`],
      forcedColorTargets: [statusSelector],
      forcedColors: {
        differences: [{
          cue: 'foreground',
          selector: statusSelector,
          property: 'color',
          againstSelector: 'body',
          againstProperty: 'backgroundColor',
          actualSystemColor: 'CanvasText',
          againstSystemColor: 'Canvas',
        }],
      },
      reflowExemptions: [],
      browserAssertions: [{ selector: statusSelector, attribute: 'aria-label', value: 'Running' }],
    },
  },
} satisfies Meta<typeof RunningIndicator>
export default meta

type Story = StoryObj<typeof meta>

export const SpinnerOnly: Story = {
  name: 'Spinner Only',
  args: {},
  play: async ({ canvasElement }) => {
    // PI1/PI3: omitted count means spinner-only, never a zero placeholder.
    const status = within(canvasElement).getByRole('status', { name: 'Running' })
    await expect(status.textContent).toBe('')
    await expect(status.querySelectorAll('svg')).toHaveLength(1)
    const arrow = status.querySelector('svg')!
    await expect(arrow).toHaveAttribute('aria-hidden', 'true')
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    await expect(getComputedStyle(arrow).animationName).toBe(reduced ? 'none' : 'spin')
  },
}

export const WithCount: Story = {
  name: 'With Count',
  args: { tokens: 4400 },
  parameters: {
    designSystem: {
      browserAssertions: [{ selector: statusSelector, attribute: 'aria-label', value: '4.4k tokens' }],
    },
  },
  play: async ({ canvasElement }) => {
    // Shared formatter: 4400 / 1000, one decimal place, k suffix.
    const status = within(canvasElement).getByRole('status', { name: '4.4k tokens' })
    await expect(status.textContent).toBe('4.4k tok')
    await expect(status.querySelectorAll('svg')).toHaveLength(1)
  },
}

export const Zero: Story = {
  args: { tokens: 0 },
  parameters: {
    designSystem: {
      browserAssertions: [{ selector: statusSelector, attribute: 'aria-label', value: '0 tokens' }],
    },
  },
  play: async ({ canvasElement }) => {
    // A known zero is distinct from the omitted count in SpinnerOnly.
    const status = within(canvasElement).getByRole('status', { name: '0 tokens' })
    await expect(status.textContent).toBe('0 tok')
  },
}

export const Still: Story = {
  args: { tokens: 44, streaming: false },
  parameters: {
    designSystem: {
      browserAssertions: [{ selector: statusSelector, attribute: 'aria-label', value: '44 tokens' }],
    },
  },
  play: async ({ canvasElement }) => {
    const status = within(canvasElement).getByRole('status', { name: '44 tokens' })
    await expect(status.textContent).toBe('44 tok')
    await expect(status.querySelectorAll('svg')).toHaveLength(1)
    await expect(getComputedStyle(status.querySelector('svg')!).animationName).toBe('none')
  },
}

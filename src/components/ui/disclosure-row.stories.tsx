import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { DisclosureRow } from './disclosure-row'

function Fixture() {
  const [expandedA, setExpandedA] = useState(false)
  const [expandedB, setExpandedB] = useState(false)
  return (
    <div className="flex w-80 flex-col gap-[var(--space-2)] font-mono text-[length:var(--type-utility-xs-size)]">
      <div>
        <DisclosureRow
          data-testid="disclosure-row-a"
          expanded={expandedA}
          onExpandedChange={setExpandedA}
          expandable
        >
          <span className="text-[var(--color-secondary)] font-medium">Read file</span>
          <span className="text-[var(--color-muted)]">Succeeded</span>
        </DisclosureRow>
        {expandedA && (
          <div data-testid="disclosure-row-a-detail" className="pl-[var(--space-2-5)] text-[var(--color-muted)]">
            src/App.tsx
          </div>
        )}
      </div>
      <DisclosureRow
        data-testid="disclosure-row-b"
        expanded={expandedB}
        onExpandedChange={setExpandedB}
        expandable
      >
        <span className="text-[var(--color-secondary)] font-medium">Web search</span>
        <span className="text-[var(--color-muted)]">Failed</span>
      </DisclosureRow>
      {/* No detail to disclose yet — still running. Disabled, aria-expanded omitted. */}
      <DisclosureRow
        data-testid="disclosure-row-disabled"
        expanded={false}
        onExpandedChange={() => {}}
        expandable={false}
      >
        <span className="text-[var(--color-secondary)] font-medium">Bash</span>
        <span className="text-[var(--color-muted)]">Running…</span>
      </DisclosureRow>
    </div>
  )
}

const meta = {
  title: 'Design System/DisclosureRow',
  component: DisclosureRow,
  args: { expanded: false, onExpandedChange: () => {}, expandable: true, children: 'Read file' },
  render: () => <Fixture />,
  parameters: {
    designSystem: {
      keyboard: [
        { trigger: '[data-testid="disclosure-row-a"]', key: 'Tab', expectFocus: '[data-testid="disclosure-row-b"]' },
      ],
      pointerTargets: ['[data-testid="disclosure-row-a"]', '[data-testid="disclosure-row-b"]'],
      motionTargets: ['[data-testid="disclosure-row-a"]'],
      forcedColorTargets: ['[data-testid="disclosure-row-a"]'],
      forcedColors: {
        differences: [
          {
            cue: 'foreground',
            selector: '[data-testid="disclosure-row-a"]',
            property: 'color',
            againstSelector: '[data-testid="disclosure-row-a"]',
            againstProperty: 'backgroundColor',
          },
        ],
        focus: ['[data-testid="disclosure-row-a"]'],
        states: [{ selector: '[data-testid="disclosure-row-a"]', attribute: 'aria-expanded', value: 'false' }],
      },
      reflowExemptions: [],
      browserAssertions: [{ selector: '[data-testid="disclosure-row-a"]', attribute: 'aria-expanded', value: 'false' }],
    },
  },
} satisfies Meta<typeof DisclosureRow>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
export const Disabled: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const disabledRow = canvas.getByTestId('disclosure-row-disabled')
    await expect(disabledRow).toBeDisabled()
    await expect(disabledRow).not.toHaveAttribute('aria-expanded')
  },
}
export const Toggle: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const rowA = canvas.getByTestId('disclosure-row-a')
    await expect(rowA).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(rowA)
    await expect(rowA).toHaveAttribute('aria-expanded', 'true')
    await expect(canvas.getByTestId('disclosure-row-a-detail')).toBeInTheDocument()
    await userEvent.click(rowA)
    await expect(rowA).toHaveAttribute('aria-expanded', 'false')
    await expect(canvas.queryByTestId('disclosure-row-a-detail')).not.toBeInTheDocument()
  },
}
export const NarrowViewport: Story = { parameters: { viewport: { defaultViewport: 'mobile1' } } }

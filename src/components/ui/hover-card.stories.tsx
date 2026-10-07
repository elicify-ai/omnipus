import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { HoverCard, HoverCardTrigger, HoverCardContent } from './hover-card'
import { Button } from './button'

function Fixture() {
  const [open, setOpen] = useState(false)
  return <HoverCard open={open} onOpenChange={setOpen} openDelay={100} closeDelay={200}>
    <HoverCardTrigger asChild><Button data-testid="hover-trigger" variant="ghost" aria-expanded={open} onFocus={() => setOpen(true)} onKeyDown={(event) => { if (event.key === 'Escape') setOpen(false) }}>Profile preview</Button></HoverCardTrigger>
    <HoverCardContent role="dialog" aria-label="Profile preview" data-testid="hover-content">
      <h3 className="font-bold">Sovereign Deep profile</h3>
      <p className="mt-[var(--space-1)] max-w-full break-normal wrap-break-word">Full metadata is visible on hover or keyboard focus, above clipped scroll areas.</p>
    </HoverCardContent>
  </HoverCard>
}
const meta = {
  title: 'Design System/HoverCard', component: HoverCard, render: () => <Fixture />,
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="hover-trigger"]', key: 'Escape', expectExpanded: false }],
    pointerTargets: ['[data-testid="hover-trigger"]'], motionTargets: ['[data-testid="hover-trigger"]'],
    forcedColorTargets: ['[data-testid="hover-trigger"]'],
    forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="hover-trigger"]', property: 'color', againstSelector: '[data-testid="hover-trigger"]', againstProperty: 'backgroundColor' }], focus: ['[data-testid="hover-trigger"]'], states: [{ selector: '[data-testid="hover-trigger"]', attribute: 'aria-expanded', value: 'false' }] },
    browserAssertions: [{ selector: '[data-testid="hover-trigger"]', attribute: 'aria-expanded', value: 'false' }], reflowExemptions: [],
  } },
} satisfies Meta<typeof HoverCard>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = {}
export const HoverAndFocus: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.hover(canvas.getByTestId('hover-trigger'))
    const page = within(canvasElement.ownerDocument.body)
    await expect(await page.findByRole('dialog', { name: 'Profile preview' })).toBeVisible()
    canvas.getByTestId('hover-trigger').focus()
    await userEvent.keyboard('{Escape}')
    await expect(page.queryByRole('dialog', { name: 'Profile preview' })).not.toBeInTheDocument()
  },
}

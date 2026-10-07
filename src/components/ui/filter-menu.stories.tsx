import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { FilterMenu } from './filter-menu'

function Fixture() {
  const [owner, setOwner] = useState<string | null>(null)
  const [tags, setTags] = useState<string[]>([])
  return <div className="flex flex-wrap items-center gap-[var(--space-3)]">
    <FilterMenu mode="single" value={owner} onChange={setOwner} label={owner === null ? 'Agent' : owner} aria-label="Filter by agent" clearLabel="All agents" data-testid="filter-owner"
      options={[{ value: 'Ray', label: 'Ray' }, { value: 'Jim', label: 'Jim' }]} />
    <FilterMenu mode="multiple" value={tags} onChange={setTags} label={tags.length ? `${tags.length} tags` : 'Tags'} aria-label="Filter by tags" clearLabel="Clear tags" data-testid="filter-tags"
      options={[{ value: 'docs', label: 'docs' }, { value: 'build', label: 'build' }]} />
  </div>
}
const meta = {
  title: 'Design System/FilterMenu', component: FilterMenu, render: () => <Fixture />,
  args: { mode: 'single', value: null, onChange: () => {}, clearLabel: 'All agents', label: 'Agent', 'aria-label': 'Filter by agent', options: [] },
  parameters: { designSystem: {
    keyboard: [{ trigger: '[data-testid="filter-owner"] button', key: 'Enter', expectExpanded: true }],
    pointerTargets: ['[data-testid="filter-owner"] button', '[data-testid="filter-tags"] button'],
    motionTargets: ['[data-testid="filter-owner"] button'], forcedColorTargets: ['[data-testid="filter-owner"] button'],
    forcedColors: { differences: [{ cue: 'foreground', selector: '[data-testid="filter-owner"] button', property: 'color', againstSelector: '[data-testid="filter-owner"] button', againstProperty: 'backgroundColor' }], focus: ['[data-testid="filter-owner"] button'], states: [{ selector: '[data-testid="filter-owner"] button', attribute: 'aria-haspopup', value: 'menu' }] },
    browserAssertions: [{ selector: '[data-testid="filter-owner"] button', attribute: 'aria-haspopup', value: 'menu' }], reflowExemptions: [],
  } },
} satisfies Meta<typeof FilterMenu>
export default meta
type Story = StoryObj<typeof meta>
export const Default: Story = {}
export const Selection: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByRole('button', { name: 'Filter by agent' }))
    const page = within(canvasElement.ownerDocument.body)
    await userEvent.click(await page.findByRole('menuitem', { name: 'Ray' }))
    await expect(canvas.getByRole('button', { name: 'Filter by agent' })).toHaveTextContent('Ray')
    await userEvent.click(canvas.getByRole('button', { name: 'Filter by tags' }))
    await userEvent.click(await page.findByRole('menuitemcheckbox', { name: 'docs' }))
    await expect(page.getByRole('menuitemcheckbox', { name: 'docs' })).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByRole('menu')).toBeVisible()
    await userEvent.keyboard('{Escape}')
  },
}

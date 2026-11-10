import { useState } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, userEvent, within } from 'storybook/test'
import { FilterMenu } from './filter-menu'
import { SearchField, SearchToolbar } from './search-field'

function FieldFixture({ initial = '' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  return (
    <div data-testid="search-field-story" className="w-80">
      <SearchField value={value} onValueChange={setValue} placeholder="Search connectors…" aria-label="Search connectors" />
    </div>
  )
}

function ToolbarFixture() {
  const [value, setValue] = useState('')
  const [owner, setOwner] = useState<string | null>(null)
  return (
    <div data-testid="search-field-story">
      <SearchToolbar aria-label="Connector filters">
        <SearchField value={value} onValueChange={setValue} placeholder="Search connectors…" aria-label="Search connectors" />
        <FilterMenu mode="single" value={owner} onChange={setOwner} label={owner ?? 'Status'} aria-label="Filter by status" clearLabel="Any status"
          options={[{ value: 'Connected', label: 'Connected' }, { value: 'Paused', label: 'Paused' }]} />
      </SearchToolbar>
    </div>
  )
}

const designSystem = {
  keyboard: [{ trigger: '[data-testid="search-field-story"] input[type="search"]', key: 'Escape', expectFocus: '[data-testid="search-field-story"] input[type="search"]' }],
  pointerTargets: ['[data-testid="search-field-story"] input[type="search"]'],
  forcedColors: { boundaries: ['[data-testid="search-field-story"] input[type="search"]'] },
  reflowExemptions: [],
  browserAssertions: [{ selector: '[data-testid="search-field-story"] input[type="search"]', attribute: 'aria-label', value: 'Search connectors' }],
}

const meta = {
  title: 'Design System/SearchField',
  component: SearchField,
  args: { value: '', onValueChange: () => {}, placeholder: 'Search connectors…', 'aria-label': 'Search connectors' },
  parameters: { designSystem },
  render: () => <FieldFixture />,
} satisfies Meta<typeof SearchField>
export default meta
type Story = StoryObj<typeof meta>

export const Default: Story = {}
export const WithValue: Story = {
  render: () => <FieldFixture initial="webhook" />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const input = canvas.getByRole('searchbox', { name: 'Search connectors' })
    await userEvent.click(canvas.getByRole('button', { name: 'Clear search' }))
    await expect(input).toHaveValue('')
    await expect(input).toHaveFocus()
    await userEvent.type(input, 'tele')
    await userEvent.keyboard('{Escape}')
    await expect(input).toHaveValue('')
  },
}
export const Toolbar: Story = { render: () => <ToolbarFixture /> }

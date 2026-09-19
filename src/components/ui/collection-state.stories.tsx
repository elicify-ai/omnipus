import type { Meta, StoryObj } from '@storybook/react-vite'
import { expect, within } from 'storybook/test'
import { CollectionState } from './collection-state'
import { Skeleton } from './skeleton'

const fixtures = { loading: <Skeleton pending className="h-16" />, empty: <p>No records</p>, error: <p>Could not load records</p>, children: <p>Cached record</p> }
const meta = {
  title: 'Design System/CollectionState', component: CollectionState,
  args: { state: 'ready', ...fixtures }, render: (args) => <div data-testid="collection"><CollectionState {...args} /></div>,
  parameters: { designSystem: { motionTargets: ['[data-testid="collection"] [data-collection-loading]'], forcedColorTargets: ['[data-testid="collection"]'], forcedColors:{differences:[{cue:'foreground',selector:'[data-testid="collection"]',property:'color',againstSelector:'[data-testid="collection"]',againstProperty:'backgroundColor'}]},reflowExemptions: [], browserAssertions: [{ selector: '[data-testid="collection"]', text: 'Cached record' }] } },
} satisfies Meta<typeof CollectionState>
export default meta
type Story = StoryObj<typeof meta>
export const Ready: Story = { play: async ({ canvasElement }) => { await expect(within(canvasElement).getByText('Cached record')).toBeInTheDocument() } }
export const InitialLoading: Story = { args: { state: 'initial-loading' } }
export const Refreshing: Story = { args: { state: 'refreshing' } }
export const Empty: Story = { args: { state: 'empty' } }
export const Partial: Story = { args: { state: 'partial' } }
export const Error: Story = { args: { state: 'error' } }

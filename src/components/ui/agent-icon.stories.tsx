import type { Meta, StoryObj } from '@storybook/react-vite'

import type { AgentFigure, AgentRole } from '@/lib/api/generated/openapi-types'
import { AgentColor } from '@/lib/api/generated/schemas'

import { AgentIcon } from './agent-icon'

type AgentIconStoryArgs = {
  figure: AgentFigure
  role: AgentRole
  size: 18 | 26 | 40 | 48
  motion?: 'none' | 'thinking' | 'working' | 'waiting'
  reducedMotion?: boolean
  name?: string
}

function renderAgentIcon(args: AgentIconStoryArgs) {
  return (
    <AgentIcon
      figure={args.figure}
      role={args.role}
      size={args.size}
      motion={args.motion}
      reducedMotion={args.reducedMotion}
      color={AgentColor.options[9]}
      decorative={false}
      name={args.name ?? 'Omnipus'}
    />
  )
}

// Shared design-system parameters. Extracted so the Monogram story can override
// only `browserAssertions` (the meta pins aria-label="Omnipus"; a decorative
// Monogram mark has no such label) while keeping every other parameter intact.
const designSystemParameters = {
  motionTargets: ['[data-ink]', '[data-glow]'],
  forcedColorTargets: ['[data-testid="agent-icon"]'],
  forcedColors: {
    differences: [{
      cue: 'foreground',
      selector: '[data-testid="agent-icon"]',
      property: 'color',
      againstSelector: 'body',
      againstProperty: 'backgroundColor',
      actualSystemColor: 'CanvasText',
      againstSystemColor: 'Canvas',
    }],
  },
  reflowExemptions: [],
  browserAssertions: [{ selector: '[data-testid="agent-icon"]', attribute: 'aria-label', value: 'Omnipus' }],
}

const meta = {
  title: 'Design System/AgentIcon',
  render: renderAgentIcon,
  args: {
    figure: 'Omnipus',
    role: 'general',
    size: 40,
    name: 'Omnipus',
  },
  parameters: {
    designSystem: designSystemParameters,
  },
} satisfies Meta<AgentIconStoryArgs>

export default meta
type Story = StoryObj<typeof meta>

export const Omnipus: Story = { args: { motion: 'none' } }
export const Robot: Story = { args: { figure: 'Robot', name: 'Robot' } }
export const Man: Story = { args: { figure: 'Man', name: 'Man' } }
export const Woman: Story = { args: { figure: 'Woman', name: 'Woman' } }
// The 5th figure. Its own outcome (not the inherited aria-label="Omnipus"):
// data-figure="Monogram" and the letter's data-initial, derived from the name.
export const Monogram: Story = {
  args: { figure: 'Monogram', name: 'Research Assistant' },
  parameters: {
    designSystem: {
      ...designSystemParameters,
      browserAssertions: [
        { selector: '[data-testid="agent-icon"]', attribute: 'data-figure', value: 'Monogram' },
        { selector: '[data-initial]', attribute: 'data-initial', value: 'R' },
      ],
    },
  },
}
export const RoleBadge: Story = { args: { role: 'writer', name: 'Writer' } }
export const Size18: Story = { args: { size: 18 } }
export const Size26: Story = { args: { size: 26 } }
export const Size40: Story = { args: { size: 40 } }
export const Size48: Story = { args: { size: 48 } }
export const MotionNone: Story = { args: { motion: 'none' } }
export const Thinking: Story = { args: { motion: 'thinking' } }
export const Working: Story = { args: { motion: 'working' } }
export const Waiting: Story = { args: { motion: 'waiting' } }
export const ReducedMotion: Story = { args: { motion: 'thinking', reducedMotion: true } }

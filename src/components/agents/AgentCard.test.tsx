import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AgentCard } from './AgentCard'
import type { Agent } from '@/lib/api'

// test_agent_card_component (test #12)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent cards render in responsive grid
//             wave5a-wire-ui-spec.md — Scenario: Agent card navigation to profile

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'general-assistant',
    name: 'General Assistant',
    type: 'core',
    locked: false,
    status: 'active',
    model: 'claude-sonnet-4-6',
    description: 'General purpose assistant',
    soul: '',
    heartbeat: '',
    instructions: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    steering_mode: 'loop',
    tool_feedback: true,
    heartbeat_enabled: false,
    heartbeat_interval: 300,
    ...overrides,
  }
}

beforeEach(() => {
  mockNavigate.mockClear()
})

describe('AgentCard — rendering (test #12)', () => {
  it('renders name, description, model, type badge for a core agent', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Agent cards render (AC1)
    // Dataset: Agent Card Rendering row 1
    render(<AgentCard agent={makeAgent()} />)
    expect(screen.getByText('General Assistant')).toBeInTheDocument()
    expect(screen.getByText(/core/i)).toBeInTheDocument()
    expect(screen.getByText(/claude-sonnet-4-6/i)).toBeInTheDocument()
    expect(screen.getByText(/General purpose assistant/i)).toBeInTheDocument()
  })

  it('renders locked core agent with core type badge', () => {
    // Dataset: Agent Card Rendering row 2
    render(
      <AgentCard
        agent={makeAgent({ id: 'mia', name: 'Mia', type: 'core', locked: true })}
      />
    )
    expect(screen.getAllByText(/^core$/i).length).toBeGreaterThan(0)
  })

  it('renders without crashing when icon is unrecognized', () => {
    // Dataset: Agent Card Rendering row 3 — unrecognized icon → fallback initial letter
    expect(() =>
      render(<AgentCard agent={makeAgent({ icon: 'unknown-icon-xyz', type: 'custom' })} />)
    ).not.toThrow()
  })

  it('renders without crashing when color is undefined', () => {
    // Dataset: Agent Card Rendering row 4 — missing color → CSS variable default
    expect(() =>
      render(<AgentCard agent={makeAgent({ color: undefined })} />)
    ).not.toThrow()
  })

  it('has aria-label containing the agent name for keyboard accessibility', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC4: card is keyboard accessible
    render(<AgentCard agent={makeAgent({ name: 'Research Bot' })} />)
    expect(screen.getByRole('button', { name: /Research Bot/i })).toBeInTheDocument()
  })

  it('shows "No description" fallback when description is empty', () => {
    // Dataset: Agent Card Rendering row 5 — empty description
    render(<AgentCard agent={makeAgent({ description: '' })} />)
    expect(screen.getByText(/No description/i)).toBeInTheDocument()
  })
})

describe('AgentCard — navigation (test #12)', () => {
  it('navigates to /agents/$agentId when card is clicked', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Agent card navigation to profile (AC3)
    render(<AgentCard agent={makeAgent({ id: 'general-assistant' })} />)
    fireEvent.click(screen.getByRole('button'))
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/agents/$agentId',
      params: { agentId: 'general-assistant' },
    })
  })
})

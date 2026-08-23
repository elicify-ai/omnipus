import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { AgentCard } from './AgentCard'
import { useUiStore } from '@/store/ui'
import type { Agent } from '@/lib/api'

// test_agent_card_component (test #12)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Agent cards render in responsive grid
//             wave5a-wire-ui-spec.md — Scenario: Agent card opens the edit slide-over

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'general-assistant',
    name: 'General Assistant',
    type: 'core',
    locked: false,
    needs_model: false,
    status: 'active',
    model: 'claude-sonnet-4-6',
    description: 'General purpose assistant',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
  }
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({ editAgentId: null })
  })
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
      render(<AgentCard agent={makeAgent({ icon: 'unknown-icon-xyz', type: 'Main' })} />)
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

describe('AgentCard — slide-over open (test #12)', () => {
  it('opens the edit slide-over via the UI store when the card is clicked', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Agent card opens the edit slide-over (AC3)
    render(<AgentCard agent={makeAgent({ id: 'general-assistant' })} />)
    fireEvent.click(screen.getByRole('button'))
    expect(useUiStore.getState().editAgentId).toBe('general-assistant')
  })

  it('uses the explicit onClick override when provided', () => {
    // The override REPLACES the default store action — parent that wants a
    // different click behaviour gets it without patching the component.
    const onClick = vi.fn()
    render(<AgentCard agent={makeAgent({ id: 'a' })} onClick={onClick} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledTimes(1)
    expect(useUiStore.getState().editAgentId).toBeNull()
  })
})

// ── sprint/258 default-star tests ─────────────────────────────────────────────
// Traces to: sprint/258-jun-2026 — AgentCard default star + "Set as default" button.

describe('AgentCard — default star (sprint/258)', () => {
  it('renders a filled star icon when agent.default is true', () => {
    // Content test: the star aria-label is present when default=true.
    render(<AgentCard agent={makeAgent({ default: true })} />)
    // AgentCard renders <Star aria-label="Default agent"> when agent.default is true.
    expect(screen.getByLabelText('Default agent')).toBeInTheDocument()
  })

  it('does NOT render the default star when agent.default is false', () => {
    // Differentiation test: absence of star when not default.
    render(<AgentCard agent={makeAgent({ default: false })} />)
    expect(screen.queryByLabelText('Default agent')).not.toBeInTheDocument()
  })

  it('does NOT render the default star when agent.default is undefined', () => {
    // Edge case: missing default field behaves same as false.
    render(<AgentCard agent={makeAgent({ default: undefined })} />)
    expect(screen.queryByLabelText('Default agent')).not.toBeInTheDocument()
  })

  it('renders "Set as default" button when agent is not default and onSetDefault is provided', () => {
    // Content test: the "Set as default" button appears on non-default agents when callback provided.
    const onSetDefault = vi.fn()
    render(<AgentCard agent={makeAgent({ default: false })} onSetDefault={onSetDefault} />)
    expect(
      screen.getByRole('button', { name: /set general assistant as default agent/i }),
    ).toBeInTheDocument()
  })

  it('does NOT render "Set as default" button when agent.default is true', () => {
    // Differentiation test: already-default agent does not show the "Set as default" button.
    const onSetDefault = vi.fn()
    render(<AgentCard agent={makeAgent({ default: true })} onSetDefault={onSetDefault} />)
    expect(
      screen.queryByRole('button', { name: /set .* as default/i }),
    ).not.toBeInTheDocument()
  })

  it('calls onSetDefault callback when "Set as default" button is clicked', () => {
    // Content test: clicking the button invokes the provided callback.
    const onSetDefault = vi.fn()
    render(<AgentCard agent={makeAgent({ default: false, id: 'jim', name: 'Jim' })} onSetDefault={onSetDefault} />)
    fireEvent.click(screen.getByRole('button', { name: /set Jim as default agent/i }))
    expect(onSetDefault).toHaveBeenCalledOnce()
  })

  it('does NOT render "Set as default" button when onSetDefault is not provided', () => {
    // Edge case: no callback → no button (prevents orphan UI element).
    render(<AgentCard agent={makeAgent({ default: false })} />)
    expect(
      screen.queryByRole('button', { name: /set .* as default/i }),
    ).not.toBeInTheDocument()
  })
})

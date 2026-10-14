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
    max_tool_iterations_source: 'global',
    max_tool_iterations_override_ignored: false,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
    revision: overrides.revision ?? '0'.repeat(64),
    figure: overrides.figure ?? 'Omnipus',
    role: overrides.role ?? 'general',
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

// ── sprint/258 default-star tests REMOVED ────────────────────────────────────
// The default-agent feature is gone (omnipus-system-agent K11/K12, bundle B1):
// `Agent.default` is off the wire, the star/"Set as default" controls are
// deleted, and the server PUT 400s any body carrying `default`. The seven
// sprint/258 star tests were deleted with the feature, not loosened.

// T068-28 — ADR-068 FR-014 (SPA half): "needs a model" / "needs a provider" copy.
// Traces to: adr-068-providers-ux-spec.md — Scenario: Dependents are listed and
// left without a model (final And: the agent list renders the copy on both rows);
// precedence per resolution #5 — degraded_reason: needs_provider wins in copy.
describe('AgentCard — needs a model / needs a provider copy (T068-28, FR-014)', () => {
  it('renders "needs a model" when needs_model is true', () => {
    render(<AgentCard agent={makeAgent({ needs_model: true, model: undefined })} />)
    expect(screen.getByText('needs a model')).toBeInTheDocument()
    expect(screen.queryByText('needs a provider')).not.toBeInTheDocument()
  })

  it('renders "needs a provider" (not "needs a model") when degraded_reason needs_provider is also set — precedence, resolution #5', () => {
    render(
      <AgentCard
        agent={makeAgent({ needs_model: true, degraded_reason: 'needs_provider' })}
      />
    )
    expect(screen.getByText('needs a provider')).toBeInTheDocument()
    expect(screen.queryByText('needs a model')).not.toBeInTheDocument()
  })

  it('renders "needs a provider" when degraded_reason is set even without needs_model', () => {
    // ADR-067 FR-016/FR-031: a needs_provider agent refuses turns regardless of needs_model.
    render(
      <AgentCard
        agent={makeAgent({ needs_model: false, degraded_reason: 'needs_provider' })}
      />
    )
    expect(screen.getByText('needs a provider')).toBeInTheDocument()
    expect(screen.queryByText('needs a model')).not.toBeInTheDocument()
  })

  it('renders neither copy on a healthy agent', () => {
    render(<AgentCard agent={makeAgent()} />)
    expect(screen.queryByText('needs a model')).not.toBeInTheDocument()
    expect(screen.queryByText('needs a provider')).not.toBeInTheDocument()
  })

  it('is not colour-only: the copy is literal text, present in the accessible tree', () => {
    // DoD: no colour-only state — the meaning is carried by the text itself.
    render(<AgentCard agent={makeAgent({ needs_model: true })} />)
    const el = screen.getByText('needs a model')
    expect(el.textContent).toBe('needs a model')
  })
})

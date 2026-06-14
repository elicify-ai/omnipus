import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TrustGraph } from './TrustGraph'
import type { Agent } from '@/lib/api'

// Trust-graph component test (Spec-3 FR-6.2 / US-3 / NFR-7).
// Proves: (a) `to` edges render with their `modes`; (b) `accept_from` and
// `budget` are NEVER rendered (NFR-7 / M-6 — they are inert in v0.1.0 and must
// not imply an active authz boundary); (c) an agent with no delegation_policy
// renders without error.

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'agent',
    name: 'Agent',
    type: 'core',
    locked: false,
    status: 'active',
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

// A distinctive id that would never legitimately appear in the rendered graph,
// used to prove accept_from / budget targets are not leaked into the DOM.
const SECRET_ACCEPT_FROM_ID = 'should-never-render-acceptfrom'
const SECRET_BUDGET_COST = 4242
const SECRET_BUDGET_TOKENS = 999777

function roster(): Agent[] {
  const jim = makeAgent({
    id: 'jim',
    name: 'Jim',
    default: true,
    delegation_policy: {
      to: [{ kind: 'local', id: 'ray' }],
      // accept_from + budget are present in the contract but MUST NOT surface.
      accept_from: [{ kind: 'local', id: SECRET_ACCEPT_FROM_ID }],
      modes: ['await', 'task'],
      depth: 3,
      budget: { max_cost_usd: SECRET_BUDGET_COST, max_tokens: SECRET_BUDGET_TOKENS },
    },
  })
  const ray = makeAgent({ id: 'ray', name: 'Ray', type: 'core' })
  return [jim, ray]
}

describe('TrustGraph — to + modes rendering (FR-6.2 / US-3)', () => {
  it('renders a `to` edge labeled with its modes', () => {
    render(<TrustGraph agents={roster()} />)

    // The edge Jim -> Ray exists.
    const edge = screen.getByTestId('trust-edge-jim-ray')
    expect(edge).toBeInTheDocument()
    // Target agent name is shown on the edge.
    expect(edge).toHaveTextContent('Ray')
    // Both allowed modes render as chips on the edge.
    expect(edge.querySelector('[data-testid="mode-chip-await"]')).not.toBeNull()
    expect(edge.querySelector('[data-testid="mode-chip-task"]')).not.toBeNull()
  })

  it('marks the default agent with the star (aria-label)', () => {
    render(<TrustGraph agents={roster()} />)
    // Jim is default — the star carries the "Default agent" aria-label.
    expect(screen.getAllByLabelText('Default agent').length).toBeGreaterThan(0)
  })

  it('shows the depth cap when present', () => {
    render(<TrustGraph agents={roster()} />)
    expect(screen.getByTestId('trust-depth-jim')).toHaveTextContent('depth 3')
  })
})

describe('TrustGraph — NFR-7: accept_from and budget are NEVER rendered', () => {
  it('does not render the accept_from target id anywhere in the DOM', () => {
    const { container } = render(<TrustGraph agents={roster()} />)
    expect(container.textContent ?? '').not.toContain(SECRET_ACCEPT_FROM_ID)
    expect(screen.queryByText(/accept[_ -]?from/i)).toBeNull()
  })

  it('does not render any budget value (cost or tokens) in the DOM', () => {
    const { container } = render(<TrustGraph agents={roster()} />)
    const text = container.textContent ?? ''
    expect(text).not.toContain(String(SECRET_BUDGET_COST))
    expect(text).not.toContain(String(SECRET_BUDGET_TOKENS))
    expect(screen.queryByText(/budget/i)).toBeNull()
  })
})

describe('TrustGraph — agents without delegation_policy', () => {
  it('renders an agent with no delegation_policy without error', () => {
    const lonely = makeAgent({ id: 'solo', name: 'Solo' })
    expect(() => render(<TrustGraph agents={[lonely]} />)).not.toThrow()
    expect(screen.getByTestId('trust-node-solo')).toBeInTheDocument()
    // No outgoing edges — the empty-state copy renders instead.
    expect(screen.getByText(/may not delegate to anyone/i)).toBeInTheDocument()
  })

  it('renders an agent with an empty `to` (undefined) without crashing', () => {
    const partial = makeAgent({
      id: 'partial',
      name: 'Partial',
      // policy present but `to` omitted — must not crash on undefined.
      delegation_policy: { modes: ['await'] },
    })
    expect(() => render(<TrustGraph agents={[partial]} />)).not.toThrow()
    expect(screen.getByTestId('trust-node-partial')).toBeInTheDocument()
  })

  it('renders the empty state when there are no agents', () => {
    render(<TrustGraph agents={[]} />)
    expect(screen.getByText(/no agents to graph/i)).toBeInTheDocument()
  })
})

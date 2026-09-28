import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkerCard } from './WorkerCard'
import type { Agent } from '@/lib/api'

// Wave 2 — sub-agent worker tier. WorkerCard shows the executor badge; it OMITS
// the chat entry, heartbeat indicator, the default-★ control (workers are never
// chat targets / heartbeat / default) and — since issue #915 — any "Test run"
// control on the tile (an external worker's runner check runs automatically
// before its profile saves).

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

function makeWorker(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'worker-1',
    name: 'General Worker',
    type: 'Subagent',
    locked: false,
    needs_model: false,
    status: 'idle',
    model: 'anthropic/claude-3.5-haiku',
    description: 'General purpose worker',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
    revision: overrides.revision ?? '0'.repeat(64),
  }
}

function renderCard(agent: Agent) {
  return render(<WorkerCard agent={agent} />)
}

beforeEach(() => {
  mockNavigate.mockClear()
})

describe('WorkerCard — content', () => {
  it('renders the Worker badge and name', () => {
    renderCard(makeWorker())
    expect(screen.getByText('General Worker')).toBeInTheDocument()
    expect(screen.getByText('Worker')).toBeInTheDocument()
  })

  it('shows "Native" executor badge when executor is absent (default)', () => {
    renderCard(makeWorker({ executor: undefined }))
    expect(screen.getByText('Native')).toBeInTheDocument()
  })

  it('shows the named CLI as the executor badge for external-cli', () => {
    renderCard(makeWorker({ executor: { kind: 'external-cli', cli: 'claude-code' } }))
    expect(screen.getByText('Claude Code')).toBeInTheDocument()
  })

  it('shows "opencode" executor badge for external-cli/opencode', () => {
    renderCard(makeWorker({ executor: { kind: 'external-cli', cli: 'opencode' } }))
    expect(screen.getByText('opencode')).toBeInTheDocument()
  })

  it('shows a visible Running state while the worker has an active turn', () => {
    renderCard(makeWorker({ status: 'active' }))
    expect(screen.getByText('Running')).toBeInTheDocument()
  })
})

describe('WorkerCard — omitted colleague affordances', () => {
  it('does NOT render a default-★ / "Set as default" control', () => {
    renderCard(makeWorker({ default: true }))
    expect(screen.queryByLabelText('Default agent')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /set .* as default/i })).not.toBeInTheDocument()
  })

  it('does NOT render a chat/open-conversation entry point', () => {
    renderCard(makeWorker())
    // No "chat", "message", or "open conversation" affordance on a worker card.
    expect(screen.queryByRole('button', { name: /chat|message|conversation/i })).not.toBeInTheDocument()
  })

  it('does NOT render a heartbeat indicator (heartbeat is workspace-scoped, not on worker cards)', () => {
    renderCard(makeWorker())
    expect(screen.queryByText(/heartbeat/i)).not.toBeInTheDocument()
  })
})

describe('WorkerCard — no Test run control on the tile (issue #915)', () => {
  // Oracle: issue #915's expected behaviour — a worker tile shows no Test run
  // control, whatever its runtime. Checked for both a native and an external-CLI
  // worker, by test id, by accessible name and by visible text.
  it.each([
    ['native', { kind: 'native' } as const],
    ['external-cli', { kind: 'external-cli', cli: 'claude-code' } as const],
  ])('renders no Test run control for a worker on the %s runtime', (_label, executor) => {
    const { container } = renderCard(makeWorker({ executor }))
    expect(container.querySelector('[data-testid^="worker-test-run-"]')).toBeNull()
    expect(container.querySelector('[data-testid^="worker-test-result-"]')).toBeNull()
    expect(screen.queryByRole('button', { name: /test run/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/test run/i)).not.toBeInTheDocument()
    // The only control on the tile is the card itself (opens the profile).
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(1)
    expect(buttons[0]).toHaveAccessibleName('View worker General Worker')
  })
})

describe('WorkerCard — navigation', () => {
  it('navigates to the worker profile on card click', () => {
    renderCard(makeWorker({ id: 'worker-7' }))
    fireEvent.click(screen.getByRole('button', { name: /view worker/i }))
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/agents/$agentId',
      params: { agentId: 'worker-7' },
    })
  })
})

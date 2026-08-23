import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkerCard } from './WorkerCard'
import type { Agent } from '@/lib/api'

// Wave 2 — sub-agent worker tier. WorkerCard shows the executor badge and a
// "Test run" affordance; it OMITS the chat entry, heartbeat indicator, and the
// default-★ control (workers are never chat targets / heartbeat / default).

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, testAgentRunner: vi.fn() }
})

import { testAgentRunner } from '@/lib/api'

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
  }
}

function renderCard(agent: Agent) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <WorkerCard agent={agent} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(testAgentRunner).mockReset()
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

describe('WorkerCard — Test run affordance', () => {
  it('disables Test run for a native worker (no external runner to probe)', () => {
    renderCard(makeWorker({ executor: { kind: 'native' } }))
    const btn = screen.getByTestId('worker-test-run-worker-1')
    expect(btn).toBeDisabled()
  })

  it('enables and wires Test run for an external-cli worker', async () => {
    vi.mocked(testAgentRunner).mockResolvedValue({
      ok: true,
      reason: '',
      message: 'claude-code found and authenticated',
      cli: 'claude-code',
      cli_version: '1.2.3',
    })
    renderCard(makeWorker({ executor: { kind: 'external-cli', cli: 'claude-code' } }))
    const btn = screen.getByTestId('worker-test-run-worker-1')
    expect(btn).not.toBeDisabled()
    fireEvent.click(btn)
    await waitFor(() => expect(testAgentRunner).toHaveBeenCalledWith('worker-1'))
    await waitFor(() =>
      expect(screen.getByText(/claude-code found and authenticated/i)).toBeInTheDocument(),
    )
  })
})

describe('WorkerCard — test-result pill keys off result.ok', () => {
  // The success/fail tone must come from the authoritative `ok` boolean, not an
  // inference from reason === ''. We assert via the success-coloured icon class
  // and the visible message; reason still drives the data-reason attribute.
  it('renders a success pill when ok:true', async () => {
    vi.mocked(testAgentRunner).mockResolvedValue({
      ok: true,
      reason: '',
      message: 'ready',
      cli: 'claude-code',
      cli_version: '1.2.3',
    })
    renderCard(makeWorker({ executor: { kind: 'external-cli', cli: 'claude-code' } }))
    fireEvent.click(screen.getByTestId('worker-test-run-worker-1'))
    const pill = await screen.findByTestId('worker-test-result-claude-code')
    // Success tone (success token) lives on the pill span — not the error/warn tones.
    expect(pill).toHaveClass('text-[var(--color-success)]')
    expect(pill).toHaveTextContent('ready')
    expect(pill).toHaveAttribute('data-reason', 'ok')
  })

  it('renders a failure pill when ok:false even if a reason is present', async () => {
    vi.mocked(testAgentRunner).mockResolvedValue({
      ok: false,
      reason: 'missing-binary',
      message: 'claude-code not on PATH',
      cli: 'claude-code',
    })
    renderCard(makeWorker({ executor: { kind: 'external-cli', cli: 'claude-code' } }))
    fireEvent.click(screen.getByTestId('worker-test-run-worker-1'))
    const pill = await screen.findByTestId('worker-test-result-claude-code')
    // Not the success tone — keyed off ok:false.
    expect(pill).not.toHaveClass('text-[var(--color-success)]')
    expect(pill).toHaveTextContent('claude-code not on PATH')
    expect(pill).toHaveAttribute('data-reason', 'missing-binary')
  })

  it('renders a warning pill when ok:false and reason is unauthenticated', async () => {
    vi.mocked(testAgentRunner).mockResolvedValue({
      ok: false,
      reason: 'unauthenticated',
      message: 'present but not logged in',
      cli: 'claude-code',
    })
    renderCard(makeWorker({ executor: { kind: 'external-cli', cli: 'claude-code' } }))
    fireEvent.click(screen.getByTestId('worker-test-run-worker-1'))
    const pill = await screen.findByTestId('worker-test-result-claude-code')
    expect(pill).toHaveClass('text-[var(--color-warning)]')
    expect(pill).toHaveTextContent('present but not logged in')
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

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TokenBudgetSection } from './TokenBudgetSection'
import type { TokenBudgetStatus } from '@/lib/api/generated/openapi-types'

// ── Mocks ──────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTokenBudgetStatus: vi.fn(),
    updateTokenBudget: vi.fn(),
  }
})

import { fetchTokenBudgetStatus, updateTokenBudget } from '@/lib/api'

// ── Fixtures ───────────────────────────────────────────────────────────────────

const bounded: TokenBudgetStatus = {
  budget: 5_000_000,
  consumed: 1_250_000,
  remaining: 3_750_000,
  exhausted: false,
  by_scope: { owner: 200_000, member: 900_000, verifier: 100_000, judge: 50_000 },
}

const unbounded: TokenBudgetStatus = {
  budget: 0,
  consumed: 4_200_000,
  remaining: 0,
  exhausted: false,
  advisory: 'unbounded — set a budget',
  by_scope: { owner: 100_000, member: 200_000, verifier: 0, judge: 0 },
}

const exhausted: TokenBudgetStatus = {
  budget: 1_000_000,
  consumed: 1_001_000,
  remaining: 0,
  exhausted: true,
  by_scope: { owner: 200_000, member: 800_000, verifier: 0, judge: 1_000 },
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <TokenBudgetSection />
    </QueryClientProvider>,
  )
}

// ── Tests ──────────────────────────────────────────────────────────────────────

describe('TokenBudgetSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchTokenBudgetStatus).mockResolvedValue(bounded)
    vi.mocked(updateTokenBudget).mockResolvedValue(bounded)
  })

  it('renders the restart-gated notice and the token≠dollar operator note', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-spend')).toBeInTheDocument())
    expect(screen.getByTestId('token-budget-restart-notice')).toHaveTextContent(/Restart required/i)
    expect(screen.getByTestId('token-budget-restart-notice')).toHaveTextContent(/Stop or Cancel/i)
    expect(screen.getByTestId('token-budget-dollar-note')).toHaveTextContent(/token cap does not bound dollar spend/i)
  })

  it('renders consumed / ceiling with a percentage for a bounded budget', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-spend-bound')).toBeInTheDocument())
    // consumed 1.25M / 5.0M, 25%
    expect(screen.getByTestId('token-budget-spend-bound')).toHaveTextContent('1.3M')
    expect(screen.getByTestId('token-budget-spend-bound')).toHaveTextContent('5.0M')
    expect(screen.getByTestId('token-budget-spend-bound')).toHaveTextContent('(25%)')
  })

  it('renders the per-workload breakdown ordered by spend (members first)', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-scopes')).toBeInTheDocument())
    const scopes = screen.getByTestId('token-budget-scopes')
    // member 900k is the largest → listed first; judge 50k smallest → last.
    // formatTokens renders one decimal: 900000 → "900.0k", 50000 → "50.0k".
    expect(scopes.textContent).toMatch(/Members.*900\.0k.*Owner loop.*200\.0k.*Verifiers.*100\.0k.*Judge.*50\.0k/)
  })

  it('shows the persistent unbounded advisory and "Unbounded (advisory)" ceiling for budget = 0', async () => {
    vi.mocked(fetchTokenBudgetStatus).mockResolvedValue(unbounded)
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-advisory')).toBeInTheDocument())
    expect(screen.getByTestId('token-budget-advisory')).toHaveTextContent(/Unbounded — set a budget/i)
    // The spend block uses the unbounded variant (no consumed/ceiling ratio).
    expect(screen.getByTestId('token-budget-spend-unbounded')).toHaveTextContent('Unbounded (advisory)')
    expect(screen.getByTestId('token-budget-spend-unbounded')).toHaveTextContent('4.2M')
  })

  it('shows an exhausted banner when exhausted is true', async () => {
    vi.mocked(fetchTokenBudgetStatus).mockResolvedValue(exhausted)
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-exhausted')).toBeInTheDocument())
    expect(screen.getByTestId('token-budget-exhausted')).toHaveTextContent(/failed\(budget_exhausted\)/)
  })

  it('disables Save until the ceiling input is dirty, then calls updateTokenBudget', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-input')).toBeInTheDocument())
    const saveBtn = screen.getByTestId('token-budget-save') as HTMLButtonElement
    // Persisted budget is 5_000_000; input is seeded with it → not dirty.
    expect(saveBtn.disabled).toBe(true)

    const input = screen.getByTestId('token-budget-input') as HTMLInputElement
    await userEvent.clear(input)
    await userEvent.type(input, '2000000')
    await waitFor(() => expect(saveBtn.disabled).toBe(false))

    await userEvent.click(saveBtn)
    await waitFor(() => expect(vi.mocked(updateTokenBudget)).toHaveBeenCalledWith(2_000_000))
  })

  it('saving an empty field persists the unbounded sentinel (0)', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-input')).toBeInTheDocument())
    const input = screen.getByTestId('token-budget-input') as HTMLInputElement
    await userEvent.clear(input)
    const saveBtn = screen.getByTestId('token-budget-save')
    await waitFor(() => expect((saveBtn as HTMLButtonElement).disabled).toBe(false))
    await userEvent.click(saveBtn)
    await waitFor(() => expect(vi.mocked(updateTokenBudget)).toHaveBeenCalledWith(0))
  })

  it('clamps a negative input to the unbounded sentinel (0)', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-input')).toBeInTheDocument())
    const input = screen.getByTestId('token-budget-input') as HTMLInputElement
    await userEvent.clear(input)
    await userEvent.type(input, '-5')
    await userEvent.click(screen.getByTestId('token-budget-save'))
    await waitFor(() => expect(vi.mocked(updateTokenBudget)).toHaveBeenCalledWith(0))
  })

  it('renders the skeleton while loading, then the section', async () => {
    vi.mocked(fetchTokenBudgetStatus).mockReturnValue(new Promise(() => {}))
    renderSection()
    expect(screen.getByTestId('token-budget-skeleton')).toBeInTheDocument()
  })

  it('degrades to an inline error note when the fetch rejects (does not block the screen)', async () => {
    vi.mocked(fetchTokenBudgetStatus).mockRejectedValue(new Error('boom'))
    renderSection()
    await waitFor(() =>
      expect(screen.getByText(/Could not load token budget/i)).toBeInTheDocument(),
    )
    // The spend accounting must not render when the fetch failed.
    expect(screen.queryByTestId('token-budget-spend')).not.toBeInTheDocument()
  })

  it('fires a query when rendered (consumes the landed TokenBudgetStatus wire type)', async () => {
    renderSection()
    await waitFor(() => expect(vi.mocked(fetchTokenBudgetStatus)).toHaveBeenCalledTimes(1))
  })

  it('renders no dollar amounts in the spend area (D12 — tokens, not money)', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByTestId('token-budget-spend')).toBeInTheDocument())
    const spend = screen.getByTestId('token-budget-spend')
    expect(spend.textContent).not.toMatch(/\$\d/)
  })
})

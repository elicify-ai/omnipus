/**
 * ExecAllowlistSection.test.tsx — mirror-image D3 defect (UAT v0.1.1 defects).
 *
 * `disabled: !isDirty` used to overload the dirty flag as the useAutoSave
 * readiness signal. Per useAutoSave.ts's "skip first render" logic, the
 * FIRST commit where `disabled` is false is captured as the baseline
 * ("already saved"), not persisted. `handleAdd`/`handleRemove` set
 * `patterns` AND `isDirty(true)` in the SAME synchronous update, so the
 * user's very FIRST add/remove was itself the commit that flipped
 * `disabled` false — useAutoSave adopted that already-containing-the-edit
 * state as its baseline instead of saving it. `hasPendingChanges()` then
 * read false, so not even the unmount flush caught it: the first edit of a
 * session was silently never sent to the server.
 *
 * Fixed by decoupling a dedicated `patternsHydrated` readiness flag (set
 * once, the moment the server GET has loaded into `patterns`) from
 * `isDirty` (which keeps its original, separate job: blocking a background
 * refetch from clobbering an in-progress local edit).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchExecAllowlist: vi.fn(),
    updateExecAllowlist: vi.fn(),
  }
})

import { fetchExecAllowlist, updateExecAllowlist } from '@/lib/api'
import { ExecAllowlistSection } from './ExecAllowlistSection'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection(client = makeClient()) {
  return render(
    <QueryClientProvider client={client}>
      <ExecAllowlistSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchExecAllowlist).mockResolvedValue({ allowed_binaries: ['git *'] })
  vi.mocked(updateExecAllowlist).mockImplementation(async (patterns: string[]) => ({
    allowed_binaries: patterns,
  }))
})

describe('ExecAllowlistSection — mirror-image D3 defect: first add/remove must actually save', () => {
  it('adding the FIRST pattern in a session calls updateExecAllowlist with it included (REVERT-PROOF: fails without the patternsHydrated/isDirty decoupling)', async () => {
    renderSection()

    // Wait for hydration.
    await screen.findByText('git *')

    const input = screen.getByLabelText('New binary pattern')
    fireEvent.change(input, { target: { value: 'custom-tool *' } })
    fireEvent.click(screen.getByRole('button', { name: /^add$/i }))

    // The new pattern renders immediately (optimistic local state).
    await screen.findByText('custom-tool *')

    // The debounced save (500ms default) must actually fire — this is the
    // user's FIRST edit in the session, the exact case the mirror-image bug
    // silently dropped (useAutoSave treated the add-containing commit as
    // "already saved").
    await waitFor(() => {
      expect(updateExecAllowlist).toHaveBeenCalledWith(['git *', 'custom-tool *'])
    })
  })

  it('hasPendingChanges is not silently cleared by the first add — closing immediately after one add still has a real pending write in flight', async () => {
    // This is the sharper form of the same regression: pre-fix,
    // `hasPendingChanges()` read FALSE right after the first add (the
    // baseline already "contained" it), so even the unmount flush would
    // not have caught it. Here we assert the save fires with NO further
    // user action beyond the single add — proving the write is not
    // dependent on a SECOND edit to accidentally surface it.
    renderSection()
    await screen.findByText('git *')

    fireEvent.change(screen.getByLabelText('New binary pattern'), {
      target: { value: 'only-edit *' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^add$/i }))
    await screen.findByText('only-edit *')

    // No second interaction. Passive wait past the debounce.
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(updateExecAllowlist).toHaveBeenCalledTimes(1)
    expect(updateExecAllowlist).toHaveBeenCalledWith(['git *', 'only-edit *'])
  })

  it('removing the FIRST (and only) pattern in a session also actually saves', async () => {
    renderSection()
    await screen.findByText('git *')

    fireEvent.click(screen.getByRole('button', { name: /remove pattern git \*/i }))

    await waitFor(() => {
      expect(updateExecAllowlist).toHaveBeenCalledWith([])
    })
  })

  it('does not save on mount / hydration alone — no interaction means no PUT', async () => {
    renderSection()
    await screen.findByText('git *')
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(updateExecAllowlist).not.toHaveBeenCalled()
  })
})

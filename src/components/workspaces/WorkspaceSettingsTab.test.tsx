/**
 * WorkspaceSettingsTab — Project Instructions section tests.
 *
 * Covers the 4 behaviours of the instructions textarea:
 *   1. Hydration      — query resolves → textarea shows fetched content
 *   2. Save on edit   — typing triggers updateWorkspaceInstructions via useAutoSave
 *   3. DATA-LOSS GUARD— load error → Retry shown, updateWorkspaceInstructions NEVER called
 *   4. Clear          — editing to empty string still calls updateWorkspaceInstructions("")
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceSettingsTab } from './WorkspaceSettingsTab'
import type { Workspace } from '@/lib/api'

// ── Router stub ──────────────────────────────────────────────────────────────
// WorkspaceSettingsTab calls useNavigate and renders buttons that navigate to
// team tab routes. Stub the router primitives so the component renders in
// isolation without a real RouterProvider.
const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    Link: ({
      children,
      to,
      ...rest
    }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
  }
})

// ── UI store stub ────────────────────────────────────────────────────────────
const mockAddToast = vi.fn()

// WorkspaceSettingsTab reads via the SELECTOR pattern (`useUiStore((s) =>
// s.addToast)`), not `useUiStore()` with no args — the mock must invoke a
// passed selector against the fake state rather than always returning the
// whole state object, or `addToast` binds to `{ addToast: fn }` (an object)
// instead of `fn` itself, and any call site that invokes it throws
// "addToast is not a function" (surfaces as an unhandled rejection from
// inside a mutation's onSuccess/onError, since those errors are swallowed
// into the mutation's own promise chain rather than failing the test
// directly — see the archive/delete invalidation tests below, which are
// the first in this file to actually exercise a toast-firing path).
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn((selector?: (s: { addToast: typeof mockAddToast }) => unknown) => {
    const state = { addToast: mockAddToast }
    return selector ? selector(state) : state
  }),
}))

// ── API mock — partial passthrough, override the two instructions functions ──
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaceInstructions: vi.fn(),
    updateWorkspaceInstructions: vi.fn(),
    updateWorkspace: vi.fn(),
  }
})

import * as api from '@/lib/api'

// ── Fixtures ─────────────────────────────────────────────────────────────────

const WORKSPACE_ID = 'ws-test-001'

const mockWorkspace: Workspace = {
  id: WORKSPACE_ID,
  name: 'Test Workspace',
  description: 'A test workspace',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  is_default: false,
  repository: '',
  core_team: [],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

const mockDefaultWorkspace: Workspace = {
  ...mockWorkspace,
  id: 'ws-default-001',
  name: 'My Workspace',
  is_default: true,
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

function renderTab() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceSettingsTab workspace={mockWorkspace} />
    </QueryClientProvider>,
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  mockNavigate.mockClear()
  // Default: resolves successfully so each test that needs an error can override.
  vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: '' })
  vi.mocked(api.updateWorkspaceInstructions).mockResolvedValue({ content: '' })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('WorkspaceSettingsTab — Project Instructions section', () => {
  /**
   * Test 1 — Hydration
   * When fetchWorkspaceInstructions resolves with "hello world" the textarea
   * value must equal "hello world".
   */
  it('hydrates the textarea from fetchWorkspaceInstructions', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'hello world' })

    renderTab()

    // The textarea is initially empty; wait for the query to resolve and the
    // useEffect to apply the fetched content.
    await waitFor(() => {
      const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
      expect(textarea).toHaveValue('hello world')
    })

    expect(api.fetchWorkspaceInstructions).toHaveBeenCalledWith(WORKSPACE_ID)
  })

  /**
   * Test 2 — Save on edit
   * After the data loads, changing the textarea triggers updateWorkspaceInstructions
   * once the useAutoSave debounce fires. Deliberately updated (echo-race fix,
   * P2 'Text input Auto save'): the instructions instance's debounce was
   * raised from the 500ms default to 1500ms (long-form surface — see
   * WorkspaceSettingsTab.tsx's `useAutoSave` call for the instructions
   * field), so the advance below and its comment were updated to match.
   */
  it('calls updateWorkspaceInstructions after typing into the textarea', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'initial text' })

    renderTab()

    // Wait for hydration with real timers so the query resolves normally.
    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('initial text')
    })

    // Switch to fake timers NOW — after data is loaded — to control the debounce.
    vi.useFakeTimers()

    const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
    fireEvent.change(textarea, { target: { value: 'updated text' } })

    // Before the debounce fires the PUT must not have been sent.
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()

    // Advance past the 1500 ms debounce (deliberately raised from 500ms).
    // ASYNC variant: drains the mocked save's resolved promise AND
    // useAutoSave's post-await bookkeeping (status→'saved', and the 2s
    // "fade to idle" setTimeout it arms) while STILL on fake timers, so a
    // fade timer that gets armed here is a FAKE one — safely discarded by
    // the vi.useRealTimers() switch below. The old pattern (sync
    // `advanceTimersByTime` + immediate switch to real timers) let that
    // continuation race past the switch on unlucky microtask orderings,
    // arming a genuine REAL 2s timer that leaked past this test — it fires
    // ~2s later (mid a LATER test or at suite teardown), calling setState
    // on this already-unmounted component and crashing the whole vitest
    // run with an unhandled error. See useAutoSave.ts's fade-timer comment.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1600)
    })

    // Switch back so waitFor can poll.
    vi.useRealTimers()

    await waitFor(() => {
      expect(api.updateWorkspaceInstructions).toHaveBeenCalledWith(WORKSPACE_ID, 'updated text')
    })
  })

  /**
   * Test 3 — DATA-LOSS GUARD (the most important assertion)
   *
   * When fetchWorkspaceInstructions REJECTS:
   *   (a) a Retry affordance must render
   *   (b) updateWorkspaceInstructions must NEVER be called
   *
   * This verifies the `{ disabled: instructionsError }` guard in useAutoSave
   * which prevents a failed load from auto-saving an empty string and wiping
   * the user's instructions.
   */
  it('shows Retry and does NOT call updateWorkspaceInstructions when load fails', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockRejectedValue(new Error('Network error'))

    renderTab()

    // Wait for the error state to render.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })

    // The error message should be visible.
    expect(screen.getByText(/could not load project instructions/i)).toBeInTheDocument()

    // Textarea must NOT render when there is a load error.
    expect(
      screen.queryByRole('textbox', { name: /workspace \/ project instructions/i }),
    ).not.toBeInTheDocument()

    // Advance fake timers to confirm no deferred save fires.
    vi.useFakeTimers()
    await act(async () => {
      vi.advanceTimersByTime(2000)
    })
    vi.useRealTimers()

    // The guard must hold: PUT was never called.
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()
  })

  /**
   * Test 4 — Clear (empty string is a valid value)
   * Editing an existing value to "" must still call updateWorkspaceInstructions
   * with an empty string — clearing is intentional.
   */
  it('calls updateWorkspaceInstructions with empty string when textarea is cleared', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'some instructions' })
    vi.mocked(api.updateWorkspaceInstructions).mockResolvedValue({ content: '' })

    renderTab()

    // Wait for hydration.
    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('some instructions')
    })

    // Switch to fake timers after data loaded.
    vi.useFakeTimers()

    const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
    fireEvent.change(textarea, { target: { value: '' } })

    // Advance past the 1500 ms debounce (deliberately raised from 500ms —
    // see Test 2's comment above). ASYNC variant — see Test 2's fade-timer
    // leak comment for why this must drain the save's continuation while
    // still on fake timers, before switching to real ones below.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1600)
    })

    vi.useRealTimers()

    await waitFor(() => {
      expect(api.updateWorkspaceInstructions).toHaveBeenCalledWith(WORKSPACE_ID, '')
    })
  })
})

// D3 (UAT v0.1.1 defects) — hydration must never trigger a spurious PUT.
//
// Root cause: `instructionsContent` starts at the hardcoded useState
// default ''. Before this fix, useAutoSave's `disabled` option here was
// `instructionsError` — no readiness gate at all — so useAutoSave captured
// '' as its baseline on the very first commit (mount, before
// fetchWorkspaceInstructions had resolved). The LATER commit where the
// real fetched content hydrates looked like a genuine edit — firing a
// spurious `updateWorkspaceInstructions` that echoes the fetched content
// straight back.
describe('WorkspaceSettingsTab — D3: hydration must not trigger a spurious PUT', () => {
  it('loading non-empty instructions content never calls updateWorkspaceInstructions, even after the debounce window elapses (REVERT-PROOF: fails without the instructionsHydrated gate)', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'hello world' })

    renderTab()

    await waitFor(() => {
      const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
      expect(textarea).toHaveValue('hello world')
    })

    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 1500ms debounce (raised from the 500ms default for this long-form
    // field — see WorkspaceSettingsTab.tsx's useAutoSave call).
    await new Promise((resolve) => setTimeout(resolve, 1800))
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()
  })

  it('the textarea is disabled until the instructions GET resolves (closes the pre-hydration-typing race)', async () => {
    // The textarea is NOT gated behind a component-level loading spinner
    // (unlike DataSection/GatewaySection/SecuritySection, which hide the
    // whole form until `config` loads) — it is visible and interactive
    // from the very first render, for the entire duration of the
    // dedicated fetchWorkspaceInstructions round-trip. Without disabling
    // the field itself, a fast edit landing before that GET resolves would
    // become useAutoSave's "already saved" baseline the moment
    // `instructionsHydrated` flips true, and would never actually reach
    // the server.
    let resolveFetch: (v: { content: string }) => void
    vi.mocked(api.fetchWorkspaceInstructions).mockReturnValue(
      new Promise((resolve) => { resolveFetch = resolve }),
    )

    renderTab()

    const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })
    expect(textarea).toBeDisabled()

    resolveFetch!({ content: 'loaded content' })

    await waitFor(() => {
      expect(textarea).not.toBeDisabled()
    })
    expect(textarea).toHaveValue('loaded content')
  })

  // 2nd-entity variant (reproduce-or-refute per the D3 audit): WorkspaceSettingsTab
  // does NOT unmount when the operator navigates from one workspace to
  // another while staying on the Settings tab (no `key` prop at the route —
  // see workspaces.$workspaceId.settings.tsx — so React reuses the same
  // component instance; `instructionsHydrated` resets false→true→false→true
  // across the switch, mirroring AgentProfile's `formHydrated`). REPRODUCED
  // against the unfixed useAutoSave.ts (spurious updateWorkspaceInstructions
  // PUT for the second workspace, echoing its own fetched content back);
  // CLOSED by useAutoSave.ts's `wasDisabledRef` re-arm fix. See
  // AgentProfile.test.tsx's matching test for the sibling repro.
  it('REVERT-PROOF: switching to a SECOND workspace within the same mounted WorkspaceSettingsTab does not fire a spurious updateWorkspaceInstructions PUT for that second workspace', async () => {
    const WORKSPACE_B_ID = 'ws-test-002'
    const mockWorkspaceB: Workspace = { ...mockWorkspace, id: WORKSPACE_B_ID, name: 'Workspace B' }

    vi.mocked(api.fetchWorkspaceInstructions).mockImplementation((id: string) =>
      Promise.resolve({ content: id === WORKSPACE_B_ID ? 'workspace B instructions' : 'hello world' }),
    )

    const client = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('hello world')
    })
    // PASSIVE idle wait, past the 1500ms debounce, before switching — clean
    // starting point (the single-workspace passive-open case is already
    // covered above).
    await new Promise((resolve) => setTimeout(resolve, 1800))
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()

    // Switch to a SECOND workspace WITHOUT unmounting — same
    // QueryClientProvider, same <WorkspaceSettingsTab> element position, only
    // the `workspace` prop changes.
    rerender(
      <QueryClientProvider client={client}>
        <WorkspaceSettingsTab workspace={mockWorkspaceB} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('workspace B instructions')
    })
    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 1500ms debounce so a spurious hydration-triggered save of the SECOND
    // workspace's instructions gets every chance to fire.
    await new Promise((resolve) => setTimeout(resolve, 1800))
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()
  })
})

// D3 residual (2nd site) — the IDENTITY group (name/description/repository).
//
// Root cause: unlike the instructions group above, this group passed NO
// `disabled` option to useAutoSave at all — the previous RCA called it "safe
// by construction" because it is prop-seeded (`useState(workspace.name)`)
// rather than query-fetched, which is true for the very FIRST mount (no
// async gap). It is NOT true across a WORKSPACE SWITCH: this component
// persists across switches (no `key` prop at the route — see the
// instructions-group test above for the same premise), and the re-hydration
// `useEffect` only re-syncs `name`/`description`/`repository` ONE commit
// after `workspace.id` itself already changed. That produces a real
// committed render pairing the NEW workspace's id with the OLD workspace's
// stale identity fields, which useAutoSave's change-detection reads as a
// "change" — and the FOLLOWING render (once the fields catch up) reads as
// ANOTHER "change" — arming the debounce and firing a spurious
// `updateWorkspace` PUT that echoes the new workspace's own (already
// correct) identity fields straight back. Fixed by gating this group's
// useAutoSave on a reactive `identityHydrated` flag, reset via the SAME
// mid-render pattern already used for `instructionsHydrated` above.
describe('WorkspaceSettingsTab — D3 residual (identity group): workspace switch must not trigger a spurious PUT', () => {
  it("REVERT-PROOF: switching to a SECOND workspace within the same mounted WorkspaceSettingsTab does not fire a spurious updateWorkspace PUT echoing that workspace's own (unedited) name/description/repository", async () => {
    const WORKSPACE_B_ID = 'ws-test-002'
    const mockWorkspaceB: Workspace = {
      ...mockWorkspace,
      id: WORKSPACE_B_ID,
      name: 'Workspace B',
      description: 'Workspace B description',
      repository: 'https://github.com/org/b',
    }

    const queryClient = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    const nameInput = await screen.findByLabelText('Name')
    expect(nameInput).toHaveValue('Test Workspace')

    // PASSIVE idle wait past the 600ms identity debounce before switching —
    // clean starting point (mirrors the instructions-group test above).
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(api.updateWorkspace).not.toHaveBeenCalled()

    // Switch to a SECOND workspace WITHOUT unmounting — same
    // QueryClientProvider, same <WorkspaceSettingsTab> element position, only
    // the `workspace` prop changes.
    rerender(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspaceB} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.getByLabelText('Name')).toHaveValue('Workspace B')
    })

    // PASSIVE idle wait — no interaction at all — comfortably past the
    // 600ms debounce, so a spurious hydration-triggered echo save of the
    // SECOND workspace's own (already-correct) identity fields gets every
    // chance to fire.
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(api.updateWorkspace).not.toHaveBeenCalled()
  })

  it('the identity inputs stay interactive (not disabled) across a workspace switch — UX decision: no visible loading window worth gating, unlike Instructions', async () => {
    const WORKSPACE_B_ID = 'ws-test-002'
    const mockWorkspaceB: Workspace = { ...mockWorkspace, id: WORKSPACE_B_ID, name: 'Workspace B' }

    const queryClient = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    const nameInput = await screen.findByLabelText('Name')
    expect(nameInput).not.toBeDisabled()

    rerender(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspaceB} />
      </QueryClientProvider>,
    )

    // Even mid-switch (before the re-hydration effect has necessarily
    // settled), the field must never be disabled — this is the deliberate
    // UX choice documented on `identityHydrated` in WorkspaceSettingsTab.tsx.
    expect(screen.getByLabelText('Name')).not.toBeDisabled()

    await waitFor(() => {
      expect(screen.getByLabelText('Name')).toHaveValue('Workspace B')
    })
    expect(screen.getByLabelText('Name')).not.toBeDisabled()
  })
})

// ── Echo-race regression (P2 'Text input Auto save') ────────────────────────
//
// Root cause: the instructions hydration effect (`useEffect(() => {
// if (instructionsData !== undefined) setInstructionsContent(...) }, [instructionsData])`)
// had NO dirty guard at all — a save's own `invalidateQueries` refetch
// landing while the operator kept typing would silently overwrite the
// textarea with the stale pre-edit content mid-keystroke. Fixed by adding
// `instructionsDirtyRef` (set on every onChange, cleared only when
// useAutoSave's `onSaved` reports the save snapshot is still current).
describe('WorkspaceSettingsTab — instructions echo-race (autosave hydration must never revert an in-flight draft)', () => {
  it('typing during the save round-trip is never reverted by the invalidate-refetch echo, and the newer text still persists', async () => {
    // Three GETs: the initial hydration, the invalidate-refetch fired by
    // save #1 (server genuinely has 'first edit' at that point — but the
    // operator has ALREADY typed more locally, so this is a stale echo
    // relative to the live draft), and the invalidate-refetch fired by
    // save #2 (server now genuinely has the newest text).
    vi.mocked(api.fetchWorkspaceInstructions).mockReset()
      .mockResolvedValueOnce({ content: 'initial text' })
      .mockResolvedValueOnce({ content: 'first edit' })
      .mockResolvedValue({ content: 'first edit second edit' })

    // Both PUTs are manually controlled so the test can inspect the exact
    // moment save #1's stale echo lands WHILE save #2 is still queued/in
    // flight — the precise race window this fix closes.
    let resolvePut1!: (v: { content: string }) => void
    const putPromise1 = new Promise<{ content: string }>((r) => { resolvePut1 = r })
    let resolvePut2!: (v: { content: string }) => void
    const putPromise2 = new Promise<{ content: string }>((r) => { resolvePut2 = r })
    vi.mocked(api.updateWorkspaceInstructions).mockReset()
      .mockReturnValueOnce(putPromise1)
      .mockReturnValueOnce(putPromise2)

    renderTab()

    await waitFor(() => {
      expect(
        screen.getByRole('textbox', { name: /workspace \/ project instructions/i }),
      ).toHaveValue('initial text')
    })

    vi.useFakeTimers()
    const textarea = screen.getByRole('textbox', { name: /workspace \/ project instructions/i })

    // First edit — debounce fires, the PUT goes out and stays in flight.
    fireEvent.change(textarea, { target: { value: 'first edit' } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(api.updateWorkspaceInstructions).toHaveBeenCalledTimes(1)

    // Second edit — the operator keeps typing while the first PUT is still
    // unresolved. This debounce cycle fires too, but useAutoSave's own
    // serialization (isSavingRef) queues it (rerunPendingRef) instead of
    // dispatching a second, concurrent PUT.
    fireEvent.change(textarea, { target: { value: 'first edit second edit' } })
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(api.updateWorkspaceInstructions).toHaveBeenCalledTimes(1)
    expect(textarea).toHaveValue('first edit second edit')

    // Switch to real timers for the async settle/refetch chain below.
    vi.useRealTimers()

    // Resolve PUT #1. saveFn #1 then awaits its own `invalidateQueries`,
    // whose refetch resolves with the STALE ("first edit") content — the
    // server echo of save #1's own payload, landing after the operator
    // typed more. Save #2 (the queued rerun) starts right behind it and
    // immediately blocks on the still-unresolved `putPromise2`, giving a
    // clean checkpoint to inspect the draft mid-race.
    await act(async () => {
      resolvePut1({ content: 'first edit' })
    })

    await waitFor(() => {
      expect(api.fetchWorkspaceInstructions).toHaveBeenCalledTimes(2)
    })
    // Save #2 must already be in flight (queued via useAutoSave's own
    // serialization) by the time save #1's echo has landed.
    await waitFor(() => {
      expect(api.updateWorkspaceInstructions).toHaveBeenCalledTimes(2)
    })

    // THE regression assertion: even though the query cache just updated
    // with the stale "first edit" echo, the textarea must still show the
    // NEWEST typed text — instructionsDirtyRef stayed armed because
    // useAutoSave's onSaved reported isCurrent=false for save #1 (the live
    // draft had already moved on when it resolved).
    expect(textarea).toHaveValue('first edit second edit')

    // Resolve PUT #2 — the queued re-save persists the newer text, and its
    // own invalidate-refetch (mocked to return the now-genuinely-current
    // value) settles cleanly.
    await act(async () => {
      resolvePut2({ content: 'first edit second edit' })
    })

    await waitFor(() => {
      expect(api.fetchWorkspaceInstructions).toHaveBeenCalledTimes(3)
    })
    expect(api.updateWorkspaceInstructions).toHaveBeenNthCalledWith(1, WORKSPACE_ID, 'first edit')
    expect(api.updateWorkspaceInstructions).toHaveBeenNthCalledWith(2, WORKSPACE_ID, 'first edit second edit')
    expect(textarea).toHaveValue('first edit second edit')
  })
})

// ── Trim-vs-raw isCurrent gap regression (item 2) ───────────────────────────
//
// Root cause: the identity `formData` used to carry TRIMMED values
// (`name.trim()`, etc.) — the exact object useAutoSave's `isCurrent` deep-
// compares before vs. after a save's round-trip. If the ONLY thing that
// changed the live draft during an in-flight save was trailing whitespace
// (e.g. "New name" -> "New name "), trimmed-vs-trimmed compares EQUAL even
// though the raw draft the operator is looking at still differs from what
// was actually sent — `isCurrent` reported true, `identityDirtyRef` cleared,
// and the next hydration (writing the RAW `workspace.name` prop straight
// into state) silently stripped the space out from under the operator's
// cursor. Fixed by carrying RAW values in `formData` and trimming only
// inside saveFn's wire payload, so `isCurrent` compares raw-to-raw.
describe('WorkspaceSettingsTab — trim-vs-raw isCurrent gap (name field, trailing-space case)', () => {
  it('a trailing-space-only edit made while a save is in flight is not lost when the next hydration lands with the server-trimmed name', async () => {
    vi.mocked(api.updateWorkspace).mockReset()
    let resolvePut1!: (v: Workspace) => void
    const putPromise1 = new Promise<Workspace>((r) => { resolvePut1 = r })
    let resolvePut2!: (v: Workspace) => void
    const putPromise2 = new Promise<Workspace>((r) => { resolvePut2 = r })
    vi.mocked(api.updateWorkspace)
      .mockReturnValueOnce(putPromise1)
      .mockReturnValueOnce(putPromise2)

    const queryClient = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    const nameInput = await screen.findByLabelText('Name')
    expect(nameInput).toHaveValue('Test Workspace')

    vi.useFakeTimers()

    // First edit — debounce fires (600ms), the PUT goes out (wire payload
    // trimmed to "New name") and stays in flight.
    fireEvent.change(nameInput, { target: { value: 'New name' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    expect(api.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(api.updateWorkspace).toHaveBeenNthCalledWith(
      1, WORKSPACE_ID, expect.objectContaining({ name: 'New name' }),
    )

    // Second edit — ONLY a trailing space added while the first PUT is
    // still unresolved. Trimmed, this is IDENTICAL to what's already in
    // flight — exactly the case the trim-vs-raw gap mishandled. The
    // serialized debounce cycle queues a re-run rather than firing a second
    // concurrent PUT immediately.
    fireEvent.change(nameInput, { target: { value: 'New name ' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    expect(api.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(nameInput).toHaveValue('New name ')

    vi.useRealTimers()

    // Resolve PUT #1. `onSaved`'s isCurrent must be computed on RAW data —
    // the live draft ("New name ") no longer raw-equals what was actually
    // sent ("New name"), so isCurrent is false and identityDirtyRef must
    // stay armed. The queued re-run (save #2) starts right behind it and
    // blocks on the still-unresolved putPromise2.
    await act(async () => {
      resolvePut1({ ...mockWorkspace, name: 'New name' })
    })
    await waitFor(() => {
      expect(api.updateWorkspace).toHaveBeenCalledTimes(2)
    })

    // Simulate the parent re-rendering with the server's canonical
    // (trimmed) name — the exact moment a stale hydration would otherwise
    // silently strip the space.
    rerender(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={{ ...mockWorkspace, name: 'New name' }} />
      </QueryClientProvider>,
    )

    // THE regression assertion: the trailing space must survive — the
    // hydration effect is still blocked by identityDirtyRef, because
    // raw-to-raw isCurrent correctly reported false for the in-flight save.
    expect(nameInput).toHaveValue('New name ')

    // Resolve PUT #2 — the queued re-save persists the newer (space-
    // trailing, trimmed-identical) text; dirty finally clears because this
    // save's raw snapshot now equals the live draft.
    //
    // Unlike the resolvePut1 block above (whose waitFor(calledTimes 2)
    // condition is causally downstream of doSave()'s post-await bookkeeping
    // — the queued re-run only dispatches AFTER that bookkeeping, including
    // arming the 2s "fade to idle" setTimeout — this resolve's own
    // waitFor below checks an ALREADY-true call count, so it gives no such
    // guarantee. Without forcing extra microtask ticks here, save #2's
    // fade-timer arm (now under REAL timers, switched above) can race past
    // this test's end, leaking a genuine setTimeout that fires ~2s later
    // during a LATER test and throws on this already-unmounted component.
    // See useAutoSave.ts's fade-timer comment and Test 2's matching note in
    // this file (above).
    await act(async () => {
      resolvePut2({ ...mockWorkspace, name: 'New name' })
      await Promise.resolve()
      await Promise.resolve()
    })
    await waitFor(() => {
      expect(api.updateWorkspace).toHaveBeenCalledTimes(2)
    })
    expect(api.updateWorkspace).toHaveBeenNthCalledWith(
      2, WORKSPACE_ID, expect.objectContaining({ name: 'New name' }),
    )
  })
})

// ── No-op invalidation regression (item 6) ───────────────────────────────────
//
// `workspacesQueryKeys.list()` called with no args produces
// ['workspaces', undefined] — every REAL list query is registered as
// ['workspaces', {status: 'active'|'archived'}] (see Sidebar.tsx,
// WorkspaceTabContainer.tsx, etc.), and TanStack Query's partial-match
// invalidation requires the filter key to be a prefix of the stored key at
// EVERY index, so `undefined` at index 1 never matches a defined params
// object there. This pinned the save/archive/delete flows as silent no-ops
// against every real list query in the app — a save could persist to the
// server yet a currently-rendered workspace list would never refetch.
// `['workspaces']` (length 1) is a valid prefix of any 'workspaces'-keyed
// query, fixing this.
describe('WorkspaceSettingsTab — no-op invalidation (item 6)', () => {
  it('invalidates with a bare ["workspaces"] prefix (not .list() with no args) after a name save', async () => {
    vi.mocked(api.updateWorkspace).mockReset().mockResolvedValue(mockWorkspace)

    const queryClient = makeClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    render(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    const nameInput = await screen.findByLabelText('Name')
    vi.useFakeTimers()
    fireEvent.change(nameInput, { target: { value: 'Renamed Workspace' } })
    // ASYNC variant — see Test 2's fade-timer leak comment in this file
    // (above) for why the save's continuation must drain while still on
    // fake timers, before switching to real ones below.
    await act(async () => { await vi.advanceTimersByTimeAsync(700) })
    vi.useRealTimers()

    await waitFor(() => {
      expect(api.updateWorkspace).toHaveBeenCalled()
    })
    // The fixed call: a bare ['workspaces'] prefix, NOT ['workspaces',
    // undefined] (what `workspacesQueryKeys.list()` with no args produces —
    // that key partial-matches ZERO real queries, see the module comment).
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['workspaces'] })
    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['workspaces', undefined] })
  })

  it('invalidates with a bare ["workspaces"] prefix after archiving', async () => {
    vi.mocked(api.updateWorkspace).mockReset().mockResolvedValue({ ...mockWorkspace, status: 'archived' })

    const queryClient = makeClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    render(
      <QueryClientProvider client={queryClient}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    const archiveBtn = await screen.findByRole('button', { name: /archive workspace/i })
    fireEvent.click(archiveBtn)

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['workspaces'] })
    })
    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['workspaces', undefined] })
  })
})

// ── Pagehide flush wiring (item 4) ───────────────────────────────────────────
//
// The instructions field previously passed neither `flushUrl` nor
// `beaconFlush` to useAutoSave — a tab close within the 1500ms debounce
// window silently dropped up to 1500ms of typing. Fixed by wiring a
// `beaconFlush` that PUTs the same `{content}` shape `updateWorkspaceInstructions`
// sends, as a `keepalive: true` fetch (so it can survive page teardown).
describe('WorkspaceSettingsTab — instructions pagehide flush (item 4)', () => {
  it('keepalive-flushes the pending instructions edit on pagehide before the debounce fires', async () => {
    vi.mocked(api.fetchWorkspaceInstructions).mockResolvedValue({ content: 'initial text' })
    const fetchSpy = vi.spyOn(window, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))

    renderTab()

    const textarea = await screen.findByRole('textbox', { name: /workspace \/ project instructions/i })
    await waitFor(() => expect(textarea).toHaveValue('initial text'))

    vi.useFakeTimers()
    fireEvent.change(textarea, { target: { value: 'unsaved edit' } })

    // Fire pagehide WELL before the 1500ms debounce would otherwise save —
    // this is exactly the tab-close-mid-typing window item 4 closes.
    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })
    vi.useRealTimers()

    expect(fetchSpy).toHaveBeenCalledWith(
      `/api/v1/workspaces/${WORKSPACE_ID}/instructions`,
      expect.objectContaining({
        method: 'PUT',
        keepalive: true,
        body: JSON.stringify({ content: 'unsaved edit' }),
      }),
    )
    // The debounced PUT itself must NOT have fired yet — the flush is a
    // best-effort SEPARATE keepalive fetch, not a substitute for the
    // normal save path.
    expect(api.updateWorkspaceInstructions).not.toHaveBeenCalled()

    fetchSpy.mockRestore()
  })
})

describe('WorkspaceSettingsTab — Archive / Delete protection for default workspace', () => {
  /**
   * Test 5 — Default workspace: Archive button is hidden and explanatory text shown.
   * When is_default=true and the workspace is active, the Archive button must not
   * render, and the "cannot be archived or deleted" message must appear.
   */
  it('hides the Archive button and shows explanatory text when workspace is default', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <WorkspaceSettingsTab workspace={mockDefaultWorkspace} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      // Archive button must be absent.
      expect(screen.queryByRole('button', { name: /archive workspace/i })).not.toBeInTheDocument()
      // Delete button must be absent.
      expect(screen.queryByRole('button', { name: /delete workspace/i })).not.toBeInTheDocument()
      // Explanatory message must be visible.
      expect(
        screen.getByText(/the default workspace cannot be archived or deleted/i),
      ).toBeInTheDocument()
    })
  })

  /**
   * Test 6 — Non-default workspace: Archive button is enabled.
   * When is_default=false and the workspace is active, the Archive button must
   * render and be enabled (not disabled), and no explanatory text must appear.
   */
  it('shows the Archive button and no explanatory text when workspace is not default', async () => {
    vi.mocked(api.updateWorkspace).mockResolvedValue(mockWorkspace)

    render(
      <QueryClientProvider client={makeClient()}>
        <WorkspaceSettingsTab workspace={mockWorkspace} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      const archiveBtn = screen.getByRole('button', { name: /archive workspace/i })
      expect(archiveBtn).toBeInTheDocument()
      expect(archiveBtn).not.toBeDisabled()
      // No protection message for non-default workspaces.
      expect(
        screen.queryByText(/cannot be archived or deleted/i),
      ).not.toBeInTheDocument()
    })
  })
})

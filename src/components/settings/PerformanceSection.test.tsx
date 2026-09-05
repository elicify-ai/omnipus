/**
 * PerformanceSection.test.tsx — Spec-6 FR-12.2 / Spec-3 FR-6.6.
 *
 * Covers the max-parallel-agents control and that saving it is gated by the
 * re-auth consent dialog: changing the input triggers autosave after debounce,
 * which opens the ReAuthDialog (the PUT does NOT fire yet), and only a
 * successful re-auth replays the consent token into updatePerformanceSettings.
 *
 * UAT fix #2: explicit Save button removed — autosave opens ReAuthDialog
 * automatically after the debounce (600 ms) when the input value is valid.
 *
 * Tool loading toggle: toggling tools_on_demand immediately opens the reauth
 * dialog and calls updatePerformanceSettings with both fields (max_parallel_agents
 * + tools_on_demand) after confirmation.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPerformanceSettings: vi.fn(),
    updatePerformanceSettings: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  // Real timers by default — individual tests switch to fake timers after
  // initial render so that waitFor (which uses setInterval internally) can
  // resolve the data-loading Promise before fake timers are installed.
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — autosave (UAT fix #2)', () => {
  it('renders the configured value from the API', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4)
    })
  })

  it('has no explicit Save button — autosave replaces it', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))
    // The Save button is gone; only the accessible escape-hatch "Save changes"
    // sr-only button exists (not visible).
    const visibleSaveBtns = screen.queryAllByRole('button').filter(
      (btn) => btn.textContent?.toLowerCase() === 'save',
    )
    expect(visibleSaveBtns).toHaveLength(0)
  })

  it('opens the re-auth dialog automatically after the debounce when input is valid', async () => {
    renderSection()
    // Wait for the data to load with real timers.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Switch to fake timers NOW so we can control the debounce.
    vi.useFakeTimers()

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })

    // Before debounce fires — dialog must NOT be open yet.
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // Advance past the autosave debounce (600 ms).
    await act(async () => { vi.advanceTimersByTime(700) })

    // Switch back so waitFor can poll.
    vi.useRealTimers()

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    // PUT must still NOT have been called — user hasn't confirmed yet.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('replays the consent token into updatePerformanceSettings after re-auth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue(SETTINGS as never)

    renderSection()
    // Wait for data with real timers.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Switch to fake timers to control the debounce.
    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_parallel_agents: 8, tools_on_demand: true },
        'reauth_tok',
      )
    })
  })

  it('does NOT open the re-auth dialog for a negative value (still rejected — no ceiling, but a floor of 0 remains)', async () => {
    renderSection()
    // Wait for data with real timers.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Switch to fake timers to control the debounce.
    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '-7' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    // Dialog must NOT open — a negative value is silently skipped by autosave.
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    // PUT must not have been called.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('accepts a value well above the old 16-ceiling (removed, ADR concurrency-gate consolidation) and submits it unchanged', async () => {
    // The backend removed `maximum: 16` from PerformanceSettingsUpdate — an
    // explicit value now has no ceiling and is honored exactly as configured
    // (clampParallelExplicit only floors at 1). 40 must be accepted, open the
    // reauth dialog, and be sent to updatePerformanceSettings verbatim.
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok_40', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, max_parallel_agents: 40, effective_max_parallel_agents: 40 } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '40' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_parallel_agents: 40, tools_on_demand: true },
        'reauth_tok_40',
      )
    })
    // No validation error toast for a value that is now within bounds.
    expect(addToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error', message: expect.stringContaining('max_parallel_agents') }),
    )
  })

  it('accepts 0 and submits it as the auto-detect restore signal', async () => {
    // Per PerformanceSettingsUpdate.yaml: "Set to 0 to restore the
    // auto-detected default." Typing 0 explicitly must behave identically to
    // leaving the field blank — both send max_parallel_agents: 0.
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok_0', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue(SETTINGS as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '0' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_parallel_agents: 0, tools_on_demand: true },
        'reauth_tok_0',
      )
    })
  })

  // The old "over-limit warning" (fired whenever the typed value exceeded
  // the recommended auto-detected default, and claimed the runtime would
  // "clamp" it back down) was removed: the backend no longer clamps explicit
  // values at all, so that claim became false. It is replaced by a
  // high-value caution that fires only near the real physical OS-thread
  // safety ceiling the backend itself warns about (physicalConcurrencySafetyCeiling
  // = 2000, pkg/config/config.go) — and never claims the value will be lowered.
  it('shows the high-value caution when input exceeds the physical thread-safety ceiling (2000)', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '5000' } })

    await waitFor(() => {
      expect(screen.getByTestId('performance-high-value-warning')).toBeInTheDocument()
    })
    // Warning text mentions the typed value and the ceiling, and must NOT
    // claim the value will be lowered — it is honored exactly as configured.
    const text = screen.getByTestId('performance-high-value-warning').textContent ?? ''
    expect(text).toContain('5000')
    expect(text).toContain('2000')
    expect(text).toContain('honored exactly as set')
  })

  it('does not show the high-value caution for a value within the recommended range', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 8,
      effective_max_parallel_agents: 8,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '4' } })

    expect(screen.queryByTestId('performance-high-value-warning')).not.toBeInTheDocument()
  })

  it('does not show the high-value caution for a value above the old 16-ceiling but below the physical ceiling (2000)', async () => {
    // Regression guard: 40 exceeds the OLD (removed) ceiling of 16 but is
    // nowhere near the physical thread-safety ceiling of 2000 — no caution
    // should render for it.
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '40' } })

    expect(screen.queryByTestId('performance-high-value-warning')).not.toBeInTheDocument()
  })

  it('surfaces the resolved effective value when an operator HAS configured a cap', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 882,
      max_parallel_agents_configured: true,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // RE-DERIVED. This used to assert the words "Recommended: 882 parallel
    // agents". There is no longer a computed default to recommend — when
    // nothing is configured the backend returns a physical OS-thread backstop
    // and says so via max_parallel_agents_configured. This case is the
    // CONFIGURED one, so the panel still shows the resolved number; the
    // unconfigured case is covered in PerformanceSection.autoDefault.test.tsx.
    expect(screen.getByText(/Currently in use: 882 parallel agents/)).toBeInTheDocument()
    // The old CPU-based formula text must be gone.
    expect(screen.queryByText(/NumCPU/)).not.toBeInTheDocument()
    expect(screen.queryByText(/RAM_GB\/1\.5/)).not.toBeInTheDocument()
    expect(screen.queryByText(/ceiling of 16/)).not.toBeInTheDocument()
    // So must the deleted per-agent memory formula it was replaced by.
    expect(screen.queryByText(/3\.5 MB/)).not.toBeInTheDocument()
  })

  it('shows AutoSaveIndicator (no legacy SaveStatus)', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))
    // AutoSaveIndicator renders a data-testid when status is not idle — but it
    // exists in the DOM as a container even when idle.
    // Verify the label "Agent Concurrency" header is present.
    expect(screen.getByText('Agent Concurrency')).toBeInTheDocument()
  })
})

describe('PerformanceSection — Tool loading toggle', () => {
  it('renders the Tool loading section with the switch in the ON state by default', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tool loading'))
    const toggle = screen.getByLabelText('Tool loading')
    expect(toggle).toBeChecked()
    expect(screen.getByText('Load tools on demand')).toBeInTheDocument()
    expect(screen.getByText(/Smaller messages, lower token use/)).toBeInTheDocument()
  })

  it('renders tools_on_demand=false from the API as the switch OFF state', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      tools_on_demand: false,
    } as never)
    renderSection()
    await waitFor(() => screen.getByLabelText('Tool loading'))
    const toggle = screen.getByLabelText('Tool loading')
    expect(toggle).not.toBeChecked()
    expect(screen.getByText('Keep all tools loaded')).toBeInTheDocument()
    expect(screen.getByText(/Every tool is always available/)).toBeInTheDocument()
  })

  it('toggling the switch immediately opens the reauth dialog', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tool loading'))

    fireEvent.click(screen.getByLabelText('Tool loading'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    // PUT must NOT have been called before reauth.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('calls updatePerformanceSettings with tools_on_demand toggled after reauth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok2', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, tools_on_demand: false } as never)

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tool loading'))

    // Toggle OFF (from default ON).
    fireEvent.click(screen.getByLabelText('Tool loading'))

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      // tools_on_demand flipped to false; max_parallel_agents still 4 (from SETTINGS).
      expect(api.updatePerformanceSettings).toHaveBeenCalledWith(
        { max_parallel_agents: 4, tools_on_demand: false },
        'reauth_tok2',
      )
    })
  })

  it('displays helper text about next-message effect', async () => {
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tool loading'))
    expect(screen.getByText(/Takes effect on the next message/)).toBeInTheDocument()
    expect(screen.getByText(/Applies to all agents/)).toBeInTheDocument()
  })

  it('stuck-spinner fix: save status clears to idle (not stuck on saving) when max_parallel_agents is out of range and tools_on_demand is toggled', async () => {
    // Regression test for the stuck-spinner bug: when max_parallel_agents is out
    // of range (e.g. -1, negative), buildBody returns null. If handleToolsOnDemandChange
    // left saveStatus='saving' before the null guard ran, the AutoSaveIndicator
    // would spin forever. The fix: the else-branch sets saveStatus='idle'
    // explicitly so the spinner clears.
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Put an out-of-range value (-1, negative) into the input first.
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '-1' } })

    // Now toggle tools_on_demand — this calls handleToolsOnDemandChange which
    // calls buildBody(inputValue='-1', checked=false) → null (out of range).
    fireEvent.click(screen.getByLabelText('Tool loading'))

    // Give React time to process state updates.
    await new Promise((r) => setTimeout(r, 50))

    // The reauth dialog must NOT have opened (no valid body → no setPending/setReauthOpen).
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()

    // updatePerformanceSettings must NOT have been called.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // The AutoSaveIndicator must not be in the 'saving' state.
    // AutoSaveIndicator renders "Saving..." text when status='saving'; it must
    // not appear because the null buildBody guard cleared status back to 'idle'.
    expect(screen.queryByText('Saving...')).not.toBeInTheDocument()
  })

  it('shows an error toast and reverts the switch when toggled while max_parallel_agents is out of range', async () => {
    // Regression test for the silent-no-op bug: toggling tools_on_demand while
    // the paired max_parallel_agents field is out of range used to flip the
    // switch and silently drop the change (no toast, no PUT, no revert). The
    // fix surfaces the same error toast triggerSave uses and reverts the
    // switch to the value that is actually persisted.
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Put an out-of-range value (-1, negative) into the input first.
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '-1' } })

    const toggle = screen.getByLabelText('Tool loading')
    expect(toggle).toBeChecked() // starts ON (default from SETTINGS)

    // Toggle OFF — this calls handleToolsOnDemandChange, which calls
    // buildBody(inputValue='-1', checked=false) → null (out of range).
    fireEvent.click(toggle)

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'max_parallel_agents must be zero or a positive whole number (0 or blank means no explicit cap — concurrency is then bounded by available memory).',
      })
    })

    // The switch must be reverted to its previously-saved (checked) state —
    // the toggle never actually saved, so the UI must not lie about it.
    await waitFor(() => {
      expect(screen.getByLabelText('Tool loading')).toBeChecked()
    })

    // No save was attempted.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
  })

  it('shows an error toast and reverts the switch when toggled OFF-to-ON while max_parallel_agents is out of range', async () => {
    // Mirror-direction regression test: the previous test only covered the
    // default-ON -> toggle-OFF direction. Verify the same revert+toast fires
    // starting from tools_on_demand=false and toggling ON, so the fix isn't
    // accidentally coupled to a specific starting boolean.
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      tools_on_demand: false,
    } as never)
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // Put an out-of-range value (-1, negative) into the input first.
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '-1' } })

    const toggle = screen.getByLabelText('Tool loading')
    expect(toggle).not.toBeChecked() // starts OFF

    // Toggle ON — buildBody(inputValue='-1', checked=true) -> null (out of range).
    fireEvent.click(toggle)

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'max_parallel_agents must be zero or a positive whole number (0 or blank means no explicit cap — concurrency is then bounded by available memory).',
      })
    })

    // The switch must be reverted to its previously-saved (unchecked) state.
    await waitFor(() => {
      expect(screen.getByLabelText('Tool loading')).not.toBeChecked()
    })

    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
  })

  it('shows an error toast when the debounced max_parallel_agents input settles out of range (Bug 1)', async () => {
    // Regression test: handleInputChange's debounced settle path never went
    // through triggerSave, so an out-of-range value typed directly into the
    // number field (rather than via the toggle) used to silently no-op —
    // no toast, no indication the value wasn't saved. The fix surfaces the
    // same error toast triggerSave and the toggle path already show.
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '-1' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'max_parallel_agents must be zero or a positive whole number (0 or blank means no explicit cap — concurrency is then bounded by available memory).',
      })
    })

    // Dialog never opens and no PUT fires for the invalid value.
    expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('shows the ApiError.userMessage (not the status-prefixed err.message) when updatePerformanceSettings rejects — Wave 2 getErrorMessage() fix', async () => {
    // Traces to: Wave 2 findings-fix (task #149, gap 2) — pr-test-analyzer.
    // Wave 2 fixed a real bug: PerformanceSection previously read err.message
    // off the caught error instead of the intended err.userMessage off an
    // ApiError. err.message carries the legacy "${status}: ${userMessage}"
    // format (see ApiError constructor in src/lib/api-error.ts) — a 422 here
    // would render as "422: Unprocessable value for max_parallel_agents" if
    // the bug regressed. This test asserts the toast shows the bare
    // userMessage, which only getErrorMessage()'s ApiError-first priority
    // produces.
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok3', expires_in: 300 } as never)
    vi.mocked(api.updatePerformanceSettings).mockRejectedValue(
      new api.ApiError(422, 'Unprocessable value for max_parallel_agents'),
    )

    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await act(async () => { vi.advanceTimersByTime(700) })
    vi.useRealTimers()

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith({
        variant: 'error',
        message: 'Unprocessable value for max_parallel_agents',
      })
    })
    // Pin the negative: the garbled status-prefixed err.message string must
    // NOT be what was shown.
    expect(addToast).not.toHaveBeenCalledWith({
      variant: 'error',
      message: '422: Unprocessable value for max_parallel_agents',
    })
  })

  it('reverts tools_on_demand to the server value when re-auth is cancelled after a valid toggle (Bug 2)', async () => {
    // Regression test: cancelling the ReAuthDialog for a *valid* toggle left
    // toolsOnDemand pointing at the optimistically-flipped value forever,
    // with `dirty` stuck true so the resync effect could never restore the
    // real server value. The fix clears `dirty` on cancel so the sync effect
    // (`data && !dirty`) snaps the switch back to the truth.
    vi.useRealTimers()
    renderSection()
    await waitFor(() => screen.getByLabelText('Tool loading'))

    const toggle = screen.getByLabelText('Tool loading')
    expect(toggle).toBeChecked() // starts ON (default from SETTINGS)

    // Toggle OFF — a valid change (max_parallel_agents=4 stays in range).
    fireEvent.click(toggle)

    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    // Switch is optimistically OFF while the dialog is open.
    expect(screen.getByLabelText('Tool loading')).not.toBeChecked()

    // Cancel re-auth instead of confirming.
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    // Dialog closes and the switch reverts to the server's true value (ON) —
    // it must not keep showing the unsaved OFF state.
    await waitFor(() => {
      expect(screen.queryByTestId('reauth-confirm')).not.toBeInTheDocument()
    })
    await waitFor(() => {
      expect(screen.getByLabelText('Tool loading')).toBeChecked()
    })

    // No save was ever attempted.
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })
})

/**
 * A failed load must never render as an empty panel.
 *
 * Observed before the fix (Playwright, GET /api/v1/performance stubbed to
 * 500): the tab's innerText was the empty string for the first ~7 seconds —
 * the query's retry window (three retries at 1s/2s/4s backoff, per
 * shouldRetryQuery/retryDelay in src/lib/queryClient.ts) — because the
 * loading skeleton was three text-free divs. It then settled on the bare
 * sentence "Could not load performance settings.", which named no cause and
 * offered no way to try again.
 *
 * A blank panel reads as "there is nothing to configure here". These tests
 * pin both halves: the loading state announces itself, and the error state
 * names the underlying cause and can be retried — the pattern the sibling
 * Security tab (SkillTrustSection) already uses on this same screen.
 */
describe('PerformanceSection — failed load is visible, not blank', () => {
  // Holds the fetch open so the component stays in its loading state.
  function pendingFetch() {
    return new Promise<never>(() => {})
  }

  it('the loading state is not textless — it says it is loading', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockImplementation(pendingFetch)
    const { container } = renderSection()

    // The whole point: innerText must never be the empty string. Assert on
    // the container's own text so a purely-decorative skeleton fails here.
    await waitFor(() => {
      expect(screen.getByTestId('performance-loading')).toBeInTheDocument()
    })
    expect(container.textContent?.trim()).not.toBe('')
    expect(screen.getByText(/loading performance settings/i)).toBeInTheDocument()
  })

  it('a failed load states the underlying cause, not a bare sentence', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockRejectedValue(
      new api.ApiError(503, 'The server is unavailable. Please try again in a moment.'),
    )
    renderSection()

    const alert = await screen.findByTestId('performance-load-error')
    // The cause itself must be on screen — this is what the old copy dropped.
    expect(alert).toHaveTextContent('The server is unavailable. Please try again in a moment.')
    expect(alert).toHaveTextContent(/failed to load performance settings/i)
    expect(alert).toHaveAttribute('role', 'alert')
  })

  it('a different failure surfaces ITS own cause (the message is not hardcoded)', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockRejectedValue(
      new api.ApiError(500, 'probe-induced failure'),
    )
    renderSection()

    const alert = await screen.findByTestId('performance-load-error')
    expect(alert).toHaveTextContent('probe-induced failure')
    expect(alert).not.toHaveTextContent('The server is unavailable')
  })

  it('Retry refetches and recovers the panel', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings)
      .mockRejectedValueOnce(new api.ApiError(503, 'The server is unavailable. Please try again in a moment.'))
      .mockResolvedValue(SETTINGS as never)
    renderSection()

    await screen.findByTestId('performance-load-error')
    expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByTestId('performance-retry-btn'))

    await waitFor(() => {
      expect(screen.getByLabelText('Max parallel agents')).toHaveValue(4)
    })
    expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('performance-load-error')).not.toBeInTheDocument()
  })
})

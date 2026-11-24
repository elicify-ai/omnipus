/**
 * PerformanceSection.test.tsx — the Limits "Agents and goals" group under the
 * Settings redesign #1519 (FR-010; the group name is the spec's: Limits holds
 * "Context and tool results, Model overrides, and Agents and goals").
 *
 * The old file covered the removed autosave model (debounce + per-change
 * "Change the performance settings?" confirm + AutoSaveIndicator + error
 * toasts that reverted optimistic toggles). FR-010 replaces all of it: a
 * change is a draft on the section's Save bar; Save changes sends one
 * partial PUT with only the changed fields and the consent token
 * (FR-015/FR-026/FR-027); the moved rows are password-marked (FR-025, the
 * "Limits → PUT /performance (password)" move table); an out-of-range value
 * is a field error that blocks the bar (FR-013, Dataset D-10); a refused
 * Save keeps its draft (FR-015/FR-020 outcome 3); Discard returns drafts to
 * the saved values (FR-012). The typed-0 "restore auto-detect" rule is
 * replaced by D-10 row 9: empty means no fixed limit, and the field's range
 * starts at 1.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
}

// Platform mode (identity.mode: 'platform') — the shell prompt is the
// confirm variant (FR-032); the password-mode (local edition) counterpart
// lives in PerformanceSection.password.test.tsx.
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

// The FR-026 prompt is shell-owned (SettingsShellContext + the shell's prompt
// hook ship with the redesign and are absent from the pre-change tree) —
// loaded lazily and failing loudly as BLOCKED RED, never as a collection
// error.
type ShellCtx = typeof import('./SettingsShellContext')
let shellCtxModule: ShellCtx | null = null
async function loadShellContext() {
  if (shellCtxModule) return shellCtxModule
  try {
    shellCtxModule = await vi.importActual<ShellCtx>('./SettingsShellContext')
    return shellCtxModule
  } catch {
    throw new Error(
      'BLOCKED RED (FR-010/FR-026/FR-027, D-8): the Settings shell context (SettingsShellContext) does not ' +
        'exist yet — required for the one password prompt per Save (FR-026) and the consent-token write (FR-027)',
    )
  }
}

function promptRecorder() {
  const prompts: string[][] = []
  const requestPassword = vi.fn(async (labels: string[]) => {
    prompts.push(labels)
    return 'reauth-tok'
  })
  return { prompts, requestPassword }
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

async function renderSectionWithShell(requestPassword: (labels: string[]) => Promise<string | null>) {
  const { SettingsShellContext } = await loadShellContext()
  return render(
    <QueryClientProvider client={makeClient()}>
      <SettingsShellContext.Provider
        value={{
          announce: () => {},
          setDraftCount: () => {},
          offerRestart: () => {},
          refreshPendingRestart: () => {},
          requestPassword,
          saveBarVisible: false,
          setSaveBarVisible: () => {},
        }}
      >
        <PerformanceSection />
      </SettingsShellContext.Provider>
    </QueryClientProvider>,
  )
}

/** The row's error: the alert the field's aria-describedby names (FR-013). */
function fieldAlertText(input: HTMLElement): string {
  const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
  const alert = ids.map((id) => document.getElementById(id)).find((el) => el?.getAttribute('role') === 'alert')
  expect(alert, 'the field must show its error as the alert its aria-describedby names').not.toBeNull()
  return alert?.textContent ?? ''
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — the one save rule (FR-010)', () => {
  it('renders the configured value from the API', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByLabelText(/max parallel agents/i)).toHaveValue('4')
    })
  })

  it('a drafted change shows the section Save bar with Save changes; there is no per-control save button (FR-010, FR-011)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))
    // No bare "Save" button on any control (FR-010: the bar is the one save).
    const visibleSaveBtns = screen.queryAllByRole('button').filter(
      (btn) => btn.textContent?.toLowerCase() === 'save',
    )
    expect(visibleSaveBtns).toHaveLength(0)

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '8' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument()
  })

  it('typing opens no prompt and stores nothing — the draft waits on the bar (FR-010, FR-011; the FR-026 prompt runs only at Save)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '8' } })

    // No confirmation dialog opens on typing, and nothing is sent.
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    // The bar counts the draft and its password mark (FR-025: the moved
    // Limits rows are password-marked).
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByText(/1 needs your password/)).toBeInTheDocument()
  })

  it('Save changes sends the drafted value once, with the consent token (FR-010, FR-015, FR-026, FR-027)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue(SETTINGS as never)
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '8' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    // One prompt listing the password-marked value by its visible label (FR-026).
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Max parallel agents'])
    // One partial PUT carrying only the changed field (FR-015) and the token (FR-027).
    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0]).toEqual([
        { max_parallel_agents: 8 },
        'reauth-tok',
      ])
    })
    // FR-018: the saved signal replaces the old confirm/save machinery.
    await screen.findByText(/Saved at/)
  })

  it('a negative value is refused by the field; the bar blocks Save, focus moves to the field and nothing is sent (FR-013, D-10)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    const input = screen.getByLabelText(/max parallel agents/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '-7' } })

    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toMatch(/1 or more/)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    expect(saveBtn).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(saveBtn)
    expect(input).toHaveFocus()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('a value above the removed 16-ceiling drafts and is stored verbatim (FR-010; no ceiling remains)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_parallel_agents: 40,
      effective_max_parallel_agents: 40,
    } as never)
    const { requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '40' } })
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))

    await waitFor(() => {
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0]).toEqual([
        { max_parallel_agents: 40 },
        'reauth-tok',
      ])
    })
    // No validation error toast for a value that is within bounds.
    expect(addToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error', message: expect.stringContaining('max_parallel_agents') }),
    )
  })

  it('D-10 replaces the typed-0 restore rule: 0 is refused; clearing to blank is accepted and saves the no-fixed-limit signal 0 (D-10 row 9, FR-013)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue(SETTINGS as never)
    const { requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))
    const input = screen.getByLabelText(/max parallel agents/i) as HTMLInputElement

    // Typing 0 is no longer a save: the field range starts at 1 (D-10).
    fireEvent.change(input, { target: { value: '0' } })
    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toMatch(/1 or more/)
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(input).toHaveFocus()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // Empty means automatic (D-10 row 9): clearing is a valid draft, and Save
    // sends the update contract's no-fixed-limit signal 0.
    fireEvent.change(input, { target: { value: '' } })
    await waitFor(() => expect(input.getAttribute('aria-invalid')).not.toBe('true'))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => {
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0]).toEqual([
        { max_parallel_agents: 0 },
        'reauth-tok',
      ])
    })
  })

  // The caution's exact prose is not spec-pinned; the pinned properties are:
  // it names the typed value, it names the physical thread ceiling, and it
  // never claims the value will be lowered or clamped.
  it('shows the high-value caution when the drafted value exceeds the physical thread-safety ceiling (2000)', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '5000' } })

    await waitFor(() => {
      expect(screen.getByTestId('performance-high-value-warning')).toBeInTheDocument()
    })
    const text = screen.getByTestId('performance-high-value-warning').textContent ?? ''
    expect(text).toContain('5000')
    expect(text).toMatch(/2,000|2000/)
    expect(text).toMatch(/used exactly as set/i)
    expect(text).toMatch(/may run out of threads/i)
    // It must NOT claim the value will be lowered or clamped.
    expect(text).not.toMatch(/clamped|lowered/i)
  })

  it('does not show the high-value caution for a drafted value within the recommended range', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 8,
      effective_max_parallel_agents: 8,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '4' } })

    expect(screen.queryByTestId('performance-high-value-warning')).not.toBeInTheDocument()
  })

  it('does not show the high-value caution for a drafted value above the old 16-ceiling but below the physical ceiling (2000)', async () => {
    // Regression guard: 40 exceeds the OLD (removed) ceiling of 16 but is
    // nowhere near the physical thread-safety ceiling of 2000 — no caution
    // should render for it.
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '40' } })

    expect(screen.queryByTestId('performance-high-value-warning')).not.toBeInTheDocument()
  })

  it('surfaces the resolved effective value when an operator HAS configured a cap', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 882,
      max_parallel_agents_configured: true,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

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

  it('no autosave status exists (FR-010 removes the AutoSaveIndicator); the resolved-value panel stands in', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))
    // The "Agent Concurrency" header and its AutoSaveIndicator are replaced
    // by the resolved-value panel and the one Save bar (FR-010).
    expect(screen.getByText(/Currently in use: 4 parallel agents/)).toBeInTheDocument()
    expect(screen.queryByText('Saving...')).not.toBeInTheDocument()
    expect(screen.queryByText(/autosav/i)).not.toBeInTheDocument()
  })
})

describe('Tool loading (drafts under FR-010)', () => {
  it('renders the Tool loading switch in the ON state by default, with the wireframe\'s row description', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/tool loading/i))
    const toggle = screen.getByLabelText(/tool loading/i)
    expect(toggle).toBeChecked()
    // The wireframe (the wording authority) replaces the old per-state copy
    // ("Load tools on demand" / "Smaller messages, lower token use") with one
    // row description.
    expect(screen.getByText(/Load tools only when an agent needs them/)).toBeInTheDocument()
  })

  it('renders tools_on_demand=false from the API as the switch OFF state', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      tools_on_demand: false,
    } as never)
    renderSection()
    await waitFor(() => screen.getByLabelText(/tool loading/i))
    const toggle = screen.getByLabelText(/tool loading/i)
    expect(toggle).not.toBeChecked()
    expect(screen.getByText(/Load tools only when an agent needs them/)).toBeInTheDocument()
  })

  it('toggling drafts the change and stores nothing (FR-010)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/tool loading/i))

    fireEvent.click(screen.getByLabelText(/tool loading/i))

    // The toggle becomes a draft on the bar; no confirm dialog opens and
    // nothing is sent (FR-010/FR-011).
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })

  it('Save changes sends the drafted toggle once, with the consent token (FR-010, FR-015, FR-026, FR-027)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({ ...SETTINGS, tools_on_demand: false } as never)
    const { prompts, requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText(/tool loading/i))

    // Toggle OFF (from default ON).
    fireEvent.click(screen.getByLabelText(/tool loading/i))
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompts.length).toBe(1))
    expect(prompts[0]).toEqual(['Tool loading'])
    // Only the changed field is sent (FR-015) — the old paired
    // max_parallel_agents echo is replaced by the partial-update rule.
    await waitFor(() => {
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0]).toEqual([
        { tools_on_demand: false },
        'reauth-tok',
      ])
    })
  })

  it('the row copy explains what the switch changes (the wireframe description replaces the old next-message helper text)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/tool loading/i))
    expect(screen.getByText(/Load tools only when an agent needs them/)).toBeInTheDocument()
    expect(screen.getByText(/each message stays small/)).toBeInTheDocument()
  })

  it('an invalid cap and a drafted toggle sit as drafts; the bar blocks Save and nothing is sent (FR-013)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    // An out-of-range cap (-1) and a drafted toggle: both are drafts, the
    // invalid one blocks the bar (FR-013) — no dialog, no PUT, no autosave
    // "Saving..." state (the old stuck-spinner regression stays covered).
    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '-1' } })
    fireEvent.click(screen.getByLabelText(/tool loading/i))

    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.queryByText('Saving...')).not.toBeInTheDocument()
  })

  it('a drafted toggle next to an invalid cap stays drafted (no revert, no toast) until the bar unblocks (FR-013)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '-1' } })
    const toggle = screen.getByLabelText(/tool loading/i)
    expect(toggle).toBeChecked() // starts ON (default from SETTINGS)

    fireEvent.click(toggle)

    // The drafted OFF state is kept (FR-013 keeps drafts; the old oracle —
    // an error toast plus reverting the switch — is replaced). Nothing is
    // sent while the bar is blocked.
    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(addToast).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('the same blocked state holds drafting OFF-to-ON beside an invalid cap (FR-013)', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      tools_on_demand: false,
    } as never)
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '-1' } })
    const toggle = screen.getByLabelText(/tool loading/i)
    expect(toggle).not.toBeChecked() // starts OFF

    fireEvent.click(toggle)

    await waitFor(() => expect(toggle).toBeChecked())
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(addToast).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('an out-of-range typed value never writes and never toasts — the field error and the blocked bar carry it (FR-013)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    const input = screen.getByLabelText(/max parallel agents/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '-1' } })

    await waitFor(() => expect(input.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(input)).toMatch(/1 or more/)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
    expect(addToast).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('a refused Save shows the server\'s userMessage — not the status-prefixed err.message — and keeps the draft (FR-015, FR-020 outcome 3)', async () => {
    // Traces to: Wave 2 findings-fix (task #149, gap 2) — pr-test-analyzer.
    // err.message carries the legacy "${status}: ${userMessage}" format (see
    // ApiError in src/lib/api-error.ts); the outcome line must show the bare
    // userMessage, which only ApiError-first message handling produces.
    vi.mocked(api.updatePerformanceSettings).mockRejectedValue(
      new api.ApiError(422, 'Unprocessable value for max_parallel_agents'),
    )
    const { requestPassword } = promptRecorder()
    await renderSectionWithShell(requestPassword)
    await waitFor(() => screen.getByLabelText(/max parallel agents/i))

    fireEvent.change(screen.getByLabelText(/max parallel agents/i), { target: { value: '8' } })
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))

    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
    })
    // The outcome line names the refusal (FR-015) — the old error toast is
    // replaced.
    expect(await screen.findByText('Unprocessable value for max_parallel_agents')).toBeInTheDocument()
    expect(await screen.findByText(/1 change could not be saved/)).toBeInTheDocument()
    // Pin the negative: the garbled status-prefixed string must NOT appear.
    expect(screen.queryByText('422: Unprocessable value for max_parallel_agents')).not.toBeInTheDocument()
    // FR-015: the draft is kept at its typed value.
    expect((screen.getByLabelText(/max parallel agents/i) as HTMLInputElement).value).toBe('8')
  })

  it('Discard returns the drafted toggle to the saved value, and the change can be re-attempted (FR-012; the old cancel-revert property)', async () => {
    renderSection()
    await waitFor(() => screen.getByLabelText(/tool loading/i))

    const toggle = screen.getByLabelText(/tool loading/i)
    expect(toggle).toBeChecked() // starts ON (default from SETTINGS)

    // Toggle OFF — a draft, not an optimistic write (FR-010).
    fireEvent.click(toggle)
    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()

    // Discard returns the draft to the saved value (FR-012) — the switch
    // must not keep showing the unsaved OFF state.
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    await waitFor(() => {
      expect(screen.getByLabelText(/tool loading/i)).toBeChecked()
    })
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // Recovery: the same change is attemptable again.
    fireEvent.click(screen.getByLabelText(/tool loading/i))
    await waitFor(() => expect(screen.getByLabelText(/tool loading/i)).not.toBeChecked())
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
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
 * names the underlying cause and can be retried. The wording follows the
 * spec's Limits group name ("Agents and goals" — Limits holds "Context and
 * tool results, Model overrides, and Agents and goals"), which replaces the
 * old "performance settings" wording.
 */
describe('PerformanceSection — failed load is visible, not blank', () => {
  // Holds the fetch open so the component stays in its loading state.
  function pendingFetch() {
    return new Promise<never>(() => {})
  }

  it('the loading state is not textless — it says it is loading', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockImplementation(pendingFetch)
    const { container } = renderSection()

    // The whole point: innerText must never be the empty string. Assert on
    // the container's own text so a purely-decorative skeleton fails here.
    await waitFor(() => {
      expect(screen.getByTestId('performance-loading')).toBeInTheDocument()
    })
    expect(container.textContent?.trim()).not.toBe('')
    expect(screen.getByText(/loading agents and goals/i)).toBeInTheDocument()
  })

  it('a failed load states the underlying cause, not a bare sentence', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockRejectedValue(
      new api.ApiError(503, 'The server is unavailable. Please try again in a moment.'),
    )
    renderSection()

    const alert = await screen.findByTestId('performance-load-error')
    // The cause itself must be on screen — this is what the old copy dropped.
    expect(alert).toHaveTextContent('The server is unavailable. Please try again in a moment.')
    expect(alert).toHaveTextContent(/could not be loaded/i)
    expect(alert).toHaveAttribute('role', 'alert')
  })

  it('a different failure surfaces ITS own cause (the message is not hardcoded)', async () => {
    vi.mocked(api.fetchPerformanceSettings).mockRejectedValue(
      new api.ApiError(500, 'probe-induced failure'),
    )
    renderSection()

    const alert = await screen.findByTestId('performance-load-error')
    expect(alert).toHaveTextContent('probe-induced failure')
    expect(alert).not.toHaveTextContent('The server is unavailable')
  })

  it('Retry refetches and recovers the panel', async () => {
    vi.mocked(api.fetchPerformanceSettings)
      .mockRejectedValueOnce(new api.ApiError(503, 'The server is unavailable. Please try again in a moment.'))
      .mockResolvedValue(SETTINGS as never)
    renderSection()

    await screen.findByTestId('performance-load-error')
    expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByTestId('performance-retry-btn'))

    await waitFor(() => {
      expect(screen.getByLabelText(/max parallel agents/i)).toHaveValue('4')
    })
    expect(api.fetchPerformanceSettings).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('performance-load-error')).not.toBeInTheDocument()
  })
})

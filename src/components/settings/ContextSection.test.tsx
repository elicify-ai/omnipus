// ADR-066 D9 / FR-036 / FR-037 / FR-010 — TDD row 46 `ContextSection.test.tsx`.
// Scenarios: B-44 (read defaults / partial write round-trip), B-14 (cap ceiling
// rows surfaced as field errors), X-08 (pre-fill a new override row from
// `?provider=&model=`). Settings redesign #1519: the section's own save
// button ("context-save", its own PUT and its own "Saved"/"Save failed"
// region) is replaced by the one Save bar of FR-010 — a change is a draft
// that stores nothing until Save changes; validation keeps the typed value
// and blocks the Save in the bar (FR-013); a refused write keeps its draft
// under the server's message (FR-015/FR-020 outcome 3); the saved signal is
// FR-018's "Saved at HH:MM". Every replaced assertion cites the FR below.
// The endpoint is mocked at the api-client seam from the generated zod
// schema: these assertions cover the COMPONENT's behaviour only and are not
// evidence that the endpoint exists. The backend contract (registration on
// the real mux, the partial-update semantics, the 400 rules) is covered by
// pkg/gateway/rest_context_settings_test.go — in particular
// TestContextSettings_RouteIsRegisteredOnRealMux, which drives the production
// registerAdditionalEndpoints mux and fails with 404 if the route disappears.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ContextSection } from './ContextSection'
import { getContextSettings, putContextSettings, ApiError } from '@/lib/api'
import type { ContextSettings } from '@/lib/api'
import { ContextSettings as ContextSettingsSchema } from '@/lib/api/generated/schemas'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    getContextSettings: vi.fn(),
    putContextSettings: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

// Spec defaults (ADR-066 §5 / FR-036, amended by MAJ-CW-007): 62,500 /
// 64,000 / 10,000 / share fraction 0.5 (50%) / 8,000,000;
// default_context_window unset; model_overrides empty.
const DEFAULTS: ContextSettings = ContextSettingsSchema.parse({
  mcp_result_cap: 62500,
  builtin_success_cap: 64000,
  builtin_failure_cap: 10000,
  tool_result_share_fraction: 0.5,
  ingest_bound_bytes: 8000000,
  model_overrides: [],
})

function renderSection(props: { prefill?: { provider: string; model: string } } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ContextSection prefillOverride={props.prefill} />
    </QueryClientProvider>,
  )
}

function inputByTestId(id: string): HTMLInputElement {
  return screen.getByTestId(id) as HTMLInputElement
}

/** The row's error: the alert the field's aria-describedby names (FR-013). */
function fieldAlertText(input: HTMLInputElement): string {
  const ids = (input.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
  const alert = ids.map((id) => document.getElementById(id)).find((el) => el?.getAttribute('role') === 'alert')
  expect(alert, 'the field must show its error as the alert its aria-describedby names').not.toBeNull()
  return alert?.textContent ?? ''
}

async function saveChanges() {
  fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))
}

describe('ContextSection (ADR-066 D9, Settings → Limits)', () => {
  beforeEach(() => {
    vi.mocked(getContextSettings).mockReset()
    vi.mocked(putContextSettings).mockReset()
    vi.mocked(getContextSettings).mockResolvedValue(DEFAULTS)
    vi.mocked(putContextSettings).mockImplementation(async (body) =>
      ContextSettingsSchema.parse({ ...DEFAULTS, ...body }),
    )
  })

  it('B-44: renders the spec defaults from GET, window unset, no override rows', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    expect(inputByTestId('context-builtin-success-cap').value).toBe('64000')
    expect(inputByTestId('context-builtin-failure-cap').value).toBe('10000')
    expect((screen.getByLabelText('Tool-result share limit') as HTMLInputElement).value).toBe('50')
    expect(inputByTestId('context-ingest-bound-bytes').value).toBe('8000000')
    expect(inputByTestId('context-default-window').value).toBe('')
    expect(screen.getByTestId('context-default-window-source').textContent).toMatch(/not set/i)
    expect(screen.queryAllByTestId('context-override-row')).toHaveLength(0)
    expect(screen.getByTestId('context-overrides-empty')).toBeTruthy()
  })

  // Independent oracle: ADR-066 MAJ-CW-007 / overflow spec Unit 4 and the
  // generated ContextSettings{,Update} contracts. Percent = fraction * 100;
  // these decimal cases must not pass through an integer-only parser.
  it('MAJ-CW-007: displays a loaded share fraction of 0.125 as 12.5 percent', async () => {
    vi.mocked(getContextSettings).mockResolvedValue({ ...DEFAULTS, tool_result_share_fraction: 0.125 })
    renderSection()
    await waitFor(() =>
      expect((screen.getByLabelText('Tool-result share limit') as HTMLInputElement).value).toBe('12.5'),
    )
  })

  // Independent oracle: ADR-066 MAJ-CW-007 and the generated contracts. The
  // write itself now follows FR-010: the typed percent is a draft (the bar
  // counts it, nothing is stored), and Save changes sends one partial PUT
  // with exactly the changed field (FR-015). FR-018's "Saved at" signal
  // replaces the old section-owned "Saved" region.
  it.each([
    { percentage: '0.1', fraction: 0.001 },
    { percentage: '1', fraction: 0.01 },
    { percentage: '12.5', fraction: 0.125 },
    { percentage: '99', fraction: 0.99 },
    { percentage: '100', fraction: 1 },
  ])('MAJ-CW-007: a drafted $percentage percent waits for Save changes, then is stored as fraction $fraction (FR-010)', async ({ percentage, fraction }) => {
    renderSection()
    const share = await screen.findByLabelText('Tool-result share limit') as HTMLInputElement
    await waitFor(() => expect(share.value).toBe('50'))

    // FR-010: typing drafts the value; nothing is stored yet.
    fireEvent.change(share, { target: { value: percentage } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(putContextSettings).not.toHaveBeenCalled()

    await saveChanges()
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({ tool_result_share_fraction: fraction })
    // FR-018: the saved signal replaces the old "Saved" region.
    await screen.findByText(/Saved at/)
    expect((screen.getByLabelText('Tool-result share limit') as HTMLInputElement).value).toBe(percentage)
  })

  // UI interval is 0 < percentage <= 100, derived from z.number().gt(0).lte(1).
  // Exact error prose is not prescribed. Range errors must name both bounds;
  // malformed/empty input must show a nonempty error on this same field.
  // FR-013 replaces the old "local save button refuses" mechanics: the bar
  // says "Fix 1 value before saving", Save changes is aria-disabled but stays
  // focusable, pressing it moves focus to the first invalid field and sends
  // nothing.
  it.each([
    { kind: 'negative percentage', value: '-1', error: /0.*100/ },
    { kind: 'zero percentage', value: '0', error: /0.*100/ },
    { kind: 'percentage above 100', value: '101', error: /0.*100/ },
    { kind: 'numeric prefix with trailing text', value: '12.5oops', error: /\S/ },
    { kind: 'empty percentage', value: '', error: /\S/ },
    { kind: 'NaN', value: 'NaN', error: /\S/ },
    { kind: 'Infinity', value: 'Infinity', error: /\S/ },
  ])('MAJ-CW-007: rejects $kind — field error, the bar blocks Save and focus moves to the field (FR-013)', async ({ value, error }) => {
    renderSection()
    const share = await screen.findByLabelText('Tool-result share limit') as HTMLInputElement
    await waitFor(() => expect(share.value).toBe('50'))

    fireEvent.change(share, { target: { value } })

    // The field carries its own visible error (specific to this field).
    await waitFor(() => expect(share.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(share)).toMatch(error)
    // FR-013: the bar names the blocked state and disables Save changes
    // without removing it from the tab order.
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    const saveBtn = screen.getByRole('button', { name: /save changes/i })
    expect(saveBtn).toHaveAttribute('aria-disabled', 'true')

    fireEvent.click(saveBtn)
    expect(share).toHaveFocus()
    expect(putContextSettings).not.toHaveBeenCalled()
  })

  it('B-44: two drafted fields wait for Save changes, which sends ONLY the changed fields (FR-010, FR-015)', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))

    fireEvent.change(inputByTestId('context-builtin-success-cap'), { target: { value: '70000' } })
    fireEvent.change(inputByTestId('context-default-window'), { target: { value: '128000' } })
    expect(await screen.findByText(/2 unsaved changes/)).toBeInTheDocument()
    expect(putContextSettings).not.toHaveBeenCalled()

    await saveChanges()
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({
      builtin_success_cap: 70000,
      default_context_window: 128000,
    })
    await screen.findByText(/Saved at/)
    expect(inputByTestId('context-builtin-success-cap').value).toBe('70000')
    expect(screen.getByTestId('context-default-window-source').textContent).toMatch(/operator/i)
  })

  it('B-44: clearing the drafted global default window waits for Save changes, which sends null (FR-010)', async () => {
    vi.mocked(getContextSettings).mockResolvedValue({ ...DEFAULTS, default_context_window: 128000 })
    renderSection()
    await waitFor(() => expect(inputByTestId('context-default-window').value).toBe('128000'))
    fireEvent.change(inputByTestId('context-default-window'), { target: { value: '' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(putContextSettings).not.toHaveBeenCalled()
    await saveChanges()
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({ default_context_window: null })
  })

  it('B-14: a drafted cap above 150,000 is a field error naming the limit; the bar blocks Save and nothing is sent (FR-013)', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.change(inputByTestId('context-mcp-result-cap'), { target: { value: '150001' } })
    const cap = inputByTestId('context-mcp-result-cap')
    await waitFor(() => expect(cap.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(cap)).toMatch(/150,000/)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(cap).toHaveFocus()
    expect(putContextSettings).not.toHaveBeenCalled()
  })

  it('B-14: a drafted 150,000 waits for Save changes, which stores it (FR-010)', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.change(inputByTestId('context-mcp-result-cap'), { target: { value: '150000' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(putContextSettings).not.toHaveBeenCalled()
    await saveChanges()
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({ mcp_result_cap: 150000 })
  })

  it('B-14: a drafted ingest bound at 8,388,608 is a field error naming the limit; the bar blocks Save (FR-013)', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-ingest-bound-bytes').value).toBe('8000000'))
    fireEvent.change(inputByTestId('context-ingest-bound-bytes'), { target: { value: '8388608' } })
    const bound = inputByTestId('context-ingest-bound-bytes')
    await waitFor(() => expect(bound.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(bound)).toMatch(/8,388,608/)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(bound).toHaveFocus()
    expect(putContextSettings).not.toHaveBeenCalled()
  })

  it('B-14: a refused Save keeps the draft under the server message; the bar says the change could not be saved (FR-015, FR-020 outcome 3)', async () => {
    vi.mocked(putContextSettings).mockRejectedValue(
      new ApiError(400, 'builtin_failure_cap must be between 1 and 150000', {
        body: JSON.stringify({
          error: 'builtin_failure_cap must be between 1 and 150000',
          details: { field: 'builtin_failure_cap', limit: 150000 },
        }),
      }),
    )
    renderSection()
    await waitFor(() => expect(inputByTestId('context-builtin-failure-cap').value).toBe('10000'))
    fireEvent.change(inputByTestId('context-builtin-failure-cap'), { target: { value: '20000' } })
    await saveChanges()
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    const cap = inputByTestId('context-builtin-failure-cap')
    await waitFor(() => expect(cap.getAttribute('aria-invalid')).toBe('true'))
    expect(fieldAlertText(cap)).toMatch(/150000/)
    // FR-015: the bar names the failure — the old section-owned
    // "Save failed" region is replaced.
    expect(await screen.findByText(/1 change could not be saved/)).toBeInTheDocument()
  })

  it('model overrides: the drafted list waits for Save changes, which sends the whole replacement list (FR-010; a list is one value)', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.click(screen.getByTestId('context-override-add'))
    const row = screen.getAllByTestId('context-override-row')[0]
    fireEvent.change(within(row).getByTestId('context-override-provider'), { target: { value: 'openrouter' } })
    fireEvent.change(within(row).getByTestId('context-override-model'), { target: { value: 'z-ai/glm-5.2' } })
    fireEvent.change(within(row).getByTestId('context-override-window'), { target: { value: '200000' } })
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(putContextSettings).not.toHaveBeenCalled()
    await saveChanges()
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({
      model_overrides: [{ provider: 'openrouter', model: 'z-ai/glm-5.2', context_window: 200000 }],
    })
  })

  it('model overrides: a drafted context_window below 1 is a field error; the bar blocks Save and nothing is sent (FR-013)', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.click(screen.getByTestId('context-override-add'))
    const row = screen.getAllByTestId('context-override-row')[0]
    fireEvent.change(within(row).getByTestId('context-override-provider'), { target: { value: 'p' } })
    fireEvent.change(within(row).getByTestId('context-override-model'), { target: { value: 'm' } })
    const win = within(row).getByTestId('context-override-window') as HTMLInputElement
    fireEvent.change(win, { target: { value: '0' } })
    await waitFor(() => expect(win.getAttribute('aria-invalid')).toBe('true'))
    // The override row renders its validation errors as alerts inside the row.
    expect(within(row).getByRole('alert').textContent).toMatch(/at least 1/i)
    expect(await screen.findByText(/Fix 1 value before saving/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(win).toHaveFocus()
    expect(putContextSettings).not.toHaveBeenCalled()
  })

  it('X-08: ?provider=&model= pre-fills a new override row', async () => {
    renderSection({ prefill: { provider: 'ollama', model: 'qwen3:8b' } })
    await waitFor(() => expect(screen.getAllByTestId('context-override-row')).toHaveLength(1))
    const row = screen.getAllByTestId('context-override-row')[0]
    expect((within(row).getByTestId('context-override-provider') as HTMLInputElement).value).toBe('ollama')
    expect((within(row).getByTestId('context-override-model') as HTMLInputElement).value).toBe('qwen3:8b')
    expect((within(row).getByTestId('context-override-window') as HTMLInputElement).value).toBe('')
    expect(screen.queryByTestId('context-overrides-empty')).toBeNull()
  })

  it('X-08: a pre-fill matching an existing override does not duplicate the row', async () => {
    vi.mocked(getContextSettings).mockResolvedValue({
      ...DEFAULTS,
      model_overrides: [{ provider: 'ollama', model: 'qwen3:8b', context_window: 32768 }],
    })
    renderSection({ prefill: { provider: 'ollama', model: 'qwen3:8b' } })
    await waitFor(() => expect(screen.getAllByTestId('context-override-row')).toHaveLength(1))
    const row = screen.getAllByTestId('context-override-row')[0]
    expect((within(row).getByTestId('context-override-window') as HTMLInputElement).value).toBe('32768')
  })
})

// ADR-066 D9 / FR-036 / FR-037 — TDD row 46 `ContextSection.test.tsx`.
// Scenarios: B-44 (read defaults / partial write round-trip), B-14 (cap ceiling
// rows surfaced as field errors), X-08 (pre-fill a new override row from
// `?provider=&model=`). The endpoint is mocked at the api-client seam from the
// generated zod schema until T066-17 lands the backend.
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

// Spec defaults (ADR-066 §5 / FR-036): 62,500 / 64,000 / 10,000 / 400,000 /
// 8,000,000; default_context_window unset; model_overrides empty.
const DEFAULTS: ContextSettings = ContextSettingsSchema.parse({
  mcp_result_cap: 62500,
  builtin_success_cap: 64000,
  builtin_failure_cap: 10000,
  absolute_trigger_chars: 400000,
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

describe('ContextSection (ADR-066 D9, Settings → Models)', () => {
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
    expect(inputByTestId('context-absolute-trigger-chars').value).toBe('400000')
    expect(inputByTestId('context-ingest-bound-bytes').value).toBe('8000000')
    expect(inputByTestId('context-default-window').value).toBe('')
    expect(screen.getByTestId('context-default-window-source').textContent).toMatch(/not set/i)
    expect(screen.queryAllByTestId('context-override-row')).toHaveLength(0)
    expect(screen.getByTestId('context-overrides-empty')).toBeTruthy()
  })

  it('B-44: a partial write sends ONLY the changed fields and round-trips the response', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))

    fireEvent.change(inputByTestId('context-builtin-success-cap'), { target: { value: '70000' } })
    fireEvent.change(inputByTestId('context-default-window'), { target: { value: '128000' } })
    fireEvent.click(screen.getByTestId('context-save'))

    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({
      builtin_success_cap: 70000,
      default_context_window: 128000,
    })
    await waitFor(() => expect(screen.getByText('Saved')).toBeTruthy())
    expect(inputByTestId('context-builtin-success-cap').value).toBe('70000')
    expect(screen.getByTestId('context-default-window-source').textContent).toMatch(/operator/i)
  })

  it('B-44: clearing the global default window sends default_context_window: null', async () => {
    vi.mocked(getContextSettings).mockResolvedValue({ ...DEFAULTS, default_context_window: 128000 })
    renderSection()
    await waitFor(() => expect(inputByTestId('context-default-window').value).toBe('128000'))
    fireEvent.change(inputByTestId('context-default-window'), { target: { value: '' } })
    fireEvent.click(screen.getByTestId('context-save'))
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({ default_context_window: null })
  })

  it('B-14: a cap above 150,000 is a field error naming the limit; nothing is sent', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.change(inputByTestId('context-mcp-result-cap'), { target: { value: '150001' } })
    fireEvent.click(screen.getByTestId('context-save'))
    const err = await screen.findByTestId('context-error-mcp_result_cap')
    expect(err.textContent).toMatch(/150,000/)
    expect(inputByTestId('context-mcp-result-cap').getAttribute('aria-invalid')).toBe('true')
    expect(putContextSettings).not.toHaveBeenCalled()
  })

  it('B-14: exactly 150,000 is accepted and sent', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.change(inputByTestId('context-mcp-result-cap'), { target: { value: '150000' } })
    fireEvent.click(screen.getByTestId('context-save'))
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({ mcp_result_cap: 150000 })
  })

  it('B-14: ingest bound at 8,388,608 is a field error naming the limit', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-ingest-bound-bytes').value).toBe('8000000'))
    fireEvent.change(inputByTestId('context-ingest-bound-bytes'), { target: { value: '8388608' } })
    fireEvent.click(screen.getByTestId('context-save'))
    const err = await screen.findByTestId('context-error-ingest_bound_bytes')
    expect(err.textContent).toMatch(/8,388,608/)
    expect(putContextSettings).not.toHaveBeenCalled()
  })

  it('B-14: a server 400 naming the field and limit renders on that field', async () => {
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
    fireEvent.click(screen.getByTestId('context-save'))
    const err = await screen.findByTestId('context-error-builtin_failure_cap')
    expect(err.textContent).toMatch(/150000/)
    expect(screen.getByText('Save failed')).toBeTruthy()
  })

  it('model overrides: add a row and save sends the whole replacement list', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.click(screen.getByTestId('context-override-add'))
    const row = screen.getAllByTestId('context-override-row')[0]
    fireEvent.change(within(row).getByTestId('context-override-provider'), { target: { value: 'openrouter' } })
    fireEvent.change(within(row).getByTestId('context-override-model'), { target: { value: 'z-ai/glm-5.2' } })
    fireEvent.change(within(row).getByTestId('context-override-window'), { target: { value: '200000' } })
    fireEvent.click(screen.getByTestId('context-save'))
    await waitFor(() => expect(putContextSettings).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putContextSettings).mock.calls[0][0]).toEqual({
      model_overrides: [{ provider: 'openrouter', model: 'z-ai/glm-5.2', context_window: 200000 }],
    })
  })

  it('model overrides: context_window below 1 is a field error; nothing is sent', async () => {
    renderSection()
    await waitFor(() => expect(inputByTestId('context-mcp-result-cap').value).toBe('62500'))
    fireEvent.click(screen.getByTestId('context-override-add'))
    const row = screen.getAllByTestId('context-override-row')[0]
    fireEvent.change(within(row).getByTestId('context-override-provider'), { target: { value: 'p' } })
    fireEvent.change(within(row).getByTestId('context-override-model'), { target: { value: 'm' } })
    fireEvent.change(within(row).getByTestId('context-override-window'), { target: { value: '0' } })
    fireEvent.click(screen.getByTestId('context-save'))
    const err = await screen.findByTestId('context-error-model_overrides.0.context_window')
    expect(err.textContent).toMatch(/at least 1/i)
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

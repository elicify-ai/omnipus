// model-effort-picker.test.tsx — RED component tests for ModelEffortPicker and
// EffortControl (T-F1 presentation, T-F2 keyboard, T-F3 save-at-once/cancel).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
//
// RED reason: both components are stubs returning null — every rendered-output
// assertion fails until GREEN publishes the real components.
//
// Seam: the picker resolves the SERVED catalog row for its value's
// provider/model through the SPA's catalog query (fetchProvidersCatalog, the
// seam ADR-068 feeds Settings and onboarding from). The mock catalog below
// carries per-model `reasoning {levels, switch}` objects — the WP-CAT catalog
// amendment the spec's data gate requires. RED/GREEN contract: tests mock
// '@/lib/api'::fetchProvidersCatalog and render inside a QueryClientProvider.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactElement } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { EffortControl, ModelEffortPicker, type EffortValue } from './model-effort-picker'
import { fetchProvidersCatalog } from '@/lib/api'
import type { ProvidersCatalog } from '@/lib/api'

// ResizeObserver is required by popover primitives; jsdom does not implement
// it (same stub as MemorySection.test.tsx).
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProvidersCatalog: vi.fn(),
  }
})

// ── Catalog fixture (served rows; WP-CAT shape: reasoning {levels, switch}) ──

interface MockReasoning {
  levels?: string[] | null
  switch?: boolean
}

function makeCatalog(models: Record<string, MockReasoning | undefined>): ProvidersCatalog {
  return {
    schema_version: '2.0.0',
    version: 'test',
    updated_at: '2026-10-31T00:00:00Z',
    source: 'test',
    default_resize_limits: { long_edge_px: 2048, max_bytes: 10485760 },
    served_from: 'embedded',
    stale: false,
    providers: [
      {
        id: 'openrouter',
        name: 'openrouter',
        company: 'OpenRouter',
        api: 'https://openrouter.ai/api/v1',
        tier: 'popular',
        auth_methods: ['api_key'],
        aliases: [],
        locality: 'cloud',
        models: Object.entries(models).map(([id, reasoning]) => ({
          id,
          name: id,
          context_window: 128000,
          max_output_tokens: 8192,
          input_modalities: ['text'],
          tool_call: true,
          status: 'active',
          reasoning,
        })),
      },
    ],
  } as unknown as ProvidersCatalog
}

const CATALOG = makeCatalog({
  // S-07 row 1 / S-10 blocked-step target: levels low, medium, high, no Off, no switch.
  'openai/gpt-6.1-sol': { levels: ['low', 'medium', 'high'], switch: false },
  // S-07 row 2 / S-08: DeepSeek — none, low, high, max; NO Medium.
  'deepseek/deepseek-v4.1-flash': { levels: ['none', 'low', 'high', 'max'], switch: false },
  // D3 row 5 / S-54 fallback 2: switch only.
  'minimax/minimax-m3': { levels: [], switch: true },
  // S-07 / D3 rows 4, 8, 9: object present, nothing sendable (budget only).
  'budget-only-model': { levels: [], switch: false },
  // S-14 / S-07: no reasoning object at all.
  'no-reasoning-model': undefined,
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderWithCatalog(ui: ReactElement) {
  return render(<QueryClientProvider client={makeClient()}>{ui}</QueryClientProvider>)
}

function effortValue(model: string, effortValue_: string): EffortValue {
  return { provider: 'openrouter', model, effort: effortValue_ }
}

const radioNames = () => screen.getAllByRole('radio').map((el) => (el.textContent ?? '').trim())

// ── T-F1: presentation (S-07, S-08, S-12, S-60) ───────────────────────────────

describe('ModelEffortPicker — T-F1 presentation', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  // Traces to: effort-levels-spec-r10.md S-07 (row "levels low, medium, high"),
  // S-12 (Not set names the inherited value).
  it('S-07: levels low, medium, high offer exactly Not set (named inherit), Provider default, Low, Medium, High', async () => {
    renderWithCatalog(
      <ModelEffortPicker value={effortValue('openai/gpt-6.1-sol', '')} inherit="high" scope="chat" />,
    )
    await screen.findByRole('radio')
    expect(radioNames()).toEqual([
      "Not set: Agent's setting: High",
      'Provider default',
      'Low',
      'Medium',
      'High',
    ])
    expect(screen.getByRole('radio', { name: "Not set: Agent's setting: High" })).toHaveAttribute(
      'aria-checked',
      'true',
    )
  })

  // Traces to: effort-levels-spec-r10.md S-08 (DeepSeek shows no Medium), S-07
  // (row "levels none, low, high, max").
  it('S-08: DeepSeek offers Not set, Provider default, Off, Low, High, Max in that order and never Medium', async () => {
    renderWithCatalog(
      <ModelEffortPicker value={effortValue('deepseek/deepseek-v4.1-flash', '')} scope="chat" />,
    )
    await screen.findByRole('radio')
    const names = radioNames()
    expect(names[0]).toMatch(/Not set/)
    expect(names.slice(1)).toEqual(['Provider default', 'Off', 'Low', 'High', 'Max'])
    expect(screen.queryByText('Medium')).toBeNull()
  })

  // Traces to: effort-levels-spec-r10.md S-07 (row "switch only").
  it('S-07: a switch-only model offers Not set, Provider default, Off, On', async () => {
    renderWithCatalog(
      <ModelEffortPicker value={effortValue('minimax/minimax-m3', '')} scope="chat" />,
    )
    await screen.findByRole('radio')
    const names = radioNames()
    expect(names[0]).toMatch(/Not set/)
    expect(names.slice(1)).toEqual(['Provider default', 'Off', 'On'])
  })

  // Traces to: effort-levels-spec-r10.md S-07 (row "levels low, high plus
  // switch (no none)"): Off reachable through the served switch.
  it('S-07: levels plus switch include Off alongside the levels', async () => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(
      makeCatalog({ 'dual-model': { levels: ['low', 'high'], switch: true } }),
    )
    renderWithCatalog(<ModelEffortPicker value={effortValue('dual-model', '')} scope="chat" />)
    await screen.findByRole('radio')
    const names = radioNames()
    expect(names[0]).toMatch(/Not set/)
    expect(names.slice(1)).toEqual(['Provider default', 'Off', 'Low', 'High'])
  })

  // Traces to: effort-levels-spec-r10.md S-60 (recap slots start at Off, items
  // follow the S-07 catalog rules with NO Not set row; FD-19).
  it('S-60: a recap slot starts at Off and has no Not set row', async () => {
    renderWithCatalog(
      <ModelEffortPicker
        value={effortValue('deepseek/deepseek-v4.1-flash', 'off')}
        scope="recap-model"
        defaultEffort="off"
      />,
    )
    await screen.findByRole('radio')
    expect(screen.queryByText(/Not set/)).toBeNull()
    expect(screen.getByRole('radio', { name: 'Off' })).toHaveAttribute('aria-checked', 'true')
    const names = radioNames()
    expect(names[0]).toMatch(/Provider default/)
    expect(names.slice(1)).toEqual(['Off', 'Low', 'High', 'Max'])
  })

  // Traces to: effort-levels-spec-r10.md S-07 (rows "catalog still loading" and
  // "catalog read failed": stored value as text, items disabled / FormError with Retry).
  it('S-07: while the catalog loads the stored value shows as text and items are disabled', async () => {
    vi.mocked(fetchProvidersCatalog).mockReturnValue(new Promise(() => {}))
    renderWithCatalog(
      <ModelEffortPicker value={effortValue('deepseek/deepseek-v4.1-flash', 'high')} scope="chat" />,
    )
    expect(screen.getByText('High')).toBeInTheDocument()
    for (const radio of screen.queryAllByRole('radio')) {
      expect(radio).toBeDisabled()
    }
  })

  it('S-07: when the catalog read fails the stored value shows, items are disabled and a FormError offers Retry', async () => {
    vi.mocked(fetchProvidersCatalog).mockRejectedValue(new Error('catalog down'))
    renderWithCatalog(
      <ModelEffortPicker value={effortValue('deepseek/deepseek-v4.1-flash', 'high')} scope="chat" />,
    )
    expect(await screen.findByText('High')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    for (const radio of screen.queryAllByRole('radio')) {
      expect(radio).toBeDisabled()
    }
  })
})

// ── T-F1: EffortControl chip (S-07, S-12, S-14, S-54) ─────────────────────────

describe('EffortControl — chip presentation', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  // Traces to: effort-levels-spec-r10.md S-07 (chip per served row) and S-14
  // (the chip disappears with a model that does not reason; custom/local rows
  // with no catalog row show none either). The positive half is load-bearing in
  // RED: the stub renders nothing, so the served-model chip assertion fails.
  it('S-07/S-14: a served model shows the Effort chip; no reasoning object or no catalog row shows none', async () => {
    const served = renderWithCatalog(
      <EffortControl value={effortValue('openai/gpt-6.1-sol', 'high')} scope="chat" />,
    )
    expect(await screen.findByRole('button', { name: /high/i })).toBeInTheDocument()
    served.unmount()

    renderWithCatalog(
      <EffortControl value={effortValue('no-reasoning-model', 'high')} scope="chat" />,
    )
    await expect(
      screen.findByRole('button', { name: /effort/i }),
    ).rejects.toThrowError(/Unable to find|multiple/i)
    // ...and a model absent from the catalog (custom/local) shows no chip either.
    const { unmount } = renderWithCatalog(
      <EffortControl value={effortValue('custom-local-model', 'high')} scope="chat" />,
    )
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(screen.queryByRole('button', { name: /effort/i })).toBeNull()
    unmount()
  })

  // Traces to: effort-levels-spec-r10.md S-07 (muted "Effort" chip with info
  // icon and the exact tooltip; no items).
  it('S-07: an object with nothing sendable renders the muted Effort chip with the exact tooltip and no items', async () => {
    renderWithCatalog(<EffortControl value={effortValue('budget-only-model', 'high')} scope="chat" />)
    const chip = await screen.findByRole('button', { name: 'Effort' })
    const tooltip =
      chip.getAttribute('title') ??
      screen.getByText("Omnipus can't change effort for this model.").textContent
    expect(tooltip).toContain("Omnipus can't change effort for this model.")
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
  })

  // Traces to: effort-levels-spec-r10.md S-12 (Not set shows what it inherits,
  // dashed outline).
  it('S-12: a Not set chip names the inherited agent setting', async () => {
    renderWithCatalog(
      <EffortControl value={effortValue('openai/gpt-6.1-sol', '')} inherit="high" scope="chat" />,
    )
    expect(await screen.findByRole('button', { name: /Not set: Agent's setting: High/ })).toBeInTheDocument()
  })

  // Traces to: effort-levels-spec-r10.md S-54 (a fallback chip has its own
  // contextual name: "Effort for fallback 2, MiniMax M3: On").
  it('S-54: the fallback chip reads the exact contextual name with its value', async () => {
    renderWithCatalog(
      <EffortControl
        value={effortValue('minimax/minimax-m3', 'on')}
        scope="agent-fallback"
        contextName="Effort for fallback 2, MiniMax M3"
      />,
    )
    expect(
      await screen.findByRole('button', { name: 'Effort for fallback 2, MiniMax M3: On' }),
    ).toBeInTheDocument()
  })
})

// ── T-F2: blocked step and keyboard (S-10, S-11, S-73) ────────────────────────

describe('EffortControl — blocked step and keyboard', () => {
  const user = userEvent.setup()

  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  // Renders Off on a model without Off/switch (the S-10 Given) and opens the
  // popover. The chip is located before opening: its accessible name carries
  // the value (S-54 pattern "<context>: <value>").
  async function openBlockedStep() {
    const onChange = vi.fn()
    renderWithCatalog(
      <EffortControl
        value={effortValue('openai/gpt-6.1-sol', 'off')}
        scope="chat"
        onChange={onChange}
      />,
    )
    const chip = await screen.findByRole('button', { name: /off/i })
    await user.click(chip)
    return { onChange, chip }
  }

  // Traces to: effort-levels-spec-r10.md S-10 (popover stays open on the
  // "pick a level" step, Low marked nearest but NOT chosen and focused, the
  // exact announcement).
  it('S-10: Off into a model without Off waits on the pick-a-level step with Low focused, nearest but not chosen', async () => {
    await openBlockedStep()
    expect(
      screen.getByText(
        "openai/gpt-6.1-sol can't turn reasoning off. Pick a level to finish switching.",
      ),
    ).toBeInTheDocument()
    const low = screen.getByRole('radio', { name: 'Low' })
    expect(low).toHaveFocus()
    expect(low).toHaveAttribute('aria-checked', 'false')
  })

  // Traces to: effort-levels-spec-r10.md S-10 ("picking High completes the
  // switch with High"); the commit is the save — no confirm step (P-Q4, S-57
  // save-on-commit semantics).
  it('S-10: picking High completes the switch — onChange fires at once with high and the popover closes', async () => {
    const { onChange } = await openBlockedStep()
    await user.click(screen.getByRole('radio', { name: 'High' }))
    expect(onChange).toHaveBeenCalled()
    expect(onChange.mock.calls[0][0]).toBe('high')
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
    // No confirm step: nothing in the popover asks before saving (P-Q4 A).
    expect(screen.queryByRole('button', { name: /confirm|save/i })).toBeNull()
  })

  // Traces to: effort-levels-spec-r10.md S-10 ("Escape instead keeps the
  // previous model and Off and announces 'Switch cancelled'"); S-74 revert.
  it('S-10: Escape keeps Off, changes nothing and announces Switch cancelled', async () => {
    const { onChange, chip } = await openBlockedStep()
    await user.keyboard('{Escape}')
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
    expect(chip).toHaveTextContent(/off/i)
    expect(screen.getByText('Switch cancelled')).toBeInTheDocument()
  })

  // Traces to: effort-levels-spec-r10.md S-11 (keyboard only: Arrow Down moves
  // the selection, Enter completes with the selected level and focus moves to
  // the effort chip).
  it('S-11: Arrow Down moves the selection and Enter commits it, returning focus to the chip', async () => {
    const { onChange, chip } = await openBlockedStep()
    expect(screen.getByRole('radio', { name: 'Low' })).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(screen.getByRole('radio', { name: 'Medium' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(onChange).toHaveBeenCalled()
    expect(onChange.mock.calls[0][0]).toBe('medium')
    expect(chip).toHaveFocus()
  })

  // Traces to: effort-levels-spec-r10.md S-11 (the same step works with Space
  // as the commit key on the focused radio).
  it('S-11: Space commits the focused level', async () => {
    const { onChange } = await openBlockedStep()
    await user.keyboard('{ArrowDown}')
    await user.keyboard(' ')
    expect(onChange).toHaveBeenCalled()
    expect(onChange.mock.calls[0][0]).toBe('medium')
  })

  // Traces to: effort-levels-spec-r10.md S-73 (Off to a model with nothing
  // sendable offers "Let the provider decide" and Cancel; the first stores
  // Provider default and completes the switch).
  it('S-73: Off into nothing sendable offers Let the provider decide and Cancel; deciding stores provider_default', async () => {
    const onChange = vi.fn()
    renderWithCatalog(
      <EffortControl
        value={effortValue('budget-only-model', 'off')}
        scope="chat"
        onChange={onChange}
      />,
    )
    const chip = await screen.findByRole('button', { name: /off/i })
    await user.click(chip)
    await user.click(screen.getByRole('button', { name: 'Let the provider decide' }))
    expect(onChange).toHaveBeenCalled()
    expect(onChange.mock.calls[0][0]).toBe('provider_default')
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
  })

  // Traces to: effort-levels-spec-r10.md S-73 ("Cancel or Escape keeps the
  // previous model and Off").
  it('S-73: Cancel keeps the previous model and Off without a change', async () => {
    const onChange = vi.fn()
    renderWithCatalog(
      <EffortControl
        value={effortValue('budget-only-model', 'off')}
        scope="chat"
        onChange={onChange}
      />,
    )
    const chip = await screen.findByRole('button', { name: /off/i })
    await user.click(chip)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)
    expect(chip).toHaveTextContent(/off/i)
  })
})

// ── T-F3: remap note with Change link (S-74, S-57) ────────────────────────────

describe('EffortControl — remapped stored value shows the note with a Change link', () => {
  const user = userEvent.setup()

  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  // Traces to: effort-levels-spec-r10.md S-74 (a switch that remaps saves at
  // once with the note and a Change link, and the link opens the effort
  // popover); S-57 (the picker carries no internal confirm step — the commit is
  // the save request; the server's value renders again when the parent passes
  // it back after a failed save).
  it('S-74: a stored Medium on a no-Medium model shows the exact note with a Change link that reopens the popover', async () => {
    renderWithCatalog(
      <EffortControl
        value={effortValue('deepseek/deepseek-v4.1-flash', 'medium')}
        scope="chat"
      />,
    )
    expect(
      screen.getByText("Effort set to Low. deepseek/deepseek-v4.1-flash doesn't offer Medium."),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Change' }))
    expect(await screen.findByRole('radio', { name: 'Medium' })).toBeInTheDocument()
    // Save-on-commit (S-57/P-Q4): no extra confirm step inside the popover.
    expect(screen.queryByRole('button', { name: /confirm|save/i })).toBeNull()
  })
})

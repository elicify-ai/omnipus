// model-effort-picker.wireframe.test.tsx — the effort chip and popover as the
// founder-approved wireframes draw them, where the spec text does not say
// otherwise (chief ruling 2026-11-07: the wireframes are binding).
//
// Oracles:
//   - Wireframes designs-20261029/effort-picker-preview (index.html, shot-1 to
//     shot-7): bar glyph per level and on the chip (barsHtml: Off 0, Minimal 1,
//     Low 1, Medium 2, High 3, Extra high 4, Max 5, On 3; none for Not set and
//     Provider default); popover title "Effort · {model}" with the help line
//     "How much the model thinks before answering. More effort is slower and
//     costs more." (hidden for a stale level); top rows with a radio dot and a
//     muted sub-text ("Agent's setting: High", "provider decides"); blocked
//     step "You stay on {previous} until you do.", "Let the provider decide
//     instead" link and a Cancel button; nothing-sendable step "This model has
//     no levels to pick. You can let the provider decide, or stay where you
//     are."; stale sentence "Saved effort "Max" is no longer offered by
//     {model}. Turns use High (nearest) until you pick one."; catalog states
//     "Loading model details. Saved effort: High." and "Couldn't load model
//     details. Saved effort: High. Retry"; muted chip info icon before
//     "Effort"; settings chip with an "Effort" key and a caret.
//   - effort-levels-spec-r10.md, Accessibility table: the nothing-sendable
//     step is announced "{model} can't turn reasoning off here. Let the
//     provider decide, or cancel."; Presentation by capability: a stale value
//     is shown as written with an amber dot and a note with what is sent.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactElement } from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { EffortControl } from './model-effort-picker'
import { fetchProvidersCatalog } from '@/lib/api'
import type { ProvidersCatalog } from '@/lib/api'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchProvidersCatalog: vi.fn() }
})

const LEVELS_MODEL = 'wireframe-fixture/low-medium-high'
const MUTED_MODEL = 'wireframe-fixture/budget-only'
const PREVIOUS_MODEL = 'wireframe-fixture/previous'

const CATALOG = {
  schema_version: '2.0.0',
  version: 'v2026.10.31',
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
      protocol: 'openai-compatible',
      protocols: [{ protocol: 'openai-compatible', api: 'https://openrouter.ai/api/v1' }],
      tier: 'popular',
      auth_methods: ['api_key'],
      aliases: [],
      locality: 'cloud',
      models: [
        { id: LEVELS_MODEL, reasoning: { levels: ['low', 'medium', 'high'], switch: false } },
        { id: MUTED_MODEL, reasoning: { levels: [], switch: false } },
      ].map((m) => ({ ...m, name: m.id, context_window: 128000, max_output_tokens: 8192, input_modalities: ['text'], tool_call: true, status: 'active' })),
    },
  ],
} as unknown as ProvidersCatalog

const HELP = 'How much the model thinks before answering. More effort is slower and costs more.'

function renderWithCatalog(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function litBars(el: Element): string | null {
  return el.querySelector('[data-effort-bars]')?.getAttribute('data-effort-bars') ?? null
}

describe('effort popover as drawn in the wireframes', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  it('titles the popover "Effort · {model}", adds the help line and draws bars on levels and the chip only', async () => {
    const user = userEvent.setup()
    renderWithCatalog(
      <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'high' }} scope="chat" inherit="medium" data-testid="chip" />,
    )
    const chip = await screen.findByTestId('chip')
    expect(litBars(chip)).toBe('3')
    await user.click(chip)
    const dialog = await screen.findByRole('dialog')
    const title = within(dialog).getByText(`· ${LEVELS_MODEL}`)
    expect(title.parentElement?.textContent).toBe(`Effort · ${LEVELS_MODEL}`)
    expect(within(dialog).getByText(HELP)).toBeInTheDocument()
    const bars = Object.fromEntries(within(dialog).getAllByRole('radio').map((r) => [r.textContent, litBars(r)]))
    expect(bars).toEqual({ "Not set: Agent's setting: Medium": null, 'Provider default': null, Low: '1', Medium: '2', High: '3' })
  })

  it('gives the top rows a radio dot and a muted sub-text without changing their names', async () => {
    const user = userEvent.setup()
    renderWithCatalog(
      <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: '' }} scope="chat" inherit="high" data-testid="chip" />,
    )
    await user.click(await screen.findByTestId('chip'))
    const notSet = await screen.findByRole('radio', { name: "Not set: Agent's setting: High" })
    expect(notSet.querySelector('[data-effort-dot]')).not.toBeNull()
    expect(within(notSet).getByText("Agent's setting: High")).toHaveAttribute('data-effort-sub')
    const providerDefault = screen.getByRole('radio', { name: 'Provider default' })
    expect(providerDefault.textContent).toBe('Provider default')
    expect(providerDefault.querySelector('[data-effort-dot]')).not.toBeNull()
    // The sub-text is drawn from the attribute, so the row's text stays "Provider default".
    expect(providerDefault.querySelector('[data-effort-sub="provider decides"]')).not.toBeNull()
  })

  it('marks a level that is no longer offered with the wireframe sentence and no help line', async () => {
    const user = userEvent.setup()
    renderWithCatalog(
      <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'max' }} scope="default-model" data-testid="chip" />,
    )
    const chip = await screen.findByTestId('chip')
    // Founder C3 (2026-11-07): the chip shows the level actually used with the
    // amber dot; the accessible name keeps the saved value and what is sent.
    expect(chip.textContent).toBe('EffortHigh')
    expect(chip).toHaveAccessibleName('Effort: Max, sent as High here')
    expect(chip.querySelector('[data-effort-mark="attention"]')).not.toBeNull()
    expect(screen.getByText(`Saved effort "Max" is no longer offered by ${LEVELS_MODEL}. Turns use High (nearest) until you pick one.`)).toBeInTheDocument()
    await user.click(chip)
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(`Your saved effort "Max" is no longer offered by ${LEVELS_MODEL}. Turns use High, the nearest level, until you pick one.`),
    ).toBeInTheDocument()
    expect(within(dialog).queryByText(HELP)).toBeNull()
  })

  it('shows a settings chip with an "Effort" key and a caret, and the muted composer chip with the info icon first', async () => {
    renderWithCatalog(
      <>
        <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'low' }} scope="agent-fallback" contextName="Effort for fallback 1" data-testid="field" />
        <EffortControl value={{ provider: 'openrouter', model: MUTED_MODEL, effort: '' }} scope="chat" data-testid="muted" />
      </>,
    )
    const field = await screen.findByTestId('field')
    expect(field).toHaveAccessibleName('Effort for fallback 1: Low')
    expect(field.textContent).toBe('EffortLow')
    expect(field.lastElementChild?.tagName.toLowerCase()).toBe('svg')
    const muted = await screen.findByTestId('muted')
    expect(muted.firstElementChild?.tagName.toLowerCase()).toBe('svg')
    expect(muted.textContent).toBe('Effort')
  })
})

describe('chip text as the founder decided (C1 to C3, 2026-11-07)', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  it('C1: an inherited value shows only the level actually used, dashed; the name keeps the source', async () => {
    renderWithCatalog(
      <>
        <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: '' }} scope="chat" inherit="high" data-testid="same" />
        <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: '' }} scope="chat" inherit="max" data-testid="mapped" />
        <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: '' }} scope="chat" inherit="off" data-testid="blocked" />
      </>,
    )
    const same = await screen.findByTestId('same')
    expect(same.textContent).toBe('High')
    expect(same).toHaveAttribute('data-effort-inherited', 'true')
    expect(same).toHaveAccessibleName("Effort: Not set: Agent's setting: High")
    expect(litBars(same)).toBe('3')
    const mapped = screen.getByTestId('mapped')
    expect(mapped.textContent).toBe('High')
    expect(mapped).toHaveAccessibleName("Effort: Not set: Agent's setting: Max, sent as High here")
    // Spec UI table: an inherited value sent differently keeps the amber dot.
    expect(mapped.querySelector('[data-effort-mark="attention"]')).not.toBeNull()
    expect(same.querySelector('[data-effort-mark]')).toBeNull()
    const blocked = screen.getByTestId('blocked')
    expect(blocked.textContent).toBe('Nothing sent')
    expect(blocked).toHaveAccessibleName("Effort: Not set: Agent's setting: Off, nothing sent here")
    expect(blocked.querySelector('[data-effort-mark="attention"]')).not.toBeNull()
  })

  it('C2: after /effort the chip shows the gold dot and the level only; the name says "set for this conversation"', async () => {
    renderWithCatalog(
      <EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'high' }} scope="chat" setByCommand data-testid="chip" />,
    )
    const chip = await screen.findByTestId('chip')
    expect(chip.textContent).toBe('High')
    expect(chip).toHaveAccessibleName('Effort: High, set for this conversation')
    expect(chip.querySelector('[data-effort-mark="command"]')).not.toBeNull()
  })
})

describe('blocked switch step as drawn in the wireframes', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  it('levels target: says where you stay, offers "Let the provider decide instead" and Cancel', async () => {
    const user = userEvent.setup()
    const onSwitchComplete = vi.fn()
    const onSwitchCancel = vi.fn()
    const view = (
      <EffortControl
        value={{ provider: 'openrouter', model: PREVIOUS_MODEL, effort: 'off' }}
        scope="chat"
        pendingSwitch={{ provider: 'openrouter', model: LEVELS_MODEL, kind: 'levels', nearest: 'low' }}
        onSwitchComplete={onSwitchComplete}
        onSwitchCancel={onSwitchCancel}
      />
    )
    const { unmount } = renderWithCatalog(view)
    await screen.findByRole('radio', { name: 'Low' })
    expect(screen.getByText(`You stay on ${PREVIOUS_MODEL} until you do.`)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Let the provider decide instead' }))
    expect(onSwitchComplete).toHaveBeenCalledWith('provider_default')
    unmount()

    renderWithCatalog(view)
    await screen.findByRole('radio', { name: 'Low' })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onSwitchCancel).toHaveBeenCalledTimes(1)
    expect(onSwitchComplete).toHaveBeenCalledTimes(1)
  })

  it('nothing-sendable target: shows the wireframe text and announces the spec sentence', async () => {
    renderWithCatalog(
      <EffortControl
        value={{ provider: 'openrouter', model: PREVIOUS_MODEL, effort: 'off' }}
        scope="chat"
        pendingSwitch={{ provider: 'openrouter', model: MUTED_MODEL, kind: 'nothing_sendable', nearest: '' }}
      />,
    )
    await screen.findByRole('button', { name: 'Let the provider decide' })
    expect(screen.getByText(`${MUTED_MODEL} can't turn reasoning off`)).toBeInTheDocument()
    expect(
      screen.getByText(`This model has no levels to pick. You can let the provider decide, or stay where you are. You stay on ${PREVIOUS_MODEL} until you do.`),
    ).toBeInTheDocument()
    const spoken = screen.getByText(`${MUTED_MODEL} can't turn reasoning off here. Let the provider decide, or cancel.`)
    expect(spoken).toHaveAttribute('role', 'status')
    expect(spoken).toHaveAttribute('aria-live', 'polite')
  })
})

describe('catalog loading and failure as drawn in the wireframes', () => {
  it('loading: a spinner and "Loading model details. Saved effort: High."', async () => {
    vi.mocked(fetchProvidersCatalog).mockReturnValue(new Promise(() => {}))
    renderWithCatalog(<EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'high' }} scope="default-model" />)
    const line = screen.getByText('High').parentElement as HTMLElement
    expect(line.textContent).toBe('Loading model details. Saved effort: High.')
    expect(line.parentElement?.querySelector('svg[data-effort-spinner]')).not.toBeNull()
  })

  it('failed: "Couldn\'t load model details. Saved effort: High." with Retry', async () => {
    vi.mocked(fetchProvidersCatalog).mockRejectedValue(new Error('catalog down'))
    renderWithCatalog(<EffortControl value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'high' }} scope="default-model" />)
    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load model details.")
    const saved = screen.getByText('High').parentElement as HTMLElement
    expect(saved.textContent).toBe('Saved effort: High.')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})

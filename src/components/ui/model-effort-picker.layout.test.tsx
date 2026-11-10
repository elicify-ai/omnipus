// model-effort-picker.layout.test.tsx — the effort radio group's layout and
// the blocked step's "nearest" marker, as the founder-approved picker
// wireframes and the effort spec draw them.
//
// Oracles:
//   - effort-levels-spec-r10.md FD-16 P-Q1 A ("row of named buttons (radio
//     group), not a slider") and "Design-System Components" ("One group: Not set
//     row (where inheritable), Provider default row, then the levels as named
//     buttons; switch models Off, On").
//   - effort-levels-spec-r10.md "UI Screens and States", Blocked-switch step:
//     "Pick a level to finish switching." with the nearest marked; S-10 ("Low
//     marked as nearest but not chosen and focused").
//   - Wireframes designs-20261029/effort-picker-preview (shot-1, shot-2, shot-7):
//     a "Level" label above the level row ("Reasoning" for an on/off model), the
//     word "nearest" under the suggested level.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactElement } from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { EffortControl, ModelEffortPicker } from './model-effort-picker'
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

const LEVELS_MODEL = 'layout-fixture/low-medium-high'
const SWITCH_MODEL = 'layout-fixture/on-off'

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
        { id: SWITCH_MODEL, reasoning: { levels: [], switch: true } },
      ].map((m) => ({ ...m, name: m.id, context_window: 128000, max_output_tokens: 8192, input_modalities: ['text'], tool_call: true, status: 'active' })),
    },
  ],
} as unknown as ProvidersCatalog

function renderWithCatalog(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

/** The group's children in document order, as "radio:<name>" or "text:<visible text>". */
function groupSequence(group: HTMLElement): string[] {
  return Array.from(group.children).flatMap((child) => {
    const el = child as HTMLElement
    if (el.getAttribute('role') === 'radio') return [`radio:${(el.textContent ?? '').trim()}`]
    if (el.classList.contains('sr-only')) return []
    return [`text:${(el.textContent ?? '').trim()}`]
  })
}

describe('effort radio group layout (FD-16 P-Q1 A)', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  it('puts Not set and Provider default on top, then a "Level" label, then the level buttons, all in one group', async () => {
    renderWithCatalog(
      <ModelEffortPicker value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: '' }} inherit="high" scope="chat" />,
    )
    const group = await screen.findByRole('radiogroup')
    expect(groupSequence(group)).toEqual([
      "radio:Not set: Agent's setting: High",
      'radio:Provider default',
      'text:Level',
      'radio:Low',
      'radio:Medium',
      'radio:High',
    ])
    // The label is decoration: assistive technology reads only the radios.
    expect(within(group).getByText('Level')).toHaveAttribute('aria-hidden', 'true')
  })

  it('labels an on/off model\'s row "Reasoning"', async () => {
    renderWithCatalog(
      <ModelEffortPicker value={{ provider: 'openrouter', model: SWITCH_MODEL, effort: 'on' }} scope="agent-fallback" />,
    )
    const group = await screen.findByRole('radiogroup')
    expect(groupSequence(group)).toEqual(['radio:Provider default', 'text:Reasoning', 'radio:Off', 'radio:On'])
  })

  it('keeps exactly one Tab stop across both rows and arrows from the top rows into the level row', async () => {
    const user = userEvent.setup()
    renderWithCatalog(
      <ModelEffortPicker value={{ provider: 'openrouter', model: LEVELS_MODEL, effort: 'provider_default' }} inherit="high" scope="chat" />,
    )
    await screen.findByRole('radiogroup')
    const radios = screen.getAllByRole('radio')
    expect(radios.filter((r) => r.tabIndex === 0)).toEqual([screen.getByRole('radio', { name: 'Provider default' })])
    screen.getByRole('radio', { name: 'Provider default' }).focus()
    await user.keyboard('{ArrowDown}')
    expect(screen.getByRole('radio', { name: 'Low' })).toHaveFocus()
  })
})

describe('blocked step marks the nearest level (S-10)', () => {
  beforeEach(() => {
    vi.mocked(fetchProvidersCatalog).mockResolvedValue(CATALOG)
  })

  it('shows "nearest" under the suggested level only, keeps its name "Low" and describes it as the nearest level', async () => {
    renderWithCatalog(
      <EffortControl
        value={{ provider: 'openrouter', model: 'layout-fixture/previous', effort: 'off' }}
        scope="chat"
        pendingSwitch={{ provider: 'openrouter', model: LEVELS_MODEL, kind: 'levels', nearest: 'low' }}
      />,
    )
    const low = await screen.findByRole('radio', { name: 'Low' })
    expect(low).toHaveTextContent(/^Lownearest$/)
    expect(low).toHaveAccessibleDescription('Nearest level')
    expect(low).toHaveAttribute('aria-checked', 'false')
    for (const name of ['Medium', 'High']) {
      const other = screen.getByRole('radio', { name })
      expect(other).not.toHaveTextContent('nearest')
      expect(other).not.toHaveAttribute('aria-describedby')
    }
    expect(screen.getAllByText('nearest')).toHaveLength(1)
  })
})

/**
 * ProviderPicker.test.tsx — ADR-068 spec TDD plan row 25.
 *
 * Scenarios covered (spec §"User Story 5"):
 *   • Picker opens with 8 Popular tiles and a collapsed list
 *   • Search expands and filters the full list (component level; the dataset
 *     rows live in provider-picker-search.test.ts, row 25a)
 *   • Expanded list is letter-grouped and virtualised (SC-005: ≤ 22 options in
 *     the 480 px / 40 px fixture)
 *   • Unsupported provider is visible, disabled, with reason
 *   • Custom endpoint is last
 *   • Recently used row appears
 *   • Keyboard-only selection
 *   • Catalog unavailable in the picker (FR-037)
 *
 * jsdom note: `@tanstack/react-virtual` sizes its window from the scroll
 * element's `offsetHeight`, which jsdom always reports as 0 — every test would
 * then render one row and SC-005's bound would pass vacuously. `stubViewport`
 * below gives ONLY the picker's scroll element a real height, so the virtual
 * window is the real 480/40 window the success criterion is stated against.
 */

import * as React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import type { CatalogProvider, Provider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import {
  ProviderPicker,
  PROVIDER_PICKER_OPEN_MARK,
  UNSUPPORTED_REASON_COPY,
  type PickerSelection,
} from './ProviderPicker'
import { CUSTOM_ENDPOINT_LABEL } from './provider-picker-model'

// ── Passthrough that records the props the picker mounts cmdk with ──────────
// FR-026 requires `shouldFilter={false}`. `@/components/ui/command` is a thin
// wrapper that forwards every prop to cmdk's `Command`, so recording what the
// picker hands the wrapper records what cmdk receives. (Mocking the `cmdk`
// package itself does not work here: Vitest pre-bundles it as an external dep,
// so the module the wrapper imports is not the one the mock replaces — the
// factory runs and the recorder never fires, which would have made this a test
// that passes without checking anything.)
const cmdkRootProps: Record<string, unknown>[] = []
vi.mock('@/components/ui/command', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/components/ui/command')>()
  const Recorder = React.forwardRef<HTMLDivElement, Record<string, unknown>>((props, ref) => {
    cmdkRootProps.push(props)
    return React.createElement(actual.Command as never, { ...props, ref } as never)
  })
  Recorder.displayName = 'CommandRecorder'
  return { ...actual, Command: Recorder }
})

// jsdom lacks ResizeObserver / scrollIntoView; cmdk needs both to mount.
if (typeof window !== 'undefined' && !window.ResizeObserver) {
  window.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}

const VIEWPORT = 480
const ROW = 40

function stubViewport() {
  const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('data-testid') === 'picker-virtual-viewport' ? VIEWPORT : 0
    },
  })
  return () => {
    if (original) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', original)
    else delete (HTMLElement.prototype as unknown as Record<string, unknown>).offsetHeight
  }
}

function popularIdsInCatalogOrder(catalog: ProvidersCatalog): string[] {
  const seen = new Set<string>()
  const ids: string[] = []
  for (const p of catalog.providers) {
    if (p.tier !== 'popular') continue
    if (seen.has(p.company)) continue
    seen.add(p.company)
    ids.push(p.id)
  }
  return ids
}

function renderPicker(props: Partial<React.ComponentProps<typeof ProviderPicker>> = {}) {
  const onSelect = vi.fn<(s: PickerSelection) => void>()
  const utils = render(
    <ProviderPicker
      catalog={PROVIDERS_CATALOG}
      onSelect={onSelect}
      viewportHeight={VIEWPORT}
      rowHeight={ROW}
      {...props}
    />,
  )
  return { ...utils, onSelect }
}

let restoreViewport: () => void

beforeEach(() => {
  cmdkRootProps.length = 0
  restoreViewport = stubViewport()
})

afterEach(() => {
  restoreViewport()
  vi.restoreAllMocks()
})

describe('ProviderPicker — Popular tiles and the collapsed list', () => {
  it('renders exactly 8 Popular tiles, one per popular company, in catalog order', () => {
    renderPicker()
    const ids = popularIdsInCatalogOrder(PROVIDERS_CATALOG)
    expect(ids).toHaveLength(8)

    const tiles = screen.getAllByTestId(/^picker-popular-/)
    expect(tiles).toHaveLength(8)
    expect(tiles.map((t) => t.getAttribute('data-testid'))).toEqual(
      ids.map((id) => `picker-popular-${id}`),
    )
  })

  it('shows "All providers (182)" collapsed, with the list hidden', () => {
    renderPicker()
    const toggle = screen.getByTestId('picker-all-toggle')
    // 190 catalog entries minus the 8 popular ones.
    expect(toggle).toHaveTextContent('All providers (182)')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByTestId(/^picker-row-/)).toHaveLength(0)
  })

  it('re-renders the band from catalog data alone when tiers move (no SPA constant)', () => {
    const mutated: ProvidersCatalog = {
      ...PROVIDERS_CATALOG,
      providers: PROVIDERS_CATALOG.providers.map((p): CatalogProvider => {
        if (p.id === 'groq') return { ...p, tier: 'standard' }
        if (p.id === 'cerebras') return { ...p, tier: 'popular' }
        return p
      }),
    }
    expect(mutated.providers.some((p) => p.id === 'cerebras')).toBe(true)

    renderPicker({ catalog: mutated })
    expect(screen.queryByTestId('picker-popular-groq')).toBeNull()
    expect(screen.getByTestId('picker-popular-cerebras')).toBeInTheDocument()
    expect(screen.getAllByTestId(/^picker-popular-/)).toHaveLength(8)
  })

  it('records a performance mark when it opens', () => {
    const mark = vi.spyOn(performance, 'mark')
    renderPicker()
    expect(mark).toHaveBeenCalledWith(PROVIDER_PICKER_OPEN_MARK)
  })
})

describe('ProviderPicker — expanded, letter-grouped, virtualised list', () => {
  it('mounts cmdk with shouldFilter={false}', () => {
    renderPicker()
    expect(cmdkRootProps.length).toBeGreaterThan(0)
    for (const props of cmdkRootProps) {
      expect(props.shouldFilter).toBe(false)
    }
  })

  it('keeps rendered options within SC-005s bound of 22 for the 480/40 fixture', () => {
    renderPicker()
    fireEvent.click(screen.getByTestId('picker-all-toggle'))

    expect(screen.getByTestId('picker-all-toggle')).toHaveAttribute('aria-expanded', 'true')
    const options = screen.getAllByRole('option')
    // 96 companies are in the filtered set; only a window of them may mount.
    expect(options.length).toBeGreaterThan(1)
    expect(options.length).toBeLessThanOrEqual(Math.floor(VIEWPORT / ROW) + 10)
  })

  it('gives every rendered company row aria-setsize and a distinct aria-posinset', () => {
    renderPicker()
    fireEvent.click(screen.getByTestId('picker-all-toggle'))

    const rows = screen.getAllByTestId(/^picker-row-/)
    expect(rows.length).toBeGreaterThan(1)

    const setSizes = new Set(rows.map((r) => r.getAttribute('aria-setsize')))
    expect(setSizes.size).toBe(1)
    const setSize = Number([...setSizes][0])
    // One row per company in the unfiltered catalog.
    const companies = new Set(PROVIDERS_CATALOG.providers.map((p) => p.company))
    expect(setSize).toBe(companies.size)

    const positions = rows.map((r) => Number(r.getAttribute('aria-posinset')))
    expect(new Set(positions).size).toBe(positions.length)
    for (const pos of positions) {
      expect(pos).toBeGreaterThanOrEqual(1)
      expect(pos).toBeLessThanOrEqual(setSize)
    }
  })

  it('renders letter headers, never out of A→Z order and never # before a letter', () => {
    renderPicker()
    fireEvent.click(screen.getByTestId('picker-all-toggle'))

    const headers = screen.getAllByTestId(/^picker-letter-/).map((h) => h.textContent ?? '')
    expect(headers.length).toBeGreaterThan(0)
    const rank = (letter: string) => (letter === '#' ? 27 : letter.charCodeAt(0) - 64)
    for (let i = 1; i < headers.length; i += 1) {
      expect(rank(headers[i])).toBeGreaterThan(rank(headers[i - 1]))
    }
  })
})

describe('ProviderPicker — unsupported providers (FR-025)', () => {
  it('shows Amazon Bedrock disabled with its mapped reason, never the raw enum', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderPicker()

    await user.type(screen.getByTestId('picker-search'), 'bedrock')

    const bedrock = screen.getByText('Amazon').closest('[role="option"]') as HTMLElement
    expect(bedrock).toHaveAttribute('aria-disabled', 'true')
    expect(bedrock).toHaveTextContent(UNSUPPORTED_REASON_COPY['cloud-iam'])
    expect(bedrock).toHaveTextContent('needs request signing')
    expect(bedrock.textContent).not.toContain('cloud-iam')

    fireEvent.click(bedrock)
    expect(onSelect).not.toHaveBeenCalled()
  })
})

describe('ProviderPicker — Recent (FR-022)', () => {
  const configured: Provider[] = [
    {
      id: 'zai-coding-plan',
      name: 'zai-coding-plan',
      display_name: 'Z.ai Coding Plan',
      status: 'connected',
      auth_method: 'api_key',
      updated_at: '2026-08-20T10:00:00Z',
    } as Provider,
    {
      id: 'openai',
      name: 'openai',
      display_name: 'OpenAI',
      status: 'connected',
      auth_method: 'api_key',
      updated_at: '2026-08-19T10:00:00Z',
    } as Provider,
    {
      id: 'groq',
      name: 'groq',
      display_name: 'Groq',
      status: 'connected',
      auth_method: 'api_key',
      updated_at: '2026-08-18T10:00:00Z',
    } as Provider,
    {
      id: 'anthropic',
      name: 'anthropic',
      display_name: 'Anthropic',
      status: 'connected',
      auth_method: 'api_key',
      updated_at: '2026-08-17T10:00:00Z',
    } as Provider,
  ]

  it('lists at most three configured providers, newest first, between the tiles and the search field', () => {
    renderPicker({ configured })

    const recent = screen.getByTestId('picker-recent')
    const labels = within(recent)
      .getAllByRole('option')
      .map((r) => r.textContent)
    expect(labels).toEqual(['Z.ai Coding Plan', 'OpenAI', 'Groq'])

    const tiles = screen.getByTestId('picker-popular')
    const search = screen.getByTestId('picker-search')
    expect(tiles.compareDocumentPosition(recent) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(recent.compareDocumentPosition(search) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('emits a recent selection carrying the configured provider', () => {
    const { onSelect } = renderPicker({ configured })
    fireEvent.click(screen.getByTestId('picker-recent-zai-coding-plan'))
    expect(onSelect).toHaveBeenCalledWith({ kind: 'recent', provider: configured[0] })
  })
})

describe('ProviderPicker — keyboard (FR-026)', () => {
  it('selects the third Popular tile with ArrowDown x3 + Enter and dispatches no pointer event', async () => {
    const user = userEvent.setup()
    const pointerEvents: string[] = []
    const record = (e: Event) => pointerEvents.push(e.type)
    for (const type of ['click', 'pointerdown', 'mousedown']) {
      document.addEventListener(type, record, true)
    }

    const { onSelect } = renderPicker()
    expect(screen.getByTestId('picker-search')).toHaveFocus()

    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{Enter}')

    const thirdId = popularIdsInCatalogOrder(PROVIDERS_CATALOG)[2]
    expect(thirdId).toBe('openrouter')
    expect(onSelect).toHaveBeenCalledTimes(1)
    const selection = onSelect.mock.calls[0][0]
    expect(selection.kind).toBe('tile')
    expect(selection.kind === 'tile' && selection.provider.id).toBe('openrouter')
    expect(pointerEvents).toEqual([])

    for (const type of ['click', 'pointerdown', 'mousedown']) {
      document.removeEventListener(type, record, true)
    }
  })

  it('End focuses Custom endpoint and scrolls the virtual list to its last row; Home returns to the first tile', async () => {
    const user = userEvent.setup()
    const onVirtualScrollToIndex = vi.fn()
    renderPicker({ onVirtualScrollToIndex })

    fireEvent.click(screen.getByTestId('picker-all-toggle'))
    screen.getByTestId('picker-search').focus()

    await user.keyboard('{End}')
    expect(screen.getByTestId('picker-custom-endpoint')).toHaveFocus()
    expect(onVirtualScrollToIndex).toHaveBeenCalled()
    const lastIndex = onVirtualScrollToIndex.mock.calls.at(-1)?.[0] as number
    // 96 company rows plus one header per letter group — the last flat entry.
    expect(lastIndex).toBeGreaterThan(96)

    await user.keyboard('{Home}')
    const firstTileId = popularIdsInCatalogOrder(PROVIDERS_CATALOG)[0]
    expect(screen.getByTestId(`picker-popular-${firstTileId}`)).toHaveFocus()
  })

  it('walks into the virtualised list by index, scrolling unmounted rows into view', async () => {
    const user = userEvent.setup()
    const onVirtualScrollToIndex = vi.fn()
    const { onSelect } = renderPicker({ onVirtualScrollToIndex })

    fireEvent.click(screen.getByTestId('picker-all-toggle'))
    screen.getByTestId('picker-search').focus()

    // 8 tiles, no recent rows — the 9th ArrowDown enters the letter list.
    await user.keyboard('{ArrowDown}'.repeat(9))
    expect(onVirtualScrollToIndex).toHaveBeenCalled()
    await user.keyboard('{Enter}')
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect.mock.calls[0][0].kind).toBe('row')
  })
})

describe('ProviderPicker — Custom endpoint is last (FR-022/FR-024)', () => {
  it('renders the Custom endpoint row after every other option', () => {
    renderPicker()
    fireEvent.click(screen.getByTestId('picker-all-toggle'))

    const options = screen.getAllByRole('option')
    expect(options.at(-1)).toHaveTextContent(CUSTOM_ENDPOINT_LABEL)
  })

  it('opens the Custom endpoint panel and emits the draft it collects', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderPicker()

    fireEvent.click(screen.getByTestId('picker-custom-endpoint'))
    const panel = screen.getByTestId('custom-endpoint-panel')

    await user.type(within(panel).getByTestId('custom-endpoint-id'), 'my-endpoint')
    await user.type(within(panel).getByTestId('custom-endpoint-api-base'), 'https://llm.example.com/v1')
    await user.selectOptions(within(panel).getByTestId('custom-endpoint-protocol'), 'anthropic')
    await user.type(within(panel).getByTestId('custom-endpoint-api-key'), 'sk-test')
    fireEvent.click(within(panel).getByTestId('custom-endpoint-submit'))

    expect(onSelect).toHaveBeenCalledWith({
      kind: 'custom',
      draft: {
        id: 'my-endpoint',
        api_base: 'https://llm.example.com/v1',
        protocol: 'anthropic',
        api_key: 'sk-test',
      },
    })
  })
})

describe('ProviderPicker — catalog unavailable (FR-037)', () => {
  it('shows a retryable alert and keeps Custom endpoint selectable', () => {
    const onRetry = vi.fn()
    const { onSelect } = renderPicker({ catalog: undefined, status: 'error', onRetry })

    const alert = screen.getByTestId('picker-catalog-error')
    expect(alert).toHaveAttribute('role', 'alert')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    fireEvent.click(screen.getByTestId('picker-catalog-retry'))
    expect(onRetry).toHaveBeenCalledTimes(1)

    const custom = screen.getByTestId('picker-custom-endpoint')
    expect(custom).toBeInTheDocument()
    fireEvent.click(custom)
    expect(screen.getByTestId('custom-endpoint-panel')).toBeInTheDocument()
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('renders no Popular tiles and no rows when the catalog is missing', () => {
    renderPicker({ catalog: null, status: 'error' })
    expect(screen.queryAllByTestId(/^picker-popular-/)).toHaveLength(0)
    expect(screen.queryAllByTestId(/^picker-row-/)).toHaveLength(0)
  })
})

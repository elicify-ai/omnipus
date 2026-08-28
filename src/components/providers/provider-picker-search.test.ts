/**
 * provider-picker-search.test.ts — ADR-068 spec TDD plan row 25a.
 *
 * FR-024 at component level (MIN-009): the picker's search field, driven over
 * the RENDERED component rather than the pure model, against the spec's
 * "Picker search query" dataset (rows 1–10).
 *
 * The oracle for "did it filter correctly" is never the component's own output:
 * each row states, from the spec, what the query is expected to match, and the
 * assertions check the rendered DOM against that. The invariant every row
 * shares — every rendered row's company, name, plan, region or alias contains
 * the query, case-insensitively and literally — is computed here from the
 * fixture, not read back from the component.
 *
 * No JSX: this file is `.ts` because the spec names it `.ts`, so the component
 * is mounted through `React.createElement`.
 */

import * as React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { ProviderPicker } from './ProviderPicker'
import { CUSTOM_ENDPOINT_LABEL } from './provider-picker-model'

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

/** Give only the picker's scroll element a height; jsdom reports 0 for all. */
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

/** Companies the spec's filter must match — computed from the fixture, not the component. */
function expectedCompanies(query: string): string[] {
  const needle = query.trim().toLocaleLowerCase()
  const out = new Set<string>()
  for (const p of PROVIDERS_CATALOG.providers) {
    const fields = [p.company, p.name, p.plan ?? '', p.region ?? '', ...(p.aliases ?? [])]
    if (fields.some((f) => f.toLocaleLowerCase().includes(needle))) out.add(p.company)
  }
  return [...out]
}

function renderPicker() {
  const onSelect = vi.fn()
  render(
    React.createElement(ProviderPicker, {
      catalog: PROVIDERS_CATALOG,
      onSelect,
      viewportHeight: VIEWPORT,
      rowHeight: ROW,
    }),
  )
  return { onSelect }
}

function renderedCompanies(): string[] {
  return screen
    .queryAllByTestId(/^picker-row-/)
    .map((el) => el.getAttribute('data-testid')!.replace(/^picker-row-/, ''))
}

let restoreViewport: () => void

beforeEach(() => {
  restoreViewport = stubViewport()
})

afterEach(() => {
  cleanup()
  restoreViewport()
})

async function search(query: string) {
  const user = userEvent.setup()
  renderPicker()
  const input = screen.getByTestId('picker-search')
  // `{` and `[` open key descriptors in user-event's keyboard grammar; doubling
  // them is the documented escape for the literal character (dataset row 8).
  if (query.length > 0) await user.type(input, query.replace(/[{[]/g, '$&$&'))
  return user
}

describe('ProviderPicker search — dataset rows 1–10 (FR-024)', () => {
  it('row 1 — the empty query leaves the list collapsed', async () => {
    await search('')
    expect(screen.getByTestId('picker-all-toggle')).toHaveAttribute('aria-expanded', 'false')
    expect(renderedCompanies()).toHaveLength(0)
  })

  it('row 2 — a whitespace-only query is trimmed and leaves the list collapsed', async () => {
    await search('   ')
    expect(screen.getByTestId('picker-all-toggle')).toHaveAttribute('aria-expanded', 'false')
    expect(renderedCompanies()).toHaveLength(0)
  })

  it('row 3 — a single character expands the list and every rendered row matches it', async () => {
    await search('z')
    expect(screen.getByTestId('picker-all-toggle')).toHaveAttribute('aria-expanded', 'true')

    const expected = expectedCompanies('z')
    expect(expected.length).toBeGreaterThan(1)
    const rendered = renderedCompanies()
    expect(rendered.length).toBeGreaterThan(0)
    for (const company of rendered) expect(expected).toContain(company)
  })

  it('row 4 — "Coding Plan" matches case-insensitively across name and plan', async () => {
    await search('Coding Plan')
    const expected = expectedCompanies('Coding Plan')
    expect(expected).toContain('Zhipu AI')
    const rendered = renderedCompanies()
    expect(rendered.length).toBeGreaterThan(0)
    for (const company of rendered) expect(expected).toContain(company)
  })

  it('row 5 — "china" matches on region', async () => {
    await search('china')
    const expected = expectedCompanies('china')
    expect(expected).toContain('Zhipu AI')
    const rendered = renderedCompanies()
    expect(rendered).toContain('Zhipu AI')
    for (const company of rendered) expect(expected).toContain(company)
  })

  it('row 6 — "glm-coding" matches on alias alone and yields the Z.ai row', async () => {
    await search('glm-coding')
    expect(expectedCompanies('glm-coding')).toEqual(['Zhipu AI'])
    expect(renderedCompanies()).toEqual(['Zhipu AI'])
  })

  it('row 7 — "bedrock" keeps the unsupported row visible and disabled', async () => {
    await search('bedrock')
    expect(renderedCompanies()).toEqual(['Amazon'])
    const row = screen.getByTestId('picker-row-Amazon')
    expect(row).toHaveAttribute('aria-disabled', 'true')
    expect(row).toHaveTextContent('needs request signing')
  })

  it('row 8 — regex metacharacters are treated literally: no crash, no match', async () => {
    await search('(*[')
    expect(expectedCompanies('(*[')).toEqual([])
    expect(renderedCompanies()).toHaveLength(0)
    expect(screen.getByTestId('picker-empty')).toHaveTextContent('No provider matches (*[')
    expect(screen.getByTestId('picker-custom-endpoint')).toHaveTextContent(CUSTOM_ENDPOINT_LABEL)
  })

  it('row 9 — a 200-character query matches nothing and shows the empty state', async () => {
    const long = 'a'.repeat(200)
    await search(long)
    expect(renderedCompanies()).toHaveLength(0)
    expect(screen.getByTestId('picker-empty')).toBeInTheDocument()
    expect(screen.getByTestId('picker-custom-endpoint')).toBeInTheDocument()
  })

  it('row 10 — a Unicode alias matches: 智谱 yields the Z.ai row', async () => {
    await search('智谱')
    expect(expectedCompanies('智谱')).toEqual(['Zhipu AI'])
    expect(renderedCompanies()).toEqual(['Zhipu AI'])
  })

  it('shows "No provider matches zzzz" with Custom endpoint still present', async () => {
    await search('zzzz')
    expect(screen.getByTestId('picker-empty')).toHaveTextContent('No provider matches zzzz')
    expect(screen.getByTestId('picker-custom-endpoint')).toBeInTheDocument()
  })
})

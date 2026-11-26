// The default-model picker with the REAL popover and a virtualised catalog
// (over 100 models). Other ModelSelector tests render the popover inline,
// which hid U6-3.
//  - U6-3: the popover mounts its content one render late; the list must
//    still show rows on the first open, and say it is loading (never
//    "No models found") while the catalog's first read is in flight.
//  - U6-1 / FR-070: on a coarse pointer the virtualiser lays rows out at the
//    44 px touch minimum (no overlap); a mouse keeps the 32 px slot.
// jsdom has no layout: physical row sizes stay a browser check.
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DefaultModelCard } from './DefaultModelCard'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import type { ComponentProps } from 'react'
import type { DefaultModel, Provider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'

beforeAll(() => {
  if (!window.ResizeObserver) {
    window.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
  // The virtualiser measures its scroll element with offsetHeight, which jsdom
  // hard-codes to 0; give the cmdk list the 300 px it has in the browser.
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true, get(this: HTMLElement) { return this.hasAttribute('cmdk-list') ? 300 : 0 },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true, get(this: HTMLElement) { return this.hasAttribute('cmdk-list') ? 320 : 0 },
  })
})

afterEach(() => { cleanup(); vi.unstubAllGlobals() })

const openrouter = PROVIDERS_CATALOG.providers.find((entry) => entry.id === 'openrouter')!
const template = openrouter.models[0]
const catalog: ProvidersCatalog = {
  ...PROVIDERS_CATALOG,
  providers: PROVIDERS_CATALOG.providers.map((entry) => entry.id !== 'openrouter' ? entry : {
    ...entry,
    models: Array.from({ length: 150 }, (_, i) => ({ ...template, id: `acme/model-${String(i).padStart(3, '0')}`, name: `Model ${i}` })),
  }),
}
const providers = [{
  id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter', status: 'connected',
  auth_method: 'api_key', dependents: [], backs_default: true, models: [],
}] as unknown as Provider[]
const defaultModel = { provider: 'openrouter', model: 'acme/model-000', context_window: 1, window_unknown: false } as DefaultModel

function pointer(coarse: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: coarse && query.includes('pointer: coarse'), media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }))
}

const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)) })
const listState = () => ({ options: screen.queryAllByRole('option').length, empty: screen.queryByText('No models found.') !== null })

async function openPicker(props: Partial<ComponentProps<typeof DefaultModelCard>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<DefaultModelCard defaultModel={defaultModel} providers={providers} catalog={catalog} onChange={vi.fn()} {...props} />, {
    wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  })
  await userEvent.click(screen.getByTestId('default-model-change-btn'))
  await userEvent.click(await screen.findByTestId('default-model-select'))
  await settle()
  return view
}

describe('U6-3 default-model picker first open', () => {
  it('lists the rows on the first open, and again on a second open', async () => {
    await openPicker()
    const first = listState()
    await userEvent.keyboard('{Escape}')
    await userEvent.click(screen.getByTestId('default-model-select'))
    await settle()
    expect(first.empty, 'the first open must not say No models found').toBe(false)
    expect(first.options).toBeGreaterThan(0)
    expect(listState(), 'a second open still lists the rows').toEqual(first)
  })

  it('says it is loading while the catalog loads, then shows the rows', async () => {
    const view = await openPicker({ catalog: undefined, catalogLoading: true })
    expect(screen.queryByText('No models found.')).toBeNull()
    expect(screen.getByText('Loading models…')).toBeInTheDocument()
    view.rerender(<DefaultModelCard defaultModel={defaultModel} providers={providers} catalog={catalog} onChange={vi.fn()} />)
    await settle()
    expect(listState().empty).toBe(false)
    expect(listState().options).toBeGreaterThan(0)
  })
})

async function virtualSlots() {
  await openPicker()
  const rows = [...screen.getByTestId('model-selector-virtual-list').children] as HTMLElement[]
  expect(rows.length).toBeGreaterThan(0)
  return rows
}

describe('U6-1 the virtualised model list sizes its row slots to the pointer', () => {
  it('coarse pointer: every slot is 44 px and the rows do not overlap', async () => {
    pointer(true)
    const rows = await virtualSlots()
    expect(new Set(rows.map((row) => row.style.height))).toEqual(new Set(['44px']))
    const starts = rows.map((row) => Number(/translateY\((\d+)px\)/.exec(row.style.transform)?.[1]))
    starts.slice(1).forEach((start, i) => expect(start - starts[i]).toBe(44))
  })

  it('fine pointer: the slot stays 32 px (no change for mouse users)', async () => {
    pointer(false)
    const rows = await virtualSlots()
    expect(new Set(rows.map((row) => row.style.height))).toEqual(new Set(['32px']))
  })
})

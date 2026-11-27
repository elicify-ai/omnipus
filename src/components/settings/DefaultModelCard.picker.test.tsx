// The default-model picker with the REAL popover and a virtualised catalog
// (over 100 models). Other ModelSelector tests render the popover inline,
// which hid U6-3.
//  - U6-3: the popover mounts its content one render late; the list must
//    still show rows on the first open, and say it is loading (never
//    "No models found") while the catalog's first read is in flight.
//  - U6-1 / FR-070: on a coarse pointer the virtualiser lays rows out at the
//    44 px touch minimum (no overlap); a mouse keeps the 32 px slot.
//  - U6-6: a row with the recommended badge still shows its model name.
// jsdom has no layout: physical row sizes and widths stay a browser check.
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

describe('U6-6 a recommended row still shows its model name', () => {
  // jsdom has no layout, so the 0 px name the tester measured cannot be shown
  // here. What jsdom can show is the cause and the guards: the popup's width
  // class must be a real CSS width (Tailwind v4 compiles `w-[--x]` to the
  // invalid `width: --x`, so the popup fell back to its narrow 18rem default),
  // the name keeps a floor of the row so the badge cannot take its place, and
  // the full name is on the row for hover and for assistive technology.
  // (U7-1 narrowed "takes the trigger width" to "at least 18rem or the trigger
  // if wider"; the exact width contract is pinned in the U7-1 case below.)
  it('the popup has a real CSS width, the name keeps its floor and its full text', async () => {
    pointer(false)
    await openPicker()
    const list = document.querySelector('[cmdk-list]') as HTMLElement
    const popup = list.closest('[data-radix-popper-content-wrapper] > *') as HTMLElement
    expect(popup.className).toContain('var(--radix-popover-trigger-width)')
    expect(popup.className).not.toMatch(/w-\[--/)
    const recommended = [...document.querySelectorAll<HTMLElement>('[cmdk-item][data-recommended]')]
    expect(recommended.length).toBeGreaterThan(0)
    for (const row of recommended) {
      const id = row.getAttribute('data-model') ?? ''
      const spans = [...row.querySelectorAll('span')]
      const name = spans.find((span) => span.textContent === id)
      const badge = spans.find((span) => span !== name && /recommended/i.test(span.textContent ?? ''))
      expect(name, `${id} has its name`).toBeDefined()
      expect(badge, `${id} has its badge`).toBeDefined()
      expect(name?.title).toBe(id)
      expect(name?.className).toContain('min-w-[50%]')
      expect(badge?.className, 'the badge gives way before the name does').not.toContain('shrink-0')
      expect(badge?.className).toContain('truncate')
    }
  })
})

describe('U7-1 the model popup is readable even on a narrow trigger', () => {
  // The agent form's trigger is about 110 px wide at 390 coarse; a popup that
  // only takes the trigger width clips every name. jsdom has no layout: this
  // pins the width contract (at least 18rem, the trigger if wider, capped at
  // the free viewport width); the physical width stays a browser check.
  it('sizes to max(18rem, trigger width), capped at the free viewport width', async () => {
    pointer(true)
    await openPicker()
    const list = document.querySelector('[cmdk-list]') as HTMLElement
    const popup = list.closest('[data-radix-popper-content-wrapper] > *') as HTMLElement
    const classes = popup.className.split(/\s+/)
    expect(classes).toContain('w-[max(18rem,var(--radix-popover-trigger-width))]')
    expect(classes).toContain('max-w-[var(--radix-popover-content-available-width)]')
    expect(classes).not.toContain('w-[var(--radix-popover-trigger-width)]')
  })
})

describe('U7-2 no row is taller than its virtual slot', () => {
  // A recommended row measured 33.19 px in a 32 px slot at 1280 fine, so its
  // bottom edge picked the next model. Each row fills its slot exactly and
  // clips anything taller. jsdom has no layout: this pins the classes.
  for (const coarse of [false, true]) {
    it(`${coarse ? 'coarse' : 'fine'} pointer: every row fills its slot and clips overflow`, async () => {
      pointer(coarse)
      const rows = await virtualSlots()
      const recommended = rows.filter((slot) => slot.querySelector('[cmdk-item][data-recommended]'))
      expect(recommended.length).toBeGreaterThan(0)
      for (const slot of rows) {
        const item = slot.querySelector('[cmdk-item]')
        if (!item) continue
        expect(item.className, item.getAttribute('data-model') ?? '').toContain('h-full')
        expect(item.className, item.getAttribute('data-model') ?? '').toContain('overflow-hidden')
      }
    })
  }
})

describe('U8-B catalog rows that share a long prefix stay distinguishable', () => {
  // Same model as the ModelSelector U8-B case: a row shows VISIBLE_CHARS
  // characters from the side its name keeps (read from its classes).
  const VISIBLE_CHARS = 17
  it('no two rendered rows show the same text (ids sharing "anthropic/claude-opus-")', async () => {
    pointer(false)
    const ids = Array.from({ length: 120 }, (_, i) => `anthropic/claude-opus-4.${i}${i % 2 ? '-fast' : ''}`)
    const shared: ProvidersCatalog = {
      ...catalog,
      providers: catalog.providers.map((entry) => entry.id !== 'openrouter' ? entry : {
        ...entry, models: ids.map((id, i) => ({ ...template, id, name: `Opus ${i}` })),
      }),
    }
    await openPicker({ catalog: shared, defaultModel: { ...defaultModel, model: ids[0] } })
    const items = [...document.querySelectorAll<HTMLElement>('[cmdk-item][data-model]')]
    expect(items.length).toBeGreaterThan(1)
    const shown = items.map((item) => {
      const id = item.getAttribute('data-model') ?? ''
      const name = [...item.querySelectorAll('span')].find((span) => span.title === id)
      expect(name, id).toBeDefined()
      expect(name?.textContent).toBe(id)
      const keepsEnd = name!.classList.contains('[direction:rtl]') && name!.classList.contains('text-left')
      return keepsEnd ? id.slice(-VISIBLE_CHARS) : id.slice(0, VISIBLE_CHARS)
    })
    expect(new Set(shown).size, JSON.stringify(shown.slice(0, 8))).toBe(shown.length)
  })
})

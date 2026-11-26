// Real Settings popup attachment and HTTP/browser edges. No primitive, hook,
// virtualizer or portal stand-in. Expected product outcomes live in the tests.
import { afterEach, expect, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { IntegrationProvidersResponse, ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import { ProfileSection } from '@/components/settings/ProfileSection'
import { catalogG1, integrationsG1, jsonG1, providersG1, renderG1, sectionG1, transportG1 } from './settings-gate-g1'

// not-wire-format: test campaign labels, not gateway data.
export type PopupU43 = 'more' | 'default-model' | 'search-default' | 'search-fallback'
  | 'memory-primary' | 'memory-fallback' | 'memory-add' | 'effort' | 'audit-events' | 'audit-decisions' | 'profile-timezone'
export const POPUPS_U43: PopupU43[] = ['more', 'default-model', 'search-default', 'search-fallback',
  'memory-primary', 'memory-fallback', 'memory-add', 'effort', 'audit-events', 'audit-decisions', 'profile-timezone']

const browserRestoresU43: (() => void)[] = []
afterEach(() => { for (const restore of browserRestoresU43.splice(0)) restore() })

export function pointerU43(coarse: boolean) {
  // jsdom 29 lacks pointer capture. This fixture has no captured pointer, so
  // the browser API truthfully answers false; Radix's real pointer handler runs.
  // No production event handler or Select primitive is replaced.
  if (typeof Element.prototype.hasPointerCapture !== 'function') {
    const previous = Object.getOwnPropertyDescriptor(Element.prototype, 'hasPointerCapture')
    Object.defineProperty(Element.prototype, 'hasPointerCapture', { configurable: true, value: () => false })
    browserRestoresU43.push(() => {
      if (previous) Object.defineProperty(Element.prototype, 'hasPointerCapture', previous)
      else Reflect.deleteProperty(Element.prototype, 'hasPointerCapture')
    })
  }
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: media === '(pointer: coarse)' ? coarse : media === '(pointer: fine)' ? !coarse : false,
    media, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  }))
  expect(window.matchMedia('(pointer: coarse)').matches).toBe(coarse)
  expect(window.matchMedia('(pointer: fine)').matches).toBe(!coarse)
}

export function modelViewportU43() {
  const nativeHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')?.get
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
    // Native virtualizer reads its REAL scroll element's offsetHeight. Only the
    // scroll viewport receives a size. NEVER return a row size or origin here.
    return this.matches('[cmdk-list]') ? 300 : nativeHeight?.call(this) ?? 0
  })
}

// Large synthetic input, not the implementation's virtualization threshold.
export const MODELS_U43 = Array.from({ length: 160 }, (_, index) => `u43-model-${String(index).padStart(3, '0')}`)
export const CATALOG_U43: ProvidersCatalog = { ...catalogG1, providers: [{ ...catalogG1.providers[0],
  models: MODELS_U43.map((id) => ({ ...catalogG1.providers[0].models[0], id, name: id })),
}] }
export function largeModelsU43(t: ReturnType<typeof transportG1>) {
  t.on('GET', '/providers', () => jsonG1([{ ...providersG1[0], models: MODELS_U43 }]))
  t.on('GET', '/providers/catalog', () => jsonG1(CATALOG_U43))
  t.on('GET', '/providers/default-model', () => jsonG1({ provider: 'openrouter', model: MODELS_U43[0], reasoning_effort: 'low', context_window: 128000, window_source: 'catalog' }))
  modelViewportU43()
}

export async function openDefaultU43() {
  await userEvent.click(await screen.findByTestId('default-model-change-btn'))
  const trigger = await screen.findByTestId('default-model-select')
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await userEvent.click(trigger)
  const popup = await screen.findByRole('dialog', { name: 'Select model' })
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  expect(popup.closest('[data-testid="default-model-selector-wrap"]'), 'real portal, not inline content').toBeNull()
  return popup
}

export function observeEmptyU43(root: Element = document.body) {
  const seen: string[] = []
  const observer = new MutationObserver(() => {
    // Observe the connected DOM after each mutation batch, not detached nodes
    // removed in the same commit. React layout effects settle before paint:
    // https://react.dev/reference/react/useLayoutEffect . This is still NOT a
    // browser-paint/video assertion; the independent lane observes visible flash.
    for (const message of root.querySelectorAll('[cmdk-empty]')) {
      if (message.isConnected && message.textContent?.trim()) seen.push(message.textContent.trim())
    }
  })
  observer.observe(root, { subtree: true, childList: true, characterData: true })
  return { seen, stop: () => observer.disconnect() }
}

const SEARCH_U43: IntegrationProvidersResponse = { ...integrationsG1, search: [
  { id: 'brave', kind: 'search', display_name: 'Brave U43', configured: true, requires_key: true, usable: true, active: true },
  { id: 'tavily', kind: 'search', display_name: 'Tavily U43', configured: true, requires_key: true, usable: true, fallback: true },
  { id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo U43', configured: true, requires_key: false, usable: true },
], default_search: 'brave', fallback_search: 'tavily' }

export async function openPopupU43(kind: PopupU43, coarse: boolean) {
  const t = transportG1()
  pointerU43(coarse) // transport installs its default first; never overwrite this input later.
  t.on('GET', '/integrations/providers', () => jsonG1(SEARCH_U43))
  t.on('GET', '/user-context', () => jsonG1({ content: 'U43 popup test account.' }))
  const section = kind.startsWith('memory-') ? 'memory' : kind.startsWith('audit-') ? 'protection' : 'providers'
  const view = kind === 'profile-timezone' ? renderG1(<ProfileSection />) : sectionG1(section)
  await waitFor(() => expect(view.client.isFetching()).toBe(0))
  let popup: HTMLElement
  if (kind === 'more') {
    await userEvent.click(await screen.findByRole('button', { name: 'More actions for OpenRouter' }))
    popup = await screen.findByRole('menu')
  } else if (kind === 'default-model') {
    popup = await openDefaultU43()
  } else if (kind.startsWith('search-')) {
    const isDefault = kind === 'search-default'
    const card = await screen.findByTestId(isDefault ? 'default-search-card' : 'fallback-search-card')
    await userEvent.click(within(card).getByRole('button', { name: 'Change' }))
    await userEvent.click(within(card).getByRole('combobox', { name: isDefault ? 'Default search' : 'Fallback' }))
    popup = await screen.findByRole('dialog', { name: isDefault ? 'Default search' : 'Fallback' })
  } else if (kind.startsWith('memory-')) {
    const id = kind === 'memory-primary' ? 'recap-model-trigger' : kind === 'memory-fallback' ? 'recap-fallback-model-g1-model-b' : 'recap-fallback-add-trigger'
    await userEvent.click(await screen.findByTestId(id))
    popup = await screen.findByRole('dialog', { name: 'Select model' })
  } else if (kind === 'effort') {
    await userEvent.click(await screen.findByTestId('default-model-effort'))
    popup = await screen.findByRole('dialog')
    expect(within(popup).getByRole('radiogroup')).toBeVisible()
  } else if (kind.startsWith('audit-')) {
    await userEvent.click(await screen.findByRole('button', { name: 'View Log' }))
    const audit = await screen.findByRole('dialog', { name: 'Audit Log' })
    await userEvent.click(within(audit).getByRole('combobox', { name: kind === 'audit-events' ? 'Event type filter' : 'Decision filter' }))
    // A searchable select uses a Popover; a short select uses a real listbox portal.
    popup = kind === 'audit-events' ? (await screen.findAllByRole('dialog')).find((dialog) => dialog !== audit)! : await screen.findByRole('listbox')
  } else {
    await userEvent.click(await screen.findByRole('combobox', { name: 'Timezone' }))
    popup = await screen.findByRole('dialog')
  }
  const role = kind === 'more' ? 'menuitem' : kind === 'effort' ? 'radio' : 'option'
  await waitFor(() => expect(within(popup).getAllByRole(role).length, `${kind}: real popup must offer choices`).toBeGreaterThan(0))
  const items = within(popup).getAllByRole(role)
  for (const item of items) expect(item).toBeVisible()
  expect(t.writes(), `${kind}: opening/inspecting is read-only`).toEqual([])
  return { ...view, t, popup, items }
}

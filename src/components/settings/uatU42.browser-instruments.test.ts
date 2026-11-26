// Test the commissioned platform instrument, NOT a product or browser PASS.
import { afterEach, expect, it, vi } from 'vitest'
import { serialBrowserU42 } from '../../../tests/helpers/settings-u42-browser'

let edge: ReturnType<typeof serialBrowserU42> | undefined
afterEach(async () => { await edge?.idle(); edge?.restore(); edge = undefined; vi.unstubAllGlobals() })

it('U4-C2 instrument — two queued Back requests apply to successive indices, never coalesce', async () => {
  edge = serialBrowserU42(true)
  window.history.replaceState({ fixture: 'limits' }, '', '/#/limits')
  window.history.pushState({ fixture: 'gateway' }, '', '/#/gateway')
  window.history.pushState({ fixture: 'about' }, '', '/#/about')
  expect(edge.navigation.currentEntry.index).toBe(2)
  window.history.back()
  window.history.back()
  await edge.idle()
  expect(edge.applied).toEqual([
    { kind: 'back', delta: -1, from: 2, to: 1 },
    { kind: 'back', delta: -1, from: 1, to: 0 },
  ])
  expect(window.location.hash).toBe('#/limits')
  expect(window.history.state).toEqual({ fixture: 'limits' })
  expect(edge.navigation.currentEntry.index).toBe(0)
  expect(window.history.length).toBe(3)
})

it('U4-C2 instrument — Navigation metadata follows Back, Forward, typed entries and forward-list truncation', async () => {
  edge = serialBrowserU42(true)
  window.history.replaceState({ fixture: 'leading' }, '', '/#/leading')
  const leadingKey = edge.navigation.currentEntry.key
  expect(edge.navigation.canGoForward).toBe(false)
  window.history.pushState({ fixture: 'gateway' }, '', '/#/gateway')
  const gatewayKey = edge.navigation.currentEntry.key
  expect(gatewayKey).not.toBe(leadingKey)
  expect(edge.navigation.canGoForward).toBe(false)
  window.history.back(); await edge.idle()
  expect(edge.navigation.currentEntry.key).toBe(leadingKey)
  expect(edge.navigation.canGoForward).toBe(true)
  window.history.forward(); await edge.idle()
  expect(edge.navigation.currentEntry.key).toBe(gatewayKey)
  expect(edge.navigation.canGoForward).toBe(false)
  edge.typeHash('/typed')
  expect(window.history.state, 'address-bar hash entry is state-less').toBeNull()
  expect(edge.navigation.currentEntry.index).toBe(2)
  expect(edge.navigation.canGoForward).toBe(false)
  window.history.back(); await edge.idle()
  expect(edge.navigation.canGoForward).toBe(true)
  window.history.pushState({ fixture: 'replacement branch' }, '', '/#/branch')
  expect(edge.navigation.entries().map(({ index, url }) => ({ index, hash: new URL(url).hash }))).toEqual([
    { index: 0, hash: '#/leading' }, { index: 1, hash: '#/gateway' }, { index: 2, hash: '#/branch' },
  ])
  expect(edge.navigation.canGoForward).toBe(false)
  const key = edge.navigation.currentEntry.key
  window.history.replaceState({ fixture: 'replaced' }, '', '/#/replaced')
  expect(edge.navigation.currentEntry.key, 'replacement preserves the slot key').toBe(key)
  expect(window.history.length).toBe(3)
})

it('U4-C2 instrument — boundaries do not traverse or grow entries, and zero-delta reload requests remain visible', async () => {
  edge = serialBrowserU42(false)
  window.history.replaceState({ fixture: 'only entry' }, '', '/#/only')
  window.history.back(); window.history.forward(); window.history.go(-0)
  await edge.idle()
  expect(Reflect.get(window, 'navigation')).toBeUndefined()
  expect(edge.applied).toEqual([
    { kind: 'back', delta: -1, from: 0, to: 0 },
    { kind: 'forward', delta: 1, from: 0, to: 0 },
    { kind: 'go', delta: -0, from: 0, to: 0 },
  ])
  expect(edge.reloads.filter((delta) => delta === 0)).toHaveLength(1)
  expect(window.location.hash).toBe('#/only')
  expect(window.history.state).toEqual({ fixture: 'only entry' })
  expect(window.history.length).toBe(1)
})

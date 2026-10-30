// PTA-1 / #1452 F1 / D5. Component coverage of the REAL private product
// cache, not tests/chat-history-paging/physical-layout-cache. Only browser
// layout/ResizeObserver edges are supplied; store/reader/hook stay real.
import { useRef } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { queryClient } from '@/lib/queryClient'
import { useHistoryViewport } from './useHistoryViewport'
import { entry, page, pos, SID } from '../../../tests/chat-history-paging/fixtures'

const positions = [pos(1), pos(2), pos(3)]
let widths = 800
let heights = [80, 80, 80]
let reads = [0, 0, 0]

function Probe({ sample }: { sample: number }) {
  void sample // Trigger a React layout sample WITHOUT changing any DOM attribute.
  const ref = useRef<HTMLDivElement>(null)
  const messages = useChatStore(state => state.messages)
  const viewport = useHistoryViewport(ref, messages)
  return <div ref={ref} data-testid="cache-scroller">
    <output data-testid="cache-average">{viewport.average}</output>
    <div data-history-content data-testid="rows-wrapper">
      {messages.map(message => <article key={message.id} data-history-anchor={message.historyPosition} data-history-position={message.historyPosition}>{message.content}</article>)}
    </div>
  </div>
}

beforeEach(async () => {
  widths = 800; heights = [80, 80, 80]; reads = [0, 0, 0]
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useChatPreferencesStore.setState(useChatPreferencesStore.getInitialState(), true)
  useSessionStore.getState().setActiveSession(SID, 'mia')
  // Hold independent layout-event delivery. Cache mutation observation itself
  // is the native jsdom MutationObserver, including synchronous takeRecords.
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  const selected = page([entry(1), entry(2), entry(3)], { rows: 3, info: { limit: 20, min_rows: 1 } })
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify(selected), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  await act(async () => {
    await useChatStore.getState().loadLatestHistory(SID, { limit: 20, min_rows: 1 })
    useChatStore.getState().enterHistoryReading(SID)
  })
  expect(useChatStore.getState().messages.map(message => message.historyPosition), 'real page adapter provides exactly three stable rows').toEqual(positions)

  const computed = globalThis.getComputedStyle
  vi.spyOn(globalThis, 'getComputedStyle').mockImplementation((element, pseudo) => {
    const index = positions.indexOf((element as HTMLElement).dataset.historyPosition ?? '')
    const style = computed(element, pseudo)
    if (index < 0) return style
    reads[index]++
    // D5's observable browser read, not a spy on historyLayoutHeight or cache.
    return new Proxy(style, { get(target, key) {
      if (key === 'height') return `${heights[index]}px`
      if (key === 'boxSizing') return 'border-box'
      return Reflect.get(target, key, target)
    } })
  })
  const rect = HTMLElement.prototype.getBoundingClientRect
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const index = positions.indexOf(this.dataset.historyPosition ?? '')
    if (this.dataset.testid === 'cache-scroller' || this.hasAttribute('data-history-content')) return new DOMRect(0, 0, widths, 600)
    if (index >= 0) return new DOMRect(0, heights.slice(0, index).reduce((sum, h) => sum + h, 0), widths, heights[index])
    return rect.call(this)
  })
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) { return this.dataset.testid === 'cache-scroller' ? 600 : 0 })
})

afterEach(() => {
  cleanup()
  useChatStore.getState().releaseSessionHistory(SID)
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('PTA-1: unchanged rows stay cached; an inner mutation re-reads only its row, outer layout attribute and width re-read all', () => {
  const view = render(<Probe sample={0} />)
  expect(screen.getByTestId('cache-average')).toHaveTextContent(/^80$/)
  expect(reads.every(n => n > 0), 'instrument: initial real viewport sample reads every drawn row').toBe(true)
  reads = [0, 0, 0]
  view.rerender(<Probe sample={1} />)
  expect(reads, 'PTA-2 deterministic guard: unchanged component sample does not re-read cached row heights').toEqual([0, 0, 0])

  // No await between mutations and sample: cache must drain takeRecords even
  // BEFORE native MutationObserver callback delivery.
  act(() => {
    heights[0] = 160
    screen.getByTestId('rows-wrapper').querySelector('article')!.append(document.createTextNode(' changed inside first row'))
    view.rerender(<Probe sample={2} />)
  })
  expect(reads, 'PTA-1: mutation inside first row invalidates ONLY first row').toEqual([1, 0, 0])
  expect(Number(screen.getByTestId('cache-average').textContent), 'D5 arithmetic: (160 + 80 + 80) / 3').toBe(320 / 3)

  reads = [0, 0, 0]
  act(() => {
    heights = [160, 160, 160]
    screen.getByTestId('rows-wrapper').classList.add('synthetic-layout-change')
    view.rerender(<Probe sample={3} />)
  })
  expect(reads, 'PTA-1: layout class attribute OUTSIDE any row invalidates ALL row heights').toEqual([1, 1, 1])
  expect(screen.getByTestId('cache-average')).toHaveTextContent(/^160$/)

  reads = [0, 0, 0]
  heights = [240, 240, 240]
  widths = 1000
  view.rerender(<Probe sample={4} />)
  expect(reads, 'PTA-1: actual scroller width change invalidates ALL row heights').toEqual([1, 1, 1])
  expect(screen.getByTestId('cache-average')).toHaveTextContent(/^240$/)
  reads = [0, 0, 0]
  view.rerender(<Probe sample={5} />)
  expect(reads, 'cache becomes reusable again after the width sample, never always-invalidating').toEqual([0, 0, 0])
})

it('PTA-1: without MutationObserver EVERY unchanged component sample re-reads ALL rows', () => {
  vi.stubGlobal('MutationObserver', undefined)
  const view = render(<Probe sample={0} />)
  expect(screen.getByTestId('cache-average')).toHaveTextContent(/^80$/)
  expect(reads.every(n => n > 0), 'initial sampling still sees real browser geometry without an observer').toBe(true)
  for (const sample of [1, 2]) {
    reads = [0, 0, 0]
    view.rerender(<Probe sample={sample} />)
    expect(reads, `PTA-1: observer absent, sample ${sample} cannot claim any row is unchanged`).toEqual([1, 1, 1])
    expect(screen.getByTestId('cache-average')).toHaveTextContent(/^80$/)
  }
})

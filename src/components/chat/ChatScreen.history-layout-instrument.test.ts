// Browser-instrument controls, not a product GREEN. S11/B98/T83 requires
// independent physical inner geometry; arithmetic below uses fixture inputs.
import { afterEach, expect, it, vi } from 'vitest'
import { installHistoryLayout } from '../../../tests/chat-history-paging/measurement-layout'

let layout: ReturnType<typeof installHistoryLayout> | undefined
const originalRect = HTMLElement.prototype.getBoundingClientRect
const originalHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight')
afterEach(() => {
  layout?.restore()
  layout = undefined
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.body.replaceChildren()
})

function fixture() {
  const viewport = document.createElement('section')
  viewport.setAttribute('aria-label', 'Conversation history')
  const prefix = document.createElement('div'), status = document.createElement('p')
  status.setAttribute('role', 'status'); prefix.append(status)
  const content = document.createElement('div')
  content.setAttribute('data-history-content', '')
  const suffix = document.createElement('div'), alert = document.createElement('p')
  alert.setAttribute('role', 'alert'); suffix.append(alert)
  viewport.append(prefix, content, suffix)
  document.body.append(viewport)
  return { viewport, content }
}
function row(position: string) {
  const element = document.createElement('article')
  element.dataset.historyAnchor = position
  element.dataset.historyPosition = position
  return element
}

it('layout instrument: 500-row geometry avoids live child-collection enumeration', () => {
  layout = installHistoryLayout()
  const { viewport, content } = fixture()
  const rows = Array.from({ length: 500 }, (_, index) => row(String(index)))
  content.append(...rows)
  layout.state.rowHeights.set('0', 40)
  rows[1].hidden = true
  const children = vi.spyOn(content, 'children', 'get')
  // Test the instrument: the spy must see an actual live-collection read.
  void content.children
  expect(children, 'positive control: live collection access is observable').toHaveBeenCalledTimes(1)
  children.mockClear()
  // Before the last row: status32 + first40 + hidden0 + 497*80 = 39832.
  expect(rows[499].getBoundingClientRect().top, 'independent preceding physical heights').toBe(39832)
  expect(viewport.scrollHeight, 'status32 + first40 + hidden0 + 498*80 + alert32').toBe(39944)
  expect(children, 'regression: measuring a row must not enumerate its live sibling collection').not.toHaveBeenCalled()
  viewport.scrollTop = 120
  expect(rows[499].getBoundingClientRect().top, 'scroll changes screen top, not physical heights').toBe(39712)
  layout.state.rowHeights.set('0', 120)
  expect(rows[499].getBoundingClientRect().top, 'input edits are visible immediately, not cached').toBe(39792)
  rows[1].hidden = false
  expect(rows[499].getBoundingClientRect().top, 'unhiding an 80px row restores its physical contribution').toBe(39872)
})

it('layout instrument: virtual translation, controls and spacer remain physical DOM inputs', () => {
  layout = installHistoryLayout({ virtual: true })
  const { viewport, content } = fixture()
  content.style.height = '4000px'
  const translated = document.createElement('div')
  translated.style.transform = 'translateY(1200px)'
  const first = row('first'), target = row('target')
  const nonRow = document.createElement('span')
  translated.append(first, nonRow, target)
  content.append(translated)
  layout.state.rowHeights.set('first', 120)
  viewport.scrollTop = 100
  expect(viewport.scrollHeight, 'status32 + actual spacer4000 + alert32').toBe(4064)
  expect(target.getBoundingClientRect().top, 'status32 + translation1200 + first120 - scroll100').toBe(1252)
  first.remove()
  expect(target.getBoundingClientRect().top, 'DOM removal must immediately drop the physical predecessor').toBe(1132)
  content.before(document.createElement('span'))
  expect(target.getBoundingClientRect().top, 'an unrelated control-less sibling contributes zero').toBe(1132)
})

it('layout instrument: inner parts, paragraphs and delayed tool growth are not row-top aliases', () => {
  layout = installHistoryLayout({ innerParts: true })
  const { viewport, content } = fixture()
  const first = row('first'), target = row('target')
  layout.state.rowHeights.set('first', 40)
  const a = document.createElement('div'), b = document.createElement('div')
  a.dataset.historyPart = 'A'; b.dataset.historyPart = 'B'
  const lead = document.createElement('p'), point = document.createElement('p')
  const tool = document.createElement('div')
  tool.dataset.historyToolRow = 'tool-A'
  a.append(tool); b.append(lead, point); target.append(a, b)
  content.append(first, target)
  layout.state.partHeights.set('A', 240)
  layout.state.partHeights.set('B', 1600)
  layout.state.toolHeights.set('tool-A', 80)
  expect(b.getBoundingClientRect().top, 'status32 + first40 + A240 + tool80').toBe(392)
  expect(point.getBoundingClientRect().top, 'second paragraph is independently 60px inside B').toBe(452)
  layout.state.toolHeights.set('tool-A', 200)
  expect(point.getBoundingClientRect().top, 'delayed tool growth above B moves the physical point by120').toBe(572)
  const inserted = row('inserted')
  layout.state.rowHeights.set('inserted', 20)
  content.insertBefore(inserted, target)
  expect(point.getBoundingClientRect().top, 'new DOM predecessor is not hidden by a cached prefix').toBe(592)
  first.dataset.historyPosition = 'rekeyed'; first.dataset.historyAnchor = 'rekeyed'
  layout.state.rowHeights.set('rekeyed', 100)
  expect(point.getBoundingClientRect().top, 'rekey reads the current physical height, not the old40').toBe(652)
  viewport.scrollTop = 7
  expect(point.getBoundingClientRect().top, 'only the browser scroll offset changes screen coordinates').toBe(645)
})

it('layout cache: unchanged physical snapshots reuse DOM discovery across rectangle reads', () => {
  layout = installHistoryLayout()
  const { viewport, content } = fixture()
  const rows = Array.from({ length: 12 }, (_, index) => row(String(index)))
  content.append(...rows)
  const discovery = vi.spyOn(viewport, 'querySelector')
  const expected = rows.map((_, index) => 32 + index * 80)
  expect(rows.map(element => element.getBoundingClientRect().top), 'independent physical prefix for every row').toEqual(expected)
  expect(rows.map(element => element.getBoundingClientRect().top), 'unchanged browser inputs retain exact geometry').toEqual(expected)
  expect(discovery, 'one content discovery per unchanged physical snapshot, not per rectangle').toHaveBeenCalledTimes(1)
})

it('layout cache: a DOM mutation invalidates synchronously before observer delivery', () => {
  layout = installHistoryLayout()
  const { content } = fixture()
  const first = row('first'), target = row('target')
  content.append(first, target)
  expect(target.getBoundingClientRect().top, 'status32 + first80').toBe(112)
  content.insertBefore(row('inserted'), target)
  // No await: the observer callback has not run. takeRecords must see this.
  expect(target.getBoundingClientRect().top, 'status32 + first80 + inserted80').toBe(192)
  first.hidden = true
  expect(target.getBoundingClientRect().top, 'a hidden predecessor contributes zero immediately').toBe(112)
})

it('layout cache: replacing an input map invalidates measured prefixes immediately', () => {
  layout = installHistoryLayout()
  const { content } = fixture()
  const first = row('first'), target = row('target')
  content.append(first, target)
  layout.state.rowHeights = new Map([['first', 40]])
  expect(target.getBoundingClientRect().top, 'status32 + first40').toBe(72)
  layout.state.rowHeights = new Map([['first', 120]])
  expect(target.getBoundingClientRect().top, 'replacement map supplies first120, not cached40').toBe(152)
})

it('layout cache: external map mutations, deletion and clear cannot leave stale heights', () => {
  layout = installHistoryLayout()
  const { content } = fixture()
  const first = row('first'), target = row('target')
  content.append(first, target)
  const external = new Map([['first', 40]])
  layout.state.rowHeights = external
  expect(target.getBoundingClientRect().top, 'status32 + externally supplied first40').toBe(72)
  external.set('first', 160)
  expect(target.getBoundingClientRect().top, 'same-size external update changes first to160').toBe(192)
  external.delete('first')
  expect(target.getBoundingClientRect().top, 'delete restores the independent default80').toBe(112)
  external.set('first', 40)
  expect(target.getBoundingClientRect().top).toBe(72)
  external.clear()
  expect(target.getBoundingClientRect().top, 'clear cannot retain the old explicit40').toBe(112)
})

it('layout cache: scalar inputs and real transform/spacer attributes remain live', () => {
  layout = installHistoryLayout({ virtual: true })
  const { viewport, content } = fixture()
  const translated = document.createElement('div')
  translated.style.transform = 'translateY(100px)'
  content.style.height = '2000px'
  const first = row('first'), target = row('target')
  translated.append(first, target); content.append(translated)
  expect(target.getBoundingClientRect().top, 'status32 + translation100 + first80').toBe(212)
  layout.state.rowHeight = 40
  expect(target.getBoundingClientRect().top, 'a default-height scalar change is a new physical snapshot').toBe(172)
  translated.style.transform = 'translateY(200px)'
  expect(target.getBoundingClientRect().top, 'the real DOM translation changes by100').toBe(272)
  expect(viewport.scrollHeight).toBe(2064)
  content.style.height = '3000px'
  expect(viewport.scrollHeight, 'new actual spacer3000 plus two32px controls').toBe(3064)
  viewport.scrollTop = 100
  expect(target.getBoundingClientRect().top, 'scroll is read fresh even when physical inputs stay fixed').toBe(172)
})

it('layout instrument: restore returns the original browser metrics between cases', () => {
  layout = installHistoryLayout({ virtual: true, innerParts: true })
  expect(HTMLElement.prototype.getBoundingClientRect, 'installed edge really differs').not.toBe(originalRect)
  layout.restore()
  layout = undefined
  expect(HTMLElement.prototype.getBoundingClientRect, 'no chained rectangle wrapper leaks to the next case').toBe(originalRect)
  expect(Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight'), 'metric descriptor is restored exactly').toEqual(originalHeight)
})

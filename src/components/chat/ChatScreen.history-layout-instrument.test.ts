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

it('layout instrument: restore returns the original browser metrics between cases', () => {
  layout = installHistoryLayout({ virtual: true, innerParts: true })
  expect(HTMLElement.prototype.getBoundingClientRect, 'installed edge really differs').not.toBe(originalRect)
  layout.restore()
  layout = undefined
  expect(HTMLElement.prototype.getBoundingClientRect, 'no chained rectangle wrapper leaks to the next case').toBe(originalRect)
  expect(Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight'), 'metric descriptor is restored exactly').toEqual(originalHeight)
})

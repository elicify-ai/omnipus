/**
 * W1 browser RED: the real CI anchor moved 35.46875 px when loading ended.
 * Oracle: the dispatch and group-chats.md promise the same visible message
 * and offset across history loading. Model ALL preceding children, not just
 * message heights: hint, loader, error, padding and gaps. The component is
 * real; only jsdom's missing browser layout is supplied. Browser acceptance
 * is a separate Playwright component receipt with the real app stylesheet.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import { RoomTranscript } from './RoomTranscript'

// Independent layout fixture: 100px message rows; 32px hint, 19.5px loader,
// 72px error; 16px gaps/padding. A loader adds 35.5px including its gap.
const GAP = 16
const PADDING = 16
const VIEWPORT = 400
let positions: WeakMap<Element, number>

function heightOf(el: Element): number {
  if (el.matches('article')) return 100
  if (el.getAttribute('role') === 'status') return 19.5
  if (el.getAttribute('role') === 'alert') return 72
  if (el.matches('button')) return 32
  return 0
}

function heightOfLog(log: Element): number {
  return 2 * PADDING + [...log.children].reduce((sum, child) => sum + heightOf(child), 0) + Math.max(0, log.children.length - 1) * GAP
}

function contentTop(el: Element): number {
  let offset = PADDING
  for (let previous = el.previousElementSibling; previous; previous = previous.previousElementSibling) offset += heightOf(previous) + GAP
  return offset
}

function rect(top: number, height: number): DOMRect {
  return { top, bottom: top + height, height, y: top, x: 0, left: 0, right: 720, width: 720, toJSON: () => ({}) } as DOMRect
}

function entry(seq: number): RoomEntry {
  return {
    id: `entry-${seq}`, room_id: 'fixture-room', seq,
    client_message_id: `client-${seq}`, author: { kind: 'user' },
    text: `Message ${seq}`, addressees: [], after_pause: false,
    deliveries: [], timestamp: '2026-10-20T10:00:00Z',
  }
}

function anchorTop(seq: number): number {
  return screen.getByText(`Message ${seq}`, { exact: true }).closest('article')!.getBoundingClientRect().top
}

function mountTranscript() {
  const loadOlder = vi.fn()
  // not-wire-format: component fixture state, not an API payload.
  let state: { entries: RoomEntry[]; status: 'idle' | 'loading' | 'error'; failed: boolean; hasOlder: boolean } = {
    entries: Array.from({ length: 15 }, (_, i) => entry(i + 6)),
    status: 'idle', failed: false, hasOlder: true,
  }
  const node = () => (
    <RoomTranscript
      roomId="fixture-room" snapshotReceived entries={state.entries}
      agentName={(id) => id} hasOlder={state.hasOlder}
      history={{ status: state.status, failed: state.failed, loadOlder }}
    />
  )
  const view = render(node())
  const log = screen.getByRole('log', { name: 'Group conversation' })
  log.scrollTop = 0
  fireEvent.scroll(log)
  expect(loadOlder).toHaveBeenCalledExactlyOnceWith()
  return {
    log, loadOlder,
    update(next: Partial<typeof state>) {
      state = { ...state, ...next }
      view.rerender(node())
    },
    rows: state.entries,
  }
}

beforeEach(() => {
  positions = new WeakMap()
  const nativeTop = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop')!
  const nativeHeight = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollHeight')!
  const nativeRect = Element.prototype.getBoundingClientRect
  vi.spyOn(Element.prototype, 'scrollTop', 'get').mockImplementation(function (this: Element) {
    return this.getAttribute('role') === 'log' ? positions.get(this) ?? 0 : nativeTop.get!.call(this)
  })
  vi.spyOn(Element.prototype, 'scrollTop', 'set').mockImplementation(function (this: Element, value: number) {
    if (this.getAttribute('role') !== 'log') { nativeTop.set!.call(this, value); return }
    positions.set(this, Math.max(0, Math.min(value, heightOfLog(this) - VIEWPORT)))
  })
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function (this: Element) {
    return this.getAttribute('role') === 'log' ? heightOfLog(this) : nativeHeight.get!.call(this)
  })
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    if (this.getAttribute('role') === 'log') return rect(0, VIEWPORT)
    const log = this.closest('[role="log"]')
    if (log && this.parentElement === log) return rect(contentTop(this) - log.scrollTop, heightOf(this))
    return nativeRect.call(this)
  })
})

afterEach(() => { vi.restoreAllMocks() })

describe('W1 — account for changing history controls above the anchor', () => {
  it('inserting the loader keeps the visible message at its pre-loading offset', () => {
    const fixture = mountTranscript()
    const before = anchorTop(6)
    fixture.update({ status: 'loading' })

    expect(anchorTop(6)).toBe(before)
    expect(fixture.log.scrollTop).toBe(35.5)
    expect(fixture.loadOlder).toHaveBeenCalledExactlyOnceWith()
  })

  it.each([false, true])('prepend with terminal=%s preserves the immediately preceding message offset when controls disappear', (terminal) => {
    const fixture = mountTranscript()
    fixture.update({ status: 'loading' })
    const before = anchorTop(6)
    fixture.update({
      entries: [...[1, 2, 3, 4, 5].map(entry), ...fixture.rows],
      status: 'idle', hasOlder: !terminal,
    })

    expect(anchorTop(6)).toBe(before)
    expect(fixture.log.scrollTop).toBe(5 * (100 + GAP) - (terminal ? 32 + GAP : 0))
    expect(fixture.loadOlder).toHaveBeenCalledExactlyOnceWith()
  })

  it('reader movement and a live tail survive loader/hint removal without counting tail height', () => {
    const fixture = mountTranscript()
    fixture.update({ status: 'loading' })
    fixture.log.scrollTop = 350
    fireEvent.scroll(fixture.log)
    const before = anchorTop(9)
    fixture.update({ entries: [...fixture.rows, entry(1000)] })
    expect(anchorTop(9)).toBe(before)
    fixture.update({
      entries: [...[1, 2, 3, 4, 5].map(entry), ...fixture.rows, entry(1000)],
      status: 'idle', hasOlder: false,
    })

    expect(anchorTop(9)).toBe(before)
    // Current 350 + 5 prepended rows/gaps - hint/gap - loader/gap.
    expect(fixture.log.scrollTop).toBe(350 + 5 * (100 + GAP) - (32 + GAP) - (19.5 + GAP))
    expect(fixture.loadOlder).toHaveBeenCalledExactlyOnceWith()
  })

  it('an empty terminal page compensates a negative header delta even though first seq does not change', () => {
    const fixture = mountTranscript()
    fixture.update({ status: 'loading' })
    fixture.log.scrollTop = 350
    const before = anchorTop(9)
    fixture.update({ status: 'idle', hasOlder: false })

    expect(anchorTop(9)).toBe(before)
    expect(fixture.log.scrollTop).toBe(350 - (32 + GAP) - (19.5 + GAP))
  })

  it('replacing the loader with the Retry error preserves the current reading position', () => {
    const fixture = mountTranscript()
    fixture.update({ status: 'loading' })
    fixture.log.scrollTop = 350
    const before = anchorTop(9)
    fixture.update({ status: 'error', failed: true })

    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(anchorTop(9)).toBe(before)
    expect(fixture.log.scrollTop).toBe(350 + 72 - 19.5)
  })
})

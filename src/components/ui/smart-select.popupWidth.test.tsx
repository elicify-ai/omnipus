/**
 * smart-select.popupWidth.test.tsx — U6-7: the searchable SmartSelect popup
 * must let every option label be read in full.
 *
 * Oracle (fix-round-u6.md, U6-7): in the Audit Log event-type filter the list
 * was about 84 px wide, so names such as provider.default_model.changed were
 * cut off with no ellipsis. Every option label can be read in full (the popup
 * is wide enough within the viewport, or the label wraps); no sideways page
 * scroll.
 *
 * jsdom has no layout, so the measured width cannot be shown here. What jsdom
 * can show is the contract that produces it: the popup sizes to its longest
 * label (never narrower than the trigger), is capped at the width Radix says is
 * free in the viewport, and a label that still does not fit wraps instead of
 * being clipped. The physical width stays a browser check.
 */
import { beforeAll, describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { SmartSelect } from './smart-select'

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

const EVENT_ITEMS = [
  { value: 'all', label: 'All events' },
  { value: 'exec', label: 'exec' },
  { value: 'file_op', label: 'file_op' },
  { value: 'provider.default_model.changed', label: 'provider.default_model.changed' },
  { value: 'provider.deleted', label: 'provider.deleted' },
  { value: 'security_setting_change', label: 'security_setting_change' },
]

function openEventFilter() {
  render(<SmartSelect value="all" onValueChange={() => {}} items={EVENT_ITEMS} ariaLabel="Event type filter" triggerClassName="w-[150px]" />)
  fireEvent.click(screen.getByRole('combobox', { name: 'Event type filter' }))
  const list = document.querySelector('[cmdk-list]') as HTMLElement
  return list.closest('[data-radix-popper-content-wrapper] > *') as HTMLElement
}

describe('U6-7 searchable SmartSelect popup fits its labels', () => {
  it('sizes to the longest label, never narrower than the trigger, capped at the free viewport width', () => {
    const popup = openEventFilter()
    const classes = popup.className.split(/\s+/)
    expect(classes).toContain('w-max')
    expect(classes).toContain('min-w-[max(8rem,var(--radix-popover-trigger-width))]')
    expect(classes).toContain('max-w-[var(--radix-popover-content-available-width)]')
    expect(classes, 'a fixed trigger-width popup clips long names').not.toContain('w-[var(--radix-popover-trigger-width)]')
  })

  it('a label that still does not fit wraps instead of being clipped', () => {
    openEventFilter()
    for (const item of EVENT_ITEMS) {
      const label = screen.getByText(item.label, { selector: '[cmdk-item] span' })
      expect(label.className, item.label).toContain('[overflow-wrap:anywhere]')
      expect(label.className, item.label).not.toMatch(/truncate|line-clamp/)
    }
  })
})

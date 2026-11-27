// U8-A / FR-070: bounded CSS arithmetic, NEVER painted boxes or hit-testing.
// Glyph=13 is the U9 measured environmental input (tester-report-u9.md §4),
// not a product minimum. Unknown active CSS yields null, never a made-up size.
import { expect } from 'vitest'
import type { AtRule, Document, Root, Rule } from 'postcss'
import { pixelsU43 } from './settings-u43-css'

export function fineBeforeU47(css: Root, target: Element) {
  let found = false
  css.walkRules((rule) => {
    if (!/::?before\b/.test(rule.selector)) return
    // Preserve the original fine guard's strength: even a SMALL added region
    // changes fine-pointer layout. Preflight resets do not draw a region.
    if (!rule.nodes.some((node) => node.type === 'decl' && ['position', 'pointer-events'].includes(node.prop))) return
    const selectors = rule.selector.split(',').map((part) => part.replace(/::?before\b/g, '').trim())
    if (!selectors.some((selector) => target.matches(selector || '*'))) return
    let coarseOnly = false
    for (let parent: Root | Rule | AtRule | Document | undefined = rule.parent; parent; parent = parent.parent) {
      if (parent.type !== 'atrule') continue
      if (parent.name === 'media' && parent.params === '(pointer: coarse)') coarseOnly = true
      else if (parent.name === 'media' || parent.name === 'supports') { found = true; return }
    }
    if (!coarseOnly) found = true
  })
  return found
}

export function radioBoxU47(css: Root, target: Element, glyph = 13) {
  const hit: Record<string, string> = {}
  css.walkRules((rule) => {
    if (!rule.selector.includes('[data-radio-target]') || !/::?before\b/.test(rule.selector)) return
    const selectors = rule.selector.split(',').map((part) => part.replace(/::?before\b/g, '').trim())
    if (selectors.some((selector) => target.matches(selector))) {
      rule.walkDecls((decl) => { hit[decl.prop] = decl.value })
    }
  })
  const length = (value: string | undefined, reference: number) => value === undefined ? null
    : pixelsU43(css, value.replace(/(-?[\d.]+)%/g, (_, n: string) => `${Number(n) * reference / 100}px`))
  const width = length(hit.width, glyph), height = length(hit.height, glyph)
  if (width === null || height === null) return null
  // CSS translate percentages are relative to the pseudo box; left/top to its
  // containing wrapper. Equivalent px/%/max spellings are intentionally legal.
  const transform = hit.transform?.trim() ?? 'none'
  const translate = /^translate\(\s*([^,]+),\s*([^,]+)\)$/.exec(transform)
  const dx = transform === 'none' ? 0 : length(translate?.[1], width)
  const dy = transform === 'none' ? 0 : length(translate?.[2], height)
  const x = length(hit.left, glyph), y = length(hit.top, glyph)
  if (x === null || y === null || dx === null || dy === null) return null
  const left = x + dx, top = y + dy
  return { width, height, left, top, right: left + width, bottom: top + height,
    centreX: left + width / 2, centreY: top + height / 2 }
}

// Read the element's OWN class and token value, not a 44px height fallback.
// Bounded bracketed lengths and literal zero only; unsupported inputs fail.
export function classLengthU47(css: Root, element: Element, prefixes: string[], absent: number | null = 0) {
  for (const prefix of prefixes) {
    const candidate = [...element.classList].find((name) => name.startsWith(prefix))
    if (!candidate) continue
    const suffix = candidate.slice(prefix.length)
    if (suffix === '0') return 0
    const value = /^\[(.+)\]$/.exec(suffix)?.[1]
    return value === undefined ? null : pixelsU43(css, value)
  }
  return absent
}

export function radioReachU47(css: Root, target: Element, card: Element, glyph = 13) {
  const border = card.classList.contains('border') ? 1 : 0 // shipped hairline class
  const padding = classLengthU47(css, card, ['pt-', 'py-', 'p-'])
  const margin = classLengthU47(css, target, ['mt-', 'my-', 'm-'])
  const gap = classLengthU47(css, card.parentElement!, ['gap-y-', 'gap-', 'space-y-'], null)
  const minimumHeight = classLengthU47(css, card, ['pointer-coarse:min-h-', 'min-h-'], null)
  const offset = padding === null || margin === null ? null : border + padding + margin
  const box = radioBoxU47(css, target, glyph)
  return { box, minimumHeight, gap, offset,
    top: box === null || offset === null ? null : offset + box.top,
    bottom: box === null || offset === null ? null : offset + box.bottom }
}

export function expectRadioReachU47(css: Root, target: Element, card: Element, name: string) {
  const reach = radioReachU47(css, target, card)
  const details = `${name}/U8-A: ${JSON.stringify(reach)}`
  expect(reach.box, `${details}; size, offsets and translation must be readable`).not.toBeNull()
  expect(reach.minimumHeight, `${details}; derive the card minimum from its own class`).not.toBeNull()
  expect(reach.gap, `${details}; derive the spacing between option cards`).not.toBeNull()
  expect(reach.offset, `${details}; derive border/padding/wrapper margin`).not.toBeNull()
  const box = reach.box!
  expect(box.width, `${details}; FR-070 tap width >=44`).toBeGreaterThanOrEqual(44)
  expect(box.height, `${details}; FR-070 tap height >=44`).toBeGreaterThanOrEqual(44)
  expect(box.width, `${details}; FR-070 no tap box wider than the 390px viewport`).toBeLessThanOrEqual(390)
  // U8-A/commission: compare resulting centres, not literal CSS spelling.
  expect(box.centreX, `${details}; tap box horizontally centred on the 13px glyph`).toBe(13 / 2)
  expect(box.centreY, `${details}; tap box vertically centred on the 13px glyph`).toBe(13 / 2)
  expect(reach.top, `${details}; known upper reach`).not.toBeNull()
  expect(reach.bottom, `${details}; known lower reach`).not.toBeNull()
  expect(reach.top!, `${details}; no reach into the previous card above the gap`).toBeGreaterThan(-reach.gap!)
  // Preserve Connect's existing stronger lower bound: inside the card itself,
  // hence strictly before the next option even at the minimum card height.
  expect(reach.bottom!, `${details}; no reach below the card's own minimum`).toBeLessThanOrEqual(reach.minimumHeight!)
}

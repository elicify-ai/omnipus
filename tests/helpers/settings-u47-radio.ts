// U8-A / FR-070: bounded CSS arithmetic, NEVER painted boxes or hit-testing.
// Glyph=13 is the U9 measured environmental input (tester-report-u9.md §4),
// not a product minimum. In radioBoxU47's matched ::before declaration model,
// unknown properties/lengths, non-declaration children or multiple box rules
// yield null with an optional per-call reason, never a made-up size. Neutral
// universal box resets are excluded; this is not a full cascade.
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

export function radioBoxU47(css: Root, target: Element, glyph = 13, diagnostics: string[] = []) {
  const supported = ['content', 'position', 'left', 'top', 'width', 'height', 'transform', 'pointer-events']
  const neutralReset: Record<string, string> = { 'box-sizing': 'border-box', margin: '0', padding: '0', border: '0 solid' }
  const hit: Record<string, string> = {}
  const refuse = (reason: string) => { diagnostics.push(reason); return null }
  let boxRules = 0, unreadable = false, boxSelector = ''
  css.walkRules((rule) => {
    if (unreadable) return // Do not attempt to match descendants of a refused nested rule.
    const selectors = rule.selector.split(',').filter((part) => /::?before\b/.test(part))
      .map((part) => part.replace(/::?before\b/g, '').trim() || '*')
    if (!selectors.some((selector) => target.matches(selector))) return
    // Check before the empty-declaration shortcut: nesting is never invisible.
    const child = rule.nodes.find((node) => node.type !== 'decl')
    if (child) {
      const description = child.type === 'atrule' ? `@${child.name} ${child.params}`
        : child.type === 'rule' ? `rule ${child.selector}` : child.type
      refuse(`rule ${rule.selector}: non-declaration child ${description}`)
      unreadable = true
      return
    }
    const declarations = rule.nodes.filter((node) => node.type === 'decl')
    if (declarations.length === 0) return
    // Only universal, exactly neutral resets are outside the tap-box model.
    // A targeted reset or an added non-neutral/unknown declaration is NOT exempt.
    if (selectors.every((selector) => selector === '*')
      && declarations.every((decl) => neutralReset[decl.prop] === decl.value.trim())) return
    const unsupported = declarations.find((decl) => !supported.includes(decl.prop))
    if (unsupported) {
      refuse(`rule ${rule.selector}: unsupported property ${unsupported.prop}: ${unsupported.value}`)
      unreadable = true
      return
    }
    if (++boxRules > 1) {
      refuse(`multiple matching box rules: ${boxSelector} and ${rule.selector}`)
      unreadable = true
      return
    }
    boxSelector = rule.selector
    for (const decl of declarations) hit[decl.prop] = decl.value
  })
  // Refuse cascade ambiguity instead of letting source order invent a box.
  if (unreadable) return null
  if (boxRules === 0) return refuse('no matching ::before box rule')
  const length = (property: string, value: string | undefined, reference: number) => {
    const result = value === undefined ? null
      : pixelsU43(css, value.replace(/(-?[\d.]+)%/g, (_, n: string) => `${Number(n) * reference / 100}px`))
    if (result === null) refuse(`rule ${boxSelector}: unreadable ${property}: ${value ?? '(missing)'}`)
    return result
  }
  const width = length('width', hit.width, glyph), height = length('height', hit.height, glyph)
  if (width === null || height === null) return null
  // CSS translate percentages are relative to the pseudo box; left/top to its
  // containing wrapper. Equivalent px/%/max spellings are intentionally legal.
  const transform = hit.transform?.trim() ?? 'none'
  const translate = /^translate\(\s*([^,]+),\s*([^,]+)\)$/.exec(transform)
  if (transform !== 'none' && !translate) return refuse(`rule ${boxSelector}: unreadable transform: ${transform}`)
  const dx = transform === 'none' ? 0 : length('transform translate-x', translate?.[1], width)
  const dy = transform === 'none' ? 0 : length('transform translate-y', translate?.[2], height)
  const x = length('left', hit.left, glyph), y = length('top', hit.top, glyph)
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
  const unreadable: string[] = []
  const box = radioBoxU47(css, target, glyph, unreadable)
  return { box, unreadable, minimumHeight, gap, offset,
    top: box === null || offset === null ? null : offset + box.top,
    bottom: box === null || offset === null ? null : offset + box.bottom }
}

export function expectRadioReachU47(css: Root, target: Element, card: Element, name: string) {
  const reach = radioReachU47(css, target, card)
  const details = `${name}/U8-A: ${JSON.stringify(reach)}`
  const cause = reach.unreadable.join('; ')
  expect(reach.box, `${details}; size, offsets and translation must be readable${cause ? `; ${cause}` : ''}`).not.toBeNull()
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

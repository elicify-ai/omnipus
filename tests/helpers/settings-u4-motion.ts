// FR-075: DOM + compiled shipped CSS contracts, never computed frame/rotation measurements.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { compile } from '@tailwindcss/node'
import { parse, type Root, type Rule, type Declaration, type AtRule } from 'postcss'

const entry = resolve('src/styles/globals.css')
let compiler: ReturnType<typeof compile> | undefined
export async function motionCssU4(elements: Element[]) {
  compiler ??= compile(readFileSync(entry, 'utf8'), { base: dirname(entry), onDependency() {} })
  // Include actual candidates, including arbitrary-value animate-[...]. A closed
  // animate-spin/animate-pulse allow-list is exactly CHECK's R5/R6 blind spot.
  const candidates = [...new Set(elements.flatMap((element) => [...element.classList]))]
  return parse((await compiler).build(candidates))
}

export function movingElementsU4(root: ParentNode) {
  return [...root.querySelectorAll('[class], [data-settings-row-target], [style]')].filter((element) =>
    [...element.classList].some((name) => /(?:^|:)animate-/.test(name) || name === 'skeleton-shimmer')
    || element.hasAttribute('data-settings-row-target')
    || /(?:^|;)\s*animation(?:-name)?\s*:/.test(element.getAttribute('style') ?? ''))
}

// Resolve nested Tailwind v4 selectors and media guards from the declaration's
// ancestors. A motion-safe:animate-none is not a reduced-motion stop.
function contextU4(declaration: Declaration, reduce: boolean) {
  const rules: Rule[] = []
  let enabled = true
  let layer = 4 // unlayered normal rules outrank named layers
  let ancestor = declaration.parent
  while (ancestor && ancestor.type !== 'root') {
    if (ancestor.type === 'rule') rules.unshift(ancestor)
    if (ancestor.type === 'atrule') {
      const at = ancestor as AtRule
      if (/keyframes$/.test(at.name)) enabled = false
      if (at.name === 'media') {
        if (/prefers-reduced-motion:\s*reduce\b/.test(at.params) && !reduce) enabled = false
        if (/prefers-reduced-motion:\s*no-preference/.test(at.params) && reduce) enabled = false
      }
      if (at.name === 'layer') layer = ['theme', 'base', 'components', 'utilities'].indexOf(at.params)
    }
    ancestor = ancestor.parent
  }
  let selectors = ['']
  for (const rule of rules) {
    const parts = rule.selector.split(',').map((part) => part.trim())
    selectors = selectors.flatMap((parent) => parts.map((part) => part.includes('&') ? part.replaceAll('&', parent) : parent ? `${parent} ${part}` : part))
  }
  return { selectors, enabled, layer }
}

function expandedU4(value: string, tokens: Map<string, string>, seen = new Set<string>()): string {
  return value.replace(/var\((--[\w-]+)\)/g, (raw, key: string) => {
    if (seen.has(key) || !tokens.has(key)) return raw
    return expandedU4(tokens.get(key)!, tokens, new Set([...seen, key]))
  })
}

export function animationU4(css: Root, target: Element, reduce = true) {
  const tokens = new Map<string, string>()
  css.walkDecls((declaration) => { if (declaration.prop.startsWith('--')) tokens.set(declaration.prop, declaration.value) })
  const candidates: { value: string; priority: number; order: number }[] = []
  let order = 0
  css.walkDecls((declaration) => {
    if (!['animation', 'animation-name'].includes(declaration.prop)) return
    const { selectors, enabled, layer } = contextU4(declaration, reduce)
    if (!enabled) return
    const matching = selectors.filter((selector) => !/::(?:before|after)/.test(selector) && target.matches(selector))
    if (matching.length === 0) return
    // Only animation declarations are compared. Respect !important and layers;
    // the class/attribute selectors shipped for these controls share specificity.
    const specificity = Math.max(...matching.map((selector) => (selector.match(/(?<!\\)[.[:#]/g) ?? []).length))
    const priority = (declaration.important ? 10000 + (4 - layer) * 100 : layer * 100) + specificity
    candidates.push({ value: expandedU4(declaration.value, tokens).trim(), priority, order: order++ })
  })
  const inline = (target as HTMLElement | SVGElement).style
  for (const prop of ['animation', 'animation-name']) {
    const value = inline?.getPropertyValue(prop)
    if (value) candidates.push({ value: expandedU4(value, tokens).trim(),
      priority: inline.getPropertyPriority(prop) === 'important' ? 20000 : 1000, order: order++ })
  }
  const winner = candidates.sort((a, b) => a.priority - b.priority || a.order - b.order).at(-1)
  return winner?.value ?? 'none'
}

export function isStaticU4(value: string) {
  return value.split(',').every((part) => /^none(?:\s|$)/.test(part.trim()))
}

export async function noMotionU4(target: Element) {
  return isStaticU4(animationU4(await motionCssU4([target]), target))
}

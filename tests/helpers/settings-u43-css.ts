// Bounded CSS contracts, not a layout engine. No DOM rectangles are manufactured.
// U6 spec: 390px coarse, 44px minimum. Page root is the normal 14px fixture;
// media-query rems use the initial 16px font, not that page root.
import type { AtRule, Declaration, Root, Rule } from 'postcss'
import type { CssEnvironmentU5 } from './settings-u5-css'
export { motionCssU4 as compiledCssU43 } from './settings-u4-motion'

function mediaU43(text: string, env: CssEnvironmentU5) {
  return text.split(/\s+and\s+/).every((term) => {
    const pointer = /^\(\s*pointer:\s*(coarse|fine)\s*\)$/.exec(term)
    if (pointer) return pointer[1] === env.pointer
    const range = /^\(\s*width\s*(>=|<=|>|<)\s*([\d.]+)(px|rem)\s*\)$/.exec(term)
    if (range) {
      const limit = Number(range[2]) * (range[3] === 'rem' ? 16 : 1)
      if (range[1] === '>=') return env.width >= limit
      if (range[1] === '<=') return env.width <= limit
      return range[1] === '>' ? env.width > limit : env.width < limit
    }
    return false // Unsupported guards cannot certify the contract.
  })
}

function contextU43(declaration: Declaration, env: CssEnvironmentU5) {
  const rules: Rule[] = []
  let enabled = true, layer = 4
  let parent = declaration.parent
  while (parent && parent.type !== 'root') {
    if (parent.type === 'rule') rules.unshift(parent)
    if (parent.type === 'atrule') {
      const at = parent as AtRule
      if (/keyframes$/.test(at.name) || at.name === 'supports') enabled = false
      if (at.name === 'media') enabled &&= mediaU43(at.params, env)
      if (at.name === 'layer') layer = ['theme', 'base', 'components', 'utilities'].indexOf(at.params)
    }
    parent = parent.parent
  }
  let selectors = ['']
  for (const rule of rules) {
    selectors = selectors.flatMap((ancestor) => splitU43(rule.selector, ',').map((part) =>
      part.includes('&') ? part.trim().replaceAll('&', ancestor) : ancestor ? `${ancestor} ${part.trim()}` : part.trim()))
  }
  return { enabled: enabled && rules.length > 0, selectors, layer }
}

const PROPERTIES = new Set(['display', 'height', 'min-height', 'width', 'min-width', 'flex-shrink', 'flex-direction',
  'margin-top', 'margin-block-start', 'font-size', 'line-height', 'padding-block', 'padding-top', 'padding-bottom',
  'position', 'content', 'pointer-events', 'top', 'transform', 'translate'])
export function declarationsU43(css: Root, target: Element, env: CssEnvironmentU5, pseudo = '') {
  const values = new Map<string, { value: string; priority: number }>()
  css.walkDecls((declaration) => {
    if (!PROPERTIES.has(declaration.prop)) return
    const { enabled, selectors, layer } = contextU43(declaration, env)
    if (!enabled) return
    const matches = selectors.filter((selector) => {
      if (/:host\b|::-(?:webkit|moz)/.test(selector)) return false
      if ((/::([\w-]+)/.exec(selector)?.[1] ?? '') !== pseudo) return false
      return target.matches(selector.replace(/::(?:before|after)\b/g, ''))
    })
    if (!matches.length) return
    const specificity = Math.max(...matches.map((selector) => (selector.match(/(?<!\\)[.[:#]/g) ?? []).length))
    const priority = (declaration.important ? 10000 + (4 - layer) * 100 : layer * 100) + specificity
    if (priority >= (values.get(declaration.prop)?.priority ?? -1)) values.set(declaration.prop, { value: declaration.value, priority })
  })
  if (!pseudo && target instanceof HTMLElement) {
    for (const prop of PROPERTIES) {
      const inline = target.style.getPropertyValue(prop)
      if (inline) values.set(prop, { value: inline, priority: 100000 })
    }
  }
  return Object.fromEntries([...values].map(([key, entry]) => [key, entry.value])) as Record<string, string | undefined>
}

function splitU43(text: string, delimiter: string) {
  const parts: string[] = []
  let depth = 0, from = 0
  for (let index = 0; index < text.length; index++) {
    if (text[index] === '(') depth++
    if (text[index] === ')') depth--
    if (depth === 0 && text[index] === delimiter) { parts.push(text.slice(from, index)); from = index + 1 }
  }
  parts.push(text.slice(from))
  return parts
}

export function pixelsU43(css: Root, source: string | undefined, root = 14): number | null {
  if (source === undefined) return null
  const tokens = new Map<string, string>()
  css.walkDecls((declaration) => {
    // Token definitions only. A random child's --tw-* cannot replace root data.
    if (declaration.prop.startsWith('--') && declaration.parent?.type === 'rule'
      && /:root|:host/.test(declaration.parent.selector)) tokens.set(declaration.prop, declaration.value)
  })
  const evaluate = (raw: string, seen = new Set<string>()): number | null => {
    const text = raw.trim()
    const variable = /^var\((--[\w-]+)\)$/.exec(text)
    if (variable) {
      if (seen.has(variable[1]) || !tokens.has(variable[1])) return null
      return evaluate(tokens.get(variable[1])!, new Set([...seen, variable[1]]))
    }
    const literal = /^(-?[\d.]+)(px|rem)?$/.exec(text)
    if (literal) return Number(literal[1]) * (literal[2] === 'rem' ? root : 1)
    const fn = /^(calc|max|min|clamp)\((.*)\)$/.exec(text)
    if (fn && fn[1] !== 'calc') {
      const values = splitU43(fn[2], ',').map((part) => evaluate(part, seen))
      if (values.some((value) => value === null)) return null
      const numbers = values as number[]
      if (fn[1] === 'max') return Math.max(...numbers)
      if (fn[1] === 'min') return Math.min(...numbers)
      return numbers.length === 3 ? Math.max(numbers[0], Math.min(numbers[1], numbers[2])) : null
    }
    const expression = fn?.[1] === 'calc' ? fn[2] : text
    for (const op of ['+', '*', '/']) {
      const parts = splitU43(expression, op)
      if (parts.length < 2) continue
      const values = parts.map((part) => evaluate(part, seen))
      if (values.some((value) => value === null)) return null
      const numbers = values as number[]
      if (op === '+') return numbers.reduce((a, b) => a + b)
      if (op === '*') return numbers.reduce((a, b) => a * b)
      return numbers.slice(1).some((value) => value === 0) ? null : numbers.slice(1).reduce((a, b) => a / b, numbers[0])
    }
    return null // %, auto, inherited sizes and unsupported arithmetic remain unknown.
  }
  return evaluate(source)
}

export function heightU43(css: Root, target: Element, env: CssEnvironmentU5) {
  const values = declarationsU43(css, target, env)
  const height = pixelsU43(css, values.height), minimum = pixelsU43(css, values['min-height'])
  return { height, minimum, guarantee: height === null && minimum === null ? null : Math.max(height ?? 0, minimum ?? 0) }
}

export function fixedWidthU43(css: Root, target: HTMLElement, env: CssEnvironmentU5) {
  const own = declarationsU43(css, target, env)
  const parent = target.parentElement ? declarationsU43(css, target.parentElement, env) : {}
  const width = pixelsU43(css, own.width), minimum = pixelsU43(css, own['min-width'])
  const rowFlex = ['flex', 'inline-flex'].includes(parent.display ?? '') && !['column', 'column-reverse'].includes(parent['flex-direction'] ?? '')
  const shrinkable = rowFlex && own['flex-shrink'] !== '0'
  const guaranteedMinimum = minimum !== null && minimum >= 44
  const pass = guaranteedMinimum || (width !== null && width >= 44 && !shrinkable)
  return { width, minimum, rowFlex, shrinkable, pass,
    blocksRowCredit: shrinkable || (width !== null && width < 44) }
}

export function fineSignatureU43(css: Root, target: Element) {
  const values = declarationsU43(css, target, { width: 390, pointer: 'fine' })
  // Characterization projection used ONLY for U6's pre/post preservation control.
  return Object.fromEntries(['height', 'min-height', 'padding-block', 'padding-top', 'padding-bottom', 'font-size', 'line-height']
    .map((prop) => [prop, values[prop] ?? null]))
}

export function centeredLinkClearanceU43(css: Root, link: HTMLElement, env: CssEnvironmentU5) {
  const paragraph = link.parentElement
  if (!paragraph) throw new Error('Inline link has no paragraph')
  const own = declarationsU43(css, link, env), line = declarationsU43(css, paragraph, env)
  const hit = declarationsU43(css, link, env, 'before')
  const centered = own.position === 'relative' && hit.position === 'absolute'
    && hit.top === '50%' && hit.transform === 'translate(-50%, -50%)' && hit['pointer-events'] === 'auto'
  const margin = pixelsU43(css, line['margin-top'] ?? line['margin-block-start'])
  const font = pixelsU43(css, line['font-size'])
  // max(100%, Xpx) guarantees Xpx, but does NOT measure the inline box.
  const floor = /^max\(100%,\s*(.+)\)$/.exec(hit.height ?? '')
  const minimum = pixelsU43(css, floor?.[1] ?? hit.height)
  const clearance = centered && margin !== null && font !== null && minimum !== null
    ? margin - Math.max(0, (minimum - font) / 2) : null
  // Explicit centered-em model only. Fonts, wrapping and painted hit testing are UAT.
  return { centered, margin, font, minimum, clearance }
}

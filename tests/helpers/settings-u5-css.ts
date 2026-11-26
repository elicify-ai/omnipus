// U5 structural CSS contracts only: never a substitute for browser layout/hit testing.
import type { AtRule, Declaration, Root, Rule } from 'postcss'
import { motionCssU4 } from './settings-u4-motion'
import { atLeastTouchU2 } from './settings-u2-css'

export const compiledCssU5 = motionCssU4
// not-wire-format: CSS test environment, not a product carrier.
export interface CssEnvironmentU5 { width: number; pointer: 'coarse' | 'fine' }

function mediaU5(params: string, env: CssEnvironmentU5) {
  // CSS media-query rem units use the initial font, not a page's 14px root.
  // This fixture declares the normal 16px initial font; no rem-based touch oracle.
  const px = (value: string, unit: string) => Number(value) * (unit === 'rem' ? 16 : 1)
  const terms = params.split(/\s+and\s+/)
  return terms.every((term) => {
    const pointer = /^\(\s*pointer:\s*(coarse|fine)\s*\)$/.exec(term)
    if (pointer) return env.pointer === pointer[1]
    const range = /^\(\s*width\s*(>=|<=|>|<)\s*([\d.]+)(px|rem)\s*\)$/.exec(term)
    if (range) {
      const limit = px(range[2], range[3])
      if (range[1] === '>=') return env.width >= limit
      if (range[1] === '<=') return env.width <= limit
      return range[1] === '>' ? env.width > limit : env.width < limit
    }
    const legacy = /^\(\s*(min|max)-width:\s*([\d.]+)(px|rem)\s*\)$/.exec(term)
    if (legacy) return legacy[1] === 'min' ? env.width >= px(legacy[2], legacy[3]) : env.width <= px(legacy[2], legacy[3])
    return false // unsupported guards cannot silently certify a mobile/coarse contract
  })
}

function contextU5(declaration: Declaration, env: CssEnvironmentU5) {
  const rules: Rule[] = []
  let enabled = true, coarse = false, layer = 4
  let parent = declaration.parent
  while (parent && parent.type !== 'root') {
    if (parent.type === 'rule') rules.unshift(parent)
    if (parent.type === 'atrule') {
      const at = parent as AtRule
      if (/keyframes$/.test(at.name)) enabled = false
      if (at.name === 'media') {
        enabled &&= mediaU5(at.params, env)
        coarse ||= /pointer:\s*coarse/.test(at.params)
      }
      if (at.name === 'layer') layer = ['theme', 'base', 'components', 'utilities'].indexOf(at.params)
    }
    parent = parent.parent
  }
  if (!rules.length) enabled = false // @property descriptors are not element declarations
  let selectors = ['']
  for (const rule of rules) {
    selectors = selectors.flatMap((ancestor) => rule.selector.split(',').map((part) =>
      part.includes('&') ? part.trim().replaceAll('&', ancestor) : ancestor ? `${ancestor} ${part.trim()}` : part.trim()))
  }
  return { selectors, enabled, coarse, layer }
}

// Deliberately bounded to the class/attribute rules these contracts exercise,
// with important/layer/source ordering. This is not a complete browser cascade.
function declarationsU5(css: Root, target: Element, env: CssEnvironmentU5, pseudo = '') {
  const values = new Map<string, { value: string; coarse: boolean; priority: number }>()
  css.walkDecls((declaration) => {
    if (!['display', 'flex-direction', 'flex-wrap', 'grid-template-columns', 'height', 'min-height', 'width', 'min-width', 'position', 'content', 'pointer-events', '--tw-content'].includes(declaration.prop)) return
    const { selectors, enabled, coarse, layer } = contextU5(declaration, env)
    if (!enabled) return
    const matching = selectors.filter((selector) => {
      const suffix = /::([\w-]+)/.exec(selector)
      if (/:host\b/.test(selector) || (suffix?.[1] ?? '') !== pseudo) return false
      return target.matches(selector.replace(/::(?:before|after)\b/g, ''))
    })
    if (!matching.length) return
    const specificity = Math.max(...matching.map((selector) => (selector.match(/(?<!\\)[.[:#]/g) ?? []).length))
    const priority = (declaration.important ? 10000 + (4 - layer) * 100 : layer * 100) + specificity
    if (priority >= (values.get(declaration.prop)?.priority ?? -1)) {
      values.set(declaration.prop, { value: declaration.value, coarse, priority })
    }
  })
  return values
}

export function mobileFlowU5(css: Root, target: Element, env: CssEnvironmentU5) {
  const values = declarationsU5(css, target, env)
  const display = values.get('display')?.value
  const direction = values.get('flex-direction')?.value ?? 'row'
  const wrap = values.get('flex-wrap')?.value ?? 'nowrap'
  const columns = values.get('grid-template-columns')?.value ?? ''
  const stacked = display === 'flex' && ['column', 'column-reverse'].includes(direction)
  const wrapped = display === 'flex' && ['wrap', 'wrap-reverse'].includes(wrap)
  const singleColumn = display === 'grid' && /^repeat\(1,\s*minmax\(0,\s*1fr\)\)$/.test(columns)
  return { pass: stacked || wrapped || singleColumn, display, direction, wrap, columns }
}

export function inlineTouchU5(css: Root, target: HTMLElement) {
  // FR-070 / SC-009: independent minimum 44px. U5 requires the coarse form
  // and touch token, not an unscoped mouse rule or min-height on an inline A.
  const env: CssEnvironmentU5 = { width: 390, pointer: 'coarse' }
  const normal = declarationsU5(css, target, env)
  const tokens = new Map<string, string>()
  css.walkDecls((declaration) => { if (declaration.prop.startsWith('--')) tokens.set(declaration.prop, declaration.value) })
  const usesTouchToken = (value: string, seen = new Set<string>()): boolean =>
    [...value.matchAll(/var\((--[\w-]+)\)/g)].some(([, key]) => key === '--target-touch-minimum'
      || (!seen.has(key) && tokens.has(key) && usesTouchToken(tokens.get(key)!, new Set([...seen, key]))))
  const tokenSized = (values: ReturnType<typeof declarationsU5>, props: string[]) => props.some((prop) => {
    const declaration = values.get(prop)
    return declaration?.coarse === true && usesTouchToken(declaration.value)
      && atLeastTouchU2(declaration.value, css)
  })
  const box = ['inline-flex', 'inline-block', 'flex', 'block', 'grid', 'inline-grid'].includes(normal.get('display')?.value ?? '')
  const height = tokenSized(normal, ['height', 'min-height'])
  const width = tokenSized(normal, ['width', 'min-width'])
  const anchored = ['relative', 'absolute', 'fixed', 'sticky'].includes(normal.get('position')?.value ?? '')
  const areas = ['before', 'after'].map((kind) => {
    const values = declarationsU5(css, target, env, kind)
    const content = values.get('content')?.value
    const emptyContent = ["''", '""'].includes(content === 'var(--tw-content)' ? values.get('--tw-content')?.value ?? '' : content ?? '')
    const pass = anchored && emptyContent && values.get('position')?.value === 'absolute'
      && values.get('pointer-events')?.value === 'auto'
      && tokenSized(values, ['width', 'min-width']) && tokenSized(values, ['height', 'min-height'])
    return { kind, pass, anchored, emptyContent, width: tokenSized(values, ['width', 'min-width']),
      height: tokenSized(values, ['height', 'min-height']),
      minimumWidth: atLeastTouchU2(values.get('width')?.value ?? '', css),
      minimumHeight: atLeastTouchU2(values.get('height')?.value ?? '', css), declarations: Object.fromEntries(values) }
  })
  const pseudo = areas.some((area) => area.pass)
  return { pass: (box && height && width) || pseudo, box, height, width, pseudo, areas }
}

export function commonFooterU5(actions: HTMLElement[]) {
  if (actions.length < 2) throw new Error('Footer instrument needs at least two actual actions')
  let ancestor = actions[0].parentElement
  while (ancestor && !actions.every((action) => ancestor!.contains(action))) ancestor = ancestor.parentElement
  if (!ancestor || ancestor === document.body) throw new Error('Actual footer actions have no bounded common container')
  return ancestor
}

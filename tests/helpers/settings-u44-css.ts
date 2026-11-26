// Structural CSS only, not physical boxes. Oracles live in r44-plan.md.
// Bounded class/attribute selectors, pointer/width media, source/layer/important
// ordering, and real inline declarations. Unsupported guards cannot certify.
import type { AtRule, Declaration, Root, Rule } from 'postcss'
import type { CssEnvironmentU5 } from './settings-u5-css'

function listU44(text: string) {
  const parts: string[] = []
  let depth = 0, from = 0
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\\') { i++; continue }
    if (text[i] === '(' || text[i] === '[') depth++
    if (text[i] === ')' || text[i] === ']') depth--
    if (text[i] === ',' && depth === 0) { parts.push(text.slice(from, i)); from = i + 1 }
  }
  parts.push(text.slice(from))
  return parts
}
function enabledU44(params: string, env: CssEnvironmentU5) {
  return params.split(/\s+and\s+/).every((term) => {
    const pointer = /^\(\s*pointer:\s*(coarse|fine)\s*\)$/.exec(term)
    if (pointer) return pointer[1] === env.pointer
    const width = /^\(\s*width\s*(>=|<=|>|<)\s*([\d.]+)(px|rem)\s*\)$/.exec(term)
    if (!width) return false
    const limit = Number(width[2]) * (width[3] === 'rem' ? 16 : 1)
    return width[1] === '>=' ? env.width >= limit : width[1] === '<=' ? env.width <= limit : width[1] === '>' ? env.width > limit : env.width < limit
  })
}
function contextU44(declaration: Declaration, env: CssEnvironmentU5) {
  const rules: Rule[] = []
  let enabled = true, layer = 4
  let parent = declaration.parent
  while (parent && parent.type !== 'root') {
    if (parent.type === 'rule') rules.unshift(parent)
    if (parent.type === 'atrule') {
      const at = parent as AtRule
      if (/keyframes$/.test(at.name) || at.name === 'supports') enabled = false
      if (at.name === 'media') enabled &&= enabledU44(at.params, env)
      if (at.name === 'layer') layer = ['theme', 'base', 'components', 'utilities'].indexOf(at.params)
    }
    parent = parent.parent
  }
  let selectors = ['']
  for (const rule of rules) selectors = selectors.flatMap((ancestor) => listU44(rule.selector).map((part) =>
    part.includes('&') ? part.trim().replaceAll('&', ancestor) : ancestor ? `${ancestor} ${part.trim()}` : part.trim()))
  return { selectors, enabled: enabled && rules.length > 0, layer }
}
export function declarationsU44(css: Root, target: Element, env: CssEnvironmentU5, pseudo = '') {
  const values = new Map<string, { value: string; priority: number; important: boolean }>()
  css.walkDecls((decl) => {
    const { selectors, enabled, layer } = contextU44(decl, env)
    if (!enabled) return
    const matches = selectors.filter((selector) => {
      // Browser-vendor pseudo selectors are outside this same bounded subset
      // as :host/keyframes/supports; they cannot certify a structural rule.
      if (/:host\b|:{1,2}-(?:webkit|moz)/.test(selector)) return false
      if ((/::([\w-]+)/.exec(selector)?.[1] ?? '') !== pseudo) return false
      // Bare ::before/::after means every originating element, not an empty
      // selector. Preserve that universal match rather than swallowing errors.
      const origin = selector.replace(/::(?:before|after)\b/g, '').trim()
      return target.matches(origin || '*')
    })
    if (!matches.length) return
    const specificity = Math.max(...matches.map((selector) => (selector.match(/(?<!\\)[.[:#]/g) ?? []).length))
    const priority = (decl.important ? 10000 + (4 - layer) * 100 : layer * 100) + specificity
    if (priority >= (values.get(decl.prop)?.priority ?? -1)) values.set(decl.prop, { value: decl.value, priority, important: Boolean(decl.important) })
  })
  if (!pseudo && target instanceof HTMLElement) {
    for (const prop of target.style) {
      const important = target.style.getPropertyPriority(prop) === 'important'
      // Author !important defeats normal inline; important inline defeats
      // author !important. MDN /en-US/docs/Web/CSS/important is the oracle.
      if (!important && values.get(prop)?.important) continue
      values.set(prop, { value: target.style.getPropertyValue(prop), priority: 100000, important })
    }
  }
  return Object.fromEntries([...values].map(([key, value]) => [key, value.value])) as Record<string, string | undefined>
}

export function popupSizingU44(css: Root, popup: HTMLElement, env: CssEnvironmentU5) {
  const own = declarationsU44(css, popup, env)
  const compact = (value: string | undefined) => value?.replace(/\s+/g, '')
  const minimum = compact(own['min-width'])
  return {
    longestLabel: own.width === 'max-content',
    triggerAndEightRem: minimum === 'max(8rem,var(--radix-popover-trigger-width))'
      || minimum === 'max(var(--radix-popover-trigger-width),8rem)',
    availableWidthCap: compact(own['max-width']) === 'var(--radix-popover-content-available-width)',
  }
}
export function wrapsLabelU44(css: Root, label: HTMLElement, popup: HTMLElement, env: CssEnvironmentU5) {
  const own = declarationsU44(css, label, env)
  const ancestors: Element[] = []
  for (let node: Element | null = label; node; node = node.parentElement) {
    ancestors.push(node)
    if (node === popup) break
  }
  const chain = ancestors.map((node) => declarationsU44(css, node, env))
  return own['min-width'] === '0px' && ['anywhere', 'break-word'].includes(own['overflow-wrap'] ?? '')
    && chain.every((rule) => !['nowrap', 'pre'].includes(rule['white-space'] ?? ''))
    && own['text-overflow'] !== 'ellipsis' && !['hidden', 'clip'].includes(own['overflow-x'] ?? own.overflow ?? '')
}
export function recommendationU44(css: Root, name: HTMLElement, badge: HTMLElement, env: CssEnvironmentU5) {
  const text = declarationsU44(css, name, env), chip = declarationsU44(css, badge, env)
  const minimum = /^(\d+(?:\.\d+)?)%$/.exec(text['min-width'] ?? '')
  return {
    nameKeepsHalf: minimum !== null && Number(minimum[1]) >= 50,
    badgeYields: chip['flex-shrink'] !== '0' && chip['min-width'] === '0px'
      && chip.overflow === 'hidden' && chip['text-overflow'] === 'ellipsis',
  }
}
export function centeredXYU44(css: Root, link: HTMLElement, env: CssEnvironmentU5) {
  const own = declarationsU44(css, link, env), hit = declarationsU44(css, link, env, 'before')
  return { anchored: own.position === 'relative' && hit.position === 'absolute', left: hit.left ?? null,
    top: hit.top ?? null, transform: hit.transform ?? null, interactive: hit['pointer-events'] === 'auto' }
}

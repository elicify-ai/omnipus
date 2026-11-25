// Test-only DOM/shipped-CSS contracts, not jsdom geometry or animation measurements.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { compile } from '@tailwindcss/node'
import { parse, type AtRule, type Document, type Root, type Rule } from 'postcss'

const entries = new Map<string, Promise<Root>>()
export function stylesheetU2(name = 'globals.css'): Promise<Root> {
  if (!entries.has(name)) {
    const entry = resolve('src/styles', name)
    entries.set(name, compile(readFileSync(entry, 'utf8'), {
      base: dirname(entry), onDependency() {},
    }).then((compiled) => parse(compiled.build([]))))
  }
  return entries.get(name)!
}

function expandU2(value: string, tokens: Map<string, string>, seen = new Set<string>()): string {
  return value.replace(/var\((--[\w-]+)\)/g, (_, key: string) => {
    if (seen.has(key)) throw new Error(`Touch contract has cyclic size token ${key}`)
    return expandU2(tokens.get(key) ?? key, tokens, new Set([...seen, key]))
  })
}

function tokensU2(css: Root) {
  const tokens = new Map<string, string>()
  css.walkDecls((declaration) => {
    if (declaration.prop.startsWith('--')) tokens.set(declaration.prop, declaration.value)
  })
  return tokens
}

// 44 is the independent minimum in US-9 AS5 / FR-070, not inferred from token names.
export function atLeastTouchU2(value: string, css: Root) {
  const resolved = expandU2(value, tokensU2(css)).trim()
  const literal = /^(\d+(?:\.\d+)?)px$/.exec(resolved)
  const maximum = /^max\(100%,\s*(\d+(?:\.\d+)?)px\)$/.exec(resolved)
  return Number(literal?.[1] ?? maximum?.[1] ?? 0) >= 44
}

function hasMediaU2(rule: Rule, condition: RegExp) {
  let parent: Root | Rule | AtRule | Document | undefined = rule.parent
  while (parent) {
    if (parent.type === 'atrule' && parent.name === 'media' && condition.test(parent.params)) return true
    parent = parent.parent
  }
  return false
}

export function pseudoTouchU2(css: Root, target: Element) {
  const base = new Map<string, string>()
  const coarse = new Map<string, string>()
  css.walkRules((rule) => {
    if (!/::?before\b/.test(rule.selector)) return
    // Match the ACTUAL control. A Switch-only rule is not evidence for Button.
    const selectors = rule.selector.split(',').map((selector) => selector.replace(/::?before\b/g, '').trim())
    if (!selectors.some((selector) => target.matches(selector))) return
    const declarations = hasMediaU2(rule, /pointer:\s*coarse/) ? coarse : base
    for (const node of rule.nodes) {
      if (node.type === 'decl') declarations.set(node.prop, node.value)
    }
  })
  const effective = new Map([...base, ...coarse])
  return ["''", '""'].includes(effective.get('content') ?? '')
    && effective.get('position') === 'absolute'
    && effective.get('pointer-events') === 'auto'
    && atLeastTouchU2(effective.get('width') ?? '', css)
    && atLeastTouchU2(effective.get('height') ?? '', css)
}

export async function touchContractU2(target: HTMLElement) {
  const css = await stylesheetU2()
  const classes = [...target.classList]
  // Same explicit coarse-pointer contract as U1 and SegmentedControlItem.
  const dimension = (axis: 'h' | 'w') => classes.some((candidate) => {
    const match = /^(?:(?:pointer-coarse:|\[@media\(pointer:coarse\)\]:))?(?:min-)?([hw])-\[(.+)\]$/.exec(candidate)
    return match?.[1] === axis && atLeastTouchU2(match[2], css)
  })
  const height = dimension('h')
  const width = dimension('w') || classes.includes('w-full')
    || (classes.includes('flex-1') && target.parentElement?.classList.contains('flex') === true)
  const pseudo = classes.includes('relative') && pseudoTouchU2(css, target)
  return { pass: (height && width) || pseudo, height, width, pseudo }
}

export async function reducedMotionU2(target: Element) {
  if (target.classList.contains('motion-reduce:animate-none')) return true
  const css = await stylesheetU2()
  let stopped = false
  css.walkRules((rule) => {
    if (!hasMediaU2(rule, /prefers-reduced-motion:\s*reduce/) || !target.matches(rule.selector)) return
    for (const node of rule.nodes) {
      if (node.type === 'decl' && ['animation', 'animation-name'].includes(node.prop) && node.value === 'none') stopped = true
    }
  })
  return stopped
}

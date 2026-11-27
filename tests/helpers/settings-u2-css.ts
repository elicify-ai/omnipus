// Test-only DOM/shipped-CSS contracts, not jsdom geometry or animation measurements.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { compile } from '@tailwindcss/node'
import { parse, type AtRule, type Document, type Root, type Rule } from 'postcss'
import { noMotionU4 } from './settings-u4-motion'

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
  // Custom-property token streams may retain trailing CSS whitespace.
  const maximum = /^max\(100%,\s*(\d+(?:\.\d+)?)px\s*\)$/.exec(resolved)
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
    const coarseOnly = hasMediaU2(rule, /^\(pointer: coarse\)$/)
    if (rule.selector.includes('[data-radio-target]')) {
      // U8-A: an unknown/extra guard cannot certify a coarse-only radio area.
      for (let parent: Root | Rule | AtRule | Document | undefined = rule.parent; parent; parent = parent.parent) {
        if (parent.type === 'atrule' && (parent.name === 'supports'
          || (parent.name === 'media' && parent.params !== '(pointer: coarse)'))) {
          throw new Error(`U8-A radio tap scope must be exactly (pointer: coarse), not @${parent.name} ${parent.params}`)
        }
      }
      if (!coarseOnly) throw new Error('U8-A radio tap scope must be exactly (pointer: coarse), not unscoped')
    }
    const declarations = coarseOnly ? coarse : base
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
  // Rule 12: explicit coarse-pointer sizing, never an unscoped mouse-screen minimum.
  // Preserve selector-matched shipped pseudo hit areas below (U1-C2).
  const dimension = (axis: 'h' | 'w') => classes.some((candidate) => {
    const match = /^(?:pointer-coarse:|\[@media\(pointer:coarse\)\]:)(?:min-)?([hw]|size)-(?:\[(.+)\]|\((--[\w-]+)\))$/.exec(candidate)
    const value = match?.[2] ?? (match?.[3] ? `var(${match[3]})` : '')
    return (match?.[1] === axis || match?.[1] === 'size') && atLeastTouchU2(value, css)
  })
  const height = dimension('h')
  const width = dimension('w') || classes.includes('w-full')
    || (classes.includes('flex-1') && target.parentElement?.classList.contains('flex') === true)
  const pseudo = classes.includes('relative') && pseudoTouchU2(css, target)
  return { pass: (height && width) || pseudo, height, width, pseudo }
}

export async function reducedMotionU2(target: Element) {
  // T4-6: read matching compiled CSS, including arbitrary animation candidates
  // and a shared reduced-motion rule. A class-name stamp alone is not proof.
  return noMotionU4(target)
}

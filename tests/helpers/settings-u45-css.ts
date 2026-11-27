// U7 structural CSS probes; NOT a layout engine or fabricated DOM geometry.
// Oracle: /workspace/logs/settings-1519/ta/r45-plan.md. Unknown stays unknown.
import type { Root } from 'postcss'
import type { CssEnvironmentU5 } from './settings-u5-css'
import { pixelsU43 } from './settings-u43-css'
import { declarationsU44 } from './settings-u44-css'

// Use the platform parser plus a bounded numeric evaluator: jsdom accepts
// calc(broken), so parser acceptance ALONE must never certify a width.
function lengthU45(css: Root, raw: string | undefined, root: number, variables: Record<string, number> = {}) {
  if (raw === undefined) return null
  const probe = document.createElement('div')
  // jsdom rejects compact max(18rem,var(...)) but accepts its identical
  // comma-spaced spelling. Canonical whitespace changes no CSS semantics.
  // Both forms have independent positive controls in uatU45.instruments.
  probe.style.width = raw.replace(/,\s*/g, ', ')
  if (!probe.style.width) return null
  const expanded = raw.replace(/var\(\s*(--[\w-]+)\s*\)/g, (text, name: string) =>
    Object.hasOwn(variables, name) ? `${variables[name]}px` : text)
  const value = pixelsU43(css, expanded, root)
  return value !== null && Number.isFinite(value) && value >= 0 ? value : null
}

// Supplied trigger/available/root are independent environmental inputs, not
// measured claims about jsdom's zero-sized trigger or Radix layout.
export function modelWidthU45(css: Root, popup: HTMLElement, env: CssEnvironmentU5, trigger: number, available: number, root: number) {
  const own = declarationsU44(css, popup, env)
  const variables = { '--radix-popover-trigger-width': trigger, '--radix-popover-content-available-width': available }
  const width = lengthU45(css, own.width, root, variables)
  const minimum = own['min-width'] === undefined || own['min-width'] === 'auto' ? 0 : lengthU45(css, own['min-width'], root, variables)
  const maximum = own['max-width'] === undefined || own['max-width'] === 'none' ? Infinity : lengthU45(css, own['max-width'], root, variables)
  // CSS min-width wins even over max-width. A hard min must not falsely pass
  // the available-viewport cap. MDN /Web/CSS/Reference/Properties/min-width.
  const used = width === null || minimum === null || maximum === null ? null : Math.max(minimum, Math.min(width, maximum))
  return { validWidth: width !== null, used }
}

function verticalU45(css: Root, own: Record<string, string | undefined>, family: 'padding' | 'border', root: number) {
  const suffix = family === 'border' ? '-width' : ''
  const shorthand = own[`${family}-block${suffix}`] ?? own[`${family}${suffix}`] ?? '0px'
  const parts = shorthand.trim().split(/\s+(?![^()]*\))/)
  // More complex shorthands cannot certify. Two-value logical block syntax
  // is distinct from two-value physical syntax (vertical, horizontal).
  const logical = own[`${family}-block${suffix}`] !== undefined
  if (parts.length > 2) return null
  const top = own[`${family}-top${suffix}`] ?? parts[0]
  const bottom = own[`${family}-bottom${suffix}`] ?? (logical ? parts[1] ?? parts[0] : parts[0])
  const a = lengthU45(css, top, root), b = lengthU45(css, bottom, root)
  return a === null || b === null ? null : a + b
}

export function virtualRowU45(css: Root, row: HTMLElement, slot: HTMLElement, env: CssEnvironmentU5, root = 14) {
  const own = declarationsU44(css, row, env)
  const slotHeight = lengthU45(css, declarationsU44(css, slot, env).height, root)
  const dimension = (raw: string | undefined) => {
    const percent = /^(\d+(?:\.\d+)?)%$/.exec(raw ?? '')
    return percent && slotHeight !== null ? slotHeight * Number(percent[1]) / 100 : lengthU45(css, raw, root)
  }
  const height = dimension(own.height)
  const maximum = own['max-height'] === undefined || own['max-height'] === 'none' ? Infinity : dimension(own['max-height'])
  const minimum = own['min-height'] === undefined || own['min-height'] === 'auto' ? 0 : dimension(own['min-height'])
  const padding = verticalU45(css, own, 'padding', root), border = verticalU45(css, own, 'border', root)
  // A content-driven auto height with no finite cap is UNKNOWN, not 0 and not
  // the slot's height. That is the old recommended-row regression's blind spot.
  const bounded = maximum !== null && minimum !== null && padding !== null && border !== null
    ? Math.max(minimum, Math.min(height ?? Infinity, maximum)) : Infinity
  const upperBound = !Number.isFinite(bounded) ? null : own['box-sizing'] === 'border-box'
    ? Math.max(bounded, padding! + border!) : bounded + padding! + border!
  // Padding/borders cannot shrink below zero content size (MDN box-sizing).
  return { slotHeight, upperBound, clipsContents: ['hidden', 'clip'].includes(own['overflow-y'] ?? own.overflow ?? '') }
}

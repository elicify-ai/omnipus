// T4-5/6: discover the rendered set, never a hand-picked list of passing controls.
import { isInaccessible } from '@testing-library/dom'
import { createRequire } from 'node:module'
// Installed 0.5.x exports expose the CJS function but omit its declaration path.
// Signature read from dist/accessible-name.d.ts; no type-suppression directive.
const { computeAccessibleName } = createRequire(import.meta.url)('dom-accessibility-api') as {
  computeAccessibleName(root: Element): string
}
import { touchContractU2 } from './settings-u2-css'
import { animationU4, isStaticU4, motionCssU4, movingElementsU4 } from './settings-u4-motion'

const CONTROLS = 'button, select, input:not([type="hidden"]), textarea, a[href], summary, [role="button"], [role="switch"], [role="combobox"], [role="checkbox"], [role="radio"], [role="option"], [role="tab"], [role="slider"], [tabindex]'
export function controlsU4(root: ParentNode = document.body) {
  return [...root.querySelectorAll<HTMLElement>(CONTROLS)].filter((element) =>
    !isInaccessible(element) && !element.closest('[inert]')
    // A noninteractive focus destination (main, headings) is not a control.
    && !(element.tabIndex < 0 && !element.matches(CONTROLS.replace(', [tabindex]', ''))))
}
export function identityU4(element: Element) {
  return { tag: element.tagName.toLowerCase(), role: element.getAttribute('role'),
    id: element.id || element.getAttribute('data-testid') || '', name: computeAccessibleName(element),
    row: element.getAttribute('data-settings-row-target'), classes: element.getAttribute('class') ?? '' }
}

function rowWidthU4(element: HTMLElement) {
  const own = element.classList
  const parent = element.parentElement?.classList
  // A bounded layout contract, NOT a measurement: filling the containing row
  // still needs physical-width UAT at 390px. Unknown/content width is false.
  // A flex box can itself be a content-sized ITEM of another flex container.
  if (own.contains('absolute') || own.contains('fixed')) return false
  const aligned = (prefix: string) => [...own].find((name) => name.startsWith(prefix) && name !== `${prefix}auto`)
  if (parent?.contains('flex') || parent?.contains('inline-flex')) {
    if (!parent.contains('flex-col') && !parent.contains('flex-col-reverse')) return false
    const self = aligned('self-')
    if (self) return self === 'self-stretch'
    return ![...parent].some((name) => name.startsWith('items-') && name !== 'items-stretch')
  }
  if (parent?.contains('grid') || parent?.contains('inline-grid')) {
    const self = aligned('justify-self-')
    if (self) return self === 'justify-self-stretch'
    return ![...parent].some((name) => name.startsWith('justify-items-') && name !== 'justify-items-stretch')
  }
  return own.contains('flex') || own.contains('block')
}

export async function touchSweepU4(state: string) {
  const controls = controlsU4()
  const rows = []
  for (const element of controls) {
    const direct = await touchContractU2(element)
    const width = direct.width || rowWidthU4(element)
    // A real associated label can be the clickable target of a compact radio /
    // checkbox, but an arbitrary large ancestor is never credited.
    const label = element.matches('input[type="checkbox"], input[type="radio"]')
      ? (element as HTMLInputElement).labels?.[0] : undefined
    const labelContract = label ? await touchContractU2(label) : undefined
    const labelPass = label && labelContract
      ? labelContract.pseudo || (labelContract.height && (labelContract.width || rowWidthU4(label))) : false
    const pass = direct.pseudo || (direct.height && width) || labelPass
    rows.push({ ...identityU4(element), ...direct, width, pass,
      labelTarget: label ? { id: label.htmlFor, classes: label.getAttribute('class') ?? '', pass: labelPass } : null })
  }
  console.info('R39_TOUCH_INVENTORY ' + JSON.stringify({ state, controls: rows }))
  return rows
}

export async function motionSweepU4(state: string, root: ParentNode = document.body) {
  // Include aria-hidden SVG/spinner/skeleton decorations: they still visibly move.
  // Only CSS/DOM-hidden (including dialog background) elements are excluded.
  const elements = movingElementsU4(root).filter((element) => {
    for (let current: Element | null = element; current; current = current.parentElement) {
      if (current.hasAttribute('hidden') || current.hasAttribute('inert')) return false
      const style = window.getComputedStyle(current)
      if (style.display === 'none' || style.visibility === 'hidden') return false
    }
    return true
  })
  const css = await motionCssU4(elements)
  const rows = elements.map((element) => {
    const reduced = animationU4(css, element)
    const normal = animationU4(css, element, false)
    // A shared reduce rule may stop everything correctly while a WRONG
    // motion-safe:animate-none also freezes just one loading site normally.
    const requiresNormal = [...element.classList].some((name) => name.startsWith('animate-') && name !== 'animate-none')
    return { ...identityU4(element), normal, reduced, pass: isStaticU4(reduced),
      normalPass: !requiresNormal || !isStaticU4(normal) }
  })
  console.info('R39_MOTION_INVENTORY ' + JSON.stringify({ state, animations: rows }))
  return rows
}

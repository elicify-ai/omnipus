// Test-instrument controls only: synthetic CSS never replaces shipped CSS or
// production code, and these are NOT CHECK's named-fault/mutation runs.
import { parse } from 'postcss'
import { expect, it } from 'vitest'
import { pseudoTouchU2 } from '../../../tests/helpers/settings-u2-css'
import { classLengthU47, fineBeforeU47, radioBoxU47, radioReachU47 } from '../../../tests/helpers/settings-u47-radio'

function target() {
  const element = document.createElement('span')
  element.setAttribute('data-radio-target', '')
  return element
}
const region = "[data-radio-target]::before { content: ''; position: absolute; pointer-events: auto; width: 44px; height: 44px; }"

it('U47 instrument: only the exact coarse media scope certifies a radio tap area', () => {
  const element = target()
  const css = parse(`@media (pointer: coarse) { ${region} }`)
  expect(pseudoTouchU2(css, element)).toBe(true)
  expect(fineBeforeU47(css, element)).toBe(false)
})

it.each([
  '@media (pointer: fine)', '@media all', '@supports (display: block)',
  '@media (pointer: coarse) and (width >= 390px)',
])('U47 instrument: %s cannot certify the coarse-only radio rule', (guard) => {
  expect(() => pseudoTouchU2(parse(`${guard} { ${region} }`), target()))
    .toThrow(/U8-A radio tap scope must be exactly \(pointer: coarse\), not @/)
})

it('U47 instrument: nesting the right media inside supports is still invalid', () => {
  const css = parse(`@supports (display: block) { @media (pointer: coarse) { ${region} } }`)
  expect(() => pseudoTouchU2(css, target())).toThrow('not @supports (display: block)')
})

it('U47 instrument: an unscoped radio rule is rejected even if it draws only a small fine region', () => {
  const css = parse("[data-radio-target]::before { content: ''; position: absolute; width: 1px; height: 1px; }")
  expect(() => pseudoTouchU2(css, target())).toThrow('not unscoped')
  expect(fineBeforeU47(css, target())).toBe(true)
})

it.each([
  ['max(100%, 44px)', '50%', 'translate(-50%, -50%)'],
  ['44px', '6.5px', 'translate(-22px, -22px)'],
] as const)('U47 instrument: equivalent %s / %s / %s geometry has the same bounds', (size, offset, transform) => {
  const css = parse(`[data-radio-target]::before { width: ${size}; height: ${size}; left: ${offset}; top: ${offset}; transform: ${transform}; }`)
  // Independent rectangle arithmetic: 13/2 - 44/2 = -15.5, ending at 28.5.
  expect(radioBoxU47(css, target())).toEqual({ width: 44, height: 44, left: -15.5, top: -15.5,
    right: 28.5, bottom: 28.5, centreX: 6.5, centreY: 6.5 })
})

it('U47 instrument: unsupported dimensions remain unknown, never a fabricated rectangle', () => {
  expect(radioBoxU47(parse('[data-radio-target]::before { width: 10vh; height: 44px; }'), target())).toBeNull()
})

it('U47 instrument: a card without its own minimum cannot inherit the 44px product threshold', () => {
  const card = document.createElement('label')
  const element = target()
  card.append(element)
  document.createElement('div').append(card)
  expect(classLengthU47(parse(':root { --touch: 44px; }'), card, ['pointer-coarse:min-h-'], null)).toBeNull()
  expect(radioReachU47(parse(''), element, card).minimumHeight).toBeNull()
})

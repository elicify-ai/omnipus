// Test-instrument controls only: synthetic CSS never replaces shipped CSS or
// production code, and these are NOT CHECK's named-fault/mutation runs.
import { parse } from 'postcss'
import { expect, it } from 'vitest'
import { pseudoTouchU2 } from '../../../tests/helpers/settings-u2-css'
import { classLengthU47, expectRadioReachU47, fineBeforeU47, radioBoxU47, radioReachU47 } from '../../../tests/helpers/settings-u47-radio'

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

// Part 48: the commission's closed declaration model, not a copied production
// rule. Exact rectangle oracle: 13 / 2 - 44 / 2 = -15.5; far edge = 28.5.
const boxU48 = "content: ''; position: absolute; left: 50%; top: 50%; width: 44px; height: 44px; transform: translate(-50%, -50%); pointer-events: auto;"
const boundsU48 = { width: 44, height: 44, left: -15.5, top: -15.5,
  right: 28.5, bottom: 28.5, centreX: 6.5, centreY: 6.5 }

it.each([
  ['scale', '1'], ['translate', '0'], ['rotate', '0deg'],
  ['margin', '0'], ['margin-top', '0'], ['max-width', 'none'], ['min-height', '0'],
])('U48 instrument: unsupported %s is unknown even with a harmless value', (property, value) => {
  const css = parse(`[data-radio-target]::before { ${boxU48} ${property}: ${value}; }`)
  expect(radioBoxU47(css, target()), `${property} must not be silently ignored`).toBeNull()
})

it('U48 instrument: an unreadable box stops at the caller\'s labelled assertion', () => {
  const element = target()
  const card = document.createElement('label')
  card.append(element)
  document.createElement('div').append(card)
  const css = parse(`[data-radio-target]::before { ${boxU48} scale: 1; }`)
  expect(() => expectRadioReachU47(css, element, card, 'closed-list control'))
    .toThrow('size, offsets and translation must be readable')
})

it.each(['[data-radio-target=""]', '.tap-probe', 'span'])(
  'U48 instrument: equivalent matching selector %s reads the same rectangle', (selector) => {
    const element = target()
    element.classList.add('tap-probe')
    expect(radioBoxU47(parse(`${selector}::before { ${boxU48} }`), element), selector).toEqual(boundsU48)
  },
)

it('U48 instrument: an unrelated unsupported rule does not match the tap box', () => {
  const css = parse(`[data-radio-target]::before { ${boxU48} } [data-other]::before { scale: 0; }`)
  expect(radioBoxU47(css, target())).toEqual(boundsU48)
})

it.each(['before', 'after'])('U48 instrument: a second box rule is refused %s the full rule', (order) => {
  const full = `[data-radio-target]::before { ${boxU48} }`
  // Even an identical width is ambiguous to this deliberately non-cascade reader.
  const other = 'span[data-radio-target]::before { width: 44px; }'
  const css = parse(order === 'before' ? `${other} ${full}` : `${full} ${other}`)
  expect(radioBoxU47(css, target()), 'do not choose a rule by source order').toBeNull()
})

it('U48 instrument: a second respelled matching rule cannot hide an offset', () => {
  const css = parse(`[data-radio-target]::before { ${boxU48} } [data-radio-target=""]::before { top: 50%; }`)
  expect(radioBoxU47(css, target()), 'both spellings match the actual target').toBeNull()
})

it('U48 instrument: disjoint box rules are refused rather than assembled into a rectangle', () => {
  const css = parse(`[data-radio-target]::before { width: 44px; height: 44px; }
    [data-radio-target]::before { left: 50%; top: 50%; transform: translate(-50%, -50%); }`)
  expect(radioBoxU47(css, target()), 'single-rule scope, not a CSS cascade engine').toBeNull()
})

it('U48 instrument: exactly neutral universal resets do not draw a second tap box', () => {
  const css = parse(`*, ::after, ::before { box-sizing: border-box; margin: 0; padding: 0; border: 0 solid; }
    *, *::before, *::after { box-sizing: border-box; }
    [data-radio-target]::before { ${boxU48} }`)
  expect(radioBoxU47(css, target())).toEqual(boundsU48)
})

it.each(['margin: 1px;', 'scale: 1;'])(
  'U48 instrument: a universal rule with %s is not an exempt neutral reset', (declaration) => {
    const css = parse(`*::before { box-sizing: border-box; ${declaration} }
      [data-radio-target]::before { ${boxU48} }`)
    expect(radioBoxU47(css, target()), 'reset exclusion must not hide active unknown CSS').toBeNull()
  },
)

it('U48 instrument: the fine-pointer reader refuses supports around the exact coarse query', () => {
  const css = parse(`@supports (display: block) { @media (pointer: coarse) { ${region} } }`)
  // Call this reader directly: pseudoTouchU2's throw must not mask this branch.
  expect(fineBeforeU47(css, target()), 'nested guards cannot certify fine-pointer preservation').toBe(true)
})

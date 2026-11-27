// Independent positive/negative calibration of the bounded U7 CSS instruments.
// Synthetic declarations below are test inputs, never production mutants.
import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { parse } from 'postcss'
import { modelWidthU45, virtualRowU45 } from '../../../tests/helpers/settings-u45-css'

afterEach(cleanup)
const env = { width: 390, pointer: 'fine' } as const
const triggerVariable = 'var(--radix-popover-trigger-width)'
const availableVariable = 'var(--radix-popover-content-available-width)'

it.each([
  ['bare custom-property is invalid', '--radix-popover-trigger-width', '', false, null],
  ['missing width stays unknown', '', '', false, null],
  ['parser-permissive invalid arithmetic stays unknown', 'calc(broken)', '', false, null],
  ['unitless nonzero width is invalid', '252', '', false, null],
  ['trigger only misses the 18rem floor', triggerVariable, `max-width:${availableVariable}`, true, 108],
  ['18rem floor at 14px root', `max(18rem,${triggerVariable})`, `max-width:${availableVariable}`, true, 252],
  ['equivalent max argument order works', `max(${triggerVariable},18rem)`, `max-width:${availableVariable}`, true, 252],
  ['equivalent min/width syntax works', triggerVariable, `min-width:18rem;max-width:${availableVariable}`, true, 252],
  ['missing cap exposes oversized width', '500px', '', true, 500],
  ['hard minimum defeats the cap', '500px', `min-width:500px;max-width:${availableVariable}`, true, 500],
] as const)('U45 width instrument — %s', (_name, width, extra, validWidth, used) => {
  render(<div data-testid="probe" className="probe" />)
  const css = parse(`.probe{width:${width};${extra}}`)
  expect(modelWidthU45(css, screen.getByTestId('probe'), env, 108, 390, 14)).toEqual({ validWidth, used })
})

it('U45 width boundaries — floor and available viewport at minus one, equal, plus one, with different roots', () => {
  render(<div data-testid="probe" className="probe" />)
  const css = parse(`.probe{width:max(18rem,${triggerVariable});max-width:${availableVariable}}`)
  for (const root of [14, 20]) {
    const floor = 18 * root // U7-1 specifies 18rem, not a guessed fixed px value.
    for (const trigger of [floor - 1, floor, floor + 1, 389, 390, 391]) {
      expect(modelWidthU45(css, screen.getByTestId('probe'), env, trigger, 390, root), `root=${root}, trigger=${trigger}`)
        .toEqual({ validWidth: true, used: Math.min(390, Math.max(floor, trigger)) })
    }
  }
})

it.each([
  ['fine slot minus one', 32, 'height:31px', 'border-box', 31, true],
  ['fine exact slot', 32, 'height:32px', 'border-box', 32, true],
  ['fine slot plus one', 32, 'height:33px', 'border-box', 33, false],
  ['coarse slot minus one', 44, 'height:43px', 'border-box', 43, true],
  ['coarse exact slot', 44, 'height:44px', 'border-box', 44, true],
  ['coarse slot plus one', 44, 'height:45px', 'border-box', 45, false],
  ['percentage fills actual slot', 32, 'height:100%', 'border-box', 32, true],
  ['auto is not a slot measurement', 32, '', 'border-box', null, false],
  ['finite cap bounds auto height', 32, 'max-height:32px', 'border-box', 32, true],
  ['minimum overrides height and cap', 32, 'height:32px;max-height:32px;min-height:33px', 'border-box', 33, false],
  ['content-box padding is additional', 32, 'height:32px;padding-block:1px', 'content-box', 34, false],
  ['border-box includes padding', 32, 'height:32px;padding-block:1px', 'border-box', 32, true],
  ['unshrinkable padding overflows', 32, 'height:32px;padding-block:17px', 'border-box', 34, false],
  ['content-box borders are additional', 32, 'height:32px;border-width:1px', 'content-box', 34, false],
  ['unknown sizing remains unknown', 32, 'height:calc(broken)', 'border-box', null, false],
] as const)('U45 row-slot instrument — %s', (_name, slotHeight, row, sizing, upperBound, fits) => {
  // 32px is the U7-2 reported slot, 44px the FR-070 coarse minimum.
  render(<div data-testid="slot" className="slot"><div data-testid="row" className="row" /></div>)
  const css = parse(`.slot{height:${slotHeight}px}.row{${row};box-sizing:${sizing};overflow:hidden}`)
  const result = virtualRowU45(css, screen.getByTestId('row'), screen.getByTestId('slot'), env)
  expect(result).toEqual({ slotHeight, upperBound, clipsContents: true })
  expect(result.upperBound !== null && result.upperBound <= result.slotHeight!).toBe(fits)
})

it('U45 row-slot instrument — visible overflow is not proof of contained painted content', () => {
  render(<div data-testid="slot" className="slot"><div data-testid="row" className="row" /></div>)
  const css = parse('.slot{height:32px}.row{height:100%;box-sizing:border-box;overflow:visible}')
  expect(virtualRowU45(css, screen.getByTestId('row'), screen.getByTestId('slot'), env))
    .toEqual({ slotHeight: 32, upperBound: 32, clipsContents: false })
})

// Instruments for T4-5/6. Synthetic CSS is deliberately independent of the product's fix.
// No production mutation: these are tiny input stylesheets and real rendered DOM.
import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { parse } from 'postcss'
import { animationU4, isStaticU4, motionCssU4, movingElementsU4 } from '../../../tests/helpers/settings-u4-motion'
import { controlsU4, touchSweepU4 } from '../../../tests/helpers/settings-u4-sweep'
import { Button } from '@/components/ui/button'

afterEach(cleanup)

it.each([
  ['no stop', '.moving { animation: spin 1s linear infinite }', false],
  ['a shipped reduced-motion stop', '.moving { animation: spin 1s linear infinite } @media (prefers-reduced-motion: reduce) { .moving { animation: none } }', true],
  ['a wrong preference is not a stop', '.moving { animation: spin 1s linear infinite } @media (prefers-reduced-motion: no-preference) { .moving { animation: none } }', false],
  ['a wrong target is not a stop', '.moving { animation: spin 1s linear infinite } @media (prefers-reduced-motion: reduce) { .other { animation: none } }', false],
  ['a global important stop wins', '.moving { animation: spin 1s linear infinite } @media (prefers-reduced-motion: reduce) { [class*="moving"] { animation: none !important } }', true],
  ['one stopped animation cannot conceal a second animation', '.moving { animation: none, spin 1s linear infinite }', false],
  ['nested Tailwind v4 media', '.moving { animation: spin 1s linear infinite; @media (prefers-reduced-motion: reduce) { animation: none } }', true],
] as const)('T4-6 instrument — %s', (_name, css, expected) => {
  render(<div data-testid="motion-instrument" className="moving" />)
  expect(isStaticU4(animationU4(parse(css), screen.getByTestId('motion-instrument')))).toBe(expected)
})

it('T4-6 instrument — DOM discovery includes arbitrary and variant animations plus the row marker, not an allow-list', async () => {
  const { container } = render(<>
    <span data-testid="spin" className="animate-spin" />
    <span data-testid="arbitrary" className="animate-[spin_1s_linear_infinite]" />
    <span data-testid="variant" className="motion-safe:animate-[pulse_2s_infinite]" />
    <span data-testid="row" data-settings-row-target="" />
    <span data-testid="static" />
  </>)
  const moving = movingElementsU4(container)
  expect(moving.map((element) => element.getAttribute('data-testid'))).toEqual(['spin', 'arbitrary', 'variant', 'row'])
  const arbitrary = screen.getByTestId('arbitrary')
  const normal = animationU4(await motionCssU4([arbitrary]), arbitrary, false)
  expect(normal, 'compiled arbitrary utility must actually set spin, not be silently treated as static').toMatch(/^spin\s/)
})

it('T4-5 instrument — sweeps enumerate visible fields, real buttons and labelled radios but not hidden inputs or focus destinations', () => {
  const { container } = render(<>
    <Button>Instrument action</Button>
    <input aria-label="Instrument field" />
    <label><input type="radio" />Instrument radio</label>
    <input type="hidden" value="not a target" />
    <main tabIndex={-1}>Focus destination</main>
    <div hidden><Button>Hidden action</Button></div>
  </>)
  expect(controlsU4(container).map((element) => element.tagName.toLowerCase())).toEqual(['button', 'input', 'input'])
})

it('T4-5 instrument — credit a real associated token-height block label, never an arbitrary large ancestor', async () => {
  render(<>
    <label htmlFor="associated-radio" className="flex pointer-coarse:min-h-[var(--target-touch-minimum)]">Associated action</label>
    <input id="associated-radio" type="radio" />
    <div className="flex pointer-coarse:min-h-[var(--target-touch-minimum)]">
      <input type="radio" aria-label="Unassociated action" />
    </div>
  </>)
  const rows = await touchSweepU4('instrument:associated-label')
  expect(rows.map(({ name, pass }) => ({ name, pass }))).toEqual([
    { name: 'Associated action', pass: true }, { name: 'Unassociated action', pass: false },
  ])
})

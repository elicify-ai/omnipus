// Instruments for T4-5/6. Synthetic CSS is deliberately independent of the product's fix.
// No production mutation: these are tiny input stylesheets and real rendered DOM.
import { afterEach, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { parse } from 'postcss'
import { animationU4, isStaticU4, motionCssU4, movingElementsU4 } from '../../../tests/helpers/settings-u4-motion'
import { controlsU4, motionSweepU4, touchSweepU4 } from '../../../tests/helpers/settings-u4-sweep'
import { Button } from '@/components/ui/button'

afterEach(cleanup)

const TOUCH_HEIGHT = 'pointer-coarse:min-h-[var(--target-touch-minimum)]'

// P-WIDTH / U4-C6: display:flex describes a box's children, not its own
// width. These expectations come from block/flex/grid sizing and alignment,
// not from jsdom rectangles or any observed product output.
it.each([
  ['row flex item', <div key="row" className="flex"><a href="#x" className={`flex ${TOUCH_HEIGHT}`}>Go</a></div>, false],
  ['end-aligned column item', <div key="end" className="flex flex-col items-end"><a href="#x" className={TOUCH_HEIGHT}>Go</a></div>, false],
  ['start-aligned column item', <div key="start" className="flex flex-col items-start"><a href="#x" className={TOUCH_HEIGHT}>Go</a></div>, false],
  ['center-aligned column item', <div key="center" className="flex flex-col items-center"><a href="#x" className={TOUCH_HEIGHT}>Go</a></div>, false],
  ['self-end overrides parent stretch', <div key="self-end" className="flex flex-col"><a href="#x" className={`self-end ${TOUCH_HEIGHT}`}>Go</a></div>, false],
  ['self-center overrides parent stretch', <div key="self-center" className="flex flex-col"><a href="#x" className={`self-center ${TOUCH_HEIGHT}`}>Go</a></div>, false],
  ['inline flex box in block flow', <div key="inline"><a href="#x" className={`inline-flex ${TOUCH_HEIGHT}`}>Go</a></div>, false],
  ['end-aligned grid item', <div key="grid-end" className="grid justify-items-end"><a href="#x" className={TOUCH_HEIGHT}>Go</a></div>, false],
  ['default column stretch', <div key="column" className="flex flex-col"><a href="#x" className={TOUCH_HEIGHT}>Go</a></div>, true],
  ['block flex box in block flow', <div key="block"><a href="#x" className={`flex ${TOUCH_HEIGHT}`}>Go</a></div>, true],
  ['self-stretch overrides parent end', <div key="self-stretch" className="flex flex-col items-end"><a href="#x" className={`self-stretch ${TOUCH_HEIGHT}`}>Go</a></div>, true],
  ['default grid stretch', <div key="grid" className="grid"><a href="#x" className={TOUCH_HEIGHT}>Go</a></div>, true],
  ['explicit full width in a row', <div key="full" className="flex"><a href="#x" className={`w-full ${TOUCH_HEIGHT}`}>Go</a></div>, true],
  ['explicit coarse minimum width', <div key="minimum" className="flex"><a href="#x" className={`pointer-coarse:min-w-[var(--target-touch-minimum)] ${TOUCH_HEIGHT}`}>Go</a></div>, true],
] as const)('T4-5 width instrument — %s', async (name, ui, expected) => {
  render(ui)
  const rows = await touchSweepU4(`instrument:width:${name}`)
  expect(rows.map(({ tag, name: accessibleName, width, pass }) => ({ tag, name: accessibleName, width, pass }))).toEqual([
    { tag: 'a', name: 'Go', width: expected, pass: expected },
  ])
})

it.each(['radio', 'checkbox'] as const)('T4-5 label instrument — an associated %s label still needs its own height AND width', async (type) => {
  render(<>
    <label htmlFor="undimensioned">No dimensions</label>
    <input id="undimensioned" type={type} />
    <label htmlFor="height-only" className={TOUCH_HEIGHT}>Height only</label>
    <input id="height-only" type={type} />
    <label htmlFor="width-only" className="flex">Width only</label>
    <input id="width-only" type={type} />
    <div className="flex">
      <label htmlFor="row-label" className={`flex ${TOUCH_HEIGHT}`}>Content-sized label</label>
      <input id="row-label" type={type} />
    </div>
    <label htmlFor="full-label" className={`flex ${TOUCH_HEIGHT}`}>Sized label</label>
    <input id="full-label" type={type} />
  </>)
  const rows = await touchSweepU4(`instrument:label:${type}`)
  expect(rows.map(({ id, pass, labelTarget }) => ({ id, pass, labelPass: labelTarget?.pass }))).toEqual([
    { id: 'undimensioned', pass: false, labelPass: false },
    { id: 'height-only', pass: false, labelPass: false },
    { id: 'width-only', pass: false, labelPass: false },
    { id: 'row-label', pass: false, labelPass: false },
    { id: 'full-label', pass: true, labelPass: true },
  ])
})

it('T4-6 normal-motion instrument — a moving sibling cannot conceal a wrongly motion-safe-stopped indicator', async () => {
  const { container } = render(<>
    <span id="moving" className="animate-spin" />
    <span id="wrong-state" className="animate-spin motion-safe:animate-none" />
    <span id="static-marker" data-settings-row-target="" style={{ animation: 'none' }} />
    <span id="variant-only" className="motion-reduce:animate-none" />
    <span id="explicitly-static" className="animate-none" />
  </>)
  const rows = await motionSweepU4('instrument:per-site-normal', container)
  expect(rows.filter((row) => !isStaticU4(row.normal)).map(({ id }) => id),
    'control: the old at-least-one-mover check is satisfied by a real compiled mover').toEqual(['moving'])
  expect(rows.map(({ id, normalPass }) => ({ id, normalPass }))).toEqual([
    { id: 'moving', normalPass: true },
    { id: 'wrong-state', normalPass: false },
    { id: 'static-marker', normalPass: true },
    { id: 'variant-only', normalPass: true },
    { id: 'explicitly-static', normalPass: true },
  ])
})

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

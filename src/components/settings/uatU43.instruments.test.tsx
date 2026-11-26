// Part-43 instruments: independent synthetic CSS / real DOM, never production mutants.
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { parse } from 'postcss'
import { touchSweepU4 } from '../../../tests/helpers/settings-u4-sweep'
import { centeredLinkClearanceU43, declarationsU43, heightU43, pixelsU43 } from '../../../tests/helpers/settings-u43-css'

import { observeEmptyU43, pointerU43 } from '../../../tests/helpers/settings-u43-popups'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
const HEIGHT = 'pointer-coarse:min-h-[var(--target-touch-minimum)]'

// No w-28 exception: ordinary numeric widths/minima use the compiler's scale.
// w-16 is 4rem (56px at this 14px root); w-8 is 2rem (28px).
// Finite JSX fixtures keep every class visible to the design-system scanners.
it.each([
  ['fixed nonshrinking', <input key="fixed" aria-label="Numeric width" className={`${HEIGHT} w-16 shrink-0`} />, true],
  ['another fixed nonshrinking width', <input key="another" aria-label="Numeric width" className={`${HEIGHT} w-20 shrink-0`} />, true],
  ['fixed but shrinkable', <input key="shrinkable" aria-label="Numeric width" className={`${HEIGHT} w-16`} />, false],
  ['protected by a sufficient minimum', <input key="protected" aria-label="Numeric width" className={`${HEIGHT} w-16 min-w-16`} />, true],
  ['minimum only', <input key="minimum" aria-label="Numeric width" className={`${HEIGHT} min-w-16`} />, true],
  ['undersized fixed width', <input key="small" aria-label="Numeric width" className={`${HEIGHT} w-8 shrink-0`} />, false],
  ['undersized fixed width and minimum', <input key="small-minimum" aria-label="Numeric width" className={`${HEIGHT} w-8 min-w-8`} />, false],
  ['small minimum does not prevent shrinking', <input key="shrinkable-minimum" aria-label="Numeric width" className={`${HEIGHT} w-16 min-w-8`} />, false],
] as const)('R42-W1 width instrument — %s', async (name, ui, expected) => {
  render(<div className="flex">{ui}</div>)
  const rows = await touchSweepU4(`r43:width:${name}`)
  expect(rows.map(({ name: label, height, width, pass }) => ({ label, height, width, pass }))).toEqual([
    { label: 'Numeric width', height: true, width: expected, pass: expected },
  ])
})

it.each([
  [43, <a key="43" href="#boundary" className={`flex shrink-0 w-[43px] ${HEIGHT}`}>Boundary action</a>],
  [44, <a key="44" href="#boundary" className={`flex shrink-0 w-[44px] ${HEIGHT}`}>Boundary action</a>],
  [45, <a key="45" href="#boundary" className={`flex shrink-0 w-[45px] ${HEIGHT}`}>Boundary action</a>],
] as const)('R42-W1 boundary — an ordinary fixed block width at %ipx is credited iff >=44', async (width, ui) => {
  render(<div>{ui}</div>)
  const rows = await touchSweepU4(`r43:fixed-width-boundary:${width}`)
  expect(rows.map(({ width: passWidth, pass }) => ({ passWidth, pass }))).toEqual([{ passWidth: width >= 44, pass: width >= 44 }])
})

// Known arithmetic, aliases and missing/cyclic variables test the measuring instrument.
it.each([
  ['calc(var(--spacing) * 16)', 56], ['calc(var(--spacing) * 8)', 28],
  ['var(--alias)', 44], ['max(12px, 0.75rem)', 12], ['2rem', 28],
  ['var(--missing)', null], ['var(--cycle)', null], ['100%', null], ['auto', null],
] as const)('U6 CSS instrument resolves %s without guessing', (value, expected) => {
  const css = parse(':root { --spacing:0.25rem; --touch:44px; --alias:var(--touch); --cycle:var(--cycle) }')
  expect(pixelsU43(css, value)).toBe(expected)
})

it.each([43, 44, 45])('U6-1 CSS instrument — independent 44px height boundary at %ipx', (height) => {
  render(<a href="#option" className="option" data-testid="height-probe">Option</a>)
  const css = parse(`.option { min-height:20px; @media (pointer:coarse) { min-height:${height}px } }`)
  expect(heightU43(css, screen.getByTestId('height-probe'), { width: 390, pointer: 'coarse' }).guarantee).toBe(height)
  expect((heightU43(css, screen.getByTestId('height-probe'), { width: 390, pointer: 'coarse' }).guarantee ?? 0) >= 44).toBe(height >= 44)
  expect(heightU43(css, screen.getByTestId('height-probe'), { width: 390, pointer: 'fine' }).guarantee).toBe(20)
})

it('U6 CSS instrument does not borrow fine or unrelated rules; inline virtual slots remain real inputs', () => {
  render(<a href="#option" className="option" data-testid="media-probe" style={{ height: 32 }}>Option</a>)
  const css = parse('.other { min-height:100px } @media (pointer:fine) { .option { min-height:44px } }')
  const probe = screen.getByTestId('media-probe')
  expect(heightU43(css, probe, { width: 390, pointer: 'coarse' })).toEqual({ height: 32, minimum: null, guarantee: 32 })
  expect(heightU43(css, probe, { width: 390, pointer: 'fine' })).toEqual({ height: 32, minimum: 44, guarantee: 44 })
  expect(declarationsU43(css, probe, { width: 390, pointer: 'coarse' })['min-height']).toBeUndefined()
})

// First-principles centered-em model: (44 - 12) / 2 = 16px of upward expansion.
// This instrument does not certify font metrics, wrapping, or physical hit testing.
it.each([[15, -1], [16, 0], [17, 1]] as const)('U6-2 centered-link instrument — gap %ipx gives clearance %ipx', (gap, expected) => {
  render(<p className="line"><a href="#limits" className="link" data-testid="clearance-probe">Limits</a></p>)
  const css = parse(`.line { margin-top:${gap}px; font-size:12px } .link { position:relative }
    @media (pointer:coarse) { .link::before { position:absolute; top:50%; transform:translate(-50%, -50%); pointer-events:auto; height:max(100%,44px) } }`)
  const { font, ...result } = centeredLinkClearanceU43(css, screen.getByTestId('clearance-probe'), { width: 390, pointer: 'coarse' })
  expect(font, 'synthetic font-size is exactly 12px, not an inline font shorthand').toBe(12)
  expect(result).toEqual({ centered: true, margin: gap, minimum: 44, clearance: expected })
  expect(centeredLinkClearanceU43(css, screen.getByTestId('clearance-probe'), { width: 390, pointer: 'fine' }).clearance).toBeNull()
})

it.each([true, false])('U6 browser instrument — pointer coarse=%s has a complementary fine answer and no captured pointer', (coarse) => {
  pointerU43(coarse)
  expect(window.matchMedia('(pointer: coarse)').matches).toBe(coarse)
  expect(window.matchMedia('(pointer: fine)').matches).toBe(!coarse)
  expect(document.body.hasPointerCapture(1)).toBe(false)
})

it('U6-3 observer instrument distinguishes a connected empty message from a node removed before batch delivery', async () => {
  const view = render(<div data-testid="observer-root" />)
  const observer = observeEmptyU43(view.container)
  await act(async () => { view.rerender(<div cmdk-empty="">No models found.</div>) })
  expect(observer.seen, 'instrument must actually see an empty message before trusting an empty journal').toContain('No models found.')
  observer.stop()
  view.unmount()
  const transient = render(<div data-testid="transient-root" />)
  const second = observeEmptyU43(transient.container)
  await act(async () => {
    const node = document.createElement('div')
    node.setAttribute('cmdk-empty', '')
    node.textContent = 'No models found.'
    transient.container.append(node)
    node.remove()
  })
  expect(second.seen, 'detached pre-delivery nodes are not a visible flash claim').toEqual([])
  second.stop()
})

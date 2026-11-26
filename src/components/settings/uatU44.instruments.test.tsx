// Positive/negative instruments for the exact structural checks; no product mutants.
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { parse } from 'postcss'
import { centeredXYU44, declarationsU44, popupSizingU44, recommendationU44, wrapsLabelU44 } from '../../../tests/helpers/settings-u44-css'
import { navigationU44 } from '../../../tests/helpers/settings-u44-navigation'

const env = { width: 390, pointer: 'coarse' } as const
const starting = { href: window.location.href, state: window.history.state }
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); window.history.replaceState(starting.state, '', starting.href) })

it.each([
  ['complete', 'width:max-content;min-width:max(8rem,var(--radix-popover-trigger-width));max-width:var(--radix-popover-content-available-width)', { longestLabel: true, triggerAndEightRem: true, availableWidthCap: true }],
  ['trigger only, not longest label', 'width:var(--radix-popover-trigger-width);min-width:8rem', { longestLabel: false, triggerAndEightRem: false, availableWidthCap: false }],
  ['missing trigger floor', 'width:max-content;min-width:8rem;max-width:var(--radix-popover-content-available-width)', { longestLabel: true, triggerAndEightRem: false, availableWidthCap: true }],
  ['missing 8rem floor', 'width:max-content;min-width:var(--radix-popover-trigger-width);max-width:var(--radix-popover-content-available-width)', { longestLabel: true, triggerAndEightRem: false, availableWidthCap: true }],
  ['missing available-width cap', 'width:max-content;min-width:max(8rem,var(--radix-popover-trigger-width))', { longestLabel: true, triggerAndEightRem: true, availableWidthCap: false }],
] as const)('U44 popup-width instrument — %s', (_name, declarations, expected) => {
  render(<div data-testid="probe" className="probe" />)
  expect(popupSizingU44(parse(`.probe { ${declarations} }`), screen.getByTestId('probe'), env)).toEqual(expected)
})

it.each([
  ['wrapping', 'min-width:0px;overflow-wrap:anywhere', true],
  ['clipped', 'min-width:0px;overflow-wrap:anywhere;overflow:hidden;text-overflow:ellipsis', false],
  ['nowrap', 'min-width:0px;overflow-wrap:anywhere;white-space:nowrap', false],
  ['no breaking rule', 'min-width:0px', false],
] as const)('U44 label instrument — %s', (_name, declarations, expected) => {
  render(<div data-testid="popup"><span data-testid="probe" className="probe">AnUnbrokenLabel</span></div>)
  expect(wrapsLabelU44(parse(`.probe { ${declarations} }`), screen.getByTestId('probe'), screen.getByTestId('popup'), env)).toBe(expected)
})

it.each([49, 50, 51])('U44 model-name minimum instrument — %i percent preserves at least half exactly at 50', (minimum) => {
  render(<div><span className="name" data-testid="name">Distinct model</span><span className="badge" data-testid="badge">Recommended</span></div>)
  const css = parse(`.name{min-width:${minimum}%}.badge{min-width:0px;overflow:hidden;text-overflow:ellipsis}`)
  expect(recommendationU44(css, screen.getByTestId('name'), screen.getByTestId('badge'), env)).toEqual({ nameKeepsHalf: minimum >= 50, badgeYields: true })
})

it('U44 badge instrument — a nonshrinking badge cannot certify the name is protected', () => {
  render(<div><span className="name" data-testid="name">Distinct model</span><span className="badge" data-testid="badge">Recommended</span></div>)
  expect(recommendationU44(parse('.name{min-width:50%}.badge{min-width:0px;overflow:hidden;text-overflow:ellipsis;flex-shrink:0}'), screen.getByTestId('name'), screen.getByTestId('badge'), env)).toEqual({ nameKeepsHalf: true, badgeYields: false })
})

it.each([
  ['centered', 'left:50%;top:50%;transform:translate(-50%, -50%)', { left: '50%', top: '50%', transform: 'translate(-50%, -50%)' }],
  ['wrong horizontal anchor', 'left:0;top:50%;transform:translate(-50%, -50%)', { left: '0', top: '50%', transform: 'translate(-50%, -50%)' }],
  ['wrong vertical anchor', 'left:50%;top:0;transform:translate(-50%, -50%)', { left: '50%', top: '0', transform: 'translate(-50%, -50%)' }],
  ['missing transform', 'left:50%;top:50%', { left: '50%', top: '50%', transform: null }],
] as const)('U44 link XY instrument — %s remains distinguishable', (_name, coordinates, expected) => {
  render(<a className="link" data-testid="probe" href="#probe">Probe</a>)
  const css = parse(`.link{position:relative}.link::before{position:absolute;pointer-events:auto;${coordinates}}`)
  expect(centeredXYU44(css, screen.getByTestId('probe'), env)).toEqual({ anchored: true, interactive: true, ...expected })
})

it('U44 cascade instrument — wrong media/selector cannot certify; nested and inline declarations really apply', () => {
  render(<div data-testid="probe" className="probe" />)
  const target = screen.getByTestId('probe')
  const css = parse('.other{width:max-content}@media(pointer:fine){.probe{width:max-content}}.probe{&:not([hidden]){min-width:8rem}}')
  expect(declarationsU44(css, target, env).width).toBeUndefined()
  expect(declarationsU44(css, target, env)['min-width']).toBe('8rem')
  target.style.setProperty('width', 'max-content')
  expect(declarationsU44(css, target, env).width).toBe('max-content')
})

it('U44 universal-pseudo instrument — bare ::before applies to every originating element and never to its normal box', () => {
  render(<a data-testid="probe" className="probe" href="#probe">Probe</a>)
  const target = screen.getByTestId('probe')
  const css = parse('::before { left:0;top:0;position:absolute }.probe::before { left:50% }')
  expect(declarationsU44(css, target, env, 'before')).toEqual({ left: '50%', top: '0', position: 'absolute' })
  expect(declarationsU44(css, target, env)).toEqual({})
})

// CSS cascade oracle: MDN /en-US/docs/Web/CSS/important, read in part44.
// Normal inline must yield to author !important; important inline wins.
it.each([
  ['normal inline cannot override authored important width', '', 'var(--radix-popover-trigger-width)', false],
  ['important inline overrides authored important width', 'important', 'max-content', true],
] as const)('U44 inline/important instrument — %s', (_name, priority, width, intrinsic) => {
  render(<div data-testid="probe" className="probe" />)
  const target = screen.getByTestId('probe')
  target.style.setProperty('width', 'max-content', priority)
  const css = parse('.probe{width:var(--radix-popover-trigger-width)!important;min-width:max(8rem,var(--radix-popover-trigger-width));max-width:var(--radix-popover-content-available-width)}')
  expect(declarationsU44(css, target, env).width, 'CSS important author width defeats normal inline width, never important inline width').toBe(width)
  expect(popupSizingU44(css, target, env), 'a normal inline width must not falsely certify intrinsic popup sizing').toEqual({ longestLabel: intrinsic, triggerAndEightRem: true, availableWidthCap: true })
})

it('U44 vendor-selector instrument — unsupported vendor pseudo rules never certify popup width', () => {
  render(<div data-testid="probe" className="probe" />)
  const css = parse('.probe:-moz-focusring{width:max-content}.probe::-webkit-details-marker{width:max-content}')
  expect(declarationsU44(css, screen.getByTestId('probe'), env).width).toBeUndefined()
  expect(popupSizingU44(css, screen.getByTestId('probe'), env)).toEqual({ longestLabel: false, triggerAndEightRem: false, availableWidthCap: false })
})

it('U44 P2 Navigation instrument — eager active key/index and back/forward capability follow native traversals, replace and typed entries', async () => {
  window.history.replaceState({}, '', '/#p2-a')
  const stub = navigationU44()
  try {
    const first = stub.navigation.currentEntry.key
    window.history.pushState({}, '', '/#p2-b')
    const second = stub.navigation.currentEntry.key
    window.history.replaceState({ retained: true }, '', '/#p2-b-replaced')
    expect(stub.navigation.currentEntry.key).toBe(second)
    window.history.pushState({}, '', '/#p2-c')
    const eager: number[] = []
    const observe = () => eager.push(stub.navigation.currentEntry.index)
    window.addEventListener('popstate', observe)
    try {
      await act(async () => { window.history.go(-2); await new Promise<void>((resolve) => window.addEventListener('popstate', () => resolve(), { once: true })) })
      expect(stub.navigation.currentEntry.key).toBe(first)
      expect(eager).toEqual([0])
      expect(stub.navigation.canGoBack).toBe(false)
      expect(stub.navigation.canGoForward).toBe(true)
      await act(async () => { window.location.hash = 'p2-typed'; await new Promise<void>((resolve) => window.addEventListener('hashchange', () => resolve(), { once: true })) })
      expect(window.history.state).toBeNull()
      expect(stub.navigation.entries().map(({ url }) => new URL(url).hash)).toEqual(['#p2-a', '#p2-typed'])
      expect(stub.navigation.currentEntry.index).toBe(1)
      expect(stub.navigation.canGoBack).toBe(true)
      expect(stub.navigation.canGoForward).toBe(false)
    } finally { window.removeEventListener('popstate', observe) }
  } finally { stub.restore() }
})

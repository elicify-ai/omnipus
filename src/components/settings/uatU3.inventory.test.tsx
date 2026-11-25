// U3-5: US-9 phone reflow, fix-round-u3.md, D-9 id3 Connect picker.
// The long name is the UAT input, not an expected value copied from the picker.
// Kit containment contracts are min-w-0/truncate (FilterMenu, SheetTitle) or
// whitespace-normal + word wrapping (EffortRadioList / FilterMenu options).
// jsdom cannot prove 390px clipping or 14px-root layout; re-test in a browser.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Button } from '@/components/ui/button'
import { catalogG1, jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { tileLabelU3 } from '../../../tests/helpers/settings-u3-contracts'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

// Instrument: every valid kit mechanism is accepted; the reported defect and the
// near-misses that still overflow a long token in a browser are rejected. The class
// strings are literals in JSX so the design-system scanners can read every one.
const LABEL = 'Long instrument label'
const INSTRUMENT = [
  { name: 'wrap-anywhere on the tile text', expected: { canShrink: true, contained: true },
    ui: <div className="grid grid-cols-4"><Button className="whitespace-normal wrap-anywhere">{LABEL}</Button></div> },
  { name: 'break-all on the tile text', expected: { canShrink: true, contained: true },
    ui: <div className="grid grid-cols-4"><Button className="whitespace-normal break-all">{LABEL}</Button></div> },
  { name: 'min-w-0 truncate label span', expected: { canShrink: true, contained: true },
    ui: <div className="grid grid-cols-4"><Button className="min-w-0"><span className="min-w-0 truncate">{LABEL}</span></Button></div> },
  { name: 'truncate label span (an overflow-hidden flex item shrinks)', expected: { canShrink: true, contained: true },
    ui: <div className="grid grid-cols-4"><Button><span className="truncate">{LABEL}</span></Button></div> },
  { name: 'line-clamp label span', expected: { canShrink: true, contained: true },
    ui: <div className="grid grid-cols-4"><Button><span className="min-w-0 line-clamp-2 whitespace-normal">{LABEL}</span></Button></div> },
  { name: 'shrinkable label span with break-words', expected: { canShrink: true, contained: true },
    ui: <div className="grid grid-cols-4"><Button><span className="min-w-0 whitespace-normal break-words">{LABEL}</span></Button></div> },
  { name: 'break-words on the tile text keeps the long word as min-content', expected: { canShrink: true, contained: false },
    ui: <div className="grid grid-cols-4"><Button className="whitespace-normal break-words">{LABEL}</Button></div> },
  { name: 'break-words label span that cannot shrink', expected: { canShrink: true, contained: false },
    ui: <div className="grid grid-cols-4"><Button><span className="whitespace-normal break-words">{LABEL}</span></Button></div> },
  { name: 'normal wrapping alone cannot break one long token', expected: { canShrink: true, contained: false },
    ui: <div className="grid grid-cols-4"><Button className="whitespace-normal">{LABEL}</Button></div> },
  { name: 'inherited nowrap with visible overflow (the reported defect)', expected: { canShrink: true, contained: false },
    ui: <div className="grid grid-cols-4"><Button>{LABEL}</Button></div> },
  { name: 'min-w-0 alone is not containment', expected: { canShrink: true, contained: false },
    ui: <div className="grid grid-cols-4"><Button className="min-w-0">{LABEL}</Button></div> },
  { name: 'a tile outside a grid track needs min-w-0 to shrink', expected: { canShrink: false, contained: true },
    ui: <div className="flex"><Button className="whitespace-normal wrap-anywhere">{LABEL}</Button></div> },
  { name: 'min-w-0 lets a tile outside a grid track shrink', expected: { canShrink: true, contained: true },
    ui: <div className="flex"><Button className="min-w-0 whitespace-normal wrap-anywhere">{LABEL}</Button></div> },
]

it.each(INSTRUMENT)('U3-5 instrument — $name', ({ name, ui, expected }) => {
  render(ui)
  const tile = screen.getByRole('button', { name: LABEL })
  // Matches the element whose own text is the label: the tile itself for direct text, the span otherwise.
  const label = within(tile).getByText(LABEL)
  expect(tileLabelU3(tile, label), name).toEqual(expected)
})

it('U3-5 — Connect picker MiniMax (minimax.io) label wraps or truncates within its shrinkable tile', async () => {
  const t = transportG1()
  // CatalogProvider.company is the grouping/label input. The rest of this
  // synthetic entry reuses a generated-type-checked, already valid fixture.
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: [
    ...catalogG1.providers,
    { ...catalogG1.providers[0], id: 'minimax', name: 'MiniMax (minimax.io)', company: 'MiniMax (minimax.io)', api: 'https://example.com/minimax/v1' },
  ] }))
  sectionG1('providers')
  await userEvent.click(await screen.findByRole('button', { name: 'Connect a provider' }))
  const picker = await screen.findByRole('dialog', { name: 'Connect a provider' })
  const popular = within(picker).getByRole('group', { name: 'Popular providers' })
  const tile = within(popular).getByRole('button', { name: 'MiniMax (minimax.io)' })
  const label = within(popular).getByText('MiniMax (minimax.io)', { exact: true })
  expect(tile.contains(label), 'reachability: the exact label belongs to the actual Popular tile').toBe(true)
  expect(tile).toBeVisible()
  expect(label).toBeVisible()
  expect(t.writes()).toEqual([])
  expect(tileLabelU3(tile, label),
    'U3-5/US-9: the long label needs a wrap or truncate contract inside its tile. Accepted: truncate or line-clamp-N, '
    + 'or whitespace-normal with wrap-anywhere/break-all (break-words only on a separate min-w-0 label); '
    + `tile=${tile.className}; label=${label.className}`).toEqual({ canShrink: true, contained: true })
})

// U2-1: settings-redesign-spec.md::US-9 AS5 / FR-070 / phone touch scenario.
// Real kit + screen DOM and shipped CSS, NOT a browser size/overlap/reflow proof.
import { parse } from 'postcss'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Select, SelectTrigger, SelectValue } from '@/components/ui/select'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { Switch } from '@/components/ui/switch'
import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion'
import { tokenTouchU3 } from '../../../tests/helpers/settings-u3-contracts'
import { sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { atLeastTouchU2, pseudoTouchU2, stylesheetU2, touchContractU2 } from '../../../tests/helpers/settings-u2-css'

// No getBoundingClientRect assertions: jsdom would return zeros, not touch geometry.
async function expectTargetsU2(targets: [string, HTMLElement][]) {
  const results = await Promise.all(targets.map(async ([name, control]) => ({
    name, ...await touchContractU2(control), classes: control.className,
  })))
  expect(results.filter((result) => !result.pass),
    'U2-1/FR-070: every named control needs its own 44x44 coarse-pointer size or selector-matched shipped hit area').toEqual([])
}

function coarsePointerU2() {
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: media === '(pointer: coarse)', media, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent: () => false,
  }))
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('U2-1 — touch-contract instrument controls', () => {
  it.each([[43, false], [44, true], [45, true]] as const)(
    'the FR-070 threshold accepts %i px exactly when it reaches 44', (size, expected) => {
      // Independent min-1/min/min+1 boundary; synthetic CSS tests the instrument.
      const css = parse(`:root { --test-touch: ${size}px; }`)
      expect(atLeastTouchU2('var(--test-touch)', css)).toBe(expected)
      expect(atLeastTouchU2('var(--missing-size)', css)).toBe(false)
    },
  )
  it('T4-4 — the existing U2 matcher rejects unscoped minima and accepts the coarse-pointer contract', async () => {
    // Synthetic native fields avoid Button's independent shared minimum and pseudo credit.
    // Rule 12: a mouse-screen 44px minimum is not the coarse-pointer contract.
    render(<>
      <input aria-label="Unscoped instrument" className="h-[44px] w-[44px]" />
      <input aria-label="Coarse instrument" className="pointer-coarse:h-[44px] pointer-coarse:w-[44px]" />
    </>)
    expect(await touchContractU2(screen.getByRole('textbox', { name: 'Unscoped instrument' }))).toEqual({
      pass: false, height: false, width: false, pseudo: false,
    })
    expect(await touchContractU2(screen.getByRole('textbox', { name: 'Coarse instrument' }))).toEqual({
      pass: true, height: true, width: true, pseudo: false,
    })
  })
  it('a Switch-only expander cannot falsely certify an ordinary Button', () => {
    render(<><Button>Ordinary action</Button><Switch aria-label="Example switch" /></>)
    const css = parse(`
      [role='switch'][data-ds-action]::before {
        content: ''; position: absolute; pointer-events: auto;
        width: 24px; height: 24px;
      }
      @media (pointer: coarse) {
        [role='switch'][data-ds-action]::before { width: 44px; height: 44px; }
      }
    `)
    expect(pseudoTouchU2(css, screen.getByRole('switch', { name: 'Example switch' }))).toBe(true)
    expect(pseudoTouchU2(css, screen.getByRole('button', { name: 'Ordinary action' }))).toBe(false)
  })
  it('the actual library expander is recognized without mistaking it for the application entry', async () => {
    render(<Button>Library action</Button>)
    expect(pseudoTouchU2(await stylesheetU2('library.css'), screen.getByRole('button', { name: 'Library action' }))).toBe(true)
    expect(atLeastTouchU2('var(--target-touch-minimum)', await stylesheetU2())).toBe(true)
  })
})

describe('U2-1 — shared controls keep the 44x44 coarse-pointer contract', () => {
  it.each(['default', 'sm', 'lg'] as const)('Button %s has a real touch contract', async (size) => {
    coarsePointerU2()
    render(<Button size={size}>Shared action</Button>)
    await expectTargetsU2([[`Button ${size}`, screen.getByRole('button', { name: 'Shared action' })]])
  })
  it.each(['default', 'sm', 'lg'] as const)('IconButton %s has a real touch contract', async (size) => {
    coarsePointerU2()
    render(<IconButton size={size} aria-label="Shared icon action"><span aria-hidden="true">x</span></IconButton>)
    await expectTargetsU2([[`IconButton ${size}`, screen.getByRole('button', { name: 'Shared icon action' })]])
  })
  it.each(['field', 'ghost'] as const)('Select %s keeps its touch minimum', async (variant) => {
    coarsePointerU2()
    render(<Select><SelectTrigger variant={variant} aria-label="Shared select"><SelectValue placeholder="Choose" /></SelectTrigger></Select>)
    await expectTargetsU2([[`Select ${variant}`, screen.getByRole('combobox', { name: 'Shared select' })]])
  })
  it('DisclosureRow keeps its explicit minimum and its real toggle callback', async () => {
    coarsePointerU2()
    const change = vi.fn()
    render(<div className="flex"><DisclosureRow expanded={false} expandable onExpandedChange={change}>Shared disclosure</DisclosureRow></div>)
    const disclosure = screen.getByRole('button', { name: 'Shared disclosure' })
    await expectTargetsU2([['DisclosureRow', disclosure]])
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(disclosure)
    expect(change.mock.calls).toEqual([[true]])
  })
  it('adjacent SegmentedControl items retain their explicit touch boxes', async () => {
    coarsePointerU2()
    render(<SegmentedControl aria-label="Example view" value="one" onValueChange={() => {}}>
      <SegmentedControlItem value="one">One</SegmentedControlItem>
      <SegmentedControlItem value="two">Two</SegmentedControlItem>
    </SegmentedControl>)
    await expectTargetsU2(['One', 'Two'].map((name) => [name, screen.getByRole('button', { name })]))
  })
})

describe('U2-1 — named Settings samples and previously fixed controls', () => {
  it('Gateway native selects, token/restart actions and Remote Access disclosure have touch contracts', async () => {
    const t = transportG1(); coarsePointerU2(); sectionG1('gateway')
    await screen.findByRole('spinbutton', { name: 'Port' })
    await expectTargetsU2([
      ['Bind address', screen.getByRole('combobox', { name: 'Bind address' })],
      ['Log level', screen.getByRole('combobox', { name: 'Log level' })],
      ['Rotate gateway token', screen.getByRole('button', { name: 'Rotate gateway token' })],
      ['Restart gateway', screen.getByRole('button', { name: 'Restart gateway…' })],
      ['Remote Access', screen.getByRole('button', { name: 'Remote Access' })],
    ])
    expect(t.writes()).toEqual([])
  })
  it('Providers Connect and credential actions have touch contracts', async () => {
    const t = transportG1(); coarsePointerU2(); sectionG1('providers')
    const connect = await screen.findByRole('button', { name: 'Connect a provider' })
    const edits = await screen.findAllByRole('button', { name: 'Edit key' })
    await expectTargetsU2([
      ['Connect a provider', connect],
      ...edits.map((control, index): [string, HTMLElement] => [`Edit key ${index + 1}`, control]),
    ])
    expect(t.writes()).toEqual([])
  })
  it('Sandbox actions and a newly drafted folder Delete path have touch contracts', async () => {
    const t = transportG1(); coarsePointerU2(); sectionG1('sandbox')
    const input = await screen.findByRole('textbox', { name: 'New allowed path' })
    await userEvent.type(input, '/var/data/u2-touch')
    await userEvent.click(screen.getByRole('button', { name: 'Add path' }))
    const remove = await screen.findByRole('button', { name: 'Delete path /var/data/u2-touch' })
    expect(screen.getByText('1 unsaved change')).toBeVisible()
    expect(t.writes(), 'adding a folder is draft-only before Save').toEqual([])
    await expectTargetsU2([
      ['Refresh sandbox status', screen.getByRole('button', { name: 'Refresh sandbox status' })],
      ['Show capabilities', screen.getByRole('button', { name: 'Show capabilities' })],
      ['Advanced (custom list)', screen.getByRole('button', { name: 'Advanced (custom list)' })],
      ['Add path', screen.getByRole('button', { name: 'Add path' })],
      ['Delete draft path', remove],
    ])
  })
  it('Gateway Preview server Switch and Port number input retain their successful touch contracts', async () => {
    const t = transportG1(); coarsePointerU2(); sectionG1('gateway')
    const port = await screen.findByRole('spinbutton', { name: 'Port' })
    await expectTargetsU2([
      ['Preview server Switch', screen.getByRole('switch', { name: 'Preview server' })],
      ['Port number input', port],
    ])
    expect(port).toHaveValue(5000)
    expect(t.writes()).toEqual([])
  })
})

// U3-2: the actual Recap limits disclosure is a shared AccordionTrigger.
// DisclosureRow's earlier successful contract/toggle case is retained above.
// Do not round 43.6 to 44 or infer h-11 from a 16px root: this pins the token.
// jsdom cannot prove the 14px-root arithmetic; the 390px browser re-test does.
describe('U3-2 — disclosure and accordion headers use the root-independent touch token', () => {
  it('Recap limits in real Memory & recaps has the token touch contract and still opens', async () => {
    const t = transportG1(); coarsePointerU2(); sectionG1('memory')
    const disclosure = await screen.findByRole('button', { name: /Recap limits/ })
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(disclosure)
    expect(disclosure).toHaveAttribute('aria-expanded', 'true')
    expect(t.writes()).toEqual([])
    expect(tokenTouchU3(disclosure), 'U3-2/FR-070: Recap limits must use the coarse-pointer touch-minimum token, not rem-derived height').toEqual({ height: true, width: true })
  })
  it('the six shared agent-sheet accordion headers have token-sized row targets and real toggles', async () => {
    coarsePointerU2()
    // Same shared Accordion/Trigger composition used by the agent sheet;
    // no agent data, auto-save or header component is mocked.
    const names = ['Basics', 'Personality', 'Tools', 'Skills', 'Heartbeat', 'Advanced']
    render(<Accordion type="single" collapsible>{names.map((name) => (
      <AccordionItem key={name} value={name}>
        <AccordionTrigger>{name}</AccordionTrigger>
        <AccordionContent>{name} content</AccordionContent>
      </AccordionItem>
    ))}</Accordion>)
    const headers = names.map((name) => screen.getByRole('button', { name }))
    for (const header of headers) expect(header).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(headers[0])
    expect(headers[0]).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Basics content')).toBeVisible()
    expect(headers.map((header) => ({ name: header.textContent, ...tokenTouchU3(header) })),
      'U3-2/SC-009: every shared header needs token minimum height; flex row supplies width').toEqual(
      names.map((name) => ({ name, height: true, width: true })),
    )
  })
})

// U6-1 oracles: fix-round-u6.md. Structural sizing, not a physical measurement.
// Fine-pointer equality is a CHARACTERIZATION/PRESERVATION control against the
// frozen e7779b259 receipt, not a claim that its compact sizes are correct.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen } from '@testing-library/react'
import { Input } from '@/components/ui/input'
import { Accordion, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Command, CommandInput } from '@/components/ui/command'
import { renderG1 } from '../../../tests/helpers/settings-gate-g1'
import reference from '../../../tests/fixtures/settings-u43-fine.json'
import { compiledCssU43, declarationsU43, fineSignatureU43, fixedWidthU43, heightU43 } from '../../../tests/helpers/settings-u43-css'
import { openPopupU43, POPUPS_U43 } from '../../../tests/helpers/settings-u43-popups'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); document.documentElement.style.removeProperty('--user-font-size') })

it.each(POPUPS_U43)('U6-1 — every real %s popup choice carries at least 44px coarse height', async (kind) => {
  const { items } = await openPopupU43(kind, true)
  const css = await compiledCssU43(items)
  const rows = items.map((item) => ({ name: item.textContent?.trim(), ...heightU43(css, item, { width: 390, pointer: 'coarse' }) }))
  console.info('R43_POPUP ' + JSON.stringify({ kind, pointer: 'coarse', rows }))
  expect(rows.filter((row) => row.guarantee === null || row.guarantee < 44), `${kind}/U6-1: every choice needs its OWN height guarantee >=44`).toEqual([])
})

it.each(POPUPS_U43)('U6-1 fine preservation — %s keeps its pre-U6 compact height/padding/type contract', async (kind) => {
  const { items } = await openPopupU43(kind, false)
  const css = await compiledCssU43(items)
  const rows = items.map((item) => ({ name: item.textContent?.trim() ?? '', signature: fineSignatureU43(css, item) }))
  console.info('R43_POPUP ' + JSON.stringify({ kind, pointer: 'fine', rows }))
  expect(reference.source).toBe('e7779b259cb9056e75b85c561cdb7075cb307d24')
  expect(rows, 'U6-1: exact pre/post equality; baseline is frozen BEFORE any U6 GREEN run').toEqual(reference.popups[kind as keyof typeof reference.popups])
})

// Part-46 move 4: the three new width floors are coarse-only. Test the real
// primitives and both media arms; an absent rule must fail the coarse control.
it.each(['Input', 'accordion trigger', 'command search'] as const)('U7-4 fine preservation — %s gets the touch width floor only with a coarse pointer', async (kind) => {
  if (kind === 'Input') renderG1(<Input aria-label="U46 number" type="number" data-testid="u46-width-control" />)
  else if (kind === 'accordion trigger') renderG1(<Accordion type="single" collapsible><AccordionItem value="u46"><AccordionTrigger data-testid="u46-width-control">U46 disclosure</AccordionTrigger></AccordionItem></Accordion>)
  else renderG1(<Command><CommandInput aria-label="U46 search" data-testid="u46-width-control" /></Command>)
  const control = await screen.findByTestId('u46-width-control') // renderG1 mounts a real async router
  const css = await compiledCssU43([control, control.parentElement!])
  const coarse = fixedWidthU43(css, control, { width: 390, pointer: 'coarse' })
  expect.soft(coarse.minimum, `${kind}/U7-4: an explicit coarse min-width is required, not a shrinkable preferred width`).not.toBeNull()
  expect.soft(coarse.minimum, `${kind}/FR-070: coarse min-width is at least 44px`).toBeGreaterThanOrEqual(44)
  expect.soft(coarse.pass, `${kind}/U7-4: the touch minimum cannot shrink away`).toBe(true)
  for (const width of [390, 1280]) {
    // Same-width fine control separates pointer scope from responsive width;
    // the desktop environment also covers the usual mouse-user viewport.
    const fineEnv = { width, pointer: 'fine' } as const
    const fine = fixedWidthU43(css, control, fineEnv)
    const rawFineMinimum = declarationsU43(css, control, fineEnv)['min-width']
    // Absent/auto is an inactive floor, NOT a measured 0px width. An unsupported
    // active expression remains unknown and cannot certify fine preservation.
    const noFineTouchFloor = rawFineMinimum === undefined || rawFineMinimum === 'auto'
      || (fine.minimum !== null && fine.minimum < 44)
    console.info('R46_WIDTH_SCOPE ' + JSON.stringify({ kind, width, coarse, fine, rawFineMinimum: rawFineMinimum ?? null }))
    expect.soft(noFineTouchFloor, `${kind}/U7-4: the coarse minimum-width floor must not apply to fine pointers at ${width}px`).toBe(true)
  }
})

// U6-1 oracles: fix-round-u6.md. Structural sizing, not a physical measurement.
// Fine-pointer equality is a CHARACTERIZATION/PRESERVATION control against the
// frozen e7779b259 receipt, not a claim that its compact sizes are correct.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'
import reference from '../../../tests/fixtures/settings-u43-fine.json'
import { compiledCssU43, fineSignatureU43, heightU43 } from '../../../tests/helpers/settings-u43-css'
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

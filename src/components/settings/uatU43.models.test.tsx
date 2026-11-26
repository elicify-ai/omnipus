// U6-1 / U6-3 from fix-round-u6.md. Real provider section, portal, cmdk and
// virtualizer. Only HTTP settlement and the browser's scroll-viewport size are inputs.
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import reference from '../../../tests/fixtures/settings-u43-fine.json'
import { heldG1, jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { CATALOG_U43, largeModelsU43, MODELS_U43, observeEmptyU43, openDefaultU43, pointerU43 } from '../../../tests/helpers/settings-u43-popups'

const cleanups: (() => void)[] = []
afterEach(() => { for (const stop of cleanups.splice(0)) stop(); cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

function watchEmpty() {
  const { seen, stop } = observeEmptyU43()
  cleanups.push(stop)
  return seen
}

it('U6-3 — first open while the catalog read is held says loading, then rows, never a false empty state', async () => {
  const t = transportG1()
  pointerU43(true)
  largeModelsU43(t)
  const held = heldG1()
  t.on('GET', '/providers/catalog', () => held.promise)
  const { client } = sectionG1('providers')
  await waitFor(() => expect(t.calls.filter((call) => call.path === '/providers/catalog')).toHaveLength(1))
  const observed = watchEmpty()
  const popup = await openDefaultU43()
  expect.soft(observed, 'U6-3: no transient No models found while HTTP is unresolved').toEqual([])
  expect.soft(within(popup).queryByText('No models found.')).not.toBeInTheDocument()
  expect.soft(within(popup).queryByTestId('default-model-select-loading'), 'U6-3: a held read must communicate loading').toHaveTextContent('Loading models…')
  await act(async () => held.resolve(jsonG1(CATALOG_U43)))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  await waitFor(() => expect(within(popup).getByTestId(`default-model-option-${MODELS_U43[0]}`)).toBeVisible())
  expect(observed, 'U6-3: no false empty flash during loading -> rows').toEqual([])
  expect(within(popup).queryByText('Loading models…')).not.toBeInTheDocument()
  expect(t.writes()).toEqual([])
})

it('U6-3 — first open with an already loaded catalog renders real virtual rows without No models found', async () => {
  const t = transportG1()
  pointerU43(true)
  largeModelsU43(t)
  const { client } = sectionG1('providers')
  await screen.findByTestId('default-model-model')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const observed = watchEmpty()
  const popup = await openDefaultU43()
  expect.soft(observed, 'U6-3: real portal first mount cannot flash empty').toEqual([])
  expect.soft(within(popup).queryByText('No models found.')).not.toBeInTheDocument()
  await waitFor(() => expect(within(popup).getByTestId(`default-model-option-${MODELS_U43[0]}`)).toBeVisible())
  expect(within(popup).getByTestId('model-selector-virtual-list').children.length).toBeGreaterThan(1)
  expect(observed).toEqual([])
  expect(t.writes()).toEqual([])
})

it('U6-3 negative control — a completed nonmatching search still says No models found', async () => {
  const t = transportG1()
  pointerU43(true)
  largeModelsU43(t)
  const { client } = sectionG1('providers')
  await screen.findByTestId('default-model-model')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const popup = await openDefaultU43()
  await userEvent.type(within(popup).getByPlaceholderText('Search models...'), 'no-match-u43-zzzz')
  expect(await within(popup).findByText('No models found.')).toBeVisible()
  expect(within(popup).queryAllByRole('option')).toEqual([])
  expect(within(popup).queryByText('Loading models…')).not.toBeInTheDocument()
  expect(t.writes()).toEqual([])
})

it.each([true, false])('U6-1 — actual virtual default-model slots do not overlap on coarse=%s; fine preserves its baseline', async (coarse) => {
  const t = transportG1()
  pointerU43(coarse)
  largeModelsU43(t)
  const { client } = sectionG1('providers')
  await screen.findByTestId('default-model-model')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const popup = await openDefaultU43()
  // A genuine matching search is a user action, not a virtualizer rerender spy.
  // It isolates slot sizing from the separately tested first-open mounting bug.
  await userEvent.type(within(popup).getByPlaceholderText('Search models...'), 'u43-model')
  const list = await within(popup).findByTestId('model-selector-virtual-list')
  await waitFor(() => expect(list.querySelectorAll('[role="option"]').length).toBeGreaterThan(1))
  const rows = [...list.children].map((child) => {
    const row = child as HTMLElement
    return { name: row.textContent?.trim(), y: Number.parseFloat(/translateY\(([-\d.]+)px\)/.exec(row.style.transform)?.[1] ?? 'NaN'), height: Number.parseFloat(row.style.height) }
  })
  console.info('R43_SLOTS ' + JSON.stringify({ coarse, rows }))
  expect(rows.filter((row) => !Number.isFinite(row.y) || !Number.isFinite(row.height) || row.height <= 0), 'instrument: actual inline row geometry, not invented rectangles').toEqual([])
  expect(rows.slice(1).filter((row, index) => row.y < rows[index].y + rows[index].height), 'U6-1: allocated rows cannot overlap').toEqual([])
  if (coarse) {
    expect(rows.filter((row) => row.height < 44), 'U6-1: actual coarse virtual slot height >=44').toEqual([])
    expect(rows.slice(1).filter((row, index) => row.y - rows[index].y < 44), 'U6-1: actual coarse row origins >=44 apart').toEqual([])
  } else {
    // Relational characterization ONLY; no imported production size constant.
    expect(rows).toEqual(reference.virtualRows)
  }
  expect(t.writes()).toEqual([])
})

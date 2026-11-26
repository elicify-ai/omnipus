// U6-6/7/9 independent oracles: fix-round-u6.md and part-44 width rules.
// Real default-model, Audit and web-search routes/portals; no mocked controls or boxes.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import { SmartSelect } from '@/components/ui/smart-select'
import { catalogG1, jsonG1, providersG1, renderG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { openDefaultU43, openPopupU43, pointerU43 } from '../../../tests/helpers/settings-u43-popups'
import { compiledCssU43 } from '../../../tests/helpers/settings-u43-css'
import { declarationsU44, popupSizingU44, recommendationU44, wrapsLabelU44 } from '../../../tests/helpers/settings-u44-css'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
const names = ['r44-provider/long-distinct-alpha-model-name', 'r44-provider/long-distinct-beta-model-name']

it.each(['coarse', 'fine'] as const)('U6-6 — recommended default-model rows keep distinct full names and make the badge yield (%s pointer)', async (pointer) => {
  const t = transportG1()
  pointerU43(pointer === 'coarse')
  const catalog: ProvidersCatalog = { ...catalogG1, providers: [{ ...catalogG1.providers[0], models: names.map((id) => ({
    ...catalogG1.providers[0].models[0], id, name: id, context_window: 256000, release_date: '2026-11-01',
  })) }] }
  t.on('GET', '/providers', () => jsonG1([{ ...providersG1[0], models: names }]))
  t.on('GET', '/providers/catalog', () => jsonG1(catalog))
  t.on('GET', '/providers/default-model', () => jsonG1({ provider: 'openrouter', model: names[0], reasoning_effort: 'low', context_window: 256000, window_source: 'catalog' }))
  const view = sectionG1('providers')
  await waitFor(() => expect(view.client.isFetching()).toBe(0))
  const popup = await openDefaultU43()
  const rows = within(popup).getAllByRole('option')
  expect(rows, 'instrument: cover both distinct recommended choices').toHaveLength(2)
  const css = await compiledCssU43([popup, ...popup.querySelectorAll('*')])
  const env = { width: pointer === 'coarse' ? 390 : 1280, pointer }
  expect.soft(declarationsU44(css, popup, env).width, 'U6-6: valid trigger-width declaration, not an invalid bare custom-property name').toBe('var(--radix-popover-trigger-width)')
  for (const id of names) {
    const text = within(popup).getByText(id, { exact: true })
    const row = text.closest<HTMLElement>('[role="option"]')!
    expect(row).toBeVisible()
    expect(row).toHaveTextContent(id)
    expect(row).toHaveAccessibleName(new RegExp(id))
    const badge = within(row).getByText(/^Recommended\b/)
    expect(badge, 'badge never takes the name node').not.toBe(text)
    expect(text.textContent, 'U6-6: name is real row content, not badge-only').toBe(id)
    expect.soft(text, 'U6-6: full exact name remains available when visual text truncates').toHaveAttribute('title', id)
    expect.soft(recommendationU44(css, text, badge, env), 'U6-6: name retains at least half the row and the badge shrinks/truncates first').toEqual({ nameKeepsHalf: true, badgeYields: true })
  }
  expect(t.writes()).toEqual([])
})

it('U6-7 — actual Audit event popup sizes to the longest label with trigger/8rem floors and an available-width cap; labels can wrap', async () => {
  const { popup, items, t } = await openPopupU43('audit-events', true)
  const css = await compiledCssU43([popup, ...popup.querySelectorAll('*')])
  const env = { width: 390, pointer: 'coarse' } as const
  expect.soft(popupSizingU44(css, popup, env), 'U6-7 exact popup width contract').toEqual({ longestLabel: true, triggerAndEightRem: true, availableWidthCap: true })
  expect(items.length, 'instrument: searchable event list really offers several labels').toBeGreaterThan(5)
  for (const option of items) {
    const label = [...option.querySelectorAll('span')].find((span) => span.textContent?.trim() && !span.querySelector('svg'))!
    expect(label, 'instrument: actual non-icon option label').toBeDefined()
    expect.soft(wrapsLabelU44(css, label, popup, env), `U6-7 actual label wraps instead of clipping: ${label.textContent}`).toBe(true)
  }
  expect(t.writes()).toEqual([])
})

it('U6-7 — shared searchable select also keeps a long unbroken label readable without truncating its option', async () => {
  const t = transportG1()
  pointerU43(true)
  const label = 'R44AnUnbrokenLabelThatCannotFitInsideANarrowTriggerWithoutWrapping'
  const items = [label, 'Alpha', 'Beta', 'Gamma', 'Delta', 'Epsilon'].map((name) => ({ value: name, label: name }))
  renderG1(<SmartSelect ariaLabel="R44 long-label choice" items={items} value="Alpha" onValueChange={vi.fn()} />)
  await userEvent.click(await screen.findByRole('combobox', { name: 'R44 long-label choice' }))
  const popup = await screen.findByRole('dialog')
  expect(within(popup).getAllByRole('option')).toHaveLength(items.length)
  const text = within(popup).getByText(label, { exact: true })
  const css = await compiledCssU43([popup, ...popup.querySelectorAll('*')])
  const env = { width: 390, pointer: 'coarse' } as const
  expect.soft(popupSizingU44(css, popup, env)).toEqual({ longestLabel: true, triggerAndEightRem: true, availableWidthCap: true })
  expect(text.textContent).toBe(label)
  expect.soft(wrapsLabelU44(css, text, popup, env), 'U6-7: unbroken words can wrap, never an ellipsis-only option').toBe(true)
  expect(t.writes()).toEqual([])
})

it.each(['search-default', 'search-fallback'] as const)('U6-9 — the actual %s popup uses the valid trigger-width expression', async (kind) => {
  const { popup, items, t } = await openPopupU43(kind, true)
  const css = await compiledCssU43([popup, ...popup.querySelectorAll('*')])
  expect(items.map((item) => item.textContent?.trim()).filter(Boolean).length, 'instrument: inspect a populated live popup').toBeGreaterThan(1)
  expect(declarationsU44(css, popup, { width: 390, pointer: 'coarse' }).width, 'U6-9: width takes the trigger, never invalid --radix-popover-trigger-width').toBe('var(--radix-popover-trigger-width)')
  expect(t.writes()).toEqual([])
})

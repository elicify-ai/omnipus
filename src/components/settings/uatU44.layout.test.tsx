// U5-C4 / U6-2: actual DOM + shipped CSS at 390 coarse. Physical boxes remain UAT.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { pointerU43 } from '../../../tests/helpers/settings-u43-popups'
import { centeredLinkClearanceU43, compiledCssU43, fixedWidthU43, heightU43 } from '../../../tests/helpers/settings-u43-css'
import { centeredXYU44, declarationsU44 } from '../../../tests/helpers/settings-u44-css'
import { inlineTouchU5 } from '../../../tests/helpers/settings-u5-css'

const env = { width: 390, pointer: 'coarse' } as const
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('U5-C4 — provider Edit Cancel/Save share one nonwrapping right-aligned row and both retain >=44px coarse targets', async () => {
  const t = transportG1()
  pointerU43(true)
  sectionG1('providers')
  await userEvent.click(await screen.findByTestId('configure-btn-openrouter'))
  const sheet = await screen.findByRole('dialog', { name: 'OpenRouter' })
  const cancel = within(sheet).getByRole('button', { name: /^Cancel$/ })
  const save = within(sheet).getByRole('button', { name: /^Save & Connect$/ })
  const group = cancel.parentElement!
  expect(save.parentElement, 'U5-C4: Cancel and Save stay together, never two independent footer rows').toBe(group)
  expect(within(group).getAllByRole('button').map((button) => button.textContent?.trim()), 'U5-C4: exact action pair and order').toEqual(['Cancel', 'Save & Connect'])
  const footer = group.parentElement!
  const remove = within(sheet).getByRole('button', { name: /^Remove provider$/ })
  const left = remove.parentElement!
  expect(group, 'ADR-068 US-3: destructive actions are separate').not.toBe(left)
  const css = await compiledCssU43([footer, left, group, cancel, save, ...footer.querySelectorAll('*')])
  const right = declarationsU44(css, group, env), leftRule = declarationsU44(css, left, env)
  expect(right.display).toBe('flex')
  expect(right['flex-direction'] ?? 'row', 'U5-C4: action group is horizontal at 390 coarse').toBe('row')
  expect(right['flex-wrap'] ?? 'nowrap', 'U5-C4: Cancel/Save cannot split onto separate lines').toBe('nowrap')
  expect(right['margin-left'], 'CHECK U5-C4: save group retains end alignment when outer footer wraps').toBe('auto')
  expect(leftRule.display).toBe('flex')
  expect(leftRule['flex-wrap'], 'CHECK U5-C4: left actions yield by wrapping').toBe('wrap')
  for (const [name, button] of [['Cancel', cancel], ['Save & Connect', save]] as const) {
    expect(heightU43(css, button, env).guarantee, `${name}: >=44 high, not merely a small visual row`).toBeGreaterThanOrEqual(44)
    expect(fixedWidthU43(css, button, env).pass, `${name}: >=44 wide despite flex shrinking`).toBe(true)
  }
  expect(t.writes()).toEqual([])
})

it('U5-C4/U6-2 — unknown-context link pins both centered axes and keeps a 44px region clear of the actual Effort control', async () => {
  const t = transportG1()
  pointerU43(true)
  t.on('GET', '/providers/default-model', () => jsonG1({ provider: 'openrouter', model: 'g1-model-a', reasoning_effort: 'low', window_unknown: true, context_window: 0 }))
  const { client } = sectionG1('providers')
  const link = await screen.findByTestId('default-model-window-unknown-link')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const effort = screen.getByTestId('default-model-effort')
  expect(link.parentElement?.previousElementSibling?.contains(effort), 'instrument: compare the immediately preceding Effort row, not a synthetic neighbor').toBe(true)
  const css = await compiledCssU43([link, link.parentElement!, effort])
  expect(centeredXYU44(css, link, env), 'CHECK U5-C4: width/height alone cannot detect a shifted interactive region').toEqual({
    anchored: true, left: '50%', top: '50%', transform: 'translate(-50%, -50%)', interactive: true,
  })
  expect(inlineTouchU5(css, link).pass, 'U6-2 never shrinks the required 44px tap region').toBe(true)
  const clearance = centeredLinkClearanceU43(css, link, env)
  expect(clearance.centered).toBe(true)
  expect(clearance.minimum).toBeGreaterThanOrEqual(44)
  expect(clearance.clearance, 'bounded centered-em clearance must be known').not.toBeNull()
  expect(clearance.clearance, 'bounded centered-em region cannot reach up into Effort').toBeGreaterThanOrEqual(0)
  const before = window.location.href
  await userEvent.click(effort)
  expect(within(await screen.findByRole('dialog')).getAllByRole('radio').length).toBeGreaterThan(0)
  expect(window.location.href, 'Effort activation is not the adjacent Limits link').toBe(before)
  expect(t.writes()).toEqual([])
})

// U6-2 / U6-4 oracles: fix-round-u6.md. Real Settings dialogs/controls.
// CSS clearance is a bounded centered-em model, not painted hit-box certification.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { compiledCssU43, centeredLinkClearanceU43, fineSignatureU43 } from '../../../tests/helpers/settings-u43-css'
import { inlineTouchU5 } from '../../../tests/helpers/settings-u5-css'
import { pointerU43 } from '../../../tests/helpers/settings-u43-popups'

const startingUrl = window.location.href
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); window.history.replaceState(null, '', startingUrl) })

it('U6-2 — the real unknown-window link keeps its 44px area below Effort under the centered-em contract; both actions still work', async () => {
  const t = transportG1()
  pointerU43(true)
  t.on('GET', '/providers/default-model', () => jsonG1({ provider: 'openrouter', model: 'g1-model-a', reasoning_effort: 'low', window_unknown: true, context_window: 0 }))
  const { client } = sectionG1('providers')
  const link = await screen.findByTestId('default-model-window-unknown-link')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const effort = screen.getByTestId('default-model-effort')
  expect(screen.getByTestId('default-model-window')).toHaveTextContent(/^No context length$/)
  expect(link).toHaveAccessibleName('Set it in Limits')
  expect(link).toHaveAttribute('href', '/#/settings?tab=models&provider=openrouter&model=g1-model-a')
  expect(link.parentElement?.previousElementSibling?.contains(effort), 'instrument: actual Effort row immediately precedes this link paragraph').toBe(true)
  const targets = [link, link.parentElement!, effort]
  const css = await compiledCssU43(targets)
  const touch = inlineTouchU5(css, link)
  expect(touch.pass, 'U6-2: no removal/shrinking of the real link hit area to hide overlap').toBe(true)
  const clearance = centeredLinkClearanceU43(css, link, { width: 390, pointer: 'coarse' })
  console.info('R43_CLEARANCE ' + JSON.stringify(clearance))
  expect(clearance.centered, 'instrument: the actual link uses the modeled centered interactive pseudo area').toBe(true)
  expect(clearance.minimum).toBeGreaterThanOrEqual(44)
  expect(clearance.clearance, 'U6-2: centered-em expansion must not extend above the gap into the preceding Effort row').not.toBeNull()
  expect.soft(clearance.clearance, 'U6-2: independent no-overlap bound is clearance >=0, NOT an implementation-specific margin class').toBeGreaterThanOrEqual(0)
  const before = targets.map((target) => ({ classes: target.getAttribute('class'), inline: target.getAttribute('style'), fine: fineSignatureU43(css, target) }))
  await userEvent.click(effort)
  const popup = await screen.findByRole('dialog')
  expect(within(popup).getAllByRole('radio').length, 'Effort opens the actual choice popup').toBeGreaterThan(0)
  expect(window.location.href, 'Effort activation must not follow the adjacent link').toBe(startingUrl)
  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(targets.map((target) => ({ classes: target.getAttribute('class'), inline: target.getAttribute('style'), fine: fineSignatureU43(css, target) })), 'U6-2: opening/closing does not change the link/paragraph/button layout contract').toEqual(before)
  await userEvent.click(link)
  await waitFor(() => expect(window.location.hash).toBe('#/settings?tab=models&provider=openrouter&model=g1-model-a'))
  expect(t.writes()).toEqual([])
})

it('U6-4 — the real only-provider removal confirm says its explanatory warning sentence exactly once', async () => {
  const t = transportG1()
  pointerU43(true)
  const { client } = sectionG1('providers')
  await screen.findByTestId('configure-btn-openrouter')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  await userEvent.click(screen.getByTestId('configure-btn-openrouter'))
  await userEvent.click(await screen.findByTestId('remove-provider-btn-openrouter'))
  const dialog = await screen.findByTestId('remove-provider-dialog')
  const warning = within(dialog).getByTestId('remove-provider-only-copy')
  expect(warning).toBeVisible()
  expect(warning).toHaveTextContent(/only provider.*backs the default model/i)
  const normalize = (text: string) => text.replace(/\s+/g, ' ').trim()
  // Metamorphic sentence-count invariant, not an imported implementation string.
  // The required count (one) is specified by U6-4, regardless of exact wording.
  const sentence = normalize(warning.textContent ?? '')
  expect(sentence.length, 'positive control: never count an empty warning').toBeGreaterThan(0)
  const occurrences = normalize(dialog.textContent ?? '').split(sentence).length - 1
  console.info('R43_CONFIRM ' + JSON.stringify({ sentence, occurrences }))
  expect(occurrences, 'U6-4: sentence appears once, never in both description and warning').toBe(1)
  expect(within(dialog).getByTestId('remove-provider-confirm')).toBeDisabled()
  await userEvent.click(within(dialog).getByTestId('remove-provider-cancel'))
  expect(t.writes()).toEqual([])
})

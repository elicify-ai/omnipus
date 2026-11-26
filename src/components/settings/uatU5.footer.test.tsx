// T5-2 / FR-070 / SC-009: 390px mobile source-layout contract, BOTH pointer classes.
// jsdom has no real geometry: right edges and inner scrollWidth remain browser UAT.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { parse } from 'postcss'
import { Button } from '@/components/ui/button'
import { SheetFooter } from '@/components/ui/sheet'
import { sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { commonFooterU5, compiledCssU5, mobileFlowU5 } from '../../../tests/helpers/settings-u5-css'

const names = ['Test connection', 'Remove provider', 'Cancel', 'Save & Connect']
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('T5-2 — real connected-provider Edit footer has a 390px wrap/stack contract for fine AND coarse pointers', async () => {
  const t = transportG1()
  sectionG1('providers')
  await userEvent.click(await screen.findByTestId('configure-btn-openrouter'))
  const sheet = await screen.findByRole('dialog', { name: 'OpenRouter' })
  const actions = names.map((name) => within(sheet).getByRole('button', { name }))
  const footer = commonFooterU5(actions)
  expect(within(footer).getAllByRole('button').map((button) => button.textContent?.trim()),
    'inventory: every actual footer action is covered, including the primary Save').toEqual(names)
  expect(actions[1].parentElement, 'ADR-068 US-3: destructive and Save actions occupy separate groups').not.toBe(actions[3].parentElement)
  const css = await compiledCssU5([footer, ...footer.querySelectorAll('*')])
  for (const pointer of ['fine', 'coarse'] as const) {
    const flow = mobileFlowU5(css, footer, { width: 390, pointer })
    console.info(`T5-2 actual footer at 390px/${pointer}: ${JSON.stringify(flow)}`)
    expect.soft(flow.pass, `FR-070/T5-2: actual Edit footer must wrap or stack at 390px/${pointer}; a single nowrap row clips Save`).toBe(true)
  }
  expect(t.writes(), 'opening and inspecting the sheet never saves a provider').toEqual([])
})

it('T5-2 GREEN control — the real catalogued SheetFooter stacks all four actions at 390px for either pointer', async () => {
  render(<SheetFooter data-testid="u5-shared-footer">{names.map((name) => <Button key={name}>{name}</Button>)}</SheetFooter>)
  const footer = screen.getByTestId('u5-shared-footer')
  expect(within(footer).getAllByRole('button').map((button) => button.textContent)).toEqual(names)
  const css = await compiledCssU5([footer, ...footer.querySelectorAll('*')])
  for (const pointer of ['fine', 'coarse'] as const) {
    expect(mobileFlowU5(css, footer, { width: 390, pointer }).pass, `shared footer at 390px/${pointer}`).toBe(true)
  }
})

it('T5-2 instrument — nowrap, desktop-only and coarse-only rules cannot certify fine-pointer mobile flow', () => {
  render(<div data-testid="u5-flow" className="footer" />)
  const target = screen.getByTestId('u5-flow')
  const css = parse('.footer { display:flex; flex-wrap:nowrap } @media (width >= 640px) { .footer { flex-wrap:wrap } } @media (pointer:coarse) { .footer { flex-direction:column } }')
  expect(mobileFlowU5(css, target, { width: 390, pointer: 'fine' }).pass).toBe(false)
  expect(mobileFlowU5(css, target, { width: 390, pointer: 'coarse' }).pass).toBe(true)
  expect(mobileFlowU5(css, target, { width: 640, pointer: 'fine' }).pass).toBe(true)
})

it.each([389, 390, 391])('T5-2 instrument — a mobile maximum of 390px handles boundary %ipx exactly', (width) => {
  render(<div data-testid="u5-boundary" className="footer" />)
  const css = parse('.footer { display:flex } @media (width <= 390px) { .footer { flex-wrap:wrap } }')
  expect(mobileFlowU5(css, screen.getByTestId('u5-boundary'), { width, pointer: 'fine' }).pass).toBe(width <= 390)
})
